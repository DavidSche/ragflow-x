package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func listKnowledgeProfiles(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	page, pageSize := pageParams(c)
	filter := repository.KnowledgeProfileFilter{
		DatasetID: c.Query("dataset_id"),
		ProjectID: c.Query("project_id"),
		Status:    c.Query("status"),
	}
	items, total, err := handlerFrom(c).Service.ListKnowledgeProfiles(c.Request.Context(), tenantID, userID, filter, page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func createKnowledgeProfile(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	input, ok := bind[service.KnowledgeProfileInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateKnowledgeProfile(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func getKnowledgeProfile(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	item, err := handlerFrom(c).Service.GetKnowledgeProfile(c.Request.Context(), tenantID, c.Param("profileId"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func updateKnowledgeProfile(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	input, ok := bind[service.KnowledgeProfileInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.UpdateKnowledgeProfile(
		c.Request.Context(), tenantID, userID, c.Param("profileId"), input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func deleteKnowledgeProfile(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	profileID := c.Param("profileId")
	if err := handlerFrom(c).Service.DeleteKnowledgeProfile(c.Request.Context(), tenantID, userID, profileID); err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"id": profileID})
}

func triggerCompilationRun(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	input, ok := bind[service.CompilationRunTriggerInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.TriggerCompilationRun(
		c.Request.Context(), tenantID, userID, c.Param("id"), input,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	c.Params = append(c.Params, gin.Param{Key: "runId", Value: item.ID})
	response.Accepted(c, item)
}

func cancelCompilationRun(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	var body struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&body)
	item, err := handlerFrom(c).Service.CancelCompilationRun(
		c.Request.Context(), tenantID, userID, c.Param("id"), c.Param("runId"), body.Reason,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func listCompilationRuns(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	page, pageSize := pageParams(c)
	filter := repository.CompilationRunFilter{
		ProfileID: c.Query("profile_id"),
		State:     c.Query("state"),
	}
	items, total, err := handlerFrom(c).Service.ListCompilationRuns(
		c.Request.Context(), tenantID, userID, c.Param("id"), filter, page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func listArtifactProjections(c *gin.Context) {
	tenantID, userID := knowledgeStrategyContext(c)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListArtifactProjections(
		c.Request.Context(), tenantID, userID, c.Param("id"), c.Query("run_id"), page, pageSize,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func reconcileCompilationRuns(c *gin.Context) {
	tenantID := c.GetString("tenant_id")
	items, err := handlerFrom(c).Service.ReconcileCompilationDataset(
		c.Request.Context(), tenantID, c.GetString("user_id"), c.Param("id"),
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.Accepted(c, items)
}

func RegisterKnowledgeCompilationRoutes(group *gin.RouterGroup) {
	group.GET("/knowledge-profiles", listKnowledgeProfiles)
	group.POST("/knowledge-profiles", createKnowledgeProfile)
	group.GET("/knowledge-profiles/:profileId", getKnowledgeProfile)
	group.PUT("/knowledge-profiles/:profileId", updateKnowledgeProfile)
	group.DELETE("/knowledge-profiles/:profileId", deleteKnowledgeProfile)
	group.POST("/datasets/:id/compilation/runs", triggerCompilationRun)
	group.POST("/datasets/:id/compilation/runs/:runId/cancel", cancelCompilationRun)
	group.GET("/datasets/:id/compilation/runs", listCompilationRuns)
	group.POST("/datasets/:id/compilation/reconcile", reconcileCompilationRuns)
	group.GET("/datasets/:id/artifacts", listArtifactProjections)
}
