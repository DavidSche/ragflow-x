package handler

import (
	"github.com/gin-gonic/gin"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ListTasks returns the tenant's operator task queue.
func (h *Handler) ListTasks(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	page, pageSize := pageParams(c)
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "task"); err != nil {
		response.Err(c, err)
		return
	}
	items, total, err := h.Service.ListTasks(c.Request.Context(), tenantID, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

// SyncTasks schedules a RAGFlow progress-sync job on the async worker (A2):
// the sync runs off the request path and its outcome is observable on
// GET /tasks/jobs with retry/backoff covered by the worker.
func (h *Handler) SyncTasks(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "task"); err != nil {
		response.Err(c, err)
		return
	}
	inserted, err := h.Service.EnqueueDocumentSync(ctx, tenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "task.sync", Resource: "task", ResourceID: tenantID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"enqueued": inserted})
}

// DeleteTasks batch-deletes terminal task history records.
func (h *Handler) DeleteTasks(c *gin.Context) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "ids is required")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "task"); err != nil {
		response.Err(c, err)
		return
	}
	deleted, err := h.Service.DeleteTasks(ctx, tenantID, req.IDs)
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "task.delete.batch", Resource: "task",
		ResourceID: tenantID, DetailJSON: strings.Join(req.IDs, ","), IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, gin.H{"deleted": deleted})
}

// ListJobs returns a page of the tenant's async job execution records (worker
// observability; doc/33 A2). kind/status query filters narrow the view.
func (h *Handler) ListJobs(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "task"); err != nil {
		response.Err(c, err)
		return
	}
	page, pageSize := pageParams(c)
	items, total, err := h.Service.ListJobs(c.Request.Context(), tenantID, c.Query("kind"), c.Query("status"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

// Usage returns metered usage rows, optionally filtered by date.
func (h *Handler) Usage(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	date := c.Query("date")
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "usage"); err != nil {
		response.Err(c, err)
		return
	}
	items, err := h.Service.UsageSummary(c.Request.Context(), tenantID, date)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// UsageDetail returns a page of per-request metering detail rows (model,
// scenario, session, dataset). Platform admins may request scope=all.
func (h *Handler) UsageDetail(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "usage"); err != nil {
		response.Err(c, err)
		return
	}
	scopeAll := c.Query("scope") == "all" && h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "governance.read", "tenant") == nil
	page, pageSize := pageParams(c)
	filter := repository.CostMetricFilter{
		Model:     c.Query("model"),
		Scenario:  c.Query("scenario"),
		SessionID: c.Query("session_id"),
		UserID:    c.Query("user_id"),
		KeyID:     c.Query("key_id"),
		ChatID:    c.Query("chat_id"),
		TenantID:  c.Query("tenant_id"),
		DateFrom:  c.Query("date_from"),
		DateTo:    c.Query("date_to"),
	}
	items, total, err := h.Service.ListUsageDetail(c.Request.Context(), tenantID, scopeAll, page, pageSize, filter)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}
