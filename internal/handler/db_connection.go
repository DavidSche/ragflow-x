package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func dbConnectionContext(c *gin.Context) (tenantID, userID string) {
	return c.GetString(middleware.ContextTenantID), c.GetString(middleware.ContextUserID)
}

func listDBConnections(c *gin.Context) {
	tenantID, _ := dbConnectionContext(c)
	page, pageSize := pageParams(c)
	filter := repository.DBConnectionFilter{
		Driver:       c.Query("driver"),
		HealthStatus: c.Query("health_status"),
	}
	items, total, err := handlerFrom(c).Service.ListDBConnections(c.Request.Context(), tenantID, filter, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func createDBConnection(c *gin.Context) {
	tenantID, userID := dbConnectionContext(c)
	input, ok := bind[service.DBConnectionInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateDBConnection(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func getDBConnection(c *gin.Context) {
	tenantID, _ := dbConnectionContext(c)
	item, err := handlerFrom(c).Service.GetDBConnection(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func updateDBConnection(c *gin.Context) {
	tenantID, userID := dbConnectionContext(c)
	input, ok := bind[service.DBConnectionInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.UpdateDBConnection(c.Request.Context(), tenantID, userID, c.Param("id"), input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func deleteDBConnection(c *gin.Context) {
	tenantID, userID := dbConnectionContext(c)
	id := c.Param("id")
	if err := handlerFrom(c).Service.DeleteDBConnection(c.Request.Context(), tenantID, userID, id); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"id": id})
}

func testDBConnection(c *gin.Context) {
	tenantID, userID := dbConnectionContext(c)
	result, err := handlerFrom(c).Service.TestDBConnection(c.Request.Context(), tenantID, userID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

func RegisterDBConnectionRoutes(group *gin.RouterGroup) {
	group.GET("/db-connections", listDBConnections)
	group.POST("/db-connections", createDBConnection)
	group.GET("/db-connections/:id", getDBConnection)
	group.PUT("/db-connections/:id", updateDBConnection)
	group.PATCH("/db-connections/:id", updateDBConnection)
	group.DELETE("/db-connections/:id", deleteDBConnection)
	group.POST("/db-connections/:id/test", testDBConnection)
}
