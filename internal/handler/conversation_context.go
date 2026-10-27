package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func previewConversationContext(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	input, ok := bind[service.ConversationContextPreviewInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.PreviewConversationContext(
		c.Request.Context(), tenantID, userID, input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func getConversationContextDecision(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	item, err := handlerFrom(c).Service.GetConversationContextDecision(
		c.Request.Context(), tenantID, c.GetString("user_id"), c.Param("decisionId"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func getConversationContextDecisionByRequest(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	item, err := handlerFrom(c).Service.GetConversationContextDecisionByRequest(
		c.Request.Context(), tenantID, c.GetString("user_id"), c.Param("requestId"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func listConversationContextDecisions(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	page, pageSize := pageParams(c)
	filter := repository.ContextDecisionFilter{
		AssistantID: c.Query("assistant_id"),
		SessionID:   c.Query("session_id"),
		UserID:      c.Query("user_id"),
		RequestKind: c.Query("request_kind"),
		Decision:    c.Query("decision"),
	}
	items, total, err := handlerFrom(c).Service.ListConversationContextDecisions(
		c.Request.Context(), tenantID, userID, filter, page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func RegisterConversationContextRoutes(group *gin.RouterGroup) {
	group.POST("/conversation-context/preview", previewConversationContext)
	group.GET("/conversation-context/decisions/:decisionId", getConversationContextDecision)
	group.GET("/conversation-context/decisions/by-request/:requestId", getConversationContextDecisionByRequest)
	group.GET("/conversation-context/decisions", listConversationContextDecisions)
}
