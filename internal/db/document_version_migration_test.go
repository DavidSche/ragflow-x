package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func TestDocumentVersionMigrationConstraints(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "document-version.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tenantID, logicalID := id.New(), id.New()
	logical := model.LogicalDocument{ID: logicalID, TenantID: tenantID, DatasetID: id.New(), Name: "Policy", CreatedAt: now, UpdatedAt: now}
	if err := gdb.Create(&logical).Error; err != nil {
		t.Fatal(err)
	}
	base := model.DocumentVersion{
		ID: id.New(), TenantID: tenantID, LogicalDocumentID: logicalID,
		RAGFlowDocumentID: "ragflow-doc-1", Version: 1,
		Status: model.DocumentVersionActive, EffectiveFrom: now,
		ContentHash: "1111111111111111111111111111111111111111111111111111111111111111",
		CreatedAt:   now, UpdatedAt: now,
	}
	if err := gdb.Create(&base).Error; err != nil {
		t.Fatal(err)
	}

	duplicateVersion := base
	duplicateVersion.ID = id.New()
	if err := gdb.Create(&duplicateVersion).Error; err == nil {
		t.Fatal("expected version number uniqueness violation")
	}
	duplicateHash := base
	duplicateHash.ID = id.New()
	duplicateHash.Version = 2
	if err := gdb.Create(&duplicateHash).Error; err == nil {
		t.Fatal("expected content hash uniqueness violation")
	}
	duplicateActive := base
	duplicateActive.ID = id.New()
	duplicateActive.Version = 3
	duplicateActive.ContentHash = "2222222222222222222222222222222222222222222222222222222222222222"
	if err := gdb.Create(&duplicateActive).Error; err == nil {
		t.Fatal("expected active version uniqueness violation")
	}
	draft := duplicateActive
	draft.ID = id.New()
	draft.Version = 4
	draft.Status = model.DocumentVersionDraft
	if err := gdb.Create(&draft).Error; err != nil {
		t.Fatalf("draft should be permitted outside temporal exclusion set: %v", err)
	}
}

func TestVersionPublishAttemptMigrationContracts(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "version-publish.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	policy := model.VersionFilterPolicy{ID: id.New(), TenantID: id.New(), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if err := gdb.Create(&policy).Error; err != nil {
		t.Fatalf("create version filter policy: %v", err)
	}
	if policy.MaxPushdownIDs != 200 || policy.MaxRequestBytes != 32768 {
		t.Fatalf("unexpected version filter policy defaults: %+v", policy)
	}

	now := time.Now().UTC()
	attempt := model.VersionPublishAttempt{
		ID: id.New(), TenantID: id.New(), LogicalDocumentID: id.New(), NewVersionID: id.New(),
		State: model.VersionPublishPreparing, CreatedAt: now, UpdatedAt: now,
	}
	if err := gdb.Create(&attempt).Error; err != nil {
		t.Fatalf("create publish attempt: %v", err)
	}
	for _, column := range []string{"source_status", "claimed_at", "claim_expires_at"} {
		if !gdb.Migrator().HasColumn(&model.VersionPublishAttempt{}, column) {
			t.Fatalf("version publish reconciler column %q is missing", column)
		}
	}
	conflict := attempt
	conflict.ID = id.New()
	conflict.NewVersionID = id.New()
	conflict.State = model.VersionPublishRAGFlowUpdate
	if err := gdb.Create(&conflict).Error; err == nil {
		t.Fatal("expected open publish attempt uniqueness violation")
	}

	attempt.State = model.VersionPublishCommitted
	attempt.UpdatedAt = now
	if err := gdb.Save(&attempt).Error; err != nil {
		t.Fatalf("commit publish attempt: %v", err)
	}
	terminal := conflict
	terminal.ID = id.New()
	terminal.State = model.VersionPublishCompensated
	if err := gdb.Create(&terminal).Error; err != nil {
		t.Fatalf("terminal publish attempts must coexist: %v", err)
	}
	manualReview := terminal
	manualReview.ID = id.New()
	manualReview.State = model.VersionPublishManualReview
	if err := gdb.Create(&manualReview).Error; err != nil {
		t.Fatalf("manual review attempts must not violate the open-attempt partial unique index: %v", err)
	}
}

func TestOutboxEventMigrationContracts(t *testing.T) {
	gdb, err := Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "outbox-migration.db")})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	if !gdb.Migrator().HasIndex(&model.OutboxEvent{}, "idx_rgx_outbox_event_dispatch") {
		t.Fatal("outbox dispatch index is missing")
	}
}
