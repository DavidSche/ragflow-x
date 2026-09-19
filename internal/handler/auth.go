package handler

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/gin-gonic/gin"
	"net/http"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

const (
	accessCookieName  = "rgx_access"
	refreshCookieName = "rgx_refresh"
)

type loginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// Login authenticates a user and returns a JWT.
func (h *Handler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid login payload")
		return
	}
	res, err := h.Service.Login(c.Request.Context(), req.Username, req.Password, c.ClientIP())
	if err != nil {
		response.Err(c, err)
		return
	}
	setAuthCookies(c, res.Token, res.Refresh)
	response.OK(c, res)
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Refresh rotates the refresh token and returns a fresh access + refresh pair.
func (h *Handler) Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid payload")
		return
	}
	token := req.RefreshToken
	if token == "" {
		cookieToken, err := c.Cookie(refreshCookieName)
		if err == nil {
			token = cookieToken
		}
	}
	if token == "" {
		response.Fail(c, 400, 40000, "refresh token is required")
		return
	}
	res, err := h.Service.RefreshAccessToken(c.Request.Context(), token)
	if err != nil {
		response.Err(c, err)
		return
	}
	setAuthCookies(c, res.Token, res.Refresh)
	response.OK(c, res)
}

// Logout revokes the presented refresh token, ending the session.
func (h *Handler) Logout(c *gin.Context) {
	var req refreshRequest
	if c.Request.Body != nil {
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 4096))
		if err != nil {
			response.Fail(c, 400, 40000, "invalid payload")
			return
		}
		if len(bytes.TrimSpace(body)) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				response.Fail(c, 400, 40000, "invalid payload")
				return
			}
		}
	}
	token := req.RefreshToken
	if token == "" {
		cookieToken, err := c.Cookie(refreshCookieName)
		if err == nil {
			token = cookieToken
		}
	}
	if token != "" {
		_ = h.Service.Logout(c.Request.Context(), token)
	}
	setAuthCookies(c, "", "")
	response.OK(c, gin.H{"ok": true})
}

func setAuthCookies(c *gin.Context, accessToken, refreshToken string) {
	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	if accessToken == "" && refreshToken == "" {
		const maxAge = -1
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie(accessCookieName, "", maxAge, "/", "", secure, true)
		c.SetCookie(refreshCookieName, "", maxAge, "/", "", secure, true)
		return
	}
	const maxAge = 0
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(accessCookieName, accessToken, maxAge, "/", "", secure, true)
	c.SetCookie(refreshCookieName, refreshToken, maxAge, "/", "", secure, true)
}

// Me returns the authenticated user profile.
func (h *Handler) Me(c *gin.Context) {
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(c.Request.Context(), userID, "read", "user"); err != nil {
		response.Err(c, err)
		return
	}
	u, err := h.Service.CurrentUser(c.Request.Context(), userID)
	if err != nil {
		response.Err(c, err)
		return
	}
	if u == nil {
		response.Fail(c, 404, 404, "user not found")
		return
	}
	mp, err := h.Service.MePermissions(c.Request.Context(), userID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{
		"id": u.ID, "username": u.Username, "email": u.Email,
		"tenant_id": u.TenantID, "role": u.Role,
		"permissions":      mp.Permissions,
		"platform":         mp.Platform,
		"approval_enabled": h.Service.ApprovalConfig().Enabled,
	})
}

var _ = service.TokenResult{}
