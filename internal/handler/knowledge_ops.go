package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
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
	out, err := h.Service.KnowledgeOpsSummary(
		c.Request.Context(), tenantID, scopeAll, from, to, c.Query("x_dataset_id"),
	)
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

// KnowledgeOpsAnswerQuality returns the Q&A proxy metrics (doc/301 §5.9 QA-3 / T20).
//
// It is a *proxy* board and the shape says so: every rate travels with the counts it was
// derived from, `null` means "no denominator", and the `bias` block carries feedback
// coverage so the page can print the caveat next to the number instead of in a footnote.
func (h *Handler) KnowledgeOpsAnswerQuality(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "knowledge-ops"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, scopeAll := knowledgeOpsScope(h, c)
	weeks, _ := strconv.Atoi(c.DefaultQuery("weeks", "0"))
	out, err := h.Service.AnswerQualityProxy(
		c.Request.Context(), tenantID, scopeAll, weeks,
		c.Query("date_from"), c.Query("date_to"), time.Now().UTC(),
	)
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
		Attribution:  c.Query("attribution"),
		// doc/301 T12: the board filters by abstain reason (`no_evidence` /
		// `low_similarity` are the two operators actually chase; `self_abstain` is
		// a model judgement, not a knowledge gap).
		AbstainReason: c.Query("abstain_reason"),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	// Decoded in the service, not here: a handler that parses a payload is a second
	// reader of a format the service already owns.
	response.OKPage(c, service.KnowledgeOpsEventViews(out), total, page, pageSize)
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
	response.OK(c, ReviewKnowledgeOpsEventResponse{ID: c.Param("id"), Status: req.Status})
}

// MetricsContract returns the frozen metric contract board (doc/107 §3.3.2):
// the latest route evaluation gate plus the 30-day answer/operations KPIs.
func (h *Handler) MetricsContract(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "knowledge-ops"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _ := knowledgeOpsScope(h, c)
	out, err := h.Service.MetricsContractSummary(c.Request.Context(), tenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, out)
}

// ListCompilationRecovery exposes the unknown-run governance queue.
func (h *Handler) ListCompilationRecovery(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "knowledge-ops"); err != nil {
		response.Err(c, err)
		return
	}
	page, pageSize := pageParams(c)
	items, total, err := h.Service.ListCompilationRecovery(
		c.Request.Context(), c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID),
		c.Query("dataset_id"), page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

// ProbeCompilationRecoveryRun performs one read-only downstream probe.
func (h *Handler) ProbeCompilationRecoveryRun(c *gin.Context) {
	item, err := h.Service.ProbeCompilationRecoveryRun(
		c.Request.Context(), c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID), c.Param("runId"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

// RequestCompilationRecoveryApproval submits the governed recovery action.
func (h *Handler) RequestCompilationRecoveryApproval(c *gin.Context) {
	var req service.CompilationRecoveryApprovalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid compilation recovery payload")
		return
	}
	approval, err := h.Service.RequestCompilationRecoveryApproval(
		c.Request.Context(), c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID),
		c.Param("runId"), req,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.Accepted(c, approval)
}

// TriggerChatCitationAudit runs QA-2's weekly chat citation sample for the caller's
// tenant (doc/301 §5.10 / T19).
//
// The handler is thin on purpose: it authorizes, resolves the tenant and hands
// over. Everything else — the week boundary, the sample size, the judgement, the
// idempotency — belongs to the service, because a second copy of "which week is
// it" in a handler is a second answer waiting to disagree with the first.
func (h *Handler) TriggerChatCitationAudit(c *gin.Context) {
	actorID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(c.Request.Context(), actorID, "manage", "knowledge-ops"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _ := knowledgeOpsScope(h, c)
	run, created, err := h.Service.RunChatCitationAudit(c.Request.Context(), tenantID, actorID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, TriggerChatCitationAuditResponse{Run: run, Created: created})
}

// ChatCitationAuditRuns lists the recent weekly runs of the caller's tenant.
func (h *Handler) ChatCitationAuditRuns(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "knowledge-ops"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _ := knowledgeOpsScope(h, c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "12"))
	runs, err := h.Service.ChatCitationAuditRuns(c.Request.Context(), tenantID, limit)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, ChatCitationAuditRunsResponse{Runs: runs})
}

// ChatCitationAuditFindings pages one run's per-citation verdicts.
func (h *Handler) ChatCitationAuditFindings(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "knowledge-ops"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _ := knowledgeOpsScope(h, c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "50"))
	findings, total, err := h.Service.ChatCitationAuditFindings(
		c.Request.Context(), tenantID, c.Param("runId"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, ChatCitationAuditFindingsResponse{Findings: findings, Total: total})
}
