package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// GetModelDefaults returns the platform default models with per-type four-state
// status (ok / invalid / verify_failed / unset, doc/269 §4.3). Response only
// exposes model identity — never provider config or API keys.
func (h *Handler) GetModelDefaults(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "model-default"); err != nil {
		response.Err(c, err)
		return
	}
	// Never cache: the current default can be changed out-of-band (RAGFlow
	// console or another client) and doc/269 P1-6 requires the next read to show
	// the upstream truth, not a proxied/browser copy.
	c.Header("Cache-Control", "no-store")
	items, err := h.Service.GetTenantDefaultModels(c.Request.Context())
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// GetModelDefaultOptions returns the per-type candidate model tree for the
// default-model panel (doc/269 §4.4). Read-only; authorized read:model-default.
func (h *Handler) GetModelDefaultOptions(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "model-default"); err != nil {
		response.Err(c, err)
		return
	}
	// Same no-store rule as GET defaults: the candidate tree follows the live
	// RAGFlow model inventory.
	c.Header("Cache-Control", "no-store")
	options, err := h.Service.ListDefaultModelOptions(c.Request.Context())
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, options)
}

// SetModelDefault sets or clears a platform default model for a model type
// (doc/269 §4.3). The body must carry the CAS declaration expected_model_id
// (three-state: absent field / explicit null / exact value); the safe-write UI
// always sends it and the service refuses a missing declaration.
func (h *Handler) SetModelDefault(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(ctx, userID, "manage", "model-default"); err != nil {
		response.Err(c, err)
		return
	}
	var req service.SetDefaultRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Err(c, httperr.BadRequest(40000, "请求体非法: "+err.Error()))
		return
	}
	result, err := h.Service.SetTenantDefaultModel(ctx, tenantID, userID, req.ModelType, req.ModelID, &req)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}
