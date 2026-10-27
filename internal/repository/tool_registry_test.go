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
)

func TestToolRegistryPersistenceAndActiveUniqueness(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "tool-registry-repo.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := NewStore(gdb)
	t.Cleanup(func() { _ = store.Close() })
	if !gdb.Migrator().HasTable(&model.ToolRegistry{}) {
		t.Fatal("tool registry table is missing")
	}
	if !gdb.Migrator().HasIndex(&model.ToolRegistry{}, "uq_rgx_tool_registry_active") {
		t.Fatal("tool registry active index is missing")
	}
	tenant := id.New()
	now := time.Now().UTC()
	first := &model.ToolRegistry{
		ID: id.New(), TenantID: tenant, ToolID: "add", Version: "v1", ToolType: model.ToolTypeCalculation,
		Name: "Add", InputSchema: "{}", OutputSchema: "{}", AuthorizationScope: "{}",
		ImplementationRef: "builtin:add", Active: true, CreatedBy: tenant, CreatedAt: now, UpdatedAt: now,
	}
	second := &model.ToolRegistry{
		ID: id.New(), TenantID: tenant, ToolID: "add", Version: "v2", ToolType: model.ToolTypeCalculation,
		Name: "Add", InputSchema: "{}", OutputSchema: "{}", AuthorizationScope: "{}",
		ImplementationRef: "builtin:add", Active: false, CreatedBy: tenant, CreatedAt: now, UpdatedAt: now,
	}
	for _, tool := range []*model.ToolRegistry{first, second} {
		if err := store.CreateToolRegistry(ctx, tool); err != nil {
			t.Fatalf("create tool registry: %v", err)
		}
	}
	count, err := store.CountActiveToolRegistries(ctx, tenant, "add", "")
	if err != nil || count != 1 {
		t.Fatalf("active count = %d err=%v", count, err)
	}
	if other, err := store.CountActiveToolRegistries(ctx, id.New(), "add", ""); err != nil || other != 0 {
		t.Fatalf("cross-tenant active count = %d err=%v", other, err)
	}
	first.Active = false
	if err := store.UpdateToolRegistry(ctx, first); err != nil {
		t.Fatalf("update tool registry: %v", err)
	}
	got, err := store.GetToolRegistry(ctx, tenant, first.ID)
	if err != nil || got.Active {
		t.Fatalf("updated tool = %+v err=%v", got, err)
	}
	active := true
	list, total, err := store.ListToolRegistries(ctx, tenant, ToolRegistryFilter{Active: &active}, 1, 20)
	if err != nil || total != 0 || len(list) != 0 {
		t.Fatalf("active list = %+v total=%d err=%v", list, total, err)
	}
	if err := store.DeleteToolRegistry(ctx, tenant, second.ID); err != nil {
		t.Fatalf("delete tool registry: %v", err)
	}
	if deleted, err := store.GetToolRegistry(ctx, tenant, second.ID); err != nil || deleted != nil {
		t.Fatalf("deleted tool = %+v err=%v", deleted, err)
	}
}
