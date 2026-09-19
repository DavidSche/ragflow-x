package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// TestSearchAppTenantIsolation verifies Search Apps are owned by a single
// platform tenant and that cross-tenant reads/writes are rejected unless the
// caller is a platform admin (scopeAll).
func TestSearchAppTenantIsolation(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := svc.CreateTenant(ctx, "TenantB")
	if err != nil {
		t.Fatal(err)
	}

	sa, err := svc.CreateSearchApp(ctx, ta.ID, "kb-search", &ragflow.SearchConfig{KbIDs: []string{"d1"}})
	if err != nil {
		t.Fatalf("create search app: %v", err)
	}
	if sa.ID == "" || sa.TenantID != ta.ID {
		t.Fatalf("unexpected search app shadow: %+v", sa)
	}

	// Tenant B must not read/update/delete/list Tenant A's Search App.
	if _, err := svc.GetSearchApp(ctx, tb.ID, sa.ID, false); err == nil {
		t.Fatal("tenant B must not read tenant A's search app")
	}
	if _, err := svc.UpdateSearchApp(ctx, tb.ID, sa.ID, "x", nil, false); err == nil {
		t.Fatal("tenant B must not update tenant A's search app")
	}
	if err := svc.DeleteSearchApp(ctx, tb.ID, sa.ID, false); err == nil {
		t.Fatal("tenant B must not delete tenant A's search app")
	}
	items, total, err := svc.ListSearchApps(ctx, tb.ID, false, repository.SearchAppFilter{}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("tenant B leaked tenant A search apps: total=%d", total)
	}

	// Owner sees its own Search App; platform admin (scopeAll) sees it too.
	if got, err := svc.GetSearchApp(ctx, ta.ID, sa.ID, false); err != nil || got.ID != sa.ID {
		t.Fatalf("owner must read own search app: err=%v got=%+v", err, got)
	}
	_, total, err = svc.ListSearchApps(ctx, "any", true, repository.SearchAppFilter{}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total < 1 {
		t.Fatalf("scopeAll should include the search app, total=%d", total)
	}
	if _, err := svc.GetSearchApp(ctx, tb.ID, sa.ID, true); err != nil {
		t.Fatalf("scopeAll read should succeed: %v", err)
	}
}

func TestSearchAppCreateMapsDatasetIDsForLiveConfig(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{
		ID: "link-1", TenantID: tenant.ID, RAGFlowDatasetID: "rf-1", Name: "KB",
	}); err != nil {
		t.Fatal(err)
	}

	app, err := svc.CreateSearchApp(ctx, tenant.ID, "mapped", &ragflow.SearchConfig{
		KbIDs: []string{"link-1"}, TopK: 8, SimilarityThreshold: 0.2, Summary: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if app.DatasetIDs != "rf-1" {
		t.Fatalf("shadow should store engine dataset id, got %q", app.DatasetIDs)
	}
	live, err := svc.RAGFlow.GetSearchApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if live.SearchConfig == nil || len(live.SearchConfig.KbIDs) != 1 || live.SearchConfig.KbIDs[0] != "rf-1" {
		t.Fatalf("live config should use engine dataset id: %+v", live.SearchConfig)
	}
}

// TestSearchAppCompletionTenantIsolation verifies the retrieval-debugging path
// is tenant-isolated and that a real completion is metered for the owner.
func TestSearchAppCompletionTenantIsolation(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := svc.CreateTenant(ctx, "TenantB")
	if err != nil {
		t.Fatal(err)
	}
	sa, err := svc.CreateSearchApp(ctx, ta.ID, "kb-search", &ragflow.SearchConfig{KbIDs: []string{"d1"}})
	if err != nil {
		t.Fatal(err)
	}

	// Cross-tenant completion is rejected.
	if _, err := svc.SearchAppCompletion(ctx, tb.ID, sa.ID, "hello", "req-x", false); err == nil {
		t.Fatal("tenant B must not run completion on tenant A's search app")
	}

	resp, err := svc.SearchAppCompletion(ctx, ta.ID, sa.ID, "hello", "req-1", false)
	if err != nil {
		t.Fatalf("owner completion should succeed: %v", err)
	}
	if resp == nil || resp.Answer == "" {
		t.Fatalf("unexpected completion: %+v", resp)
	}

	// The real consumption is metered into the daily aggregate for the owner.
	rows, err := svc.Store.SummarizeUsage(ctx, ta.ID, time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		t.Fatalf("summarize usage: %v", err)
	}
	if len(rows) == 0 || rows[0].Requests < 1 {
		t.Fatalf("expected metered search usage: %+v", rows)
	}
}
