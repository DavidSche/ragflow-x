package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// datasetNames returns the names of a dataset summary list for assertions.
func datasetNames(items []DatasetSummary) []string {
	out := make([]string, 0, len(items))
	for _, d := range items {
		out = append(out, d.Name)
	}
	return out
}

// TestDatasetListFilterNotPollutedByCache is a regression test for the P1
// cache bug: DatasetList used to memoize results under a tenant-only key while
// applying the name filter inside the loader, so two different filtered
// requests in the same tenant within the TTL returned the first request's
// rows. Filtered requests must now bypass the shared cache entirely.
func TestDatasetListFilterNotPollutedByCache(t *testing.T) {
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
	for _, d := range []model.DatasetLink{
		{ID: "d1", TenantID: ta.ID, RAGFlowDatasetID: "r1", Name: "alpha"},
		{ID: "d2", TenantID: ta.ID, RAGFlowDatasetID: "r2", Name: "beta"},
		{ID: "d3", TenantID: tb.ID, RAGFlowDatasetID: "r3", Name: "gamma"},
	} {
		if err := svc.Store.CreateDatasetLink(ctx, &d); err != nil {
			t.Fatalf("create dataset link %s: %v", d.ID, err)
		}
	}

	byName := func(filter repository.DatasetFilter) []string {
		items, err := svc.ListDatasets(ctx, ta.ID, filter)
		if err != nil {
			t.Fatalf("list datasets: %v", err)
		}
		return datasetNames(items)
	}

	// Populate the unfiltered cache first on purpose.
	all := byName(repository.DatasetFilter{})
	if len(all) != 2 {
		t.Fatalf("expected 2 tenant datasets, got %v", all)
	}

	// A filtered request must not be served the unfiltered cached rows.
	beta := byName(repository.DatasetFilter{Name: "beta"})
	if len(beta) != 1 || beta[0] != "beta" {
		t.Fatalf("filtered (beta) result polluted by cache: got %v", beta)
	}
	alpha := byName(repository.DatasetFilter{Name: "alpha"})
	if len(alpha) != 1 || alpha[0] != "alpha" {
		t.Fatalf("filtered (alpha) result polluted by cache: got %v", alpha)
	}

	// Unfiltered still returns the full tenant set.
	again := byName(repository.DatasetFilter{})
	if len(again) != 2 {
		t.Fatalf("unfiltered result changed after filtered calls: got %v", again)
	}

	// Cross-tenant list applies tenant_id without leaking.
	items, err := svc.ListAllDatasets(ctx, repository.DatasetFilter{TenantID: tb.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "gamma" {
		t.Fatalf("cross-tenant tenant_id filter: got %v", datasetNames(items))
	}
}
