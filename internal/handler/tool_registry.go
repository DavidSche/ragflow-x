package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func toolRegistryContext(c *gin.Context) (tenantID, userID string) {
	return c.GetString(middleware.ContextTenantID), c.GetString(middleware.ContextUserID)
}

func listToolRegistries(c *gin.Context) {
	tenantID, _ := toolRegistryContext(c)
	page, pageSize := pageParams(c)
	filter := repository.ToolRegistryFilter{ToolType: c.Query("tool_type")}
	if value, err := parseOptionalBool(c.Query("active")); err == nil {
		filter.Active = value
	} else {
		response.Fail(c, 400, 40160, "active must be true or false")
		return
	}
	items, total, err := handlerFrom(c).Service.ListToolRegistries(c.Request.Context(), tenantID, filter, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func createToolRegistry(c *gin.Context) {
	tenantID, userID := toolRegistryContext(c)
	input, ok := bind[service.ToolRegistryInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateToolRegistry(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func getToolRegistry(c *gin.Context) {
	tenantID, _ := toolRegistryContext(c)
	item, err := handlerFrom(c).Service.GetToolRegistry(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func updateToolRegistry(c *gin.Context) {
	tenantID, userID := toolRegistryContext(c)
	input, ok := bind[service.ToolRegistryInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.UpdateToolRegistry(c.Request.Context(), tenantID, userID, c.Param("id"), input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func executeToolRegistry(c *gin.Context) {
	tenantID, userID := toolRegistryContext(c)
	input, ok := bind[service.ToolExecutionInput](c)
	if !ok {
		return
	}
	result, err := handlerFrom(c).Service.ExecuteToolRegistry(c.Request.Context(), tenantID, userID, c.Param("id"), input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

func createRoutedAnswerRun(c *gin.Context) {
	tenantID, userID := toolRegistryContext(c)
	input, ok := bind[service.RoutedAnswerRunInput](c)
	if !ok {
		return
	}
	run, err := handlerFrom(c).Service.CreateRoutedAnswerRun(
		c.Request.Context(), tenantID, userID, input.AssistantID, input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, run)
}

func registerRoutedEvidenceFacts(c *gin.Context) {
	tenantID, userID := toolRegistryContext(c)
	answerRunID := c.Param("answerRunId")
	input, ok := bind[service.RoutedEvidenceFactsInput](c)
	if !ok {
		return
	}
	result, err := handlerFrom(c).Service.RegisterRoutedEvidenceFacts(
		c.Request.Context(), tenantID, userID, answerRunID, input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

func listRoutedEvidenceFacts(c *gin.Context) {
	tenantID, _ := toolRegistryContext(c)
	answerRunID := c.Param("answerRunId")
	facts, conflicts, err := handlerFrom(c).Service.ListEvidenceFacts(
		c.Request.Context(), tenantID, answerRunID,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{
		"answer_run_id": answerRunID,
		"facts":         facts,
		"conflicts":     conflicts,
	})
}

func getRoutedFactGuardOverview(c *gin.Context) {
	tenantID, _ := toolRegistryContext(c)
	overview, err := handlerFrom(c).Service.GetRoutedFactGuardOverview(
		c.Request.Context(), tenantID, c.Param("answerRunId"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, overview)
}

func deleteToolRegistry(c *gin.Context) {
	tenantID, userID := toolRegistryContext(c)
	id := c.Param("id")
	if err := handlerFrom(c).Service.DeleteToolRegistry(c.Request.Context(), tenantID, userID, id); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"id": id})
}

func RegisterToolRoutingRoutes(group *gin.RouterGroup) {
	group.GET("/tool-registry", listToolRegistries)
	group.POST("/tool-registry", createToolRegistry)
	group.GET("/tool-registry/:id", getToolRegistry)
	group.PUT("/tool-registry/:id", updateToolRegistry)
	group.PATCH("/tool-registry/:id", updateToolRegistry)
	group.DELETE("/tool-registry/:id", deleteToolRegistry)
	group.POST("/tool-registry/:id/execute", executeToolRegistry)
	group.GET("/source-routing-rules", listSourceRoutingRules)
	group.POST("/source-routing-rules", createSourceRoutingRule)
	group.GET("/source-routing-rules/:id", getSourceRoutingRule)
	group.PUT("/source-routing-rules/:id", updateSourceRoutingRule)
	group.PATCH("/source-routing-rules/:id", updateSourceRoutingRule)
	group.DELETE("/source-routing-rules/:id", deleteSourceRoutingRule)
	group.POST("/tool-routing/routed-sql-answer", executeRoutedSQLToolAnswer)
	group.POST("/tool-routing/routed-sql-answers", executeRoutedSQLMultiSubqueryAnswer)
	group.POST("/tool-routing/routed-mixed-tool-answers", executeRoutedMixedToolAnswer)
	group.POST("/tool-routing/planned-mixed-tool-answers", executePlannedRoutedMixedToolAnswer)
	group.POST("/tool-routing/answer-runs", createRoutedAnswerRun)
	group.POST("/tool-routing/answer-runs/:answerRunId/evidence-facts", registerRoutedEvidenceFacts)
	group.GET("/tool-routing/answer-runs/:answerRunId/evidence-facts", listRoutedEvidenceFacts)
	group.GET("/tool-routing/answer-runs/:answerRunId/fact-guard", getRoutedFactGuardOverview)
}
