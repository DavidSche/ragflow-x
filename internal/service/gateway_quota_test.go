package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// quotaKey builds a gateway API key with an explicit monthly token budget.
func quotaKey(id string, budget int64) *model.APIKey {
	return &model.APIKey{ID: id, TenantID: "t1", UserID: "u1", TokenQuota: budget}
}

func requestQuotaKey(id string, budget int64) *model.APIKey {
	return &model.APIKey{ID: id, TenantID: "t1", UserID: "u1", RequestQuota: budget}
}

func assertQuotaHTTPError(t *testing.T, err error, status, code int) *httperr.Error {
	t.Helper()
	herr, ok := err.(*httperr.Error)
	if !ok {
		t.Fatalf("expected *httperr.Error, got %T: %v", err, err)
	}
	if herr.Status != status || herr.Code != code {
		t.Fatalf("expected status=%d code=%d, got status=%d code=%d (%s)", status, code, herr.Status, herr.Code, herr.Message)
	}
	return herr
}

func TestPreauthorizeUnlimitedKeyBypassesQuota(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	key := quotaKey("key-unlimited", 0) // 0 = unlimited
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

	res, err := svc.PreauthorizeGatewayQuota(ctx, key, raw, "req-u")
	if err != nil {
		t.Fatalf("unlimited preauth: %v", err)
	}
	if res.Enabled {
		t.Fatal("unlimited key must disable the quota path")
	}
	if res.Remaining != -1 {
		t.Fatalf("expected unlimited remaining -1, got %d", res.Remaining)
	}
	// An unlimited key must not create a quota limit ledger row.
	q, err := svc.Store.GetQuotaLimit(ctx, key.ID, quotaPeriodStart())
	if err != nil {
		t.Fatal(err)
	}
	if q != nil {
		t.Fatalf("unlimited key must not create a quota limit row, got %+v", q)
	}
}

func TestPreauthorizeQuotaSufficient(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	key := quotaKey("key-ok", 1_000_000)
	raw := []byte(`{"messages":[{"role":"user","content":"hi there"}],"max_tokens":128}`)

	res, err := svc.PreauthorizeGatewayQuota(ctx, key, raw, "req-ok")
	if err != nil {
		t.Fatalf("preauth: %v", err)
	}
	est := estimateRequestTokens(raw)
	if !res.Enabled {
		t.Fatal("quota key must enable the reservation path")
	}
	if res.Remaining != 1_000_000-est {
		t.Fatalf("expected remaining %d, got %d", 1_000_000-est, res.Remaining)
	}
	r, err := svc.Store.GetQuotaReservation(ctx, "req-ok")
	if err != nil || r == nil {
		t.Fatalf("reservation: %+v err=%v", r, err)
	}
	if r.Status != model.QuotaReservationPending || r.Estimated != est {
		t.Fatalf("unexpected reservation: status=%s est=%d", r.Status, r.Estimated)
	}
}

func TestPreauthorizeOverBudgetRejectedAndRolledBack(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	key := quotaKey("key-low", 10)
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	period := quotaPeriodStart()
	// Pre-seed a tiny budget row; UpsertQuotaLimit uses DO NOTHING so it persists.
	if err := svc.Store.UpsertQuotaLimit(ctx, &model.QuotaLimit{
		TenantID: "t1", KeyID: key.ID, PeriodStart: period, TokenLimit: 10,
	}); err != nil {
		t.Fatal(err)
	}

	_, err := svc.PreauthorizeGatewayQuota(ctx, key, raw, "req-low")
	if err == nil {
		t.Fatal("expected over-budget preauth to be rejected")
	}
	_ = assertQuotaHTTPError(t, err, 429, 42930)

	q, err := svc.Store.GetQuotaLimit(ctx, key.ID, period)
	if err != nil || q == nil {
		t.Fatalf("quota row: %+v err=%v", q, err)
	}
	if q.Pending != 0 || q.TokensUsed != 0 {
		t.Fatalf("denied request must leave the ledger untouched, got used=%d pending=%d", q.TokensUsed, q.Pending)
	}
	r, err := svc.Store.GetQuotaReservation(ctx, "req-low")
	if err != nil || r == nil {
		t.Fatalf("reservation: %+v err=%v", r, err)
	}
	if r.Status != model.QuotaReservationReleased {
		t.Fatalf("denied request reservation must be rolled back, got status=%s", r.Status)
	}
}

// ScenarioID: SC-QUOTA-001
func TestP0_QUOTA_001_PreauthorizeIdempotentSameRequestID(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	key := quotaKey("key-idem", 1_000_000)
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	est := estimateRequestTokens(raw)

	first, err := svc.PreauthorizeGatewayQuota(ctx, key, raw, "req-idem")
	if err != nil {
		t.Fatalf("first preauth: %v", err)
	}
	second, err := svc.PreauthorizeGatewayQuota(ctx, key, raw, "req-idem")
	if err != nil {
		t.Fatalf("retry preauth: %v", err)
	}
	if !second.Enabled {
		t.Fatal("retry must still report quota enabled")
	}
	if second.Remaining != first.Remaining {
		t.Fatalf("retry must not double-deduct: first=%d second=%d", first.Remaining, second.Remaining)
	}
	q, err := svc.Store.GetQuotaLimit(ctx, key.ID, quotaPeriodStart())
	if err != nil || q == nil {
		t.Fatalf("quota row: %+v err=%v", q, err)
	}
	if q.Pending != est {
		t.Fatalf("expected pending %d (single reservation), got %d", est, q.Pending)
	}
}

func TestFinalizeGatewayQuotaCommitsActual(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	key := quotaKey("key-fin", 1_000_000)
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

	if _, err := svc.PreauthorizeGatewayQuota(ctx, key, raw, "req-fin"); err != nil {
		t.Fatal(err)
	}
	svc.FinalizeGatewayQuota(ctx, key, "req-fin", 42)
	q, err := svc.Store.GetQuotaLimit(ctx, key.ID, quotaPeriodStart())
	if err != nil || q == nil {
		t.Fatalf("quota row: %+v err=%v", q, err)
	}
	if q.TokensUsed != 42 || q.Pending != 0 {
		t.Fatalf("after finalize: used=%d pending=%d (want 42/0)", q.TokensUsed, q.Pending)
	}
	r, err := svc.Store.GetQuotaReservation(ctx, "req-fin")
	if err != nil || r == nil {
		t.Fatalf("reservation: %+v err=%v", r, err)
	}
	if r.Status != model.QuotaReservationFinalized || r.ActualTokens != 42 {
		t.Fatalf("unexpected reservation after finalize: %+v", r)
	}
	// Double finalize must be a no-op.
	svc.FinalizeGatewayQuota(ctx, key, "req-fin", 999)
	q, _ = svc.Store.GetQuotaLimit(ctx, key.ID, quotaPeriodStart())
	if q.TokensUsed != 42 {
		t.Fatalf("double finalize must be idempotent, used=%d", q.TokensUsed)
	}
}

func TestRequestOnlyQuotaRejectsSecondRequest(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	key := requestQuotaKey("key-request-only", 1)
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

	first, err := svc.PreauthorizeGatewayQuota(ctx, key, raw, "req-first")
	if err != nil {
		t.Fatalf("first request-only preauth: %v", err)
	}
	if !first.Enabled || !first.RequestEnabled || first.TokenEnabled {
		t.Fatalf("unexpected request-only quota flags: %+v", first)
	}
	if first.Remaining != -1 || first.RemainingRequests != 0 {
		t.Fatalf("unexpected request-only remainders: %+v", first)
	}

	_, err = svc.PreauthorizeGatewayQuota(ctx, key, raw, "req-second")
	if err == nil {
		t.Fatal("second request-only preauth must be rejected")
	}
	_ = assertQuotaHTTPError(t, err, 429, 42930)
}

func TestRequestOnlyQuotaFinalizesLogicalRequest(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	key := requestQuotaKey("key-request-fin", 2)
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

	if _, err := svc.PreauthorizeGatewayQuota(ctx, key, raw, "req-request-fin"); err != nil {
		t.Fatal(err)
	}
	svc.FinalizeGatewayQuota(ctx, key, "req-request-fin", 0)
	q, err := svc.Store.GetQuotaLimit(ctx, key.ID, quotaPeriodStart())
	if err != nil || q == nil {
		t.Fatalf("quota row: %+v err=%v", q, err)
	}
	if q.RequestsUsed != 1 || q.PendingRequests != 0 {
		t.Fatalf("after request finalize: requests_used=%d pending_requests=%d", q.RequestsUsed, q.PendingRequests)
	}
}

func TestRequestOnlyQuotaReleaseDoesNotConsumeRequest(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	key := requestQuotaKey("key-request-release", 1)
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

	if _, err := svc.PreauthorizeGatewayQuota(ctx, key, raw, "req-request-release"); err != nil {
		t.Fatal(err)
	}
	svc.ReleaseGatewayQuota(ctx, key, "req-request-release")
	q, err := svc.Store.GetQuotaLimit(ctx, key.ID, quotaPeriodStart())
	if err != nil || q == nil {
		t.Fatalf("quota row: %+v err=%v", q, err)
	}
	if q.RequestsUsed != 0 || q.PendingRequests != 0 {
		t.Fatalf("after request release: requests_used=%d pending_requests=%d", q.RequestsUsed, q.PendingRequests)
	}

	if _, err := svc.PreauthorizeGatewayQuota(ctx, key, raw, "req-request-retry"); err != nil {
		t.Fatalf("released request capacity must be reusable: %v", err)
	}
}

func TestReleaseGatewayQuotaAborts(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	key := quotaKey("key-rel", 1_000_000)
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

	if _, err := svc.PreauthorizeGatewayQuota(ctx, key, raw, "req-rel"); err != nil {
		t.Fatal(err)
	}
	svc.ReleaseGatewayQuota(ctx, key, "req-rel")
	q, err := svc.Store.GetQuotaLimit(ctx, key.ID, quotaPeriodStart())
	if err != nil || q == nil {
		t.Fatalf("quota row: %+v err=%v", q, err)
	}
	if q.TokensUsed != 0 || q.Pending != 0 {
		t.Fatalf("after release: used=%d pending=%d (want 0/0)", q.TokensUsed, q.Pending)
	}
	r, err := svc.Store.GetQuotaReservation(ctx, "req-rel")
	if err != nil || r == nil {
		t.Fatalf("reservation: %+v err=%v", r, err)
	}
	if r.Status != model.QuotaReservationReleased {
		t.Fatalf("unexpected reservation after release: %+v", r)
	}
	// Double release must be a no-op.
	svc.ReleaseGatewayQuota(ctx, key, "req-rel")
	q, _ = svc.Store.GetQuotaLimit(ctx, key.ID, quotaPeriodStart())
	if q.Pending != 0 {
		t.Fatalf("double release must be idempotent, pending=%d", q.Pending)
	}
}

func TestPreauthorizeExhaustsBudgetAtBoundary(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	raw := []byte(`{"messages":[{"role":"user","content":"hello world"}],"max_tokens":32}`)
	est := estimateRequestTokens(raw)
	budget := 3*est + 1 // room for exactly three preauthorizations
	key := quotaKey("key-bound", budget)

	ids := []string{"req-b0", "req-b1", "req-b2"}
	for i, id := range ids {
		res, err := svc.PreauthorizeGatewayQuota(ctx, key, raw, id)
		if err != nil {
			t.Fatalf("preauth %d (%s): %v", i, id, err)
		}
		if !res.Enabled {
			t.Fatalf("quota path must be enabled on attempt %d", i)
		}
	}

	// A fourth request must be rejected: remaining (budget-3*est) < est.
	_, err := svc.PreauthorizeGatewayQuota(ctx, key, raw, "req-b3")
	if err == nil {
		t.Fatal("expected budget exhaustion to reject the request")
	}
	_ = assertQuotaHTTPError(t, err, 429, 42930)
}

func TestPreauthorizeFailClosedOnLedgerError(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	key := quotaKey("key-closed", 100)
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

	// Force a ledger failure by closing the backing store: spending controls
	// must fail closed (502) rather than silently allowing an un-metered call.
	if err := svc.Store.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := svc.PreauthorizeGatewayQuota(ctx, key, raw, "req-closed")
	if err == nil {
		t.Fatal("expected fail-closed rejection on ledger error")
	}
	_ = assertQuotaHTTPError(t, err, 502, 50239)
}
