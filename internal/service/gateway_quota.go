package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// QuotaResult carries the outcome of gateway quota preauthorization so the
// handler can advertise only the budgets configured on the API key.
type QuotaResult struct {
	// Enabled is true when any configured quota reservation path ran.
	Enabled bool
	// TokenEnabled is true when a non-zero token quota is configured.
	TokenEnabled bool
	// Remaining is the post-reservation remaining budget in tokens; -1 means
	// unlimited.
	Remaining int64
	// RequestEnabled is true when a non-zero logical request quota is configured.
	RequestEnabled bool
	// RemainingRequests is the post-reservation request remainder; -1 means
	// unlimited.
	RemainingRequests int64
}

// staleQuotaReapInterval bounds how often stale in-flight reservations are
// swept per key/period, so the reap does not add a query to every request.
const staleQuotaReapInterval = time.Minute

// PreauthorizeGatewayQuota is gateway A1's pre-deduction gate. For keys with a
// configured monthly token quota (TokenQuota > 0) it atomically reserves an
// estimate of the request before any provider call: over-budget requests are
// rejected with 429 and never reach RAGFlow, while concurrent requests cannot
// collectively exceed the budget (reserve and reconcile are exactly-once per
// request_id). Unlimited keys bypass all of this.
//
// The returned error is *httperr.Error: 42930 on quota exhaustion, 50239 when
// the quota ledger is unavailable (fail-closed for spending control so a
// degraded control plane cannot silently overrun a budget).
func (s *Service) PreauthorizeGatewayQuota(ctx context.Context, key *model.APIKey, rawReq []byte, requestID string) (QuotaResult, error) {
	tokenEnabled := key.TokenQuota > 0
	requestEnabled := key.RequestQuota > 0
	if !tokenEnabled && !requestEnabled {
		return QuotaResult{Remaining: -1, RemainingRequests: -1}, nil
	}
	period := quotaPeriodStart()
	s.reapStaleQuota(ctx, key, period)

	result := QuotaResult{
		Enabled:           true,
		TokenEnabled:      tokenEnabled,
		Remaining:         -1,
		RequestEnabled:    requestEnabled,
		RemainingRequests: -1,
	}
	if err := s.Store.UpsertQuotaLimit(ctx, &model.QuotaLimit{
		TenantID: key.TenantID, KeyID: key.ID, PeriodStart: period,
		TokenLimit: key.TokenQuota, RequestLimit: key.RequestQuota,
	}); err != nil {
		return result, s.quotaLedgerErr(key, requestID, err)
	}

	est := estimateRequestTokens(rawReq)
	inserted, err := s.Store.AddQuotaReservation(ctx, &model.QuotaReservation{
		RequestID: requestID, TenantID: key.TenantID, KeyID: key.ID,
		PeriodStart: period, Estimated: est, Status: model.QuotaReservationPending,
	})
	if err != nil {
		return result, s.quotaLedgerErr(key, requestID, err)
	}
	if !inserted {
		// Same request_id retried: reservation already exists, do not double
		// deduct. Surface the current remaining budget.
		tokenRemaining, requestRemaining, rerr := s.currentQuotaRemaining(ctx, key.ID, period)
		if rerr != nil {
			return result, s.quotaLedgerErr(key, requestID, rerr)
		}
		result.Remaining, result.RemainingRequests = tokenRemaining, requestRemaining
		return result, nil
	}

	applied, tokenRemaining, requestRemaining, err := s.Store.ReserveQuotaTokens(ctx, key.ID, period, requestID, est, requestEnabled)
	if err != nil {
		return result, s.quotaLedgerErr(key, requestID, err)
	}
	if !applied {
		// Roll the ledger back so a denied request leaves no dangling row.
		_ = s.Store.ReleaseQuotaReservation(ctx, requestID)
		notify.Emit(ctx, notify.Event{
			Title: "gateway quota exhausted", Severity: "warn", TenantID: key.TenantID,
			Resource: "chat", Type: "quota_exceeded", ResourceID: key.ID,
			Detail: "request rejected before any provider call",
		})
		result.Remaining, result.RemainingRequests = tokenRemaining, requestRemaining
		return result, httperr.New(429, 42930, "insufficient quota: monthly token or request budget exhausted for this key")
	}
	result.Remaining, result.RemainingRequests = tokenRemaining, requestRemaining
	return result, nil
}

// FinalizeGatewayQuota commits a successfully metered request's actual token
// consumption against the pre-reserved budget. It is idempotent by request_id
// and best-effort (metering failures must never fail the response).
func (s *Service) FinalizeGatewayQuota(ctx context.Context, key *model.APIKey, requestID string, actualTokens int64) {
	if key.TokenQuota <= 0 && key.RequestQuota <= 0 {
		return
	}
	if err := s.Store.FinalizeQuotaReservation(ctx, requestID, actualTokens); err != nil {
		logger.Warn("gateway quota finalize failed", "tenant_id", key.TenantID, "key_id", key.ID, "request_id", requestID, "error", err)
	}
}

// ReleaseGatewayQuota aborts a reservation without consuming budget (provider
// error, closed connection, route miss). Idempotent and best-effort.
func (s *Service) ReleaseGatewayQuota(ctx context.Context, key *model.APIKey, requestID string) {
	if key.TokenQuota <= 0 && key.RequestQuota <= 0 {
		return
	}
	if err := s.Store.ReleaseQuotaReservation(ctx, requestID); err != nil {
		logger.Warn("gateway quota release failed", "tenant_id", key.TenantID, "key_id", key.ID, "request_id", requestID, "error", err)
	}
}

// currentQuotaRemaining returns current token and logical-request remainders
// for a key/period (-1 means unlimited for that dimension).
func (s *Service) currentQuotaRemaining(ctx context.Context, keyID, period string) (int64, int64, error) {
	q, err := s.Store.GetQuotaLimit(ctx, keyID, period)
	if err != nil {
		return 0, 0, err
	}
	if q == nil {
		return -1, -1, nil
	}
	return quotaLimitRemaining(q.TokenLimit, q.TokensUsed, q.Pending), quotaLimitRemaining(q.RequestLimit, q.RequestsUsed, q.PendingRequests), nil
}

func quotaLimitRemaining(limit, used, pending int64) int64 {
	if limit <= 0 {
		return -1
	}
	remaining := limit - used - pending
	if remaining < 0 {
		return 0
	}
	return remaining
}

// quotaLedgerErr converts a quota-ledger store failure into a fail-closed 502
// (spending controls must not silently fail open) plus an alert.
func (s *Service) quotaLedgerErr(key *model.APIKey, requestID string, err error) error {
	logger.Error("gateway quota ledger failed", "tenant_id", key.TenantID, "key_id", key.ID, "request_id", requestID, "error", err)
	notify.Emit(context.Background(), notify.Event{
		Title: "gateway quota ledger failed", Severity: "error", TenantID: key.TenantID,
		Resource: "chat", Type: "quota_ledger_error", ResourceID: key.ID, Detail: err.Error(),
	})
	return httperr.New(502, 50239, "quota check failed, request rejected")
}

// estimateRequestTokens produces a conservative upper-bound token estimate for
// a chat/completions request body: prompt tokens are approximated from payload
// size and completion tokens from an explicit max_tokens (or a modest default).
// Reconcilation replaces the estimate with the provider-reported actuals.
func estimateRequestTokens(raw []byte) int64 {
	var b struct {
		MaxTokens           *int64 `json:"max_tokens"`
		MaxCompletionTokens *int64 `json:"max_completion_tokens"`
	}
	_ = json.Unmarshal(raw, &b)
	var completion int64 = 64
	if b.MaxTokens != nil && *b.MaxTokens > 0 {
		completion = *b.MaxTokens
	} else if b.MaxCompletionTokens != nil && *b.MaxCompletionTokens > 0 {
		completion = *b.MaxCompletionTokens
	}
	prompt := int64(len(raw) / 2)
	if prompt < 1 {
		prompt = 1
	}
	return prompt + completion
}

// EstimateGatewayTokens exposes the conservative preauthorization estimate to
// HTTP adapters so all gateway entrypoints use one quota sizing policy.
func (s *Service) EstimateGatewayTokens(raw []byte) int64 {
	return estimateRequestTokens(raw)
}

// quotaPeriodStart is the UTC month boundary used to roll quota periods.
func quotaPeriodStart() string {
	return time.Now().UTC().Format("2006-01")
}

// reapStaleQuota opportunistically releases reservations orphaned by crashes
// (a stuck pending balance must not permanently lock a near-exhausted key),
// throttled to once per key/period per minute.
func (s *Service) reapStaleQuota(ctx context.Context, key *model.APIKey, period string) {
	s.quotaReapMu.Lock()
	defer s.quotaReapMu.Unlock()
	if s.quotaLastReap == nil {
		s.quotaLastReap = make(map[string]time.Time)
	}
	k := key.ID + "|" + period
	if last, ok := s.quotaLastReap[k]; ok && time.Since(last) < staleQuotaReapInterval {
		return
	}
	s.quotaLastReap[k] = time.Now()
	n, err := s.Store.ReapStaleQuotaReservations(ctx, key.ID, period, time.Now().Add(-staleQuotaReapInterval))
	if err != nil {
		logger.Warn("gateway quota reap failed", "tenant_id", key.TenantID, "key_id", key.ID, "error", err)
		return
	}
	if n > 0 {
		logger.Warn("gateway quota stale reservations released", "tenant_id", key.TenantID, "key_id", key.ID, "released_tokens", n)
	}
}
