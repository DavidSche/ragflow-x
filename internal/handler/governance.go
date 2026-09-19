package handler

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

func governanceContext(c *gin.Context) (string, string, bool) {
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	return tenantID, userID, hCanGovernAll(c)
}

func hCanGovernAll(c *gin.Context) bool {
	h := c.MustGet("handler").(*Handler)
	return h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "governance.read", "tenant") == nil
}

func activePromptPolicy(h *Handler, c *gin.Context, scope, objectID string) *model.PromptPolicyVersion {
	policy, err := h.Service.ResolvePromptPolicy(c.Request.Context(), c.GetString(middleware.ContextTenantID), scope, objectID)
	if err != nil || policy == nil {
		return nil
	}
	return policy
}

func policyBody(policy *model.PromptPolicyVersion) map[string]interface{} {
	if policy == nil || strings.TrimSpace(policy.Payload) == "" {
		return map[string]interface{}{}
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(policy.Payload), &body); err != nil {
		return map[string]interface{}{}
	}
	return body
}

func applyPromptPolicyToChat(req *chatPayload, policy *model.PromptPolicyVersion) {
	body := policyBody(policy)
	if req.System == "" {
		req.System = stringFromPolicy(body, "system")
	}
	if req.Prologue == "" {
		req.Prologue = stringFromPolicy(body, "prologue")
	}
	if req.EmptyResponse == "" {
		req.EmptyResponse = stringFromPolicy(body, "empty_response")
	}
	if req.TopN == 0 {
		req.TopN = intFromPolicy(body, "top_n")
	}
	if req.TopK == 0 {
		req.TopK = intFromPolicy(body, "top_k")
	}
	if req.SimilarityThreshold == 0 {
		req.SimilarityThreshold = floatFromPolicy(body, "similarity_threshold")
	}
	if req.VectorSimilarityWeight == 0 {
		req.VectorSimilarityWeight = floatFromPolicy(body, "vector_similarity_weight")
	}
	if req.LLMSetting == nil {
		req.LLMSetting = mapFromPolicy(body, "llm_setting")
	}
	if req.PromptConfig == nil {
		req.PromptConfig = map[string]interface{}{}
	}
	for _, key := range []string{"system", "prologue", "empty_response"} {
		if value := stringFromPolicy(body, key); value != "" {
			if _, exists := req.PromptConfig[key]; !exists {
				req.PromptConfig[key] = value
			}
		}
	}
}

func applyPromptPolicyToSearch(req *searchAppPayload, policy *model.PromptPolicyVersion) {
	body := policyBody(policy)
	if req.SearchConfig == nil {
		req.SearchConfig = &searchAppConfigPayload{}
	}
	cfg := req.SearchConfig
	if cfg.TopK == nil {
		if value := intFromPolicy(body, "top_k"); value > 0 {
			cfg.TopK = &value
		}
	}
	if cfg.SimilarityThreshold == nil {
		if value := floatFromPolicy(body, "similarity_threshold"); value > 0 {
			cfg.SimilarityThreshold = &value
		}
	}
	if cfg.VectorSimilarityWeight == nil {
		if value := floatFromPolicy(body, "vector_similarity_weight"); value > 0 {
			cfg.VectorSimilarityWeight = &value
		}
	}
	if cfg.LLMSetting == nil {
		cfg.LLMSetting = mapFromPolicy(body, "llm_setting")
	}
}

func applyPromptPolicyToAgent(req *agentCreatePayload, policy *model.PromptPolicyVersion) {
	body := policyBody(policy)
	next, exists := body["dsl"]
	if !exists {
		return
	}
	nextDSL, ok := next.(map[string]interface{})
	if !ok {
		return
	}
	if req.Dsl == nil {
		req.Dsl = map[string]interface{}{}
	}
	for key, value := range nextDSL {
		if _, exists := req.Dsl[key]; !exists {
			req.Dsl[key] = value
		}
	}
}

func stringFromPolicy(body map[string]interface{}, key string) string {
	if value, ok := body[key].(string); ok {
		return value
	}
	return ""
}

func intFromPolicy(body map[string]interface{}, key string) int {
	switch value := body[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return 0
	}
}

func floatFromPolicy(body map[string]interface{}, key string) float64 {
	if value, ok := body[key].(float64); ok {
		return value
	}
	return 0
}

func mapFromPolicy(body map[string]interface{}, key string) map[string]interface{} {
	if value, ok := body[key].(map[string]interface{}); ok {
		return value
	}
	return nil
}

func authorizeResource(c *gin.Context, resource, action string) error {
	return c.MustGet("handler").(*Handler).Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), action, resource)
}

func governanceAudit(c *gin.Context, action, resource, resourceID string, detail string) {
	h := c.MustGet("handler").(*Handler)
	h.RecordAudit(c.Request.Context(), &model.AuditLog{
		TenantID: c.GetString(middleware.ContextTenantID), UserID: c.GetString(middleware.ContextUserID),
		Action: action, Resource: resource, ResourceID: resourceID, DetailJSON: detail,
		IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
}

func scenarioTemplateInput(c *gin.Context) (service.ScenarioTemplateInput, error) {
	var req service.ScenarioTemplateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		return req, httperr.BadRequest(40060, "invalid scenario template payload")
	}
	return req, nil
}

func parseTemplateVersion(c *gin.Context) (int64, error) {
	value := c.Query("version")
	if value == "" {
		return 0, nil
	}
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || version < 0 {
		return 0, httperr.BadRequest(40061, "version must be a non-negative integer")
	}
	return version, nil
}

func listScenarioTemplates(c *gin.Context) {
	if err := authorizeResource(c, "scenario-template", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, scopeAll := governanceContext(c)
	page, pageSize := pageParams(c)
	out, total, err := c.MustGet("handler").(*Handler).Service.ListScenarioTemplates(c.Request.Context(), tenantID, scopeAll, page, pageSize, repository.GovernanceFilter{
		Key: c.Query("key"), Name: c.Query("name"), Status: c.Query("status"),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, out, total, page, pageSize)
}

func createScenarioTemplate(c *gin.Context) {
	if err := authorizeResource(c, "scenario-template", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, userID, _ := governanceContext(c)
	req, err := scenarioTemplateInput(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	h := c.MustGet("handler").(*Handler)
	asset, err := h.Service.CreateScenarioTemplate(c.Request.Context(), tenantID, userID, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "scenario_template.create", "scenario-template", asset.ID, req.Key)
	response.OK(c, asset)
}

func updateScenarioTemplate(c *gin.Context) {
	if err := authorizeResource(c, "scenario-template", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, userID, _ := governanceContext(c)
	req, err := scenarioTemplateInput(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	h := c.MustGet("handler").(*Handler)
	asset, err := h.Service.UpdateScenarioTemplate(c.Request.Context(), tenantID, userID, c.Param("id"), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "scenario_template.version", "scenario-template", asset.ID, strconv.FormatInt(asset.LatestVersion, 10))
	response.OK(c, asset)
}

func getScenarioTemplate(c *gin.Context) {
	if err := authorizeResource(c, "scenario-template", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, _ := governanceContext(c)
	version, err := parseTemplateVersion(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	h := c.MustGet("handler").(*Handler)
	asset, selected, versions, err := h.Service.GetScenarioTemplate(c.Request.Context(), tenantID, c.Param("id"), version, true)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, gin.H{"template": asset, "version": selected, "versions": versions})
}

func exportScenarioTemplate(c *gin.Context) {
	if err := authorizeResource(c, "scenario-template", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, _ := governanceContext(c)
	version, err := parseTemplateVersion(c)
	if err != nil {
		response.Err(c, err)
		return
	}
	h := c.MustGet("handler").(*Handler)
	out, err := h.Service.ExportScenarioTemplate(c.Request.Context(), tenantID, c.Param("id"), version)
	if err != nil {
		response.Err(c, err)
		return
	}
	c.Header("Content-Disposition", "attachment; filename="+out.Key+".json")
	response.OK(c, out)
}

func importScenarioTemplate(c *gin.Context) {
	if err := authorizeResource(c, "scenario-template", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	var req service.ScenarioTemplateExport
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40062, "invalid scenario template export payload")
		return
	}
	tenantID, userID, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	asset, err := h.Service.ImportScenarioTemplate(c.Request.Context(), tenantID, userID, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "scenario_template.import", "scenario-template", asset.ID, req.Schema)
	response.OK(c, asset)
}

func copyScenarioTemplate(c *gin.Context) {
	tenantID, _, _ := governanceContext(c)
	var req struct {
		TargetTenantID string `json:"target_tenant_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40063, "target_tenant_id is required")
		return
	}
	h := c.MustGet("handler").(*Handler)
	asset, err := h.Service.CopyScenarioTemplate(c.Request.Context(), c.GetString(middleware.ContextUserID), tenantID, c.Param("id"), req.TargetTenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "scenario_template.copy", "scenario-template", asset.ID, req.TargetTenantID)
	response.OK(c, asset)
}

func archiveScenarioTemplate(c *gin.Context) {
	if err := authorizeResource(c, "scenario-template", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	if err := h.Service.ArchiveScenarioTemplate(c.Request.Context(), tenantID, c.Param("id")); err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "scenario_template.archive", "scenario-template", c.Param("id"), "")
	response.OK(c, gin.H{"id": c.Param("id"), "status": model.AssetStatusArchived})
}

func instantiateChatFromScenarioTemplate(c *gin.Context) {
	if err := authorizeResource(c, "scenario-template", "read"); err != nil {
		response.Err(c, err)
		return
	}
	if err := authorizeResource(c, "chat", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	var req service.ScenarioTemplateInstantiationInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40065, "invalid chat instantiation payload")
		return
	}
	tenantID, userID, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	createPayload, err := h.Service.PrepareScenarioTemplateInstantiation(
		c.Request.Context(), tenantID, c.Param("id"), req,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	name, _ := createPayload["name"].(string)
	if h.approvalHold(c, model.ApprovalObjectChat, model.ApprovalActionCreate, "new:"+name, createPayload) {
		return
	}
	out, err := h.Service.CreateChatFromScenarioTemplate(
		c.Request.Context(), tenantID, userID, c.Param("id"), req,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "chat.from_scenario_template", "chat", out.Chat.ID, c.Param("id"))
	response.OK(c, out)
}

func createEvalSetFromTemplate(c *gin.Context) {
	if err := authorizeResource(c, "eval-set", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40064, "invalid evaluation set payload")
		return
	}
	tenantID, userID, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	out, err := h.Service.CreateEvalSetFromTemplate(c.Request.Context(), tenantID, userID, c.Param("id"), req.Name)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "eval_set.from_template", "eval-set", out.ID, c.Param("id"))
	response.OK(c, out)
}

func createTemplateReleaseCandidate(c *gin.Context) {
	if err := authorizeResource(c, "release-governance", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	var req service.ScenarioTemplateReleaseCandidateInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40070, "invalid release candidate payload")
		return
	}
	tenantID, userID, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	result, err := h.Service.CreateScenarioTemplateReleaseCandidate(
		c.Request.Context(), tenantID, userID, c.Param("id"), req,
	)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "scenario_template.release_candidate", "release-governance", result.Candidate.ID, c.Param("id"))
	response.OK(c, result)
}

func generateMissingTemplateEvalSets(c *gin.Context) {
	if err := authorizeResource(c, "eval-set", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, userID, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	created, err := h.Service.GenerateMissingTemplateEvalSets(c.Request.Context(), tenantID, userID)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "eval_set.generate_missing", "eval-set", tenantID, strconv.Itoa(created))
	response.OK(c, gin.H{"created": created})
}

func listPromptPolicies(c *gin.Context) {
	if err := authorizeResource(c, "prompt-policy", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, scopeAll := governanceContext(c)
	page, pageSize := pageParams(c)
	h := c.MustGet("handler").(*Handler)
	out, total, err := h.Service.ListPromptPolicies(c.Request.Context(), tenantID, scopeAll, page, pageSize, repository.GovernanceFilter{
		Scope: c.Query("scope"), ObjectID: c.Query("object_id"),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, out, total, page, pageSize)
}

func createPromptPolicy(c *gin.Context) {
	if err := authorizeResource(c, "prompt-policy", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	var req service.PromptPolicyInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40065, "invalid prompt policy payload")
		return
	}
	tenantID, userID, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	policy, err := h.Service.SavePromptPolicy(c.Request.Context(), tenantID, userID, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "prompt_policy.create", "prompt-policy", policy.ID, policy.Scope)
	response.OK(c, policy)
}

func getPromptPolicy(c *gin.Context) {
	if err := authorizeResource(c, "prompt-policy", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	policy, err := h.Service.Store.GetPromptPolicy(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	if policy == nil {
		response.Err(c, httperr.NotFound("prompt policy not found"))
		return
	}
	response.OK(c, policy)
}

func rollbackPromptPolicy(c *gin.Context) {
	if err := authorizeResource(c, "prompt-policy", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	policy, err := h.Service.RollbackPromptPolicy(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "prompt_policy.rollback", "prompt-policy", policy.ID, strconv.FormatInt(policy.Version, 10))
	response.OK(c, policy)
}

func deletePromptPolicy(c *gin.Context) {
	if err := authorizeResource(c, "prompt-policy", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	deleted, err := h.Service.Store.DeletePromptPolicy(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	if !deleted {
		response.Err(c, httperr.NotFound("prompt policy not found"))
		return
	}
	governanceAudit(c, "prompt_policy.delete", "prompt-policy", c.Param("id"), "")
	response.OK(c, gin.H{"id": c.Param("id")})
}

func listKnowledgeLifecycle(c *gin.Context) {
	if err := authorizeResource(c, "knowledge-lifecycle", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	out, err := h.Service.ListKnowledgeLifecycle(c.Request.Context(), tenantID, repository.GovernanceFilter{
		OwnerID: c.Query("owner_id"), OwnerTeamID: c.Query("owner_team_id"),
		ReviewStatus: c.Query("review_status"), Search: c.Query("search"),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, out)
}

func updateDatasetLifecycle(c *gin.Context) {
	if err := authorizeResource(c, "knowledge-lifecycle", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	var req service.DatasetLifecycleInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40066, "invalid lifecycle payload")
		return
	}
	tenantID, _, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	out, err := h.Service.UpdateKnowledgeLifecycle(c.Request.Context(), tenantID, c.Param("id"), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "dataset.lifecycle", "knowledge-lifecycle", c.Param("id"), out.LifecycleStatus)
	response.OK(c, out)
}

func listEvalSets(c *gin.Context) {
	if err := authorizeResource(c, "eval-set", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, scopeAll := governanceContext(c)
	page, pageSize := pageParams(c)
	h := c.MustGet("handler").(*Handler)
	out, total, err := h.Service.ListEvalSets(c.Request.Context(), tenantID, scopeAll, page, pageSize, repository.GovernanceFilter{
		Name: c.Query("name"), AppType: c.Query("app_type"), Status: c.Query("status"),
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OKPage(c, out, total, page, pageSize)
}

func createEvalSet(c *gin.Context) {
	if err := authorizeResource(c, "eval-set", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	var req service.EvalSetInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40067, "invalid evaluation set payload")
		return
	}
	tenantID, userID, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	out, err := h.Service.CreateEvalSet(c.Request.Context(), tenantID, userID, req)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "eval_set.create", "eval-set", out.ID, req.Name)
	response.OK(c, out)
}

func getEvalSet(c *gin.Context) {
	if err := authorizeResource(c, "eval-set", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	evalSet, cases, err := h.Service.GetEvalSet(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	if evalSet == nil {
		response.Err(c, httperr.NotFound("evaluation set not found"))
		return
	}
	response.OK(c, gin.H{"eval_set": evalSet, "cases": cases})
}

func updateEvalSet(c *gin.Context) {
	if err := authorizeResource(c, "eval-set", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	var req service.EvalSetInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40068, "invalid evaluation set payload")
		return
	}
	tenantID, userID, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	out, err := h.Service.UpdateEvalSet(c.Request.Context(), tenantID, userID, c.Param("id"), req)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "eval_set.update", "eval-set", out.ID, req.Name)
	response.OK(c, out)
}

func deleteEvalSet(c *gin.Context) {
	if err := authorizeResource(c, "eval-set", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	if err := h.Service.DeleteEvalSet(c.Request.Context(), tenantID, c.Param("id")); err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "eval_set.delete", "eval-set", c.Param("id"), "")
	response.OK(c, gin.H{"id": c.Param("id")})
}

func convertBadcaseToEvalCase(c *gin.Context) {
	if err := authorizeResource(c, "eval-set", "manage"); err != nil {
		response.Err(c, err)
		return
	}
	var req struct {
		EvalSetID string `json:"eval_set_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40069, "eval_set_id is required")
		return
	}
	tenantID, _, scopeAll := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	evalCase, err := h.Service.ConvertBadcaseToEvalCase(c.Request.Context(), tenantID, scopeAll, c.Param("id"), req.EvalSetID)
	if err != nil {
		response.Err(c, err)
		return
	}
	governanceAudit(c, "eval_set.badcase_convert", "eval-set", evalCase.ID, c.Param("id"))
	response.OK(c, evalCase)
}

func governanceOverview(c *gin.Context) {
	if err := authorizeResource(c, "scenario-template", "read"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, _ := governanceContext(c)
	h := c.MustGet("handler").(*Handler)
	out, err := h.Service.GovernanceOverview(c.Request.Context(), tenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, out)
}

// RegisterGovernanceRoutes attaches handlers to a Gin group. Kept explicit to
// avoid shadowing imports with methods on the Handler type.
func RegisterGovernanceRoutes(group *gin.RouterGroup, h *Handler) {
	group.Use(func(c *gin.Context) {
		c.Set("handler", h)
		c.Next()
	})
	group.GET("/scenario-template-assets", listScenarioTemplates)
	group.POST("/scenario-template-assets", createScenarioTemplate)
	group.POST("/scenario-template-assets/import", importScenarioTemplate)
	group.PUT("/scenario-template-assets/:id", updateScenarioTemplate)
	group.GET("/scenario-template-assets/:id", getScenarioTemplate)
	group.GET("/scenario-template-assets/:id/export", exportScenarioTemplate)
	group.POST("/scenario-template-assets/:id/copy", copyScenarioTemplate)
	group.DELETE("/scenario-template-assets/:id", archiveScenarioTemplate)
	group.POST("/scenario-template-assets/:id/eval-set", createEvalSetFromTemplate)
	group.POST("/scenario-template-assets/:id/release-candidate", createTemplateReleaseCandidate)
	group.POST("/scenario-template-assets/:id/instantiate-chat", instantiateChatFromScenarioTemplate)

	group.GET("/prompt-policies", listPromptPolicies)
	group.POST("/prompt-policies", createPromptPolicy)
	group.GET("/prompt-policies/:id", getPromptPolicy)
	group.POST("/prompt-policies/:id/rollback", rollbackPromptPolicy)
	group.DELETE("/prompt-policies/:id", deletePromptPolicy)

	group.GET("/knowledge-lifecycle", listKnowledgeLifecycle)
	group.PUT("/datasets/:id/lifecycle", updateDatasetLifecycle)

	group.GET("/eval-sets", listEvalSets)
	group.POST("/eval-sets", createEvalSet)
	group.POST("/eval-sets/generate-from-templates", generateMissingTemplateEvalSets)
	group.GET("/eval-sets/:id", getEvalSet)
	group.PUT("/eval-sets/:id", updateEvalSet)
	group.DELETE("/eval-sets/:id", deleteEvalSet)
	group.POST("/knowledge-ops/events/:id/to-eval-case", convertBadcaseToEvalCase)

	group.GET("/governance/overview", governanceOverview)
}
