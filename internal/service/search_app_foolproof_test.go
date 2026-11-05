package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// TestResolveRAGFlowDatasetIDsByIDNamePassthrough verifies the three accepted
// dataset references all map to the engine dataset id: a rgx_dataset_link id,
// a case-insensitive Link name, and a raw RAGFlow dataset id (passthrough).
func TestResolveRAGFlowDatasetIDsByIDNamePassthrough(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{
		ID: "link-1", TenantID: tenant.ID, RAGFlowDatasetID: "rf-1", Name: "Safety-Ledger",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := svc.resolveRAGFlowDatasetIDs(ctx, tenant.ID, []string{
		"link-1",        // X 层 link id
		"safety-ledger", // case-insensitive name lookup (GetDatasetLinkByName lowercases)
		"raw-engine-9",  // raw engine id passthrough
		"",              // skipped empty
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"rf-1", "rf-1", "raw-engine-9"}
	if len(got) != len(want) {
		t.Fatalf("resolve count mismatch: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resolve[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}
}

// TestResolveDatasetRefBrokenLink40095 verifies a rgx_dataset_link row that
// exists but carries no engine dataset id surfaces a 40095 instead of silently
// passing a broken id through to the engine.
func TestResolveDatasetRefBrokenLink40095(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{
		ID: "link-broken", TenantID: tenant.ID, Name: "未同步库",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = svc.resolveRAGFlowDatasetIDs(ctx, tenant.ID, []string{"link-broken"})
	if err == nil {
		t.Fatal("expected an error for a broken link row")
	}
	var he *httperr.Error
	if !errors.As(err, &he) || he.Code != 40095 {
		t.Fatalf("expected 40095, got %v", err)
	}
}

// TestClosestDatasetNamesRanking verifies suggestions rank exact > prefix >
// substring > close edit distance, drop far-away names, and stay
// case-insensitive.
func TestClosestDatasetNamesRanking(t *testing.T) {
	owned := []ragflow.Dataset{
		{ID: "d1", Name: "Safety-Ledger"},
		{ID: "d2", Name: "Safety-Docs"},
		{ID: "d3", Name: "Ledger-Archive"},
		{ID: "d4", Name: "Water-Journal"},
	}
	got := closestDatasetNames(owned, "safety-ledger", 3)
	if len(got) != 1 || got[0] != "Safety-Ledger" {
		t.Fatalf("exact match first and alone, got %v", got)
	}
	got = closestDatasetNames(owned, "ledger", 3)
	want := []string{"Ledger-Archive", "Safety-Ledger"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("prefix before substring, got %v want %v", got, want)
	}
	got = closestDatasetNames(owned, "saftey-ledger", 3) // typo (t/y swap)
	if len(got) != 1 || got[0] != "Safety-Ledger" {
		t.Fatalf("near typo should recover the exact name, got %v", got)
	}
	if got = closestDatasetNames(owned, "zzz-nonsense", 3); len(got) != 0 {
		t.Fatalf("far-away ref must not flood suggestions, got %v", got)
	}
	// Chinese names sort the same way: exact first, close typographic dup second.
	zh := []ragflow.Dataset{
		{ID: "d1", Name: "安全文件甲"},
		{ID: "d2", Name: "安监月刊"},
		{ID: "d3", Name: "水文台账"},
		{ID: "d4", Name: "安全台账"},
	}
	if got = closestDatasetNames(zh, "安全台账", 3); len(got) < 1 || got[0] != "安全台账" {
		t.Fatalf("chinese exact should lead, got %v", got)
	}
}

// TestSearchAppDatasetOptionsLinkedOnly verifies the picker returns linked
// rgx_dataset_link rows under a plain tenant scope and never crosses tenants.
func TestSearchAppDatasetOptionsLinkedOnly(t *testing.T) {
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
	op, err := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "sa-op-a", Password: "secret123", Role: "tenant_admin"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{
		ID: "link-a1", TenantID: ta.ID, RAGFlowDatasetID: "rf-a1", Name: "安全生产台账", DocumentCount: 12, Sensitivity: "sensitive",
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{
		ID: "link-b1", TenantID: tb.ID, RAGFlowDatasetID: "rf-b1", Name: "B库", DocumentCount: 3,
	}); err != nil {
		t.Fatal(err)
	}

	scope, err := svc.ResolveTenantScopeFor(ctx, op.ID, ta.ID, "", "", "search-app")
	if err != nil {
		t.Fatal(err)
	}
	opts, err := svc.SearchAppDatasetOptions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts) != 1 || opts[0].LinkID != "link-a1" || opts[0].Source != "linked" {
		t.Fatalf("tenant scope should expose only its own linked dataset, got %+v", opts)
	}
	if opts[0].DocumentCount != 12 || opts[0].Sensitivity != "sensitive" {
		t.Fatalf("picker should carry linked metadata, got %+v", opts[0])
	}
}

// TestSearchAppDatasetOptionsEngineFallbackWithDedup verifies raw engine
// datasets complete the picker when they are not yet linked, and that a linked
// engine dataset is not duplicated.
func TestSearchAppDatasetOptionsEngineFallbackWithDedup(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	// Swap in a provider that reports the live HTTP name so the engine
	// fallback path (gated by s.RAGFlow.Name()=="http") is exercised.
	svc.RAGFlow = httpNamedMock{svc.RAGFlow, "http"}
	tenant, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	op, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{Username: "sa-op-a", Password: "secret123", Role: "tenant_admin"})
	if err != nil {
		t.Fatal(err)
	}
	linkedEngine, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "已链接台账"})
	if err != nil {
		t.Fatal(err)
	}
	rawEngine, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "新入库未链接库"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{
		ID: "link-x", TenantID: tenant.ID, RAGFlowDatasetID: linkedEngine.ID, Name: "已链接台账", DocumentCount: 7,
	}); err != nil {
		t.Fatal(err)
	}

	scope, err := svc.ResolveTenantScopeFor(ctx, op.ID, tenant.ID, "", "", "search-app")
	if err != nil {
		t.Fatal(err)
	}
	opts, err := svc.SearchAppDatasetOptions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	for _, o := range opts {
		byID[o.RAGFlowDatasetID] = o.Source
	}
	if byID[linkedEngine.ID] != "linked" {
		t.Fatalf("linked engine dataset must not duplicate as engine row: %+v", byID)
	}
	if byID[rawEngine.ID] != "engine" {
		t.Fatalf("raw engine dataset should appear as engine fallback: %+v", byID)
	}
	if len(opts) != 2 {
		t.Fatalf("expected exactly 2 picker rows (1 linked + 1 engine), got %+v", opts)
	}
}

type httpNamedMock struct {
	ragflow.Client
	name string
}

func (m httpNamedMock) Name() string { return m.name }

// TestSearchAppDatasetOptionsEngineFallbackExcludesOtherTenantLinks guards the
// cross-tenant boundary of the engine fallback (report §13.2 P1 guard rails):
// with a single shared engine account, ListDatasets sees every tenant's
// libraries, so a library any OTHER tenant has already linked must never be
// re-surfaced to this scope as a bindable engine option.
func TestSearchAppDatasetOptionsEngineFallbackExcludesOtherTenantLinks(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	// Swap in a provider that reports the live HTTP name so the engine
	// fallback path (gated by s.RAGFlow.Name()=="http") is exercised.
	svc.RAGFlow = httpNamedMock{svc.RAGFlow, "http"}
	tenantA, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	tenantB, err := svc.CreateTenant(ctx, "TenantB")
	if err != nil {
		t.Fatal(err)
	}
	opA, err := svc.CreateUser(ctx, tenantA.ID, "", CreateUserRequest{Username: "sa-op-a2", Password: "secret123", Role: "tenant_admin"})
	if err != nil {
		t.Fatal(err)
	}
	bEngine, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "B已链接台账"})
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "无人认领孤儿库"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{
		ID: "link-b2", TenantID: tenantB.ID, RAGFlowDatasetID: bEngine.ID, Name: "B已链接台账", DocumentCount: 5,
	}); err != nil {
		t.Fatal(err)
	}

	scope, err := svc.ResolveTenantScopeFor(ctx, opA.ID, tenantA.ID, "", "", "search-app")
	if err != nil {
		t.Fatal(err)
	}
	opts, err := svc.SearchAppDatasetOptions(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]string{}
	for _, o := range opts {
		byID[o.RAGFlowDatasetID] = o.Source
	}
	if source, cross := byID[bEngine.ID]; cross {
		t.Fatalf("other tenant's linked engine dataset leaked into this scope (source=%s): %+v", source, byID)
	}
	if byID[orphan.ID] != "engine" {
		t.Fatalf("unclaimed orphan engine dataset should appear as an explicit engine option, got %+v", byID)
	}
	if len(opts) != 1 {
		t.Fatalf("expected exactly 1 picker row (the orphan engine-only option), got %+v", opts)
	}
}

// TestValidateOwnedDatasetsCrossTenantBoundary closes the binding execution
// face of the cross-tenant guard (doc/252 §隔离): with a single shared engine
// account ListDatasets returns every tenant's libraries, so raw engine
// existence alone must not let tenant A bind a library tenant B has already
// linked — even when A submits the engine id directly (bypassing the guarded
// dataset-options picker). A's own unlinked orphan library still passes so the
// explicitly-tagged engine fallback option stays bindable.
func TestValidateOwnedDatasetsCrossTenantBoundary(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.RAGFlow = httpNamedMock{svc.RAGFlow, "http"}
	tenantA, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	tenantB, err := svc.CreateTenant(ctx, "TenantB")
	if err != nil {
		t.Fatal(err)
	}
	bLib, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "B已链接台账"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{
		ID: "link-b3", TenantID: tenantB.ID, RAGFlowDatasetID: bLib.ID, Name: "B已链接台账",
	}); err != nil {
		t.Fatal(err)
	}
	own, err := svc.RAGFlow.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "A新建孤儿库"})
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.validateOwnedDatasets(ctx, tenantA.ID, []string{bLib.ID}); err == nil {
		t.Fatal("tenant A must NOT bind tenant B's already-linked library")
	} else if herr, ok := err.(*httperr.Error); !ok || herr.Status != 400 {
		t.Fatalf("cross-tenant binding must fail with 40092 (got %v)", err)
	}

	if err := svc.validateOwnedDatasets(ctx, tenantA.ID, []string{own.ID}); err != nil {
		t.Fatalf("tenant A's own unlinked orphan library must still bind: %v", err)
	}
}
