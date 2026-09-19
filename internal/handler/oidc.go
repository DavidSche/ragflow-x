package handler

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

const (
	oidcStateCookie = "rgx_oidc_state"
	oidcNonceCookie = "rgx_oidc_nonce"
	oidcFlowTTL     = 10 * time.Minute
)

func randomOIDCValue() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// OIDCStatus tells the login page whether the deployment offers enterprise SSO.
func (h *Handler) OIDCStatus(c *gin.Context) {
	cfg := h.Service.OIDCConfig()
	response.OK(c, gin.H{
		"enabled":    cfg.Enabled,
		"issuer":     cfg.Issuer,
		"login_path": "/api/v1/auth/oidc/start",
	})
}

// OIDCStart redirects to the configured IdP with a state/nonce CSRF pair.
func (h *Handler) OIDCStart(c *gin.Context) {
	state, err := randomOIDCValue()
	if err != nil {
		response.Fail(c, http.StatusInternalServerError, 50000, "failed to create OIDC state")
		return
	}
	nonce, err := randomOIDCValue()
	if err != nil {
		response.Fail(c, http.StatusInternalServerError, 50000, "failed to create OIDC nonce")
		return
	}
	authURL, _, err := h.Service.OIDCAuthURL(c.Request.Context(), state, nonce)
	if err != nil {
		response.Err(c, err)
		return
	}
	setOIDCFlowCookies(c, state, nonce)
	c.Redirect(http.StatusFound, authURL)
}

// OIDCCallback verifies state, exchanges the authorization code, and converts
// the verified enterprise identity into a normal RAGFlow-X session.
func (h *Handler) OIDCCallback(c *gin.Context) {
	state := c.Query("state")
	code := c.Query("code")
	storedState, _ := c.Cookie(oidcStateCookie)
	nonce, _ := c.Cookie(oidcNonceCookie)
	clearOIDCFlowCookies(c)
	if code == "" || state == "" || storedState == "" || nonce == "" ||
		state != storedState || len(state) < 32 || len(nonce) < 32 {
		response.Fail(c, http.StatusBadRequest, 40141, "invalid OIDC callback state")
		return
	}
	res, cfg, err := h.Service.LoginWithOIDC(c.Request.Context(), code, nonce, c.ClientIP())
	if err != nil {
		response.Err(c, err)
		return
	}
	setAuthCookies(c, res.Token, res.Refresh)
	c.Redirect(http.StatusFound, cfg.PostLoginPath)
}

func setOIDCFlowCookies(c *gin.Context, state, nonce string) {
	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oidcStateCookie, state, int(oidcFlowTTL.Seconds()), "/api/v1/auth/oidc", "", secure, true)
	c.SetCookie(oidcNonceCookie, nonce, int(oidcFlowTTL.Seconds()), "/api/v1/auth/oidc", "", secure, true)
}

func clearOIDCFlowCookies(c *gin.Context) {
	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(oidcStateCookie, "", -1, "/api/v1/auth/oidc", "", secure, true)
	c.SetCookie(oidcNonceCookie, "", -1, "/api/v1/auth/oidc", "", secure, true)
}
