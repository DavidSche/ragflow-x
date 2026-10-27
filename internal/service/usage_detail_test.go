package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// TestListUsageDetailScopeAndPagination verifies the service-level read path
// enforces tenant scope unless scopeAll is set, and clamps pagination.
func TestListUsageDetailScopeAndPagination(t *testing.T) {
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
	for i, td := range []struct {
		tenant string
		model  string
	}{
		{ta.ID, "qwen-max"},
		{ta.ID, "gpt-4o"},
		{tb.ID, "qwen-max"},
	} {
		if _, err := svc.Store.RecordCostMetric(ctx, &model.CostMetric{
			RequestID: "srv-req-" + string(rune('a'+i)), TenantID: td.tenant, UserID: "u1",
			Date: "2026-08-24", Model: td.model, Scenario: "chat", TokensIn: 1, TokensOut: 1,
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	// Default: scoped to the caller's tenant.
	items, total, err := svc.ListUsageDetail(ctx, ta.ID, false, 1, 1, repository.CostMetricFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 1 {
		t.Fatalf("tenant + page_size=1: got total=%d len=%d", total, len(items))
	}

	// scopeAll returns every tenant's rows.
	_, total, err = svc.ListUsageDetail(ctx, ta.ID, true, 1, 20, repository.CostMetricFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("scopeAll: got total=%d", total)
	}

	// A missing/too-large page size is clamped by the service.
	items, total, err = svc.ListUsageDetail(ctx, ta.ID, false, 0, 9999, repository.CostMetricFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("clamped pagination: got total=%d len=%d", total, len(items))
	}
}
