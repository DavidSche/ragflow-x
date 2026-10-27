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

func TestQueryTemplatePersistenceAndTenantIsolation(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "query-template-repo.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := NewStore(gdb)
	t.Cleanup(func() { _ = store.Close() })
	if !gdb.Migrator().HasTable(&model.QueryTemplate{}) {
		t.Fatal("query template table is missing")
	}
	tenant := id.New()
	now := time.Now().UTC()
	template := &model.QueryTemplate{
		ID: id.New(), TenantID: tenant, Name: "Risk exposure", SQLTemplate: "SELECT 1",
		ParameterSchema: "{}", ResultSchema: "{}", ExecutionPolicy: "{}", AuthorizationScope: "{}",
		ConnectionID: id.New(), Active: true, CreatedBy: tenant, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateQueryTemplate(ctx, template); err != nil {
		t.Fatalf("create query template: %v", err)
	}
	duplicate := *template
	duplicate.ID = id.New()
	if err := store.CreateQueryTemplate(ctx, &duplicate); err == nil {
		t.Fatal("duplicate tenant/name must violate unique index")
	}
	active := true
	items, total, err := store.ListQueryTemplates(ctx, id.New(), QueryTemplateFilter{Active: &active}, 1, 20)
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("cross-tenant query templates = %+v total=%d err=%v", items, total, err)
	}
	found, err := store.GetQueryTemplateByName(ctx, tenant, "Risk exposure", "")
	if err != nil || found == nil || found.ID != template.ID {
		t.Fatalf("query template by name = %+v err=%v", found, err)
	}
	template.Active = false
	if err := store.UpdateQueryTemplate(ctx, template); err != nil {
		t.Fatalf("update query template: %v", err)
	}
	if err := store.DeleteQueryTemplate(ctx, tenant, template.ID); err != nil {
		t.Fatalf("delete query template: %v", err)
	}
	if deleted, err := store.GetQueryTemplate(ctx, tenant, template.ID); err != nil || deleted != nil {
		t.Fatalf("deleted query template = %+v err=%v", deleted, err)
	}
}
