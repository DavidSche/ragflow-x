package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/pkg/password"
)

// SystemTenantID is the reserved platform tenant owning built-in admin users.
const SystemTenantID = model.PlatformTenantID

// ErrInvalidCredentials is returned on failed authentication.
var ErrInvalidCredentials = httperr.New(401, 40101, "invalid username or password")

const (
	loginMaxAttempts   = 10
	loginWindowMinutes = 15
)

// TokenResult is the payload returned by Login.
type TokenResult struct {
	Token   string      `json:"token"`
	Refresh string      `json:"refresh_token"`
	User    *model.User `json:"user"`
}

// Login validates credentials, enforces an in-memory per-source rate limit for
// failed attempts, and records a login audit event on success.
func (s *Service) Login(ctx context.Context, username, pass, remoteIP string) (*TokenResult, error) {
	if !s.loginPermitted(ctx, remoteIP) {
		return nil, httperr.New(429, 429, "too many login attempts, please retry later")
	}
	u, err := s.Store.GetUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if u == nil || u.Status != model.UserStatusActive || !password.Verify(u.PasswordHash, pass) {
		s.recordLoginFailure(ctx, remoteIP)
		return nil, ErrInvalidCredentials
	}
	tenant, err := s.Store.GetTenant(ctx, u.TenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil || tenant.Status != model.TenantStatusActive {
		return nil, httperr.New(403, 40301, "workspace is disabled")
	}
	s.clearLoginFailures(ctx, remoteIP)
	access, refresh, jti, refreshExp, err := s.JWT.IssuePair(u.ID, u.TenantID, u.Username, u.Role)
	if err != nil {
		return nil, err
	}
	if err := s.Store.CreateRefreshToken(ctx, &model.RefreshToken{
		ID: jti, UserID: u.ID, TenantID: u.TenantID,
		TokenHash: refreshTokenHash(refresh), ExpiresAt: time.Unix(refreshExp, 0),
	}); err != nil {
		return nil, err
	}
	if err := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: u.TenantID, UserID: u.ID, Action: "auth.login", Resource: "auth",
		ResourceID: u.ID, IP: remoteIP,
	}); err != nil {
		_ = s.Store.RevokeRefreshToken(ctx, jti)
		return nil, fmt.Errorf("failed to persist authentication audit: %w", err)
	}
	return &TokenResult{Token: access, Refresh: refresh, User: u}, nil
}

const loginWindow = loginWindowMinutes * time.Minute

// loginPermitted returns whether an attempt is still under the per-source
// limit within the sliding window. It fails open if the configured limiter
// backend is temporarily unavailable so login is not blocked by the limiter
// itself; the degradation is surfaced via a Prometheus counter and a notify
// event so the security-relevant fail-open window stays observable.
func (s *Service) loginPermitted(ctx context.Context, key string) bool {
	n, err := s.LoginLimiter.Count(ctx, key, loginWindow)
	if err != nil {
		logger.Warn("login rate limiter unavailable; allowing attempt", "error", err)
		obs.Get().IncLimiterFailOpen()
		notify.Emit(ctx, notify.Event{
			Title: "登录限速器不可用，已降级放行", Severity: "warn", Type: "limiter_failopen",
			Resource: "auth", ResourceID: key, Detail: err.Error(),
		})
		return true
	}
	return n < loginMaxAttempts
}

func (s *Service) recordLoginFailure(ctx context.Context, key string) {
	_, _ = s.LoginLimiter.Incr(ctx, key, loginWindow)
}

func (s *Service) clearLoginFailures(ctx context.Context, key string) {
	_ = s.LoginLimiter.Reset(ctx, key)
}

func refreshTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// RefreshAccessToken rotates a refresh token: it validates + revokes the old
// token and issues a new access + refresh pair, so a stolen refresh token
// cannot be reused (rotation). Reuse of an already-rotated/revoked token is
// rejected.
func (s *Service) RefreshAccessToken(ctx context.Context, token string) (*TokenResult, error) {
	claims, err := s.JWT.Parse(token)
	if err != nil || claims.TokenType != jwt.TokenRefresh {
		return nil, httperr.Unauthorized("invalid refresh token")
	}
	stored, err := s.Store.FindRefreshTokenByHash(ctx, refreshTokenHash(token))
	if err != nil {
		return nil, err
	}
	if stored == nil || stored.RevokedAt != nil || time.Now().After(stored.ExpiresAt) {
		return nil, httperr.Unauthorized("invalid or revoked refresh token")
	}
	u, err := s.Store.GetUser(ctx, stored.UserID)
	if err != nil {
		return nil, err
	}
	if u == nil || u.Status != model.UserStatusActive {
		return nil, httperr.Unauthorized("user disabled or missing")
	}
	tenant, err := s.Store.GetTenant(ctx, u.TenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil || tenant.Status != model.TenantStatusActive {
		return nil, httperr.New(403, 40301, "workspace is disabled")
	}
	claimed, err := s.Store.RevokeActiveRefreshToken(ctx, stored.ID, time.Now())
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, httperr.Unauthorized("invalid or revoked refresh token")
	}
	access, refresh, jti, refreshExp, err := s.JWT.IssuePair(u.ID, u.TenantID, u.Username, u.Role)
	if err != nil {
		return nil, err
	}
	if err := s.Store.CreateRefreshToken(ctx, &model.RefreshToken{
		ID: jti, UserID: u.ID, TenantID: u.TenantID,
		TokenHash: refreshTokenHash(refresh), ExpiresAt: time.Unix(refreshExp, 0),
	}); err != nil {
		return nil, err
	}
	if err := s.RecordAudit(ctx, &model.AuditLog{TenantID: u.TenantID, UserID: u.ID, Action: "auth.refresh", Resource: "auth", ResourceID: u.ID}); err != nil {
		_ = s.Store.RevokeRefreshToken(ctx, jti)
		return nil, fmt.Errorf("failed to persist authentication audit: %w", err)
	}
	return &TokenResult{Token: access, Refresh: refresh, User: u}, nil
}

// Logout revokes the given refresh token, ending the session.
func (s *Service) Logout(ctx context.Context, token string) error {
	claims, err := s.JWT.Parse(token)
	if err != nil || claims.TokenType != jwt.TokenRefresh {
		return nil
	}
	stored, err := s.Store.FindRefreshTokenByHash(ctx, refreshTokenHash(token))
	if err != nil {
		return err
	}
	if stored != nil {
		return s.Store.RevokeRefreshToken(ctx, stored.ID)
	}
	return nil
}

// BootstrapAdmin creates the system tenant and a default platform admin when
// none exist. It is idempotent and intended for local development/bootstrap.
func (s *Service) BootstrapAdmin(ctx context.Context, username, pass string) error {
	tenant, err := s.Store.GetTenant(ctx, SystemTenantID)
	if err != nil {
		return err
	}
	if tenant == nil {
		tenant = &model.Tenant{ID: SystemTenantID, Name: "Platform", Status: model.TenantStatusActive}
		if err := s.Store.CreateTenant(ctx, tenant); err != nil {
			return err
		}
	}

	existing, err := s.Store.GetUserByUsername(ctx, username)
	if err != nil {
		return err
	}
	if existing != nil {
		// Reconcile the designated super-admin to platform_admin so an admin can
		// never get locked out of role/user management.
		if existing.Role != model.RolePlatformAdmin || existing.Status != model.UserStatusActive {
			existing.Role = model.RolePlatformAdmin
			existing.Status = model.UserStatusActive
			if err := s.Store.UpdateUser(ctx, existing); err != nil {
				return err
			}
		}
		if err := s.Store.SetUserPrimaryRole(ctx, existing.ID, model.RolePlatformAdmin); err != nil {
			return err
		}
		return nil
	}

	hash, err := password.Hash(pass)
	if err != nil {
		return err
	}
	u := &model.User{ID: id.New(), TenantID: SystemTenantID, Username: username, PasswordHash: hash, Role: model.RolePlatformAdmin, Status: model.UserStatusActive}
	if err := s.Store.CreateUser(ctx, u); err != nil {
		return err
	}
	return s.Store.SetUserPrimaryRole(ctx, u.ID, model.RolePlatformAdmin)
}
