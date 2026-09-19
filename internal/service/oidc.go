package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// OIDCClaims carries the subset of the verified ID token used to link an
// enterprise subject to a pre-provisioned RAGFlow-X account.
type OIDCClaims struct {
	Issuer        string
	Subject       string
	Nonce         string
	Email         string
	EmailVerified bool
	Claims        jwt.MapClaims
	Verified      time.Time
}

// OIDCClient isolates outbound IdP calls so service and router tests can use a
// local contract-test provider.
type OIDCClient interface {
	ResolveEndpoints(ctx context.Context, cfg config.OIDC) (config.OIDC, error)
	AuthURL(cfg config.OIDC, state, nonce string) string
	Exchange(ctx context.Context, cfg config.OIDC, code string) (OIDCClaims, error)
}

func (s *Service) SetOIDCConfig(cfg config.OIDC) {
	s.oidcMu.Lock()
	defer s.oidcMu.Unlock()
	s.oidcConfig = normalizeOIDC(cfg)
}

func (s *Service) SetOIDCClient(client OIDCClient) {
	s.oidcMu.Lock()
	defer s.oidcMu.Unlock()
	s.oidcClient = client
}

// OIDCConfig returns the currently installed enterprise IdP configuration.
func (s *Service) OIDCConfig() config.OIDC {
	s.oidcMu.RLock()
	defer s.oidcMu.RUnlock()
	return s.oidcConfig
}

func (s *Service) oidcSettings() config.OIDC {
	s.oidcMu.RLock()
	defer s.oidcMu.RUnlock()
	return s.oidcConfig
}

// OIDCAuthURL creates the redirect target for authorization code flow.
func (s *Service) OIDCAuthURL(ctx context.Context, state, nonce string) (string, config.OIDC, error) {
	cfg, err := s.enabledOIDCConfig(ctx)
	if err != nil {
		return "", cfg, err
	}
	client := s.currentOIDCClient()
	return client.AuthURL(cfg, state, nonce), cfg, nil
}

// LoginWithOIDC validates an external identity and issues the same rotating
// refresh-token session as password login. It never upgrades roles and never
// creates a user from an IdP claim.
func (s *Service) LoginWithOIDC(ctx context.Context, code, nonce, remoteIP string) (*TokenResult, config.OIDC, error) {
	if !s.loginPermitted(ctx, remoteIP) {
		return nil, config.OIDC{}, httperr.New(429, 429, "too many login attempts, please retry later")
	}
	cfg, err := s.enabledOIDCConfig(ctx)
	if err != nil {
		return nil, cfg, err
	}
	claims, err := s.currentOIDCClient().Exchange(ctx, cfg, code)
	if err != nil || claims.Issuer != cfg.Issuer || claims.Nonce != nonce ||
		claims.Subject == "" || claims.Email == "" {
		s.recordLoginFailure(ctx, remoteIP)
		return nil, cfg, httperr.Unauthorized("invalid OIDC identity")
	}
	if cfg.RequireEmailVerified && !claims.EmailVerified {
		s.recordLoginFailure(ctx, remoteIP)
		s.recordOIDCAudit(ctx, cfg, "auth.oidc.denied", "email is not verified")
		return nil, cfg, httperr.Forbidden("email is not verified")
	}
	if !emailAllowed(cfg.AllowedEmailDomains, claims.Email) {
		s.recordLoginFailure(ctx, remoteIP)
		s.recordOIDCAudit(ctx, cfg, "auth.oidc.denied", "email domain is not allowed")
		return nil, cfg, httperr.Forbidden("email is not allowed")
	}

	identity, err := s.Store.GetOidcIdentity(ctx, claims.Issuer, claims.Subject)
	if err != nil {
		return nil, cfg, err
	}
	var user *model.User
	if identity != nil {
		user, err = s.Store.GetUser(ctx, identity.UserID)
	} else {
		user, err = s.Store.GetUserByEmail(ctx, claims.Email)
		if err == nil && user != nil && cfg.TenantID != "" && user.TenantID != cfg.TenantID {
			user = nil
		}
	}
	if err != nil {
		return nil, cfg, err
	}
	if user == nil || user.Status != model.UserStatusActive {
		s.recordLoginFailure(ctx, remoteIP)
		s.recordOIDCAudit(ctx, cfg, "auth.oidc.denied", "no active provisioned user")
		return nil, cfg, httperr.Forbidden("OIDC account is not provisioned")
	}
	tenant, err := s.Store.GetTenant(ctx, user.TenantID)
	if err != nil {
		return nil, cfg, err
	}
	if tenant == nil || tenant.Status != model.TenantStatusActive {
		s.recordOIDCAudit(ctx, cfg, "auth.oidc.denied", "workspace is disabled")
		return nil, cfg, httperr.New(403, 40301, "workspace is disabled")
	}
	if cfg.TenantID != "" && user.TenantID != cfg.TenantID {
		s.recordOIDCAudit(ctx, cfg, "auth.oidc.denied", "tenant binding mismatch")
		return nil, cfg, httperr.Forbidden("OIDC account is not provisioned")
	}
	if identity == nil {
		identity = &model.OidcIdentity{
			ID: id.New(), Issuer: claims.Issuer, Subject: claims.Subject,
			UserID: user.ID, Email: claims.Email, LastLoginAt: time.Now().UTC(),
		}
	} else if identity.UserID != user.ID {
		s.recordOIDCAudit(ctx, cfg, "auth.oidc.denied", "identity ownership conflict")
		return nil, cfg, httperr.Forbidden("OIDC identity is already linked to another account")
	} else {
		identity.Email = claims.Email
		identity.LastLoginAt = time.Now().UTC()
	}
	if err := s.Store.UpsertOidcIdentity(ctx, identity); err != nil {
		return nil, cfg, err
	}

	s.clearLoginFailures(ctx, remoteIP)
	access, refresh, jti, refreshExp, err := s.JWT.IssuePair(user.ID, user.TenantID, user.Username, user.Role)
	if err != nil {
		return nil, cfg, err
	}
	if err := s.Store.CreateRefreshToken(ctx, &model.RefreshToken{
		ID: jti, UserID: user.ID, TenantID: user.TenantID,
		TokenHash: refreshTokenHash(refresh), ExpiresAt: time.Unix(refreshExp, 0),
	}); err != nil {
		return nil, cfg, err
	}
	if err := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: user.TenantID, UserID: user.ID, Action: "auth.oidc.login",
		Resource: "auth", ResourceID: user.ID, IP: remoteIP,
	}); err != nil {
		_ = s.Store.RevokeRefreshToken(ctx, jti)
		return nil, cfg, fmt.Errorf("failed to persist authentication audit: %w", err)
	}
	return &TokenResult{Token: access, Refresh: refresh, User: user}, cfg, nil
}

func (s *Service) enabledOIDCConfig(ctx context.Context) (config.OIDC, error) {
	cfg := s.oidcSettings()
	if !cfg.Enabled || strings.TrimSpace(cfg.Issuer) == "" || strings.TrimSpace(cfg.ClientID) == "" ||
		strings.TrimSpace(cfg.ClientSecret) == "" || strings.TrimSpace(cfg.RedirectURL) == "" {
		return cfg, httperr.New(400, 40140, "OIDC login is not configured")
	}
	client := s.currentOIDCClient()
	resolved, err := client.ResolveEndpoints(ctx, cfg)
	if err != nil {
		return cfg, err
	}
	return normalizeOIDC(resolved), nil
}

func (s *Service) currentOIDCClient() OIDCClient {
	s.oidcMu.Lock()
	defer s.oidcMu.Unlock()
	if s.oidcClient == nil {
		cfg := normalizeOIDC(s.oidcConfig)
		client := *s.httpClient
		client.Timeout = time.Duration(cfg.HTTPTimeoutSec) * time.Second
		s.oidcClient = newHTTPOIDCClient(&client, s.allowPrivateProviderBaseURL)
	}
	return s.oidcClient
}

func (s *Service) recordOIDCAudit(ctx context.Context, cfg config.OIDC, action, detail string) {
	tenantID := cfg.TenantID
	if tenantID == "" {
		tenantID = model.PlatformTenantID
	}
	_ = s.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, Action: action, Resource: "auth", DetailJSON: detail,
	})
}

func emailAllowed(domains []string, email string) bool {
	if len(domains) == 0 {
		return true
	}
	at := strings.LastIndex(email, "@")
	if at <= 0 {
		return false
	}
	domain := strings.ToLower(email[at+1:])
	for _, allowed := range domains {
		if strings.EqualFold(strings.TrimSpace(allowed), domain) {
			return true
		}
	}
	return false
}
