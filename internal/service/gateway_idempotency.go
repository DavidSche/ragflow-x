package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
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
// execution request. A failed completion is not retried by the gateway.
func (s *Service) CompleteGatewayIdempotency(ctx context.Context, state *GatewayIdempotencyState, requestID, responseRef string) {
	if state == nil || state.Record == nil || state.Replayed {
		return
	}
	if err := s.Store.CompleteGatewayIdempotency(ctx, state.Record.ID, requestID, responseRef); err != nil {
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
