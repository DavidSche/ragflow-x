package handler

import (
	"io"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// requireSetup writes a 500 when the setup manager is unavailable (bootstrap
// mode) — these endpoints are only reachable on an operational engine.
func (h *Handler) requireSetup(c *gin.Context) bool {
	if h.setupMgr == nil {
		response.Fail(c, 500, 500, "setup manager not configured")
		return false
	}
	return true
}

// SystemConfigView returns the current connection settings with secrets masked.
// RBAC restricts this to platform admins (system resource).
func (h *Handler) SystemConfigView(c *gin.Context) {
	if !h.requireSetup(c) {
		return
	}
	view, err := h.setupMgr.ConfigView()
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// SystemConfigPublicKey returns the RSA public key used to encrypt secrets
// submitted to the protected config endpoints.
func (h *Handler) SystemConfigPublicKey(c *gin.Context) {
	if !h.requireSetup(c) {
		return
	}
	response.OK(c, gin.H{"public_key": h.setupMgr.PublicKeyPEM()})
}

// SystemConfigPreflight validates the proposed database/RAGFlow settings
// without persisting anything.
func (h *Handler) SystemConfigPreflight(c *gin.Context) {
	if !h.requireSetup(c) {
		return
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.Fail(c, 400, 40000, "invalid config payload")
		return
	}
	res, err := h.setupMgr.PreflightRaw(c.Request.Context(), raw)
	if err != nil {
		logger.Warn("system config preflight failed", "error", err)
		response.Fail(c, 400, 40005, err.Error())
		return
	}
	response.OK(c, res)
}

// SystemConfigApply persists the submitted settings and hot-swaps the running
// engine (in-place reconnect, same as the first-run wizard).
func (h *Handler) SystemConfigApply(c *gin.Context) {
	if !h.requireSetup(c) {
		return
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		response.Fail(c, 400, 40000, "invalid config payload")
		return
	}
	if err := h.setupMgr.ApplyRaw(c.Request.Context(), raw); err != nil {
		logger.Warn("system config apply failed", "error", err)
		response.Fail(c, 400, 40005, err.Error())
		return
	}
	response.OK(c, gin.H{"configured": true})
}
