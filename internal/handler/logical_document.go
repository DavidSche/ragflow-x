package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func listLogicalDocuments(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	page, pageSize := pageParams(c)
	filter := repository.DocumentVersionFilter{DatasetID: c.Query("dataset_id")}
	items, total, err := handlerFrom(c).Service.ListLogicalDocuments(c.Request.Context(), tenantID, filter, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func createLogicalDocument(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.LogicalDocumentInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateLogicalDocument(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func getLogicalDocument(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	item, err := handlerFrom(c).Service.GetLogicalDocument(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func updateLogicalDocument(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.LogicalDocumentPatchInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.UpdateLogicalDocument(
		c.Request.Context(), tenantID, userID, c.Param("id"), input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func deleteLogicalDocument(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	id := c.Param("id")
	if err := handlerFrom(c).Service.DeleteLogicalDocument(c.Request.Context(), tenantID, userID, id); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"id": id})
}

func listLogicalDocumentVersions(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListDocumentVersions(
		c.Request.Context(), tenantID, c.Param("id"), page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func supersedeLogicalDocumentVersion(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.SupersedeDocumentVersionInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.SupersedeDocumentVersion(
		c.Request.Context(), tenantID, userID, c.Param("id"), input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func restoreLogicalDocumentVersion(c *gin.Context) {
	tenantID, userID := releaseGovernanceContext(c)
	input, ok := bind[service.SupersedeDocumentVersionInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.RestoreDocumentVersion(
		c.Request.Context(), tenantID, userID, c.Param("id"), input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func listLogicalDocumentPublishAttempts(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListVersionPublishAttempts(
		c.Request.Context(), tenantID, c.Param("id"), page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func RegisterLogicalDocumentRoutes(group *gin.RouterGroup) {
	group.GET("/logical-documents", listLogicalDocuments)
	group.POST("/logical-documents", createLogicalDocument)
	group.GET("/logical-documents/:id", getLogicalDocument)
	group.PUT("/logical-documents/:id", updateLogicalDocument)
	group.PATCH("/logical-documents/:id", updateLogicalDocument)
	group.DELETE("/logical-documents/:id", deleteLogicalDocument)
	group.GET("/logical-documents/:id/versions", listLogicalDocumentVersions)
	group.POST("/logical-documents/:id/supersede", supersedeLogicalDocumentVersion)
	group.POST("/logical-documents/:id/restore", restoreLogicalDocumentVersion)
	group.GET("/logical-documents/:id/publish-attempts", listLogicalDocumentPublishAttempts)
}
