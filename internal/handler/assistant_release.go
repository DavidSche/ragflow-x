package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func assistantReleaseContext(c *gin.Context) (tenantID, userID string) {
	return c.GetString(middleware.ContextTenantID), c.GetString(middleware.ContextUserID)
}

func operationInputFromRequest(c *gin.Context, body service.OperationInput) service.OperationInput {
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = c.GetHeader("Idempotency-Key")
	}
	return body
}

func listAssistants(c *gin.Context) {
	tenantID, _ := assistantReleaseContext(c)
	page, pageSize := pageParams(c)
	items, err := handlerFrom(c).Service.ListAssistants(c.Request.Context(), tenantID, c.Query("project_id"), c.Query("lifecycle"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items.Items, items.Total, page, pageSize)
}

func createAssistantRelease(c *gin.Context) {
	tenantID, userID := assistantReleaseContext(c)
	input, ok := bind[service.CreateAssistantReleaseInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CreateAssistantRelease(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "assistant_release.create", "release-governance", item.ID, item.ReleaseState)
	response.OK(c, item)
}

func listAssistantReleases(c *gin.Context) {
	tenantID, _ := assistantReleaseContext(c)
	page, pageSize := pageParams(c)
	items, err := handlerFrom(c).Service.ListAssistantReleases(c.Request.Context(), tenantID, c.Query("assistant_id"), c.Query("state"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items.Items, items.Total, page, pageSize)
}

func getAssistantRelease(c *gin.Context) {
	tenantID, _ := assistantReleaseContext(c)
	item, err := handlerFrom(c).Service.GetAssistantRelease(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func getAssistantSnapshotManifest(c *gin.Context) {
	tenantID, _ := assistantReleaseContext(c)
	item, err := handlerFrom(c).Service.GetAssistantRelease(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	manifest, err := handlerFrom(c).Service.GetSnapshotManifest(c.Request.Context(), tenantID, item.SnapshotManifestID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, manifest)
}

func transitionAssistantRelease(c *gin.Context) {
	tenantID, userID := assistantReleaseContext(c)
	input, ok := bind[struct {
		FromState string                 `json:"from_state" binding:"required"`
		ToState   string                 `json:"to_state" binding:"required"`
		Operation service.OperationInput `json:"operation"`
	}](c)
	if !ok {
		return
	}
	input.Operation = operationInputFromRequest(c, input.Operation)
	item, err := handlerFrom(c).Service.TransitionAssistantRelease(c.Request.Context(), tenantID, userID, c.Param("id"), input.FromState, input.ToState, input.Operation)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "assistant_release.transition", "release-governance", item.ID, item.ReleaseState)
	response.OK(c, item)
}

func applyAssistantRelease(c *gin.Context) {
	tenantID, userID := assistantReleaseContext(c)
	input, ok := bind[service.OperationInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.ApplyAssistantRelease(c.Request.Context(), tenantID, c.Param("id"), userID, operationInputFromRequest(c, input))
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "assistant_release.apply", "release-governance", item.ID, item.ReleaseState)
	response.OK(c, item)
}

func reconcileAssistantRelease(c *gin.Context) {
	tenantID, userID := assistantReleaseContext(c)
	input, ok := bind[service.OperationInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.ReconcileAssistantRelease(c.Request.Context(), tenantID, userID, c.Param("id"), operationInputFromRequest(c, input))
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "assistant_release.reconcile", "release-governance", item.ID, item.ReconcileStatus)
	response.OK(c, item)
}

func activateAssistantRelease(c *gin.Context) {
	tenantID, userID := assistantReleaseContext(c)
	input, ok := bind[service.ActivationInput](c)
	if !ok {
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = c.GetHeader("Idempotency-Key")
	}
	item, err := handlerFrom(c).Service.ActivateAssistantRelease(c.Request.Context(), tenantID, userID, c.Param("id"), input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "assistant_release.activate", "release-governance", item.TargetReleaseID, item.OperationState)
	response.OK(c, item)
}

func promoteCanaryAssistantRelease(c *gin.Context) {
	tenantID, userID := assistantReleaseContext(c)
	input, ok := bind[service.OperationInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.PromoteCanaryRelease(c.Request.Context(), tenantID, userID, c.Param("id"), operationInputFromRequest(c, input))
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "assistant_release.promote_canary", "release-governance", item.TargetReleaseID, item.OperationState)
	response.OK(c, item)
}

func rollbackAssistantRelease(c *gin.Context) {
	tenantID, userID := assistantReleaseContext(c)
	input, ok := bind[service.RollbackInput](c)
	if !ok {
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = c.GetHeader("Idempotency-Key")
	}
	item, err := handlerFrom(c).Service.RollbackAssistantRelease(c.Request.Context(), tenantID, userID, c.Param("id"), c.Param("targetId"), input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "assistant_release.rollback", "release-governance", item.TargetReleaseID, item.OperationState)
	response.OK(c, item)
}

func listAssistantReleaseOperations(c *gin.Context) {
	tenantID, _ := assistantReleaseContext(c)
	page, pageSize := pageParams(c)
	items, err := handlerFrom(c).Service.ListReleaseOperations(c.Request.Context(), tenantID, c.Param("id"), c.Query("operation_type"), c.Query("state"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items.Items, items.Total, page, pageSize)
}

func recoverAssistantReleaseOperation(c *gin.Context) {
	tenantID, userID := assistantReleaseContext(c)
	item, err := handlerFrom(c).Service.RecoverAssistantReleaseOperation(c.Request.Context(), tenantID, userID, c.Param("operationId"))
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "assistant_release.operation_recover", "release-governance", item.ReleaseID, item.OperationState)
	response.OK(c, item)
}

func compensateAssistantRelease(c *gin.Context) {
	tenantID, userID := assistantReleaseContext(c)
	input, ok := bind[service.OperationInput](c)
	if !ok {
		return
	}
	item, err := handlerFrom(c).Service.CompensateAssistantRelease(c.Request.Context(), tenantID, userID, c.Param("id"), operationInputFromRequest(c, input))
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "assistant_release.compensate", "release-governance", item.ID, item.ReleaseState)
	response.OK(c, item)
}

func getAssistantReleaseHealth(c *gin.Context) {
	tenantID, _ := assistantReleaseContext(c)
	item, err := handlerFrom(c).Service.GetRuntimeHealth(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, item)
}

func updateAssistantReleaseHealth(c *gin.Context) {
	tenantID, _ := assistantReleaseContext(c)
	input, ok := bind[model.RuntimeHealth](c)
	if !ok {
		return
	}
	input.AssistantReleaseID = c.Param("id")
	item, err := handlerFrom(c).Service.ReportRuntimeHealth(c.Request.Context(), tenantID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "assistant_release.health", "release-governance", item.AssistantReleaseID, item.Health)
	response.OK(c, item)
}

func listAssistantAuthorizationSnapshots(c *gin.Context) {
	tenantID, _ := assistantReleaseContext(c)
	page, pageSize := pageParams(c)
	items, total, err := handlerFrom(c).Service.ListAuthorizationSnapshots(c.Request.Context(), tenantID, c.Param("id"), c.Query("trace_id"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items, total, page, pageSize)
}

func createAssistantAuthorizationSnapshot(c *gin.Context) {
	tenantID, _ := assistantReleaseContext(c)
	input, ok := bind[model.AuthorizationSnapshot](c)
	if !ok {
		return
	}
	input.TenantID = tenantID
	input.AssistantReleaseID = c.Param("id")
	item, err := handlerFrom(c).Service.RecordAuthorizationSnapshot(c.Request.Context(), tenantID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "assistant_release.authorization_snapshot", "release-governance", item.AssistantReleaseID, item.Decision)
	response.OK(c, item)
}

func RegisterAssistantReleaseRoutes(group *gin.RouterGroup, handler *Handler) {
	group.Use(func(c *gin.Context) {
		c.Set("handler", handler)
		c.Next()
	})
	group.GET("/assistants", listAssistants)
	group.POST("/assistant-releases", createAssistantRelease)
	group.GET("/assistant-releases", listAssistantReleases)
	group.GET("/assistant-releases/:id", getAssistantRelease)
	group.GET("/assistant-releases/:id/manifest", getAssistantSnapshotManifest)
	group.POST("/assistant-releases/:id/transition", transitionAssistantRelease)
	group.POST("/assistant-releases/:id/apply", applyAssistantRelease)
	group.POST("/assistant-releases/:id/activate", activateAssistantRelease)
	group.POST("/assistant-releases/:id/promote-canary", promoteCanaryAssistantRelease)
	group.POST("/assistant-releases/:id/rollback/:targetId", rollbackAssistantRelease)
	group.POST("/assistant-releases/:id/reconcile", reconcileAssistantRelease)
	group.GET("/assistant-releases/:id/operations", listAssistantReleaseOperations)
	group.POST("/assistant-releases/:id/operations/:operationId/recover", recoverAssistantReleaseOperation)
	group.POST("/assistant-releases/:id/compensate", compensateAssistantRelease)
	group.GET("/assistant-releases/:id/health", getAssistantReleaseHealth)
	group.PUT("/assistant-releases/:id/health", updateAssistantReleaseHealth)
	group.GET("/assistant-releases/:id/authorization-snapshots", listAssistantAuthorizationSnapshots)
	group.POST("/assistant-releases/:id/authorization-snapshots", createAssistantAuthorizationSnapshot)
}
