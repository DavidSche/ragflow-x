// Package jwt issues and validates application JWTs.
package jwt

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Token types used to distinguish short-lived access tokens from rotating
// refresh tokens.
const (
	TokenAccess  = "access"
	TokenRefresh = "refresh"
)

// MinSecretLen is the minimum accepted HMAC signing secret length. Shorter
// secrets are brute-forceable offline once a token leaks.
const MinSecretLen = 32

// insecureSecrets lists well-known placeholder values shipped in templates.
var insecureSecrets = map[string]bool{
	"change-me":         true,
	"change-me-in-prod": true,
	"changeme":          true,
	"secret":            true,
}

// ValidateSecret enforces the production baseline for the JWT signing secret:
// non-empty, not a documented placeholder, and at least MinSecretLen bytes.
// It is called at engine-build time so an unsafe configuration refuses to
// start instead of silently issuing forgeable tokens.
func ValidateSecret(secret string) error {
	s := strings.TrimSpace(secret)
	if s == "" {
		return errors.New("jwt secret is empty: set app.jwt_secret or RGX_JWT_SECRET (or let the server generate and persist one)")
	}
	if insecureSecrets[strings.ToLower(s)] {
		return fmt.Errorf("jwt secret is a known placeholder %q: generate a strong secret (e.g. openssl rand -hex 32) and set RGX_JWT_SECRET", s)
	}
	if len(s) < MinSecretLen {
		return fmt.Errorf("jwt secret is too weak (%d bytes, need >= %d): generate one with openssl rand -hex 32 and set RGX_JWT_SECRET", len(s), MinSecretLen)
	}
	return nil
}

// Claims is the application claim set.
type Claims struct {
	UserID    string `json:"uid"`
	TenantID  string `json:"tid"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	TokenType string `json:"typ"`
	jwt.RegisteredClaims
}

// Manager signs and verifies tokens.
type Manager struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// NewManager creates a token manager. expireHours sets the access token TTL;
// the refresh token defaults to seven days.
func NewManager(secret string, expireHours int) *Manager {
	if expireHours <= 0 {
		expireHours = 24
	}
	return &Manager{
		secret:     []byte(secret),
		accessTTL:  time.Duration(expireHours) * time.Hour,
		refreshTTL: 7 * 24 * time.Hour,
	}
}

// SetAccessTTL overrides the access token lifetime.
func (m *Manager) SetAccessTTL(d time.Duration) {
	if d > 0 {
		m.accessTTL = d
	}
}

// SetRefreshTTL overrides the refresh token lifetime.
func (m *Manager) SetRefreshTTL(d time.Duration) {
	if d > 0 {
		m.refreshTTL = d
	}
}

// Issue creates a signed access token for the claims.
func (m *Manager) Issue(userID, tenantID, username, role string) (string, error) {
	return m.issue(userID, tenantID, username, role, TokenAccess, m.accessTTL, "")
}

// IssueRefresh creates a signed refresh token with its jti set to id.
func (m *Manager) IssueRefresh(userID, tenantID, username, role, id string) (string, error) {
	return m.issue(userID, tenantID, username, role, TokenRefresh, m.refreshTTL, id)
}

// IssueAccess creates a signed access token, suitable when using the explicit
// pair helper is not needed.
func (m *Manager) IssueAccess(userID, tenantID, username, role string) (string, error) {
	return m.issue(userID, tenantID, username, role, TokenAccess, m.accessTTL, "")
}

// IssuePair returns an access token and a rotating refresh token, plus the
// refresh token's id (jti) and expiry. The caller persists the refresh token
// (e.g. hashed) to support rotation and revocation.
func (m *Manager) IssuePair(userID, tenantID, username, role string) (access, refresh string, refreshJTI string, refreshExpiresAt int64, err error) {
	refreshJTI = uuid.NewString()
	refreshExpiresAt = time.Now().Add(m.refreshTTL).Unix()
	access, err = m.IssueAccess(userID, tenantID, username, role)
	if err != nil {
		return "", "", "", 0, err
	}
	refresh, err = m.IssueRefresh(userID, tenantID, username, role, refreshJTI)
	if err != nil {
		return "", "", "", 0, err
	}
	return access, refresh, refreshJTI, refreshExpiresAt, nil
}

func (m *Manager) issue(userID, tenantID, username, role, tokenType string, ttl time.Duration, id string) (string, error) {
	claims := Claims{
		UserID: userID, TenantID: tenantID, Username: username, Role: role, TokenType: tokenType,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ID:        id,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

// Parse validates a token and returns its claims.
func (m *Manager) Parse(token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return m.secret, nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
