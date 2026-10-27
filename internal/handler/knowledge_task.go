package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func (h *Handler) CreateKnowledgeTask(c *gin.Context) {
	var req service.KnowledgeTaskInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid knowledge task payload")
		return
	}
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	task, err := h.Service.CreateKnowledgeTask(c.Request.Context(), tenantID, userID, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(c.Request.Context(), &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "knowledge-task.create", Resource: "knowledge-ops",
		ResourceID: task.ID, DetailJSON: task.Category, IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, task)
}

func (h *Handler) GetKnowledgeTask(c *gin.Context) {
	task, err := h.Service.GetKnowledgeTask(
		c.Request.Context(), c.GetString(middleware.ContextUserID),
		c.GetString(middleware.ContextTenantID), c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, task)
}

func (h *Handler) ListKnowledgeTasks(c *gin.Context) {
	page, pageSize := pageParams(c)
	tenantID, _ := knowledgeOpsScope(h, c)
	out, total, err := h.Service.ListKnowledgeTasks(c.Request.Context(), c.GetString(middleware.ContextUserID), tenantID, page, pageSize, repository.KnowledgeTaskFilter{
		Status: c.Query("status"), Category: c.Query("category"), Attribution: c.Query("attribution"),
		OwnerID: c.Query("owner_id"), SourceEventID: c.Query("source_event_id"),
		Search: c.Query("search"), Overdue: c.Query("overdue") == "true",
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, out, total, page, pageSize)
}

func (h *Handler) KnowledgeTaskSummary(c *gin.Context) {
	tenantID, _ := knowledgeOpsScope(h, c)
	out, err := h.Service.KnowledgeTaskSummary(c.Request.Context(), c.GetString(middleware.ContextUserID), tenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, out)
}

func (h *Handler) UpdateKnowledgeTask(c *gin.Context) {
	var req service.KnowledgeTaskUpdateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid knowledge task payload")
		return
	}
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	task, err := h.Service.UpdateKnowledgeTask(c.Request.Context(), userID, tenantID, c.Param("id"), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(c.Request.Context(), &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "knowledge-task.update", Resource: "knowledge-ops",
		ResourceID: task.ID, DetailJSON: task.Status, IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, task)
}
