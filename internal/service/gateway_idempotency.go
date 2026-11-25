package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// GatewayIdempotencyState is the result of opening a caller retry ledger entry.
type GatewayIdempotencyState struct {
	Record   *model.GatewayIdempotency
	Replayed bool
}

// BeginGatewayIdempotency creates or resolves a caller retry ledger entry. The
// unique constraint is tenant/principal/key; fingerprint is compared only after
// the key collision is resolved so conflicting content cannot be inserted.
func (s *Service) BeginGatewayIdempotency(ctx context.Context, key *model.APIKey, idempotencyKey, method, target string, body []byte, requestID string) (*GatewayIdempotencyState, error) {
	if key == nil || idempotencyKey == "" {
		return &GatewayIdempotencyState{}, nil
	}
	fingerprint := gatewayFingerprint(idempotencyKey, method, target, body)
	record := &model.GatewayIdempotency{
		ID:                 id.New(),
		TenantID:           key.TenantID,
		PrincipalID:        key.UserID,
		IdempotencyKey:     idempotencyKey,
		RequestFingerprint: fingerprint,
		Status:             model.IdempotencyInProgress,
		RequestID:          requestID,
		CreatedAt:          time.Now().UTC(),
		ExpiresAt:          time.Now().UTC().Add(24 * time.Hour),
	}
	created, err := s.Store.CreateGatewayIdempotency(ctx, record)
	if err != nil {
		return nil, httperr.New(502, 50240, "gateway idempotency ledger failed")
	}
	if created {
		return &GatewayIdempotencyState{Record: record}, nil
	}
	existing, err := s.Store.GetGatewayIdempotency(ctx, key.TenantID, key.UserID, idempotencyKey)
	if err != nil || existing == nil {
		return nil, httperr.New(502, 50240, "gateway idempotency lookup failed")
	}
	if existing.RequestFingerprint != fingerprint {
		return nil, httperr.New(409, 40980, "idempotency key conflicts with original request")
	}
	if time.Now().UTC().After(existing.ExpiresAt) {
		existing.Status = model.IdempotencyExpired
		return &GatewayIdempotencyState{Record: existing}, nil
	}
	if existing.Status == model.IdempotencyInProgress {
		return &GatewayIdempotencyState{Record: existing}, nil
	}
	if existing.Status == model.IdempotencyCompleted || existing.Status == model.IdempotencyFailed {
		return &GatewayIdempotencyState{Record: existing, Replayed: true}, nil
	}
	return &GatewayIdempotencyState{Record: existing}, nil
}

// CompleteGatewayIdempotency marks the in-progress entry for this exact
// execution request, and it now has to be able to say whether the request's books
// were written (doc/304 §4 E147/E148).
//
// The rule it enforces is the one E146 showed was missing: `COMPLETED` used to be
// written unconditionally, after a metering failure that only a log line
// remembered. The path now resolves the request's metering intent and applies
// meteringCompletionDecision: `recorded`/`none` complete, `failed` completes only
// with the intent id that makes the gap recoverable, and an unreadable verdict, an
// unwritten intent or a `pending` outcome refuse. Refusing keeps the entry
// IN_PROGRESS, which is the loud failure: the caller's retry re-executes instead of
// replaying a body whose books nobody can vouch for.
func (s *Service) CompleteGatewayIdempotency(ctx context.Context, state *GatewayIdempotencyState, requestID, responseRef string) {
	if state == nil || state.Record == nil || state.Replayed {
		return
	}
	verdict, err := s.meteringVerdict(ctx, state.Record.TenantID, requestID)
	allowed, reason := meteringCompletionDecision(verdict, err)
	if !allowed {
		logger.Error("gateway idempotency completion deferred: "+reason,
			"id", state.Record.ID, "request_id", requestID, "intent_id", verdict.IntentID,
			"metering_state", verdict.State, "error", err)
		ledger := "verdict"
		if verdict.Unwritten {
			ledger = "intent"
		}
		obs.Get().IncMeteringWriteFailure(state.Record.TenantID, ledger)
		return
	}
	if verdict.State == model.MeteringFailed {
		logger.Error("gateway request completed with metering failed",
			"id", state.Record.ID, "request_id", requestID, "intent_id", verdict.IntentID)
	}
	if err := s.Store.CompleteGatewayIdempotency(ctx, state.Record.ID, requestID, responseRef, verdict.State, verdict.IntentID); err != nil {
		logger.Warn("gateway idempotency completion failed", "id", state.Record.ID, "request_id", requestID, "error", err)
	}
}

// FailGatewayIdempotency stores the terminal failure response and prevents the
// same caller key from silently re-executing after a terminal error.
func (s *Service) FailGatewayIdempotency(ctx context.Context, state *GatewayIdempotencyState, requestID, responseRef string) {
	if state == nil || state.Record == nil || state.Replayed {
		return
	}
	if err := s.Store.FailGatewayIdempotency(ctx, state.Record.ID, requestID, responseRef); err != nil {
		logger.Warn("gateway idempotency failure write failed", "id", state.Record.ID, "request_id", requestID, "error", err)
	}
}

func gatewayFingerprint(idempotencyKey, method, target string, body []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte(idempotencyKey))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(method))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write([]byte(target))
	_, _ = digest.Write([]byte{0})
	_, _ = digest.Write(body)
	return hex.EncodeToString(digest.Sum(nil))
}
