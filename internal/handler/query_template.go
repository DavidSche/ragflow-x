package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func queryTemplateContext(c *gin.Context) (tenantID, userID string) {
	return c.GetString(middleware.ContextTenantID), c.GetString(middleware.ContextUserID)
}

func listQueryTemplates(c *gin.Context) {
	tenantID, _ := queryTemplateContext(c)
	page, pageSize := pageParams(c)
	filter := repository.QueryTemplateFilter{ConnectionID: c.Query("connection_id")}
	if value, err := parseOptionalBool(c.Query("active")); err == nil {
		filter.Active = value
	} else {
		response.Fail(c, 400, 40180, "active must be true or false")
		return
	}
	items, total, err := handlerFrom(c).Service.ListQueryTemplates(c.Request.Context(), tenantID, filter, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func createQueryTemplate(c *gin.Context) {
	tenantID, userID := queryTemplateContext(c)
	input, ok := bind[service.QueryTemplateInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateQueryTemplate(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func getQueryTemplate(c *gin.Context) {
	tenantID, _ := queryTemplateContext(c)
	item, err := handlerFrom(c).Service.GetQueryTemplate(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func updateQueryTemplate(c *gin.Context) {
	tenantID, userID := queryTemplateContext(c)
	input, ok := bind[service.QueryTemplateInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.UpdateQueryTemplate(
		c.Request.Context(), tenantID, userID, c.Param("id"), input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func deleteQueryTemplate(c *gin.Context) {
	tenantID, userID := queryTemplateContext(c)
	id := c.Param("id")
	if err := handlerFrom(c).Service.DeleteQueryTemplate(c.Request.Context(), tenantID, userID, id); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"id": id})
}

func RegisterQueryTemplateRoutes(group *gin.RouterGroup) {
	group.GET("/query-templates", listQueryTemplates)
	group.POST("/query-templates", createQueryTemplate)
	group.GET("/query-templates/:id", getQueryTemplate)
	group.PUT("/query-templates/:id", updateQueryTemplate)
	group.PATCH("/query-templates/:id", updateQueryTemplate)
	group.DELETE("/query-templates/:id", deleteQueryTemplate)
}
