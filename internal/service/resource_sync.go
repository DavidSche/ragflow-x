package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

const (
	ResourceSyncSourceID            = "ragflow_global"
	ResourceSyncCredentialVersion   = "ragflow_global_v1"
	ResourceSyncGlobalTenant        = "__GLOBAL__"
	staleSyncRunTimeout             = 30 * time.Minute
	resourceSyncRunExecutionTimeout = 30 * time.Minute
)

var allowedResourceSyncTypes = map[string]struct{}{
	model.SyncItemTypeDataset:   {},
	model.SyncItemTypeChat:      {},
	model.SyncItemTypeAgent:     {},
	model.SyncItemTypeSearchApp: {},
	model.SyncItemTypeMemory:    {},
}

type ResourceSyncSettingUpdate struct {
	Enabled                   *bool    `json:"enabled"`
	ScheduledReconcileEnabled *bool    `json:"scheduled_reconcile_enabled"`
	ResourceTypes             []string `json:"resource_types"`
	IntervalSeconds           *int     `json:"interval_seconds"`
	BatchSize                 *int     `json:"batch_size"`
	MaxResources              *int     `json:"max_resources"`
	DeletionConfirmations     *int     `json:"deletion_confirmations"`
	DefaultTargetTenantID     *string  `json:"default_target_tenant_id"`
	DefaultOwnerID            *string  `json:"default_owner_id"`
}

type ResourceSyncSettingView struct {
	ID                        string          `json:"id"`
	SourceID                  string          `json:"source_id"`
	Enabled                   bool            `json:"enabled"`
	ScheduledReconcileEnabled bool            `json:"scheduled_reconcile_enabled"`
	ResourceTypes             []string        `json:"resource_types"`
	Scope                     json.RawMessage `json:"scope"`
	IntervalSeconds           int             `json:"interval_seconds"`
	BatchSize                 int             `json:"batch_size"`
	MaxResources              int             `json:"max_resources"`
	DeletionConfirmations     int             `json:"deletion_confirmations"`
	DefaultTargetTenantID     string          `json:"default_target_tenant_id"`
	DefaultOwnerID            string          `json:"default_owner_id"`
	CreatedAt                 time.Time       `json:"created_at"`
	UpdatedAt                 time.Time       `json:"updated_at"`
}

func NewResourceSyncSettingView(setting *model.ResourceSyncSetting) (*ResourceSyncSettingView, error) {
	resourceTypes := []string{}
	if err := json.Unmarshal([]byte(setting.ResourceTypesJSON), &resourceTypes); err != nil {
		return nil, httperr.Internal("invalid ragflow sync resource type configuration")
	}
	var scope json.RawMessage
	if err := json.Unmarshal([]byte(setting.ScopeJSON), &scope); err != nil {
		return nil, httperr.Internal("invalid ragflow sync scope configuration")
	}
	return &ResourceSyncSettingView{
		ID:                        setting.ID,
		SourceID:                  setting.SourceID,
		Enabled:                   setting.Enabled,
		ScheduledReconcileEnabled: setting.ScheduledReconcileEnabled,
		ResourceTypes:             resourceTypes,
		Scope:                     scope,
		IntervalSeconds:           setting.IntervalSeconds,
		BatchSize:                 setting.BatchSize,
		MaxResources:              setting.MaxResources,
		DeletionConfirmations:     setting.DeletionConfirmations,
		DefaultTargetTenantID:     setting.DefaultTargetTenantID,
		DefaultOwnerID:            setting.DefaultOwnerID,
		CreatedAt:                 setting.CreatedAt,
		UpdatedAt:                 setting.UpdatedAt,
	}, nil
}

type ResourceSyncMappingRequest struct {
	ExternalTenantID string `json:"external_tenant_id"`
	TargetTenantID   string `json:"target_tenant_id"`
}

type ResourceSyncConflictRequest struct {
	ItemID     string `json:"item_id" binding:"required"`
	Resolution string `json:"resolution" binding:"required"`
}

type ResourceSyncImportRequest struct {
	Async bool `json:"async"`
}

type ResourceSyncItemAssignmentRequest struct {
	ItemIDs  []string `json:"item_ids" binding:"required"`
	TenantID string   `json:"tenant_id" binding:"required"`
	OwnerID  string   `json:"owner_id"`
}

type ResourceSyncItemAssignmentResult struct {
	Updated int `json:"updated"`
}

type ResourceSyncRelinkRequest struct {
	ExternalTenantID string `json:"external_tenant_id"`
	ExternalID       string `json:"external_id" binding:"required"`
	Scope            string `json:"scope"`
	LocalID          string `json:"local_id" binding:"required"`
	TenantID         string `json:"tenant_id" binding:"required"`
	OwnerID          string `json:"owner_id"`
}

type ResourceSyncRelinkTenantOption struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

type ResourceSyncRelinkResourceOption struct {
	TenantID string `json:"tenant_id"`
	LocalID  string `json:"local_id"`
	Name     string `json:"name"`
}

type resourceScanResult struct {
	items    []model.SyncItem
	snapshot map[string]json.RawMessage
	complete bool
}

// resourceSyncSummary reports action/result counts per resource type so a run
// remains reviewable without scanning every SyncItem.
type resourceSyncSummary map[string]map[string]int

func newResourceSyncSummary() resourceSyncSummary {
	return map[string]map[string]int{}
}

func (summary resourceSyncSummary) add(resourceType, key string) {
	if summary[resourceType] == nil {
		summary[resourceType] = map[string]int{}
	}
	summary[resourceType][key]++
}

func (summary resourceSyncSummary) count(resourceType, key string) int {
	return summary[resourceType][key]
}

func (summary resourceSyncSummary) marshal() string {
	raw, _ := json.Marshal(summary)
	return string(raw)
}

func defaultResourceSyncSetting(tenantID string) model.ResourceSyncSetting {
	return model.ResourceSyncSetting{
		ID:                        id.New(),
		SourceID:                  ResourceSyncSourceID,
		TenantID:                  tenantID,
		Enabled:                   false,
		ScheduledReconcileEnabled: false,
		ResourceTypesJSON:         `["dataset","chat","agent","search_app"]`,
		ScopeJSON:                 `{"scope_type":"ALL_AUTHORIZED","tenant_ids":[]}`,
		IntervalSeconds:           900,
		BatchSize:                 100,
		MaxResources:              5000,
		DeletionConfirmations:     3,
	}
}

func normalizeResourceTypes(values []string) ([]string, error) {
	if len(values) == 0 {
		return []string{
			model.SyncItemTypeDataset, model.SyncItemTypeChat,
			model.SyncItemTypeAgent, model.SyncItemTypeSearchApp,
		}, nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := allowedResourceSyncTypes[value]; !ok {
			return nil, httperr.BadRequest(40099, "unsupported ragflow sync resource type")
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, httperr.BadRequest(40099, "at least one ragflow sync resource type is required")
	}
	return result, nil
}

func normalizeExternalTenant(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ResourceSyncGlobalTenant
	}
	return value
}

func resourceStringSlice(value interface{}) []string {
	items, ok := value.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if typed, ok := item.(string); ok {
			result = append(result, typed)
		}
	}
	return result
}

func (s *Service) GetResourceSyncSetting(ctx context.Context, tenantID string) (*model.ResourceSyncSetting, error) {
	setting, err := s.Store.GetResourceSyncSetting(ctx, ResourceSyncSourceID)
	if err != nil {
		return nil, err
	}
	if setting != nil {
		return setting, nil
	}
	defaultSetting := defaultResourceSyncSetting(tenantID)
	return &defaultSetting, nil
}

func (s *Service) UpdateResourceSyncSetting(ctx context.Context, tenantID string, update ResourceSyncSettingUpdate) (*model.ResourceSyncSetting, error) {
	setting, err := s.GetResourceSyncSetting(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if update.ResourceTypes != nil {
		resourceTypes, err := normalizeResourceTypes(update.ResourceTypes)
		if err != nil {
			return nil, err
		}
		typesJSON, err := json.Marshal(resourceTypes)
		if err != nil {
			return nil, err
		}
		setting.ResourceTypesJSON = string(typesJSON)
	}

	if update.Enabled != nil {
		setting.Enabled = *update.Enabled
	}
	if update.ScheduledReconcileEnabled != nil {
		setting.ScheduledReconcileEnabled = *update.ScheduledReconcileEnabled
	}
	if update.IntervalSeconds != nil {
		if *update.IntervalSeconds < 60 || *update.IntervalSeconds > 86400 {
			return nil, httperr.BadRequest(40099, "sync interval must be between 60 and 86400 seconds")
		}
		setting.IntervalSeconds = *update.IntervalSeconds
	}
	if update.BatchSize != nil {
		if *update.BatchSize < 10 || *update.BatchSize > 500 {
			return nil, httperr.BadRequest(40099, "sync batch size must be between 10 and 500")
		}
		setting.BatchSize = *update.BatchSize
	}
	if update.MaxResources != nil {
		if *update.MaxResources < 1 || *update.MaxResources > 100000 {
			return nil, httperr.BadRequest(40099, "max resources must be between 1 and 100000")
		}
		setting.MaxResources = *update.MaxResources
	}
	if update.DeletionConfirmations != nil {
		if *update.DeletionConfirmations < 2 || *update.DeletionConfirmations > 10 {
			return nil, httperr.BadRequest(40099, "deletion confirmations must be between 2 and 10")
		}
		setting.DeletionConfirmations = *update.DeletionConfirmations
	}
	if update.DefaultTargetTenantID != nil {
		value := strings.TrimSpace(*update.DefaultTargetTenantID)
		if value != "" {
			tenant, err := s.Store.GetTenant(ctx, value)
			if err != nil {
				return nil, err
			}
			if tenant == nil || tenant.Status != model.TenantStatusActive {
				return nil, httperr.BadRequest(40099, "default target tenant must be active")
			}
		}
		setting.DefaultTargetTenantID = value
	}
	if update.DefaultOwnerID != nil {
		ownerID := strings.TrimSpace(*update.DefaultOwnerID)
		if ownerID != "" {
			owner, err := s.Store.GetUser(ctx, ownerID)
			if err != nil {
				return nil, err
			}
			if owner == nil || owner.Status != model.UserStatusActive {
				return nil, httperr.BadRequest(40099, "default owner must be an active user")
			}
			if setting.DefaultTargetTenantID != "" && owner.TenantID != setting.DefaultTargetTenantID {
				return nil, httperr.BadRequest(40099, "default owner must belong to default target tenant")
			}
		}
		setting.DefaultOwnerID = ownerID
	}
	if setting.DefaultTargetTenantID != "" && setting.DefaultOwnerID != "" {
		owner, err := s.Store.GetUser(ctx, setting.DefaultOwnerID)
		if err != nil {
			return nil, err
		}
		if owner == nil || owner.Status != model.UserStatusActive || owner.TenantID != setting.DefaultTargetTenantID {
			return nil, httperr.BadRequest(40099, "default owner must belong to default target tenant")
		}
	}
	setting.UpdatedAt = time.Now().UTC()
	if err := s.Store.SaveResourceSyncSetting(ctx, setting); err != nil {
		return nil, err
	}
	if !setting.ScheduledReconcileEnabled {
		if _, err := s.Store.CancelQueuedJobsWithPayload(ctx, model.JobKindRAGFlowReconcile, ResourceSyncSourceID, map[string]interface{}{"scheduled": true}); err != nil {
			return nil, err
		}
	}
	if setting.Enabled && setting.ScheduledReconcileEnabled && s.Runner != nil {
		if _, err := s.ScheduleResourceSyncReconcile(ctx, ""); err != nil {
			return nil, err
		}
	}
	return setting, nil
}

func (s *Service) recoverStaleSyncRuns(ctx context.Context) error {
	now := time.Now().UTC()
	recovered, err := s.Store.FailStaleSyncRuns(
		ctx, ResourceSyncSourceID, now.Add(-staleSyncRunTimeout), now,
		"ragflow sync run timed out and was recovered",
	)
	obs.Get().AddResourceSyncStaleRuns(ResourceSyncSourceID, recovered)
	return err
}

func (s *Service) failSyncRun(ctx context.Context, run *model.SyncRun, cause error) {
	if run == nil {
		return
	}
	now := time.Now().UTC()
	if persisted, err := s.Store.GetSyncRun(ctx, run.ID); err == nil && persisted != nil {
		run.ProgressTotal = persisted.ProgressTotal
		run.ProgressDone = persisted.ProgressDone
		run.ProgressFailed = persisted.ProgressFailed
	}
	run.Status = model.SyncRunFailed
	run.Error = cause.Error()
	run.FinishedAt = &now
	run.UpdatedAt = now
	if err := s.Store.UpdateSyncRun(ctx, run); err != nil {
		return
	}
	obs.Get().ClearResourceSyncRunProgress(run.SourceID)
}

func (s *Service) ListTenantMappings(ctx context.Context) ([]model.ResourceSyncTenantMapping, error) {
	return s.Store.ListTenantMappings(ctx, ResourceSyncSourceID)
}

func (s *Service) UpsertTenantMapping(ctx context.Context, createdBy string, request ResourceSyncMappingRequest) (*model.ResourceSyncTenantMapping, error) {
	externalTenantID := normalizeExternalTenant(request.ExternalTenantID)
	targetTenantID := strings.TrimSpace(request.TargetTenantID)
	if targetTenantID == "" {
		return nil, httperr.BadRequest(40099, "target tenant is required")
	}
	tenant, err := s.Store.GetTenant(ctx, targetTenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil || tenant.Status != model.TenantStatusActive {
		return nil, httperr.BadRequest(40099, "target tenant must be active")
	}
	mapping, err := s.Store.GetTenantMapping(ctx, ResourceSyncSourceID, externalTenantID)
	if err != nil {
		return nil, err
	}
	if mapping == nil {
		mapping = &model.ResourceSyncTenantMapping{
			ID:               id.New(),
			SourceID:         ResourceSyncSourceID,
			ExternalTenantID: externalTenantID,
			CreatedBy:        createdBy,
			CreatedAt:        time.Now().UTC(),
		}
	}
	mapping.TargetTenantID = targetTenantID
	mapping.Status = "active"
	mapping.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpsertTenantMapping(ctx, mapping); err != nil {
		return nil, err
	}
	return mapping, nil
}

func (s *Service) DeleteTenantMapping(ctx context.Context, externalTenantID string) error {
	return s.Store.DeleteTenantMapping(ctx, ResourceSyncSourceID, normalizeExternalTenant(externalTenantID))
}

func (s *Service) ListResourceSyncRuns(ctx context.Context, page, pageSize int, status string) ([]model.SyncRun, int64, error) {
	return s.Store.ListSyncRuns(ctx, page, pageSize, ResourceSyncSourceID, status)
}

func (s *Service) GetResourceSyncRun(ctx context.Context, id string) (*model.SyncRun, error) {
	run, err := s.Store.GetSyncRun(ctx, id)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, httperr.NotFound("sync run not found")
	}
	return run, nil
}

func (s *Service) ListResourceSyncItems(ctx context.Context, runID string, page, pageSize int) ([]model.SyncItem, int64, error) {
	items, total, err := s.Store.ListSyncItems(ctx, runID, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	artifactIDs := make([]string, 0, len(items))
	for _, item := range items {
		if artifactID := artifactIDFromRef(item.PayloadDiffRef); artifactID != "" && artifactID != item.PayloadDiffRef {
			artifactIDs = append(artifactIDs, artifactID)
		}
	}
	artifactsByID := make(map[string]model.ResourceSyncArtifact, len(artifactIDs))
	if len(artifactIDs) > 0 {
		artifacts, err := s.Store.ListResourceSyncArtifactsByIDs(ctx, artifactIDs)
		if err != nil {
			return nil, 0, err
		}
		for _, artifact := range artifacts {
			artifactsByID[artifact.ID] = artifact
		}
	}
	for index := range items {
		if artifact, ok := artifactsByID[artifactIDFromRef(items[index].PayloadDiffRef)]; ok {
			items[index].PayloadDiffJSON = artifact.ContentJSON
		}
		items[index].SyncState = resourceItemSyncState(items[index])
	}
	return items, total, nil
}

func (s *Service) ListResourceSyncResources(ctx context.Context, resourceType, lifecycle string, page, pageSize int) ([]model.ResourceBinding, int64, error) {
	items, total, err := s.Store.ListResourceBindings(ctx, ResourceSyncSourceID, resourceType, lifecycle, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	for index := range items {
		items[index].SyncState = resourceBindingSyncState(items[index])
	}
	return items, total, nil
}

func (s *Service) ListResourceSyncRelinkTenantOptions(ctx context.Context, actorID, actorTenantID, requestedScope, search string, page, pageSize int) ([]ResourceSyncRelinkTenantOption, int64, error) {
	scope, err := s.ResolveTenantScope(ctx, actorID, actorTenantID, requestedScope, "")
	if err != nil {
		return nil, 0, err
	}
	tenants, total, err := s.Store.ListTenantsForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), page, pageSize, repository.TenantFilter{
		Name:   strings.TrimSpace(search),
		Status: model.TenantStatusActive,
	})
	if err != nil {
		return nil, 0, err
	}
	options := make([]ResourceSyncRelinkTenantOption, 0, len(tenants))
	for _, tenant := range tenants {
		options = append(options, ResourceSyncRelinkTenantOption{
			ID: tenant.ID, Name: tenant.Name, Type: tenant.Type, Status: tenant.Status,
		})
	}
	return options, total, nil
}

func (s *Service) ListResourceSyncRelinkResourceOptions(ctx context.Context, actorID, actorTenantID, resourceType, requestedScope, requestedTenantID, search string, page, pageSize int) ([]ResourceSyncRelinkResourceOption, int64, error) {
	if _, ok := allowedResourceSyncTypes[resourceType]; !ok {
		return nil, 0, httperr.BadRequest(40099, "unsupported resource type")
	}
	scope, err := s.ResolveTenantScope(ctx, actorID, actorTenantID, requestedScope, "")
	if err != nil {
		return nil, 0, err
	}
	targetTenantID := strings.TrimSpace(requestedTenantID)
	switch scope.Kind {
	case TenantScopeCurrent:
		if targetTenantID != "" && targetTenantID != scope.ActorTenantID {
			return nil, 0, ErrForbidden
		}
		targetTenantID = scope.ActorTenantID
	case TenantScopeSpecific:
		if targetTenantID == "" {
			targetTenantID = scope.TargetTenantID
		}
		if targetTenantID != scope.TargetTenantID {
			return nil, 0, ErrForbidden
		}
	case TenantScopeAllAuthorized:
		if targetTenantID != "" && !scope.ContainsTenant(targetTenantID) {
			return nil, 0, ErrForbidden
		}
	}

	options := make([]ResourceSyncRelinkResourceOption, 0)
	var total int64
	switch resourceType {
	case model.SyncItemTypeDataset:
		items, datasetTotal, err := s.Store.ListDatasetLinksForScopePage(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), repository.DatasetFilter{
			Name: strings.TrimSpace(search), TenantID: targetTenantID,
		}, page, pageSize)
		if err != nil {
			return nil, 0, err
		}
		total = datasetTotal
		for _, item := range items {
			options = append(options, ResourceSyncRelinkResourceOption{TenantID: item.TenantID, LocalID: item.ID, Name: item.Name})
		}
	case model.SyncItemTypeChat:
		items, chatTotal, err := s.Store.ListChatShadowsForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), repository.ChatFilter{
			Name: strings.TrimSpace(search), TenantID: targetTenantID,
		}, page, pageSize)
		if err != nil {
			return nil, 0, err
		}
		total = chatTotal
		for _, item := range items {
			options = append(options, ResourceSyncRelinkResourceOption{TenantID: item.TenantID, LocalID: item.ID, Name: item.Name})
		}
	case model.SyncItemTypeAgent:
		items, agentTotal, err := s.Store.ListAgentShadowsForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), repository.AgentFilter{
			Title: strings.TrimSpace(search), TenantID: targetTenantID,
		}, page, pageSize)
		if err != nil {
			return nil, 0, err
		}
		total = agentTotal
		for _, item := range items {
			options = append(options, ResourceSyncRelinkResourceOption{TenantID: item.TenantID, LocalID: item.ID, Name: item.Title})
		}
	case model.SyncItemTypeSearchApp:
		items, appTotal, err := s.Store.ListSearchAppShadowsForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), repository.SearchAppFilter{
			Name: strings.TrimSpace(search), TenantID: targetTenantID,
		}, page, pageSize)
		if err != nil {
			return nil, 0, err
		}
		total = appTotal
		for _, item := range items {
			options = append(options, ResourceSyncRelinkResourceOption{TenantID: item.TenantID, LocalID: item.ID, Name: item.Name})
		}
	case model.SyncItemTypeMemory:
		items, memoryTotal, err := s.Store.ListMemoryShadows(ctx, targetTenantID, targetTenantID == "", repository.MemoryFilter{
			Name: strings.TrimSpace(search),
		}, page, pageSize)
		if err != nil {
			return nil, 0, err
		}
		total = memoryTotal
		for _, item := range items {
			options = append(options, ResourceSyncRelinkResourceOption{TenantID: item.TenantID, LocalID: item.ID, Name: item.Name})
		}
	}
	return options, total, nil
}

func (s *Service) ListResourceBindingVersions(ctx context.Context, resourceType, externalTenantID, externalID string, page, pageSize int) ([]model.ResourceBindingVersion, int64, error) {
	binding, err := s.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, resourceType, normalizeExternalTenant(externalTenantID), externalID)
	if err != nil {
		return nil, 0, err
	}
	if binding == nil {
		return nil, 0, httperr.NotFound("resource binding not found")
	}
	return s.Store.ListBindingVersions(ctx, binding.ID, page, pageSize)
}

func (s *Service) ResolveResourceSyncConflict(ctx context.Context, resourceType, externalTenantID, externalID string, request ResourceSyncConflictRequest, userID, actorTenantID string) (*model.ResourceBinding, error) {
	binding, err := s.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, resourceType, normalizeExternalTenant(externalTenantID), externalID)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, httperr.NotFound("resource binding not found")
	}
	scope, err := s.ResolveTenantScope(ctx, userID, actorTenantID, TenantScopeValueSpecific, binding.TenantID)
	if err != nil {
		return nil, err
	}
	if !scope.ContainsTenant(binding.TenantID) {
		return nil, ErrForbidden
	}
	if binding.BindingLifecycle != model.ResourceBindingLifecycleActive {
		return nil, httperr.New(409, 40900, "resource binding is not active")
	}
	if binding.ConflictType == model.SyncConflictNone {
		return nil, httperr.New(409, 40900, "resource is not in conflict")
	}
	item, err := s.Store.GetSyncItem(ctx, request.ItemID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, httperr.NotFound("sync item not found")
	}
	if item.Action != model.SyncActionConflict || item.Status != model.SyncItemStatusSkipped ||
		item.ConflictType != binding.ConflictType ||
		item.ResourceType != binding.ResourceType || item.ExternalScopeKey != binding.ExternalScopeKey || item.ExternalID != binding.ExternalID {
		return nil, httperr.BadRequest(40099, "sync item does not match resource")
	}
	now := time.Now().UTC()
	switch request.Resolution {
	case "use_upstream":
		if item.ConflictType != model.SyncConflictContent {
			return nil, httperr.BadRequest(40099, "use_upstream is only valid for content conflicts")
		}
		run, err := s.GetResourceSyncRun(ctx, item.RunID)
		if err != nil {
			return nil, err
		}
		snapshot, err := s.getRunSnapshot(ctx, run)
		if err != nil {
			return nil, err
		}
		rawPayload, found := snapshot[resourceIdentityKey(item.ResourceType, item.ExternalScopeKey, item.ExternalID)]
		if !found {
			return nil, httperr.Internal("upstream payload is unavailable")
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(rawPayload, &payload); err != nil {
			return nil, httperr.Internal("invalid upstream payload")
		}
		if err := s.applyUpstreamResource(ctx, run.ID, item.ID, item.ResourceType, item.ExternalScopeKey, item.ExternalID, payload, binding.TenantID, "", userID); err != nil {
			return nil, err
		}
		binding.ConflictType = model.SyncConflictNone
		binding.GovernanceState = model.ResourceGovernanceNormal
	case "use_local":
		if item.ConflictType != model.SyncConflictContent {
			return nil, httperr.BadRequest(40099, "use_local is only valid for content conflicts")
		}
		binding.ConflictType = model.SyncConflictNone
		binding.LastSyncedUpstreamHash = item.UpstreamCurrentHash
		binding.LastSyncedLocalHash = s.currentLocalHash(ctx, binding.ResourceType, binding.TenantID, binding.LocalID)
	case "manual_merge":
		if item.ConflictType != model.SyncConflictContent {
			return nil, httperr.BadRequest(40099, "manual_merge is only valid for content conflicts")
		}
		binding.ConflictType = model.SyncConflictNone
		binding.GovernanceState = model.ResourceGovernancePendingReview
	default:
		return nil, httperr.BadRequest(40099, "unsupported conflict resolution")
	}
	binding.UpdatedAt = now
	if err := s.Store.UpdateResourceBinding(ctx, binding); err != nil {
		return nil, err
	}
	item.Status = model.SyncItemStatusSucceeded
	item.UpdatedAt = now
	if err := s.Store.UpdateSyncItem(ctx, item); err != nil {
		return nil, err
	}
	detail, err := json.Marshal(map[string]any{
		"item_id":          item.ID,
		"run_id":           item.RunID,
		"resolution":       request.Resolution,
		"conflict_type":    item.ConflictType,
		"binding_id":       binding.ID,
		"governance_state": binding.GovernanceState,
	})
	if err != nil {
		return nil, err
	}
	if err := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: binding.TenantID, UserID: userID, ActorTenantID: actorTenantID, TargetTenantID: binding.TenantID,
		Action: "ragflow_sync.resource.conflict_resolved", Resource: "ragflow-sync", ResourceID: binding.ID,
		DetailJSON: string(detail), AuthorizationPermission: "manage:ragflow-sync",
	}); err != nil {
		return nil, err
	}
	return binding, nil
}

func (s *Service) RelinkResourceSync(ctx context.Context, resourceType, externalTenantID, externalID string, request ResourceSyncRelinkRequest, actorID, actorTenantID string) (*model.ResourceBinding, error) {
	if _, ok := allowedResourceSyncTypes[resourceType]; !ok {
		return nil, httperr.BadRequest(40099, "unsupported resource type")
	}
	targetTenantID := strings.TrimSpace(request.TenantID)
	if targetTenantID == "" || strings.TrimSpace(request.LocalID) == "" {
		return nil, httperr.BadRequest(40099, "target tenant and local resource are required")
	}
	requestedScope := TenantScopeValueSpecific
	if targetTenantID == actorTenantID {
		requestedScope = TenantScopeValueCurrent
	}
	scope, err := s.ResolveTenantScope(ctx, actorID, actorTenantID, requestedScope, targetTenantID)
	if err != nil {
		return nil, err
	}
	if !scope.ContainsTenant(targetTenantID) {
		return nil, ErrForbidden
	}
	binding, err := s.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, resourceType, normalizeExternalTenant(externalTenantID), externalID)
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, httperr.NotFound("resource binding not found")
	}
	existingLocal, err := s.Store.GetResourceBindingByLocal(ctx, resourceType, strings.TrimSpace(request.LocalID))
	if err != nil {
		return nil, err
	}
	if existingLocal != nil && existingLocal.ID != binding.ID {
		return nil, httperr.New(409, 40900, "target local resource is already bound")
	}
	localHash := s.currentLocalHash(ctx, resourceType, targetTenantID, request.LocalID)
	if localHash == "" {
		return nil, httperr.BadRequest(40099, "target local resource does not exist")
	}
	now := time.Now().UTC()
	previousTenantID := binding.TenantID
	previousLocalID := binding.LocalID
	previousConflictType := binding.ConflictType
	binding.LocalID = request.LocalID
	binding.TenantID = targetTenantID
	binding.LocalType = resourceType
	binding.ConflictType = model.SyncConflictNone
	binding.BindingLifecycle = model.ResourceBindingLifecycleActive
	binding.GovernanceState = model.ResourceGovernancePendingReview
	binding.MissingConfirmations = 0
	binding.LastSyncedLocalHash = localHash
	binding.UpdatedAt = now
	if err := s.Store.UpdateResourceBinding(ctx, binding); err != nil {
		return nil, err
	}
	if _, err := s.Store.MarkResourceSyncItemsSucceeded(ctx, resourceType, binding.ExternalScopeKey, binding.ExternalID, previousConflictType, now); err != nil {
		return nil, err
	}
	detail, _ := json.Marshal(map[string]any{
		"external_scope_key": binding.ExternalScopeKey,
		"external_id":        binding.ExternalID,
		"resource_type":      resourceType,
		"previous_tenant_id": previousTenantID,
		"previous_local_id":  previousLocalID,
		"target_tenant_id":   targetTenantID,
		"target_local_id":    request.LocalID,
	})
	if err := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: targetTenantID, UserID: actorID, ActorTenantID: actorTenantID, TargetTenantID: targetTenantID,
		Action: "ragflow_sync.resource.relinked", Resource: "ragflow-sync", ResourceID: binding.ID,
		DetailJSON: string(detail), AuthorizationPermission: "manage:ragflow-sync",
	}); err != nil {
		return nil, err
	}
	return binding, nil
}

func (s *Service) EnqueueResourceSyncImport(ctx context.Context, runID, userID string) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	active, err := s.Store.CountActiveJobs(ctx, model.JobKindRAGFlowImport, runID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	payload, _ := json.Marshal(map[string]string{"run_id": runID, "user_id": userID})
	return s.Runner.Enqueue(ctx, model.JobKindRAGFlowImport, "ragflow_import:"+runID, ResourceSyncSourceID, string(payload), time.Time{}, 0)
}

func (s *Service) EnqueueResourceSyncReconcile(ctx context.Context, resourceTypes []string) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	if len(resourceTypes) == 0 {
		resourceTypes = []string{
			model.SyncItemTypeDataset, model.SyncItemTypeChat,
			model.SyncItemTypeAgent, model.SyncItemTypeSearchApp,
			model.SyncItemTypeMemory,
		}
	}
	normalized, err := normalizeResourceTypes(resourceTypes)
	if err != nil {
		return false, err
	}
	payload, _ := json.Marshal(map[string][]string{"resource_types": normalized})
	key := "ragflow_reconcile:" + strconv.FormatInt(time.Now().UTC().Unix(), 10)
	return s.Runner.Enqueue(ctx, model.JobKindRAGFlowReconcile, key, ResourceSyncSourceID, string(payload), time.Time{}, 0)
}

func resourceIdentityKey(resourceType, externalTenantID, externalID string) string {
	return strings.Join([]string{resourceType, externalTenantID, externalID}, "\x00")
}

func (s *Service) tenantMappingTarget(ctx context.Context, externalTenantID string, setting *model.ResourceSyncSetting) (string, error) {
	mapping, err := s.Store.GetTenantMapping(ctx, ResourceSyncSourceID, externalTenantID)
	if err != nil {
		return "", err
	}
	if mapping != nil && mapping.Status == "active" {
		return mapping.TargetTenantID, nil
	}
	if setting.DefaultTargetTenantID != "" {
		tenant, err := s.Store.GetTenant(ctx, setting.DefaultTargetTenantID)
		if err != nil {
			return "", err
		}
		if tenant != nil && tenant.Status == model.TenantStatusActive {
			return setting.DefaultTargetTenantID, nil
		}
	}
	return "", nil
}

func (s *Service) listAllAgents(ctx context.Context, pageSize, maxCount int) ([]ragflow.Agent, bool, error) {
	var all []ragflow.Agent
	for page := 1; ; page++ {
		items, _, err := s.RAGFlow.ListAgents(ctx, ragflow.ListAgentsFilter{Page: page, PageSize: pageSize})
		if err != nil {
			return nil, false, err
		}
		all = append(all, items...)
		if len(all) >= maxCount {
			return all[:maxCount], false, nil
		}
		if len(items) < pageSize {
			return all, true, nil
		}
	}
}

func (s *Service) listAllSearchApps(ctx context.Context, pageSize, maxCount int) ([]ragflow.SearchAppListItem, bool, error) {
	var all []ragflow.SearchAppListItem
	for page := 1; ; page++ {
		items, _, err := s.RAGFlow.ListSearchApps(ctx, ragflow.ListSearchAppsFilter{Page: page, PageSize: pageSize})
		if err != nil {
			return nil, false, err
		}
		all = append(all, items...)
		if len(all) >= maxCount {
			return all[:maxCount], false, nil
		}
		if len(items) < pageSize {
			return all, true, nil
		}
	}
}

func (s *Service) scanResourceTypes(ctx context.Context, resourceTypes []string, setting *model.ResourceSyncSetting) (*resourceScanResult, error) {
	result := &resourceScanResult{snapshot: map[string]json.RawMessage{}, complete: true}
	now := time.Now().UTC()
	appendItem := func(resourceType, externalTenantID, externalID, externalVersion string, externalUpdatedAt *time.Time, payload map[string]interface{}, action, conflictType, localID, tenantID, localHash string) {
		scope := normalizeExternalTenant(externalTenantID)
		key := resourceIdentityKey(resourceType, scope, externalID)
		payload["id"] = externalID
		payload["external_scope_key"] = scope
		payload["resource_type"] = resourceType
		persistedPayload := resourcePersistedPayload(resourceType, payload)
		persistedPayload["id"] = externalID
		persistedPayload["external_scope_key"] = scope
		persistedPayload["resource_type"] = resourceType
		payloadJSON, _ := json.Marshal(persistedPayload)
		result.snapshot[key] = payloadJSON
		result.items = append(result.items, model.SyncItem{
			ID:                     id.New(),
			RunID:                  "",
			ResourceType:           resourceType,
			ExternalScopeKey:       scope,
			ExternalID:             externalID,
			ExternalTenantID:       scope,
			ExternalVersion:        externalVersion,
			ExternalUpdatedAt:      externalUpdatedAt,
			LocalID:                localID,
			TenantID:               tenantID,
			OwnerID:                setting.DefaultOwnerID,
			Action:                 action,
			Status:                 model.SyncItemStatusPending,
			ConflictType:           conflictType,
			UpstreamCurrentHash:    resourceUpstreamHash(resourceType, payload),
			UpstreamLastSyncedHash: "",
			LocalCurrentHash:       localHash,
			LocalLastSyncedHash:    "",
			CreatedAt:              now,
			UpdatedAt:              now,
		})
	}

	for _, resourceType := range resourceTypes {
		switch resourceType {
		case model.SyncItemTypeDataset:
			datasets, err := s.RAGFlow.ListDatasets(ctx)
			if err != nil {
				return nil, httperr.New(502, 50208, fmt.Sprintf("ragflow list datasets failed: %v", err))
			}
			if len(datasets) > setting.MaxResources {
				datasets = datasets[:setting.MaxResources]
				result.complete = false
			}
			for _, dataset := range datasets {
				payload := map[string]interface{}{
					"name": dataset.Name, "chunk_count": dataset.ChunkCount,
					"document_count": dataset.DocumentCount,
					"update_time":    dataset.UpdateTime,
				}
				externalUpdatedAt := ragflowUpdateTime(dataset.UpdateTime)
				localHash, action, conflictType, localID, tenantID, planErr := s.planExistingResource(ctx, setting, resourceType, dataset.ID, payload)
				if planErr != nil {
					return nil, planErr
				}
				appendItem(resourceType, ResourceSyncGlobalTenant, dataset.ID, strconv.FormatInt(dataset.UpdateTime, 10), externalUpdatedAt, payload, action, conflictType, localID, tenantID, localHash)
			}
		case model.SyncItemTypeChat:
			chats, err := s.RAGFlow.ListChats(ctx)
			if err != nil {
				return nil, httperr.New(502, 50245, fmt.Sprintf("ragflow list chats failed: %v", err))
			}
			if len(chats) > setting.MaxResources {
				chats = chats[:setting.MaxResources]
				result.complete = false
			}
			for _, chat := range chats {
				payload := map[string]interface{}{
					"name": chat.Name, "dataset_ids": chat.DatasetIDs,
					"status": chat.Status, "update_time": chat.UpdateTime,
				}
				externalUpdatedAt := ragflowUpdateTime(chat.UpdateTime)
				localHash, action, conflictType, localID, tenantID, planErr := s.planExistingResource(ctx, setting, resourceType, chat.ID, payload)
				if planErr != nil {
					return nil, planErr
				}
				appendItem(resourceType, ResourceSyncGlobalTenant, chat.ID, strconv.FormatInt(chat.UpdateTime, 10), externalUpdatedAt, payload, action, conflictType, localID, tenantID, localHash)
			}
		case model.SyncItemTypeAgent:
			agents, complete, err := s.listAllAgents(ctx, setting.BatchSize, setting.MaxResources)
			if err != nil {
				return nil, httperr.New(502, 50280, fmt.Sprintf("ragflow list agents failed: %v", err))
			}
			if !complete {
				result.complete = false
			}
			for _, agent := range agents {
				payload := map[string]interface{}{
					"title": agent.Title, "canvas_type": agent.CanvasType,
					"canvas_category": agent.CanvasCategory, "release": agent.Release,
					"update_time": agent.UpdateTime,
				}
				externalUpdatedAt := ragflowUpdateTime(agent.UpdateTime)
				localHash, action, conflictType, localID, tenantID, planErr := s.planExistingResource(ctx, setting, resourceType, agent.ID, payload)
				if planErr != nil {
					return nil, planErr
				}
				appendItem(resourceType, ResourceSyncGlobalTenant, agent.ID, strconv.FormatInt(agent.UpdateTime, 10), externalUpdatedAt, payload, action, conflictType, localID, tenantID, localHash)
			}
		case model.SyncItemTypeSearchApp:
			apps, complete, err := s.listAllSearchApps(ctx, setting.BatchSize, setting.MaxResources)
			if err != nil {
				return nil, httperr.New(502, 50260, fmt.Sprintf("ragflow list search apps failed: %v", err))
			}
			if !complete {
				result.complete = false
			}
			for _, app := range apps {
				payload := map[string]interface{}{
					"name": app.Name, "description": app.Description,
					"update_time": app.UpdateTime,
				}
				externalUpdatedAt := ragflowUpdateTime(app.UpdateTime)
				localHash, action, conflictType, localID, tenantID, planErr := s.planExistingResource(ctx, setting, resourceType, app.ID, payload)
				if planErr != nil {
					return nil, planErr
				}
				appendItem(resourceType, ResourceSyncGlobalTenant, app.ID, strconv.FormatInt(app.UpdateTime, 10), externalUpdatedAt, payload, action, conflictType, localID, tenantID, localHash)
			}
		case model.SyncItemTypeMemory:
			memories, complete, err := s.listAllMemories(ctx, setting.BatchSize, setting.MaxResources)
			if err != nil {
				return nil, httperr.New(502, 50292, fmt.Sprintf("ragflow list memories failed: %v", err))
			}
			if !complete {
				result.complete = false
			}
			for _, memory := range memories {
				payload := map[string]interface{}{
					"name": memory.Name, "description": memory.Description,
					"memory_type": memory.MemoryType, "update_time": memory.UpdateTime,
				}
				externalUpdatedAt := ragflowUpdateTime(memory.UpdateTime)
				localHash, action, conflictType, localID, tenantID, planErr := s.planExistingResource(ctx, setting, resourceType, memory.ID, payload)
				if planErr != nil {
					return nil, planErr
				}
				appendItem(resourceType, ResourceSyncGlobalTenant, memory.ID, strconv.FormatInt(memory.UpdateTime, 10), externalUpdatedAt, payload, action, conflictType, localID, tenantID, localHash)
			}
		}
	}
	return result, nil
}

func (s *Service) listAllMemories(ctx context.Context, pageSize, maxCount int) ([]ragflow.Memory, bool, error) {
	var all []ragflow.Memory
	for page := 1; ; page++ {
		items, total, err := s.RAGFlow.ListMemories(ctx, ragflow.ListMemoriesFilter{Page: page, PageSize: pageSize})
		if err != nil {
			return nil, false, err
		}
		all = append(all, items...)
		if len(all) >= maxCount {
			return all[:maxCount], false, nil
		}
		if int64(page*pageSize) >= total || len(items) == 0 {
			return all, true, nil
		}
	}
}

func unixTime(seconds int64) *time.Time {
	if seconds <= 0 {
		return nil
	}
	value := time.Unix(seconds, 0).UTC()
	return &value
}

func ragflowUpdateTime(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	seconds := value
	switch {
	case value < 100_000_000_000:
	case value < 100_000_000_000_000:
		seconds = value / 1000
	case value < 100_000_000_000_000_000:
		seconds = value / 1_000_000
	default:
		seconds = value / 1_000_000_000
	}
	valueTime := time.Unix(seconds, 0).UTC()
	if valueTime.Year() < 0 || valueTime.Year() > 9999 {
		return nil
	}
	return &valueTime
}

func (s *Service) planExistingResource(ctx context.Context, setting *model.ResourceSyncSetting, resourceType, externalID string, payload map[string]interface{}) (localHash, action, conflictType, localID, tenantID string, planErr error) {
	action = model.SyncActionCreate
	conflictType = model.SyncConflictNone
	scope := ResourceSyncGlobalTenant
	binding, err := s.Store.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, resourceType, scope, externalID)
	if err != nil {
		return "", "", "", "", "", err
	}
	if binding != nil {
		localID = binding.LocalID
		tenantID = binding.TenantID
	}
	if binding == nil {
		existingLocalID, existingTenantID, found, err := s.findExistingShadowForScope(ctx, resourceType, externalID)
		if err != nil {
			return "", "", "", "", "", err
		}
		if found {
			localHash := s.currentLocalHash(ctx, resourceType, existingTenantID, existingLocalID)
			return localHash, model.SyncActionRelink, model.SyncConflictIdentity, existingLocalID, existingTenantID, nil
		}
		tenantID, err = s.tenantMappingTarget(ctx, scope, setting)
		if err != nil {
			return "", model.SyncActionConflict, model.SyncConflictMapping, "", "", nil
		}
		if tenantID == "" {
			return "", model.SyncActionConflict, model.SyncConflictMapping, "", "", nil
		}
		localID = externalID
	}
	currentLocalHash := s.currentLocalHash(ctx, resourceType, tenantID, localID)
	if binding != nil && currentLocalHash == "" {
		return "", model.SyncActionConflict, model.SyncConflictLocalMissing, localID, tenantID, nil
	}
	if binding != nil && currentLocalHash != "" {
		upstreamHash := resourceUpstreamHash(resourceType, payload)
		upstreamChanged := binding.LastSyncedUpstreamHash != upstreamHash
		localChanged := currentLocalHash != binding.LastSyncedLocalHash
		if upstreamChanged && localChanged {
			return currentLocalHash, model.SyncActionConflict, model.SyncConflictContent, localID, tenantID, nil
		}
		if !upstreamChanged && localChanged {
			return currentLocalHash, model.SyncActionSkip, model.SyncConflictNone, localID, tenantID, nil
		}
		if upstreamChanged {
			return currentLocalHash, model.SyncActionUpdate, model.SyncConflictNone, localID, tenantID, nil
		}
		return currentLocalHash, model.SyncActionSkip, model.SyncConflictNone, localID, tenantID, nil
	}
	return currentLocalHash, action, conflictType, localID, tenantID, nil
}

func (s *Service) findExistingShadowForScope(ctx context.Context, resourceType, externalID string) (string, string, bool, error) {
	switch resourceType {
	case model.SyncItemTypeDataset:
		resource, err := s.Store.GetRAGFlowDatasetLinkForScope(ctx, true, nil, externalID)
		if err != nil {
			return "", "", false, err
		}
		if resource != nil {
			return resource.ID, resource.TenantID, true, nil
		}
	case model.SyncItemTypeChat:
		resource, err := s.Store.GetChatShadowForScope(ctx, true, nil, externalID)
		if err != nil {
			return "", "", false, err
		}
		if resource != nil {
			return resource.ID, resource.TenantID, true, nil
		}
	case model.SyncItemTypeAgent:
		resource, err := s.Store.GetAgentShadowForScope(ctx, true, nil, externalID)
		if err != nil {
			return "", "", false, err
		}
		if resource != nil {
			return resource.ID, resource.TenantID, true, nil
		}
	case model.SyncItemTypeSearchApp:
		resource, err := s.Store.GetSearchAppShadowForScope(ctx, true, nil, externalID)
		if err != nil {
			return "", "", false, err
		}
		if resource != nil {
			return resource.ID, resource.TenantID, true, nil
		}
	case model.SyncItemTypeMemory:
		resource, err := s.Store.GetMemoryShadow(ctx, "", externalID, true)
		if err != nil {
			return "", "", false, err
		}
		if resource != nil {
			return resource.ID, resource.TenantID, true, nil
		}
	}
	return "", "", false, nil
}

func (s *Service) currentLocalHash(ctx context.Context, resourceType, tenantID, localID string) string {
	return s.currentLocalHashFrom(ctx, s.Store, resourceType, tenantID, localID)
}

func (s *Service) currentLocalHashFrom(ctx context.Context, store repository.Store, resourceType, tenantID, localID string) string {
	if tenantID == "" || localID == "" {
		return ""
	}
	var parts map[string]interface{}
	switch resourceType {
	case model.SyncItemTypeDataset:
		resource, err := store.GetDatasetLink(ctx, tenantID, localID)
		if err != nil || resource == nil {
			return ""
		}
		parts = map[string]interface{}{
			"name": resource.Name, "document_count": resource.DocumentCount,
		}
	case model.SyncItemTypeChat:
		resource, err := store.GetChatShadow(ctx, tenantID, localID, true)
		if err != nil || resource == nil {
			return ""
		}
		parts = map[string]interface{}{
			"name": resource.Name, "status": resource.Status, "dataset_ids": resource.DatasetIDs,
		}
	case model.SyncItemTypeAgent:
		resource, err := store.GetAgentShadow(ctx, tenantID, localID, true)
		if err != nil || resource == nil {
			return ""
		}
		parts = map[string]interface{}{
			"title": resource.Title,
		}
	case model.SyncItemTypeSearchApp:
		resource, err := store.GetSearchAppShadow(ctx, tenantID, localID, true)
		if err != nil || resource == nil {
			return ""
		}
		parts = map[string]interface{}{
			"name": resource.Name, "status": resource.Status, "dataset_ids": resource.DatasetIDs,
		}
	case model.SyncItemTypeMemory:
		resource, err := store.GetMemoryShadow(ctx, tenantID, localID, true)
		if err != nil || resource == nil {
			return ""
		}
		parts = map[string]interface{}{
			"name": resource.Name, "memory_type": resource.MemoryType,
		}
	default:
		return ""
	}
	return resourceLocalHash(resourceType, parts)
}

func resourceItemSyncState(item model.SyncItem) string {
	if item.ConflictType == model.SyncConflictLocalMissing || item.ConflictType == model.SyncConflictIdentity || item.ConflictType == model.SyncConflictMapping {
		return model.ResourceSyncStateConflict
	}
	switch item.Action {
	case model.SyncActionCreate, model.SyncActionUpdate:
		return model.ResourceSyncStateUpstreamChanged
	case model.SyncActionMarkMissing:
		return model.ResourceSyncStateStale
	case model.SyncActionSkip:
		if item.LocalCurrentHash != "" && item.LocalCurrentHash != item.LocalLastSyncedHash {
			return model.ResourceSyncStateLocalChanged
		}
		return model.ResourceSyncStateSynced
	default:
		if item.ConflictType != model.SyncConflictNone {
			return model.ResourceSyncStateConflict
		}
		return model.ResourceSyncStateSynced
	}
}

func resourceBindingSyncState(binding model.ResourceBinding) string {
	if binding.BindingLifecycle == model.ResourceBindingLifecycleMissing {
		return model.ResourceSyncStateStale
	}
	if binding.ConflictType != model.SyncConflictNone {
		return model.ResourceSyncStateConflict
	}
	return model.ResourceSyncStateSynced
}

func (s *Service) AssignResourceSyncItems(ctx context.Context, runID string, request ResourceSyncItemAssignmentRequest, actorID, actorTenantID string) (*ResourceSyncItemAssignmentResult, error) {
	run, err := s.Store.GetSyncRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, httperr.NotFound("sync run not found")
	}
	if run.Status != model.SyncRunPlanned {
		return nil, httperr.New(409, 40900, "sync run is not assignable")
	}
	if len(request.ItemIDs) == 0 {
		return nil, httperr.BadRequest(40099, "at least one sync item is required")
	}
	targetTenantID := strings.TrimSpace(request.TenantID)
	scope, err := s.ResolveTenantScope(ctx, actorID, actorTenantID, TenantScopeValueSpecific, targetTenantID)
	if err != nil {
		return nil, err
	}
	if !scope.ContainsTenant(targetTenantID) {
		return nil, ErrForbidden
	}
	tenant, err := s.Store.GetTenant(ctx, targetTenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil || tenant.Status != model.TenantStatusActive {
		return nil, httperr.BadRequest(40099, "target tenant must be active")
	}
	ownerID := strings.TrimSpace(request.OwnerID)
	if ownerID != "" {
		owner, err := s.Store.GetUser(ctx, ownerID)
		if err != nil {
			return nil, err
		}
		if owner == nil || owner.Status != model.UserStatusActive {
			return nil, httperr.BadRequest(40099, "owner must be an active user")
		}
		if owner.TenantID != targetTenantID {
			return nil, httperr.BadRequest(40099, "owner must belong to target tenant")
		}
	}
	updated := 0
	now := time.Now().UTC()
	assigned := make([]string, 0, len(request.ItemIDs))
	for _, itemID := range request.ItemIDs {
		item, err := s.Store.GetSyncItem(ctx, itemID)
		if err != nil {
			return nil, err
		}
		if item == nil || item.RunID != run.ID || item.Action != model.SyncActionCreate || item.Status != model.SyncItemStatusPending || item.ConflictType != model.SyncConflictNone {
			continue
		}
		item.TenantID = targetTenantID
		item.WorkspaceID = targetTenantID
		item.OwnerID = ownerID
		item.UpdatedAt = now
		if err := s.Store.UpdateSyncItem(ctx, item); err != nil {
			return nil, err
		}
		assigned = append(assigned, item.ID)
		updated++
	}
	if updated == 0 {
		return nil, httperr.BadRequest(40099, "no assignable sync items were selected")
	}
	detail, _ := json.Marshal(map[string]any{
		"run_id": run.ID, "item_ids": assigned,
		"target_tenant_id": targetTenantID, "target_owner_id": ownerID,
	})
	if err := s.RecordAudit(ctx, &model.AuditLog{
		TenantID: targetTenantID, UserID: actorID, ActorTenantID: actorTenantID, TargetTenantID: targetTenantID,
		Action: "ragflow_sync.items.assigned", Resource: "ragflow-sync", ResourceID: run.ID,
		DetailJSON: string(detail), AuthorizationPermission: "manage:ragflow-sync",
	}); err != nil {
		return nil, err
	}
	return &ResourceSyncItemAssignmentResult{Updated: updated}, nil
}

func (s *Service) ScanResourceSync(ctx context.Context, createdBy string, requestedTypes []string) (*model.SyncRun, error) {
	setting, err := s.GetResourceSyncSetting(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("load ragflow sync setting: %w", err)
	}
	if !setting.Enabled {
		return nil, httperr.BadRequest(40099, "ragflow resource sync is disabled")
	}
	if err := s.recoverStaleSyncRuns(ctx); err != nil {
		return nil, fmt.Errorf("recover stale ragflow sync runs: %w", err)
	}
	active, err := s.Store.CountActiveSyncRuns(ctx, ResourceSyncSourceID)
	if err != nil {
		return nil, fmt.Errorf("count active ragflow sync runs: %w", err)
	}
	if active > 0 {
		return nil, httperr.New(409, 40900, "another ragflow sync run is active")
	}
	resourceTypes, err := normalizeResourceTypes(requestedTypes)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	run := &model.SyncRun{
		ID: id.New(), SourceID: ResourceSyncSourceID, SourceCredentialVersion: ResourceSyncCredentialVersion,
		TriggerType: model.SyncTriggerManualImport, Status: model.SyncRunScanning,
		ResourceTypesJSON: marshalStringSlice(resourceTypes),
		ScopeJSON:         `{"scope_type":"ALL_AUTHORIZED","tenant_ids":[]}`,
		ScanStartedAt:     &now, ScanConsistency: model.SyncConsistencyIncomplete,
		CreatedBy: createdBy, CreatedAt: now, UpdatedAt: now,
	}
	created, err := s.Store.CreateSyncRunIfSourceIdle(ctx, run)
	if err != nil {
		return nil, fmt.Errorf("create ragflow sync run: %w", err)
	}
	if !created {
		return nil, httperr.New(409, 40900, "another ragflow sync run is active")
	}
	scan, err := s.scanResourceTypes(ctx, resourceTypes, setting)
	if err != nil {
		run.Status = model.SyncRunFailed
		run.Error = fmt.Sprintf("scan ragflow resources: %v", err)
		run.FinishedAt = &now
		run.UpdatedAt = now
		_ = s.Store.UpdateSyncRun(ctx, run)
		return nil, fmt.Errorf("scan ragflow resources: %w", err)
	}
	if len(scan.items) > 0 {
		for index := range scan.items {
			scan.items[index].RunID = run.ID
		}
		if err := s.Store.CreateSyncItems(ctx, scan.items); err != nil {
			err = fmt.Errorf("create ragflow sync items: %w", err)
			run.Status = model.SyncRunFailed
			run.Error = err.Error()
			run.FinishedAt = &now
			run.UpdatedAt = now
			_ = s.Store.UpdateSyncRun(ctx, run)
			return nil, err
		}
	}
	snapshotJSON, _ := json.Marshal(scan.snapshot)
	artifact, err := s.createResourceSyncArtifact(ctx, s.Store, "sync_snapshot", "", json.RawMessage(snapshotJSON), run.ID, "", "")
	if err != nil {
		err = fmt.Errorf("create ragflow sync snapshot artifact: %w", err)
		run.Status = model.SyncRunFailed
		run.Error = err.Error()
		run.FinishedAt = &now
		run.UpdatedAt = now
		_ = s.Store.UpdateSyncRun(ctx, run)
		return nil, err
	}
	run.SourceSnapshotRef = artifactRef(artifact)
	planSummary := newResourceSyncSummary()
	for _, item := range scan.items {
		planSummary.add(item.ResourceType, item.Action)
		if item.ConflictType != model.SyncConflictNone {
			planSummary.add(item.ResourceType, item.ConflictType)
		}
	}
	run.Status = model.SyncRunPlanned
	run.ScanCompletedAt = &now
	run.ScanConsistency = model.SyncConsistencyPageScan
	run.DeletionSafe = false
	run.PlanSummaryJSON = planSummary.marshal()
	run.UpdatedAt = now
	if err := s.Store.UpdateSyncRun(ctx, run); err != nil {
		return nil, fmt.Errorf("update ragflow sync run after scan: %w", err)
	}
	return run, nil
}

func marshalStringSlice(values []string) string {
	raw, _ := json.Marshal(values)
	return string(raw)
}

func (s *Service) ImportResourceSync(ctx context.Context, runID, userID string, request ResourceSyncImportRequest) (*model.SyncRun, error) {
	run, err := s.Store.GetSyncRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, httperr.NotFound("sync run not found")
	}
	if run.Status != model.SyncRunPlanned {
		return nil, httperr.New(409, 40900, "sync run is not importable")
	}
	if request.Async {
		queued, err := s.EnqueueResourceSyncImport(ctx, run.ID, userID)
		if err != nil {
			return nil, err
		}
		if !queued {
			return nil, httperr.New(409, 40900, "sync import job already exists")
		}
		return run, nil
	}
	now := time.Now().UTC()
	claimed, err := s.Store.BeginSyncRun(ctx, run.ID, "ragflow-sync-worker", now)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, httperr.New(409, 40900, "sync run is not importable")
	}
	run.Status = model.SyncRunRunning
	run.StartedAt = &now
	run.UpdatedAt = now
	run.LeaseOwner = "ragflow-sync-worker"
	run.LeaseExpiresAt = ptrTime(now.Add(staleSyncRunTimeout))
	ctx, cancel := context.WithTimeout(ctx, resourceSyncRunExecutionTimeout)
	defer cancel()
	executedRun, err := s.executeSyncRun(ctx, run, userID)
	if err != nil {
		s.failSyncRun(ctx, run, err)
		return nil, err
	}
	return executedRun, nil
}

func (s *Service) ReconcileResourceSync(ctx context.Context, userID, triggerType string, requestedTypes []string) (*model.SyncRun, error) {
	if triggerType != model.SyncTriggerManualReconcile && triggerType != model.SyncTriggerScheduledReconcile {
		return nil, httperr.BadRequest(40099, "unsupported ragflow reconcile trigger")
	}
	setting, err := s.GetResourceSyncSetting(ctx, "")
	if err != nil {
		return nil, err
	}
	if !setting.Enabled {
		return nil, httperr.BadRequest(40099, "ragflow resource sync is disabled")
	}
	if err := s.recoverStaleSyncRuns(ctx); err != nil {
		return nil, err
	}
	active, err := s.Store.CountActiveSyncRuns(ctx, ResourceSyncSourceID)
	if err != nil {
		return nil, err
	}
	if active > 0 {
		return nil, httperr.New(409, 40900, "another ragflow sync run is active")
	}
	resourceTypes, err := normalizeResourceTypes(requestedTypes)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	run := &model.SyncRun{
		ID: id.New(), SourceID: ResourceSyncSourceID, SourceCredentialVersion: ResourceSyncCredentialVersion,
		TriggerType: triggerType, Status: model.SyncRunRunning,
		ResourceTypesJSON: marshalStringSlice(resourceTypes),
		ScopeJSON:         `{"scope_type":"ALL_AUTHORIZED","tenant_ids":[]}`,
		ScanStartedAt:     &now, ScanConsistency: model.SyncConsistencyIncomplete,
		StartedAt: &now, CreatedBy: userID, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.CreateSyncRun(ctx, run); err != nil {
		return nil, err
	}
	scan, err := s.scanResourceTypes(ctx, resourceTypes, setting)
	if err != nil {
		run.Status = model.SyncRunFailed
		run.Error = err.Error()
		run.FinishedAt = &now
		run.UpdatedAt = now
		_ = s.Store.UpdateSyncRun(ctx, run)
		return nil, err
	}
	if len(scan.items) > 0 {
		for index := range scan.items {
			scan.items[index].RunID = run.ID
		}
		if err := s.Store.CreateSyncItems(ctx, scan.items); err != nil {
			run.Status = model.SyncRunFailed
			run.Error = err.Error()
			run.FinishedAt = &now
			run.UpdatedAt = now
			_ = s.Store.UpdateSyncRun(ctx, run)
			return nil, err
		}
	}
	snapshotJSON, _ := json.Marshal(scan.snapshot)
	artifact, err := s.createResourceSyncArtifact(ctx, s.Store, "sync_snapshot", "", json.RawMessage(snapshotJSON), run.ID, "", "")
	if err != nil {
		run.Status = model.SyncRunFailed
		run.Error = err.Error()
		run.FinishedAt = &now
		run.UpdatedAt = now
		_ = s.Store.UpdateSyncRun(ctx, run)
		return nil, err
	}
	run.SourceSnapshotRef = artifactRef(artifact)
	planSummary := newResourceSyncSummary()
	for _, item := range scan.items {
		planSummary.add(item.ResourceType, item.Action)
		if item.ConflictType != model.SyncConflictNone {
			planSummary.add(item.ResourceType, item.ConflictType)
		}
	}
	run.ScanCompletedAt = &now
	run.ScanConsistency = model.SyncConsistencyPageScan
	if !scan.complete {
		run.ScanConsistency = model.SyncConsistencyIncomplete
	}
	run.DeletionSafe = run.ScanConsistency == model.SyncConsistencyPageScan
	run.PlanSummaryJSON = planSummary.marshal()
	run.LeaseOwner = "ragflow-sync-worker"
	run.LeaseExpiresAt = ptrTime(now.Add(staleSyncRunTimeout))
	run.UpdatedAt = now
	if err := s.Store.UpdateSyncRun(ctx, run); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, resourceSyncRunExecutionTimeout)
	defer cancel()
	executedRun, err := s.executeSyncRun(ctx, run, userID)
	if err != nil {
		s.failSyncRun(ctx, run, err)
		return nil, err
	}
	return executedRun, nil
}

func (s *Service) executeSyncRun(ctx context.Context, run *model.SyncRun, userID string) (*model.SyncRun, error) {
	var resourceTypes []string
	_ = json.Unmarshal([]byte(run.ResourceTypesJSON), &resourceTypes)
	bindings, err := s.Store.ListAllResourceBindings(ctx, ResourceSyncSourceID, resourceTypes)
	if err != nil {
		return nil, err
	}
	bindingsByKey := make(map[string]model.ResourceBinding, len(bindings))
	for _, binding := range bindings {
		bindingsByKey[resourceIdentityKey(binding.ResourceType, binding.ExternalScopeKey, binding.ExternalID)] = binding
	}
	var items []model.SyncItem
	for page := 1; ; page++ {
		pageItems, total, err := s.Store.ListSyncItems(ctx, run.ID, page, 200)
		if err != nil {
			return nil, err
		}
		items = append(items, pageItems...)
		if int64(len(items)) >= total || len(pageItems) == 0 {
			break
		}
		leaseNow := time.Now().UTC()
		if err := s.Store.TouchSyncRunLease(ctx, run.ID, "ragflow-sync-worker", leaseNow); err != nil {
			return nil, err
		}
	}
	seen := map[string]struct{}{}
	resultSummary := newResourceSyncSummary()
	now := time.Now().UTC()
	if err := s.Store.TouchSyncRunLease(ctx, run.ID, "ragflow-sync-worker", now); err != nil {
		return nil, err
	}
	snapshot, err := s.getRunSnapshot(ctx, run)
	if err != nil {
		return nil, err
	}
	// Phase 1: batch-create item_diff artifacts. Identical contents share one
	// row to honour the (artifact_type, content_hash) uniqueness contract, and
	// a single batched insert replaces the previous per-item write round trip.
	type diffPlan struct {
		resourceType string
		itemID       string
		content      map[string]interface{}
	}
	diffPlans := make(map[string]diffPlan, len(items))
	diffPlanOrder := make([]string, 0, len(items))
	diffArtifactRefs := make(map[string]string, len(items))
	for _, item := range items {
		if item.Action != model.SyncActionCreate && item.Action != model.SyncActionUpdate && item.Action != model.SyncActionConflict {
			continue
		}
		key := resourceIdentityKey(item.ResourceType, item.ExternalScopeKey, item.ExternalID)
		rawPayload, found := snapshot[key]
		if !found {
			continue
		}
		var diffPayload map[string]interface{}
		if err := json.Unmarshal(rawPayload, &diffPayload); err != nil {
			return nil, httperr.Internal("invalid upstream payload")
		}
		if _, planned := diffPlans[key]; !planned {
			diffPlanOrder = append(diffPlanOrder, key)
		}
		diffPlans[key] = diffPlan{
			resourceType: item.ResourceType,
			itemID:       item.ID,
			content: map[string]interface{}{
				"schema_version":   1,
				"action":           item.Action,
				"conflict_type":    item.ConflictType,
				"upstream_payload": resourcePersistedPayload(item.ResourceType, diffPayload),
				"hashes": map[string]string{
					"upstream_current":     item.UpstreamCurrentHash,
					"upstream_last_synced": item.UpstreamLastSyncedHash,
					"local_current":        item.LocalCurrentHash,
					"local_last_synced":    item.LocalLastSyncedHash,
				},
			},
		}
	}
	if len(diffPlans) > 0 {
		artifacts := make([]model.ResourceSyncArtifact, 0, len(diffPlans))
		lifecycles := make([]model.ResourceSyncArtifactLifecycle, 0, len(diffPlans))
		emitted := make(map[string]string, len(diffPlans))
		for _, key := range diffPlanOrder {
			plan := diffPlans[key]
			contentHash := canonicalJSONHash(plan.content)
			if existing, err := s.Store.GetResourceSyncArtifactByHash(ctx, "item_diff", contentHash); err != nil {
				return nil, err
			} else if existing != nil {
				diffArtifactRefs[key] = artifactRef(existing)
				continue
			}
			if prior, deduped := emitted[contentHash]; deduped {
				diffArtifactRefs[key] = prior
				continue
			}
			now := time.Now().UTC()
			contentJSON, err := json.Marshal(plan.content)
			if err != nil {
				return nil, err
			}
			artifact := model.ResourceSyncArtifact{
				ID: id.New(), ArtifactType: "item_diff", ResourceType: plan.resourceType,
				SourceID: ResourceSyncSourceID, SourceCredentialVersion: ResourceSyncCredentialVersion,
				SchemaVersion: 1, ContentHash: contentHash,
				ContentJSON: string(contentJSON),
				SyncRunID:   run.ID, SyncItemID: plan.itemID,
				RetainUntil: s.resourceSyncArtifactRetainUntil(now),
				CreatedAt:   now, UpdatedAt: now,
			}
			emitted[contentHash] = artifactRef(&artifact)
			diffArtifactRefs[key] = emitted[contentHash]
			artifacts = append(artifacts, artifact)
			lifecycles = append(lifecycles, model.ResourceSyncArtifactLifecycle{
				ArtifactID: artifact.ID, RetainUntil: artifact.RetainUntil,
				CreatedAt: now, UpdatedAt: now,
			})
		}
		if len(artifacts) > 0 {
			if err := s.Store.CreateResourceSyncArtifacts(ctx, artifacts, lifecycles); err != nil {
				return nil, err
			}
		}
	}
	// Progress is surfaced at run level so a long execution reports both
	// liveness (lease) and completion. The first touch publishes the total.
	totalItems := len(items)
	progressDone := 0
	progressFailed := 0
	touchProgress := func() {
		run.ProgressTotal = totalItems
		run.ProgressDone = progressDone
		run.ProgressFailed = progressFailed
		if err := s.Store.TouchSyncRunProgress(ctx, run.ID, "ragflow-sync-worker", totalItems, progressDone, progressFailed, time.Now().UTC()); err != nil {
			logger.Warn("sync run progress touch failed", "run_id", run.ID, "error", err)
		}
		obs.Get().SetResourceSyncRunProgress(run.SourceID, int64(totalItems), int64(progressDone), int64(progressFailed))
	}
	touchProgress()
	for _, item := range items {
		key := resourceIdentityKey(item.ResourceType, item.ExternalScopeKey, item.ExternalID)
		seen[key] = struct{}{}
		if ref, found := diffArtifactRefs[key]; found {
			item.PayloadDiffRef = ref
		}
		switch item.Action {
		case model.SyncActionCreate, model.SyncActionUpdate:
			var payload map[string]interface{}
			rawPayload, found := snapshot[key]
			if !found || json.Unmarshal(rawPayload, &payload) != nil {
				item.Status = model.SyncItemStatusFailed
				item.Error = "invalid upstream payload"
			} else if err := s.applyUpstreamResource(ctx, run.ID, item.ID, item.ResourceType, item.ExternalScopeKey, item.ExternalID, payload, item.TenantID, item.OwnerID, userID); err != nil {
				item.Status = model.SyncItemStatusFailed
				item.Error = err.Error()
			} else {
				item.Status = model.SyncItemStatusSucceeded
			}
			item.UpdatedAt = time.Now().UTC()
			if err := s.Store.UpdateSyncItem(ctx, &item); err != nil {
				return nil, err
			}
		default:
			if item.Action == model.SyncActionConflict {
				item.Status = model.SyncItemStatusSkipped
				item.UpdatedAt = time.Now().UTC()
				if err := s.Store.UpdateSyncItem(ctx, &item); err != nil {
					return nil, err
				}
				if item.ConflictType == model.SyncConflictIdentity || item.ConflictType == model.SyncConflictMapping || item.ConflictType == model.SyncConflictLocalMissing {
					if binding, found := bindingsByKey[key]; found && binding.BindingLifecycle == model.ResourceBindingLifecycleActive {
						binding.ConflictType = item.ConflictType
						binding.GovernanceState = model.ResourceGovernancePendingReview
						binding.UpdatedAt = now
						if err := s.Store.UpdateResourceBinding(ctx, &binding); err != nil {
							return nil, err
						}
					}
				}
			}
		}
		resultSummary.add(item.ResourceType, item.Action+"_"+item.Status)
		obs.Get().AddResourceSyncItems(run.SourceID, item.ResourceType, item.Status, 1)
		switch item.Status {
		case model.SyncItemStatusSucceeded, model.SyncItemStatusSkipped:
			progressDone++
		case model.SyncItemStatusFailed:
			progressFailed++
		}
		if (progressDone+progressFailed)%20 == 0 || progressDone+progressFailed == totalItems {
			touchProgress()
		}
	}
	touchProgress()
	if (run.TriggerType == model.SyncTriggerManualReconcile || run.TriggerType == model.SyncTriggerScheduledReconcile) &&
		run.DeletionSafe && run.ScanConsistency == model.SyncConsistencyPageScan {
		setting, err := s.GetResourceSyncSetting(ctx, "")
		if err != nil {
			return nil, err
		}
		for _, binding := range bindings {
			if binding.BindingLifecycle != model.ResourceBindingLifecycleActive {
				continue
			}
			if _, found := seen[resourceIdentityKey(binding.ResourceType, binding.ExternalScopeKey, binding.ExternalID)]; found {
				if binding.MissingConfirmations != 0 {
					if err := s.Store.ResetMissingConfirmations(ctx, binding.ID, now); err != nil {
						return nil, err
					}
				}
				continue
			}
			count, err := s.Store.IncrementMissingConfirmations(ctx, binding.ID, now)
			if err != nil {
				return nil, err
			}
			resultSummary.add(binding.ResourceType, "missing_pending")
			if count < setting.DeletionConfirmations {
				continue
			}
			binding.BindingLifecycle = model.ResourceBindingLifecycleMissing
			binding.GovernanceState = model.ResourceGovernancePendingReview
			binding.MissingConfirmations = count
			binding.ConflictType = model.SyncConflictIdentity
			binding.LastSyncedAt = &now
			binding.UpdatedAt = now
			if err := s.Store.UpdateResourceBinding(ctx, &binding); err != nil {
				return nil, err
			}
			resultSummary.add(binding.ResourceType, model.SyncActionMarkMissing)
		}
	}
	finishedAt := time.Now().UTC()
	hasFailure := false
	for _, resourceType := range resourceTypes {
		if resultSummary.count(resourceType, "create_failed") > 0 || resultSummary.count(resourceType, "update_failed") > 0 {
			hasFailure = true
			break
		}
	}
	if hasFailure {
		run.Status = model.SyncRunPartialFailed
	} else {
		run.Status = model.SyncRunSucceeded
	}
	run.ResultSummaryJSON = resultSummary.marshal()
	run.ProgressTotal = totalItems
	run.ProgressDone = progressDone
	run.ProgressFailed = progressFailed
	if run.ScanConsistency != model.SyncConsistencyIncomplete {
		run.ScanConsistency = model.SyncConsistencyPageScan
	}
	run.FinishedAt = &finishedAt
	run.UpdatedAt = finishedAt
	if err := s.Store.UpdateSyncRun(ctx, run); err != nil {
		return nil, err
	}
	obs.Get().ClearResourceSyncRunProgress(run.SourceID)
	return run, nil
}

func (s *Service) applyUpstreamResource(ctx context.Context, runID, itemID, resourceType, scope, externalID string, payload map[string]interface{}, targetTenantID, ownerID, userID string) error {
	now := time.Now().UTC()
	if targetTenantID == "" {
		setting, err := s.GetResourceSyncSetting(ctx, "")
		if err != nil {
			return err
		}
		targetTenantID, err = s.tenantMappingTarget(ctx, scope, setting)
		if err != nil {
			return err
		}
		if targetTenantID == "" {
			return httperr.New(409, 40900, "resource tenant mapping is missing")
		}
	}
	return s.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		binding, err := tx.GetResourceBindingByIdentity(ctx, ResourceSyncSourceID, resourceType, scope, externalID)
		if err != nil {
			return err
		}
		if binding == nil {
			localID, err := s.createResourceShadow(ctx, tx, resourceType, externalID, targetTenantID, ownerID, payload, now)
			if err != nil {
				return err
			}
			binding = &model.ResourceBinding{
				ID: id.New(), SourceID: ResourceSyncSourceID, ResourceType: resourceType,
				ExternalScopeKey: scope, ExternalID: externalID, ExternalTenantID: scope,
				LocalType: resourceType, LocalID: localID, TenantID: targetTenantID,
				ExternalIdentityVersion: 1, BindingLifecycle: model.ResourceBindingLifecycleActive,
				GovernanceState:     model.ResourceGovernancePendingReview,
				ActiveMutationRunID: runID, MutationFencingToken: now.UnixNano(),
				LastSeenAt: &now, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.UpdateResourceBinding(ctx, binding); err != nil {
				return err
			}
			localHash := s.currentLocalHashFrom(ctx, tx, resourceType, targetTenantID, localID)
			return s.createBindingVersion(ctx, tx, binding, resourceType, payload, runID, itemID, userID, localHash, now)
		}

		if err := s.updateResourceShadow(ctx, tx, binding.ResourceType, binding.TenantID, binding.LocalID, payload, now); err != nil {
			return err
		}
		localHash := s.currentLocalHashFrom(ctx, tx, binding.ResourceType, binding.TenantID, binding.LocalID)
		binding.ActiveMutationRunID = runID
		binding.MutationFencingToken = now.UnixNano()
		binding.LastSeenAt = &now
		binding.UpdatedAt = now
		if err := tx.UpdateResourceBinding(ctx, binding); err != nil {
			return err
		}
		return s.createBindingVersion(ctx, tx, binding, binding.ResourceType, payload, runID, itemID, userID, localHash, now)
	})
}

func (s *Service) createResourceShadow(ctx context.Context, store repository.Store, resourceType, externalID, tenantID, ownerID string, payload map[string]interface{}, now time.Time) (string, error) {
	switch resourceType {
	case model.SyncItemTypeDataset:
		name, _ := payload["name"].(string)
		count, _ := payload["document_count"].(float64)
		existing, err := store.GetDatasetLinkByName(ctx, tenantID, name, externalID)
		if err != nil {
			return "", err
		}
		if existing != nil {
			return "", displayNameConflict("dataset")
		}
		dataset := &model.DatasetLink{
			ID: externalID, TenantID: tenantID, RAGFlowDatasetID: externalID, Name: name,
			DocumentCount: int64(count), SourceType: "ragflow_import", ReviewStatus: "none",
			OwnerID: ownerID, CreatedAt: now, UpdatedAt: now,
		}
		if err := store.UpsertDatasetLink(ctx, dataset); err != nil {
			return "", err
		}
		return dataset.ID, nil
	case model.SyncItemTypeChat:
		name, _ := payload["name"].(string)
		datasetIDs := resourceStringSlice(payload["dataset_ids"])
		chat := &model.ChatShadow{
			ID: externalID, TenantID: tenantID, Name: name, Status: "active",
			DatasetIDs: strings.Join(datasetIDs, ","), OwnerID: ownerID,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := store.UpsertChatShadow(ctx, chat); err != nil {
			return "", err
		}
		if err := s.upsertImportedAssistantCatalog(ctx, store, tenantID, model.AssistantKindChat, chat.ID, chat.Name, chat.OwnerID, now); err != nil {
			return "", err
		}
		return chat.ID, nil
	case model.SyncItemTypeAgent:
		title, _ := payload["title"].(string)
		existing, err := store.GetAgentShadowByTitle(ctx, tenantID, title, externalID)
		if err != nil {
			return "", err
		}
		if existing != nil {
			return "", displayNameConflict("agent")
		}
		agent := &model.AgentShadow{
			ID: externalID, TenantID: tenantID, Title: title, Status: "active",
			Release: false, OwnerID: ownerID, CreatedAt: now, UpdatedAt: now,
		}
		if err := store.UpsertAgentShadow(ctx, agent); err != nil {
			return "", err
		}
		if err := s.upsertImportedAssistantCatalog(ctx, store, tenantID, model.AssistantKindAgent, agent.ID, agent.Title, agent.OwnerID, now); err != nil {
			return "", err
		}
		return agent.ID, nil
	case model.SyncItemTypeSearchApp:
		name, _ := payload["name"].(string)
		app := &model.SearchAppShadow{
			ID: externalID, TenantID: tenantID, Name: name, Status: "active",
			OwnerID: ownerID, CreatedAt: now, UpdatedAt: now,
		}
		if err := store.UpsertSearchAppShadow(ctx, app); err != nil {
			return "", err
		}
		return app.ID, nil
	case model.SyncItemTypeMemory:
		name, _ := payload["name"].(string)
		memoryType, _ := payload["memory_type"].([]interface{})
		typeNames := make([]string, 0, len(memoryType))
		for _, value := range memoryType {
			if typed, ok := value.(string); ok {
				typeNames = append(typeNames, typed)
			}
		}
		memory := &model.MemoryShadow{
			ID: externalID, TenantID: tenantID, Name: name,
			MemoryType: strings.Join(typeNames, ","), OwnerID: ownerID,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := store.UpsertMemoryShadow(ctx, memory); err != nil {
			return "", err
		}
		return memory.ID, nil
	default:
		return "", httperr.BadRequest(40099, "unsupported resource type")
	}
}

func (s *Service) updateResourceShadow(ctx context.Context, store repository.Store, resourceType, tenantID, localID string, payload map[string]interface{}, now time.Time) error {
	switch resourceType {
	case model.SyncItemTypeDataset:
		dataset, err := store.GetDatasetLink(ctx, tenantID, localID)
		if err != nil || dataset == nil {
			return httperr.NotFound("dataset shadow not found")
		}
		dataset.Name, _ = payload["name"].(string)
		if existing, err := store.GetDatasetLinkByName(ctx, tenantID, dataset.Name, dataset.ID); err != nil {
			return err
		} else if existing != nil {
			return displayNameConflict("dataset")
		}
		if count, ok := payload["document_count"].(float64); ok {
			dataset.DocumentCount = int64(count)
		}
		dataset.UpdatedAt = now
		return store.UpsertDatasetLink(ctx, dataset)
	case model.SyncItemTypeChat:
		chat, err := store.GetChatShadow(ctx, tenantID, localID, true)
		if err != nil || chat == nil {
			return httperr.NotFound("chat shadow not found")
		}
		chat.Name, _ = payload["name"].(string)
		if datasetIDs := resourceStringSlice(payload["dataset_ids"]); datasetIDs != nil {
			chat.DatasetIDs = strings.Join(datasetIDs, ",")
		}
		if status, ok := payload["status"].(string); ok && status != "" {
			chat.Status = status
		}
		chat.UpdatedAt = now
		if err := store.UpsertChatShadow(ctx, chat); err != nil {
			return err
		}
		return s.upsertImportedAssistantCatalog(ctx, store, tenantID, model.AssistantKindChat, chat.ID, chat.Name, chat.OwnerID, now)
	case model.SyncItemTypeAgent:
		agent, err := store.GetAgentShadow(ctx, tenantID, localID, true)
		if err != nil || agent == nil {
			return httperr.NotFound("agent shadow not found")
		}
		agent.Title, _ = payload["title"].(string)
		if existing, err := store.GetAgentShadowByTitle(ctx, tenantID, agent.Title, agent.ID); err != nil {
			return err
		} else if existing != nil {
			return displayNameConflict("agent")
		}
		agent.UpdatedAt = now
		if err := store.UpsertAgentShadow(ctx, agent); err != nil {
			return err
		}
		return s.upsertImportedAssistantCatalog(ctx, store, tenantID, model.AssistantKindAgent, agent.ID, agent.Title, agent.OwnerID, now)
	case model.SyncItemTypeSearchApp:
		app, err := store.GetSearchAppShadow(ctx, tenantID, localID, true)
		if err != nil || app == nil {
			return httperr.NotFound("search app shadow not found")
		}
		app.Name, _ = payload["name"].(string)
		app.UpdatedAt = now
		return store.UpsertSearchAppShadow(ctx, app)
	case model.SyncItemTypeMemory:
		memory, err := store.GetMemoryShadow(ctx, tenantID, localID, true)
		if err != nil || memory == nil {
			return httperr.NotFound("memory shadow not found")
		}
		memory.Name, _ = payload["name"].(string)
		if types, ok := payload["memory_type"].([]interface{}); ok {
			typeNames := make([]string, 0, len(types))
			for _, value := range types {
				if typed, ok := value.(string); ok {
					typeNames = append(typeNames, typed)
				}
			}
			memory.MemoryType = strings.Join(typeNames, ",")
		}
		memory.UpdatedAt = now
		return store.UpsertMemoryShadow(ctx, memory)
	default:
		return httperr.BadRequest(40099, "unsupported resource type")
	}
}

func (s *Service) upsertImportedAssistantCatalog(ctx context.Context, store repository.Store, tenantID, kind, targetID, name, ownerID string, now time.Time) error {
	existing, err := store.GetAssistantCatalog(ctx, tenantID, kind, targetID)
	if err != nil {
		return err
	}
	if existing != nil {
		existing.Name = name
		existing.UpstreamStatus = "active"
		existing.OwnerID = ownerID
		existing.UpdatedAt = now
		return store.SyncAssistantCatalogBasicFields(ctx, existing)
	}
	capabilities := `["conversation"]`
	riskLevel := model.AssistantRiskLow
	flowReadiness := 1.0
	if kind == model.AssistantKindAgent {
		capabilities = `["workflow","conversation"]`
		riskLevel = model.AssistantRiskMedium
		flowReadiness = 0
	}
	item := &model.AssistantCatalog{
		ID: tenantID + ":" + kind + ":" + targetID, TenantID: tenantID, Kind: kind, TargetID: targetID,
		Name: name, UpstreamStatus: "active", GovernanceStatus: model.AssistantGovernanceDisabled,
		EffectiveStatus: model.AssistantEffectiveInactive, EffectiveStatusReason: model.AssistantStatusReasonGovernanceDisabled,
		OwnerID: ownerID, CategoriesJSON: "[]", CapabilitiesJSON: capabilities,
		IntentsJSON: "[]", KeywordsJSON: "[]", ExamplesJSON: "[]",
		RoutingWeight: 1, AssistantRiskLevel: riskLevel, CapabilityRiskJSON: "{}", WorkflowRiskJSON: "{}",
		WorkflowRiskUpperBound: riskLevel, Discoverable: false, AutoSelectEnabled: false,
		CatalogVersion: 1, RoutingReadiness: 0.25, AgentFlowReadiness: flowReadiness,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.UpsertAssistantCatalog(ctx, item); err != nil {
		return err
	}
	item.Discoverable = false
	return store.UpdateAssistantGovernanceFields(ctx, item)
}

func (s *Service) createBindingVersion(ctx context.Context, store repository.Store, binding *model.ResourceBinding, resourceType string, payload map[string]interface{}, runID, itemID, userID, localHash string, now time.Time) error {
	current, err := store.GetCurrentBindingVersion(ctx, binding.ID)
	if err != nil {
		return err
	}
	nextVersion := int64(1)
	if current != nil {
		nextVersion = current.Version + 1
	}
	artifact, err := s.createResourceSyncArtifact(ctx, store, "binding_payload", resourceType, resourcePersistedPayload(resourceType, payload), runID, itemID, "")
	if err != nil {
		return err
	}
	version := &model.ResourceBindingVersion{
		ID: id.New(), BindingID: binding.ID, Version: nextVersion,
		UpstreamHash:            resourceUpstreamHash(resourceType, payload),
		UpstreamPayloadRef:      artifactRef(artifact),
		UpstreamPayloadJSON:     "{}",
		SourceCredentialVersion: ResourceSyncCredentialVersion,
		ExternalVersion:         strconv.FormatInt(timeValue(payload["update_time"]), 10),
		SyncRunID:               runID, SyncItemID: itemID, CreatedBy: userID,
		CreatedAt: now, UpdatedAt: now,
	}
	return store.CreateBindingVersionWithPointer(ctx, version, version.UpstreamHash, localHash)
}

func timeValue(value interface{}) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	default:
		return 0
	}
}
