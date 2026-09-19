package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestResourceSyncUsesImmutableSnapshotAndBindingPayloadArtifacts(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Sync Artifact")
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "artifact-dataset"})
	if err != nil {
		t.Fatal(err)
	}
	enableResourceSyncForTest(t, svc, tenant.ID)
	run, err := svc.ScanResourceSync(ctx, "admin", []string{model.SyncItemTypeDataset})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(run.SourceSnapshotRef, "artifact:") || run.SourceSnapshotJSON != "" {
		t.Fatalf("run must reference an immutable snapshot artifact: ref=%s json=%s", run.SourceSnapshotRef, run.SourceSnapshotJSON)
	}
	artifactID := artifactIDFromRef(run.SourceSnapshotRef)
	snapshotArtifact, err := svc.GetResourceSyncArtifact(ctx, artifactID)
	if err != nil || snapshotArtifact == nil || snapshotArtifact.ArtifactType != "sync_snapshot" {
		t.Fatalf("snapshot artifact missing: %+v err=%v", snapshotArtifact, err)
	}
	run, err = svc.ImportResourceSync(ctx, run.ID, "admin", ResourceSyncImportRequest{})
	if err != nil || run.Status != model.SyncRunSucceeded {
		t.Fatalf("artifact-backed import failed: %+v err=%v", run, err)
	}
	binding, err := svc.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, model.SyncItemTypeDataset, ResourceSyncGlobalTenant, dataset.ID)
	if err != nil || binding == nil {
		t.Fatalf("binding missing: %+v err=%v", binding, err)
	}
	version, err := svc.Store.GetCurrentBindingVersion(ctx, binding.ID)
	if err != nil || version == nil {
		t.Fatalf("binding version missing: %+v err=%v", version, err)
	}
	if !strings.HasPrefix(version.UpstreamPayloadRef, "artifact:") || version.UpstreamPayloadJSON != "{}" {
		t.Fatalf("binding version must reference payload artifact: ref=%s json=%s", version.UpstreamPayloadRef, version.UpstreamPayloadJSON)
	}
	payloadArtifact, err := svc.GetResourceSyncArtifact(ctx, artifactIDFromRef(version.UpstreamPayloadRef))
	if err != nil || payloadArtifact == nil || payloadArtifact.ArtifactType != "binding_payload" {
		t.Fatalf("payload artifact missing: %+v err=%v", payloadArtifact, err)
	}
	if payloadArtifact.ContentHash != version.UpstreamHash {
		t.Fatalf("payload artifact hash mismatch: artifact=%s version=%s", payloadArtifact.ContentHash, version.UpstreamHash)
	}
}

func TestRunSnapshotFallsBackToLegacyJSON(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	key := resourceIdentityKey(model.SyncItemTypeDataset, ResourceSyncGlobalTenant, "external")
	legacy, _ := json.Marshal(map[string]json.RawMessage{key: json.RawMessage(`{"id":"external"}`)})
	run := &model.SyncRun{ID: "legacy-run", SourceSnapshotJSON: string(legacy)}
	snapshot, err := svc.getRunSnapshot(ctx, run)
	if err != nil || len(snapshot) != 1 {
		t.Fatalf("legacy snapshot fallback failed: %+v err=%v", snapshot, err)
	}
}

func TestResourceSyncArtifactLifecycleExtendsOnContentReuse(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	content := map[string]string{"resource": "artifact"}
	svc.SetApprovalConfig(config.Approval{RetentionDays: 1})
	first, err := svc.createResourceSyncArtifact(ctx, svc.Store, "item_diff", model.SyncItemTypeDataset, content, "run-1", "item-1", "")
	if err != nil || first.RetainUntil == nil {
		t.Fatalf("first artifact lifecycle missing: %+v err=%v", first, err)
	}
	svc.SetApprovalConfig(config.Approval{RetentionDays: 8})
	second, err := svc.createResourceSyncArtifact(ctx, svc.Store, "item_diff", model.SyncItemTypeDataset, content, "run-2", "item-2", "")
	if err != nil || second.ID != first.ID {
		t.Fatalf("content-addressed artifact must be reused: first=%s second=%s err=%v", first.ID, second.ID, err)
	}
	if second.RetainUntil == nil || !second.RetainUntil.After(*first.RetainUntil) {
		t.Fatalf("lifecycle must extend for later references: first=%v second=%v", first.RetainUntil, second.RetainUntil)
	}
	lifecycle, err := svc.Store.GetResourceSyncArtifactLifecycle(ctx, first.ID)
	if err != nil || lifecycle == nil || !lifecycle.RetainUntil.Equal(*second.RetainUntil) {
		t.Fatalf("stored lifecycle not extended: %+v err=%v", lifecycle, err)
	}
}

func TestConvergeResourceSyncArtifactLifecyclesAfterRetentionGrows(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	svc.SetApprovalConfig(config.Approval{RetentionDays: 1})
	artifact, err := svc.createResourceSyncArtifact(ctx, svc.Store, "item_diff", model.SyncItemTypeDataset, map[string]string{"converge": "yes"}, "run", "item", "")
	if err != nil || artifact.RetainUntil == nil {
		t.Fatalf("artifact lifecycle missing: %+v err=%v", artifact, err)
	}
	svc.SetApprovalConfig(config.Approval{RetentionDays: 8})
	if err := svc.convergeResourceSyncArtifactLifecycles(ctx); err != nil {
		t.Fatal(err)
	}
	lifecycle, err := svc.Store.GetResourceSyncArtifactLifecycle(ctx, artifact.ID)
	if err != nil || lifecycle == nil || lifecycle.RetainUntil == nil || !lifecycle.RetainUntil.After(*artifact.RetainUntil) {
		t.Fatalf("lifecycle not converged: %+v err=%v", lifecycle, err)
	}
}

func TestConvergeResourceSyncArtifactLifecyclesWhenRetentionDisabled(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	svc.SetApprovalConfig(config.Approval{RetentionDays: 1})
	artifact, err := svc.createResourceSyncArtifact(ctx, svc.Store, "item_diff", model.SyncItemTypeDataset, map[string]string{"disabled": "retention"}, "run", "item", "")
	if err != nil || artifact.RetainUntil == nil {
		t.Fatalf("artifact lifecycle missing: %+v err=%v", artifact, err)
	}
	svc.SetApprovalConfig(config.Approval{RetentionDays: 0})
	if err := svc.convergeResourceSyncArtifactLifecycles(ctx); err != nil {
		t.Fatal(err)
	}
	lifecycle, err := svc.Store.GetResourceSyncArtifactLifecycle(ctx, artifact.ID)
	if err != nil || lifecycle == nil || lifecycle.RetainUntil != nil {
		t.Fatalf("disabled retention must extend lifecycle to unbounded: %+v err=%v", lifecycle, err)
	}
}

func TestGetResourceSyncArtifactRejectsExpiredLifecycle(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	now := time.Now().UTC().Add(-time.Minute)
	svc.SetApprovalConfig(config.Approval{RetentionDays: 0})
	artifact, err := svc.createResourceSyncArtifact(ctx, svc.Store, "item_diff", model.SyncItemTypeDataset, map[string]string{"expired": "yes"}, "run", "item", "")
	if err != nil {
		t.Fatal(err)
	}
	expired := now
	lifecycle := &model.ResourceSyncArtifactLifecycle{
		ArtifactID: artifact.ID, RetainUntil: &expired,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.ExtendResourceSyncArtifactLifecycle(ctx, lifecycle); err != nil {
		t.Fatal(err)
	}
	if artifact, err := svc.GetResourceSyncArtifact(ctx, artifact.ID); err == nil || artifact != nil {
		t.Fatalf("expired artifact must not be readable: %+v err=%v", artifact, err)
	}
}

func TestGetResourceSyncArtifactFailsClosedWithoutLifecycle(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "lifecycle.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := repository.NewStore(gdb)
	svc := New(store, ragflow.NewMock(), jwt.NewManager("secret", 24), "key")
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	svc.SetApprovalConfig(config.Approval{RetentionDays: 0})
	artifact, err := svc.createResourceSyncArtifact(ctx, svc.Store, "item_diff", model.SyncItemTypeDataset, map[string]string{"missing": "lifecycle"}, "run", "item", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := gdb.Where("artifact_id = ?", artifact.ID).Delete(&model.ResourceSyncArtifactLifecycle{}).Error; err != nil {
		t.Fatal(err)
	}
	if artifact, err := svc.GetResourceSyncArtifact(ctx, artifact.ID); err == nil || artifact != nil {
		t.Fatalf("missing lifecycle must fail closed: %+v err=%v", artifact, err)
	}
}
