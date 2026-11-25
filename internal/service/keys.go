package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/keygen"
)

// CreateAPIKey creates a gateway credential and returns the raw key exactly
// once; only its hash is persisted. tokenQuota is the monthly token budget
// (<= 0 means unlimited) used by gateway quota preauthorization.
func (s *Service) CreateAPIKey(ctx context.Context, tenantID, userID, name string, expiry *time.Time, tokenQuota, requestQuota int64, scopes *APIKeyScopes, allowedIPs *[]string) (string, *model.APIKey, error) {
	if name == "" {
		return "", nil, httperr.BadRequest(40040, "key name is required")
	}
	if tokenQuota < 0 {
		return "", nil, httperr.BadRequest(40041, "token_quota must be zero or a positive integer")
	}
	if requestQuota < 0 {
		return "", nil, httperr.BadRequest(40041, "request_quota must be zero or a positive integer")
	}
	allowedIPsJSON := ""
	if allowedIPs != nil {
		if err := validateAPIKeyIPs(*allowedIPs); err != nil {
			return "", nil, err
		}
		data, err := json.Marshal(*allowedIPs)
		if err != nil {
			return "", nil, httperr.BadRequest(40043, "invalid api key allowed ips")
		}
		allowedIPsJSON = string(data)
	}
	scopeJSON := ""
	if scopes != nil {
		data, err := json.Marshal(scopes)
		if err != nil {
			return "", nil, httperr.BadRequest(40042, "invalid api key scopes")
		}
		scopeJSON = string(data)
	}
	raw, err := keygen.New()
	if err != nil {
		return "", nil, err
	}
	k := &model.APIKey{
		ID:             id.New(),
		TenantID:       tenantID,
		UserID:         userID,
		Name:           name,
		KeyHash:        keygen.Hash(raw),
		Prefix:         keygen.Prefix(raw),
		Enabled:        true,
		ScopesJSON:     scopeJSON,
		AllowedIPsJSON: allowedIPsJSON,
		ExpiredAt:      expiry,
		TokenQuota:     tokenQuota,
		RequestQuota:   requestQuota,
	}
	if err := s.Store.CreateAPIKey(ctx, k); err != nil {
		return "", nil, err
	}
	return raw, k, nil
}

// apiKeyExpiredAt reports whether a key is already dead at `now`.
//
// The comparison is inclusive: the instant a key expires is *past* it. This is not
// pedantry. A rotation with grace 0 stores `expired_at = time.Now()`, and the wall
// clock is quantised — on Windows to a tick of about a millisecond — so the very
// next request can read the same instant the key was retired at. With a strict
// `now.After(expired_at)` the retired secret stayed usable for that tick, and
// `TestAPIKeyRotationKeepsTheOldKeyUntilItsWindowCloses` failed about once in
// twenty-five runs under load (`s07.go-test-all` red on a tree that was fine).
// Grace 0 means "dead when the response returns", so equal must be dead
// (doc/304 §4 E144).
func apiKeyExpiredAt(expiredAt *time.Time, now time.Time) bool {
	return expiredAt != nil && !now.Before(*expiredAt)
}

// ListAPIKeys returns a tenant's gateway keys (without hashes).
func (s *Service) ListAPIKeys(ctx context.Context, tenantID string) ([]model.APIKey, error) {
	return s.Store.ListAPIKeys(ctx, tenantID)
}

// Key rotation (doc/301 GW-4 / T22).
//
// Before this existed, rotating a credential meant "revoke, then create": the old key
// died at the instant of the revoke, so every caller holding it failed at the same
// second the new secret was generated. That is not a rotation, it is an outage with
// extra steps.
//
// The design keeps exactly one clock. The retired key is *not* disabled — it is given
// an `expired_at` at the end of the grace window, and `ResolveAPIKey` already refuses an
// expired key. A separate "grace until" column would be a second authority on when the
// key stops working, and the two would eventually disagree.
const (
	// apiKeyRotationDefaultGraceHours is the window used when the caller does not say.
	// 24h covers "deploy the new secret across your callers" without leaving a retired
	// credential alive for a week by accident.
	apiKeyRotationDefaultGraceHours = 24
	// apiKeyRotationMaxGraceHours bounds the window. An unbounded grace is an unbounded
	// lifetime for a secret that was supposed to be replaced.
	apiKeyRotationMaxGraceHours = 720
)

// RotateAPIKey creates a successor key that inherits the old key's constraints and
// retires the old one after `graceHours` (clamped; nil = default).
//
// Inheritance is deliberate and total: quotas, scopes, allowed IPs and owning user are
// copied. A rotation that quietly widened a scope would be a privilege escalation with
// an innocuous name, so there is no parameter to pass new ones — if a caller wants
// different constraints they create a new key and revoke the old one.
func (s *Service) RotateAPIKey(
	ctx context.Context, tenantID, keyID string, graceHours *int,
) (string, *model.APIKey, *model.APIKey, error) {
	if strings.TrimSpace(keyID) == "" {
		return "", nil, nil, httperr.BadRequest(40040, "key id is required")
	}
	grace := apiKeyRotationDefaultGraceHours
	if graceHours != nil {
		grace = *graceHours
	}
	if grace < 0 {
		return "", nil, nil, httperr.BadRequest(40041, "grace_hours must be zero or a positive integer")
	}
	if grace > apiKeyRotationMaxGraceHours {
		grace = apiKeyRotationMaxGraceHours
	}

	old, err := s.Store.GetAPIKey(ctx, tenantID, keyID)
	if err != nil {
		return "", nil, nil, err
	}
	if old == nil {
		return "", nil, nil, httperr.NotFound("api key not found")
	}
	// A revoked key is a decision that has already been made. Rotating it would
	// resurrect the capability under a new secret, which is the one thing a revoke
	// must be able to guarantee it prevented.
	if !old.Enabled {
		return "", nil, nil, httperr.New(409, httperr.CodeAPIKeyRotationConflict, "a revoked api key cannot be rotated")
	}
	if old.RotatedToKeyID != "" {
		return "", nil, nil, httperr.New(409, httperr.CodeAPIKeyRotationConflict, "api key has already been rotated")
	}

	raw, err := keygen.New()
	if err != nil {
		return "", nil, nil, err
	}
	successor := &model.APIKey{
		ID:             id.New(),
		TenantID:       old.TenantID,
		UserID:         old.UserID,
		Name:           old.Name,
		KeyHash:        keygen.Hash(raw),
		Prefix:         keygen.Prefix(raw),
		TokenQuota:     old.TokenQuota,
		RequestQuota:   old.RequestQuota,
		ScopesJSON:     old.ScopesJSON,
		AllowedIPsJSON: old.AllowedIPsJSON,
		Enabled:        true,
	}
	if err := s.Store.CreateAPIKey(ctx, successor); err != nil {
		return "", nil, nil, err
	}

	retiredAt := time.Now().UTC().Add(time.Duration(grace) * time.Hour)
	ok, err := s.Store.RetireAPIKeyForRotation(ctx, tenantID, keyID, successor.ID, retiredAt)
	if err != nil {
		return "", nil, nil, err
	}
	if !ok {
		// Lost a race with a concurrent rotation or revoke. The successor row is
		// already inserted, so it is disabled rather than left live: a secret
		// nobody's caller knows about must not be able to authenticate.
		_ = s.Store.RevokeAPIKey(ctx, tenantID, successor.ID)
		return "", nil, nil, httperr.New(409, httperr.CodeAPIKeyRotationConflict, "api key has already been rotated")
	}
	// Read the old row back so the response shows the stored expiry rather than the
	// value this process computed; a caller that prints "grace until" must print the
	// number that actually governs the key.
	if refreshed, err := s.Store.GetAPIKey(ctx, tenantID, keyID); err == nil && refreshed != nil {
		old = refreshed
	}
	return raw, successor, old, nil
}

// RevokeAPIKey disables a tenant's key.
func (s *Service) RevokeAPIKey(ctx context.Context, tenantID, keyID string) error {
	return s.Store.RevokeAPIKey(ctx, tenantID, keyID)
}

// ResolveAPIKey validates a raw gateway key and returns the credential record.
func (s *Service) ResolveAPIKey(ctx context.Context, raw string) (*model.APIKey, error) {
	if raw == "" {
		return nil, httperr.Unauthorized("missing api key")
	}
	k, err := s.Store.GetAPIKeyByHash(ctx, keygen.Hash(raw))
	if err != nil {
		return nil, err
	}
	if k == nil || !k.Enabled {
		return nil, httperr.Unauthorized("invalid api key")
	}
	tenant, err := s.Store.GetTenant(ctx, k.TenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil || tenant.Status != model.TenantStatusActive {
		return nil, httperr.Forbidden("workspace is disabled")
	}
	if apiKeyExpiredAt(k.ExpiredAt, time.Now()) {
		return nil, httperr.Unauthorized("api key expired")
	}
	if err := s.Store.TouchAPIKey(ctx, k.ID); err != nil {
		return nil, err
	}
	return k, nil
}
