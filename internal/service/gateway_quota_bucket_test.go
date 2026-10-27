package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// seedTenantBucket configures a tenant-dimension gateway quota bucket for a
// tenant/period with the given monthly token and request budgets.
func seedTenantBucket(t *testing.T, svc *Service, tenantID string, tokenLimit, requestLimit int64) {
	t.Helper()
	err := svc.Store.UpsertQuotaBucket(context.Background(), &model.QuotaBucket{
		TenantID: tenantID, Dimension: model.QuotaBucketDimensionTenant, DimensionKey: tenantID,
		PeriodStart: quotaPeriodStart(), TokenLimit: tokenLimit, RequestLimit: requestLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func bucketKey(id, tenantID string) *model.APIKey {
	return &model.APIKey{ID: id, TenantID: tenantID, UserID: "u1", TokenQuota: 0, RequestQuota: 0}
}

// ScenarioID: SC-GW-006-1 (doc/224 §2.5 T5.1)
func TestBucketAbsentTenantBypasses(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	key := bucketKey("key-no-bucket", "tenant-absent")
	res, err := svc.PreauthorizeTenantBucketQuota(ctx, key, "req-nb", 100, true, true)
	if err != nil {
		t.Fatalf("absent tenant bucket must not block: %v", err)
	}
	if res.Enabled {
		t.Fatal("absent tenant bucket must disable the bucket path")
	}
	if res.Remaining != -1 || res.RemainingRequests != -1 {
		t.Fatalf("absent tenant bucket must report unlimited: %+v", res)
	}
	bkt, err := svc.Store.GetQuotaBucket(ctx, key.TenantID, model.QuotaBucketDimensionTenant, key.TenantID, quotaPeriodStart())
	if err != nil {
		t.Fatal(err)
	}
	if bkt != nil {
		t.Fatalf("absent bucket path must not create rows, got %+v", bkt)
	}
}

// ScenarioID: SC-GW-006-2
func TestBucketPreauthorizeSufficient(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	seedTenantBucket(t, svc, "t-bucket", 1_000_000, 0)
	key := bucketKey("key-b1", "t-bucket")
	res, err := svc.PreauthorizeTenantBucketQuota(ctx, key, "req-b1", 1000, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Enabled || !res.TokenEnabled {
		t.Fatalf("bucket gate must be enabled: %+v", res)
	}
	if res.Remaining != 1_000_000-1000 {
		t.Fatalf("expected remaining %d, got %d", 1_000_000-1000, res.Remaining)
	}
}

// ScenarioID: SC-GW-006-3 — HTTP 430 would-block on bucket exhaustion, with no
// dangling reservation row left behind.
func TestBucketPreauthorizeExhausted430(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	seedTenantBucket(t, svc, "t-low", 100, 0)
	key := bucketKey("key-blow", "t-low")
	_, err := svc.PreauthorizeTenantBucketQuota(ctx, key, "req-blow", 10_000, true, false)
	_ = assertQuotaHTTPError(t, err, 430, gatewayBucketQuotaExhausted)
	// Denied request must leave no dangling reservation row.
	r, err := svc.Store.GetQuotaBucketReservation(ctx, "req-blow")
	if err != nil {
		t.Fatal(err)
	}
	if r != nil && r.Applied {
		t.Fatalf("exhausted reservation must not be applied: %+v", r)
	}
}

// ScenarioID: SC-GW-006-4
func TestBucketFinalizeCommitsActual(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	seedTenantBucket(t, svc, "t-f", 1_000_000, 0)
	key := bucketKey("key-bf", "t-f")
	if _, err := svc.PreauthorizeTenantBucketQuota(ctx, key, "req-bf", 1000, true, false); err != nil {
		t.Fatal(err)
	}
	svc.FinalizeTenantBucketQuota(ctx, key, "req-bf", 420)
	bkt, err := svc.Store.GetQuotaBucket(ctx, key.TenantID, model.QuotaBucketDimensionTenant, key.TenantID, quotaPeriodStart())
	if err != nil {
		t.Fatal(err)
	}
	if bkt.TokensUsed != 420 {
		t.Fatalf("expected actual tokens consumed 420, got %d", bkt.TokensUsed)
	}
	if bkt.Pending != 0 {
		t.Fatalf("finalize must clear pending, got %d", bkt.Pending)
	}
	if bkt.TokenLimit-bkt.TokensUsed != 1_000_000-420 {
		t.Fatalf("remaining must reflect actual consumption, got limit=%d used=%d", bkt.TokenLimit, bkt.TokensUsed)
	}
}

// ScenarioID: SC-GW-006-5
func TestBucketReleaseAborts(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	seedTenantBucket(t, svc, "t-r", 1_000_000, 0)
	key := bucketKey("key-br", "t-r")
	if _, err := svc.PreauthorizeTenantBucketQuota(ctx, key, "req-br", 1000, true, false); err != nil {
		t.Fatal(err)
	}
	svc.ReleaseTenantBucketQuota(ctx, key, "req-br")
	bkt, err := svc.Store.GetQuotaBucket(ctx, key.TenantID, model.QuotaBucketDimensionTenant, key.TenantID, quotaPeriodStart())
	if err != nil {
		t.Fatal(err)
	}
	if bkt.Pending != 0 || bkt.TokensUsed != 0 {
		t.Fatalf("release must not consume budget: used=%d pending=%d", bkt.TokensUsed, bkt.Pending)
	}
}

// ScenarioID: SC-GW-006-6 — request-only bucket rejects the second in-flight
// request (batch semantics identical to the key ledger).
func TestBucketRequestOnlyRejectsSecondRequest(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	seedTenantBucket(t, svc, "t-rq", 0, 1)
	key := bucketKey("key-brq", "t-rq")
	first, err := svc.PreauthorizeTenantBucketQuota(ctx, key, "req-rq1", 1, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if first.RemainingRequests != 0 {
		t.Fatalf("expected 0 requests remaining after first reserve, got %d", first.RemainingRequests)
	}
	_, err = svc.PreauthorizeTenantBucketQuota(ctx, key, "req-rq2", 1, false, true)
	_ = assertQuotaHTTPError(t, err, 430, gatewayBucketQuotaExhausted)
}

// ScenarioID: SC-GW-006-7 — stale in-flight reservations are swept so a crash
// cannot permanently wedge a near-exhausted bucket.
func TestReapStaleBucketReservation(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	seedTenantBucket(t, svc, "t-stale", 1000, 0)
	key := bucketKey("key-bstale", "t-stale")
	if _, err := svc.PreauthorizeTenantBucketQuota(ctx, key, "req-stale", 500, true, false); err != nil {
		t.Fatal(err)
	}
	// Sweep with a now cutoff — the reservation was just created so it is
	// strictly older than now, exercising the same release path as the aged
	// case without needing a created_at backdate helper.
	released, err := svc.Store.ReapStaleQuotaBucketReservations(ctx, key.TenantID, model.QuotaBucketDimensionTenant, key.TenantID, quotaPeriodStart(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if released != 500 {
		t.Fatalf("expected 500 stale tokens released, got %d", released)
	}
	bkt, err := svc.Store.GetQuotaBucket(ctx, key.TenantID, model.QuotaBucketDimensionTenant, key.TenantID, quotaPeriodStart())
	if err != nil {
		t.Fatal(err)
	}
	if bkt.Pending != 0 {
		t.Fatalf("stale pending must be swept, got %d", bkt.Pending)
	}
}

// ScenarioID: SC-GW-006-8 — the composite gate reports the tightest remaining
// budget across key and tenant-bucket dimensions.
func TestGatewayQuotaWithBucketComposite(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	seedTenantBucket(t, svc, "t-comp", 5_000, 0)
	// Key budget 10k, tenant bucket 5k -> remaining must be driven by the
	// bucket (4,000 after a 1,000 reservation).
	key := &model.APIKey{ID: "key-comp", TenantID: "t-comp", UserID: "u1", TokenQuota: 10_000}
	raw := []byte(`{"messages":[{"role":"user","content":"hello world"}]}`)
	res, err := svc.PreauthorizeGatewayQuotaWithBucket(ctx, key, raw, "req-comp")
	if err != nil {
		t.Fatal(err)
	}
	if res.Remaining > 5_000 {
		t.Fatalf("composite gate must report tightest dimension: %+v", res)
	}
}

// ScenarioID: SC-GW-006-9 — when the bucket rejects, the key-side reservation
// is rolled back so no ledger row dangles across dimensions.
func TestGatewayQuotaWithBucketRollsBackKeyOnBucketReject(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	seedTenantBucket(t, svc, "t-rej", 1, 0)
	key := &model.APIKey{ID: "key-rej", TenantID: "t-rej", UserID: "u1", TokenQuota: 10_000}
	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	_, err := svc.PreauthorizeGatewayQuotaWithBucket(ctx, key, raw, "req-rej")
	_ = assertQuotaHTTPError(t, err, 430, gatewayBucketQuotaExhausted)
	// Key ledger must have no pending/used after the rejected composite gate.
	ql, err := svc.Store.GetQuotaLimit(ctx, key.ID, quotaPeriodStart())
	if err != nil {
		t.Fatal(err)
	}
	if ql == nil {
		t.Fatal("key ledger row expected after key preauth")
	}
	if ql.Pending != 0 || ql.TokensUsed != 0 {
		t.Fatalf("key reservation must be rolled back on bucket reject: used=%d pending=%d", ql.TokensUsed, ql.Pending)
	}
}

// seedModelBucket configures a model-dimension quota bucket for a tenant/model.
func seedModelBucket(t *testing.T, svc *Service, tenantID, modelName string, tokenLimit, requestLimit int64) {
	t.Helper()
	err := svc.Store.UpsertQuotaBucket(context.Background(), &model.QuotaBucket{
		TenantID: tenantID, Dimension: model.QuotaBucketDimensionModel, DimensionKey: modelName,
		PeriodStart: quotaPeriodStart(), TokenLimit: tokenLimit, RequestLimit: requestLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// modelRequestBody builds a composite-gate request body naming a model with a
// non-trivial payload so the token estimate clears typical bucket limits.
func modelRequestBody(modelName string, payloadChars int) []byte {
	return []byte(`{"model":"` + modelName + `","messages":[{"role":"user","content":"` + strings.Repeat("a", payloadChars) + `"}]}`)
}

// ScenarioID: SC-GW-008-1 (doc/224 §2.5 T5 — model dimension wired at the call
// point). When the request body names a model and a model bucket exists, the
// composite gate reserves against the model bucket and the finalize settles it.
func TestBucketModelDimensionWiredByCompositeGate(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	seedModelBucket(t, svc, "t-m1", "gpt-4o", 1_000_000, 0)
	key := bucketKey("key-m1", "t-m1")
	raw := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	res, err := svc.PreauthorizeGatewayQuotaWithBucket(ctx, key, raw, "req-m1")
	if err != nil {
		t.Fatalf("model bucket sufficient must admit: %v", err)
	}
	if !res.Enabled {
		t.Fatal("model bucket gate must be enabled")
	}
	svc.FinalizeGatewayQuotaWithBucket(ctx, key, "req-m1", 350)
	bkt, err := svc.Store.GetQuotaBucket(ctx, key.TenantID, model.QuotaBucketDimensionModel, "gpt-4o", quotaPeriodStart())
	if err != nil {
		t.Fatal(err)
	}
	if bkt.TokensUsed != 350 || bkt.Pending != 0 {
		t.Fatalf("finalize must settle the model bucket: used=%d pending=%d", bkt.TokensUsed, bkt.Pending)
	}
	// Reservation root cause must be the model row, not a tenant row.
	tenant, err := svc.Store.GetQuotaBucket(ctx, key.TenantID, model.QuotaBucketDimensionTenant, key.TenantID, quotaPeriodStart())
	if err != nil {
		t.Fatal(err)
	}
	if tenant != nil {
		t.Fatalf("no tenant bucket row should be created when the model bucket wins: %+v", tenant)
	}
}

// ScenarioID: SC-GW-008-2 — exhausted model bucket rejects with 430/42931 and
// leaves no dangling reservation.
func TestBucketModelDimensionExhaustedRejects430(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	seedModelBucket(t, svc, "t-m2", "gpt-4o", 100, 0)
	key := bucketKey("key-m2", "t-m2")
	raw := modelRequestBody("gpt-4o", 800)
	_, err := svc.PreauthorizeGatewayQuotaWithBucket(ctx, key, raw, "req-m2")
	perr, ok := err.(*httperr.Error)
	if !ok || perr.Status != 430 || perr.Code != 42931 {
		t.Fatalf("model exhaust must reject 430/42931, got %v", err)
	}
	resv, err := svc.Store.GetQuotaBucketReservation(ctx, "req-m2")
	if err != nil {
		t.Fatal(err)
	}
	if resv != nil && resv.Applied {
		t.Fatalf("rejected model bucket must not leave an applied reservation: %+v", resv)
	}
	if resv != nil && resv.Status != model.QuotaReservationReleased {
		t.Fatalf("rejected model bucket reservation must be released for audit, got status=%q", resv.Status)
	}
}

// ScenarioID: SC-GW-008-3 — when no model bucket exists the composite gate
// falls back to the tenant bucket, preserving legacy tenant semantics.
func TestBucketModelAbsentFallsBackToTenant(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	seedTenantBucket(t, svc, "t-m3", 500_000, 0)
	key := bucketKey("key-m3", "t-m3")
	raw := []byte(`{"model":"gpt-4o","messages":[]}`)
	res, err := svc.PreauthorizeGatewayQuotaWithBucket(ctx, key, raw, "req-m3")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Enabled {
		t.Fatal("fallback tenant bucket must be enabled")
	}
	svc.FinalizeGatewayQuotaWithBucket(ctx, key, "req-m3", 77)
	tenant, err := svc.Store.GetQuotaBucket(ctx, key.TenantID, model.QuotaBucketDimensionTenant, key.TenantID, quotaPeriodStart())
	if err != nil {
		t.Fatal(err)
	}
	if tenant == nil || tenant.TokensUsed != 77 {
		t.Fatalf("tenant bucket must settle the fallback: %+v", tenant)
	}
}

// ScenarioID: SC-GW-008-4 — model bucket takes precedence over a tenant bucket
// when both exist; the model reservation is what finalize/release settle.
func TestBucketModelTakesPrecedenceOverTenant(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	seedTenantBucket(t, svc, "t-m4", 9_999_999, 0)
	seedModelBucket(t, svc, "t-m4", "gpt-4o", 100, 0)
	key := bucketKey("key-m4", "t-m4")
	raw := modelRequestBody("gpt-4o", 800)
	_, err := svc.PreauthorizeGatewayQuotaWithBucket(ctx, key, raw, "req-m4")
	if err == nil {
		t.Fatal("precedence model bucket (100 tokens) must reject the oversized request")
	}
	// The tenant bucket holds far more budget, so a rejection can only come
	// from the model bucket — proving precedence without a body parse.
	perr, ok := err.(*httperr.Error)
	if !ok || perr.Status != 430 || perr.Code != 42931 {
		t.Fatalf("precedence exhaust must reject 430/42931, got %v", err)
	}
}

// ScenarioID: SC-GW-008-5 — release on the composite gate frees the model
// bucket reservation without consuming budget.
func TestBucketModelReleaseDoesNotConsume(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	seedModelBucket(t, svc, "t-m5", "gpt-4o", 1_000_000, 0)
	key := bucketKey("key-m5", "t-m5")
	raw := []byte(`{"model":"gpt-4o","messages":[]}`)
	if _, err := svc.PreauthorizeGatewayQuotaWithBucket(ctx, key, raw, "req-m5"); err != nil {
		t.Fatal(err)
	}
	svc.ReleaseGatewayQuotaWithBucket(ctx, key, "req-m5")
	bkt, err := svc.Store.GetQuotaBucket(ctx, key.TenantID, model.QuotaBucketDimensionModel, "gpt-4o", quotaPeriodStart())
	if err != nil {
		t.Fatal(err)
	}
	if bkt.Pending != 0 || bkt.TokensUsed != 0 {
		t.Fatalf("release must not consume the model bucket: used=%d pending=%d", bkt.TokensUsed, bkt.Pending)
	}
}

// ScenarioID: SC-GW-008-6 — the body model-name extractor is permissive: the
// model field wins for the composite gate, missing/non-string bodies fall back.
func TestExtractModelNameParsesRequestBody(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"plain string", `{"model":"gpt-4o","messages":[]}`, "gpt-4o"},
		{"whitespace trimmed", `{"model":"  glm-5  ","messages":[]}`, "glm-5"},
		{"missing model", `{"messages":[]}`, ""},
		{"empty body", ``, ""},
		{"not json", `not-a-json`, ""},
		{"model is not a string", `{"model":42}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractModelName([]byte(tc.body)); got != tc.want {
				t.Fatalf("extractModelName(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}
