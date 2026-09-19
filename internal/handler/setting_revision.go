package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func recordSettingAudit(h *Handler, c *gin.Context, action, resourceID, detail string) {
	_ = h.Service.RecordAudit(c.Request.Context(), &model.AuditLog{
		TenantID: c.GetString(middleware.ContextTenantID), UserID: c.GetString(middleware.ContextUserID),
		ActorTenantID: c.GetString(middleware.ContextTenantID), TargetTenantID: c.GetString(middleware.ContextTenantID),
		Action: action, Resource: "system-settings", ResourceID: resourceID, DetailJSON: detail,
		AuthorizationPermission: "manage:system", IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
}

func (h *Handler) SystemSettingsView(c *gin.Context) {
	view, err := h.Service.GetSystemSettings(c.Request.Context())
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

func (h *Handler) SystemSettingsUpdate(c *gin.Context) {
	var request service.SystemSettingsPatchRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Fail(c, 400, 40098, "invalid request")
		return
	}
	revision, err := h.Service.UpdateSystemSettings(c.Request.Context(), c.GetString(middleware.ContextUserID), request)
	if err != nil {
		response.Err(c, err)
		return
	}
	recordSettingAudit(h, c, "system_settings.updated", revision.ID, request.Note)
	response.OK(c, revision)
}

func (h *Handler) ListSystemSettingRevisions(c *gin.Context) {
	page, pageSize := pageParams(c)
	items, total, err := h.Service.ListSystemSettingRevisions(c.Request.Context(), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func (h *Handler) ReportRuntimeSettingState(c *gin.Context) {
	var request service.RuntimeSettingStateReport
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Fail(c, 400, 40098, "invalid request")
		return
	}
	state, err := h.Service.ReportRuntimeSettingState(c.Request.Context(), request)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, state)
}

func (h *Handler) GetSystemSettingRevision(c *gin.Context) {
	revision, err := h.Service.GetSystemSettingRevision(c.Request.Context(), c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, revision)
}

func (h *Handler) RollbackSystemSettingRevision(c *gin.Context) {
	var request struct {
		Confirmed bool   `json:"confirmed"`
		Note      string `json:"note"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Fail(c, 400, 40098, "invalid request")
		return
	}
	revision, err := h.Service.RollbackSystemSettings(c.Request.Context(), c.GetString(middleware.ContextUserID), c.Param("id"), request.Note, request.Confirmed)
	if err != nil {
		response.Err(c, err)
		return
	}
	recordSettingAudit(h, c, "system_settings.rolled_back", revision.ID, revision.Note)
	response.OK(c, revision)
}

func (h *Handler) UpdateSystemSettingSecret(c *gin.Context) {
	if h.rsa == nil {
		response.Fail(c, 500, 500, "system setting secret key is unavailable")
		return
	}
	var request service.SystemSettingSecretRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Fail(c, 400, 40098, "invalid request")
		return
	}
	plaintext, err := h.rsa.Decrypt(request.Secret)
	if err != nil {
		response.Fail(c, 400, 40098, "invalid encrypted secret")
		return
	}
	revision, err := h.Service.UpdateSystemSettingSecret(
		c.Request.Context(), c.GetString(middleware.ContextUserID), c.Param("key"), plaintext, request.Note, request.Confirmed,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	recordSettingAudit(h, c, "system_settings.secret_rotated", c.Param("key"), request.Note)
	response.OK(c, revision)
}
