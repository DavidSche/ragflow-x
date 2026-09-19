package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-AUDIT-001
func TestP0_AUDIT_001_AuditAnchorCreatesImmutableTenantTails(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	first, err := svc.CreateTenant(ctx, "tenant-one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateTenant(ctx, "tenant-two")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAudit(ctx, &model.AuditLog{TenantID: first.ID, Action: "one", Resource: "test", ResourceID: "1"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAudit(ctx, &model.AuditLog{TenantID: second.ID, Action: "two", Resource: "test", ResourceID: "2"}); err != nil {
		t.Fatal(err)
	}

	svc.SetupWorker(DefaultWorkerConfig())
	svc.SetupAuditAnchor(config.AuditAnchor{Enabled: true, IntervalSec: 3600})
	queued, err := svc.ScheduleAuditAnchor(ctx, "")
	if err != nil || !queued {
		t.Fatalf("schedule first anchor job: queued=%v err=%v", queued, err)
	}
	queuedAgain, err := svc.ScheduleAuditAnchor(ctx, "")
	if err != nil || queuedAgain {
		t.Fatalf("schedule duplicate anchor job: queued=%v err=%v", queuedAgain, err)
	}

	summary, err := svc.RunAuditAnchoring(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Enabled != true || summary.Tenants != 3 || summary.Anchored != 2 || summary.Skipped != 1 {
		t.Fatalf("unexpected first summary: %+v", summary)
	}
	unchanged, err := svc.RunAuditAnchoring(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Anchored != 0 || unchanged.Unchanged != 2 || unchanged.Skipped != 1 {
		t.Fatalf("expected idempotent rerun, got %+v", unchanged)
	}

	if err := svc.RecordAudit(ctx, &model.AuditLog{TenantID: first.ID, Action: "three", Resource: "test", ResourceID: "3"}); err != nil {
		t.Fatal(err)
	}
	changed, err := svc.RunAuditAnchoring(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Anchored != 1 || changed.Unchanged != 1 {
		t.Fatalf("unexpected changed summary: %+v", changed)
	}

	firstAnchors, total, err := svc.ListAuditAnchors(ctx, first.ID, false, 1, 20)
	if err != nil || total != 2 {
		t.Fatalf("tenant scoped anchors: total=%d err=%v", total, err)
	}
	for _, anchor := range firstAnchors {
		if anchor.TenantID != first.ID {
			t.Fatalf("tenant isolation violated: %s", anchor.TenantID)
		}
	}
	allAnchors, total, err := svc.ListAuditAnchors(ctx, first.ID, true, 1, 20)
	if err != nil || total != 3 {
		t.Fatalf("platform scoped anchors: total=%d err=%v", total, err)
	}
	if len(allAnchors) != 3 {
		t.Fatalf("expected all anchors, got %d", len(allAnchors))
	}
}

func TestAuditAnchorWorkerSelfSchedules(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	tenant, err := svc.CreateTenant(ctx, "worker-tenant")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAudit(ctx, &model.AuditLog{TenantID: tenant.ID, Action: "worker", Resource: "test", ResourceID: "1"}); err != nil {
		t.Fatal(err)
	}
	svc.SetupWorker(DefaultWorkerConfig())
	svc.SetupAuditAnchor(config.AuditAnchor{Enabled: true, IntervalSec: 3600})
	if err := (&auditAnchorWorker{svc: svc}).Run(ctx, &model.Job{ID: "audit-anchor-test"}); err != nil {
		t.Fatal(err)
	}
	active, err := svc.Store.CountActiveJobsExcept(ctx, model.JobKindAuditAnchor, SystemTenantID, "")
	if err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("active anchor jobs = %d, want 1", active)
	}
}

// ScenarioID: SC-AUDIT-001
func TestP0_AUDIT_001_ExternalExportIsIdempotentAndTamperEvident(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	tenant, err := svc.CreateTenant(ctx, "export-tenant")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAudit(ctx, &model.AuditLog{TenantID: tenant.ID, Action: "export", Resource: "test", ResourceID: "1"}); err != nil {
		t.Fatal(err)
	}
	exportDir := t.TempDir()
	svc.SetAuditAnchorPolicy(config.AuditAnchor{Enabled: true, IntervalSec: 3600, ExportDir: exportDir})
	first, err := svc.RunAuditAnchoring(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Anchored != 1 || first.Exported != 1 || first.ExportSkipped != 0 {
		t.Fatalf("unexpected first export summary: %+v", first)
	}
	anchors, _, err := svc.ListAuditAnchors(ctx, tenant.ID, false, 1, 20)
	if err != nil || len(anchors) != 1 {
		t.Fatalf("anchors = %d err=%v", len(anchors), err)
	}
	path := filepath.Join(exportDir, anchors[0].ID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		SchemaVersion int       `json:"schema_version"`
		ID            string    `json:"id"`
		TenantID      string    `json:"tenant_id"`
		LastSeq       int64     `json:"last_seq"`
		LastHash      string    `json:"last_hash"`
		Algorithm     string    `json:"algorithm"`
		AnchorAt      time.Time `json:"anchor_at"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 1 || document.ID != anchors[0].ID || document.TenantID != tenant.ID ||
		document.LastSeq != anchors[0].LastSeq || document.LastHash != anchors[0].LastHash ||
		document.Algorithm != model.AuditAnchorAlgorithm || document.AnchorAt.IsZero() {
		t.Fatalf("unexpected exported document: %+v", document)
	}
	second, err := svc.RunAuditAnchoring(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Anchored != 0 || second.Unchanged != 1 || second.Exported != 0 || second.ExportSkipped != 1 {
		t.Fatalf("unexpected idempotent export summary: %+v", second)
	}
	if err := os.WriteFile(path, []byte(`{"tampered":true}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunAuditAnchoring(ctx); err == nil {
		t.Fatal("expected tampered external anchor to fail export")
	}
}

func TestAuditAnchorExportRequiresExistingDirectory(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)
	tenant, err := svc.CreateTenant(ctx, "missing-export-tenant")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordAudit(ctx, &model.AuditLog{TenantID: tenant.ID, Action: "missing-export", Resource: "test", ResourceID: "1"}); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "not-created")
	svc.SetAuditAnchorPolicy(config.AuditAnchor{Enabled: true, IntervalSec: 3600, ExportDir: missing})
	if _, err := svc.RunAuditAnchoring(ctx); err == nil {
		t.Fatal("expected missing export directory to fail")
	}
}

// ScenarioID: SC-AUDIT-001
func TestP0_AUDIT_001_AuditAnchorMigrationIsImmutable(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "anchor.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	anchor := model.AuditAnchor{
		ID: "0123456789abcdef0123456789abcdef", TenantID: "tenant",
		LastSeq: 1, LastHash: "hash", Algorithm: model.AuditAnchorAlgorithm,
	}
	if err := gdb.Create(&anchor).Error; err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.AuditAnchor{}).Where("id = ?", anchor.ID).Update("last_hash", "tampered").Error; err == nil {
		t.Fatal("expected update to be rejected")
	}
	if err := gdb.Delete(&model.AuditAnchor{}, "id = ?", anchor.ID).Error; err == nil {
		t.Fatal("expected delete to be rejected")
	}
	var count int64
	if err := gdb.Model(&model.AuditAnchor{}).Where("id = ?", anchor.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected immutable anchor to survive, count=%d", count)
	}
}
