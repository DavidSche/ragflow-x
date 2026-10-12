package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func listMemoryOperations(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	page, pageSize := pageParams(c)
	filter := repository.MemoryOperationFilter{
		ProjectID:   c.Query("project_id"),
		UserID:      c.Query("user_id"),
		AssistantID: c.Query("assistant_id"),
		MemoryID:    c.Query("memory_id"),
		Operation:   c.Query("operation"),
		State:       c.Query("state"),
	}
	if tombstone, err := parseOptionalBool(c.Query("tombstone_active")); err == nil {
		filter.Tombstone = tombstone
	} else {
		response.Fail(c, 400, 40142, "tombstone_active must be true or false")
		return
	}
	items, total, err := handlerFrom(c).Service.ListMemoryOperations(
		c.Request.Context(), tenantID, userID, filter, page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func getMemoryOperation(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	item, err := handlerFrom(c).Service.GetMemoryOperation(
		c.Request.Context(), tenantID, userID, c.Param("operationId"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func tombstoneMemoryOperation(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	item, err := handlerFrom(c).Service.TombstoneMemoryOperation(
		c.Request.Context(), tenantID, userID, c.Param("operationId"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func RegisterMemoryOperationRoutes(group *gin.RouterGroup) {
	group.GET("/memory-operations", listMemoryOperations)
	group.GET("/memory-operations/:operationId", getMemoryOperation)
	group.POST("/memory-operations/:operationId/tombstone", tombstoneMemoryOperation)
}
