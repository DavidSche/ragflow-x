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

func TestSourceRoutingRulePersistenceAndLookup(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "source-routing-rule-repo.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := NewStore(gdb)
	t.Cleanup(func() { _ = store.Close() })
	if !gdb.Migrator().HasTable(&model.SourceRoutingRule{}) {
		t.Fatal("source routing rule table is missing")
	}
	if !gdb.Migrator().HasIndex(&model.SourceRoutingRule{}, "idx_rgx_source_routing_rule_lookup") {
		t.Fatal("source routing rule lookup index is missing")
	}

	tenant := id.New()
	now := time.Now().UTC()
	first := &model.SourceRoutingRule{
		ID: id.New(), TenantID: tenant, AssistantID: "assistant-1", SourceType: model.SourceTypeTool,
		Matcher: `{"intent":["exposure_query"]}`, ToolID: "add", ToolVersion: "v1",
		PolicyVersion: "policy-v1", Priority: 2, ConfidenceThreshold: 0.8,
		Active: true, CreatedBy: tenant, CreatedAt: now, UpdatedAt: now,
	}
	second := &model.SourceRoutingRule{
		ID: id.New(), TenantID: tenant, SourceType: model.SourceTypeTool,
		Matcher: `{"intent":["exposure_query"]}`, ToolID: "subtract", ToolVersion: "v1",
		PolicyVersion: "policy-v1", Priority: 1, ConfidenceThreshold: 0.8,
		Active: true, CreatedBy: tenant, CreatedAt: now.Add(time.Second), UpdatedAt: now,
	}
	inactive := &model.SourceRoutingRule{
		ID: id.New(), TenantID: tenant, SourceType: model.SourceTypeTool,
		Matcher: `{"intent":["exposure_query"]}`, ToolID: "sum", ToolVersion: "v1",
		PolicyVersion: "policy-v1", Priority: 0, ConfidenceThreshold: 0.8,
		Active: false, CreatedBy: tenant, CreatedAt: now, UpdatedAt: now,
	}
	for _, rule := range []*model.SourceRoutingRule{first, second, inactive} {
		if err := store.CreateSourceRoutingRule(ctx, rule); err != nil {
			t.Fatalf("create source routing rule: %v", err)
		}
	}

	rules, err := store.ListActiveSourceRoutingRules(ctx, tenant, "assistant-1")
	if err != nil {
		t.Fatalf("list active rules: %v", err)
	}
	if len(rules) != 2 || rules[0].ID != second.ID || rules[1].ID != first.ID {
		t.Fatalf("unexpected active rules: %+v", rules)
	}
	if global, err := store.ListActiveSourceRoutingRules(ctx, tenant, "assistant-2"); err != nil || len(global) != 1 || global[0].ID != second.ID {
		t.Fatalf("global rules = %+v err=%v", global, err)
	}
	if other, err := store.ListActiveSourceRoutingRules(ctx, id.New(), "assistant-1"); err != nil || len(other) != 0 {
		t.Fatalf("cross-tenant rules = %+v err=%v", other, err)
	}

	active := true
	list, total, err := store.ListSourceRoutingRules(ctx, tenant, SourceRoutingRuleFilter{Active: &active}, 1, 20)
	if err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("active list = %+v total=%d err=%v", list, total, err)
	}
	second.Matcher = `{"intent":"exposure_query"}`
	second.UpdatedAt = now.Add(2 * time.Second)
	if err := store.UpdateSourceRoutingRule(ctx, second); err != nil {
		t.Fatalf("update source routing rule: %v", err)
	}
	got, err := store.GetSourceRoutingRule(ctx, tenant, second.ID)
	if err != nil || got.Matcher != second.Matcher {
		t.Fatalf("updated rule = %+v err=%v", got, err)
	}
	if crossTenant, err := store.GetSourceRoutingRule(ctx, id.New(), second.ID); err != nil || crossTenant != nil {
		t.Fatalf("cross-tenant get = %+v err=%v", crossTenant, err)
	}
	if err := store.DeleteSourceRoutingRule(ctx, tenant, inactive.ID); err != nil {
		t.Fatalf("delete source routing rule: %v", err)
	}
	if deleted, err := store.GetSourceRoutingRule(ctx, tenant, inactive.ID); err != nil || deleted != nil {
		t.Fatalf("deleted rule = %+v err=%v", deleted, err)
	}
}
