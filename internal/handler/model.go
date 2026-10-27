package handler

import (
	"github.com/gin-gonic/gin"

	"context"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

type createModelProviderRequest struct {
	ProviderType   string `json:"provider_type"`
	Name           string `json:"name"`
	ProviderName   string `json:"provider_name"`
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	ModelsJSON     string `json:"models_json"`
	Enabled        bool   `json:"enabled"`
	ModelName      string `json:"model_name"`
	MaxTokens      int    `json:"max_tokens"`
	RagflowFactory string `json:"ragflow_factory"`
	Register       *bool  `json:"register"`
}

type modelInfoRequest struct {
	ModelName  string   `json:"model_name"`
	ModelTypes []string `json:"model_type"`
	MaxTokens  int      `json:"max_tokens"`
	IsTools    bool     `json:"is_tools"`
	Thinking   bool     `json:"thinking"`
}

type createProviderInstanceRequest struct {
	InstanceName string             `json:"instance_name"`
	APIKey       string             `json:"api_key"`
	BaseURL      string             `json:"base_url"`
	Region       string             `json:"region"`
	Models       []modelInfoRequest `json:"model_info"`
}

func (r *createProviderInstanceRequest) toService() service.CreateProviderInstanceRequest {
	models := make([]service.ModelInfoInput, 0, len(r.Models))
	for _, m := range r.Models {
		models = append(models, service.ModelInfoInput{
			ModelName:  m.ModelName,
			ModelTypes: m.ModelTypes,
			MaxTokens:  m.MaxTokens,
			IsTools:    m.IsTools,
			Thinking:   m.Thinking,
		})
	}
	return service.CreateProviderInstanceRequest{
		InstanceName: r.InstanceName,
		APIKey:       r.APIKey,
		BaseURL:      r.BaseURL,
		Region:       r.Region,
		Models:       models,
	}
}

type updateModelRequest struct {
	Status    string                 `json:"status"`
	MaxTokens int                    `json:"max_tokens"`
	ModelType []string               `json:"model_type"`
	Extra     map[string]interface{} `json:"extra"`
}

type deleteInstancesRequest struct {
	Instances []string `json:"instances"`
}

type deleteModelsRequest struct {
	ModelIDs []string `json:"model_ids"`
}

type chatTestRequest struct {
	Message string `json:"message"`
}

// ListAvailableProviders returns the RAGFlow provider catalog.
func (h *Handler) ListAvailableProviders(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	items, err := h.Service.ListAvailableProviders(c.Request.Context())
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// ListModelProviders lists the tenant's providers with instance/model counts.
func (h *Handler) ListModelProviders(c *gin.Context) {
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "model-provider")
	if !ok {
		return
	}
	var items []service.ProviderView
	var err error
	if scope.ScopeAll() || scope.Kind == service.TenantScopeSpecific {
		items, err = h.Service.ListProviderGovernance(c.Request.Context(), scope, c.Query("name"))
	} else {
		items, err = h.Service.ListProviderConfigs(c.Request.Context(), scope.CurrentTenantID())
	}
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// GetModelProvider returns a single provider's summary for the detail view.
func (h *Handler) GetModelProvider(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	view, err := h.Service.GetProviderConfig(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, view)
}

// DeleteModelProvider removes a tenant's provider (local + RAGFlow).
func (h *Handler) DeleteModelProvider(c *gin.Context) {
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, crossTenant, ok := h.resolveModelProviderWrite(c)
	if !ok {
		return
	}
	providerID := c.Param("id")
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectModelProvider, model.ApprovalActionDelete, providerID, map[string]any{}) {
		return
	}
	if err := h.Service.DeleteModelProvider(ctx, tenantID, providerID); err != nil {
		response.Err(c, err)
		return
	}
	h.auditProvider(ctx, c, userID, tenantID, "model-provider.delete", providerID)
	response.OK(c, gin.H{"id": providerID})
}

// CreateModelProvider supports both the new factory-add (provider_name) and the
// legacy quick-add (provider_type + name + api_key).
func (h *Handler) CreateModelProvider(c *gin.Context) {
	var req createModelProviderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid model provider payload")
		return
	}
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, crossTenant, ok := h.resolveModelProviderWrite(c)
	if !ok {
		return
	}
	var p *model.ModelProvider
	var serviceErr error
	providerKey := req.ProviderName
	if providerKey == "" {
		providerKey = req.Name
	}
	approvalPayload := map[string]any{"provider_name": req.ProviderName}
	if req.ProviderName == "" {
		var err error
		approvalPayload, err = payloadFromRequest(req)
		if err != nil {
			response.Err(c, err)
			return
		}
	}
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectModelProvider, model.ApprovalActionCreate, "new:"+providerKey, approvalPayload) {
		return
	}
	if req.ProviderName != "" && req.Name == "" {
		p, serviceErr = h.Service.AddModelProvider(ctx, tenantID, req.ProviderName)
	} else {
		p, serviceErr = h.Service.CreateModelProvider(ctx, tenantID, service.CreateModelProviderRequest{
			ProviderType: req.ProviderType, Name: req.Name, BaseURL: req.BaseURL, APIKey: req.APIKey, ModelsJSON: req.ModelsJSON, Enabled: req.Enabled,
			ModelName: req.ModelName, MaxTokens: req.MaxTokens, RagflowFactory: req.RagflowFactory, Register: req.Register,
		})
	}
	if serviceErr != nil {
		response.Err(c, serviceErr)
		return
	}
	h.auditProvider(ctx, c, userID, tenantID, "model-provider.create", p.ID)
	response.OK(c, p)
}

// ListProviderInstances lists a provider's instances.
func (h *Handler) ListProviderInstances(c *gin.Context) {
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(c.Request.Context(), userID, "read", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "model-provider")
	if !ok {
		return
	}
	if scope.ScopeAll() {
		response.Fail(c, 400, 40000, "select one workspace to list provider instances")
		return
	}
	items, err := h.Service.ListProviderInstances(c.Request.Context(), scope.CurrentTenantID(), c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// CreateProviderInstance creates an instance + models (synced to RAGFlow).
func (h *Handler) CreateProviderInstance(c *gin.Context) {
	var req createProviderInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid provider instance payload")
		return
	}
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, userID, crossTenant, ok := h.resolveModelProviderWrite(c)
	if !ok {
		return
	}
	payload, payloadErr := payloadFromRequest(req.toService())
	if payloadErr != nil {
		response.Err(c, payloadErr)
		return
	}
	payload["provider_id"] = c.Param("id")
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectModelInstance, model.ApprovalActionCreate, "new:"+c.Param("id")+":"+req.InstanceName, payload) {
		return
	}
	inst, err := h.Service.CreateProviderInstance(ctx, tenantID, c.Param("id"), req.toService())
	if err != nil {
		response.Err(c, err)
		return
	}
	h.auditProvider(ctx, c, userID, tenantID, "model-provider.instance.create", inst.ID)
	response.OK(c, inst)
}

// UpdateProviderInstance updates an instance and reconciles its models.
func (h *Handler) UpdateProviderInstance(c *gin.Context) {
	var req createProviderInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid provider instance payload")
		return
	}
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceWriteScope(c, "model-provider")
	if !ok {
		return
	}
	tenantID := scope.CurrentTenantID()
	crossTenant := tenantID != scope.ActorTenantID
	payload, err := payloadFromRequest(req.toService())
	if err != nil {
		response.Err(c, err)
		return
	}
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectModelInstance, model.ApprovalActionUpdate, c.Param("id")+":"+c.Param("instanceId"), payload) {
		return
	}
	inst, err := h.Service.UpdateProviderInstance(ctx, tenantID, c.Param("id"), c.Param("instanceId"), req.toService())
	if err != nil {
		response.Err(c, err)
		return
	}
	h.auditProvider(ctx, c, userID, tenantID, "model-provider.instance.update", inst.ID)
	response.OK(c, inst)
}

// DeleteProviderInstances deletes one or more instances of a provider.
func (h *Handler) DeleteProviderInstances(c *gin.Context) {
	var req deleteInstancesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "instances is required")
		return
	}
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, userID, crossTenant, ok := h.resolveModelProviderWrite(c)
	if !ok {
		return
	}
	for _, instanceID := range req.Instances {
		if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectModelInstance, model.ApprovalActionDelete, c.Param("id")+":"+instanceID, map[string]any{}) {
			return
		}
	}
	if err := h.Service.DeleteProviderInstances(ctx, tenantID, c.Param("id"), req.Instances); err != nil {
		response.Err(c, err)
		return
	}
	h.auditProvider(ctx, c, userID, tenantID, "model-provider.instance.delete", c.Param("id"))
	response.OK(c, gin.H{"deleted": len(req.Instances)})
}

// VerifyProviderConnection validates an API key/base URL against RAGFlow.
func (h *Handler) VerifyProviderConnection(c *gin.Context) {
	var req createProviderInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid verify payload")
		return
	}
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, crossTenant, ok := h.resolveModelProviderWrite(c)
	if !ok {
		return
	}
	if crossTenant {
		response.Fail(c, 400, 40000, "cross-tenant provider verification is not enabled")
		return
	}
	result, err := h.Service.VerifyProviderConnection(ctx, tenantID, c.Param("id"), req.toService())
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, result)
}

// ListProviderInstanceModels lists models on an instance.
func (h *Handler) ListProviderInstanceModels(c *gin.Context) {
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(c.Request.Context(), userID, "read", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	scope, ok := h.resolveGovernanceScope(c, "model-provider")
	if !ok {
		return
	}
	if scope.ScopeAll() {
		response.Fail(c, 400, 40000, "select one workspace to list provider models")
		return
	}
	items, err := h.Service.ListProviderInstanceModels(c.Request.Context(), scope.CurrentTenantID(), c.Param("instanceId"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// AddProviderModel adds a model to an instance.
func (h *Handler) AddProviderModel(c *gin.Context) {
	var req modelInfoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid model payload")
		return
	}
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, userID, crossTenant, ok := h.resolveModelProviderWrite(c)
	if !ok {
		return
	}
	provID := c.Param("id")
	payload, payloadErr := payloadFromRequest(service.ModelInfoInput{
		ModelName: req.ModelName, ModelTypes: req.ModelTypes, MaxTokens: req.MaxTokens, IsTools: req.IsTools, Thinking: req.Thinking,
	})
	if payloadErr != nil {
		response.Err(c, payloadErr)
		return
	}
	payload["provider_id"] = provID
	payload["instance_id"] = c.Param("instanceId")
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectModelModel, model.ApprovalActionCreate, "new:"+provID+":"+c.Param("instanceId")+":"+req.ModelName, payload) {
		return
	}
	mod, err := h.Service.AddProviderModel(ctx, tenantID, provID, c.Param("instanceId"), service.ModelInfoInput{
		ModelName:  req.ModelName,
		ModelTypes: req.ModelTypes,
		MaxTokens:  req.MaxTokens,
		IsTools:    req.IsTools,
		Thinking:   req.Thinking,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	h.auditProvider(ctx, c, userID, tenantID, "model-provider.model.create", mod.ID)
	response.OK(c, mod)
}

// UpdateProviderModel patches a model's status/max_tokens/model_type.
func (h *Handler) UpdateProviderModel(c *gin.Context) {
	var req updateModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid model payload")
		return
	}
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, userID, crossTenant, ok := h.resolveModelProviderWrite(c)
	if !ok {
		return
	}
	payload, payloadErr := payloadFromRequest(struct {
		ProviderID string                 `json:"provider_id"`
		InstanceID string                 `json:"instance_id"`
		ModelID    string                 `json:"model_id"`
		Update     service.ModelUpdateDTO `json:"update"`
	}{ProviderID: c.Param("id"), InstanceID: c.Param("instanceId"), ModelID: c.Param("modelId"), Update: service.ModelUpdateDTO{
		Status: req.Status, MaxTokens: req.MaxTokens, ModelType: req.ModelType, Extra: req.Extra,
	}})
	if payloadErr != nil {
		response.Err(c, payloadErr)
		return
	}
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectModelModel, model.ApprovalActionUpdate, c.Param("id")+":"+c.Param("instanceId")+":"+c.Param("modelId"), payload) {
		return
	}
	mod, err := h.Service.UpdateProviderModel(ctx, tenantID, c.Param("id"), c.Param("instanceId"), c.Param("modelId"), service.ModelUpdateDTO{
		Status:    req.Status,
		MaxTokens: req.MaxTokens,
		ModelType: req.ModelType,
		Extra:     req.Extra,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	h.auditProvider(ctx, c, userID, tenantID, "model-provider.model.update", mod.ID)
	response.OK(c, mod)
}

// DeleteProviderModels removes models from an instance.
func (h *Handler) DeleteProviderModels(c *gin.Context) {
	var req deleteModelsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "model_ids is required")
		return
	}
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, userID, crossTenant, ok := h.resolveModelProviderWrite(c)
	if !ok {
		return
	}
	for _, modelID := range req.ModelIDs {
		payload, payloadErr := payloadFromRequest(map[string]any{
			"provider_id": c.Param("id"), "instance_id": c.Param("instanceId"), "model_id": modelID,
		})
		if payloadErr != nil {
			response.Err(c, payloadErr)
			return
		}
		if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectModelModel, model.ApprovalActionDelete, c.Param("id")+":"+c.Param("instanceId")+":"+modelID, payload) {
			return
		}
	}
	if err := h.Service.DeleteProviderModels(ctx, tenantID, c.Param("id"), c.Param("instanceId"), req.ModelIDs); err != nil {
		response.Err(c, err)
		return
	}
	h.auditProvider(ctx, c, userID, tenantID, "model-provider.model.delete", c.Param("instanceId"))
	response.OK(c, gin.H{"deleted": len(req.ModelIDs)})
}

// TestProviderModel sends a chat test message to a configured model.
func (h *Handler) TestProviderModel(c *gin.Context) {
	var req chatTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "message is required")
		return
	}
	ctx := c.Request.Context()
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "execute", "model-provider"); err != nil {
		response.Err(c, err)
		return
	}
	tenantID, _, crossTenant, ok := h.resolveModelProviderWrite(c)
	if !ok {
		return
	}
	payload, payloadErr := payloadFromRequest(map[string]any{
		"provider_id": c.Param("id"), "instance_id": c.Param("instanceId"), "model_id": c.Param("modelId"), "message": req.Message,
	})
	if payloadErr != nil {
		response.Err(c, payloadErr)
		return
	}
	if h.approvalHoldForTenant(c, tenantID, crossTenant, model.ApprovalObjectModelModel, model.ApprovalActionTest, c.Param("id")+":"+c.Param("instanceId")+":"+c.Param("modelId"), payload) {
		return
	}
	out, err := h.Service.TestProviderModel(ctx, tenantID, c.Param("id"), c.Param("instanceId"), c.Param("modelId"), req.Message)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, out)
}

func (h *Handler) auditProvider(ctx context.Context, c *gin.Context, userID, tenantID, action, resourceID string) {
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: action, Resource: "model-provider", ResourceID: resourceID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
}

func (h *Handler) resolveModelProviderWrite(c *gin.Context) (tenantID, userID string, crossTenant bool, ok bool) {
	scope, scopeOK := h.resolveGovernanceWriteScope(c, "model-provider")
	if !scopeOK {
		return "", "", false, false
	}
	return scope.CurrentTenantID(), c.GetString(middleware.ContextUserID), scope.CurrentTenantID() != scope.ActorTenantID, true
}

type createModelRouteRequest struct {
	ProviderID  string `json:"provider_id" binding:"required"`
	Scenario    string `json:"scenario"`
	ModelAlias  string `json:"model_alias" binding:"required"`
	TargetModel string `json:"target_model" binding:"required"`
}

// ListModelRoutes lists the tenant's routing rules.
func (h *Handler) ListModelRoutes(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "model-route"); err != nil {
		response.Err(c, err)
		return
	}
	items, err := h.Service.ListModelRoutes(c.Request.Context(), tenantID)
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, items)
}

// CreateModelRoute creates a routing rule under a tenant provider.
func (h *Handler) CreateModelRoute(c *gin.Context) {
	var req createModelRouteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid model route payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "model-route"); err != nil {
		response.Err(c, err)
		return
	}
	r, err := h.Service.CreateModelRoute(ctx, tenantID, service.CreateModelRouteRequest{
		ProviderID: req.ProviderID, Scenario: req.Scenario, ModelAlias: req.ModelAlias, TargetModel: req.TargetModel,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "model-route.create", Resource: "model-route", ResourceID: r.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, r)
}

type updateModelRouteRequest struct {
	ProviderID  string `json:"provider_id"`
	Scenario    string `json:"scenario"`
	ModelAlias  string `json:"model_alias"`
	TargetModel string `json:"target_model"`
}

type pinModelRouteRequest struct {
	ConnectionID      string `json:"connection_id"`
	ConnectionVersion int64  `json:"connection_version"`
	BindingID         string `json:"binding_id"`
	BindingVersion    int64  `json:"binding_version"`
	ModelRef          string `json:"model_ref"`
}

// UpdateModelRoute edits an existing routing rule.
func (h *Handler) UpdateModelRoute(c *gin.Context) {
	var req updateModelRouteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40000, "invalid model route payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "model-route"); err != nil {
		response.Err(c, err)
		return
	}
	r, err := h.Service.UpdateModelRoute(ctx, tenantID, c.Param("id"), service.CreateModelRouteRequest{
		ProviderID: req.ProviderID, Scenario: req.Scenario, ModelAlias: req.ModelAlias, TargetModel: req.TargetModel,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "model-route.update", Resource: "model-route", ResourceID: r.ID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, r)
}

// DeleteModelRoute removes a routing rule.
func (h *Handler) DeleteModelRoute(c *gin.Context) {
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "model-route"); err != nil {
		response.Err(c, err)
		return
	}
	routeID := c.Param("id")
	if err := h.Service.DeleteModelRoute(ctx, tenantID, routeID); err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{TenantID: tenantID, UserID: userID, Action: "model-route.delete", Resource: "model-route", ResourceID: routeID, IP: c.ClientIP(), TraceID: c.GetString("request_id")})
	response.OK(c, gin.H{"id": routeID})
}

// PinEnterpriseModelRoute pins a route to exact enterprise runtime versions.
func (h *Handler) PinEnterpriseModelRoute(c *gin.Context) {
	var req pinModelRouteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Fail(c, 400, 40065, "invalid model route pin payload")
		return
	}
	ctx := c.Request.Context()
	tenantID := c.GetString(middleware.ContextTenantID)
	userID := c.GetString(middleware.ContextUserID)
	if err := h.Service.Authorize(ctx, userID, "manage", "model-route"); err != nil {
		response.Err(c, err)
		return
	}
	pin, err := h.Service.PinEnterpriseModelRoute(ctx, userID, tenantID, c.Param("id"), service.PinEnterpriseModelRouteRequest{
		ConnectionID: req.ConnectionID, ConnectionVersion: req.ConnectionVersion,
		BindingID: req.BindingID, BindingVersion: req.BindingVersion, ModelRef: req.ModelRef,
	})
	if err != nil {
		response.Err(c, err)
		return
	}
	h.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenantID, UserID: userID, Action: "model-route.enterprise-pin",
		Resource: "model-route", ResourceID: c.Param("id"),
		DetailJSON: service.ModelRoutePinReference(pin), IP: c.ClientIP(), TraceID: c.GetString("request_id"),
	})
	response.OK(c, pin)
}

// ListEnterpriseModelRoutePins returns the route's immutable pin history.
func (h *Handler) ListEnterpriseModelRoutePins(c *gin.Context) {
	tenantID := c.GetString(middleware.ContextTenantID)
	if err := h.Service.Authorize(c.Request.Context(), c.GetString(middleware.ContextUserID), "read", "model-route"); err != nil {
		response.Err(c, err)
		return
	}
	pins, err := h.Service.ListEnterpriseModelRoutePins(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		response.Err(c, err)
		return
	}
	response.OK(c, pins)
}
