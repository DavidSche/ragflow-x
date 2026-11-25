package service

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// meteringFaultStore fails one ledger write on demand. The seam is the store
// interface rather than a branch inside the service, because that is where a real
// ledger failure appears (a database error at the write), so these tests exercise
// the production path: intent → ledgers → settle → verdict → completion.
type meteringFaultStore struct {
	repository.Store
	// failDetail is a countdown of detail-ledger writes that must fail. It is
	// shared across the retry attempts, so the count is "how many writes fail",
	// not "how many requests fail" — the distinction the retry policy turns on.
	failDetail  atomic.Int32
	detailCalls atomic.Int32
	// failSettle fails the outcome write, which leaves the intent pending even
	// when both ledgers landed.
	failSettle atomic.Bool
	// failIntent fails the write of the recovery record itself. It is the one
	// failure that cannot be repaired later, because there is no row naming what is
	// owed (doc/304 §4 E148).
	failIntent atomic.Bool
	// failCount fails the per-tenant gap query, which is the sweep's only source for
	// the unsettled gauge.
	failCount atomic.Bool
}

func (f *meteringFaultStore) CreateMeteringIntentIfAbsent(ctx context.Context, in *model.MeteringIntent) (bool, error) {
	if f.failIntent.Load() {
		return false, errors.New("metering intent insert unavailable: connection reset by peer")
	}
	return f.Store.CreateMeteringIntentIfAbsent(ctx, in)
}

func (f *meteringFaultStore) CountUnsettledMeteringIntentsByTenant(ctx context.Context) (map[string]int64, error) {
	if f.failCount.Load() {
		return nil, errors.New("metering state query unavailable")
	}
	return f.Store.CountUnsettledMeteringIntentsByTenant(ctx)
}

func (f *meteringFaultStore) RecordCostMetric(ctx context.Context, m *model.CostMetric) (bool, error) {
	f.detailCalls.Add(1)
	for {
		cur := f.failDetail.Load()
		if cur <= 0 {
			break
		}
		if f.failDetail.CompareAndSwap(cur, cur-1) {
			return false, errors.New("cost ledger unavailable: connection reset by peer")
		}
	}
	return f.Store.RecordCostMetric(ctx, m)
}

func (f *meteringFaultStore) SettleMeteringIntent(ctx context.Context, id, state, lastError string, attempts int) error {
	if f.failSettle.Load() {
		return errors.New("metering intent settle unavailable")
	}
	return f.Store.SettleMeteringIntent(ctx, id, state, lastError, attempts)
}

type meteringFixture struct {
	svc    *Service
	fault  *meteringFaultStore
	tenant model.Tenant
	key    *model.APIKey
}

func newMeteringFixture(t *testing.T) *meteringFixture {
	t.Helper()
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Metering Tenant")
	if err != nil {
		t.Fatalf("create tenant: %v", err)
	}
	user, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || user == nil {
		t.Fatalf("admin user: %+v err=%v", user, err)
	}
	fault := &meteringFaultStore{Store: svc.Store}
	svc.Store = fault
	return &meteringFixture{
		svc: svc, fault: fault, tenant: tenant,
		key: &model.APIKey{ID: "key-metering", TenantID: tenant.ID, UserID: user.ID},
	}
}

// intent is a small chat request: 120 in, 30 out, one request, a stated cost.
func (f *meteringFixture) intent(requestID string) *model.MeteringIntent {
	return &model.MeteringIntent{
		TenantID: f.tenant.ID, UserID: f.key.UserID, KeyID: f.key.ID, RequestID: requestID,
		TokensIn: 120, TokensOut: 30, Requests: 1, Model: "qwen-test", Scenario: "chat",
		EstimatedCost: 0.15,
	}
}

// books reads back what the ledgers actually hold, which is what a recovery
// claim has to be checked against — never the intent's own state.
func (f *meteringFixture) books(t *testing.T) (requests, tokensIn, costs int64) {
	t.Helper()
	ctx := context.Background()
	usage, err := f.svc.Store.SummarizeUsage(ctx, f.tenant.ID, "")
	if err != nil {
		t.Fatalf("summarize usage: %v", err)
	}
	for _, row := range usage {
		requests += row.Requests
		tokensIn += row.TokensIn
	}
	_, total, err := f.svc.Store.ListCostMetrics(ctx, f.tenant.ID, false, 1, 50, repository.CostMetricFilter{})
	if err != nil {
		t.Fatalf("list cost metrics: %v", err)
	}
	return requests, tokensIn, total
}

func (f *meteringFixture) intentRow(t *testing.T, requestID string) *model.MeteringIntent {
	t.Helper()
	row, err := f.svc.Store.GetMeteringIntent(context.Background(), requestID)
	if err != nil || row == nil {
		t.Fatalf("metering intent %s: row=%+v err=%v", requestID, row, err)
	}
	return row
}

// TestGatewayMeteringWritesBothLedgersAndSettles is the ordinary path, and it
// asserts the two ledgers *and* the intent's verdict — a `recorded` state without
// both rows would be exactly the claim E146 could not verify.
func TestGatewayMeteringWritesBothLedgersAndSettles(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()

	if state := f.svc.GatewayMetering(ctx, f.intent("req-meter-ok")); state != model.MeteringRecorded {
		t.Fatalf("state = %q, want %q", state, model.MeteringRecorded)
	}
	row := f.intentRow(t, "req-meter-ok")
	if row.State != model.MeteringRecorded || row.Attempts != 1 || row.RecordedAt == nil {
		t.Fatalf("intent row = %+v, want recorded/1 attempt/recorded_at", row)
	}
	requests, tokensIn, costs := f.books(t)
	if requests != 1 || tokensIn != 120 || costs != 1 {
		t.Fatalf("books = requests %d / tokens_in %d / cost rows %d, want 1 / 120 / 1", requests, tokensIn, costs)
	}
}

// TestGatewayMeteringFailureIsPersistedAndObservable: a failed ledger write must
// survive the request. The failure is read back from the row (state, attempts,
// last_error), not from a log line, because the log is what rotated away in E146.
func TestGatewayMeteringFailureIsPersistedAndObservable(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()
	f.fault.failDetail.Store(2) // both attempts fail

	if state := f.svc.GatewayMetering(ctx, f.intent("req-meter-fail")); state != model.MeteringFailed {
		t.Fatalf("state = %q, want %q", state, model.MeteringFailed)
	}
	row := f.intentRow(t, "req-meter-fail")
	if row.State != model.MeteringFailed {
		t.Fatalf("intent state = %q, want failed", row.State)
	}
	if row.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2 (one try plus one retry)", row.Attempts)
	}
	if !strings.Contains(row.LastError, "detail") || !strings.Contains(row.LastError, "connection reset") {
		t.Fatalf("last_error = %q, want the detail-ledger cause", row.LastError)
	}
	if got := f.fault.detailCalls.Load(); got != 2 {
		t.Fatalf("detail write attempts = %d, want 2", got)
	}
	requests, _, costs := f.books(t)
	if requests != 1 || costs != 0 {
		t.Fatalf("books = requests %d / cost rows %d, want 1 / 0 (aggregate landed, detail did not)", requests, costs)
	}
}

// TestGatewayMeteringTransientFailureRecoversOnRetry: a single blip is repaired by
// the built-in retry, so the common failure never reaches the sweep.
func TestGatewayMeteringTransientFailureRecoversOnRetry(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()
	f.fault.failDetail.Store(1)

	if state := f.svc.GatewayMetering(ctx, f.intent("req-meter-blip")); state != model.MeteringRecorded {
		t.Fatalf("state = %q, want recorded after retry", state)
	}
	row := f.intentRow(t, "req-meter-blip")
	if row.Attempts != 2 || row.State != model.MeteringRecorded {
		t.Fatalf("intent row = %+v, want recorded on attempt 2", row)
	}
	if requests, _, costs := f.books(t); requests != 1 || costs != 1 {
		t.Fatalf("books = requests %d / cost rows %d, want 1 / 1 (retry must not double count)", requests, costs)
	}
}

// TestGatewayIdempotencyCompletionRefusedWhileMeteringPending is the first of the
// two reverse tests and the one E146 was missing: when no metering verdict exists,
// the request must not be reported as completed. Leaving the entry IN_PROGRESS is
// the loud failure — the caller's retry re-executes rather than replaying a body
// whose books nobody can vouch for.
func TestGatewayIdempotencyCompletionRefusedWhileMeteringPending(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()

	begin, err := f.svc.BeginGatewayIdempotency(ctx, f.key, "idem-pending", "POST", "/api/v1/chat/completions", []byte(`{"m":1}`), "req-pending")
	if err != nil || begin.Record == nil {
		t.Fatalf("begin: %+v err=%v", begin, err)
	}
	f.fault.failSettle.Store(true)
	if state := f.svc.GatewayMetering(ctx, f.intent("req-pending")); state != model.MeteringPending {
		t.Fatalf("state = %q, want pending when the outcome cannot be settled", state)
	}
	f.fault.failSettle.Store(false)

	f.svc.CompleteGatewayIdempotency(ctx, begin, "req-pending", `{"ok":true}`)

	row, err := f.svc.Store.GetGatewayIdempotency(ctx, f.tenant.ID, f.key.UserID, "idem-pending")
	if err != nil || row == nil {
		t.Fatalf("read idempotency row: %+v err=%v", row, err)
	}
	if row.Status != model.IdempotencyInProgress {
		t.Fatalf("status = %q, want %q: a completion with no metering verdict must be refused", row.Status, model.IdempotencyInProgress)
	}
	if row.MeteringState == model.MeteringRecorded {
		t.Fatalf("metering_state = %q, but nothing was recorded", row.MeteringState)
	}
}

// TestGatewayIdempotencyCompletionRecordsFailedMetering is the other side of the
// same rule: a *known* loss may complete, but only by carrying the verdict, so
// "served and never metered" becomes a query instead of an investigation. Refusing
// here would make the caller retry re-run the model, trading a recoverable gap for
// a duplicate charge.
func TestGatewayIdempotencyCompletionRecordsFailedMetering(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()
	begin, err := f.svc.BeginGatewayIdempotency(ctx, f.key, "idem-failed", "POST", "/api/v1/chat/completions", []byte(`{"m":2}`), "req-failed")
	if err != nil || begin.Record == nil {
		t.Fatalf("begin: %+v err=%v", begin, err)
	}
	f.fault.failDetail.Store(2)
	if state := f.svc.GatewayMetering(ctx, f.intent("req-failed")); state != model.MeteringFailed {
		t.Fatalf("state = %q, want failed", state)
	}
	f.svc.CompleteGatewayIdempotency(ctx, begin, "req-failed", `{"ok":true}`)

	row, err := f.svc.Store.GetGatewayIdempotency(ctx, f.tenant.ID, f.key.UserID, "idem-failed")
	if err != nil || row == nil {
		t.Fatalf("read idempotency row: %+v err=%v", row, err)
	}
	if row.Status != model.IdempotencyCompleted {
		t.Fatalf("status = %q, want completed (the loss is recorded, not hidden)", row.Status)
	}
	if row.MeteringState != model.MeteringFailed {
		t.Fatalf("metering_state = %q, want %q", row.MeteringState, model.MeteringFailed)
	}
	if row.MeteringIntentID == "" {
		t.Fatalf("metering_intent_id is empty: the completion cannot be traced to the intent that owes the books")
	}

	// A caller retry with the same idempotency key must replay the stored body
	// rather than re-execute the model.
	replay, err := f.svc.BeginGatewayIdempotency(ctx, f.key, "idem-failed", "POST", "/api/v1/chat/completions", []byte(`{"m":2}`), "req-retry")
	if err != nil || !replay.Replayed || replay.Record.ResponseRef != `{"ok":true}` {
		t.Fatalf("replay after metering failure: %+v err=%v", replay, err)
	}
}

// TestMeteringRecoveryFillsGap is the repair half: the sweep replays the intent and
// the books are complete afterwards, with the row's attempts recording that it took
// a recovery pass.
func TestMeteringRecoveryFillsGap(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()
	f.fault.failDetail.Store(2)
	if state := f.svc.GatewayMetering(ctx, f.intent("req-recover")); state != model.MeteringFailed {
		t.Fatalf("state = %q, want failed", state)
	}
	if requests, tokensIn, costs := f.books(t); requests != 1 || tokensIn != 120 || costs != 0 {
		t.Fatalf("before recovery: requests %d / tokens_in %d / cost rows %d, want 1 / 120 / 0", requests, tokensIn, costs)
	}

	recovered, open, err := f.svc.recoverGatewayMeteringBefore(ctx, time.Now().UTC().Add(time.Hour), 100)
	if err != nil || recovered != 1 || open != 0 {
		t.Fatalf("recovery = recovered %d / open %d / err %v, want 1 / 0 / nil", recovered, open, err)
	}
	if requests, tokensIn, costs := f.books(t); requests != 1 || tokensIn != 120 || costs != 1 {
		t.Fatalf("after recovery: requests %d / tokens_in %d / cost rows %d, want 1 / 120 / 1", requests, tokensIn, costs)
	}
	row := f.intentRow(t, "req-recover")
	if row.State != model.MeteringRecorded || row.RecordedAt == nil || row.Attempts < 3 {
		t.Fatalf("intent row = %+v, want recorded with the recovery attempt counted", row)
	}
}

// TestMeteringRecoveryDoesNotDoubleCount is the second reverse test, and the one
// that makes "recover until it works" safe: replaying a settled intent must not add
// a second cost row or a second request to the aggregate, because the business key
// is the request id and the ledgers enforce it.
func TestMeteringRecoveryDoesNotDoubleCount(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()
	if state := f.svc.GatewayMetering(ctx, f.intent("req-twice")); state != model.MeteringRecorded {
		t.Fatalf("state = %q, want recorded", state)
	}
	settled := f.intentRow(t, "req-twice")

	// A second sweep pass must find nothing to do for a recorded intent.
	recovered, open, err := f.svc.recoverGatewayMeteringBefore(ctx, time.Now().UTC().Add(time.Hour), 100)
	if err != nil || recovered != 0 || open != 0 {
		t.Fatalf("second sweep = recovered %d / open %d / err %v, want 0 / 0 / nil", recovered, open, err)
	}
	// Even a forced replay of the settled intent (what a crashed sweep would look
	// like, were the state write to be lost) must be a no-op in the books.
	if state := f.svc.applyMeteringIntent(ctx, settled, false); state != model.MeteringRecorded {
		t.Fatalf("forced replay state = %q, want recorded", state)
	}
	if requests, tokensIn, costs := f.books(t); requests != 1 || tokensIn != 120 || costs != 1 {
		t.Fatalf("books after replay = requests %d / tokens_in %d / cost rows %d, want 1 / 120 / 1", requests, tokensIn, costs)
	}
}

// TestMeteringRecoveryKeepsDiagnosticsOnPersistentFailure: a ledger that stays down
// must keep the cause and the attempt count readable, and must not invent success.
func TestMeteringRecoveryKeepsDiagnosticsOnPersistentFailure(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()
	f.fault.failDetail.Store(6) // three attempt batches fail
	if state := f.svc.GatewayMetering(ctx, f.intent("req-stuck")); state != model.MeteringFailed {
		t.Fatalf("state = %q, want failed", state)
	}
	cutoff := time.Now().UTC().Add(time.Hour)
	if recovered, open, err := f.svc.recoverGatewayMeteringBefore(ctx, cutoff, 100); err != nil || recovered != 0 || open != 1 {
		t.Fatalf("sweep 1 = recovered %d / open %d / err %v, want 0 / 1 / nil", recovered, open, err)
	}
	if recovered, open, err := f.svc.recoverGatewayMeteringBefore(ctx, cutoff, 100); err != nil || recovered != 0 || open != 1 {
		t.Fatalf("sweep 2 = recovered %d / open %d / err %v, want 0 / 1 / nil", recovered, open, err)
	}
	row := f.intentRow(t, "req-stuck")
	if row.State != model.MeteringFailed || row.Attempts != 6 {
		t.Fatalf("intent row = state %q / attempts %d, want failed / 6", row.State, row.Attempts)
	}
	if !strings.Contains(row.LastError, "connection reset") {
		t.Fatalf("last_error = %q, want the retained cause", row.LastError)
	}
	if _, _, costs := f.books(t); costs != 0 {
		t.Fatalf("cost rows = %d, want 0: a persistent failure must not book anything", costs)
	}
}

// TestGatewayMeteringRejectsIntentWithoutRequestID: the idempotency key is the
// request id, so an intent without one cannot be written safely. It is refused
// rather than given a generated id, which would hide the caller's bug.
func TestGatewayMeteringRejectsIntentWithoutRequestID(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()
	intent := f.intent("")
	if state := f.svc.GatewayMetering(ctx, intent); state != model.MeteringFailed {
		t.Fatalf("state = %q, want failed", state)
	}
	if requests, _, costs := f.books(t); requests != 0 || costs != 0 {
		t.Fatalf("books = requests %d / cost rows %d, want 0 / 0", requests, costs)
	}
}

// TestGatewayMeteringHealthShowsTheGapAndItsCause is the reader test: a persisted
// failure has to be visible from the API, attributed to the request and carrying the
// cause, and a tenant must not see another tenant's gap. The empty answer is `[]`,
// not an omitted field — "nothing is owed" and "nobody looked" are different.
func TestGatewayMeteringHealthShowsTheGapAndItsCause(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()

	empty, err := f.svc.GatewayMeteringHealth(ctx, f.tenant.ID, false)
	if err != nil {
		t.Fatalf("health (empty): %v", err)
	}
	if empty.Unsettled != 0 || empty.Intents == nil || len(empty.Intents) != 0 {
		t.Fatalf("empty health = %+v, want 0 unsettled and an empty list", empty)
	}

	f.fault.failDetail.Store(2)
	if state := f.svc.GatewayMetering(ctx, f.intent("req-health")); state != model.MeteringFailed {
		t.Fatalf("state = %q, want failed", state)
	}
	health, err := f.svc.GatewayMeteringHealth(ctx, f.tenant.ID, false)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.Failed != 1 || health.Pending != 0 || health.Unsettled != 1 {
		t.Fatalf("health counts = %+v, want failed 1 / pending 0 / unsettled 1", health)
	}
	if len(health.Intents) != 1 || health.Intents[0].RequestID != "req-health" {
		t.Fatalf("health intents = %+v, want the failed request", health.Intents)
	}
	if !strings.Contains(health.Intents[0].LastError, "connection reset") {
		t.Fatalf("health intent last_error = %q, want the retained cause", health.Intents[0].LastError)
	}

	otherTenant, err := f.svc.CreateTenant(ctx, "Other Tenant")
	if err != nil {
		t.Fatalf("create other tenant: %v", err)
	}
	isolated, err := f.svc.GatewayMeteringHealth(ctx, otherTenant.ID, false)
	if err != nil {
		t.Fatalf("health (other tenant): %v", err)
	}
	if isolated.Unsettled != 0 {
		t.Fatalf("other tenant sees %d unsettled intents, want 0: the gap must be tenant-scoped", isolated.Unsettled)
	}
	all, err := f.svc.GatewayMeteringHealth(ctx, "", true)
	if err != nil {
		t.Fatalf("health (scope all): %v", err)
	}
	if all.Unsettled != 1 {
		t.Fatalf("scope=all unsettled = %d, want 1", all.Unsettled)
	}
}

// TestMeteringCompletionDecisionIsTheInvariant is the invariant E148 makes
// checkable, one row per case: a request may be marked terminal only when its books
// are written, nothing was owed, or the gap has a durable intent to replay it.
// `failed` without an intent id is the row that used to be accepted — an
// unrecoverable loss wearing the label of a recoverable one.
func TestMeteringCompletionDecisionIsTheInvariant(t *testing.T) {
	cases := []struct {
		name    string
		verdict meteringVerdict
		err     error
		allow   bool
		why     string
	}{
		{"recorded", meteringVerdict{State: model.MeteringRecorded, IntentID: "i1"}, nil, true, "books are written"},
		{"none", meteringVerdict{State: model.MeteringNone}, nil, true, "nothing was owed"},
		{"failed_with_intent", meteringVerdict{State: model.MeteringFailed, IntentID: "i2"}, nil, true, "the gap is queued for replay"},
		{"failed_without_intent", meteringVerdict{State: model.MeteringFailed}, nil, false, "nothing will replay this gap"},
		{"pending", meteringVerdict{State: model.MeteringPending, IntentID: "i3"}, nil, false, "owed and not yet written"},
		{"unwritten", meteringVerdict{State: model.MeteringNone, Unwritten: true}, nil, false, "the record of what is owed does not exist"},
		{"unreadable", meteringVerdict{}, errors.New("db down"), false, "cannot vouch for the books"},
		{"empty", meteringVerdict{}, nil, false, "no verdict at all"},
	}
	for _, c := range cases {
		allowed, _ := meteringCompletionDecision(c.verdict, c.err)
		if allowed != c.allow {
			t.Errorf("%s: allowed = %v, want %v (%s)", c.name, allowed, c.allow, c.why)
		}
	}
}

// TestGatewayIdempotencyCompletionRefusedWhenIntentCannotBeWritten is the
// unrecoverable case, and the one an in-memory `failed` verdict would have hidden:
// the intent row could not be created, so nothing names the gap and the sweep
// cannot replay it. The request must stay IN_PROGRESS instead of completing with a
// `failed` state that claims a queue nobody owns.
func TestGatewayIdempotencyCompletionRefusedWhenIntentCannotBeWritten(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()
	begin, err := f.svc.BeginGatewayIdempotency(ctx, f.key, "idem-unwritten", "POST", "/api/v1/chat/completions", []byte(`{"m":3}`), "req-unwritten")
	if err != nil || begin.Record == nil {
		t.Fatalf("begin: %+v err=%v", begin, err)
	}
	f.fault.failIntent.Store(true)
	if state := f.svc.GatewayMetering(ctx, f.intent("req-unwritten")); state != model.MeteringFailed {
		t.Fatalf("state = %q, want failed", state)
	}
	f.svc.CompleteGatewayIdempotency(ctx, begin, "req-unwritten", `{"ok":true}`)

	row, err := f.svc.Store.GetGatewayIdempotency(ctx, f.tenant.ID, f.key.UserID, "idem-unwritten")
	if err != nil || row == nil {
		t.Fatalf("read idempotency row: %+v err=%v", row, err)
	}
	if row.Status != model.IdempotencyInProgress {
		t.Fatalf("status = %q, want %q: a request with no durable intent must not complete", row.Status, model.IdempotencyInProgress)
	}
	if row.MeteringIntentID != "" {
		t.Fatalf("metering_intent_id = %q, want empty (no row exists)", row.MeteringIntentID)
	}

	// Once the database accepts the intent, the same caller key re-executes and its
	// books land: refusing the completion was recoverable, completing would not be.
	f.fault.failIntent.Store(false)
	retry, err := f.svc.BeginGatewayIdempotency(ctx, f.key, "idem-unwritten", "POST", "/api/v1/chat/completions", []byte(`{"m":3}`), "req-unwritten-2")
	if err != nil || retry.Replayed {
		t.Fatalf("retry after the refusal must re-execute, not replay: %+v err=%v", retry, err)
	}
	if state := f.svc.GatewayMetering(ctx, f.intent("req-unwritten-2")); state != model.MeteringRecorded {
		t.Fatalf("state = %q, want recorded after the database recovered", state)
	}
}

// TestGatewayIdempotencyRetryAfterRefusalReexecutesTheServedRequest pins the *price*
// of E148's refusal, and it is a price, not a free win (doc/305 §2.47.6).
//
// The two invariants cannot both be strict on this path, because the intent row is
// written *after* the provider answered (it carries the payload, and a payload that
// could be written before the answer would not need recovery at all):
//
//	P1  COMPLETED ⇒ Recorded ∨ DurableIntent      — holds, by refusing (E148)
//	P2  BusinessEffectAppliedAtMostOnce           — does NOT hold here
//
// When the intent insert fails, P1's only honest option is to leave the row
// IN_PROGRESS (nothing names a queue), and `BeginGatewayIdempotency` answers
// IN_PROGRESS with `Replayed=false`, so the caller's retry re-runs the model. The
// metering side is still at-most-once (`request_id` is the ledger key); the *model
// call* is not. This test exists so that fact is a pinned behaviour with a name,
// not a sentence in a commit message: if it starts failing, someone changed the
// trade — make that deliberate (the two candidate fixes are named in doc/305
// §2.47.6: a pre-flight claim row with `payload_ready`, or a durable `served/lost`
// mark that would let the retry replay the stored body).
func TestGatewayIdempotencyRetryAfterRefusalReexecutesTheServedRequest(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()
	// executions counts the place where the handler would call the provider: the
	// side effect the caller pays for.
	executions := 0

	begin, err := f.svc.BeginGatewayIdempotency(ctx, f.key, "idem-twice", "POST", "/api/v1/chat/completions", []byte(`{"m":3}`), "req-twice-1")
	if err != nil || begin.Record == nil || begin.Replayed {
		t.Fatalf("begin #1: %+v err=%v", begin, err)
	}
	executions++ // the first request reaches the provider and answers
	f.fault.failIntent.Store(true)
	if state := f.svc.GatewayMetering(ctx, f.intent("req-twice-1")); state != model.MeteringFailed {
		t.Fatalf("state = %q, want failed (no intent row was written)", state)
	}
	f.svc.CompleteGatewayIdempotency(ctx, begin, "req-twice-1", `{"ok":1}`)

	row, err := f.svc.Store.GetGatewayIdempotency(ctx, f.tenant.ID, f.key.UserID, "idem-twice")
	if err != nil || row == nil {
		t.Fatalf("read idempotency row: %+v err=%v", row, err)
	}
	if row.Status != model.IdempotencyInProgress {
		t.Fatalf("status = %q, want IN_PROGRESS: the refusal is what keeps P1 true", row.Status)
	}
	if row.ResponseRef != "" {
		t.Fatalf("response_ref = %q, want empty: the refusal must not store a body as if completion were pending", row.ResponseRef)
	}

	// The retry is a new request id against the same caller key. Because the row is
	// IN_PROGRESS it is not a replay: the business effect runs a second time.
	retry, err := f.svc.BeginGatewayIdempotency(ctx, f.key, "idem-twice", "POST", "/api/v1/chat/completions", []byte(`{"m":3}`), "req-twice-2")
	if err != nil || retry.Record == nil {
		t.Fatalf("begin #2: %+v err=%v", retry, err)
	}
	if retry.Replayed {
		t.Fatalf("retry replayed: P2 would suddenly hold, but nothing changed to make it hold")
	}
	executions++
	if executions != 2 {
		t.Fatalf("provider executions = %d, want 2 — this test pins the trade, not a bug", executions)
	}

	// And the books for both requests are distinct and idempotent per request id, so
	// the extra execution cannot double-count: that half of P2 does hold.
	f.fault.failIntent.Store(false)
	if state := f.svc.GatewayMetering(ctx, f.intent("req-twice-2")); state != model.MeteringRecorded {
		t.Fatalf("state = %q, want recorded for the retried request", state)
	}
	if requests, _, costs := f.books(t); requests < 1 || costs < 1 {
		t.Fatalf("books after the retry: requests %d / cost rows %d, want both >= 1", requests, costs)
	}
}

// TestCompletedRequestWithNothingOwedRecordsNone closes the loop the reconciler
// fix (E149) depends on: a request that never reached the metering point (an
// upstream 4xx/5xx body, an IM channel) completes *and* says `none` on the row.
// Without this half, "completed with metering_state=none is not a finding" would
// be a predicate no production row could ever satisfy, and the 14 "missing_meter"
// findings of 2026-11-23 would come back under a new name.
func TestCompletedRequestWithNothingOwedRecordsNone(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()
	begin, err := f.svc.BeginGatewayIdempotency(ctx, f.key, "idem-none", "POST", "/api/v1/chat/completions", []byte(`{"m":3}`), "req-none")
	if err != nil || begin.Record == nil {
		t.Fatalf("begin: %+v err=%v", begin, err)
	}
	// No GatewayMetering call: the provider answered 429, nothing was served.
	f.svc.CompleteGatewayIdempotency(ctx, begin, "req-none", `{"error":{"message":"Rate limit exceeded"}}`)

	row, err := f.svc.Store.GetGatewayIdempotency(ctx, f.tenant.ID, f.key.UserID, "idem-none")
	if err != nil || row == nil {
		t.Fatalf("read idempotency row: %+v err=%v", row, err)
	}
	if row.Status != model.IdempotencyCompleted {
		t.Fatalf("status = %q, want COMPLETED: nothing was owed, so nothing blocks completion", row.Status)
	}
	if row.MeteringState != model.MeteringNone {
		t.Fatalf("metering_state = %q, want %q: the reconciler reads this column to tell \"owed\" from \"nothing owed\"", row.MeteringState, model.MeteringNone)
	}
	if row.ResponseRef != `{"error":{"message":"Rate limit exceeded"}}` {
		t.Fatalf("response_ref = %q: the served body must still be replayable", row.ResponseRef)
	}
}

// TestGatewayIdempotencySecondExecutionCannotOverwriteTheFirst pins the *other* way
// the business effect can happen twice, which no refusal is involved in: the claim
// on an idempotency key is a unique index, not a lease. Two requests in flight with
// the same key both proceed (Replayed=false), both reach the provider, and both are
// metered under their own request_id. What the store does guarantee is that the
// second execution cannot take the row: completion is scoped to
// (id, request_id, status=in_progress), so the loser's write updates 0 rows and the
// row keeps the first execution's body. The cost of having no lease is a duplicate
// business effect; the cost of not scoping the update would be a replayed body that
// belongs to a different request.
func TestGatewayIdempotencySecondExecutionCannotOverwriteTheFirst(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()
	body := []byte(`{"n":2}`)

	first, err := f.svc.BeginGatewayIdempotency(ctx, f.key, "idem-both", "POST", "/api/v1/chat/completions", body, "req-both-1")
	if err != nil || first.Record == nil || first.Replayed {
		t.Fatalf("first begin: %+v err=%v", first, err)
	}
	second, err := f.svc.BeginGatewayIdempotency(ctx, f.key, "idem-both", "POST", "/api/v1/chat/completions", body, "req-both-2")
	if err != nil || second.Record == nil {
		t.Fatalf("second begin: %+v err=%v", second, err)
	}
	if second.Replayed {
		t.Fatalf("a second in-flight request is NOT a replay: there is no lease, only a unique index")
	}
	if second.Record.ID != first.Record.ID {
		t.Fatalf("both executions must point at the same row: %q vs %q", second.Record.ID, first.Record.ID)
	}

	// The loser finishes first (it got there second) and must not take the row.
	f.svc.CompleteGatewayIdempotency(ctx, second, "req-both-2", `{"body":"second"}`)
	row, err := f.svc.Store.GetGatewayIdempotency(ctx, f.tenant.ID, f.key.UserID, "idem-both")
	if err != nil || row == nil {
		t.Fatalf("read after loser completion: %+v err=%v", row, err)
	}
	if row.Status != model.IdempotencyInProgress || row.ResponseRef != "" {
		t.Fatalf("the second execution stole the row: status=%q response_ref=%q", row.Status, row.ResponseRef)
	}

	// The winner still completes, and the replayable body is its own.
	f.svc.CompleteGatewayIdempotency(ctx, first, "req-both-1", `{"body":"first"}`)
	row, err = f.svc.Store.GetGatewayIdempotency(ctx, f.tenant.ID, f.key.UserID, "idem-both")
	if err != nil || row == nil {
		t.Fatalf("read after winner completion: %+v err=%v", row, err)
	}
	if row.Status != model.IdempotencyCompleted || row.ResponseRef != `{"body":"first"}` {
		t.Fatalf("completed row = %q/%q, want COMPLETED and the first body", row.Status, row.ResponseRef)
	}
}

// TestMeteringRecoveryFromAnotherProcessDoesNotDoubleCount is the crash test: the
// process dies after both ledgers landed but before the intent was settled, and a
// fresh Service over the same database replays the row. The replay must settle the
// intent without adding a second request to the aggregate or a second cost row,
// because the sweep carries no memory of the first attempt — only the database.
func TestMeteringRecoveryFromAnotherProcessDoesNotDoubleCount(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()
	f.fault.failSettle.Store(true)
	if state := f.svc.GatewayMetering(ctx, f.intent("req-crash")); state != model.MeteringPending {
		t.Fatalf("state = %q, want pending when the settle fails", state)
	}
	f.fault.failSettle.Store(false)
	// Both ledgers landed before the "crash" — that is what makes the replay a
	// no-op rather than a repair.
	if requests, _, costs := f.books(t); requests != 1 || costs != 1 {
		t.Fatalf("before replay: requests %d / cost rows %d, want 1 / 1", requests, costs)
	}

	// A new process: no in-memory state from the request that wrote the ledgers.
	restarted := &Service{Store: f.svc.Store}
	recovered, open, err := restarted.recoverGatewayMeteringBefore(ctx, time.Now().UTC().Add(time.Hour), 100)
	if err != nil || recovered != 1 || open != 0 {
		t.Fatalf("replay from a fresh process = recovered %d / open %d / err %v, want 1 / 0 / nil", recovered, open, err)
	}
	row := f.intentRow(t, "req-crash")
	if row.State != model.MeteringRecorded || row.RecordedAt == nil {
		t.Fatalf("intent row after replay = %+v, want recorded", row)
	}
	if requests, tokensIn, costs := f.books(t); requests != 1 || tokensIn != 120 || costs != 1 {
		t.Fatalf("books after replay = requests %d / tokens_in %d / cost rows %d, want 1 / 120 / 1", requests, tokensIn, costs)
	}
}

// TestMeteringSweepReportsUnknownInsteadOfZero: when the per-tenant gap query
// fails, the sweep must fail loudly and publish nothing. A sweep that published an
// empty map would say "every tenant is clean" precisely because it could not read
// the answer, and the liveness stamp would hide the failure as well.
func TestMeteringSweepReportsUnknownInsteadOfZero(t *testing.T) {
	f := newMeteringFixture(t)
	ctx := context.Background()
	f.fault.failDetail.Store(2)
	if state := f.svc.GatewayMetering(ctx, f.intent("req-unknown")); state != model.MeteringFailed {
		t.Fatalf("state = %q, want failed", state)
	}

	var published []map[string]int64
	meteringPublishHook = func(m map[string]int64) { published = append(published, m) }
	defer func() { meteringPublishHook = nil }()

	cutoff := time.Now().UTC().Add(time.Hour)
	f.fault.failCount.Store(true)
	if _, _, err := f.svc.recoverGatewayMeteringBefore(ctx, cutoff, 100); err == nil {
		t.Fatalf("sweep with an unreadable state query returned no error: the failure has to be visible")
	}
	if len(published) != 0 {
		t.Fatalf("sweep published %v while it could not read the gap: unknown must not be reported as zero", published)
	}

	// The replay did land, so the same pass that could not publish still repaired
	// the books — the two halves are independent on purpose.
	row := f.intentRow(t, "req-unknown")
	if row.State != model.MeteringRecorded {
		t.Fatalf("intent state = %q, want recorded: recovery must not depend on the reading", row.State)
	}

	f.fault.failCount.Store(false)
	if _, _, err := f.svc.recoverGatewayMeteringBefore(ctx, cutoff, 100); err != nil {
		t.Fatalf("sweep after the query recovered: %v", err)
	}
	if len(published) != 1 {
		t.Fatalf("published %d times, want 1 after the query recovered: %v", len(published), published)
	}
	// The repaired tenant is *absent*, not present at 0: the gap is a state and a
	// tenant with nothing owed has no series, which is exactly what keeps
	// `unsettled_intents > 0` from paging on history. That absence is only readable
	// next to a fresh `sweep_last_success_timestamp_seconds` — which this pass set.
	if len(published[0]) != 0 {
		t.Fatalf("published gap = %v, want no series after the repair", published[0])
	}
}
