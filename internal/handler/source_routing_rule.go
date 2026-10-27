package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func sourceRoutingRuleContext(c *gin.Context) (tenantID, userID string) {
	return c.GetString(middleware.ContextTenantID), c.GetString(middleware.ContextUserID)
}

func listSourceRoutingRules(c *gin.Context) {
	tenantID, _ := sourceRoutingRuleContext(c)
	page, pageSize := pageParams(c)
	filter := repository.SourceRoutingRuleFilter{
		AssistantID: c.Query("assistant_id"),
		SourceType:  c.Query("source_type"),
	}
	if value, err := parseOptionalBool(c.Query("active")); err == nil {
		filter.Active = value
	} else {
		response.Fail(c, 400, 40161, "active must be true or false")
		return
	}
	items, total, err := handlerFrom(c).Service.ListSourceRoutingRules(c.Request.Context(), tenantID, filter, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func createSourceRoutingRule(c *gin.Context) {
	tenantID, userID := sourceRoutingRuleContext(c)
	input, ok := bind[service.SourceRoutingRuleInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateSourceRoutingRule(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func getSourceRoutingRule(c *gin.Context) {
	tenantID, _ := sourceRoutingRuleContext(c)
	item, err := handlerFrom(c).Service.GetSourceRoutingRule(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func updateSourceRoutingRule(c *gin.Context) {
	tenantID, userID := sourceRoutingRuleContext(c)
	input, ok := bind[service.SourceRoutingRuleInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.UpdateSourceRoutingRule(c.Request.Context(), tenantID, userID, c.Param("id"), input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func deleteSourceRoutingRule(c *gin.Context) {
	tenantID, userID := sourceRoutingRuleContext(c)
	id := c.Param("id")
	if err := handlerFrom(c).Service.DeleteSourceRoutingRule(c.Request.Context(), tenantID, userID, id); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"id": id})
}
