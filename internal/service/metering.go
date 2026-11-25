package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

const (
	// meteringWriteTimeout bounds one attempt batch. The write runs on a context
	// detached from the request, because a caller that hangs up mid-stream must
	// not be able to cancel the books for the tokens it already received.
	meteringWriteTimeout = 10 * time.Second
	// meteringWriteAttempts is one try plus one retry: transient blips are the
	// common failure and retrying immediately costs a few milliseconds, while a
	// longer chain would hold the response path open for a broken database.
	meteringWriteAttempts = 2
	// meteringRecoveryGrace keeps the sweep away from in-flight requests: a row
	// younger than this may still be settled by the request that created it.
	meteringRecoveryGrace = 60 * time.Second
	// meteringRecoveryInterval is how often the sweep replays unfinished intents.
	meteringRecoveryInterval = 5 * time.Minute
	meteringRecoveryBatch    = 100
)

// GatewayMetering is the single path a served gateway request's books are
// written through, and it answers the question doc/304 §4 E146 could not answer
// after the fact: did this request's metering land?
//
// It writes the intent first (doc/304 §4 E147). That ordering is the whole point:
// a crash or a failing ledger leaves a `pending`/`failed` row with the payload
// attached, so the gap is a *record* that the recovery sweep can replay, instead
// of a `logger.Warn` line that rotates away. The ledgers themselves remain the
// only record of what was booked — the intent never substitutes for them.
//
// The replay is safe because `rgx_meter_request.request_id` is the primary key of
// the idempotency ledger: writing the same request twice cannot double-count, so
// "recover until it works" and "never charge twice" are not in tension.
//
// The returned state is the verdict the completion path requires. `failed` still
// means the request may complete — the loss is known and queued for recovery, and
// refusing the completion would make a caller retry re-run the model, trading a
// recoverable gap for a duplicate. `pending` means no verdict exists yet, and that
// is what must not complete.
func (s *Service) GatewayMetering(ctx context.Context, intent *model.MeteringIntent) string {
	if intent == nil {
		return model.MeteringNone
	}
	if intent.TenantID == "" || intent.RequestID == "" {
		// The tenant attributes the books and the request id makes the write
		// idempotent; without either the ledgers cannot be trusted. An empty user
		// is tolerated (search apps may be owned by no one) because it costs a
		// column, not the key — but a missing request id is refused rather than
		// generated here, since inventing one would hide the caller's bug.
		logger.Error("gateway metering was handed an intent with no tenant or request id",
			"tenant_id", intent.TenantID, "user_id", intent.UserID, "scenario", intent.Scenario)
		obs.Get().IncMeteringWriteFailure(intent.TenantID, "intent")
		return model.MeteringFailed
	}

	now := time.Now().UTC()
	if intent.ID == "" {
		intent.ID = id.New()
	}
	if intent.Date == "" {
		intent.Date = now.Format("2006-01-02")
	}
	if intent.Requests == 0 {
		intent.Requests = 1
	}
	if intent.EstimationPolicyVersion == "" {
		intent.EstimationPolicyVersion = "v1"
	}
	intent.State = model.MeteringPending
	intent.CreatedAt, intent.UpdatedAt = now, now

	prepared, err := s.Store.CreateMeteringIntentIfAbsent(ctx, intent)
	if err != nil {
		// There is no durable row to recover from, so this is the one case that
		// cannot be repaired later. It is loud on purpose: the ledgers may or may
		// not have been written, and only the request id can tell.
		logger.Error("gateway metering intent could not be persisted",
			"tenant_id", intent.TenantID, "request_id", intent.RequestID, "scenario", intent.Scenario, "error", err)
		// Nothing durable names this gap, so the completion path must refuse to call
		// the request finished (doc/304 §4 E148): a `failed` verdict with no intent
		// row would claim "known loss, queued for replay" when only the first half is
		// true.
		s.markMeteringUnwritten(intent.RequestID)
		obs.Get().IncMeteringWriteFailure(intent.TenantID, "intent")
		notify.Emit(ctx, notify.Event{
			Title: "gateway metering intent could not be persisted", Severity: "error",
			TenantID: intent.TenantID, Resource: "usage", Type: "metering_failed", ResourceID: intent.RequestID,
			Detail: fmt.Sprintf("request %s (%s) reached metering and the intent row could not be written: %v. Nothing will recover this request automatically; the daily reconciliation will report it as missing_meter.",
				intent.RequestID, intent.Scenario, err),
		})
		return model.MeteringFailed
	}
	if !prepared {
		existing, lookupErr := s.Store.GetMeteringIntent(ctx, intent.RequestID)
		if lookupErr != nil || existing == nil {
			logger.Error("gateway metering intent exists but could not be read",
				"tenant_id", intent.TenantID, "request_id", intent.RequestID, "error", lookupErr)
			s.markMeteringUnwritten(intent.RequestID)
			obs.Get().IncMeteringWriteFailure(intent.TenantID, "intent")
			return model.MeteringFailed
		}
		if existing.State == model.MeteringRecorded {
			// A replay of an already-metred request: answer from the row instead of
			// re-deriving the payload from configuration that may have moved.
			return model.MeteringRecorded
		}
		intent = existing
	}
	return s.applyMeteringIntent(ctx, intent, true)
}

// applyMeteringIntent writes both ledgers, retrying once, and settles the row.
// `announce` is false on the recovery sweep: a database that has been down for an
// hour must not page once per five-minute pass with the same cause. There the row's
// attempts/last_error and the unsettled gauge are the reading, and the sweep logs
// its own summary.
func (s *Service) applyMeteringIntent(ctx context.Context, intent *model.MeteringIntent, announce bool) string {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), meteringWriteTimeout)
	defer cancel()

	attempts := 0
	var applyErr error
	for attempt := 0; attempt < meteringWriteAttempts; attempt++ {
		attempts++
		if applyErr = s.applyMeteringLedgers(writeCtx, intent); applyErr == nil {
			break
		}
	}

	state, lastError := model.MeteringRecorded, ""
	if applyErr != nil {
		state, lastError = model.MeteringFailed, applyErr.Error()
	}
	if settleErr := s.Store.SettleMeteringIntent(writeCtx, intent.ID, state, lastError, attempts); settleErr != nil {
		// The ledgers may have landed while the settle did not. Leaving the row
		// unsettled is the safe side: the replay is idempotent, so recovery settles
		// it after re-applying a write that changes nothing.
		logger.Error("gateway metering outcome could not be settled",
			"tenant_id", intent.TenantID, "request_id", intent.RequestID, "outcome", state, "error", settleErr)
		return model.MeteringPending
	}
	if applyErr != nil && announce {
		s.announceMeteringFailure(ctx, intent, applyErr)
	}
	return state
}

// applyMeteringLedgers writes the daily aggregate and the per-request cost row.
// Each write is idempotent by request_id, and the aggregate's increment is guarded
// by the idempotency ledger inside the same transaction (RecordUsage), so a partial
// batch is replayable: the ledger that already landed is a no-op next time.
func (s *Service) applyMeteringLedgers(ctx context.Context, intent *model.MeteringIntent) error {
	if _, err := s.Store.RecordUsage(ctx, intent.UsageRecord()); err != nil {
		return fmt.Errorf("aggregate: %w", err)
	}
	if _, err := s.Store.RecordCostMetric(ctx, intent.CostRecord()); err != nil {
		return fmt.Errorf("detail: %w", err)
	}
	return nil
}

func (s *Service) announceMeteringFailure(ctx context.Context, intent *model.MeteringIntent, cause error) {
	ledger := "aggregate"
	if strings.HasPrefix(cause.Error(), "detail:") {
		ledger = "detail"
	}
	logger.Error("gateway metering write failed",
		"tenant_id", intent.TenantID, "user_id", intent.UserID, "request_id", intent.RequestID,
		"scenario", intent.Scenario, "ledger", ledger, "attempts", intent.Attempts+1, "error", cause)
	obs.Get().IncMeteringWriteFailure(intent.TenantID, ledger)
	notify.Emit(ctx, notify.Event{
		Title: "gateway metering write failed", Severity: "error",
		TenantID: intent.TenantID, Resource: "usage", Type: "metering_failed", ResourceID: intent.RequestID,
		Detail: fmt.Sprintf("request %s (%s) was served but its %s ledger write failed after %d attempt(s): %v. It is queued for the metering recovery sweep; recover it before trusting the day's cost report.",
			intent.RequestID, intent.Scenario, ledger, meteringWriteAttempts, cause),
	})
}

// RecoverGatewayMetering replays the intents whose ledgers never landed and
// reports how many were repaired. It is safe to run concurrently with live
// traffic: rows younger than the grace window are skipped, and the replay itself
// is idempotent by request_id.
func (s *Service) RecoverGatewayMetering(ctx context.Context, limit int) (int, int, error) {
	return s.recoverGatewayMeteringBefore(ctx, time.Now().UTC().Add(-meteringRecoveryGrace), limit)
}

// recoverGatewayMeteringBefore is the sweep with an explicit age cutoff. It exists
// so a test can exercise recovery without waiting out the grace window that keeps
// the sweep away from in-flight requests; production always passes now-grace.
func (s *Service) recoverGatewayMeteringBefore(ctx context.Context, cutoff time.Time, limit int) (int, int, error) {
	intents, err := s.Store.ListMeteringIntentsByState(ctx,
		[]string{model.MeteringPending, model.MeteringFailed}, cutoff, limit)
	if err != nil {
		return 0, 0, err
	}
	recovered, stillOpen := 0, 0
	for i := range intents {
		switch s.applyMeteringIntent(ctx, &intents[i], false) {
		case model.MeteringRecorded:
			recovered++
		default:
			stillOpen++
		}
	}
	if recovered > 0 {
		obs.Get().AddMeteringRecovered(recovered)
	}
	// The pass is only a reading once the state it publishes has actually been
	// read. Stamping liveness while the count query is failing would report the
	// repair path as healthy at the exact moment it cannot answer — the same
	// "unknown read as zero" mistake in a different seat (doc/304 §4 E148).
	if err := s.publishMeteringState(ctx); err != nil {
		return recovered, stillOpen, err
	}
	return recovered, stillOpen, nil
}

// meteringPublishHook is a test seam for the publication step: it lets a test see
// what the sweep would have published, and prove that a failed read publishes
// nothing at all. Production leaves it nil, so the cost is one nil check.
var meteringPublishHook func(map[string]int64)

// publishMeteringState reads the current gap per tenant and publishes it. A read
// failure returns an error and publishes nothing: the last known values stay
// readable, and the missing liveness stamp turns the stall alert on after 15
// minutes. Reporting the tenant's gap as `0` because the query failed would be a
// false clean bill of health, which is worse than no reading at all.
func (s *Service) publishMeteringState(ctx context.Context) error {
	byTenant, err := s.Store.CountUnsettledMeteringIntentsByTenant(ctx)
	if err != nil {
		return fmt.Errorf("metering state could not be read: %w", err)
	}
	if meteringPublishHook != nil {
		meteringPublishHook(byTenant)
	}
	obs.Get().PublishMeteringUnsettled(byTenant)
	// A completed pass is a reading even when it had nothing to replay: without it,
	// `unsettled=0` cannot be told apart from "the sweep has not run".
	obs.Get().SetMeteringSweepSuccess(time.Now().UTC())
	return nil
}

// meteringVerdict answers the completion-time question for one request: which
// metering state may be written onto the idempotency row, and which intent row it
// came from. A nil intent is `none` — nothing was owed — and never an error.
type meteringVerdict struct {
	State    string
	IntentID string
	// Unwritten says metering was attempted for this request and the record that
	// makes it recoverable could not be created. It is a fourth kind of absence,
	// separate from `none` (nothing owed), `pending` (owed and queued), and a read
	// error (we cannot tell): here we know something is owed and we know nothing
	// will replay it (doc/304 §4 E148).
	Unwritten bool
}

func (s *Service) meteringVerdict(ctx context.Context, tenantID, requestID string) (meteringVerdict, error) {
	if requestID == "" {
		return meteringVerdict{State: model.MeteringNone}, nil
	}
	intent, err := s.Store.GetMeteringIntent(ctx, requestID)
	if err != nil {
		return meteringVerdict{}, err
	}
	if intent == nil {
		// No row: either nothing was owed, or the row could not be written. The
		// second case is only knowable from the request that tried, and it is
		// knowable for as long as that request has not completed yet.
		return meteringVerdict{State: model.MeteringNone, Unwritten: s.meteringIsUnwritten(requestID)}, nil
	}
	if intent.TenantID != tenantID {
		// A request id we cannot attribute to this tenant is not evidence about this
		// request. Treating it as recorded would let one tenant's books vouch for
		// another's.
		return meteringVerdict{State: model.MeteringNone}, nil
	}
	return meteringVerdict{State: intent.State, IntentID: intent.ID}, nil
}

// meteringUnwrittenTTL bounds how long the in-process marker is consulted. A
// completion is written milliseconds after the metering attempt, so minutes are
// generous; anything older is answered by the idempotency row still being
// IN_PROGRESS, which is the durable half of the same rule.
const meteringUnwrittenTTL = 2 * time.Minute

// meteringUnwrittenCap bounds the marker map during a database outage, when every
// served request fails the same insert. Oldest entries are dropped first; dropping
// one only weakens the answer for a request that is already far past its
// completion.
const meteringUnwrittenCap = 4096

func (s *Service) markMeteringUnwritten(requestID string) {
	if requestID == "" {
		return
	}
	now := time.Now().UTC()
	s.meteringUnwrittenMu.Lock()
	defer s.meteringUnwrittenMu.Unlock()
	if s.meteringUnwritten == nil {
		s.meteringUnwritten = map[string]time.Time{}
	}
	for id, expires := range s.meteringUnwritten {
		if now.After(expires) {
			delete(s.meteringUnwritten, id)
		}
	}
	if len(s.meteringUnwritten) >= meteringUnwrittenCap {
		oldestID, oldest := "", now
		for id, expires := range s.meteringUnwritten {
			if expires.Before(oldest) {
				oldestID, oldest = id, expires
			}
		}
		delete(s.meteringUnwritten, oldestID)
	}
	s.meteringUnwritten[requestID] = now.Add(meteringUnwrittenTTL)
}

func (s *Service) meteringIsUnwritten(requestID string) bool {
	s.meteringUnwrittenMu.Lock()
	defer s.meteringUnwrittenMu.Unlock()
	expires, ok := s.meteringUnwritten[requestID]
	if !ok || time.Now().UTC().After(expires) {
		return false
	}
	return true
}

// meteringCompletionDecision is the whole rule, written as one place so it can be
// read and tested in isolation (doc/304 §4 E148). It answers: may this request be
// marked terminal?
//
//   - `recorded` → yes; the books are written.
//   - `failed` → yes, but only with the intent id that makes the gap recoverable.
//     A `failed` verdict with no intent row is an unrecoverable loss dressed as a
//     known one, so it is refused; the metric and the log carry it.
//   - `pending` → no; owed and queued, but the books are not written yet.
//   - `none` → yes; nothing was owed.
//   - unwritten → no; the record of what is owed does not exist.
//   - unreadable → no; we cannot vouch for the books at all.
func meteringCompletionDecision(v meteringVerdict, err error) (bool, string) {
	switch {
	case err != nil:
		return false, "metering verdict unreadable"
	case v.Unwritten:
		return false, "metering intent could not be persisted, so nothing will recover this request"
	case v.State == model.MeteringPending:
		return false, "metering outcome is not settled"
	case v.State == model.MeteringFailed && v.IntentID == "":
		return false, "metering failed with no durable intent to replay"
	case v.State == "":
		return false, "metering verdict is empty"
	default:
		return true, ""
	}
}

// GatewayMeteringHealth is the tenant-facing reading of the metering gap
// (doc/304 §4 E147). It answers, without an investigation: how many served requests
// are owed books, which ones, and why the write failed.
//
// It reads the intents, not the ledgers: the ledgers are the book of record for
// what was written, while the intents are the only place that knows what was
// *supposed* to be written. A dashboard from either one alone cannot show a gap.
type GatewayMeteringHealth struct {
	Pending   int64                  `json:"pending"`
	Failed    int64                  `json:"failed"`
	Unsettled int64                  `json:"unsettled"`
	Intents   []model.MeteringIntent `json:"intents"`
	// Window names what the counts are taken over, because "0 unsettled" without a
	// denominator can mean "nothing owed" or "nothing looked at".
	Window string `json:"window"`
}

// GatewayMeteringHealth reports the unsettled intents for a tenant. `scopeAll` is a
// governance capability, decided by the caller (the handler checks it), not a
// parameter this method re-derives.
func (s *Service) GatewayMeteringHealth(ctx context.Context, tenantID string, scopeAll bool) (*GatewayMeteringHealth, error) {
	states := []string{model.MeteringPending, model.MeteringFailed}
	counts, err := s.Store.CountMeteringIntentsForTenant(ctx, tenantID, scopeAll, states)
	if err != nil {
		return nil, err
	}
	intents, err := s.Store.ListUnsettledMeteringIntents(ctx, tenantID, scopeAll, 20)
	if err != nil {
		return nil, err
	}
	health := &GatewayMeteringHealth{
		Pending:   counts[model.MeteringPending],
		Failed:    counts[model.MeteringFailed],
		Unsettled: counts[model.MeteringPending] + counts[model.MeteringFailed],
		Intents:   intents,
		Window:    "all unsettled intents (no age cutoff); the recovery sweep replays them every " + meteringRecoveryInterval.String(),
	}
	if health.Intents == nil {
		// An empty list is a reading ("nothing is owed"), an omitted field is not.
		health.Intents = []model.MeteringIntent{}
	}
	return health, nil
}
