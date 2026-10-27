package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
)

func newResourceSyncStore(t *testing.T) (Store, *gorm.DB) {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "sync.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return NewStore(gdb), gdb
}

func TestBindingVersionIsImmutableAndPointerAtomic(t *testing.T) {
	store, gdb := newResourceSyncStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	binding := &model.ResourceBinding{
		ID: id.New(), SourceID: "test", ResourceType: "dataset", ExternalScopeKey: "__GLOBAL__",
		ExternalID: "d1", ExternalTenantID: "__GLOBAL__", LocalType: "dataset", LocalID: "d1",
		TenantID: "tenant", CreatedAt: now, UpdatedAt: now,
	}
	if err := store.UpdateResourceBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	version := &model.ResourceBindingVersion{
		ID: id.New(), BindingID: binding.ID, Version: 1, UpstreamHash: "hash",
		UpstreamPayloadJSON: "{}", SourceCredentialVersion: "v1",
		SyncRunID: id.New(), SyncItemID: id.New(), CreatedBy: "test",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateBindingVersionWithPointer(ctx, version, "upstream", "local"); err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.ResourceBindingVersion{}).Where("id = ?", version.ID).
		Update("upstream_hash", "changed").Error; err == nil {
		t.Fatal("binding version update must be rejected")
	}
	if err := gdb.Where("id = ?", version.ID).Delete(&model.ResourceBindingVersion{}).Error; err == nil {
		t.Fatal("binding version delete must be rejected")
	}
	updated, err := store.GetCurrentBindingVersion(ctx, binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated == nil || updated.UpstreamHash != "hash" {
		t.Fatalf("version was mutated: %+v", updated)
	}
	current, err := store.GetResourceBindingByIdentity(ctx, binding.SourceID, binding.ResourceType, binding.ExternalScopeKey, binding.ExternalID)
	if err != nil || current == nil {
		t.Fatal(err)
	}
	if current.CurrentBindingVersionID == nil || *current.CurrentBindingVersionID != version.ID || current.LastSyncedUpstreamHash != "upstream" || current.LastSyncedLocalHash != "local" {
		t.Fatalf("pointer/baseline was not updated atomically: %+v", current)
	}
}
