package handler

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func outboxEventFilter(c *gin.Context) repository.OutboxEventFilter {
	return repository.OutboxEventFilter{
		EventType:     strings.TrimSpace(c.Query("event_type")),
		AggregateType: strings.TrimSpace(c.Query("aggregate_type")),
		Status:        strings.TrimSpace(c.Query("status")),
	}
}

func listOutboxEvents(c *gin.Context) {
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListOutboxEvents(
		c.Request.Context(), c.GetString(middleware.ContextTenantID), outboxEventFilter(c), page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func getOutboxEvent(c *gin.Context) {
	view, err := handlerFrom(c).Service.GetOutboxEvent(
		c.Request.Context(), c.GetString(middleware.ContextTenantID), c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

func retryOutboxEvent(c *gin.Context) {
	view, err := handlerFrom(c).Service.RequestOutboxEventRetry(
		c.Request.Context(), c.GetString(middleware.ContextTenantID),
		c.GetString(middleware.ContextUserID), c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

func RegisterOutboxEventRoutes(group *gin.RouterGroup) {
	group.GET("/outbox-events", listOutboxEvents)
	group.GET("/outbox-events/:id", getOutboxEvent)
	group.POST("/outbox-events/:id/retry", retryOutboxEvent)
}
