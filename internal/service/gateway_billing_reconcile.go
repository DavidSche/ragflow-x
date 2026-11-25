package service

import (
	"context"
	"fmt"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// Gateway billing reconciliation (doc/301 §5.10 GW-3 / T21).
//
// One served API request touches four ledgers. This file compares them and nothing
// else: the arithmetic is a pure function over a snapshot (`reconcileGatewayBilling`)
// so it can be reasoned about — and tested — without a database, a clock or a
// goroutine. The service wrapper only decides *which* day and *which* tenant, and
// writes the result down.

const (
	// gatewayBillingStaleReservationGrace is how long a reservation may stay pending
	// before it counts as leaked. It is much longer than the on-demand reaper's own
	// interval (one minute) on purpose: the reaper only runs when a key is next used,
	// so a key that went quiet keeps its pending row forever — and *that* is the
	// condition worth a daily finding, not a request that was in flight a minute ago.
	gatewayBillingStaleReservationGrace = time.Hour

	// gatewayBillingDriftToleranceTokens absorbs rounding in the settle path: an
	// estimate is replaced by the provider's actual, and a bucket that was reaped
	// mid-flight can legitimately differ by a token or two. Anything larger is a real
	// disagreement between the settlement ledger and the key counter.
	gatewayBillingDriftToleranceTokens = int64(2)
)

// gatewayBillingOutcome is the pure result of comparing one snapshot.
type gatewayBillingOutcome struct {
	Checked              int
	Findings             int
	Counts               map[string]int
	QuotaDriftBuckets    int
	QuotaExpectedTokens  int64
	QuotaActualTokens    int64
	UsageMeteredRequests int64
	UsageBilledRequests  int64
	Rows                 []model.GatewayBillingReconcileFinding
}

// reconcileGatewayBilling compares the four ledgers of one tenant-day.
//
// The denominator is `Checked`: every idempotency row in the window plus every leaked
// reservation plus every bucket examined. That is the population the check is *about*
// — a ratio over "requests we happened to look at" would be a different, flattering
// number.
func reconcileGatewayBilling(snapshot *model.GatewayBillingSnapshot) gatewayBillingOutcome {
	out := gatewayBillingOutcome{Counts: map[string]int{}}
	if snapshot == nil {
		return out
	}

	metered := make(map[string]struct{}, len(snapshot.Metered))
	for _, id := range snapshot.Metered {
		metered[id] = struct{}{}
	}
	charged := make(map[string]struct{}, len(snapshot.Charged))
	for _, ref := range snapshot.Charged {
		charged[ref.RequestID] = struct{}{}
	}
	duplicated := make(map[string]struct{}, len(snapshot.DuplicateRequests))
	for _, id := range snapshot.DuplicateRequests {
		duplicated[id] = struct{}{}
	}

	add := func(kind, requestID, keyID, detail string) {
		out.Counts[kind]++
		out.Rows = append(out.Rows, model.GatewayBillingReconcileFinding{
			Kind: kind, RequestID: requestID, KeyID: keyID, Detail: detail,
		})
	}

	seenDuplicate := map[string]struct{}{}
	for _, ref := range snapshot.Idempotency {
		out.Checked++
		switch ref.Status {
		case model.IdempotencyCompleted:
			// Served. What "served" owes depends on the metering state, and that
			// dependency is the E146 correction: `COMPLETED` used to carry "request
			// finished, response produced, books written" at once, so the reconciler
			// read it as "books must exist" and reported every completed request that
			// owed nothing as a hole in the books.
			switch ref.MeteringState {
			case model.MeteringNone:
				// Nothing was owed (an upstream 4xx/5xx body, an IM channel). The
				// missing stamp is the answer, not a finding — and the opposite
				// direction is still checked: a cost row for a request that owed
				// nothing is a charge nobody can justify.
				if _, ok := charged[ref.RequestID]; ok {
					add(model.GatewayBillingFindingChargedAfterFailure, ref.RequestID, ref.KeyID,
						"completed request with metering_state=none carries a cost row")
				}
				continue
			}
			if _, ok := metered[ref.RequestID]; !ok {
				add(model.GatewayBillingFindingMissingMeter, ref.RequestID, ref.KeyID,
					fmt.Sprintf("completed request has no metering stamp (metering_state=%s)",
						meteringStateOrNotTracked(ref.MeteringState)))
				continue
			}
			if _, ok := charged[ref.RequestID]; !ok {
				add(model.GatewayBillingFindingMissingCost, ref.RequestID, ref.KeyID,
					"metered request has no cost row")
			}
		case model.IdempotencyFailed:
			// Failed. It must not be charged at all.
			if _, ok := charged[ref.RequestID]; ok {
				add(model.GatewayBillingFindingChargedAfterFailure, ref.RequestID, "",
					"failed request carries a cost row")
			}
		}
	}

	// One request id on two retry rows means two caller keys were spent on one call.
	// Reported once per request id, not once per row, so the count is the number of
	// doubly-billed requests.
	for _, id := range snapshot.DuplicateRequests {
		if _, ok := seenDuplicate[id]; ok {
			continue
		}
		seenDuplicate[id] = struct{}{}
		out.Checked++
		add(model.GatewayBillingFindingDuplicateRequest, id, "", "request id appears on more than one retry row")
	}

	for _, ref := range snapshot.StaleReservations {
		out.Checked++
		add(model.GatewayBillingFindingStaleReservation, ref.RequestID, ref.KeyID,
			fmt.Sprintf("pending for more than %s (estimated %d tokens, period %s)",
				gatewayBillingStaleReservationGrace, ref.Estimated, ref.PeriodStart))
	}

	for _, bucket := range snapshot.Buckets {
		out.Checked++
		out.QuotaExpectedTokens += bucket.ExpectedTokens
		out.QuotaActualTokens += bucket.ActualTokens
		drift := bucket.ExpectedTokens - bucket.ActualTokens
		if drift < 0 {
			drift = -drift
		}
		if drift <= gatewayBillingDriftToleranceTokens {
			continue
		}
		out.QuotaDriftBuckets++
		add(model.GatewayBillingFindingQuotaDrift, "", bucket.KeyID,
			fmt.Sprintf("period %s settled %d tokens, counter says %d",
				bucket.PeriodStart, bucket.ExpectedTokens, bucket.ActualTokens))
	}

	// The daily aggregate cannot be compared per request (it has no request ids), so
	// it is compared as one count on each side. Symmetric by construction: every path
	// that inserts a metering stamp also bills exactly one request.
	out.UsageMeteredRequests = int64(len(snapshot.Metered))
	out.UsageBilledRequests = snapshot.UsageRequests
	out.Checked++
	if out.UsageMeteredRequests != out.UsageBilledRequests {
		add(model.GatewayBillingFindingUsageDrift, "", "",
			fmt.Sprintf("metering ledger has %d requests for the day, usage aggregate billed %d",
				out.UsageMeteredRequests, out.UsageBilledRequests))
	}

	out.Findings = len(out.Rows)
	return out
}

// gatewayBillingReconcileDay is the day a run reconciles when the caller does not
// name one: the previous completed UTC day. A day that is still running would report
// `missing_cost` for every request whose cost row lands a moment after its meter row —
// findings manufactured by the clock rather than by a defect.
func gatewayBillingReconcileDay(now time.Time) string {
	return now.UTC().AddDate(0, 0, -1).Format("2006-01-02")
}

// meteringStateOrNotTracked names the absence. A row with no state is not "fine",
// it is "cannot tell" — the pre-E148 history — and the finding's detail must say
// which of the two absences it is (doc/305 §2.45: three kinds of empty).
func meteringStateOrNotTracked(state string) string {
	if state == "" {
		return model.MeteringNotTracked
	}
	return state
}

// gatewayBillingDayWindow returns the half-open UTC window of one day string.
func gatewayBillingDayWindow(day string) (time.Time, time.Time, error) {
	start, err := time.ParseInLocation("2006-01-02", day, time.UTC)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return start, start.AddDate(0, 0, 1), nil
}

// ReconcileGatewayBillingDay reconciles one tenant-day and records the result.
//
// Idempotent by (tenant, day): the unique index decides, and a second call returns
// the stored run untouched. That is what makes the daily job replay-safe — a crash
// after the write and a retry cannot double-count findings.
func (s *Service) ReconcileGatewayBillingDay(ctx context.Context, tenantID, day string) (*model.GatewayBillingReconcileRun, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	if day == "" {
		day = gatewayBillingReconcileDay(time.Now())
	}
	from, to, err := gatewayBillingDayWindow(day)
	if err != nil {
		return nil, fmt.Errorf("invalid reconciliation day %q: %w", day, err)
	}

	run := &model.GatewayBillingReconcileRun{
		ID:        id.New(),
		TenantID:  tenantID,
		Day:       day,
		Status:    model.GatewayBillingReconcileRunning,
		StartedAt: time.Now().UTC(),
		CreatedAt: time.Now().UTC(),
	}
	created, err := s.Store.CreateGatewayBillingReconcileRunIfAbsent(ctx, run)
	if err != nil {
		return nil, err
	}
	if !created {
		return s.Store.GetGatewayBillingReconcileRunByDay(ctx, tenantID, day)
	}

	snapshot, err := s.Store.GatewayBillingSnapshot(ctx, tenantID, day, from, to,
		time.Now().UTC().Add(-gatewayBillingStaleReservationGrace))
	if err != nil {
		s.failGatewayBillingRun(ctx, run, err)
		return nil, err
	}

	outcome := reconcileGatewayBilling(snapshot)
	for i := range outcome.Rows {
		outcome.Rows[i].ID = id.New()
		outcome.Rows[i].TenantID = tenantID
		outcome.Rows[i].RunID = run.ID
		outcome.Rows[i].Day = day
		outcome.Rows[i].CreatedAt = time.Now().UTC()
	}
	if err := s.Store.InsertGatewayBillingReconcileFindings(ctx, outcome.Rows); err != nil {
		s.failGatewayBillingRun(ctx, run, err)
		return nil, err
	}

	finished := time.Now().UTC()
	run.Status = model.GatewayBillingReconcileCompleted
	run.Checked = outcome.Checked
	run.Findings = outcome.Findings
	run.MissingMeter = outcome.Counts[model.GatewayBillingFindingMissingMeter]
	run.MissingCost = outcome.Counts[model.GatewayBillingFindingMissingCost]
	run.ChargedAfterFailure = outcome.Counts[model.GatewayBillingFindingChargedAfterFailure]
	run.DuplicateRequest = outcome.Counts[model.GatewayBillingFindingDuplicateRequest]
	run.StaleReservation = outcome.Counts[model.GatewayBillingFindingStaleReservation]
	run.QuotaDriftBuckets = outcome.QuotaDriftBuckets
	run.UsageDrift = outcome.Counts[model.GatewayBillingFindingUsageDrift]
	run.QuotaExpectedTokens = outcome.QuotaExpectedTokens
	run.QuotaActualTokens = outcome.QuotaActualTokens
	run.UsageMeteredRequests = outcome.UsageMeteredRequests
	run.UsageBilledRequests = outcome.UsageBilledRequests
	run.FinishedAt = &finished
	if err := s.Store.FinishGatewayBillingReconcileRun(ctx, run); err != nil {
		return nil, err
	}
	s.recordGatewayBillingOutcome(run)
	return run, nil
}

// failGatewayBillingRun records a failed run. A failure is a status, not a swallowed
// error: an operator has to see that a day went unreconciled, because silence would
// read as "nothing disagreed".
func (s *Service) failGatewayBillingRun(ctx context.Context, run *model.GatewayBillingReconcileRun, cause error) {
	finished := time.Now().UTC()
	run.Status = model.GatewayBillingReconcileFailed
	run.FailureReason = cause.Error()
	run.FinishedAt = &finished
	if err := s.Store.FinishGatewayBillingReconcileRun(ctx, run); err != nil {
		logger.Warn("gateway billing reconcile failure could not be recorded",
			"tenant_id", run.TenantID, "day", run.Day, "error", err)
	}
	s.recordGatewayBillingOutcome(run)
}

// ReconcileGatewayBillingDayAll walks the tenants with gateway activity in the day and
// reconciles each. The sweep walks tenants rather than a registered list: a tenant that
// hit the gateway must be reconciled whether or not anything enumerates it.
func (s *Service) ReconcileGatewayBillingDayAll(ctx context.Context, day string) (int, error) {
	if day == "" {
		day = gatewayBillingReconcileDay(time.Now())
	}
	from, to, err := gatewayBillingDayWindow(day)
	if err != nil {
		return 0, err
	}
	tenants, err := s.Store.GatewayBillingReconcileTenants(ctx, from, to)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, tenantID := range tenants {
		if _, err := s.ReconcileGatewayBillingDay(ctx, tenantID, day); err != nil {
			logger.Warn("gateway billing reconcile failed",
				"tenant_id", tenantID, "day", day, "error", err)
			continue
		}
		done++
	}
	return done, nil
}

// recordGatewayBillingOutcome publishes one run. Metrics are best-effort by design:
// a metrics pipeline that is switched off (or absent in tests) must never turn a
// completed reconciliation into a failed one.
func (s *Service) recordGatewayBillingOutcome(run *model.GatewayBillingReconcileRun) {
	observer := obs.Get()
	if observer == nil || run == nil {
		return
	}
	counts := map[string]int{
		model.GatewayBillingFindingMissingMeter:        run.MissingMeter,
		model.GatewayBillingFindingMissingCost:         run.MissingCost,
		model.GatewayBillingFindingChargedAfterFailure: run.ChargedAfterFailure,
		model.GatewayBillingFindingDuplicateRequest:    run.DuplicateRequest,
		model.GatewayBillingFindingStaleReservation:    run.StaleReservation,
		model.GatewayBillingFindingQuotaDrift:          run.QuotaDriftBuckets,
		model.GatewayBillingFindingUsageDrift:          run.UsageDrift,
	}
	observer.RecordGatewayBillingReconcile(run.TenantID, run.Status, run.Checked, counts,
		run.QuotaExpectedTokens, run.QuotaActualTokens)
}

// gatewayBillingReconcileInterval is the sweep cadence: daily, because the check
// reconciles one completed UTC day at a time (doc/301 §5.10 GW-3).
const gatewayBillingReconcileInterval = 24 * time.Hour

// ScheduleGatewayBillingReconcile enqueues the next daily reconciliation and is
// itself idempotent: an already queued or running sweep means the cadence is
// healthy, so a second trigger is dropped rather than queued behind the first.
func (s *Service) ScheduleGatewayBillingReconcile(ctx context.Context, excludeID string) (bool, error) {
	if s.Runner == nil {
		return false, fmt.Errorf("async worker not initialized")
	}
	active, err := s.Store.CountActiveJobsExcept(
		ctx, model.JobKindGatewayBillingReconcile, SystemTenantID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	runAfter := time.Now().UTC().Add(gatewayBillingReconcileInterval)
	return s.Runner.Enqueue(ctx, model.JobKindGatewayBillingReconcile,
		"gateway_billing_reconcile:"+time.Now().UTC().Format("20060102T150405Z"),
		SystemTenantID, "", runAfter, 0)
}

// gatewayBillingReconcileWorker runs one daily check then re-arms itself.
type gatewayBillingReconcileWorker struct{ svc *Service }

func (w *gatewayBillingReconcileWorker) Kind() string {
	return model.JobKindGatewayBillingReconcile
}

func (w *gatewayBillingReconcileWorker) Run(ctx context.Context, job *model.Job) error {
	day := gatewayBillingReconcileDay(time.Now())
	done, err := w.svc.ReconcileGatewayBillingDayAll(ctx, day)
	if err != nil {
		logger.Warn("gateway billing reconcile sweep failed", "day", day, "error", err)
		return err
	}
	logger.Info("gateway billing reconcile sweep finished", "day", day, "tenants", done)
	if _, err := w.svc.ScheduleGatewayBillingReconcile(ctx, job.ID); err != nil {
		logger.Warn("gateway billing reconcile reschedule failed", "job_id", job.ID, "error", err)
		return err
	}
	return nil
}

// TenantGatewayBillingReconcileRuns lists the caller's tenant's recent daily runs.
//
// The drill reads this: `checked` next to `findings` on every row means a reader can
// tell "nothing disagreed" from "nothing was examined", which a findings count alone
// can never say.
func (s *Service) TenantGatewayBillingReconcileRuns(ctx context.Context, tenantID string, limit int) ([]model.GatewayBillingReconcileRun, error) {
	if tenantID == "" {
		return nil, httperr.BadRequest(40001, "tenant is required")
	}
	return s.Store.ListGatewayBillingReconcileRuns(ctx, tenantID, limit)
}

// GatewayBillingReconcileFindings pages one run's disagreements.
func (s *Service) GatewayBillingReconcileFindings(ctx context.Context, tenantID, runID, kind string, limit int) ([]model.GatewayBillingReconcileFinding, error) {
	if tenantID == "" || runID == "" {
		return nil, httperr.BadRequest(40001, "tenant and run id are required")
	}
	if kind != "" && !model.IsGatewayBillingFindingKind(kind) {
		// A closed set means an unknown value is a client error, not an empty page:
		// an empty page would read as "that kind never happens here".
		return nil, httperr.BadRequest(40096, "unknown reconciliation finding kind")
	}
	return s.Store.ListGatewayBillingReconcileFindings(ctx, tenantID, runID, kind, limit)
}

// ReconcileGatewayBillingAllDay is the on-demand form of the daily sweep, for the
// 7-day drill and for re-running a day whose run failed. Idempotent per tenant-day,
// so calling it twice on a finished day returns the stored runs instead of new ones.
func (s *Service) ReconcileGatewayBillingAllDay(ctx context.Context, day string) (string, int, error) {
	if day == "" {
		day = gatewayBillingReconcileDay(time.Now())
	}
	if _, _, err := gatewayBillingDayWindow(day); err != nil {
		return day, 0, httperr.BadRequest(40095, "invalid day, expected YYYY-MM-DD")
	}
	done, err := s.ReconcileGatewayBillingDayAll(ctx, day)
	return day, done, err
}
