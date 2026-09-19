package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func TestScenarioTemplatePostgreSQLCreateUsesReservedWordColumn(t *testing.T) {
	store := newPostgresContractStore(t)
	ctx := context.Background()
	tenant := mustCreatePostgresTenant(t, store, "PG Scenario Template Tenant")
	now := time.Now().UTC()
	payload := `{"evaluationQuestions":["question"]}`
	asset := &model.ScenarioTemplateAsset{
		ID: id.New(), TenantID: tenant.ID, Key: id.New(), Name: "PG Reserved Key",
		AppTypes: "chat", LatestVersion: 1, Source: model.TemplateSourceCustom,
		Status: model.AssetStatusDraft, PayloadJSON: payload,
		CreatedBy: id.New(), CreatedAt: now, UpdatedAt: now,
	}
	version := &model.ScenarioTemplateVersion{
		ID: id.New(), TenantID: tenant.ID, TemplateID: asset.ID, Version: 1,
		PayloadJSON: payload, ChangeNote: "create", CreatedBy: asset.CreatedBy, CreatedAt: now,
	}
	if err := store.CreateScenarioTemplate(ctx, asset, version); err != nil {
		t.Fatalf("create scenario template: %v", err)
	}
	found, err := store.GetScenarioTemplateByKey(ctx, tenant.ID, asset.Key)
	if err != nil {
		t.Fatalf("get scenario template by key: %v", err)
	}
	if found == nil || found.ID != asset.ID {
		t.Fatalf("unexpected scenario template: %+v", found)
	}
	items, total, err := store.ListScenarioTemplates(ctx, tenant.ID, 1, 10, GovernanceFilter{Key: asset.Key})
	if err != nil {
		t.Fatalf("list scenario templates by key: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].ID != asset.ID {
		t.Fatalf("unexpected scenario template list: total=%d items=%+v", total, items)
	}
}
