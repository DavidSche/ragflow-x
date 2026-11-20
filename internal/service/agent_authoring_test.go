package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// TestAgentAuthoringOptionsListsPickableResources covers the canvas inspector's
// data surface: a node parameter that references a system resource must come
// from a list, so every picker section has to be populated from the same source
// of truth the chat / search-app editors already use.
func TestAgentAuthoringOptionsListsPickableResources(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{
		ID: "link-1", TenantID: tenant.ID, RAGFlowDatasetID: "engine-ds-1",
		Name: "安全规程库", DocumentCount: 12, Sensitivity: "internal",
	}); err != nil {
		t.Fatalf("seed dataset link: %v", err)
	}
	if err := svc.Store.UpsertMemoryShadow(ctx, &model.MemoryShadow{
		ID: "mem-1", TenantID: tenant.ID, Name: "对话记忆",
	}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	scope := TenantScope{
		Kind:             TenantScopeCurrent,
		ActorTenantID:    tenant.ID,
		TargetTenantID:   tenant.ID,
		AllowedTenantIDs: []string{tenant.ID},
	}
	opts := svc.AgentAuthoringOptions(ctx, scope)

	// Models: the mock engine answers one model per writable type.
	if opts.ModelsError != "" {
		t.Fatalf("models section failed: %s", opts.ModelsError)
	}
	if opts.Models == nil || len(opts.Models.Chat) == 0 || len(opts.Models.Rerank) == 0 {
		t.Fatalf("models section must carry chat and rerank catalogs, got %+v", opts.Models)
	}

	// Datasets: linked rows are what the agent DSL stores (engine dataset id),
	// and the display name comes from the X-layer alias, never the raw id.
	if opts.DatasetsError != "" {
		t.Fatalf("datasets section failed: %s", opts.DatasetsError)
	}
	if len(opts.Datasets) != 1 {
		t.Fatalf("expected 1 dataset option, got %+v", opts.Datasets)
	}
	if opts.Datasets[0].RAGFlowDatasetID != "engine-ds-1" || opts.Datasets[0].Name != "安全规程库" {
		t.Fatalf("dataset option must pair alias name with engine id, got %+v", opts.Datasets[0])
	}

	// Memories: needed by retrieval_from=memory, which stores memory_ids.
	if opts.MemoriesError != "" {
		t.Fatalf("memories section failed: %s", opts.MemoriesError)
	}
	if len(opts.Memories) != 1 || opts.Memories[0].ID != "mem-1" || opts.Memories[0].Name != "对话记忆" {
		t.Fatalf("expected the seeded memory, got %+v", opts.Memories)
	}
}

// TestAgentAuthoringOptionsIsTenantScoped verifies another tenant's dataset link
// and memory never leak into the picker.
func TestAgentAuthoringOptionsIsTenantScoped(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	mine, err := svc.CreateTenant(ctx, "Mine")
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateTenant(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{
		ID: "link-other", TenantID: other.ID, RAGFlowDatasetID: "engine-ds-other", Name: "别人的库",
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertMemoryShadow(ctx, &model.MemoryShadow{
		ID: "mem-other", TenantID: other.ID, Name: "别人的记忆",
	}); err != nil {
		t.Fatal(err)
	}

	scope := TenantScope{
		Kind:             TenantScopeCurrent,
		ActorTenantID:    mine.ID,
		TargetTenantID:   mine.ID,
		AllowedTenantIDs: []string{mine.ID},
	}
	opts := svc.AgentAuthoringOptions(ctx, scope)
	if len(opts.Datasets) != 0 {
		t.Fatalf("cross-tenant dataset leaked into the picker: %+v", opts.Datasets)
	}
	if len(opts.Memories) != 0 {
		t.Fatalf("cross-tenant memory leaked into the picker: %+v", opts.Memories)
	}
}
