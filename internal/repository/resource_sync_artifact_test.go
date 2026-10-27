package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func TestResourceSyncArtifactIsImmutable(t *testing.T) {
	store, gdb := newResourceSyncStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	artifact := &model.ResourceSyncArtifact{
		ID: id.New(), ArtifactType: "sync_snapshot", SourceID: "test",
		SourceCredentialVersion: "v1", ContentHash: "hash", ContentJSON: "{}",
		CreatedAt: now, UpdatedAt: now,
	}
	lifecycle := &model.ResourceSyncArtifactLifecycle{
		ArtifactID: artifact.ID, RetainUntil: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateResourceSyncArtifact(ctx, artifact, lifecycle); err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.ResourceSyncArtifact{}).Where("id = ?", artifact.ID).
		Update("content_json", `{"changed":true}`).Error; err == nil {
		t.Fatal("resource sync artifact update must be rejected")
	}
	if err := gdb.Where("id = ?", artifact.ID).Delete(&model.ResourceSyncArtifact{}).Error; err == nil {
		t.Fatal("resource sync artifact delete must be rejected")
	}
	stored, err := store.GetResourceSyncArtifact(ctx, artifact.ID)
	if err != nil || stored == nil || stored.ContentJSON != "{}" {
		t.Fatalf("artifact was mutated: %+v err=%v", stored, err)
	}
	storedLifecycle, err := store.GetResourceSyncArtifactLifecycle(ctx, artifact.ID)
	if err != nil || storedLifecycle == nil || storedLifecycle.RetainUntil == nil || !storedLifecycle.RetainUntil.Equal(now) {
		t.Fatalf("artifact lifecycle missing or changed: %+v err=%v", storedLifecycle, err)
	}
}
