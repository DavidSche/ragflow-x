package handler

import (
	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func templateInstanceContext(c *gin.Context) (tenantID, userID string) {
	return c.GetString(middleware.ContextTenantID), c.GetString(middleware.ContextUserID)
}

func instantiateTemplateInstance(c *gin.Context) {
	if err := authorizeResource(c, "scenario-template", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	if err := authorizeResource(c, "release-governance", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	input, ok := bind[service.InstantiateTemplateInstanceInput](c)
	if !ok {
		return
	}
	tenantID, userID := templateInstanceContext(c)
	view, err := handlerFrom(c).Service.InstantiateTemplateInstance(c.Request.Context(), tenantID, userID, input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "template_instance.instantiate", "release-governance", view.ID, view.GovernanceStatus)
	response.OK(c, view)
}

func listTemplateInstances(c *gin.Context) {
	if err := authorizeResource(c, "release-governance", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _ := templateInstanceContext(c)
	page, pageSize := pageParams(c)
	items, err := handlerFrom(c).Service.ListTemplateInstances(c.Request.Context(), tenantID, c.Query("project_id"), c.Query("status"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items.Items, items.Total, page, pageSize)
}

func getTemplateInstance(c *gin.Context) {
	if err := authorizeResource(c, "release-governance", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _ := templateInstanceContext(c)
	view, err := handlerFrom(c).Service.GetTemplateInstance(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

func dryRunTemplateInstance(c *gin.Context) {
	if err := authorizeResource(c, "scenario-template", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	input, ok := bind[service.TemplateInstanceDryRunInput](c)
	if !ok {
		return
	}
	tenantID, _ := templateInstanceContext(c)
	report, err := handlerFrom(c).Service.DryRunTemplateInstance(c.Request.Context(), tenantID, c.Param("id"), input)
	if err != nil {
		response.Err(c, err)
		return
	}
	detail := "false"
	if report.OK {
		detail = "true"
	}
	governanceAudit(c, "template_instance.dry_run", "release-governance", c.Param("id"), detail)
	response.OK(c, report)
}

func evaluateTemplateInstance(c *gin.Context) {
	if err := authorizeResource(c, "release-governance", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, userID := templateInstanceContext(c)
	result, err := handlerFrom(c).Service.EvaluateTemplateInstance(c.Request.Context(), tenantID, userID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "template_instance.evaluate", "release-governance", result.Candidate.CandidateID, result.Candidate.Status)
	response.OK(c, result)
}

func releaseTemplateInstance(c *gin.Context) {
	if err := authorizeResource(c, "release-governance", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	input, ok := bind[service.CreateAssistantReleaseInput](c)
	if !ok {
		return
	}
	tenantID, userID := templateInstanceContext(c)
	release, err := handlerFrom(c).Service.ReleaseTemplateInstance(c.Request.Context(), tenantID, userID, c.Param("id"), input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "template_instance.release", "release-governance", release.ID, release.ReleaseState)
	response.OK(c, release)
}

func activateTemplateInstanceRelease(c *gin.Context) {
	if err := authorizeResource(c, "release-governance", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	input, ok := bind[service.ActivationInput](c)
	if !ok {
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = c.GetHeader("Idempotency-Key")
	}
	tenantID, userID := templateInstanceContext(c)
	operation, err := handlerFrom(c).Service.ActivateAssistantRelease(c.Request.Context(), tenantID, userID, c.Param("releaseId"), input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "template_instance.activate", "release-governance", operation.TargetReleaseID, operation.OperationState)
	response.OK(c, operation)
}

func reconcileTemplateInstanceRelease(c *gin.Context) {
	if err := authorizeResource(c, "release-governance", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	input, ok := bind[service.OperationInput](c)
	if !ok {
		return
	}
	tenantID, userID := templateInstanceContext(c)
	release, err := handlerFrom(c).Service.ReconcileAssistantRelease(c.Request.Context(), tenantID, userID, c.Param("releaseId"), operationInputFromRequest(c, input))
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "template_instance.reconcile", "release-governance", release.ID, release.ReconcileStatus)
	response.OK(c, release)
}

func rollbackTemplateInstance(c *gin.Context) {
	if err := authorizeResource(c, "release-governance", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	input, ok := bind[struct {
		TargetReleaseID string                `json:"target_release_id" binding:"required"`
		Operation       service.RollbackInput `json:"operation"`
	}](c)
	if !ok {
		return
	}
	if input.Operation.IdempotencyKey == "" {
		input.Operation.IdempotencyKey = c.GetHeader("Idempotency-Key")
	}
	tenantID, userID := templateInstanceContext(c)
	operation, err := handlerFrom(c).Service.RollbackAssistantRelease(c.Request.Context(), tenantID, userID, c.Param("id"), input.TargetReleaseID, input.Operation)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "template_instance.rollback", "release-governance", operation.TargetReleaseID, operation.OperationState)
	response.OK(c, operation)
}

func getTemplateInstanceHealth(c *gin.Context) {
	if err := authorizeResource(c, "release-governance", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _ := templateInstanceContext(c)
	view, err := handlerFrom(c).Service.TemplateInstanceHealth(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

func listTemplateInstanceReleases(c *gin.Context) {
	if err := authorizeResource(c, "release-governance", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _ := templateInstanceContext(c)
	page, pageSize := pageParams(c)
	items, err := handlerFrom(c).Service.ListTemplateInstanceReleases(c.Request.Context(), tenantID, c.Param("id"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, items.Items, items.Total, page, pageSize)
}

func getAssistantRolloutPolicies(c *gin.Context) {
	if err := authorizeResource(c, "release-governance", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _ := templateInstanceContext(c)
	page, pageSize := pageParams(c)
	items, active, total, err := handlerFrom(c).Service.GetAssistantRolloutPolicies(c.Request.Context(), tenantID, c.Param("id"), page, pageSize)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"items": items, "active": active, "total": total, "page": page, "page_size": pageSize})
}

func createAssistantRolloutPolicy(c *gin.Context) {
	if err := authorizeResource(c, "release-governance", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	input, ok := bind[service.RolloutPolicyInput](c)
	if !ok {
		return
	}
	tenantID, userID := templateInstanceContext(c)
	policy, err := handlerFrom(c).Service.CreateAssistantRolloutPolicy(c.Request.Context(), tenantID, userID, c.Param("id"), input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "assistant_release.rollout_policy.create", "release-governance", policy.ID, policy.Status)
	response.OK(c, policy)
}

func updateAssistantRolloutPolicy(c *gin.Context) {
	if err := authorizeResource(c, "release-governance", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	input, ok := bind[service.RolloutPolicyInput](c)
	if !ok {
		return
	}
	tenantID, userID := templateInstanceContext(c)
	policy, err := handlerFrom(c).Service.UpdateAssistantRolloutPolicy(c.Request.Context(), tenantID, userID, c.Param("id"), c.Param("policyId"), input)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "assistant_release.rollout_policy.update", "release-governance", policy.ID, policy.Status)
	response.OK(c, policy)
}

// RegisterTemplateInstanceRoutes mounts the doc/107 §3.1.5 template instance
// API family and the rollout policy endpoints (doc/124 §2/§3).
func RegisterTemplateInstanceRoutes(group *gin.RouterGroup, handler *Handler) {
	group.Use(func(c *gin.Context) {
		c.Set("handler", handler)
		c.Next()
	})
	group.POST("/template-instances", instantiateTemplateInstance)
	group.GET("/template-instances", listTemplateInstances)
	group.GET("/template-instances/:id", getTemplateInstance)
	group.POST("/template-instances/:id/dry-run", dryRunTemplateInstance)
	group.POST("/template-instances/:id/evaluate", evaluateTemplateInstance)
	group.POST("/template-instances/:id/release", releaseTemplateInstance)
	group.POST("/template-instances/:id/releases/:releaseId/activate", activateTemplateInstanceRelease)
	group.POST("/template-instances/:id/rollback", rollbackTemplateInstance)
	group.GET("/template-instances/:id/health", getTemplateInstanceHealth)
	group.GET("/template-instances/:id/releases", listTemplateInstanceReleases)
	group.POST("/template-instances/:id/releases/:releaseId/reconcile", reconcileTemplateInstanceRelease)
	group.GET("/assistants/:id/rollout-policies", getAssistantRolloutPolicies)
	group.POST("/assistants/:id/rollout-policies", createAssistantRolloutPolicy)
	group.PUT("/assistants/:id/rollout-policies/:policyId", updateAssistantRolloutPolicy)
}
