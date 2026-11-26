package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// The 2026-11-26 4.151 drill: the scenario wizard prefills the template's name,
// the first instantiation creates "客服 FAQ", the second one failed with
// `50261 ragflow configure search app failed`. Root cause, measured on engine
// v1.0.0:
//
//  1. `POST /searches` does NOT reject a duplicate name — it stores the name
//     **suffixed** ("客服 FAQ(1)"). The mock used to store the bare name.
//  2. Uphold's configure step (`PUT /searches/<id>`) compares the name it is
//     handed against every *other* app. We re-sent the requested name while the
//     app had been renamed underneath us, so the engine answered
//     **HTTP 200 + {"code":102,"message":"Duplicated search name."}** — a
//     business refusal inside a success status.
//  3. The service flattened every configure error into 502/50261, so the only
//     sentence the operator saw blamed the network (doc/304 §4 E169).
//
// This test pins the behavioural half of the fix: a second instantiation with
// the same name succeeds, under the engine's own suffixed name, and the shadow
// row records what the engine actually holds (not what we asked for).
func TestASecondInstantiationSucceedsUnderTheEnginesSuffixedName(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantNameSuffix")
	if err != nil {
		t.Fatal(err)
	}

	first, err := svc.CreateSearchApp(ctx, tenant.ID, "客服 FAQ", &ragflow.SearchConfig{KbIDs: []string{"d1"}})
	if err != nil {
		t.Fatalf("first instantiation must succeed: %v", err)
	}
	if first.Name != "客服 FAQ" {
		t.Fatalf("first app keeps the requested name, got %q", first.Name)
	}

	second, err := svc.CreateSearchApp(ctx, tenant.ID, "客服 FAQ", &ragflow.SearchConfig{KbIDs: []string{"d1"}})
	if err != nil {
		t.Fatalf("second instantiation must succeed too, got %v", err)
	}
	if second.Name != "客服 FAQ(1)" {
		t.Fatalf("second app must carry the engine's suffixed name, got %q", second.Name)
	}
	if second.ID == first.ID {
		t.Fatal("the two instantiations must be different apps")
	}

	// Both are real and both are configured: the configure step ran under the
	// suffixed name instead of colliding with the first app.
	items, total, err := svc.ListSearchApps(ctx, tenant.ID, false, repository.SearchAppFilter{}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("both apps must exist, got total=%d items=%d", total, len(items))
	}
	upstream, upstreamTotal, err := svc.RAGFlow.ListSearchApps(ctx, ragflow.ListSearchAppsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if upstreamTotal != 2 || len(upstream) != 2 {
		t.Fatalf("engine must hold both apps, got total=%d items=%d", upstreamTotal, len(upstream))
	}
}

// TestConfigureRejectionStillMapsTo409AndLeavesNoOrphan keeps the *defensive*
// half covered: an engine that refuses the configure step for a duplicate name
// (a genuine rename collision, or a version that does not suffix) must produce
// 409/41141 — not a 502 — and must not strand the app `POST` already created.
func TestConfigureRejectionStillMapsTo409AndLeavesNoOrphan(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantNameConflict")
	if err != nil {
		t.Fatal(err)
	}
	svc.RAGFlow = &failingConfigureRAGFlow{
		Mock: ragflow.NewMock(),
		err: &ragflow.Error{
			HTTPStatus: 200, Code: 102,
			Message: "Duplicated search name.", Type: ragflow.ErrorTypeBusiness,
		},
	}

	_, err = svc.CreateSearchApp(ctx, tenant.ID, "报告解读", &ragflow.SearchConfig{KbIDs: []string{"d1"}})
	if err == nil {
		t.Fatal("a refused configure must not look like success")
	}
	var he *httperr.Error
	if !errors.As(err, &he) {
		t.Fatalf("expected a structured error, got %T: %v", err, err)
	}
	if he.Status != 409 || he.Code != httperr.CodeSearchAppNameTaken {
		t.Fatalf("want 409/%d, got %d/%d (%s)", httperr.CodeSearchAppNameTaken, he.Status, he.Code, he.Message)
	}
	if !strings.Contains(he.Message, "换一个名称") {
		t.Fatalf("message must say what to do: %q", he.Message)
	}

	// No shadow row …
	_, total, err := svc.ListSearchApps(ctx, tenant.ID, false, repository.SearchAppFilter{}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 {
		t.Fatalf("a failed create must not leave a shadow row, got total=%d", total)
	}
	// … and no orphan upstream: the compensating delete removed what POST made.
	upstream, upstreamTotal, err := svc.RAGFlow.ListSearchApps(ctx, ragflow.ListSearchAppsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if upstreamTotal != 0 || len(upstream) != 0 {
		t.Fatalf("engine must hold nothing, got total=%d items=%d (orphan left behind)", upstreamTotal, len(upstream))
	}
}

// TestSearchAppUpstreamRejectionKeepsTheEngineSentence pins the general rule
// behind the same drill finding: a 5xx we cannot classify must still carry what
// the engine said, otherwise the next investigation starts from zero.
func TestSearchAppUpstreamRejectionKeepsTheEngineSentence(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantUpstreamDetail")
	if err != nil {
		t.Fatal(err)
	}
	svc.RAGFlow = &failingConfigureRAGFlow{
		Mock: ragflow.NewMock(),
		err:  &ragflow.Error{HTTPStatus: 500, Message: "clickhouse is unavailable", Type: ragflow.ErrorTypeTransport},
	}

	_, err = svc.CreateSearchApp(ctx, tenant.ID, "any name", &ragflow.SearchConfig{KbIDs: []string{"d1"}})
	if err == nil {
		t.Fatal("a failing configure must not look like success")
	}
	var he *httperr.Error
	if !errors.As(err, &he) {
		t.Fatalf("expected a structured error, got %T: %v", err, err)
	}
	if he.Status != 502 || he.Code != 50261 {
		t.Fatalf("want 502/50261, got %d/%d", he.Status, he.Code)
	}
	if !strings.Contains(he.Message, "clickhouse is unavailable") {
		t.Fatalf("the engine's sentence must survive into our error: %q", he.Message)
	}
	// Unclassifiable failures clean up too: the drill left `客服 FAQ(1)` and
	// `客服 FAQ(2)` in the engine with nothing on our side pointing at them.
	if _, upstreamTotal, err := svc.RAGFlow.ListSearchApps(ctx, ragflow.ListSearchAppsFilter{}); err != nil || upstreamTotal != 0 {
		t.Fatalf("engine must hold nothing after a failed configure, got total=%d err=%v", upstreamTotal, err)
	}
}

// TestUpdatingASearchAppOntoATakenNameIsAConflict covers the duplicate-name
// mapping on the edit path (`PUT /search-apps/:id`), which shares the helper.
func TestUpdatingASearchAppOntoATakenNameIsAConflict(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantRenameConflict")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSearchApp(ctx, tenant.ID, "报告解读", &ragflow.SearchConfig{KbIDs: []string{"d1"}}); err != nil {
		t.Fatal(err)
	}
	other, err := svc.CreateSearchApp(ctx, tenant.ID, "报告解读-v2", &ragflow.SearchConfig{KbIDs: []string{"d1"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpdateSearchApp(ctx, tenant.ID, other.ID, "报告解读", nil, false)
	if err == nil {
		t.Fatal("renaming onto a taken name must be refused")
	}
	var he *httperr.Error
	if !errors.As(err, &he) || he.Status != 409 || he.Code != httperr.CodeSearchAppNameTaken {
		t.Fatalf("want 409/%d, got %+v", httperr.CodeSearchAppNameTaken, err)
	}
}

// failingConfigureRAGFlow is a Client whose configure step fails with a chosen
// error; every other call is the standard mock.
type failingConfigureRAGFlow struct {
	*ragflow.Mock
	err error
}

func (f *failingConfigureRAGFlow) UpdateSearchApp(ctx context.Context, searchAppID string, req ragflow.UpdateSearchAppRequest) (*ragflow.SearchApp, error) {
	return nil, f.err
}
