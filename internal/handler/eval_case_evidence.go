package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func getEvalCaseEvidence(c *gin.Context) {
	bundle, err := handlerFrom(c).Service.GetEvalCaseEvidence(
		c.Request.Context(), c.GetString(middleware.ContextTenantID), c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, bundle)
}

func updateEvalCaseEvidence(c *gin.Context) {
	var input service.EvidenceSnapshotInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Fail(c, 400, 40090, "invalid eval case evidence payload")
		return
	}
	bundle, err := handlerFrom(c).Service.UpdateEvalCaseEvidence(
		c.Request.Context(), c.GetString(middleware.ContextTenantID),
		c.GetString(middleware.ContextUserID), c.Param("id"), input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, bundle)
}

func revalidateEvalCaseEvidence(c *gin.Context) {
	bundle, err := handlerFrom(c).Service.RevalidateEvalCaseEvidence(
		c.Request.Context(), c.GetString(middleware.ContextTenantID),
		c.GetString(middleware.ContextUserID), c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, bundle)
}

func RegisterEvalCaseEvidenceRoutes(group *gin.RouterGroup) {
	group.GET("/eval-cases/:id/evidence", getEvalCaseEvidence)
	group.PUT("/eval-cases/:id/evidence", updateEvalCaseEvidence)
	group.POST("/eval-cases/:id/revalidate", revalidateEvalCaseEvidence)
}
