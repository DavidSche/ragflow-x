package handler

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func (h *Handler) CreateTraceRun(c *gin.Context) {
	var req service.TraceRunInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid trace run payload")
		return
	}
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	run, err := h.Service.CreateTraceRun(c.Request.Context(), userID, tenantID, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	detail, _ := json.Marshal(map[string]string{"trace_id": run.TraceID, "status": run.Status})
	h.RecordAudit(c.Request.Context(), &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "trace-run.create", Resource: "knowledge-ops",
		ResourceID: run.ID, DetailJSON: string(detail), IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, run)
}

func (h *Handler) ListTraceRuns(c *gin.Context) {
	page, pageSize := pageParams(c)
	tenantID, _ := knowledgeOpsScope(h, c)
	out, total, err := h.Service.ListTraceRuns(c.Request.Context(), c.GetString(middleware.ContextUserID), tenantID, page, pageSize, repository.TraceRunFilter{
		TraceID: c.Query("trace_id"), RequestID: c.Query("request_id"), SessionID: c.Query("session_id"),
		AssistantID: c.Query("assistant_id"), AppType: c.Query("app_type"), Status: c.Query("status"),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, out, total, page, pageSize)
}

func (h *Handler) GetTraceRun(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	out, err := h.Service.GetTraceRun(c.Request.Context(), c.GetString(middleware.ContextUserID), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, out)
}
