package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

var errApprovalOperationPending = errors.New("approval operation outcome is pending")
var errAgentShadowUnavailable = errors.New("agent shadow unavailable")

const approvalOperationPendingWindow = 5 * time.Minute

type ApprovalExecutor interface {
	Healthy(ctx context.Context) error
	Validate(ctx context.Context, approval *model.Approval) error
	Execute(ctx context.Context, approval *model.Approval) (map[string]any, error)
}

type ApprovalExecutorRegistry struct {
	executors map[string]ApprovalExecutor
}

func NewApprovalExecutorRegistry() *ApprovalExecutorRegistry {
	return &ApprovalExecutorRegistry{executors: map[string]ApprovalExecutor{}}
}

func (r *ApprovalExecutorRegistry) Register(key string, executor ApprovalExecutor) bool {
	if _, exists := r.executors[key]; exists {
		return false
	}
	r.executors[key] = executor
	return true
}

func (r *ApprovalExecutorRegistry) Get(key string) (ApprovalExecutor, bool) {
	executor, ok := r.executors[key]
	return executor, ok
}

func (s *Service) approvalExecutorRegistry() *ApprovalExecutorRegistry {
	if s.approvalExecutorRegistryOverride != nil {
		return s.approvalExecutorRegistryOverride
	}
	registry := NewApprovalExecutorRegistry()
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectDataset, model.ApprovalActionDelete), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectDataset, action: model.ApprovalActionDelete,
		validate: validateApprovalDatasetExists, execute: s.executeDatasetDelete,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectDataset, model.ApprovalActionCreate), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectDataset, action: model.ApprovalActionCreate,
		validate: validateApprovalDatasetCreate, execute: s.executeDatasetCreate,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectDataset, model.ApprovalActionUpdate), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectDataset, action: model.ApprovalActionUpdate,
		validate: validateApprovalDatasetExists, execute: s.executeDatasetUpdate,
	})
	for _, action := range []string{
		model.ApprovalActionUpdate, model.ApprovalActionDelete, model.ApprovalActionParse,
		model.ApprovalActionStop, model.ApprovalActionEnable, model.ApprovalActionDisable,
	} {
		registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectDocument, action), &serviceApprovalExecutor{
			svc: s, objectType: model.ApprovalObjectDocument, action: action,
			validate: validateApprovalDocumentAction, execute: s.executeDocumentAction,
		})
	}
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectDocumentChunk, model.ApprovalActionDelete), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectDocumentChunk, action: model.ApprovalActionDelete,
		validate: validateApprovalDocumentChunkAction, execute: s.executeDocumentChunkAction,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectDocumentChunk, model.ApprovalActionEnable), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectDocumentChunk, action: model.ApprovalActionEnable,
		validate: validateApprovalDocumentChunkAction, execute: s.executeDocumentChunkAction,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectDocumentChunk, model.ApprovalActionDisable), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectDocumentChunk, action: model.ApprovalActionDisable,
		validate: validateApprovalDocumentChunkAction, execute: s.executeDocumentChunkAction,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectAPIKey, model.ApprovalActionCreate), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectAPIKey, action: model.ApprovalActionCreate,
		validate: validateApprovalAPIKeyCreate, execute: s.executeAPIKeyCreate,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectAgent, model.ApprovalActionCreate), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectAgent, action: model.ApprovalActionCreate,
		validate: validateApprovalAgentCreate, execute: s.executeAgentCreate,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectAgent, model.ApprovalActionUpdate), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectAgent, action: model.ApprovalActionUpdate,
		validate: validateApprovalAgentAction, execute: s.executeAgentUpdate,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectAgent, model.ApprovalActionDelete), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectAgent, action: model.ApprovalActionDelete,
		validate: validateApprovalAgentAction, execute: s.executeAgentDelete,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectChat, model.ApprovalActionCreate), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectChat, action: model.ApprovalActionCreate,
		validate: validateApprovalChatCreate, execute: s.executeChatCreate,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectChat, model.ApprovalActionUpdate), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectChat, action: model.ApprovalActionUpdate,
		validate: validateApprovalChatAction, execute: s.executeChatUpdate,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectChat, model.ApprovalActionDelete), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectChat, action: model.ApprovalActionDelete,
		validate: validateApprovalChatAction, execute: s.executeChatDelete,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectAPIKey, model.ApprovalActionRevoke), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectAPIKey, action: model.ApprovalActionRevoke,
		validate: validateApprovalAPIKeyRevoke, execute: s.executeAPIKeyRevoke,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectModelProvider, model.ApprovalActionCreate), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectModelProvider, action: model.ApprovalActionCreate,
		validate: validateApprovalModelProviderCreate, execute: s.executeModelProviderCreate,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectModelProvider, model.ApprovalActionDelete), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectModelProvider, action: model.ApprovalActionDelete,
		validate: validateApprovalModelProviderDelete, execute: s.executeModelProviderDelete,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectModelInstance, model.ApprovalActionUpdate), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectModelInstance, action: model.ApprovalActionUpdate,
		validate: validateApprovalProviderInstanceUpdate, execute: s.executeProviderInstanceUpdate,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectModelInstance, model.ApprovalActionCreate), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectModelInstance, action: model.ApprovalActionCreate,
		validate: validateApprovalProviderInstanceCreate, execute: s.executeProviderInstanceCreate,
	})
	registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectModelInstance, model.ApprovalActionDelete), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectModelInstance, action: model.ApprovalActionDelete,
		validate: validateApprovalProviderInstanceDelete, execute: s.executeProviderInstanceDelete,
	})
	for _, action := range []string{model.ApprovalActionCreate, model.ApprovalActionUpdate, model.ApprovalActionDelete, model.ApprovalActionTest} {
		registry.Register(fmt.Sprintf("%s.%s", model.ApprovalObjectModelModel, action), &serviceApprovalExecutor{
			svc: s, objectType: model.ApprovalObjectModelModel, action: action,
			validate: validateApprovalProviderModel, execute: s.executeProviderModelAction,
		})
	}
	registry.Register(enterpriseApprovalExecutorKey(model.ApprovalObjectEnterpriseBinding, model.ApprovalActionBind), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectEnterpriseBinding, action: model.ApprovalActionBind,
		validate: validateEnterpriseConnectionApproval, execute: executeEnterpriseBindingCreate,
	})
	registry.Register(enterpriseApprovalExecutorKey(model.ApprovalObjectEnterpriseBinding, model.ApprovalActionUpdate), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectEnterpriseBinding, action: model.ApprovalActionUpdate,
		validate: validateEnterpriseConnectionApproval, execute: executeEnterpriseBindingUpdate,
	})
	registry.Register(enterpriseApprovalExecutorKey(model.ApprovalObjectEnterpriseBinding, model.ApprovalActionRevoke), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectEnterpriseBinding, action: model.ApprovalActionRevoke,
		validate: validateEnterpriseConnectionApproval, execute: executeEnterpriseBindingRevoke,
	})
	registry.Register(enterpriseApprovalExecutorKey(model.ApprovalObjectEnterpriseConnection, model.ApprovalActionRotateCredential), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectEnterpriseConnection, action: model.ApprovalActionRotateCredential,
		validate: validateEnterpriseConnectionApproval, execute: executeEnterpriseCredentialRotation,
	})
	registry.Register(enterpriseApprovalExecutorKey(model.ApprovalObjectEnterpriseConnection, model.ApprovalActionCreate), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectEnterpriseConnection, action: model.ApprovalActionCreate,
		validate: validateEnterpriseConnectionApproval, execute: executeEnterpriseConnectionCreate,
	})
	registry.Register(enterpriseApprovalExecutorKey(model.ApprovalObjectEnterpriseConnection, model.ApprovalActionUpdate), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectEnterpriseConnection, action: model.ApprovalActionUpdate,
		validate: validateEnterpriseConnectionApproval, execute: executeEnterpriseConnectionUpdate,
	})
	registry.Register(enterpriseApprovalExecutorKey(model.ApprovalObjectEnterpriseConnection, model.ApprovalActionRetire), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectEnterpriseConnection, action: model.ApprovalActionRetire,
		validate: validateEnterpriseConnectionApproval, execute: executeEnterpriseConnectionRetire,
	})
	registry.Register(enterpriseApprovalExecutorKey(model.ApprovalObjectEnterpriseConnection, model.ApprovalActionDeprecate), &serviceApprovalExecutor{
		svc: s, objectType: model.ApprovalObjectEnterpriseConnection, action: model.ApprovalActionDeprecate,
		validate: validateEnterpriseConnectionApproval, execute: executeEnterpriseConnectionDeprecate,
	})
	return registry
}

type serviceApprovalExecutor struct {
	svc        *Service
	objectType string
	action     string
	validate   func(context.Context, *Service, *model.Approval) error
	execute    func(context.Context, *Service, *model.Approval) (map[string]any, error)
}

func (e *serviceApprovalExecutor) Healthy(context.Context) error { return nil }

func (e *serviceApprovalExecutor) Validate(ctx context.Context, approval *model.Approval) error {
	return e.validate(ctx, e.svc, approval)
}

func (e *serviceApprovalExecutor) Execute(ctx context.Context, approval *model.Approval) (map[string]any, error) {
	return e.execute(ctx, e.svc, approval)
}

func approvalPayload(approval *model.Approval) (map[string]any, error) {
	payload := map[string]any{}
	if approval.PayloadJSON == "" {
		return payload, nil
	}
	if err := json.Unmarshal([]byte(approval.PayloadJSON), &payload); err != nil {
		return nil, httperr.BadRequest(40061, "invalid approval payload")
	}
	return payload, nil
}

func validateApprovalDatasetExists(ctx context.Context, svc *Service, approval *model.Approval) error {
	dataset, err := svc.Store.GetDatasetLink(ctx, approval.TenantID, approval.ObjectID)
	if err != nil {
		return err
	}
	if dataset == nil {
		return httperr.NotFound("dataset not found")
	}
	return nil
}

func validateApprovalDatasetCreate(_ context.Context, _ *Service, approval *model.Approval) error {
	payload, err := approvalPayload(approval)
	if err != nil {
		return err
	}
	name, _ := payload["name"].(string)
	if strings.TrimSpace(name) == "" {
		return httperr.BadRequest(40089, "dataset name is required")
	}
	return nil
}

func validateApprovalAgentCreate(_ context.Context, _ *Service, approval *model.Approval) error {
	payload, err := approvalPayload(approval)
	if err != nil {
		return err
	}
	title, _ := payload["title"].(string)
	dsl, _ := payload["dsl"].(map[string]any)
	if strings.TrimSpace(title) == "" || len(dsl) == 0 {
		return httperr.BadRequest(40091, "agent title and dsl are required")
	}
	return nil
}

func validateApprovalAgentAction(ctx context.Context, svc *Service, approval *model.Approval) error {
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return err
	}
	agent, err := svc.Store.GetAgentShadow(ctx, targetTenantID, approval.ObjectID, false)
	if err != nil {
		return err
	}
	if agent == nil {
		return httperr.NotFound("agent not found")
	}
	snapshot := map[string]any{}
	if err := json.Unmarshal([]byte(approval.SnapshotJSON), &snapshot); err != nil {
		return httperr.BadRequest(40091, "invalid agent approval snapshot")
	}
	currentUpdatedAt := agent.UpdatedAt.UTC().Format(time.RFC3339Nano)
	if fmt.Sprint(snapshot["agent_updated_at"]) != currentUpdatedAt {
		return httperr.New(409, 40972, "agent approval snapshot has changed")
	}
	return nil
}

func validateApprovalChatCreate(_ context.Context, _ *Service, approval *model.Approval) error {
	payload, err := approvalPayload(approval)
	if err != nil {
		return err
	}
	name, _ := payload["name"].(string)
	if strings.TrimSpace(name) == "" {
		return httperr.BadRequest(40090, "chat name is required")
	}
	if templateID, _ := payload["scenario_template_id"].(string); strings.TrimSpace(templateID) != "" {
		version := int64(approvalAnyToFloat(payload["scenario_template_version"]))
		if version <= 0 {
			return httperr.BadRequest(40090, "scenario template version is required")
		}
	}
	return nil
}

func validateApprovalChatAction(ctx context.Context, svc *Service, approval *model.Approval) error {
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return err
	}
	chat, err := svc.Store.GetChatShadow(ctx, targetTenantID, approval.ObjectID, false)
	if err != nil {
		return err
	}
	if chat == nil {
		return httperr.NotFound("chat not found")
	}
	snapshot := map[string]any{}
	if err := json.Unmarshal([]byte(approval.SnapshotJSON), &snapshot); err != nil {
		return httperr.BadRequest(40090, "invalid chat approval snapshot")
	}
	currentUpdatedAt := chat.UpdatedAt.UTC().Format(time.RFC3339Nano)
	if fmt.Sprint(snapshot["chat_updated_at"]) != currentUpdatedAt {
		return httperr.New(409, 40972, "chat approval snapshot has changed")
	}
	return nil
}

func validateApprovalAPIKeyCreate(_ context.Context, _ *Service, approval *model.Approval) error {
	payload, err := approvalPayload(approval)
	if err != nil {
		return err
	}
	name, _ := payload["name"].(string)
	if strings.TrimSpace(name) == "" {
		return httperr.BadRequest(40062, "key name is required")
	}
	return nil
}

func validateApprovalAPIKeyRevoke(ctx context.Context, svc *Service, approval *model.Approval) error {
	key, err := svc.Store.GetAPIKey(ctx, approval.TenantID, approval.ObjectID)
	if err != nil {
		return err
	}
	if key == nil {
		return httperr.NotFound("api key not found")
	}
	return nil
}

func validateApprovalModelProviderCreate(_ context.Context, svc *Service, approval *model.Approval) error {
	payload, err := approvalPayload(approval)
	if err != nil {
		return err
	}
	var req CreateModelProviderRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return httperr.BadRequest(40063, "invalid model provider payload")
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.ProviderType) == "" {
		return httperr.BadRequest(40064, "provider type and name are required")
	}
	return ValidateProviderBaseURL(req.BaseURL, svc.allowPrivateProviderBaseURL)
}

func validateApprovalModelProviderDelete(ctx context.Context, svc *Service, approval *model.Approval) error {
	provider, err := svc.Store.GetModelProvider(ctx, approval.TenantID, approval.ObjectID)
	if err != nil {
		return err
	}
	if provider == nil {
		return httperr.NotFound("model provider not found")
	}
	return nil
}

func validateApprovalProviderInstanceUpdate(ctx context.Context, svc *Service, approval *model.Approval) error {
	providerID, instanceID, ok := splitApprovalObjectID(approval.ObjectID)
	if !ok {
		return httperr.BadRequest(40065, "provider instance object_id must be providerId:instanceId")
	}
	instance, err := svc.Store.GetModelProviderInstance(ctx, approval.TenantID, providerID, instanceID)
	if err != nil {
		return err
	}
	if instance == nil {
		return httperr.NotFound("provider instance not found")
	}
	payload, err := approvalPayload(approval)
	if err != nil {
		return err
	}
	var req CreateProviderInstanceRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return httperr.BadRequest(40066, "invalid provider instance payload")
	}
	return ValidateProviderBaseURL(req.BaseURL, svc.allowPrivateProviderBaseURL)
}

func validateApprovalProviderInstanceCreate(ctx context.Context, svc *Service, approval *model.Approval) error {
	payload, err := approvalPayload(approval)
	if err != nil {
		return err
	}
	var req CreateProviderInstanceRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return httperr.BadRequest(40066, "invalid provider instance payload")
	}
	if strings.TrimSpace(req.InstanceName) == "" {
		return httperr.BadRequest(40040, "instance_name is required")
	}
	providerID, _ := payload["provider_id"].(string)
	provider, err := svc.Store.GetModelProvider(ctx, approval.TenantID, providerID)
	if err != nil {
		return err
	}
	if provider == nil {
		return httperr.NotFound("model provider not found")
	}
	return ValidateProviderBaseURL(req.BaseURL, svc.allowPrivateProviderBaseURL)
}

func validateApprovalProviderInstanceDelete(ctx context.Context, svc *Service, approval *model.Approval) error {
	providerID, instanceID, ok := splitApprovalObjectID(approval.ObjectID)
	if !ok {
		return httperr.BadRequest(40065, "provider instance object_id must be providerId:instanceId")
	}
	instance, err := svc.Store.GetModelProviderInstance(ctx, approval.TenantID, providerID, instanceID)
	if err != nil {
		return err
	}
	if instance == nil {
		return httperr.NotFound("provider instance not found")
	}
	return nil
}

func validateApprovalProviderModel(ctx context.Context, svc *Service, approval *model.Approval) error {
	payload, err := approvalPayload(approval)
	if err != nil {
		return err
	}
	providerID, _ := payload["provider_id"].(string)
	instanceID, _ := payload["instance_id"].(string)
	modelID, _ := payload["model_id"].(string)
	if providerID == "" || instanceID == "" {
		return httperr.BadRequest(40066, "provider_id and instance_id are required")
	}
	if approval.Action != model.ApprovalActionCreate {
		if modelID == "" {
			return httperr.BadRequest(40066, "model_id is required")
		}
		mod, err := svc.Store.GetModelProviderModel(ctx, approval.TenantID, instanceID, modelID)
		if err != nil {
			return err
		}
		if mod == nil {
			return httperr.NotFound("model not found")
		}
	}
	return nil
}

// approvalCredentialValue restores an approved secret without putting it back
// into the approval payload, result ledger, logs, or audit events.
func (s *Service) approvalCredentialValue(ctx context.Context, approval *model.Approval) (string, error) {
	credential, err := s.Store.GetRetainedApprovalCredential(ctx, approval.TenantID, approval.ID)
	if err != nil {
		return "", err
	}
	if credential == nil || credential.Ciphertext == "" {
		return "", nil
	}
	raw, err := crypto.Decrypt(s.EncryptKey, credential.Ciphertext)
	if err != nil {
		return "", httperr.Internal("failed to decrypt approval credential")
	}
	return raw, nil
}

func (s *Service) executeDatasetDelete(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	if err := s.DeleteDataset(ctx, targetTenantID, approval.ObjectID); err != nil {
		return nil, err
	}
	return map[string]any{"dataset_id": approval.ObjectID}, nil
}

func (s *Service) executeDatasetCreate(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	name, _ := payload["name"].(string)
	if strings.TrimSpace(name) == "" {
		return nil, httperr.BadRequest(40089, "dataset name is required")
	}
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	dataset, err := s.CreateDataset(ctx, targetTenantID, name)
	if err != nil {
		return nil, err
	}
	return map[string]any{"dataset_id": dataset.ID, "name": dataset.Name}, nil
}

func (s *Service) executeDatasetUpdate(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	var req ApprovalDatasetUpdateRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return nil, httperr.BadRequest(40067, "invalid dataset update payload")
	}
	if req.Name == "" && req.ProjectID == "" && req.Config == nil {
		return nil, httperr.BadRequest(40067, "dataset name, project or config is required")
	}
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	if req.Name != "" {
		if _, err := s.UpdateDataset(ctx, targetTenantID, approval.ObjectID, req.Name); err != nil {
			return nil, err
		}
	}
	if req.ProjectID != "" {
		project, err := s.Store.GetProject(ctx, targetTenantID, req.ProjectID)
		if err != nil {
			return nil, err
		}
		if project == nil {
			return nil, httperr.NotFound("project not found")
		}
		if err := s.Store.SetDatasetProject(ctx, targetTenantID, approval.ObjectID, req.ProjectID); err != nil {
			return nil, err
		}
	}
	if req.Config != nil {
		if err := s.UpdateDatasetConfig(ctx, targetTenantID, approval.ObjectID, *req.Config); err != nil {
			return nil, err
		}
	}
	result := map[string]any{"dataset_id": approval.ObjectID}
	if req.Name != "" {
		result["name"] = req.Name
	}
	if req.ProjectID != "" {
		result["project_id"] = req.ProjectID
	}
	return result, nil
}

type ApprovalDatasetUpdateRequest struct {
	Name      string                       `json:"name"`
	ProjectID string                       `json:"project_id"`
	Config    *ragflow.DatasetConfigUpdate `json:"config"`
}

type ApprovalAgentCreateRequest struct {
	Title          string                 `json:"title"`
	Dsl            map[string]interface{} `json:"dsl"`
	Release        bool                   `json:"release"`
	CanvasCategory string                 `json:"canvas_category"`
}

type ApprovalAgentUpdateRequest struct {
	Title             string                 `json:"title"`
	Dsl               map[string]interface{} `json:"dsl"`
	Release           *bool                  `json:"release"`
	RollbackVersionID string                 `json:"rollback_version_id"`
}

type ApprovalChatCreateRequest struct {
	Name                    string        `json:"name"`
	DatasetIDs              []string      `json:"dataset_ids"`
	DatasetSuggestions      []string      `json:"dataset_suggestions"`
	CreateMissingDatasets   bool          `json:"create_missing_datasets"`
	ScenarioTemplateID      string        `json:"scenario_template_id"`
	ScenarioTemplateVersion int64         `json:"scenario_template_version"`
	Authoring               ChatAuthoring `json:"authoring"`
}

type ApprovalChatUpdateRequest struct {
	Name       string         `json:"name"`
	DatasetIDs []string       `json:"dataset_ids"`
	Authoring  *ChatAuthoring `json:"authoring"`
}

// normalizeDatasetUpdateApproval accepts both the approval transport shape
// (`config`) and the dataset-config API shape (flat fields) so callers do not
// need to know two different payload contracts for the same update.
func normalizeDatasetUpdateApproval(payload map[string]any) (*ApprovalDatasetUpdateRequest, error) {
	var req ApprovalDatasetUpdateRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return nil, err
	}
	if req.Config != nil {
		return &req, nil
	}
	var cfg ragflow.DatasetConfigUpdate
	if err := decodeApprovalPayload(payload, &cfg); err != nil {
		return nil, err
	}
	if cfg.Name != nil || cfg.Description != nil || cfg.ChunkMethod != nil || cfg.EmbeddingModel != nil || cfg.Permission != nil || cfg.ParserConfig != nil {
		req.Config = &cfg
	}
	return &req, nil
}

func (s *Service) executeAgentCreate(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	var req ApprovalAgentCreateRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return nil, httperr.BadRequest(40091, "invalid agent create payload")
	}
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	agent, err := s.CreateAgent(ctx, targetTenantID, req.Title, req.Dsl, req.Release, req.CanvasCategory)
	if err != nil {
		return nil, err
	}
	if err := s.SetAgentOwner(ctx, agent, approval.RequesterID); err != nil {
		return nil, err
	}
	return map[string]any{"agent_id": agent.ID, "title": agent.Title}, nil
}

func (s *Service) executeAgentUpdate(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	var req ApprovalAgentUpdateRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return nil, httperr.BadRequest(40091, "invalid agent update payload")
	}
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	if req.RollbackVersionID != "" {
		if err := s.RollbackAgentVersion(ctx, targetTenantID, approval.ObjectID, req.RollbackVersionID, false); err != nil {
			return nil, err
		}
		return map[string]any{"agent_id": approval.ObjectID, "rollback_version_id": req.RollbackVersionID}, nil
	}
	agent, err := s.UpdateAgent(ctx, targetTenantID, approval.ObjectID, req.Title, req.Dsl, req.Release, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{"agent_id": agent.ID, "title": agent.Title, "release": agent.Release}, nil
}

func (s *Service) executeAgentDelete(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	if err := s.DeleteAgent(ctx, targetTenantID, approval.ObjectID, false); err != nil {
		return nil, err
	}
	return map[string]any{"agent_id": approval.ObjectID, "deleted": true}, nil
}

func (s *Service) executeChatCreate(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	var req ApprovalChatCreateRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return nil, httperr.BadRequest(40090, "invalid chat create payload")
	}
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	if req.ScenarioTemplateID != "" {
		out, err := s.CreateChatFromScenarioTemplate(ctx, targetTenantID, approval.RequesterID, req.ScenarioTemplateID, ScenarioTemplateInstantiationInput{
			Name:                  req.Name,
			DatasetIDs:            req.DatasetIDs,
			DatasetSuggestions:    req.DatasetSuggestions,
			CreateMissingDatasets: req.CreateMissingDatasets,
			TemplateVersion:       req.ScenarioTemplateVersion,
			Authoring:             req.Authoring,
		})
		if err != nil {
			return nil, err
		}
		if err := s.SetChatOwner(ctx, out.Chat, approval.RequesterID); err != nil {
			return nil, err
		}
		return map[string]any{
			"chat_id":                   out.Chat.ID,
			"name":                      out.Chat.Name,
			"scenario_template_id":      req.ScenarioTemplateID,
			"scenario_template_version": req.ScenarioTemplateVersion,
			"created_dataset_ids":       out.CreatedDatasetIDs,
		}, nil
	}
	chat, err := s.CreateChatWithConfig(ctx, targetTenantID, req.Name, req.DatasetIDs, req.Authoring)
	if err != nil {
		return nil, err
	}
	if err := s.SetChatOwner(ctx, chat, approval.RequesterID); err != nil {
		return nil, err
	}
	return map[string]any{"chat_id": chat.ID, "name": chat.Name}, nil
}

func (s *Service) executeChatUpdate(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	var req ApprovalChatUpdateRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return nil, httperr.BadRequest(40090, "invalid chat update payload")
	}
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	authoring := ChatAuthoring{}
	if req.Authoring != nil {
		authoring = *req.Authoring
	}
	chat, err := s.UpdateChatWithConfig(ctx, targetTenantID, approval.ObjectID, req.Name, req.DatasetIDs, authoring, false)
	if err != nil {
		return nil, err
	}
	return map[string]any{"chat_id": chat.ID, "name": chat.Name}, nil
}

func (s *Service) executeChatDelete(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	if err := s.DeleteChat(ctx, targetTenantID, approval.ObjectID, false); err != nil {
		return nil, err
	}
	return map[string]any{"chat_id": approval.ObjectID, "deleted": true}, nil
}

func (s *Service) executeAPIKeyCreate(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	name, _ := payload["name"].(string)
	tokenQuota := int64(approvalAnyToFloat(payload["token_quota"]))
	requestQuota := int64(approvalAnyToFloat(payload["request_quota"]))
	expiry, err := approvalOptionalTime(payload["expiry_at"])
	if err != nil {
		return nil, httperr.BadRequest(40068, "invalid key expiry")
	}
	var scopes *APIKeyScopes
	if payload["scopes"] != nil {
		scopeData, err := json.Marshal(payload["scopes"])
		if err != nil {
			return nil, httperr.BadRequest(40068, "invalid key scopes")
		}
		scopes = &APIKeyScopes{}
		if err := json.Unmarshal(scopeData, scopes); err != nil {
			return nil, httperr.BadRequest(40068, "invalid key scopes")
		}
	}
	var allowedIPs *[]string
	if payload["allowed_ips"] != nil {
		ipData, err := json.Marshal(payload["allowed_ips"])
		if err != nil {
			return nil, httperr.BadRequest(40068, "invalid key allowed ips")
		}
		var ips []string
		if err := json.Unmarshal(ipData, &ips); err != nil {
			return nil, httperr.BadRequest(40068, "invalid key allowed ips")
		}
		allowedIPs = &ips
	}
	raw, key, err := s.CreateAPIKey(ctx, approval.TenantID, approval.RequesterID, name, expiry, tokenQuota, requestQuota, scopes, allowedIPs)
	if err != nil {
		return nil, err
	}
	secretCiphertext, err := crypto.Encrypt(s.EncryptKey, raw)
	if err != nil {
		return nil, httperr.Internal("failed to encrypt approval secret")
	}
	return map[string]any{"key_id": key.ID, "name": key.Name, "api_key_ciphertext": secretCiphertext}, nil
}

func (s *Service) executeAPIKeyRevoke(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	if err := s.RevokeAPIKey(ctx, approval.TenantID, approval.ObjectID); err != nil {
		return nil, err
	}
	return map[string]any{"key_id": approval.ObjectID}, nil
}

func (s *Service) executeModelProviderCreate(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	var req CreateModelProviderRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return nil, httperr.BadRequest(40069, "invalid model provider payload")
	}
	apiKey, err := s.approvalCredentialValue(ctx, approval)
	if err != nil {
		return nil, err
	}
	req.APIKey = apiKey
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.ProviderName) != "" {
		provider, err := s.AddModelProvider(ctx, targetTenantID, req.ProviderName)
		if err != nil {
			return nil, err
		}
		return map[string]any{"provider_id": provider.ID, "name": provider.Name}, nil
	}
	provider, err := s.CreateModelProvider(ctx, targetTenantID, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{"provider_id": provider.ID, "name": provider.Name}, nil
}

func (s *Service) executeProviderInstanceUpdate(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	providerID, instanceID, ok := splitApprovalObjectID(approval.ObjectID)
	if !ok {
		return nil, httperr.BadRequest(40070, "provider instance object_id must be providerId:instanceId")
	}
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	var req CreateProviderInstanceRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return nil, httperr.BadRequest(40071, "invalid provider instance payload")
	}
	apiKey, err := s.approvalCredentialValue(ctx, approval)
	if err != nil {
		return nil, err
	}
	req.APIKey = apiKey
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	instance, err := s.UpdateProviderInstance(ctx, targetTenantID, providerID, instanceID, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{"provider_id": providerID, "instance_id": instance.ID}, nil
}

func (s *Service) executeModelProviderDelete(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	if err := s.DeleteModelProvider(ctx, targetTenantID, approval.ObjectID); err != nil {
		return nil, err
	}
	return map[string]any{"provider_id": approval.ObjectID}, nil
}

func (s *Service) executeProviderInstanceCreate(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	var req CreateProviderInstanceRequest
	if err := decodeApprovalPayload(payload, &req); err != nil {
		return nil, httperr.BadRequest(40071, "invalid provider instance payload")
	}
	providerID, _ := payload["provider_id"].(string)
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	apiKey, err := s.approvalCredentialValue(ctx, approval)
	if err != nil {
		return nil, err
	}
	req.APIKey = apiKey
	instance, err := s.CreateProviderInstance(ctx, targetTenantID, providerID, req)
	if err != nil {
		return nil, err
	}
	return map[string]any{"provider_id": providerID, "instance_id": instance.ID}, nil
}

func (s *Service) executeProviderInstanceDelete(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	providerID, instanceID, ok := splitApprovalObjectID(approval.ObjectID)
	if !ok {
		return nil, httperr.BadRequest(40070, "provider instance object_id must be providerId:instanceId")
	}
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	if err := s.DeleteProviderInstances(ctx, targetTenantID, providerID, []string{instanceID}); err != nil {
		return nil, err
	}
	return map[string]any{"provider_id": providerID, "instance_id": instanceID, "deleted": true}, nil
}

func (s *Service) executeProviderModelAction(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	providerID, _ := payload["provider_id"].(string)
	instanceID, _ := payload["instance_id"].(string)
	modelID, _ := payload["model_id"].(string)
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	switch approval.Action {
	case model.ApprovalActionCreate:
		var req ModelInfoInput
		if err := decodeApprovalPayload(payload, &req); err != nil {
			return nil, httperr.BadRequest(40071, "invalid model payload")
		}
		mod, err := s.AddProviderModel(ctx, targetTenantID, providerID, instanceID, req)
		if err != nil {
			return nil, err
		}
		return map[string]any{"provider_id": providerID, "instance_id": instanceID, "model_id": mod.ID}, nil
	case model.ApprovalActionUpdate:
		updateRaw, err := json.Marshal(payload["update"])
		if err != nil {
			return nil, httperr.BadRequest(40071, "invalid model update payload")
		}
		var req ModelUpdateDTO
		if err := json.Unmarshal(updateRaw, &req); err != nil {
			return nil, httperr.BadRequest(40071, "invalid model update payload")
		}
		mod, err := s.UpdateProviderModel(ctx, targetTenantID, providerID, instanceID, modelID, req)
		if err != nil {
			return nil, err
		}
		return map[string]any{"provider_id": providerID, "instance_id": instanceID, "model_id": mod.ID}, nil
	case model.ApprovalActionDelete:
		if err := s.DeleteProviderModels(ctx, targetTenantID, providerID, instanceID, []string{modelID}); err != nil {
			return nil, err
		}
		return map[string]any{"provider_id": providerID, "instance_id": instanceID, "model_id": modelID, "deleted": true}, nil
	case model.ApprovalActionTest:
		message, _ := payload["message"].(string)
		out, err := s.TestProviderModel(ctx, targetTenantID, providerID, instanceID, modelID, message)
		if err != nil {
			return nil, err
		}
		return map[string]any{"provider_id": providerID, "instance_id": instanceID, "model_id": modelID, "output": out}, nil
	default:
		return nil, httperr.BadRequest(40070, "unsupported model action")
	}
}

func approvalOptionalTime(value any) (*time.Time, error) {
	text, _ := value.(string)
	if text == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

type approvalExecuteWorker struct {
	svc *Service
}

func (w *approvalExecuteWorker) Kind() string { return model.JobKindApprovalExecute }

func (w *approvalExecuteWorker) Run(ctx context.Context, job *model.Job) error {
	var payload approvalExecutionPayload
	if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
		return err
	}
	approval, err := w.svc.Store.GetApproval(ctx, job.TenantID, payload.ApprovalID)
	if err != nil {
		return err
	}
	if approval == nil {
		return httperr.NotFound("approval not found")
	}
	actingContext, err := w.svc.validateApprovalExecutionPayload(ctx, approval, payload)
	if err != nil {
		return err
	}
	operation, proceed, resolveErr := w.resolveApprovalExecution(ctx, job, approval, actingContext)
	if resolveErr != nil {
		return resolveErr
	}
	if !proceed {
		return nil
	}
	now := time.Now().UTC()
	claimedContext := actingContext
	if approval.Status == model.ApprovalStatusApproved {
		claimed, err := w.svc.Store.TransitionApproval(ctx, approval.TenantID, approval.ID, model.ApprovalStatusApproved, model.ApprovalStatusExecuting, nil)
		if err != nil {
			_, _ = w.svc.Store.CompleteApprovalOperation(ctx, operation.ID, model.ApprovalOperationRunning, model.ApprovalOperationFailed, "{}", "approval transition failed", now)
			_, _ = w.svc.Store.CompleteActingContext(ctx, claimedContext.ID, model.ActingContextClaimed, model.ActingContextFailed)
			return err
		}
		if !claimed {
			return nil
		}
		approval.Status = model.ApprovalStatusExecuting
	}
	if approval.Status != model.ApprovalStatusExecuting {
		_, _ = w.svc.Store.CompleteApprovalOperation(ctx, operation.ID, model.ApprovalOperationRunning, model.ApprovalOperationFailed, "{}", "approval is not executing", now)
		_, _ = w.svc.Store.CompleteActingContext(ctx, claimedContext.ID, model.ActingContextClaimed, model.ActingContextFailed)
		return nil
	}
	registry := w.svc.approvalExecutorRegistry()
	executor, ok := registry.Get(fmt.Sprintf("%s.%s", approval.ObjectType, approval.Action))
	if !ok {
		_, _ = w.svc.Store.CompleteApprovalOperation(ctx, operation.ID, model.ApprovalOperationRunning, model.ApprovalOperationFailed, "{}", "approval executor not found", now)
		_, _ = w.svc.Store.CompleteActingContext(ctx, claimedContext.ID, model.ActingContextClaimed, model.ActingContextFailed)
		return w.finishApprovalExecution(ctx, job.ID, approval, false, nil, httperr.BadRequest(40073, "approval executor not found"), true)
	}
	if err := executor.Healthy(ctx); err != nil {
		_, _ = w.svc.Store.CompleteApprovalOperation(ctx, operation.ID, model.ApprovalOperationRunning, model.ApprovalOperationFailed, "{}", "approval executor unhealthy", now)
		_, _ = w.svc.Store.CompleteActingContext(ctx, claimedContext.ID, model.ActingContextClaimed, model.ActingContextFailed)
		return w.finishApprovalExecution(ctx, job.ID, approval, false, nil, fmt.Errorf("approval executor unhealthy: %w", err), true)
	}
	if err := w.svc.validateApprovalResource(ctx, approval); err != nil {
		_, _ = w.svc.Store.CompleteApprovalOperation(ctx, operation.ID, model.ApprovalOperationRunning, model.ApprovalOperationFailed, "{}", "approval resource revalidation failed", now)
		_, _ = w.svc.Store.CompleteActingContext(ctx, claimedContext.ID, model.ActingContextClaimed, model.ActingContextFailed)
		return w.finishApprovalExecution(ctx, job.ID, approval, false, nil, err, true)
	}
	if err := executor.Validate(ctx, approval); err != nil {
		_, _ = w.svc.Store.CompleteApprovalOperation(ctx, operation.ID, model.ApprovalOperationRunning, model.ApprovalOperationFailed, "{}", "approval validation failed", now)
		_, _ = w.svc.Store.CompleteActingContext(ctx, claimedContext.ID, model.ActingContextClaimed, model.ActingContextFailed)
		return w.finishApprovalExecution(ctx, job.ID, approval, false, nil, err, true)
	}
	result, execErr := executor.Execute(ctx, approval)
	if execErr != nil {
		_, operationErr := w.svc.Store.CompleteApprovalOperation(ctx, operation.ID, model.ApprovalOperationRunning, model.ApprovalOperationFailed, "{}", w.svc.sanitizeApprovalError(execErr), now)
		if operationErr != nil {
			return operationErr
		}
		if _, contextErr := w.svc.Store.CompleteActingContext(ctx, claimedContext.ID, model.ActingContextClaimed, model.ActingContextFailed); contextErr != nil {
			return contextErr
		}
	} else {
		resultJSON := "{}"
		if raw, marshalErr := json.Marshal(result); marshalErr == nil {
			resultJSON = string(raw)
		}
		_, operationErr := w.svc.Store.CompleteApprovalOperation(ctx, operation.ID, model.ApprovalOperationRunning, model.ApprovalOperationCompleted, resultJSON, "", now)
		if operationErr != nil {
			return operationErr
		}
	}
	settleErr := w.finishApprovalExecution(ctx, job.ID, approval, execErr == nil, result, execErr, true)
	if settleErr == nil && execErr == nil {
		if _, contextErr := w.svc.Store.CompleteActingContext(ctx, claimedContext.ID, model.ActingContextClaimed, model.ActingContextConsumed); contextErr != nil {
			return contextErr
		}
	}
	return settleErr
}

func (w *approvalExecuteWorker) resolveApprovalExecution(
	ctx context.Context, job *model.Job, approval *model.Approval, context *model.ActingContext,
) (*model.ApprovalOperation, bool, error) {
	now := time.Now().UTC()
	switch context.Status {
	case model.ActingContextActive:
		operation, proceed, err := w.claimApprovalOperation(ctx, approval, context, job.ID, now)
		if err != nil || !proceed {
			return operation, proceed, err
		}
		if operation.Status != model.ApprovalOperationRunning {
			return w.reconcileApprovalOperation(ctx, job, approval, context, operation)
		}
		return operation, true, nil
	case model.ActingContextExpired:
		if latest, err := w.svc.Store.GetLatestApprovalOperation(ctx, context.TargetTenantID, approval.ID); err != nil {
			return nil, false, err
		} else if latest != nil && (latest.Status == model.ApprovalOperationRunning || latest.Status == model.ApprovalOperationUnknown) {
			return nil, false, httperr.New(409, 40972, "prior approval operation outcome is unresolved")
		}
		retryContext, err := w.svc.prepareActingContext(ctx, approval)
		if err != nil {
			return nil, false, err
		}
		if retryContext.ApprovalActionHash != context.ApprovalActionHash {
			return nil, false, httperr.New(409, 40972, "approval action fingerprint changed during retry")
		}
		operation, proceed, err := w.claimApprovalOperation(ctx, approval, retryContext, job.ID, now)
		if err != nil || !proceed {
			return operation, proceed, err
		}
		if operation.Status != model.ApprovalOperationRunning {
			return w.reconcileApprovalOperation(ctx, job, approval, retryContext, operation)
		}
		return operation, true, nil
	case model.ActingContextFailed:
		operation, err := w.svc.Store.GetApprovalOperationByContext(ctx, context.ID)
		if err != nil {
			return nil, false, err
		}
		if operation != nil {
			if operation.Status == model.ApprovalOperationFailed && approval.Status == model.ApprovalStatusApproved {
				retryContext, retryErr := w.svc.prepareActingContext(ctx, approval)
				if retryErr != nil {
					return nil, false, retryErr
				}
				operation, proceed, retryErr := w.claimApprovalOperation(ctx, approval, retryContext, job.ID, now)
				if retryErr != nil || !proceed {
					return operation, proceed, retryErr
				}
				if operation.Status != model.ApprovalOperationRunning {
					return w.reconcileApprovalOperation(ctx, job, approval, retryContext, operation)
				}
				return operation, true, nil
			}
			return w.reconcileApprovalOperation(ctx, job, approval, context, operation)
		}
		if latest, latestErr := w.svc.Store.GetLatestApprovalOperation(ctx, context.TargetTenantID, approval.ID); latestErr != nil {
			return nil, false, latestErr
		} else if latest != nil && (latest.Status == model.ApprovalOperationRunning || latest.Status == model.ApprovalOperationUnknown) {
			return nil, false, httperr.New(409, 40972, "prior approval operation outcome is unresolved")
		}
		retryContext, err := w.svc.prepareActingContext(ctx, approval)
		if err != nil {
			return nil, false, err
		}
		operation, proceed, err := w.claimApprovalOperation(ctx, approval, retryContext, job.ID, now)
		if err != nil || !proceed {
			return operation, proceed, err
		}
		if operation.Status != model.ApprovalOperationRunning {
			return w.reconcileApprovalOperation(ctx, job, approval, retryContext, operation)
		}
		return operation, true, nil
	case model.ActingContextClaimed:
		operation, err := w.svc.Store.GetApprovalOperationByContext(ctx, context.ID)
		if err != nil {
			return nil, false, err
		}
		if operation != nil && (operation.Status == model.ApprovalOperationCompleted || operation.Status == model.ApprovalOperationFailed) {
			return w.reconcileApprovalOperation(ctx, job, approval, context, operation)
		}
		if context.ClaimExpiresAt != nil && context.ClaimExpiresAt.After(now) {
			return nil, false, httperr.New(409, 40972, "acting context is already claimed")
		}
		if operation == nil {
			_, _ = w.svc.Store.CompleteActingContext(ctx, context.ID, model.ActingContextClaimed, model.ActingContextFailed)
			expectedErr := errors.New("claimed acting context has no operation result; reconciliation required")
			if approval.Status == model.ApprovalStatusExecuting {
				if err := w.finishApprovalExecution(ctx, job.ID, approval, false, nil, expectedErr, true); err != nil && !errors.Is(err, expectedErr) {
					return nil, false, err
				}
			}
			return nil, false, nil
		}
		if operation.Status == model.ApprovalOperationRunning && now.Sub(operation.UpdatedAt) < approvalOperationPendingWindow {
			return nil, false, errApprovalOperationPending
		}
		if operation.Status == model.ApprovalOperationRunning {
			if _, err := w.svc.Store.MarkStaleApprovalOperationUnknown(ctx, operation.ID, now.Add(-approvalOperationPendingWindow), now); err != nil {
				return nil, false, err
			}
			operation.Status = model.ApprovalOperationUnknown
		}
		return w.reconcileApprovalOperation(ctx, job, approval, context, operation)
	case model.ActingContextConsumed:
		operation, err := w.svc.Store.GetApprovalOperationByContext(ctx, context.ID)
		if err != nil {
			return nil, false, err
		}
		if operation == nil {
			expectedErr := errors.New("consumed acting context has no operation result; reconciliation required")
			if approval.Status == model.ApprovalStatusExecuting {
				if err := w.finishApprovalExecution(ctx, job.ID, approval, false, nil, expectedErr, true); err != nil && !errors.Is(err, expectedErr) {
					return nil, false, err
				}
			}
			return nil, false, nil
		}
		return w.reconcileApprovalOperation(ctx, job, approval, context, operation)
	default:
		return nil, false, httperr.New(409, 40972, "acting context is not executable")
	}
}

func (w *approvalExecuteWorker) claimApprovalOperation(
	ctx context.Context, approval *model.Approval, context *model.ActingContext, jobID string, now time.Time,
) (*model.ApprovalOperation, bool, error) {
	operation, err := newApprovalOperation(approval, context, jobID, now, now.Add(actingContextClaimTTL))
	if err != nil {
		return nil, false, err
	}
	operation, claimed, err := w.svc.Store.ClaimActingContextForOperation(
		ctx, context.ID, jobID, now, now.Add(actingContextClaimTTL), operation,
	)
	if err != nil {
		return nil, false, err
	}
	if !claimed {
		return nil, false, httperr.New(409, 40972, "acting context is not claimable")
	}
	return operation, true, nil
}

func (w *approvalExecuteWorker) reconcileApprovalOperation(
	ctx context.Context, job *model.Job, approval *model.Approval, context *model.ActingContext, operation *model.ApprovalOperation,
) (*model.ApprovalOperation, bool, error) {
	fingerprint, err := approvalOperationFingerprint(approval, context)
	if err != nil {
		return nil, false, err
	}
	if operation.ApprovalID != approval.ID || operation.TenantID != context.TargetTenantID ||
		operation.ActingContextID != context.ID || operation.ApprovalActionHash != context.ApprovalActionHash ||
		operation.OperationFingerprint != fingerprint {
		return nil, false, httperr.New(409, 40972, "approval operation identity mismatch")
	}
	switch operation.Status {
	case model.ApprovalOperationCompleted:
		if approval.Status == model.ApprovalStatusExecuting {
			if err := w.finishApprovalExecution(ctx, job.ID, approval, true, decodeApprovalOperationResult(operation.ResultJSON), nil, true); err != nil {
				return nil, false, err
			}
			approval.Status = model.ApprovalStatusCompleted
		}
		if approval.Status == model.ApprovalStatusCompleted {
			if context.Status == model.ActingContextClaimed {
				if _, err := w.svc.Store.CompleteActingContext(ctx, context.ID, model.ActingContextClaimed, model.ActingContextConsumed); err != nil {
					return nil, false, err
				}
			}
			job.Result = operation.ResultJSON
			return nil, false, nil
		}
		return nil, false, httperr.New(409, 40972, "approval operation result conflicts with approval state")
	case model.ApprovalOperationFailed:
		expectedErr := errors.New(operation.LastError)
		if approval.Status == model.ApprovalStatusExecuting {
			if err := w.finishApprovalExecution(ctx, job.ID, approval, false, nil, expectedErr, true); err != nil && !errors.Is(err, expectedErr) {
				return nil, false, err
			}
			approval.Status = model.ApprovalStatusExecutionFailed
		}
		if approval.Status == model.ApprovalStatusExecutionFailed {
			if context.Status == model.ActingContextClaimed {
				if _, err := w.svc.Store.CompleteActingContext(ctx, context.ID, model.ActingContextClaimed, model.ActingContextFailed); err != nil {
					return nil, false, err
				}
			}
			return nil, false, nil
		}
		return nil, false, httperr.New(409, 40972, "approval operation result conflicts with approval state")
	case model.ApprovalOperationUnknown:
		expectedErr := errors.New("approval operation outcome is unknown; reconciliation required")
		if approval.Status == model.ApprovalStatusExecuting {
			if err := w.finishApprovalExecution(ctx, job.ID, approval, false, nil, expectedErr, true); err != nil && !errors.Is(err, expectedErr) {
				return nil, false, err
			}
			approval.Status = model.ApprovalStatusExecutionFailed
		}
		if approval.Status == model.ApprovalStatusExecutionFailed {
			if context.Status == model.ActingContextClaimed {
				if _, err := w.svc.Store.CompleteActingContext(ctx, context.ID, model.ActingContextClaimed, model.ActingContextFailed); err != nil {
					return nil, false, err
				}
			}
			return nil, false, nil
		}
		return nil, false, httperr.New(409, 40972, "approval operation result conflicts with approval state")
	default:
		return nil, false, errApprovalOperationPending
	}
}

func (w *approvalExecuteWorker) finishApprovalExecution(ctx context.Context, jobID string, approval *model.Approval, success bool, result map[string]any, execErr error, final bool) error {
	if !success && !final {
		return execErr
	}
	resultJSON := "{}"
	if result != nil {
		if raw, err := json.Marshal(result); err == nil {
			resultJSON = string(raw)
		}
	}
	lastError := ""
	if !success {
		lastError = w.svc.sanitizeApprovalError(execErr)
	}
	action := "approval.executed"
	if !success {
		action = "approval.execution_failed"
	}
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return err
	}
	// Fetch the retained credential once so a retry failure refreshes its
	// retention window instead of leaving it tied to the first failure.
	var credentialID string
	if credential, err := w.svc.Store.GetRetainedApprovalCredential(ctx, approval.TenantID, approval.ID); err != nil {
		return err
	} else if credential != nil {
		credentialID = credential.ID
	}
	actingContextID := ""
	if operation, opErr := w.svc.Store.GetLatestApprovalOperation(ctx, targetTenantID, approval.ID); opErr == nil && operation != nil {
		actingContextID = operation.ActingContextID
	}
	if success {
		obs.Get().IncApprovalExecution(approval.ObjectType, approval.Action, "success")
	}
	executionDetail := map[string]any{
		"job_id": jobID, "success": success, "object_type": approval.ObjectType, "action": approval.Action,
	}
	if result != nil {
		summary := map[string]any{}
		for _, key := range []string{"object_id", "dataset_id", "key_id", "provider_id", "instance_id"} {
			if value, ok := result[key]; ok {
				summary[key] = value
			}
		}
		executionDetail["result"] = summary
	}
	detailJSON, _ := json.Marshal(executionDetail)
	settleErr := w.svc.Store.SettleApprovalExecution(ctx, approval.TenantID, approval.ID, success, resultJSON, lastError, credentialID, time.Now().UTC().Add(24*time.Hour), &model.AuditLog{
		TenantID: approval.TenantID, UserID: "system", Action: action, Resource: "approval",
		ResourceID: approval.ID, DetailJSON: string(detailJSON),
		ActorTenantID: model.PlatformTenantID, TargetTenantID: targetTenantID,
		ApprovalID: approval.ID, ActingContextID: actingContextID, ApprovalActionHash: approval.ApprovalActionHash,
		Result:                map[bool]string{true: "SUCCESS", false: "FAILED"}[success],
		AuthorizationDecision: "ALLOW", AuthorizationPermission: "system:approval-executor",
		AuthorizationPolicyVersion: "explicit-rbac-v1",
	})
	if settleErr != nil {
		if execErr != nil {
			return fmt.Errorf("%v; settle failed: %w", execErr, settleErr)
		}
		return settleErr
	}
	if !success {
		obs.Get().IncApprovalExecution(approval.ObjectType, approval.Action, "failed")
	}
	return execErr
}

func (s *Service) SetupApprovalWorker(runner *Runner) {
	if runner == nil {
		return
	}
	runner.Register(&approvalExecuteWorker{svc: s})
}
