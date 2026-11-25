package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"gorm.io/gorm"
)

// GW-3 daily gateway billing reconciliation (doc/301 §5.10 / T21).
//
// Two layers are tested separately, on purpose:
//
//   - the pure comparison against a hand-built snapshot, so every finding kind has a
//     test that cannot pass because of a database coincidence;
//   - the service against a real migrated schema, because the checks are cross-table
//     facts and the idempotency guard lives in a unique index, not in Go.

type billingFixture struct {
	svc      *Service
	gdb      *gorm.DB
	tenantID string
}

func newBillingFixture(t *testing.T, name string) *billingFixture {
	t.Helper()
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: testSQLiteDSN(t, "gw-billing-"+name+".db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	engine := &reportTestEngine{Mock: *ragflow.NewMock()}
	svc := New(repository.NewStore(gdb), engine, jwt.NewManager("secret", 24), "key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	svc.RAGFlow = engine
	if err := svc.BootstrapAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	tenant, err := svc.CreateTenant(ctx, "计费对账工作区 "+name)
	if err != nil {
		t.Fatal(err)
	}
	return &billingFixture{svc: svc, gdb: gdb, tenantID: tenant.ID}
}

// seedIdempotency writes one caller-facing retry row at `at`.
func (f *billingFixture) seedIdempotency(t *testing.T, requestID, key, status string, at time.Time) {
	t.Helper()
	row := &model.GatewayIdempotency{
		ID:                 "idem-" + requestID + "-" + key,
		TenantID:           f.tenantID,
		PrincipalID:        "principal-" + f.tenantID,
		IdempotencyKey:     key,
		RequestFingerprint: "fp-" + key,
		Status:             status,
		RequestID:          requestID,
		ResponseRef:        "",
		CreatedAt:          at,
		ExpiresAt:          at.Add(24 * time.Hour),
	}
	if err := f.gdb.Create(row).Error; err != nil {
		t.Fatal(err)
	}
}

func (f *billingFixture) seedMeter(t *testing.T, requestID, keyID, day string) {
	t.Helper()
	if err := f.gdb.Create(&model.MeterRequest{
		RequestID: requestID, TenantID: f.tenantID, UserID: "u1", KeyID: keyID,
		Date: day, CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func (f *billingFixture) seedUsage(t *testing.T, keyID, day string, requests int64) {
	t.Helper()
	if err := f.gdb.Create(&model.QuotaUsage{
		ID: "usage-" + keyID + "-" + day, TenantID: f.tenantID, UserID: "u1", KeyID: keyID,
		Date: day, Requests: requests, CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func (f *billingFixture) seedCost(t *testing.T, requestID, keyID, day string) {
	t.Helper()
	if err := f.gdb.Create(&model.CostMetric{
		ID: "cost-" + requestID, RequestID: requestID, TenantID: f.tenantID, UserID: "u1",
		KeyID: keyID, Date: day, CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func (f *billingFixture) seedReservation(t *testing.T, requestID, keyID, period, status string, estimated, actual int64, at time.Time) {
	t.Helper()
	if err := f.gdb.Create(&model.QuotaReservation{
		RequestID: requestID, TenantID: f.tenantID, KeyID: keyID, PeriodStart: period,
		Estimated: estimated, ActualTokens: actual, Status: status, Applied: true, CreatedAt: at,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func (f *billingFixture) seedBalance(t *testing.T, keyID, period string, used int64) {
	t.Helper()
	if err := f.gdb.Create(&model.QuotaLimit{
		ID: "limit-" + keyID + "-" + period, TenantID: f.tenantID, KeyID: keyID,
		PeriodStart: period, TokenLimit: 1_000_000, TokensUsed: used,
		CreatedAt: time.Now().UTC(),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

// TestReconcileGatewayBillingPure is the unit layer: one table row per finding kind,
// each sourced from a snapshot a human can read and check by eye.
func TestReconcileGatewayBillingPure(t *testing.T) {
	now := time.Now().UTC()
	cases := []struct {
		name        string
		snapshot    model.GatewayBillingSnapshot
		wantKinds   []string
		wantChecked int
	}{
		{
			name: "clean day has findings zero but a non-zero denominator",
			snapshot: model.GatewayBillingSnapshot{
				Idempotency:   []model.GatewayBillingLedgerRef{{RequestID: "r1", Status: model.IdempotencyCompleted}},
				Metered:       []string{"r1"},
				Charged:       []model.GatewayBillingLedgerRef{{RequestID: "r1", KeyID: "k1"}},
				UsageRequests: 1,
			},
			wantChecked: 2, // one retry row + the day-level usage pair
		},
		{
			name: "completed without a metering stamp is missing_meter",
			snapshot: model.GatewayBillingSnapshot{
				Idempotency:   []model.GatewayBillingLedgerRef{{RequestID: "r1", Status: model.IdempotencyCompleted}},
				UsageRequests: 0,
			},
			wantKinds:   []string{model.GatewayBillingFindingMissingMeter},
			wantChecked: 2,
		},
		{
			name: "metered without a cost row is missing_cost",
			snapshot: model.GatewayBillingSnapshot{
				Idempotency:   []model.GatewayBillingLedgerRef{{RequestID: "r1", Status: model.IdempotencyCompleted}},
				Metered:       []string{"r1"},
				UsageRequests: 1,
			},
			wantKinds:   []string{model.GatewayBillingFindingMissingCost},
			wantChecked: 2,
		},
		{
			name: "a failed request that carries a cost row is charged_after_failure",
			snapshot: model.GatewayBillingSnapshot{
				Idempotency:   []model.GatewayBillingLedgerRef{{RequestID: "r1", Status: model.IdempotencyFailed}},
				Charged:       []model.GatewayBillingLedgerRef{{RequestID: "r1"}},
				UsageRequests: 0,
			},
			wantKinds:   []string{model.GatewayBillingFindingChargedAfterFailure},
			wantChecked: 2,
		},
		{
			name: "one request id on two retry rows is duplicate_request",
			snapshot: model.GatewayBillingSnapshot{
				Idempotency: []model.GatewayBillingLedgerRef{
					{RequestID: "r1", Status: model.IdempotencyCompleted},
					{RequestID: "r1", Status: model.IdempotencyCompleted},
				},
				Metered:           []string{"r1"},
				Charged:           []model.GatewayBillingLedgerRef{{RequestID: "r1"}},
				DuplicateRequests: []string{"r1"},
				UsageRequests:     1,
			},
			wantKinds:   []string{model.GatewayBillingFindingDuplicateRequest},
			wantChecked: 4, // two retry rows + the duplicate id + the usage pair
		},
		{
			name: "a leaked reservation is stale_reservation",
			snapshot: model.GatewayBillingSnapshot{
				StaleReservations: []model.GatewayBillingReservationRef{
					{RequestID: "r1", KeyID: "k1", PeriodStart: "2026-08", Estimated: 100, CreatedAt: now.Add(-2 * time.Hour)},
				},
			},
			wantKinds:   []string{model.GatewayBillingFindingStaleReservation},
			wantChecked: 2, // the reservation + the usage pair
		},
		{
			name: "settled tokens away from the counter is quota_drift",
			snapshot: model.GatewayBillingSnapshot{
				Buckets: []model.GatewayBillingBucketDrift{
					{KeyID: "k1", PeriodStart: "2026-08", ExpectedTokens: 900, ActualTokens: 800},
				},
			},
			wantKinds:   []string{model.GatewayBillingFindingQuotaDrift},
			wantChecked: 2, // the bucket + the usage pair
		},
		{
			name: "a one-token settle rounding is inside tolerance",
			snapshot: model.GatewayBillingSnapshot{
				Buckets: []model.GatewayBillingBucketDrift{
					{KeyID: "k1", PeriodStart: "2026-08", ExpectedTokens: 900, ActualTokens: 899},
				},
			},
			wantChecked: 2,
		},
		{
			name: "metering count away from the usage aggregate is usage_drift",
			snapshot: model.GatewayBillingSnapshot{
				Metered:       []string{"r1", "r2"},
				UsageRequests: 1,
			},
			wantKinds:   []string{model.GatewayBillingFindingUsageDrift},
			wantChecked: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := reconcileGatewayBilling(&tc.snapshot)
			if out.Checked != tc.wantChecked {
				t.Fatalf("checked = %d, want %d", out.Checked, tc.wantChecked)
			}
			got := map[string]int{}
			for _, row := range out.Rows {
				got[row.Kind]++
				if !model.IsGatewayBillingFindingKind(row.Kind) {
					t.Fatalf("finding kind %q is outside the closed set", row.Kind)
				}
			}
			if len(got) != len(tc.wantKinds) {
				t.Fatalf("kinds = %v, want %v", got, tc.wantKinds)
			}
			for _, kind := range tc.wantKinds {
				if got[kind] != 1 {
					t.Fatalf("kind %q count = %d, want 1 (all: %v)", kind, got[kind], got)
				}
			}
			if out.Findings != len(out.Rows) {
				t.Fatalf("findings = %d, rows = %d", out.Findings, len(out.Rows))
			}
		})
	}
}

// TestReconcileGatewayBillingReadsMeteringState is the E146 correction in one place.
//
// The nightly run of 2026-11-23 reported 14 `missing_meter` findings. All 14 were
// completed requests whose provider had answered 429 (
// `{"error":{"message":"Rate limit exceeded: maximum 20 requests per minute…"}}`),
// so nothing was served, nothing was owed, and no metering row existed — correctly.
// The reconciler could not tell that apart from a lost write, because `COMPLETED`
// meant three things at once until E147 split them. It reads the split now:
//
//	none          nothing owed      → no finding (a cost row is still a finding)
//	not_tracked   cannot vouch      → missing_meter, and the detail says which absence
//	recorded      books were written → a missing meter row is a contradiction, still reported
//
// The 14 historical rows stay reported if that day is ever re-reconciled: they
// predate the column and read back as `not_tracked`, which is exactly the honest
// answer. What changed is that the *next* upstream-error request cannot manufacture
// the same finding.
func TestReconcileGatewayBillingReadsMeteringState(t *testing.T) {
	cases := []struct {
		name      string
		state     string
		charged   bool
		wantKinds []string
		wantIn    string // a sentence the finding detail must carry, if any
	}{
		{
			name:  "nothing owed is not a hole in the books",
			state: model.MeteringNone,
		},
		{
			name:  "nothing owed but charged is still a finding",
			state: model.MeteringNone, charged: true,
			wantKinds: []string{model.GatewayBillingFindingChargedAfterFailure},
			wantIn:    "metering_state=none",
		},
		{
			name:      "history cannot be vouched for",
			state:     "",
			wantKinds: []string{model.GatewayBillingFindingMissingMeter},
			wantIn:    "metering_state=not_tracked",
		},
		{
			name:      "recorded without a meter row is a contradiction and stays reported",
			state:     model.MeteringRecorded,
			wantKinds: []string{model.GatewayBillingFindingMissingMeter},
			wantIn:    "metering_state=recorded",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := model.GatewayBillingSnapshot{
				Idempotency: []model.GatewayBillingLedgerRef{
					{RequestID: "r1", KeyID: "k1", Status: model.IdempotencyCompleted, MeteringState: tc.state},
				},
			}
			if tc.charged {
				snapshot.Charged = []model.GatewayBillingLedgerRef{{RequestID: "r1", KeyID: "k1"}}
			}
			out := reconcileGatewayBilling(&snapshot)
			if out.Checked != 2 {
				t.Fatalf("checked = %d, want 2 (one retry row + the usage pair)", out.Checked)
			}
			if len(out.Rows) != len(tc.wantKinds) {
				t.Fatalf("findings = %v, want %v", out.Rows, tc.wantKinds)
			}
			for i, kind := range tc.wantKinds {
				if out.Rows[i].Kind != kind {
					t.Fatalf("kind = %q, want %q", out.Rows[i].Kind, kind)
				}
			}
			if tc.wantIn != "" {
				if len(out.Rows) == 0 || !strings.Contains(out.Rows[0].Detail, tc.wantIn) {
					t.Fatalf("detail = %q, want it to name %q", out.Rows, tc.wantIn)
				}
			}
		})
	}
}

// TestReconcileGatewayBillingNilSnapshot proves the pure layer cannot panic on a
// tenant with no gateway traffic at all: the commonest day in a fresh install.
func TestReconcileGatewayBillingNilSnapshot(t *testing.T) {
	out := reconcileGatewayBilling(nil)
	if out.Checked != 0 || out.Findings != 0 || len(out.Rows) != 0 {
		t.Fatalf("nil snapshot produced %+v", out)
	}
}

// TestGatewayBillingReconcileDayIsThePreviousCompletedDay pins the window choice:
// reconciling the running day would report `missing_cost` for every request whose cost
// row lands a moment after its meter row — findings manufactured by the clock.
func TestGatewayBillingReconcileDayIsThePreviousCompletedDay(t *testing.T) {
	now := time.Date(2026, 8, 30, 0, 5, 0, 0, time.UTC)
	if got := gatewayBillingReconcileDay(now); got != "2026-08-29" {
		t.Fatalf("day = %s, want 2026-08-29", got)
	}
	from, to, err := gatewayBillingDayWindow("2026-08-29")
	if err != nil {
		t.Fatal(err)
	}
	if to.Sub(from) != 24*time.Hour || !from.Equal(time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("window = %s..%s", from, to)
	}
	if _, _, err := gatewayBillingDayWindow("2026/08/29"); err == nil {
		t.Fatal("a malformed day must not parse")
	}
}

// TestReconcileGatewayBillingDayEndToEnd drives one dirty day through a real schema
// and then proves the run is the idempotency guard.
func TestReconcileGatewayBillingDayEndToEnd(t *testing.T) {
	f := newBillingFixture(t, "e2e")
	ctx := context.Background()
	day := "2026-08-20"
	at := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

	// r1: completed, metered, costed — clean.
	f.seedIdempotency(t, "r1", "key-1", model.IdempotencyCompleted, at)
	f.seedMeter(t, "r1", "k1", day)
	f.seedCost(t, "r1", "k1", day)
	// r2: completed, never metered.
	f.seedIdempotency(t, "r2", "key-2", model.IdempotencyCompleted, at)
	// r3: metered, no cost row.
	f.seedIdempotency(t, "r3", "key-3", model.IdempotencyCompleted, at)
	f.seedMeter(t, "r3", "k1", day)
	// r4: failed but charged.
	f.seedIdempotency(t, "r4", "key-4", model.IdempotencyFailed, at)
	f.seedCost(t, "r4", "k1", day)
	// r5 on two retry rows (one request, two caller keys).
	f.seedIdempotency(t, "r5", "key-5a", model.IdempotencyCompleted, at)
	f.seedIdempotency(t, "r5", "key-5b", model.IdempotencyCompleted, at)
	f.seedMeter(t, "r5", "k1", day)
	f.seedCost(t, "r5", "k1", day)
	// A leaked reservation from two hours ago.
	f.seedReservation(t, "r6", "k1", "2026-08", model.QuotaReservationPending, 500, 0,
		time.Now().UTC().Add(-2*time.Hour))
	// One settled reservation of 900 against a counter that says 800.
	f.seedReservation(t, "r7", "k1", "2026-08", model.QuotaReservationFinalized, 900, 900, at)
	f.seedBalance(t, "k1", "2026-08", 800)
	// The usage aggregate bills three requests; the metering ledger stamped three.
	f.seedUsage(t, "k1", day, 3)

	// A row from another day must not be examined: the day window is the guard.
	f.seedIdempotency(t, "other", "key-other", model.IdempotencyCompleted, at.AddDate(0, 0, -1))

	run, err := f.svc.ReconcileGatewayBillingDay(ctx, f.tenantID, day)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != model.GatewayBillingReconcileCompleted {
		t.Fatalf("status = %s, reason = %s", run.Status, run.FailureReason)
	}
	want := map[string]int{
		model.GatewayBillingFindingMissingMeter:        1,
		model.GatewayBillingFindingMissingCost:         1,
		model.GatewayBillingFindingChargedAfterFailure: 1,
		model.GatewayBillingFindingDuplicateRequest:    1,
		model.GatewayBillingFindingStaleReservation:    1,
		model.GatewayBillingFindingQuotaDrift:          1,
		model.GatewayBillingFindingUsageDrift:          0,
	}
	got := map[string]int{
		model.GatewayBillingFindingMissingMeter:        run.MissingMeter,
		model.GatewayBillingFindingMissingCost:         run.MissingCost,
		model.GatewayBillingFindingChargedAfterFailure: run.ChargedAfterFailure,
		model.GatewayBillingFindingDuplicateRequest:    run.DuplicateRequest,
		model.GatewayBillingFindingStaleReservation:    run.StaleReservation,
		model.GatewayBillingFindingQuotaDrift:          run.QuotaDriftBuckets,
		model.GatewayBillingFindingUsageDrift:          run.UsageDrift,
	}
	for kind, count := range want {
		if got[kind] != count {
			t.Fatalf("%s = %d, want %d (run: %+v)", kind, got[kind], count, run)
		}
	}
	// Denominator, by hand: six in-window retry rows (r1, r2, r3, r4 and r5's two
	// rows) + one duplicate request id + one leaked reservation + one bucket + the
	// day-level usage pair = 10. The number is spelled out because it is the one value
	// a reader would otherwise have to trust.
	if run.Checked != 10 {
		t.Fatalf("checked = %d, want 10", run.Checked)
	}
	if run.Findings != 6 {
		t.Fatalf("findings = %d, want 6", run.Findings)
	}
	if run.QuotaExpectedTokens != 900 || run.QuotaActualTokens != 800 {
		t.Fatalf("token sides = %d/%d, want 900/800", run.QuotaExpectedTokens, run.QuotaActualTokens)
	}
	if run.UsageMeteredRequests != 3 || run.UsageBilledRequests != 3 {
		t.Fatalf("usage sides = %d/%d, want 3/3", run.UsageMeteredRequests, run.UsageBilledRequests)
	}

	findings, err := f.svc.GatewayBillingReconcileFindings(ctx, f.tenantID, run.ID, model.GatewayBillingFindingQuotaDrift, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].KeyID != "k1" {
		t.Fatalf("quota_drift findings = %+v", findings)
	}

	// The run is the idempotency guard: a replay returns the same row and writes no
	// second set of findings.
	again, err := f.svc.ReconcileGatewayBillingDay(ctx, f.tenantID, day)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != run.ID {
		t.Fatalf("replay created a second run: %s vs %s", again.ID, run.ID)
	}
	var total int64
	if err := f.gdb.Model(&model.GatewayBillingReconcileFinding{}).
		Where("tenant_id = ? AND run_id = ?", f.tenantID, run.ID).Count(&total).Error; err != nil {
		t.Fatal(err)
	}
	if total != 6 {
		t.Fatalf("findings after replay = %d, want 6", total)
	}
}

// TestReconcileGatewayBillingCleanDay proves the healthy answer is "checked > 0,
// findings = 0" — never "0/0", which would make a broken query look healthy.
func TestReconcileGatewayBillingCleanDay(t *testing.T) {
	f := newBillingFixture(t, "clean")
	ctx := context.Background()
	day := "2026-08-21"
	at := time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC)
	f.seedIdempotency(t, "r1", "key-1", model.IdempotencyCompleted, at)
	f.seedMeter(t, "r1", "k1", day)
	f.seedCost(t, "r1", "k1", day)
	f.seedUsage(t, "k1", day, 1)

	run, err := f.svc.ReconcileGatewayBillingDay(ctx, f.tenantID, day)
	if err != nil {
		t.Fatal(err)
	}
	if run.Findings != 0 || run.Checked == 0 {
		t.Fatalf("clean day = findings %d, checked %d", run.Findings, run.Checked)
	}
}

// TestReconcileGatewayBillingRejectsBadInput keeps the closed set closed: a malformed
// day and an unknown finding kind are client errors, not empty answers.
func TestReconcileGatewayBillingRejectsBadInput(t *testing.T) {
	f := newBillingFixture(t, "input")
	ctx := context.Background()
	if _, err := f.svc.ReconcileGatewayBillingDay(ctx, "", "2026-8-9"); err == nil {
		t.Fatal("a malformed day must be refused")
	}
	if _, _, err := f.svc.ReconcileGatewayBillingAllDay(ctx, "2026-8-9"); err == nil {
		t.Fatal("a malformed day must be refused by the on-demand form too")
	}
	if _, err := f.svc.GatewayBillingReconcileFindings(ctx, f.tenantID, "run-x", "not_a_kind", 10); err == nil {
		t.Fatal("an unknown kind must be refused")
	}
}
