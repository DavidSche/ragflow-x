package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ModelInfoInput is a single model a caller wants on an instance.
type ModelInfoInput struct {
	ModelName  string   `json:"model_name"`
	ModelTypes []string `json:"model_type"`
	MaxTokens  int      `json:"max_tokens"`
	IsTools    bool     `json:"is_tools"`
	Thinking   bool     `json:"thinking"`
}

// CreateProviderInstanceRequest carries instance creation/update parameters.
type CreateProviderInstanceRequest struct {
	InstanceName string           `json:"instance_name"`
	APIKey       string           `json:"api_key"`
	BaseURL      string           `json:"base_url"`
	Region       string           `json:"region"`
	Models       []ModelInfoInput `json:"model_info"`
}

// ProviderView is the tenant-facing provider summary for the management UI.
type ProviderView struct {
	ID            string   `json:"id"`
	TenantID      string   `json:"tenant_id,omitempty"`
	Name          string   `json:"name"`
	TenantName    string   `json:"tenant_name,omitempty"`
	ProviderType  string   `json:"provider_type"`
	Status        string   `json:"status"`
	Enabled       bool     `json:"enabled"`
	BaseURL       string   `json:"base_url"`
	HasInstance   bool     `json:"has_instance"`
	InstanceCount int      `json:"instance_count"`
	ModelCount    int      `json:"model_count"`
	ModelTypes    []string `json:"model_types"`
}

// CatalogProvider is an entry from the RAGFlow provider catalog.
type CatalogProvider struct {
	Name       string   `json:"name"`
	DefaultURL string   `json:"default_url"`
	IntlURL    string   `json:"intl_url,omitempty"`
	ModelTypes []string `json:"model_types"`
}

func (r *CreateProviderInstanceRequest) toModelInfo() []ragflow.ModelInfo {
	out := make([]ragflow.ModelInfo, 0, len(r.Models))
	for _, m := range r.Models {
		types := m.ModelTypes
		if len(types) == 0 {
			types = []string{"chat"}
		}
		out = append(out, ragflow.ModelInfo{
			ModelType: types,
			ModelName: m.ModelName,
			MaxTokens: m.MaxTokens,
			Extra: map[string]interface{}{
				"is_tools":   m.IsTools,
				"thinking":   m.Thinking,
				"max_tokens": m.MaxTokens,
			},
		})
	}
	return out
}

// modelTestTimeout bounds an outbound chat test so a slow backend never blocks a
// handler goroutine indefinitely (large-model inference routinely takes minutes).
const modelTestTimeout = 60 * time.Second

// CreateModelProviderRequest carries a unified LLM provider (legacy quick add).
type CreateModelProviderRequest struct {
	ProviderName   string `json:"provider_name"`
	ProviderType   string `json:"provider_type"`
	Name           string `json:"name"`
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	ModelsJSON     string `json:"models_json"`
	Enabled        bool   `json:"enabled"`
	ModelName      string `json:"model_name"`
	MaxTokens      int    `json:"max_tokens"`
	RagflowFactory string `json:"ragflow_factory"`
	Register       *bool  `json:"register"`
}

// ListAvailableProviders returns the RAGFlow provider catalog.
func (s *Service) ListAvailableProviders(ctx context.Context) ([]CatalogProvider, error) {
	infos, err := s.RAGFlow.ListProviders(ctx, true)
	if err != nil {
		return nil, err
	}
	out := make([]CatalogProvider, 0, len(infos))
	for _, info := range infos {
		out = append(out, CatalogProvider{
			Name:       info.Name,
			DefaultURL: info.URL.Default,
			IntlURL:    info.URL.Intl,
			ModelTypes: info.ModelTypes,
		})
	}
	return out, nil
}

// ListProviderGovernance returns read-only provider summaries for the
// authorized tenant scope. Credentials never leave this function.
func (s *Service) ListProviderGovernance(ctx context.Context, scope TenantScope, name string) ([]ProviderView, error) {
	filter := repository.ModelProviderFilter{Name: name}
	if scope.Kind == TenantScopeSpecific {
		filter.TenantID = scope.TargetTenantID
	}
	providers, err := s.Store.ListModelProvidersForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), filter)
	if err != nil {
		return nil, err
	}
	instanceCounts, modelCounts, err := s.Store.CountModelProviderResourcesForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs())
	if err != nil {
		return nil, err
	}
	tenantNames, err := s.tenantNames(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ProviderView, 0, len(providers))
	for i := range providers {
		p := &providers[i]
		view := ProviderView{
			ID: p.ID, TenantID: p.TenantID, Name: p.Name, TenantName: tenantNames[p.TenantID],
			ProviderType: p.ProviderType, Status: p.Status, Enabled: p.Enabled, BaseURL: p.BaseURL,
			InstanceCount: int(instanceCounts[p.ID].Count), ModelCount: int(modelCounts[p.ID].Count),
		}
		view.HasInstance = view.InstanceCount > 0
		out = append(out, view)
	}
	return out, nil
}

func (s *Service) tenantNames(ctx context.Context) (map[string]string, error) {
	tenants, err := s.Store.ListAllTenants(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(tenants))
	for _, tenant := range tenants {
		names[tenant.ID] = tenant.Name
	}
	return names, nil
}

// AddModelProvider registers a factory with RAGFlow and mirrors it locally.
func (s *Service) AddModelProvider(ctx context.Context, tenantID, providerName string) (*model.ModelProvider, error) {
	if strings.TrimSpace(providerName) == "" {
		return nil, httperr.BadRequest(40030, "provider_name is required")
	}
	if err := s.RAGFlow.AddProvider(ctx, providerName); err != nil {
		return nil, httperr.New(502, 50204, "register provider to ragflow failed")
	}
	p := &model.ModelProvider{
		ID:           id.New(),
		TenantID:     tenantID,
		ProviderType: providerName,
		Name:         providerName,
		Enabled:      true,
		Status:       model.ProviderStatusActive,
	}
	if err := s.Store.CreateModelProvider(ctx, p); err != nil {
		rollbackExternalProviderCreation(ctx, s, tenantID, p.ID, p.ProviderType, "add-model-provider")
		return nil, err
	}
	return p, nil
}

// ListProviderConfigs lists a tenant's providers with instance/model counts.
func (s *Service) ListProviderConfigs(ctx context.Context, tenantID string) ([]ProviderView, error) {
	providers, err := s.Store.ListModelProviders(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	instances, err := s.Store.ListModelProviderInstancesByTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(instances))
	for _, inst := range instances {
		ids = append(ids, inst.ID)
	}
	// Load every model for this tenant's instances in a single batched query so
	// the list view stays at a constant query count regardless of how many
	// providers/instances a tenant has.
	models, err := s.Store.ListModelProviderModelsByInstanceIDs(ctx, tenantID, ids)
	if err != nil {
		return nil, err
	}
	instByProvider := map[string][]model.ModelProviderInstance{}
	for _, inst := range instances {
		instByProvider[inst.ProviderID] = append(instByProvider[inst.ProviderID], inst)
	}
	modelsByInstance := map[string][]model.ModelProviderModel{}
	for _, m := range models {
		modelsByInstance[m.InstanceID] = append(modelsByInstance[m.InstanceID], m)
	}
	out := make([]ProviderView, 0, len(providers))
	for i := range providers {
		p := &providers[i]
		pInstances := instByProvider[p.ID]
		view := ProviderView{
			ID:            p.ID,
			Name:          p.Name,
			ProviderType:  p.ProviderType,
			Status:        p.Status,
			Enabled:       p.Enabled,
			BaseURL:       p.BaseURL,
			HasInstance:   len(pInstances) > 0,
			InstanceCount: len(pInstances),
		}
		for _, inst := range pInstances {
			instModels := modelsByInstance[inst.ID]
			view.ModelCount += len(instModels)
			for _, m := range instModels {
				for _, t := range model.ModelTypeLabels(m.ModelType) {
					if !containsString(view.ModelTypes, t) {
						view.ModelTypes = append(view.ModelTypes, t)
					}
				}
			}
		}
		out = append(out, view)
	}
	return out, nil
}

// GetProviderConfig returns a single provider's summary for the detail view.
func (s *Service) GetProviderConfig(ctx context.Context, tenantID, providerID string) (*ProviderView, error) {
	p, err := s.Store.GetModelProvider(ctx, tenantID, providerID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, httperr.NotFound("model provider not found")
	}
	instances, err := s.Store.ListModelProviderInstances(ctx, tenantID, providerID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(instances))
	for _, inst := range instances {
		ids = append(ids, inst.ID)
	}
	models, err := s.Store.ListModelProviderModelsByInstanceIDs(ctx, tenantID, ids)
	if err != nil {
		return nil, err
	}
	modelByInstance := map[string][]model.ModelProviderModel{}
	for _, m := range models {
		modelByInstance[m.InstanceID] = append(modelByInstance[m.InstanceID], m)
	}
	view := &ProviderView{
		ID: p.ID, Name: p.Name, ProviderType: p.ProviderType, Status: p.Status, Enabled: p.Enabled, BaseURL: p.BaseURL,
		HasInstance: len(instances) > 0, InstanceCount: len(instances),
	}
	for _, inst := range instances {
		for _, m := range modelByInstance[inst.ID] {
			view.ModelCount++
			for _, t := range model.ModelTypeLabels(m.ModelType) {
				if !containsString(view.ModelTypes, t) {
					view.ModelTypes = append(view.ModelTypes, t)
				}
			}
		}
	}
	return view, nil
}

// CreateModelProvider persists a provider, encrypting its credential at rest
// (legacy quick-add that also registers a default instance + one model).
func (s *Service) CreateModelProvider(ctx context.Context, tenantID string, req CreateModelProviderRequest) (*model.ModelProvider, error) {
	if req.Name == "" || req.ProviderType == "" {
		return nil, httperr.BadRequest(40030, "provider name and type are required")
	}
	if err := ValidateProviderBaseURL(req.BaseURL, s.allowPrivateProviderBaseURL); err != nil {
		return nil, err
	}
	enc, err := crypto.Encrypt(s.EncryptKey, req.APIKey)
	if err != nil {
		return nil, httperr.Internal("failed to encrypt provider credential")
	}
	p := &model.ModelProvider{
		ID:           id.New(),
		TenantID:     tenantID,
		ProviderType: req.ProviderType,
		Name:         req.Name,
		BaseURL:      req.BaseURL,
		APIKeyEnc:    enc,
		ModelsJSON:   req.ModelsJSON,
		Enabled:      req.Enabled,
		Status:       model.ProviderStatusActive,
	}
	modelName := req.ModelName
	provModel := &model.ModelProviderModel{
		ID:         id.New(),
		TenantID:   tenantID,
		ProviderID: p.ID,
		ModelName:  modelName,
		ModelType:  model.ModelTypeChat,
		Status:     model.ModelStatusActive,
		MaxTokens:  req.MaxTokens,
		IsTools:    true,
		Verify:     model.ModelVerifyUnknown,
	}
	provInstance := &model.ModelProviderInstance{
		ID:           id.New(),
		TenantID:     tenantID,
		ProviderID:   p.ID,
		InstanceName: "default",
		APIKeyEnc:    enc,
		BaseURL:      req.BaseURL,
		Status:       model.ProviderStatusActive,
		ExtraJSON:    "{}",
	}
	if shouldRegister := req.Register != nil && *req.Register || (req.Register == nil && s.RegisterLLM); shouldRegister && modelName != "" {
		factory := req.RagflowFactory
		if factory == "" {
			factory = defaultRAGFlowFactory(req.ProviderType)
		}
		p.ProviderType = factory
		regReq := ragflow.RegisterModelProviderRequest{
			FactoryName:  factory,
			InstanceName: provInstance.InstanceName,
			APIKey:       req.APIKey,
			BaseURL:      req.BaseURL,
			ModelName:    modelName,
			MaxTokens:    req.MaxTokens,
			ModelTypes:   []string{"chat"},
			IsTools:      true,
		}
		if err := s.RAGFlow.UpsertModelProvider(ctx, regReq); err != nil {
			return nil, httperr.New(502, 50204, "register provider to ragflow failed")
		}
	}
	provModel.InstanceID = provInstance.ID
	if err := s.Store.CreateModelProvider(ctx, p); err != nil {
		rollbackExternalProviderCreation(ctx, s, tenantID, p.ID, p.ProviderType, "create-model-provider")
		return nil, err
	}
	if modelName != "" {
		if err := s.Store.CreateModelProviderInstance(ctx, provInstance); err != nil {
			_ = s.Store.DeleteModelProvider(ctx, tenantID, p.ID)
			rollbackExternalProviderCreation(ctx, s, tenantID, p.ID, p.ProviderType, "create-model-provider")
			return nil, err
		}
		if err := s.Store.CreateModelProviderModel(ctx, provModel); err != nil {
			_ = s.Store.DeleteModelProvider(ctx, tenantID, p.ID)
			rollbackExternalProviderCreation(ctx, s, tenantID, p.ID, p.ProviderType, "create-model-provider")
			return nil, err
		}
	}
	return p, nil
}

// ListModelProviders returns a tenant's providers.
func (s *Service) ListModelProviders(ctx context.Context, tenantID string) ([]model.ModelProvider, error) {
	return s.Store.ListModelProviders(ctx, tenantID)
}

// DeleteModelProvider removes a provider locally (cascade) and best-effort from
// RAGFlow, so a failed engine sync never blocks the operator.
func (s *Service) DeleteModelProvider(ctx context.Context, tenantID, id string) error {
	p, err := s.Store.GetModelProvider(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if p == nil {
		return httperr.NotFound("model provider not found")
	}
	if err := s.Store.DeleteModelProvider(ctx, tenantID, id); err != nil {
		return err
	}
	if deleteErr := s.RAGFlow.DeleteProvider(ctx, p.ProviderType); deleteErr != nil {
		logger.Warn("failed to delete external provider after local deletion",
			"provider_type", p.ProviderType, "error", deleteErr)
		recordProviderCompensationFailure(ctx, s, tenantID, id, "delete-model-provider", "delete-upstream-provider", deleteErr)
	}
	return nil
}

// ListProviderInstances returns a provider's instances.
func (s *Service) ListProviderInstances(ctx context.Context, tenantID, providerID string) ([]model.ModelProviderInstance, error) {
	return s.Store.ListModelProviderInstances(ctx, tenantID, providerID)
}

// CreateProviderInstance creates an instance + models in RAGFlow first, then
// mirrors them locally. The first instance becomes the provider's gateway
// default so chat routing keeps working.
func (s *Service) CreateProviderInstance(ctx context.Context, tenantID, providerID string, req CreateProviderInstanceRequest) (*model.ModelProviderInstance, error) {
	if strings.TrimSpace(req.InstanceName) == "" {
		return nil, httperr.BadRequest(40040, "instance_name is required")
	}
	if err := ValidateProviderBaseURL(req.BaseURL, s.allowPrivateProviderBaseURL); err != nil {
		return nil, err
	}
	p, err := s.Store.GetModelProvider(ctx, tenantID, providerID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, httperr.NotFound("model provider not found")
	}
	existing, err := s.Store.ListModelProviderInstances(ctx, tenantID, providerID)
	if err != nil {
		return nil, err
	}
	if findProviderInstance(existing, req.InstanceName) != nil {
		return nil, httperr.New(409, 40940, "provider instance name already exists")
	}
	enc, err := crypto.Encrypt(s.EncryptKey, req.APIKey)
	if err != nil {
		return nil, httperr.Internal("failed to encrypt provider credential")
	}
	models := req.Models
	if models == nil {
		models = []ModelInfoInput{}
	}
	factory, ferr := s.resolveRAGFlowFactory(ctx, p.ProviderType)
	if ferr != nil {
		return nil, ferr
	}
	regReq := ragflow.RegisterModelProviderRequest{
		FactoryName:  factory,
		InstanceName: req.InstanceName,
		APIKey:       req.APIKey,
		BaseURL:      req.BaseURL,
		Region:       req.Region,
		Models:       (&req).toModelInfo(),
	}
	remote, err := s.RAGFlow.CreateProviderInstance(ctx, regReq)
	if err != nil {
		return nil, httperr.New(502, 50205, "create provider instance in ragflow failed")
	}
	inst := &model.ModelProviderInstance{
		ID:           id.New(),
		TenantID:     tenantID,
		ProviderID:   providerID,
		InstanceName: req.InstanceName,
		APIKeyEnc:    enc,
		BaseURL:      req.BaseURL,
		Region:       req.Region,
		Status:       model.ProviderStatusActive,
		ExtraJSON:    "{}",
	}
	modelRows := make([]*model.ModelProviderModel, 0, len(models))
	for _, mi := range models {
		mod := &model.ModelProviderModel{
			ID:         id.New(),
			TenantID:   tenantID,
			ProviderID: providerID,
			InstanceID: inst.ID,
			ModelName:  mi.ModelName,
			ModelType:  model.ModelTypeMask(mi.ModelTypes),
			Status:     model.ModelStatusActive,
			MaxTokens:  mi.MaxTokens,
			IsTools:    mi.IsTools,
			Thinking:   mi.Thinking,
			Verify:     model.ModelVerifyUnknown,
			ExtraJSON:  "{}",
		}
		if mod.ModelType == 0 {
			mod.ModelType = model.ModelTypeChat
		}
		if mod.MaxTokens <= 0 {
			mod.MaxTokens = 8192
		}
		modelRows = append(modelRows, mod)
	}
	if err := s.Store.CreateModelProviderInstanceWithModels(ctx, inst, modelRows); err != nil {
		if deleteErr := s.RAGFlow.DeleteProviderInstances(ctx, factory, remoteInstanceIdentifiers(remote)); deleteErr != nil {
			logger.Warn("failed to roll back external provider instance after local persistence failure",
				"provider", factory, "instance_id", remote.ID, "instance_name", remote.InstanceName, "error", deleteErr)
			recordProviderCompensationFailure(ctx, s, tenantID, providerID, "create-provider-instance", "delete-upstream-instance", deleteErr)
		}
		return nil, err
	}
	return inst, nil
}

// UpdateProviderInstance updates an instance and its models in RAGFlow, then
// reconciles the local rows.
func (s *Service) UpdateProviderInstance(ctx context.Context, tenantID, providerID, instanceID string, req CreateProviderInstanceRequest) (*model.ModelProviderInstance, error) {
	if err := ValidateProviderBaseURL(req.BaseURL, s.allowPrivateProviderBaseURL); err != nil {
		return nil, err
	}
	p, err := s.Store.GetModelProvider(ctx, tenantID, providerID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, httperr.NotFound("model provider not found")
	}
	inst, err := s.Store.GetModelProviderInstance(ctx, tenantID, providerID, instanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, httperr.NotFound("provider instance not found")
	}
	apiKey := req.APIKey
	if apiKey == "" {
		if inst.APIKeyEnc == "" {
			return nil, httperr.BadRequest(40040, "api_key is required")
		}
		plain, err := crypto.Decrypt(s.EncryptKey, inst.APIKeyEnc)
		if err != nil {
			return nil, httperr.Internal("failed to decrypt provider credential")
		}
		apiKey = plain
	}
	enc, err := crypto.Encrypt(s.EncryptKey, apiKey)
	if err != nil {
		return nil, httperr.Internal("failed to encrypt provider credential")
	}
	if inst.InstanceName == "" {
		inst.InstanceName = req.InstanceName
	}
	existingModels, err := s.Store.ListModelProviderModels(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	if req.InstanceName != "" && req.InstanceName != inst.InstanceName {
		existing, err := s.Store.ListModelProviderInstances(ctx, tenantID, providerID)
		if err != nil {
			return nil, err
		}
		if findProviderInstance(existing, req.InstanceName) != nil {
			return nil, httperr.New(409, 40940, "provider instance name already exists")
		}
	}
	factory, ferr := s.resolveRAGFlowFactory(ctx, p.ProviderType)
	if ferr != nil {
		return nil, ferr
	}
	regReq := ragflow.RegisterModelProviderRequest{
		FactoryName:  factory,
		InstanceName: inst.InstanceName,
		APIKey:       apiKey,
		BaseURL:      req.BaseURL,
		Region:       req.Region,
		Models:       (&req).toModelInfo(),
	}
	if err := s.RAGFlow.UpdateProviderInstance(ctx, factory, inst.InstanceName, regReq); err != nil {
		return nil, httperr.New(502, 50206, "update provider instance in ragflow failed")
	}
	previous := *inst
	previousModels := make([]model.ModelProviderModel, len(existingModels))
	copy(previousModels, existingModels)
	inst.APIKeyEnc = enc
	inst.BaseURL = req.BaseURL
	inst.Region = req.Region
	if req.InstanceName != "" {
		inst.InstanceName = req.InstanceName
	}
	submitted := map[string]ModelInfoInput{}
	for _, mi := range req.Models {
		submitted[mi.ModelName] = mi
	}
	changedModels := make([]*model.ModelProviderModel, 0, len(submitted))
	removed := make([]string, 0, len(existingModels))
	for _, currentModel := range existingModels {
		if _, ok := submitted[currentModel.ModelName]; !ok {
			removed = append(removed, currentModel.ID)
		}
	}
	for name, mi := range submitted {
		currentModel := findProviderModel(existingModels, name)
		if currentModel == nil {
			mod := &model.ModelProviderModel{
				ID:         id.New(),
				TenantID:   tenantID,
				ProviderID: providerID,
				InstanceID: instanceID,
				ModelName:  name,
				ModelType:  model.ModelTypeMask(mi.ModelTypes),
				Status:     model.ModelStatusActive,
				MaxTokens:  mi.MaxTokens,
				IsTools:    mi.IsTools,
				Thinking:   mi.Thinking,
				Verify:     model.ModelVerifyUnknown,
				ExtraJSON:  "{}",
			}
			if mod.ModelType == 0 {
				mod.ModelType = model.ModelTypeChat
			}
			if mod.MaxTokens <= 0 {
				mod.MaxTokens = 8192
			}
			changedModels = append(changedModels, mod)
		} else {
			mod := *currentModel
			mod.ModelType = model.ModelTypeMask(mi.ModelTypes)
			mod.MaxTokens = mi.MaxTokens
			mod.IsTools = mi.IsTools
			mod.Thinking = mi.Thinking
			if mod.ModelType == 0 {
				mod.ModelType = model.ModelTypeChat
			}
			changedModels = append(changedModels, &mod)
		}
	}
	var providerSnapshot *model.ModelProvider
	all, err := s.Store.ListModelProviderInstances(ctx, tenantID, providerID)
	if err != nil {
		if rollbackErr := rollbackProviderInstanceUpdate(ctx, s, factory, &previous, previousModels); rollbackErr != nil {
			recordProviderCompensationFailure(ctx, s, tenantID, providerID, "update-provider-instance", "restore-upstream-instance", rollbackErr)
		}
		return nil, err
	}
	if len(all) == 1 {
		providerSnapshot = &model.ModelProvider{ID: p.ID, TenantID: p.TenantID, BaseURL: req.BaseURL, APIKeyEnc: enc}
	}
	if err := s.Store.UpdateModelProviderInstanceWithModels(ctx, providerSnapshot, inst, changedModels, removed); err != nil {
		if rollbackErr := rollbackProviderInstanceUpdate(ctx, s, factory, &previous, previousModels); rollbackErr != nil {
			recordProviderCompensationFailure(ctx, s, tenantID, providerID, "update-provider-instance", "restore-upstream-instance", rollbackErr)
		}
		return nil, err
	}
	return inst, nil
}

// DeleteProviderInstances atomically removes the local shadow first, then the
// upstream instances. If upstream deletion fails, the shadow is restored from
// the pre-delete snapshot before returning the original error.
func (s *Service) DeleteProviderInstances(ctx context.Context, tenantID, providerID string, ids []string) error {
	p, err := s.Store.GetModelProvider(ctx, tenantID, providerID)
	if err != nil {
		return err
	}
	if p == nil {
		return httperr.NotFound("model provider not found")
	}
	instances, err := s.Store.ListModelProviderInstances(ctx, tenantID, providerID)
	if err != nil {
		return err
	}
	selected := make([]model.ModelProviderInstance, 0, len(ids))
	for _, instance := range instances {
		for _, requestedID := range ids {
			if instance.ID == requestedID {
				selected = append(selected, instance)
				break
			}
		}
	}
	if len(selected) == 0 {
		return nil
	}
	models := make([]*model.ModelProviderModel, 0)
	for _, instance := range selected {
		instanceModels, err := s.Store.ListModelProviderModels(ctx, tenantID, instance.ID)
		if err != nil {
			return err
		}
		for index := range instanceModels {
			models = append(models, &instanceModels[index])
		}
	}
	selectedIDs := make([]string, 0, len(selected))
	for _, instance := range selected {
		selectedIDs = append(selectedIDs, instance.ID)
	}
	providerSnapshot := *p
	if err := s.Store.DeleteModelProviderInstancesWithModels(ctx, p, selectedIDs); err != nil {
		return err
	}

	factory, ferr := s.resolveRAGFlowFactory(ctx, p.ProviderType)
	if ferr != nil {
		return ferr
	}
	remoteIDsByLocalID, err := resolveUpstreamInstanceIDsByLocalID(ctx, s.RAGFlow, factory, selected)
	if err != nil {
		if restoreErr := s.Store.RestoreModelProviderInstancesWithModels(ctx, &providerSnapshot, toInstancePointers(selected), models); restoreErr != nil {
			recordProviderCompensationFailure(ctx, s, tenantID, providerID, "delete-provider-instance", "restore-local-shadow", restoreErr)
			return restoreErr
		}
		return err
	}
	modelsByInstance := make(map[string][]*model.ModelProviderModel)
	for _, providerModel := range models {
		modelsByInstance[providerModel.InstanceID] = append(modelsByInstance[providerModel.InstanceID], providerModel)
	}
	deleteFailures := make([]error, 0)
	for index := range selected {
		remoteID := remoteIDsByLocalID[selected[index].ID]
		if remoteID == "" {
			continue
		}
		if deleteErr := s.RAGFlow.DeleteProviderInstances(ctx, factory, []string{remoteID}); deleteErr != nil {
			instanceModels := modelsByInstance[selected[index].ID]
			restoreErr := s.Store.RestoreModelProviderInstancesWithModels(
				ctx, &providerSnapshot, []*model.ModelProviderInstance{&selected[index]}, instanceModels,
			)
			if restoreErr != nil {
				recordProviderCompensationFailure(ctx, s, tenantID, providerID, "delete-provider-instance", "restore-local-shadow", restoreErr)
				deleteFailures = append(deleteFailures, restoreErr)
				continue
			}
			deleteFailures = append(deleteFailures, deleteErr)
		}
	}
	return errors.Join(deleteFailures...)
}

func resolveUpstreamInstanceIDsByLocalID(ctx context.Context, client ragflow.Client, factory string, instances []model.ModelProviderInstance) (map[string]string, error) {
	remoteInstances, err := client.ListProviderInstances(ctx, factory)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]string, len(instances))
	for _, instance := range instances {
		for _, remoteInstance := range remoteInstances {
			if strings.EqualFold(strings.TrimSpace(remoteInstance.InstanceName), strings.TrimSpace(instance.InstanceName)) {
				ids[instance.ID] = remoteInstance.ID
				break
			}
		}
	}
	return ids, nil
}

func toInstancePointers(instances []model.ModelProviderInstance) []*model.ModelProviderInstance {
	pointers := make([]*model.ModelProviderInstance, 0, len(instances))
	for index := range instances {
		pointers = append(pointers, &instances[index])
	}
	return pointers
}

// ListProviderInstanceModels lists models on an instance.
func (s *Service) ListProviderInstanceModels(ctx context.Context, tenantID, instanceID string) ([]model.ModelProviderModel, error) {
	return s.Store.ListModelProviderModels(ctx, tenantID, instanceID)
}

// AddProviderModel adds a model to an instance in RAGFlow, then mirrors it.
func (s *Service) AddProviderModel(ctx context.Context, tenantID, providerID, instanceID string, input ModelInfoInput) (*model.ModelProviderModel, error) {
	if strings.TrimSpace(input.ModelName) == "" {
		return nil, httperr.BadRequest(40041, "model_name is required")
	}
	input.ModelName = strings.TrimSpace(input.ModelName)
	p, err := s.Store.GetModelProvider(ctx, tenantID, providerID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, httperr.NotFound("model provider not found")
	}
	existing, err := s.Store.ListModelProviderModels(ctx, tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	if findProviderModel(existing, input.ModelName) != nil {
		return nil, httperr.New(409, 40941, "model name already exists on provider instance")
	}
	inst, err := s.Store.GetModelProviderInstance(ctx, tenantID, providerID, instanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, httperr.NotFound("provider instance not found")
	}
	factory, ferr := s.resolveRAGFlowFactory(ctx, p.ProviderType)
	if ferr != nil {
		return nil, ferr
	}
	if err := s.RAGFlow.AddModelToInstance(ctx, factory, inst.InstanceName, ragflow.ModelInfo{
		ModelType: defaultTypes(input.ModelTypes),
		ModelName: input.ModelName,
		MaxTokens: input.MaxTokens,
		Extra:     modelInfoExtra(input.IsTools, input.Thinking),
	}); err != nil {
		return nil, httperr.New(502, 50207, "add model in ragflow failed")
	}
	mod := &model.ModelProviderModel{
		ID:         id.New(),
		TenantID:   tenantID,
		ProviderID: providerID,
		InstanceID: instanceID,
		ModelName:  input.ModelName,
		ModelType:  maskOrDefault(input.ModelTypes),
		Status:     model.ModelStatusActive,
		MaxTokens:  defaultMaxTokens(input.MaxTokens),
		IsTools:    input.IsTools,
		Thinking:   input.Thinking,
		Verify:     model.ModelVerifyUnknown,
		ExtraJSON:  "{}",
	}
	if err := s.Store.CreateModelProviderModel(ctx, mod); err != nil {
		if deleteErr := s.RAGFlow.DeleteModelsFromInstance(ctx, factory, inst.InstanceName, []string{input.ModelName}); deleteErr != nil {
			recordProviderCompensationFailure(ctx, s, tenantID, providerID, "add-provider-model", "delete-upstream-model", deleteErr)
		}
		return nil, err
	}
	return mod, nil
}

// UpdateProviderModel patches a model on an instance (RAGFlow + local).
func (s *Service) UpdateProviderModel(ctx context.Context, tenantID, providerID, instanceID, modelID string, req ModelUpdateDTO) (*model.ModelProviderModel, error) {
	p, err := s.Store.GetModelProvider(ctx, tenantID, providerID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, httperr.NotFound("model provider not found")
	}
	inst, err := s.Store.GetModelProviderInstance(ctx, tenantID, providerID, instanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, httperr.NotFound("provider instance not found")
	}
	mod, err := s.Store.GetModelProviderModel(ctx, tenantID, instanceID, modelID)
	if err != nil {
		return nil, err
	}
	if mod == nil {
		return nil, httperr.NotFound("model not found")
	}
	update := ragflow.ModelUpdate{
		Status:    req.Status,
		MaxTokens: req.MaxTokens,
		ModelType: req.ModelType,
		Extra:     req.Extra,
	}
	previousModel := *mod
	factory, ferr := s.resolveRAGFlowFactory(ctx, p.ProviderType)
	if ferr != nil {
		return nil, ferr
	}
	if err := s.RAGFlow.UpdateModel(ctx, factory, inst.InstanceName, mod.ModelName, update); err != nil {
		return nil, httperr.New(502, 50208, "update model in ragflow failed")
	}
	if req.Status != "" {
		mod.Status = req.Status
	}
	if req.MaxTokens > 0 {
		mod.MaxTokens = req.MaxTokens
	}
	if req.ModelType != nil {
		mod.ModelType = maskOrDefault(req.ModelType)
	}
	if req.Extra != nil {
		if b, err := json.Marshal(req.Extra); err == nil {
			mod.ExtraJSON = string(b)
		}
		if v, ok := req.Extra["is_tools"].(bool); ok {
			mod.IsTools = v
		}
		if v, ok := req.Extra["thinking"].(bool); ok {
			mod.Thinking = v
		}
	}
	if err := s.Store.UpdateModelProviderModel(ctx, mod); err != nil {
		if rollbackErr := rollbackProviderModelUpdate(ctx, s, factory, inst, &previousModel); rollbackErr != nil {
			recordProviderCompensationFailure(ctx, s, tenantID, providerID, "update-provider-model", "restore-upstream-model", rollbackErr)
		}
		return nil, err
	}
	return mod, nil
}

// DeleteProviderModels removes models upstream first, then from the shadow.
func (s *Service) DeleteProviderModels(ctx context.Context, tenantID, providerID, instanceID string, ids []string) error {
	p, err := s.Store.GetModelProvider(ctx, tenantID, providerID)
	if err != nil {
		return err
	}
	if p == nil {
		return httperr.NotFound("model provider not found")
	}
	inst, err := s.Store.GetModelProviderInstance(ctx, tenantID, providerID, instanceID)
	if err != nil {
		return err
	}
	if inst == nil {
		return httperr.NotFound("provider instance not found")
	}
	models, err := s.Store.ListModelProviderModels(ctx, tenantID, instanceID)
	if err != nil {
		return err
	}
	var names []string
	for _, m := range models {
		for _, id := range ids {
			if id == m.ID {
				names = append(names, m.ModelName)
			}
		}
	}
	factory, ferr := s.resolveRAGFlowFactory(ctx, p.ProviderType)
	if ferr != nil {
		return ferr
	}
	if err := s.RAGFlow.DeleteModelsFromInstance(ctx, factory, inst.InstanceName, names); err != nil {
		return httperr.New(502, 50209, "delete models in ragflow failed")
	}
	if err := s.Store.DeleteModelProviderModels(ctx, tenantID, instanceID, ids); err != nil {
		recordProviderCompensationFailure(ctx, s, tenantID, providerID, "delete-provider-model", "delete-local-shadow", err)
		return err
	}
	return nil
}

// VerifyProviderConnection validates an API key/base URL and returns per-model
// verify status as reported by RAGFlow.
func (s *Service) VerifyProviderConnection(ctx context.Context, tenantID, providerID string, req CreateProviderInstanceRequest) (map[string]string, error) {
	p, err := s.Store.GetModelProvider(ctx, tenantID, providerID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, httperr.NotFound("model provider not found")
	}
	info := make([]ragflow.ModelInfo, 0, len(req.Models))
	for _, m := range req.Models {
		info = append(info, ragflow.ModelInfo{
			ModelType: defaultTypes(m.ModelTypes),
			ModelName: m.ModelName,
			MaxTokens: m.MaxTokens,
		})
	}
	factory, ferr := s.resolveRAGFlowFactory(ctx, p.ProviderType)
	if ferr != nil {
		return nil, ferr
	}
	return s.RAGFlow.VerifyConnection(ctx, factory, req.APIKey, req.BaseURL, req.Region, info)
}

// TestProviderModel sends a chat test message to a configured model.
func (s *Service) TestProviderModel(ctx context.Context, tenantID, providerID, instanceID, modelID, message string) (string, error) {
	p, err := s.Store.GetModelProvider(ctx, tenantID, providerID)
	if err != nil {
		return "", err
	}
	inst, err := s.Store.GetModelProviderInstance(ctx, tenantID, providerID, instanceID)
	if err != nil {
		return "", err
	}
	mod, err := s.Store.GetModelProviderModel(ctx, tenantID, instanceID, modelID)
	if err != nil {
		return "", err
	}
	if p == nil || inst == nil || mod == nil {
		return "", httperr.NotFound("provider instance model not found")
	}
	// Bound the outbound chat test so a slow/broken backend can never hold a
	// handler goroutine open indefinitely.
	testCtx, cancel := context.WithTimeout(ctx, modelTestTimeout)
	defer cancel()
	factory, ferr := s.resolveRAGFlowFactory(ctx, p.ProviderType)
	if ferr != nil {
		return "", ferr
	}
	return s.RAGFlow.ChatToModel(testCtx, factory, inst.InstanceName, mod.ModelName, message, false, mod.Thinking)
}

// ModelUpdateDTO is the patchable model fields from the API.
type ModelUpdateDTO struct {
	Status    string                 `json:"status"`
	MaxTokens int                    `json:"max_tokens"`
	ModelType []string               `json:"model_type"`
	Extra     map[string]interface{} `json:"extra"`
}

// CreateModelRouteRequest carries a routing rule.
type CreateModelRouteRequest struct {
	ProviderID  string
	Scenario    string
	ModelAlias  string
	TargetModel string
}

// CreateModelRoute creates a routing rule under a tenant's provider.
func (s *Service) CreateModelRoute(ctx context.Context, tenantID string, req CreateModelRouteRequest) (*model.ModelRoute, error) {
	if req.ProviderID == "" || req.ModelAlias == "" || req.TargetModel == "" {
		return nil, httperr.BadRequest(40031, "provider_id, model_alias and target_model are required")
	}
	p, err := s.Store.GetModelProvider(ctx, tenantID, req.ProviderID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, httperr.NotFound("model provider not found")
	}
	r := &model.ModelRoute{
		ID:          id.New(),
		TenantID:    tenantID,
		ProviderID:  req.ProviderID,
		Scenario:    req.Scenario,
		ModelAlias:  req.ModelAlias,
		TargetModel: req.TargetModel,
		Enabled:     true,
	}
	if err := s.Store.CreateModelRoute(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

// ListModelRoutes returns a tenant's routing rules.
func (s *Service) ListModelRoutes(ctx context.Context, tenantID string) ([]model.ModelRoute, error) {
	return s.Store.ListModelRoutes(ctx, tenantID)
}

// UpdateModelRoute edits an existing routing rule.
func (s *Service) UpdateModelRoute(ctx context.Context, tenantID, id string, req CreateModelRouteRequest) (*model.ModelRoute, error) {
	r, err := s.Store.GetModelRoute(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, httperr.NotFound("model route not found")
	}
	pin, err := s.ValidateEnterpriseModelRoutePin(ctx, r)
	if err != nil {
		return nil, err
	}
	if pin != nil && req.TargetModel != "" && req.TargetModel != r.TargetModel {
		return nil, httperr.BadRequest(40031, "pinned model route target cannot change")
	}
	if pin != nil && req.ProviderID != "" && req.ProviderID != r.ProviderID {
		return nil, httperr.BadRequest(40031, "pinned model route provider cannot change")
	}
	if req.ProviderID != "" {
		provider, err := s.Store.GetModelProvider(ctx, tenantID, req.ProviderID)
		if err != nil {
			return nil, err
		}
		if provider == nil {
			return nil, httperr.NotFound("model provider not found")
		}
		r.ProviderID = req.ProviderID
	}
	if req.ModelAlias != "" {
		r.ModelAlias = req.ModelAlias
	}
	if req.TargetModel != "" {
		r.TargetModel = req.TargetModel
	}
	r.Scenario = req.Scenario
	if err := s.Store.UpdateModelRoute(ctx, r); err != nil {
		return nil, err
	}
	return r, nil
}

// DeleteModelRoute removes a routing rule.
func (s *Service) DeleteModelRoute(ctx context.Context, tenantID, id string) error {
	r, err := s.Store.GetModelRoute(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if r == nil {
		return httperr.NotFound("model route not found")
	}
	pin, err := s.ValidateEnterpriseModelRoutePin(ctx, r)
	if err != nil {
		return err
	}
	if pin != nil {
		return httperr.BadRequest(40031, "pinned model route cannot be deleted")
	}
	return s.Store.DeleteModelRoute(ctx, tenantID, id)
}

// defaultRAGFlowFactory maps a RAGFlow-X provider type to the RAGFlow factory
// name used for OpenAI-compatible and related providers.
func defaultRAGFlowFactory(providerType string) string {
	switch providerType {
	case "vllm":
		return "VLLM"
	case "ollama":
		return "Ollama"
	default:
		// openai and any OpenAI-compatible local endpoint
		return "OpenAI-API-Compatible"
	}
}

// resolveRAGFlowFactory maps a provider's stored type to a real RAGFlow factory.
// It matches the factory catalog case-insensitively and, for legacy or custom
// names (e.g. "deepseek", "openai"), falls back to the OpenAI-compatible
// factory so instance/model calls against uni-cloud endpoints keep working.
func (s *Service) resolveRAGFlowFactory(ctx context.Context, providerType string) (string, error) {
	if strings.TrimSpace(providerType) == "" {
		return "", httperr.BadRequest(40030, "provider type is required")
	}
	infos, err := s.RAGFlow.ListProviders(ctx, true)
	if err != nil {
		return "", err
	}
	for _, info := range infos {
		if strings.EqualFold(info.Name, providerType) {
			return info.Name, nil
		}
	}
	return defaultRAGFlowFactory(providerType), nil
}

func containsString(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func defaultTypes(t []string) []string {
	if len(t) == 0 {
		return []string{"chat"}
	}
	return t
}

func defaultMaxTokens(m int) int {
	if m <= 0 {
		return 8192
	}
	return m
}

func findProviderInstance(instances []model.ModelProviderInstance, name string) *model.ModelProviderInstance {
	for index := range instances {
		if strings.EqualFold(instances[index].InstanceName, name) {
			return &instances[index]
		}
	}
	return nil
}

func findProviderModel(models []model.ModelProviderModel, name string) *model.ModelProviderModel {
	for index := range models {
		if strings.EqualFold(models[index].ModelName, name) {
			return &models[index]
		}
	}
	return nil
}

func remoteInstanceIdentifiers(remote *ragflow.ProviderInstance) []string {
	if remote == nil {
		return nil
	}
	if remote.ID != "" {
		return []string{remote.ID}
	}
	return []string{remote.InstanceName}
}

func rollbackProviderInstanceUpdate(ctx context.Context, svc *Service, factory string, previous *model.ModelProviderInstance, previousModels []model.ModelProviderModel) error {
	previousAPIKey := previous.APIKeyEnc
	if plainAPIKey, err := crypto.Decrypt(svc.EncryptKey, previous.APIKeyEnc); err == nil {
		previousAPIKey = plainAPIKey
	} else {
		logger.Warn("failed to decrypt previous provider credential before remote rollback",
			"provider", factory, "instance_name", previous.InstanceName, "error", err)
	}
	previousInfos := make([]ragflow.ModelInfo, 0, len(previousModels))
	for _, previousModel := range previousModels {
		previousInfos = append(previousInfos, ragflow.ModelInfo{
			ModelType: model.ModelTypeLabels(previousModel.ModelType),
			ModelName: previousModel.ModelName,
			MaxTokens: previousModel.MaxTokens,
		})
	}
	if err := svc.RAGFlow.UpdateProviderInstance(ctx, factory, previous.InstanceName, ragflow.RegisterModelProviderRequest{
		InstanceName: previous.InstanceName,
		APIKey:       previousAPIKey,
		BaseURL:      previous.BaseURL,
		Region:       previous.Region,
		Models:       previousInfos,
	}); err != nil {
		logger.Warn("failed to restore external provider instance after local update failure",
			"provider", factory, "instance_name", previous.InstanceName, "error", err)
		return err
	}
	return nil
}

func rollbackProviderModelUpdate(ctx context.Context, svc *Service, factory string, instance *model.ModelProviderInstance, previous *model.ModelProviderModel) error {
	if err := svc.RAGFlow.UpdateModel(ctx, factory, instance.InstanceName, previous.ModelName, ragflow.ModelUpdate{
		Status:    previous.Status,
		MaxTokens: previous.MaxTokens,
		ModelType: model.ModelTypeLabels(previous.ModelType),
	}); err != nil {
		logger.Warn("failed to restore external provider model after local update failure",
			"provider", factory, "instance_name", instance.InstanceName, "model_name", previous.ModelName, "error", err)
		return err
	}
	return nil
}

func rollbackExternalProviderCreation(ctx context.Context, svc *Service, tenantID, providerID, providerType, operation string) {
	if err := svc.RAGFlow.DeleteProvider(ctx, providerType); err != nil {
		logger.Warn("failed to roll back external provider after local persistence failure",
			"provider_type", providerType, "error", err)
		recordProviderCompensationFailure(ctx, svc, tenantID, providerID, operation, "delete-upstream-provider", err)
	}
}

func recordProviderCompensationFailure(ctx context.Context, svc *Service, tenantID, providerID, operation, stage string, cause error) {
	detail, err := json.Marshal(map[string]any{
		"operation": operation,
		"stage":     stage,
		"error":     cause.Error(),
	})
	if err != nil {
		logger.Warn("failed to encode provider compensation audit", "operation", operation, "error", err)
		return
	}
	if auditErr := svc.RecordAudit(ctx, &model.AuditLog{
		TenantID:   tenantID,
		Action:     AuditActionProviderCompensationFailed,
		Resource:   "model-provider",
		ResourceID: providerID,
		DetailJSON: string(detail),
		Result:     "FAILURE",
	}); auditErr != nil {
		logger.Warn("failed to record provider compensation failure",
			"tenant_id", tenantID, "provider_id", providerID, "operation", operation, "error", auditErr)
	}
}

const AuditActionProviderCompensationFailed = "provider.compensation.failed"

func maskOrDefault(t []string) int {
	m := model.ModelTypeMask(t)
	if m == 0 {
		return model.ModelTypeChat
	}
	return m
}

func modelInfoExtra(isTools, thinking bool) map[string]interface{} {
	return map[string]interface{}{"is_tools": isTools, "thinking": thinking}
}
