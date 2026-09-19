package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func knowledgeOpsScope(h *Handler, c *gin.Context) (string, bool) {
	return c.GetString(middleware.ContextTenantID), h.Service.KnowledgeOpsScopeAll(c.Request.Context(), c.GetString(middleware.ContextUserID))
}

func knowledgeOpsRange(c *gin.Context) (string, string) {
	return c.Query("date_from"), c.Query("date_to")
}

// KnowledgeOpsSummary returns aggregate KPIs for a date range.
func (h *Handler) KnowledgeOpsSummary(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "knowledge-ops"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, scopeAll := knowledgeOpsScope(h, c)
	from, to := knowledgeOpsRange(c)
	out, err := h.Service.KnowledgeOpsSummary(c.Request.Context(), tenantID, scopeAll, from, to)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, out)
}

// KnowledgeOpsTopQueries returns the highest-volume questions in a date range.
func (h *Handler) KnowledgeOpsTopQueries(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "knowledge-ops"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, scopeAll := knowledgeOpsScope(h, c)
	from, to := knowledgeOpsRange(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	out, err := h.Service.TopKnowledgeQueries(c.Request.Context(), tenantID, scopeAll, from, to, limit)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, out)
}

// KnowledgeOpsEvents returns badcase candidates with review state.
func (h *Handler) KnowledgeOpsEvents(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "knowledge-ops"); err != nil {
		response.Err(c, err)
		return
	}
	page, pageSize := pageParams(c)
	tenantID, scopeAll := knowledgeOpsScope(h, c)
	out, total, err := h.Service.ListKnowledgeOpsEvents(c.Request.Context(), tenantID, scopeAll, page, pageSize, repository.KnowledgeOpsFilter{
		AppType:      c.Query("app_type"),
		AppID:        c.Query("app_id"),
		Status:       c.Query("status"),
		ReviewStatus: c.DefaultQuery("review_status", "open"),
		Search:       c.Query("search"),
		DateFrom:     c.Query("date_from"),
		DateTo:       c.Query("date_to"),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, out, total, page, pageSize)
}

type knowledgeOpsReviewPayload struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

// ReviewKnowledgeOpsEvent closes a badcase candidate.
func (h *Handler) ReviewKnowledgeOpsEvent(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "manage", "knowledge-ops"); err != nil {
		response.Err(c, err)
		return
	}
	var req knowledgeOpsReviewPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid review payload")
		return
	}
	tenantID, scopeAll := knowledgeOpsScope(h, c)
	err := h.Service.ReviewKnowledgeOpsEvent(c.Request.Context(), tenantID, c.Param("id"), scopeAll, repository.KnowledgeOpsReview{
		Status: req.Status,
		Note:   req.Note,
		Actor:  c.GetString(middleware.ContextUserID),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"id": c.Param("id"), "status": req.Status})
}
