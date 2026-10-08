package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

func listDocumentParseAttempts(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListDocumentParseAttempts(
		c.Request.Context(), tenantID, c.Param("id"), c.Param("docId"), page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func getLatestDocumentParseQualityReport(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	report, err := handlerFrom(c).Service.GetLatestDocumentParseQualityReport(
		c.Request.Context(), tenantID, c.Param("id"), c.Param("docId"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	if report == nil {
		response.Err(c, httperr.NotFound("parse quality report not found"))
		return
	}
	response.OK(c, report)
}

func listDocumentParseQualityReports(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListDocumentParseQualityReports(
		c.Request.Context(), tenantID, c.Param("id"), c.Param("docId"), page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func listDatasetParseQualityReports(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListDatasetLatestParseQualityReports(
		c.Request.Context(), tenantID, c.Param("id"), page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func RegisterParseQualityRoutes(group *gin.RouterGroup) {
	group.GET("/datasets/:id/parse-quality-reports", listDatasetParseQualityReports)
	group.GET("/datasets/:id/documents/:docId/parse-attempts", listDocumentParseAttempts)
	group.GET("/datasets/:id/documents/:docId/parse-quality-report", getLatestDocumentParseQualityReport)
	group.GET("/datasets/:id/documents/:docId/parse-quality-reports", listDocumentParseQualityReports)
}
