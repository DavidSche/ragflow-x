package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func knowledgeStrategyContext(c *gin.Context) (tenantID, userID string) {
	return c.GetString(middleware.ContextTenantID), c.GetString(middleware.ContextUserID)
}

func listKnowledgeStrategies(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	page, pageSize := pageParams(c)
	filter := repository.KnowledgeStrategyFilter{
		DatasetID:    c.Query("dataset_id"),
		ProjectID:    c.Query("project_id"),
		StrategyType: c.Query("strategy_type"),
	}
	if value, err := parseOptionalBool(c.Query("active")); err == nil {
		filter.Active = value
	} else {
		response.Fail(c, 400, 40120, "active must be true or false")
		return
	}
	items, total, err := handlerFrom(c).Service.ListKnowledgeStrategies(c.Request.Context(), tenantID, userID, filter, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func createKnowledgeStrategy(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	input, ok := bind[service.KnowledgeStrategyInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateKnowledgeStrategy(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func getKnowledgeStrategy(c *gin.Context) {
	tenantID, _ := knowledgeStrategyContext(c)
	item, err := handlerFrom(c).Service.GetKnowledgeStrategy(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func updateKnowledgeStrategy(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	input, ok := bind[service.KnowledgeStrategyInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.UpdateKnowledgeStrategy(
		c.Request.Context(), tenantID, userID, c.Param("id"), input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func deleteKnowledgeStrategy(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	id := c.Param("id")
	if err := handlerFrom(c).Service.DeleteKnowledgeStrategy(c.Request.Context(), tenantID, userID, id); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"id": id})
}

func probeKnowledgeStrategy(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	probe, err := handlerFrom(c).Service.ProbeKnowledgeStrategy(
		c.Request.Context(), tenantID, userID, c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, probe)
}

func executeKnowledgeStrategy(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	input, ok := bind[service.KnowledgeStrategyRetrieveInput](c)
	if !ok {
		return
	}
	result, err := handlerFrom(c).Service.ExecuteKnowledgeStrategy(
		c.Request.Context(), tenantID, userID, c.Param("id"), input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

func RegisterKnowledgeStrategyRoutes(group *gin.RouterGroup) {
	group.GET("/knowledge-strategies", listKnowledgeStrategies)
	group.POST("/knowledge-strategies", createKnowledgeStrategy)
	group.GET("/knowledge-strategies/:id", getKnowledgeStrategy)
	group.PUT("/knowledge-strategies/:id", updateKnowledgeStrategy)
	group.PATCH("/knowledge-strategies/:id", updateKnowledgeStrategy)
	group.DELETE("/knowledge-strategies/:id", deleteKnowledgeStrategy)
	group.POST("/knowledge-strategies/:id/probe", probeKnowledgeStrategy)
	group.POST("/knowledge-strategies/:id/retrieve", executeKnowledgeStrategy)
}
