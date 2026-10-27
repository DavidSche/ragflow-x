package service

import (
	"context"
	"encoding/json"
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

// ListAPIKeys returns a tenant's gateway keys (without hashes).
func (s *Service) ListAPIKeys(ctx context.Context, tenantID string) ([]model.APIKey, error) {
	return s.Store.ListAPIKeys(ctx, tenantID)
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
	if k.ExpiredAt != nil && time.Now().After(*k.ExpiredAt) {
		return nil, httperr.Unauthorized("api key expired")
	}
	if err := s.Store.TouchAPIKey(ctx, k.ID); err != nil {
		return nil, err
	}
	return k, nil
}
