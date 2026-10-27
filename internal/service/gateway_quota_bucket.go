package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// Bucket quota error codes (doc/224 §2.5 T5.1). Key-scoped exhaustion stays
// 429/42930 for backward compatibility; bucket (tenant/model/assistant)
// exhaustion uses 430/42931 — HTTP 430 "Would Block" is the standard status
// for a secondary dimension denying admission, so consumers can distinguish
// which ledger rejected the request without parsing the body.
const (
	gatewayBucketQuotaExhausted = 42931
)

// PreauthorizeTenantBucketQuota is gateway A1's second pre-deduction gate,
// layered after the key-scoped quota. When the caller's tenant has a
// configured tenant-dimension bucket (token or request limit > 0) it
// atomically reserves the same estimate with the same batch semantics; if the
// bucket cannot satisfy the request it is rejected with 430/42931 before any
// provider call. Tenants without a configured bucket are unlimited and skip
// this gate entirely (opt-in, so existing per-key quota behavior is unchanged).
//
// Est/request bounds mirror the key gate: tokenEnabled/requestEnabled decide
// which balancing columns are reserved, copied from the key's configuration
// which the caller already evaluated.
// PreauthorizeTenantBucketQuota is the tenant-dimension preauthorization gate.
// It is the default bucket when no more specific dimension is configured.
func (s *Service) PreauthorizeTenantBucketQuota(ctx context.Context, key *model.APIKey, requestID string, est int64, tokenEnabled, requestEnabled bool) (QuotaResult, error) {
	return s.PreauthorizeBucketQuota(ctx, key, requestID, est, tokenEnabled, requestEnabled,
		model.QuotaBucketDimensionTenant, key.TenantID)
}

// PreauthorizeBucketQuota is the dimension-generic bucket gate (tenant, model or
// assistant): it reserves estimated tokens/requests against the (dimension,
// dimensionKey) bucket for the current period, rejecting with 430/42931 (HTTP
// 430 Would-Block) when the budget is exhausted. A missing or unlimited bucket
// is a no-op that reports  unlimited remaining, so callers fall back to the key
// gate semantics. The ledger reservation is keyed by requestID and records its
// own dimension, so finalize/release stay dimension-agnostic.
func (s *Service) PreauthorizeBucketQuota(ctx context.Context, key *model.APIKey, requestID string, est int64, tokenEnabled, requestEnabled bool, dimension, dimensionKey string) (QuotaResult, error) {
	period := quotaPeriodStart()
	bucket, err := s.Store.GetQuotaBucket(ctx, key.TenantID, dimension, dimensionKey, period)
	if err != nil {
		return QuotaResult{}, s.quotaLedgerErr(key, requestID, err)
	}
	if bucket == nil || (bucket.TokenLimit <= 0 && bucket.RequestLimit <= 0) {
		return QuotaResult{Remaining: -1, RemainingRequests: -1}, nil
	}
	tokenLimit := bucket.TokenLimit > 0
	requestLimit := bucket.RequestLimit > 0
	result := QuotaResult{
		Enabled:           true,
		TokenEnabled:      tokenEnabled,
		Remaining:         -1,
		RequestEnabled:    requestEnabled,
		RemainingRequests: -1,
	}
	if err := s.reapStaleBucketQuota(ctx, key.TenantID, dimension, dimensionKey, period); err != nil {
		return result, s.quotaLedgerErr(key, requestID, err)
	}
	inserted, err := s.Store.AddQuotaBucketReservation(ctx, &model.QuotaBucketReservation{
		RequestID: requestID, TenantID: key.TenantID,
		Dimension: dimension, DimensionKey: dimensionKey,
		PeriodStart: period, Estimated: est, Status: model.QuotaReservationPending,
	})
	if err != nil {
		return result, s.quotaLedgerErr(key, requestID, err)
	}
	if !inserted {
		// Same request_id retried: already reserved against this bucket, no
		// double-deduct. Surface the current remaining balances.
		current, err := s.Store.GetQuotaBucket(ctx, key.TenantID, dimension, dimensionKey, period)
		if err != nil {
			return result, s.quotaLedgerErr(key, requestID, err)
		}
		result.Remaining = -1
		result.RemainingRequests = -1
		if current != nil {
			if tokenLimit {
				result.Remaining = quotaLimitRemaining(current.TokenLimit, current.TokensUsed, current.Pending)
			}
			if requestLimit {
				result.RemainingRequests = quotaLimitRemaining(current.RequestLimit, current.RequestsUsed, current.PendingRequests)
			}
		}
		return result, nil
	}
	applied, tokenRemaining, requestRemaining, err := s.Store.ReserveQuotaBucketTokens(
		ctx, key.TenantID, dimension, dimensionKey, period, requestID, est, requestEnabled,
	)
	if err != nil {
		_ = s.Store.ReleaseQuotaBucketReservation(ctx, requestID)
		return result, s.quotaLedgerErr(key, requestID, err)
	}
	if !applied {
		_ = s.Store.ReleaseQuotaBucketReservation(ctx, requestID)
		notify.Emit(ctx, notify.Event{
			Title: "gateway quota bucket exhausted", Severity: "warn", TenantID: key.TenantID,
			Resource: "chat", Type: "bucket_quota_exceeded", ResourceID: key.TenantID,
			Detail: "request rejected by " + dimension + " quota bucket before any provider call",
		})
		result.Remaining, result.RemainingRequests = tokenRemaining, requestRemaining
		return result, httperr.New(430, gatewayBucketQuotaExhausted, "insufficient quota: "+dimension+" quota bucket budget exhausted")
	}
	result.Remaining, result.RemainingRequests = tokenRemaining, requestRemaining
	return result, nil
}

// FinalizeTenantBucketQuota commits a successfully metered request's actual
// consumption against a tenant bucket reservation. It is idempotent by
// request_id and best-effort (a missing bucket reservation is a no-op).
func (s *Service) FinalizeTenantBucketQuota(ctx context.Context, key *model.APIKey, requestID string, actualTokens int64) {
	if err := s.Store.FinalizeQuotaBucketReservation(ctx, requestID, actualTokens); err != nil {
		logger.Warn("gateway tenant bucket quota finalize failed", "tenant_id", key.TenantID, "request_id", requestID, "error", err)
	}
}

// ReleaseTenantBucketQuota aborts a tenant bucket reservation without
// consuming budget. Idempotent and best-effort.
func (s *Service) ReleaseTenantBucketQuota(ctx context.Context, key *model.APIKey, requestID string) {
	if err := s.Store.ReleaseQuotaBucketReservation(ctx, requestID); err != nil {
		logger.Warn("gateway tenant bucket quota release failed", "tenant_id", key.TenantID, "request_id", requestID, "error", err)
	}
}

// extractModelName pulls the "model" field out of a gateway request body so
// the composite gate can target a model-dimension bucket when one is
// configured. Unparsable bodies or missing model names yield "", which falls
// back to the tenant bucket path.
func extractModelName(rawReq []byte) string {
	if len(rawReq) == 0 {
		return ""
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(rawReq, &body); err != nil {
		return ""
	}
	return strings.TrimSpace(body.Model)
}

// PreauthorizeGatewayQuotaWithBucket is the composite A1 gate: key-scoped
// pre-deduction followed by a dimension bucket gate when one is configured.
// The bucket dimension is resolved per request: a configured model bucket (the
// request body names a model and a model-dimension bucket exists for it) wins
// over the tenant bucket; otherwise the tenant bucket is used. It reports the
// tightest remaining budget across every enabled dimension so
// X-Quota-Remaining never over-advertises. When no bucket of any dimension
// exists the behavior is byte-for-byte the legacy key gate. On bucket
// rejection the key reservation is released so no ledger row is left dangling.
func (s *Service) PreauthorizeGatewayQuotaWithBucket(ctx context.Context, key *model.APIKey, rawReq []byte, requestID string) (QuotaResult, error) {
	result, err := s.PreauthorizeGatewayQuota(ctx, key, rawReq, requestID)
	if err != nil {
		return result, err
	}
	est := estimateRequestTokens(rawReq)
	var bres QuotaResult
	var berr error
	if modelName := extractModelName(rawReq); modelName != "" {
		bres, berr = s.PreauthorizeBucketQuota(ctx, key, requestID, est, key.TokenQuota > 0, key.RequestQuota > 0,
			model.QuotaBucketDimensionModel, modelName)
	}
	if berr == nil && !bres.Enabled {
		// No (or an unlimited) model bucket: fall back to the tenant bucket to
		// preserve legacy tenant-dimension behavior.
		bres, berr = s.PreauthorizeTenantBucketQuota(ctx, key, requestID, est, key.TokenQuota > 0, key.RequestQuota > 0)
	}
	if berr != nil {
		s.ReleaseGatewayQuota(ctx, key, requestID)
		return bres, berr
	}
	if result.Enabled && bres.Enabled {
		result.Enabled = true
		result.Remaining = minQuotaRemaining(result.Remaining, bres.Remaining)
		result.RemainingRequests = minQuotaRemaining(result.RemainingRequests, bres.RemainingRequests)
		result.TokenEnabled = result.TokenEnabled || bres.TokenEnabled
		result.RequestEnabled = result.RequestEnabled || bres.RequestEnabled
	} else if bres.Enabled {
		result = bres
	}
	return result, nil
}

// FinalizeGatewayQuotaWithBucket commits a successful meter against both the
// key ledger and any tenant bucket reservation.
func (s *Service) FinalizeGatewayQuotaWithBucket(ctx context.Context, key *model.APIKey, requestID string, actualTokens int64) {
	s.FinalizeGatewayQuota(ctx, key, requestID, actualTokens)
	s.FinalizeTenantBucketQuota(ctx, key, requestID, actualTokens)
}

// ReleaseGatewayQuotaWithBucket aborts reservations on both the key ledger and
// any tenant bucket reservation without consuming budget.
func (s *Service) ReleaseGatewayQuotaWithBucket(ctx context.Context, key *model.APIKey, requestID string) {
	s.ReleaseGatewayQuota(ctx, key, requestID)
	s.ReleaseTenantBucketQuota(ctx, key, requestID)
}

// minQuotaRemaining returns the tighter of two remainders, treating -1
// (unlimited) as +∞.
func minQuotaRemaining(a, b int64) int64 {
	if a < 0 {
		return b
	}
	if b < 0 {
		return a
	}
	if a < b {
		return a
	}
	return b
}

// reapStaleBucketQuota ages out stuck in-flight bucket reservations for a
// tenant/dimension/period, bounded by the shared reap interval so it does not
// add a query to every request.
func (s *Service) reapStaleBucketQuota(ctx context.Context, tenantID, dimension, dimensionKey, period string) error {
	released, err := s.Store.ReapStaleQuotaBucketReservations(ctx, tenantID, dimension, dimensionKey, period, time.Now().Add(-staleQuotaReapInterval))
	if err != nil {
		return err
	}
	if released > 0 {
		logger.Info("reaped stale bucket quota reservations", "tenant_id", tenantID, "dimension", dimension, "released_tokens", released)
	}
	return nil
}
