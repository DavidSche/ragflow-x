package handler

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// ListConversationAssistants returns executable Chat/Agent assistants.
func (h *Handler) ListConversationAssistants(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "read", "assistant"); err != nil {
		response.Err(c, err)
		return
	}
	kinds := []string{}
	for _, kind := range strings.Split(c.Query("kind"), ",") {
		if kind = strings.TrimSpace(kind); kind != "" {
			kinds = append(kinds, kind)
		}
	}
	page, pageSize := pageParams(c)
	if pageSize > 100 {
		pageSize = 100
	}
	items, total, err := h.Service.ListConversationAssistants(ctx, userID, c.GetString(middleware.ContextTenantID), strings.TrimSpace(c.Query("query")), kinds, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

// UpdateConversationAssistant maintains route metadata and tenant auto-route opt-in.
func (h *Handler) UpdateConversationAssistant(c *gin.Context) {
	var request service.UpdateConversationAssistantRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Err(c, err)
		return
	}
	item, err := h.Service.UpdateConversationAssistant(
		c.Request.Context(), c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID),
		c.Param("kind"), c.Param("targetId"), request,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

// UpdateConversationAssistantGovernance manages governance-only fields
// (governance_status, discoverable, routing_weight, owner, risk).
func (h *Handler) UpdateConversationAssistantGovernance(c *gin.Context) {
	var request service.UpdateConversationAssistantGovernanceRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Err(c, err)
		return
	}
	item, err := h.Service.UpdateConversationAssistantGovernance(
		c.Request.Context(), c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID),
		c.Param("kind"), c.Param("targetId"), request,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

// RouteConversation recommends executable assistants from deterministic
// metadata. The response is always a suggestion in M2.
func (h *Handler) RouteConversation(c *gin.Context) {
	var request service.RouteRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Err(c, err)
		return
	}
	decision, err := h.Service.RouteConversation(c.Request.Context(), c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID), request)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, decision)
}

// SelectRouteCandidate creates a one-time selection binding.
func (h *Handler) SelectRouteCandidate(c *gin.Context) {
	var request service.SelectRouteCandidateRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Err(c, err)
		return
	}
	selection, err := h.Service.SelectRouteCandidate(c.Request.Context(), c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID), c.Param("routeId"), c.GetHeader("Idempotency-Key"), request)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, selection)
}

// BootstrapRouteSelection creates the target session after real-time checks.
func (h *Handler) BootstrapRouteSelection(c *gin.Context) {
	result, err := h.Service.BootstrapRouteSelection(c.Request.Context(), c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID), c.Param("routeSelectionId"), c.GetHeader("Idempotency-Key"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

// RunRouteEvaluation executes the M2.5 offline discovery gate.
func (h *Handler) RunRouteEvaluation(c *gin.Context) {
	var request service.RouteEvaluationRequest
	if err := c.ShouldBindJSON(&request); err != nil && err.Error() != "EOF" {
		response.Err(c, err)
		return
	}
	report, err := h.Service.RunRouteEvaluation(c.Request.Context(), c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID), request)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, report)
}

// GetTenantRoutePolicy returns the tenant rollout mode.
func (h *Handler) GetTenantRoutePolicy(c *gin.Context) {
	item, err := h.Service.GetTenantRoutePolicy(
		c.Request.Context(), c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

// SetTenantRoutePolicy updates the tenant rollout mode.
func (h *Handler) SetTenantRoutePolicy(c *gin.Context) {
	var request service.UpdateRoutePolicyRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.Err(c, err)
		return
	}
	item, err := h.Service.SetTenantRoutePolicy(
		c.Request.Context(), c.GetString(middleware.ContextUserID), c.GetString(middleware.ContextTenantID), request,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

// ListRouteEvaluationRuns returns prior M2.5 gate outcomes.
func (h *Handler) ListRouteEvaluationRuns(c *gin.Context) {
	page, pageSize := pageParams(c)
	if pageSize > 100 {
		pageSize = 100
	}
	items, total, err := h.Service.ListRouteEvaluationRuns(c.Request.Context(), c.GetString(middleware.ContextTenantID), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

// GetRouteEvaluationRun returns one immutable M2.5 report.
func (h *Handler) GetRouteEvaluationRun(c *gin.Context) {
	run, err := h.Service.GetRouteEvaluationRun(c.Request.Context(), c.GetString(middleware.ContextTenantID), c.Param("runId"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, run)
}
