package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// AuthVerifier is a minimal interface so middleware stays decoupled.
type AuthVerifier interface {
	Parse(token string) (*jwt.Claims, error)
}

// TenantLifecycleGate rechecks workspace status on every authenticated request.
type TenantLifecycleGate interface {
	EnsureTenantActive(ctx context.Context, tenantID string) error
}

const (
	// ContextUserID is the gin context key for the authenticated user id.
	ContextUserID = "user_id"
	// ContextTenantID is the gin context key for the authenticated tenant id.
	ContextTenantID = "tenant_id"
	// ContextRole is the gin context key for the authenticated role.
	ContextRole = "role"
	// ContextUsername is the gin context key for the authenticated username.
	ContextUsername = "username"
)

// Auth validates the Bearer token and stores identity in the context.
func Auth(verifier AuthVerifier, lifecycle TenantLifecycleGate) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		token, ok := bearerToken(header)
		if !ok {
			cookieToken, err := c.Cookie("rgx_access")
			if err != nil || cookieToken == "" {
				response.Fail(c, http.StatusUnauthorized, 401, "missing bearer token")
				c.Abort()
				return
			}
			token = cookieToken
		}
		claims, err := verifier.Parse(token)
		if err != nil {
			response.Fail(c, http.StatusUnauthorized, 401, "invalid or expired token")
			c.Abort()
			return
		}
		if claims.TokenType == jwt.TokenRefresh {
			response.Fail(c, http.StatusUnauthorized, 401, "refresh token cannot be used for access")
			c.Abort()
			return
		}
		c.Set(ContextUserID, claims.UserID)
		c.Set(ContextTenantID, claims.TenantID)
		c.Set(ContextRole, claims.Role)
		c.Set(ContextUsername, claims.Username)
		if lifecycle != nil {
			if err := lifecycle.EnsureTenantActive(c.Request.Context(), claims.TenantID); err != nil {
				response.Err(c, err)
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

func bearerToken(header string) (string, bool) {
	if !strings.HasPrefix(header, "Bearer ") {
		return "", false
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
	return token, token != ""
}
