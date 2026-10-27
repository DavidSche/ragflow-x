package handler

import (
	"github.com/gin-gonic/gin"

	"io"

	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

type setupAdminRequest struct {
	Username    string `json:"username" binding:"required"`
	Password    string `json:"password"`
	PasswordEnc string `json:"password_enc"`
}

type preflightRequest struct {
	BaseURL string `json:"base_url" binding:"required"`
	APIKey  string `json:"api_key" binding:"required"`
}

// SetupStatus returns the first-run state of the platform so the setup wizard
// can decide whether to ask for the first admin.
func (h *Handler) SetupStatus(c *gin.Context) {
	status, err := h.Service.SetupStatus(c.Request.Context())
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, status)
}

// SetupPublicKey returns the RSA public key used by the setup wizard to encrypt
// secrets submitted to preflight/apply.
func (h *Handler) SetupPublicKey(c *gin.Context) {
	if h.setupMgr == nil {
		response.Fail(c, 500, 500, "setup manager not configured")
		return
	}
	response.OK(c, gin.H{"public_key": h.setupMgr.PublicKeyPEM()})
}

// SetupApply persists the wizard configuration and hot-swaps the running engine.
func (h *Handler) SetupApply(c *gin.Context) {
	if h.setupMgr == nil {
		response.Fail(c, 500, 500, "setup manager not configured")
		return
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.Fail(c, 400, 40000, "invalid setup payload")
		return
	}
	if err := h.setupMgr.ApplyRaw(c.Request.Context(), raw); err != nil {
		logger.Warn("setup apply failed", "error", err)
		response.Fail(c, 400, 40004, err.Error())
		return
	}
	response.OK(c, gin.H{"configured": true})
}

// SetupAdmin creates the first platform admin. It is only effective before
// the system is initialized.
func (h *Handler) SetupAdmin(c *gin.Context) {
	var req setupAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid setup payload")
		return
	}
	password := req.Password
	if req.PasswordEnc != "" {
		if h.rsa == nil {
			response.Fail(c, 500, 500, "setup encryption key not configured")
			return
		}
		dec, err := h.rsa.Decrypt(req.PasswordEnc)
		if err != nil {
			response.Fail(c, 400, 40000, "invalid encrypted password")
			return
		}
		password = dec
	}
	if err := h.Service.CreateFirstAdmin(c.Request.Context(), req.Username, password); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"initialized": true})
}

// Preflight validates a proposed RAGFlow connection without persisting any
// secret, so the operator can confirm the endpoint before finalizing config.
func (h *Handler) Preflight(c *gin.Context) {
	var req preflightRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid preflight payload")
		return
	}
	res, err := h.Service.PreflightRAGFlow(c.Request.Context(), req.BaseURL, req.APIKey)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, res)
}
