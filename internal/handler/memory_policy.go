package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func listMemoryPolicies(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	page, pageSize := pageParams(c)
	filter := repository.MemoryPolicyFilter{
		Scope:       c.Query("scope"),
		ProjectID:   c.Query("project_id"),
		UserID:      c.Query("user_id"),
		AssistantID: c.Query("assistant_id"),
		State:       c.Query("state"),
	}
	items, total, err := handlerFrom(c).Service.ListMemoryPolicies(c.Request.Context(), tenantID, userID, filter, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func createMemoryPolicy(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	input, ok := bind[service.MemoryPolicyInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateMemoryPolicy(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func getMemoryPolicy(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	item, err := handlerFrom(c).Service.GetMemoryPolicy(c.Request.Context(), tenantID, c.Param("policyId"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func getMemoryPolicyQualityGate(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	item, err := handlerFrom(c).Service.GetMemoryPolicyQualityGate(
		c.Request.Context(), tenantID, c.Param("policyId"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func updateMemoryPolicy(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	input, ok := bind[service.MemoryPolicyInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.UpdateMemoryPolicy(
		c.Request.Context(), tenantID, userID, c.Param("policyId"), input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func deleteMemoryPolicy(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	policyID := c.Param("policyId")
	if err := handlerFrom(c).Service.DeleteMemoryPolicy(c.Request.Context(), tenantID, userID, policyID); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"id": policyID})
}

func optInMemoryPolicy(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	item, err := handlerFrom(c).Service.SetMemoryPolicyConsent(
		c.Request.Context(), tenantID, userID, c.Param("policyId"), true,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func optOutMemoryPolicy(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	item, err := handlerFrom(c).Service.SetMemoryPolicyConsent(
		c.Request.Context(), tenantID, userID, c.Param("policyId"), false,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func getSelfMemoryPolicyTemplate(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	item, err := handlerFrom(c).Service.GetSelfMemoryPolicyTemplate(c.Request.Context(), tenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func updateSelfMemoryPolicyTemplate(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	input, ok := bind[service.SelfMemoryPolicyTemplateInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.UpdateSelfMemoryPolicyTemplate(
		c.Request.Context(), tenantID, userID, input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func listSelfMemoryPolicies(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListSelfMemoryPolicies(
		c.Request.Context(), tenantID, userID, page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func createSelfMemoryPolicy(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	type selfMemoryPolicyInput struct {
		AssistantID string `json:"assistant_id" binding:"required"`
	}
	input, ok := bind[selfMemoryPolicyInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateSelfMemoryPolicy(
		c.Request.Context(), tenantID, userID, input.AssistantID,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func RegisterMemoryPolicyRoutes(group *gin.RouterGroup) {
	RegisterMemoryOperationRoutes(group)
	group.GET("/memory-policies", listMemoryPolicies)
	group.POST("/memory-policies", createMemoryPolicy)
	group.GET("/memory-policies/:policyId", getMemoryPolicy)
	group.GET("/memory-policies/:policyId/quality-gate", getMemoryPolicyQualityGate)
	group.PUT("/memory-policies/:policyId", updateMemoryPolicy)
	group.DELETE("/memory-policies/:policyId", deleteMemoryPolicy)
	group.POST("/memory-policies/:policyId/opt-in", optInMemoryPolicy)
	group.POST("/memory-policies/:policyId/opt-out", optOutMemoryPolicy)
	group.GET("/memory-privacy/self-policy-template", getSelfMemoryPolicyTemplate)
	group.PUT("/memory-privacy/self-policy-template", updateSelfMemoryPolicyTemplate)
	group.GET("/self/memory-policies", listSelfMemoryPolicies)
	group.POST("/self/memory-policies", createSelfMemoryPolicy)
}
