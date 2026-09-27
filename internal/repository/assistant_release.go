package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// AssistantReleaseRepo persists the canonical assistant release aggregate.
type AssistantReleaseRepo interface {
	CreateTemplateInstance(ctx context.Context, instance *model.TemplateInstance) error
	CreateScenarioPackVersion(ctx context.Context, version *model.ScenarioPackVersion) error
	GetTemplateInstance(ctx context.Context, tenantID, id string) (*model.TemplateInstance, error)
	GetTemplateInstanceByNameKey(ctx context.Context, tenantID, projectID, templateID, nameKey string) (*model.TemplateInstance, error)
	BindTemplateInstanceAssistant(ctx context.Context, tenantID, id, assistantID string) error
	ListTemplateInstances(ctx context.Context, tenantID, projectID, status string, page, pageSize int) ([]model.TemplateInstance, int64, error)
	UpdateTemplateInstanceState(ctx context.Context, tenantID, id, fromStatus, toStatus string) error
	CreateAssistant(ctx context.Context, assistant *model.Assistant) error
	GetAssistant(ctx context.Context, tenantID, id string) (*model.Assistant, error)
	GetAssistantForUpdate(ctx context.Context, tenantID, id string) (*model.Assistant, error)
	ListAssistants(ctx context.Context, tenantID, projectID, lifecycle string, page, pageSize int) ([]model.Assistant, int64, error)
	UpdateAssistantLifecycle(ctx context.Context, tenantID, id, fromStatus, toStatus string) error
	TransitionAssistantCurrentRelease(ctx context.Context, tenantID, assistantID, expectedReleaseID, targetReleaseID string) error
	CreateAssistantVersion(ctx context.Context, version *model.AssistantVersion) error
	GetAssistantVersion(ctx context.Context, tenantID, id string) (*model.AssistantVersion, error)
	GetAssistantVersionByNumber(ctx context.Context, tenantID, assistantID string, version int64) (*model.AssistantVersion, error)
	CreateAssistantRelease(ctx context.Context, release *model.AssistantRelease) error
	GetAssistantRelease(ctx context.Context, tenantID, id string) (*model.AssistantRelease, error)
	GetAssistantReleaseByNumber(ctx context.Context, tenantID, assistantID string, releaseVersion int64) (*model.AssistantRelease, error)
	ListAssistantReleases(ctx context.Context, tenantID, assistantID, state string, page, pageSize int) ([]model.AssistantRelease, int64, error)
	UpdateAssistantReleaseState(ctx context.Context, tenantID, id, fromState, toState string, optimisticVersion int64) error
	ReconcileAssistantRelease(ctx context.Context, release *model.AssistantRelease) error
	CreateCapabilityBindingVersion(ctx context.Context, binding *model.CapabilityBindingVersion) error
	ListCapabilityBindingVersions(ctx context.Context, tenantID, releaseID string) ([]model.CapabilityBindingVersion, error)
	CreateDatasetBindingVersion(ctx context.Context, binding *model.DatasetBindingVersion) error
	ListDatasetBindingVersions(ctx context.Context, tenantID, releaseID string) ([]model.DatasetBindingVersion, error)
	CreateModelRouteBindingVersion(ctx context.Context, binding *model.ModelRouteBindingVersion) error
	ListModelRouteBindingVersions(ctx context.Context, tenantID, releaseID string) ([]model.ModelRouteBindingVersion, error)
	CreateToolBindingVersion(ctx context.Context, binding *model.ToolBindingVersion) error
	ListToolBindingVersions(ctx context.Context, tenantID, releaseID string) ([]model.ToolBindingVersion, error)
	CreateRuntimeProfileSnapshot(ctx context.Context, snapshot *model.RuntimeProfileSnapshot) error
	GetRuntimeProfileSnapshot(ctx context.Context, tenantID, id string) (*model.RuntimeProfileSnapshot, error)
	CreatePolicySnapshot(ctx context.Context, snapshot *model.PolicySnapshot) error
	GetPolicySnapshot(ctx context.Context, tenantID, id string) (*model.PolicySnapshot, error)
	CreateSnapshotManifest(ctx context.Context, manifest *model.SnapshotManifest) error
	GetSnapshotManifest(ctx context.Context, tenantID, id string) (*model.SnapshotManifest, error)
	CreateReleaseOperation(ctx context.Context, operation *model.ReleaseOperation) error
	GetReleaseOperation(ctx context.Context, tenantID, id string) (*model.ReleaseOperation, error)
	GetReleaseOperationByIdempotencyKey(ctx context.Context, tenantID, key string) (*model.ReleaseOperation, error)
	ListReleaseOperations(ctx context.Context, tenantID, releaseID, operationType, state string, page, pageSize int) ([]model.ReleaseOperation, int64, error)
	UpdateReleaseOperation(ctx context.Context, operation *model.ReleaseOperation) error
	UpsertRuntimeHealth(ctx context.Context, health *model.RuntimeHealth) error
	GetRuntimeHealth(ctx context.Context, tenantID, releaseID string) (*model.RuntimeHealth, error)
	CreateDependencyHealthSignal(ctx context.Context, signal *model.DependencyHealthSignal) error
	ListDependencyHealthSignals(ctx context.Context, tenantID, releaseID string, page, pageSize int) ([]model.DependencyHealthSignal, int64, error)
	CreateRolloutPolicy(ctx context.Context, policy *model.RolloutPolicy) error
	GetActiveRolloutPolicy(ctx context.Context, tenantID, assistantID string) (*model.RolloutPolicy, error)
	GetRolloutPolicy(ctx context.Context, tenantID, id string) (*model.RolloutPolicy, error)
	ListRolloutPolicies(ctx context.Context, tenantID, assistantID string, page, pageSize int) ([]model.RolloutPolicy, int64, error)
	UpdateRolloutPolicy(ctx context.Context, policy *model.RolloutPolicy, expectedVersion int64) error
	CreateAuthorizationSnapshot(ctx context.Context, snapshot *model.AuthorizationSnapshot) error
	ListAuthorizationSnapshots(ctx context.Context, tenantID, releaseID, traceID string, page, pageSize int) ([]model.AuthorizationSnapshot, int64, error)
}

func firstAssistant[T any](ctx context.Context, db *gorm.DB, destination *T, query string, args ...interface{}) (*T, error) {
	err := db.WithContext(ctx).Where(query, args...).First(destination).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return destination, nil
}

func (s *store) CreateTemplateInstance(ctx context.Context, instance *model.TemplateInstance) error {
	return s.WithContext(ctx).Create(instance).Error
}

func (s *store) CreateScenarioPackVersion(ctx context.Context, version *model.ScenarioPackVersion) error {
	return s.WithContext(ctx).Create(version).Error
}

func (s *store) GetTemplateInstance(ctx context.Context, tenantID, id string) (*model.TemplateInstance, error) {
	return firstAssistant(ctx, s.DB, &model.TemplateInstance{}, "tenant_id = ? AND id = ?", tenantID, id)
}

// GetTemplateInstanceByNameKey resolves the instantiation replay anchor
// (doc/124 §2.2): the same tenant/project/template/name reuses one instance.
func (s *store) GetTemplateInstanceByNameKey(ctx context.Context, tenantID, projectID, templateID, nameKey string) (*model.TemplateInstance, error) {
	return firstAssistant(ctx, s.DB, &model.TemplateInstance{},
		"tenant_id = ? AND project_id = ? AND template_id = ? AND name_key = ?",
		tenantID, projectID, templateID, nameKey)
}

// BindTemplateInstanceAssistant links a pre-created instance projection to
// its assistant once the release saga provisions it (doc/124 §2.2).
func (s *store) BindTemplateInstanceAssistant(ctx context.Context, tenantID, id, assistantID string) error {
	result := s.WithContext(ctx).Model(&model.TemplateInstance{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Updates(map[string]interface{}{"assistant_id": assistantID, "updated_at": time.Now().UTC()})
	if result.Error == nil && result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return result.Error
}

func (s *store) ListTemplateInstances(ctx context.Context, tenantID, projectID, status string, page, pageSize int) ([]model.TemplateInstance, int64, error) {
	offset, limit := paginate(page, pageSize)
	query := s.WithContext(ctx).Model(&model.TemplateInstance{}).Where("tenant_id = ?", tenantID)
	if projectID != "" {
		query = query.Where("project_id = ?", projectID)
	}
	if status != "" {
		query = query.Where("governance_status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.TemplateInstance
	err := query.Order("updated_at DESC, id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (s *store) UpdateTemplateInstanceState(ctx context.Context, tenantID, id, fromStatus, toStatus string) error {
	result := s.WithContext(ctx).Model(&model.TemplateInstance{}).
		Where("tenant_id = ? AND id = ? AND governance_status = ?", tenantID, id, fromStatus).
		Updates(map[string]interface{}{"governance_status": toStatus, "updated_at": time.Now().UTC()})
	if result.Error == nil && result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return result.Error
}

func (s *store) CreateAssistant(ctx context.Context, assistant *model.Assistant) error {
	return s.WithContext(ctx).Create(assistant).Error
}

func (s *store) GetAssistant(ctx context.Context, tenantID, id string) (*model.Assistant, error) {
	return firstAssistant(ctx, s.DB, &model.Assistant{}, "tenant_id = ? AND id = ?", tenantID, id)
}

func (s *store) GetAssistantForUpdate(ctx context.Context, tenantID, id string) (*model.Assistant, error) {
	return firstAssistant(ctx, s.DB.Clauses(clause.Locking{Strength: "UPDATE"}), &model.Assistant{}, "tenant_id = ? AND id = ?", tenantID, id)
}

func (s *store) ListAssistants(ctx context.Context, tenantID, projectID, lifecycle string, page, pageSize int) ([]model.Assistant, int64, error) {
	offset, limit := paginate(page, pageSize)
	query := s.WithContext(ctx).Model(&model.Assistant{}).Where("tenant_id = ?", tenantID)
	if projectID != "" {
		query = query.Where("project_id = ?", projectID)
	}
	if lifecycle != "" {
		query = query.Where("lifecycle_status = ?", lifecycle)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.Assistant
	err := query.Order("updated_at DESC, id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (s *store) UpdateAssistantLifecycle(ctx context.Context, tenantID, id, fromStatus, toStatus string) error {
	result := s.WithContext(ctx).Model(&model.Assistant{}).
		Where("tenant_id = ? AND id = ? AND lifecycle_status = ?", tenantID, id, fromStatus).
		Updates(map[string]interface{}{"lifecycle_status": toStatus, "updated_at": time.Now().UTC()})
	if result.Error == nil && result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return result.Error
}

func (s *store) TransitionAssistantCurrentRelease(ctx context.Context, tenantID, assistantID, expectedReleaseID, targetReleaseID string) error {
	query := s.WithContext(ctx).Model(&model.Assistant{}).
		Where("tenant_id = ? AND id = ?", tenantID, assistantID)
	if expectedReleaseID != "" {
		query = query.Where("current_assistant_release_id = ?", expectedReleaseID)
	}
	result := query.Updates(map[string]interface{}{
		"current_assistant_release_id": targetReleaseID,
		"updated_at":                   time.Now().UTC(),
	})
	if result.Error == nil && result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return result.Error
}

func (s *store) CreateAssistantVersion(ctx context.Context, version *model.AssistantVersion) error {
	return s.WithContext(ctx).Create(version).Error
}

func (s *store) GetAssistantVersion(ctx context.Context, tenantID, id string) (*model.AssistantVersion, error) {
	return firstAssistant(ctx, s.DB, &model.AssistantVersion{}, "tenant_id = ? AND id = ?", tenantID, id)
}

func (s *store) GetAssistantVersionByNumber(ctx context.Context, tenantID, assistantID string, version int64) (*model.AssistantVersion, error) {
	return firstAssistant(ctx, s.DB, &model.AssistantVersion{}, "tenant_id = ? AND assistant_id = ? AND version = ?", tenantID, assistantID, version)
}

func (s *store) CreateAssistantRelease(ctx context.Context, release *model.AssistantRelease) error {
	return s.WithContext(ctx).Create(release).Error
}

func (s *store) GetAssistantRelease(ctx context.Context, tenantID, id string) (*model.AssistantRelease, error) {
	return firstAssistant(ctx, s.DB, &model.AssistantRelease{}, "tenant_id = ? AND id = ?", tenantID, id)
}

func (s *store) GetAssistantReleaseByNumber(ctx context.Context, tenantID, assistantID string, releaseVersion int64) (*model.AssistantRelease, error) {
	return firstAssistant(ctx, s.DB, &model.AssistantRelease{}, "tenant_id = ? AND assistant_id = ? AND release_version = ?", tenantID, assistantID, releaseVersion)
}

func (s *store) ListAssistantReleases(ctx context.Context, tenantID, assistantID, state string, page, pageSize int) ([]model.AssistantRelease, int64, error) {
	offset, limit := paginate(page, pageSize)
	query := s.WithContext(ctx).Model(&model.AssistantRelease{}).Where("tenant_id = ?", tenantID)
	if assistantID != "" {
		query = query.Where("assistant_id = ?", assistantID)
	}
	if state != "" {
		query = query.Where("release_state = ?", state)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.AssistantRelease
	err := query.Order("release_version DESC, id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (s *store) UpdateAssistantReleaseState(ctx context.Context, tenantID, id, fromState, toState string, optimisticVersion int64) error {
	query := s.WithContext(ctx).Model(&model.AssistantRelease{}).
		Where("tenant_id = ? AND id = ? AND release_state = ?", tenantID, id, fromState)
	if optimisticVersion > 0 {
		query = query.Where("optimistic_version = ?", optimisticVersion)
	}
	result := query.Updates(map[string]interface{}{
		"release_state":      toState,
		"optimistic_version": gorm.Expr("optimistic_version + 1"),
		"fencing_token":      gorm.Expr("fencing_token + 1"),
		"updated_at":         time.Now().UTC(),
	})
	if result.Error == nil && result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return result.Error
}

func (s *store) ReconcileAssistantRelease(ctx context.Context, release *model.AssistantRelease) error {
	return s.WithContext(ctx).Model(&model.AssistantRelease{}).
		Where("tenant_id = ? AND id = ?", release.TenantID, release.ID).
		Updates(map[string]interface{}{
			"actual_state_json":   release.ActualStateJSON,
			"actual_state_hash":   release.ActualStateHash,
			"actual_observed_at":  release.ActualObservedAt,
			"provider_version":    release.ProviderVersion,
			"provider_request_id": release.ProviderRequestID,
			"reconcile_status":    release.ReconcileStatus,
			"updated_at":          time.Now().UTC(),
		}).Error
}

func (s *store) CreateCapabilityBindingVersion(ctx context.Context, binding *model.CapabilityBindingVersion) error {
	return s.WithContext(ctx).Create(binding).Error
}

func (s *store) ListCapabilityBindingVersions(ctx context.Context, tenantID, releaseID string) ([]model.CapabilityBindingVersion, error) {
	var items []model.CapabilityBindingVersion
	err := s.WithContext(ctx).Where("tenant_id = ? AND assistant_release_id = ?", tenantID, releaseID).Order("binding_id, version").Find(&items).Error
	return items, err
}

func (s *store) CreateDatasetBindingVersion(ctx context.Context, binding *model.DatasetBindingVersion) error {
	return s.WithContext(ctx).Create(binding).Error
}

func (s *store) ListDatasetBindingVersions(ctx context.Context, tenantID, releaseID string) ([]model.DatasetBindingVersion, error) {
	var items []model.DatasetBindingVersion
	err := s.WithContext(ctx).Where("tenant_id = ? AND assistant_release_id = ?", tenantID, releaseID).Order("binding_id, version").Find(&items).Error
	return items, err
}

func (s *store) CreateModelRouteBindingVersion(ctx context.Context, binding *model.ModelRouteBindingVersion) error {
	return s.WithContext(ctx).Create(binding).Error
}

func (s *store) ListModelRouteBindingVersions(ctx context.Context, tenantID, releaseID string) ([]model.ModelRouteBindingVersion, error) {
	var items []model.ModelRouteBindingVersion
	err := s.WithContext(ctx).Where("tenant_id = ? AND assistant_release_id = ?", tenantID, releaseID).Order("binding_id, version").Find(&items).Error
	return items, err
}

func (s *store) CreateToolBindingVersion(ctx context.Context, binding *model.ToolBindingVersion) error {
	return s.WithContext(ctx).Create(binding).Error
}

func (s *store) ListToolBindingVersions(ctx context.Context, tenantID, releaseID string) ([]model.ToolBindingVersion, error) {
	var items []model.ToolBindingVersion
	err := s.WithContext(ctx).Where("tenant_id = ? AND assistant_release_id = ?", tenantID, releaseID).Order("binding_id, version").Find(&items).Error
	return items, err
}

func (s *store) CreateRuntimeProfileSnapshot(ctx context.Context, snapshot *model.RuntimeProfileSnapshot) error {
	return s.WithContext(ctx).Create(snapshot).Error
}

func (s *store) GetRuntimeProfileSnapshot(ctx context.Context, tenantID, id string) (*model.RuntimeProfileSnapshot, error) {
	return firstAssistant(ctx, s.DB, &model.RuntimeProfileSnapshot{}, "tenant_id = ? AND id = ?", tenantID, id)
}

func (s *store) CreatePolicySnapshot(ctx context.Context, snapshot *model.PolicySnapshot) error {
	return s.WithContext(ctx).Create(snapshot).Error
}

func (s *store) GetPolicySnapshot(ctx context.Context, tenantID, id string) (*model.PolicySnapshot, error) {
	return firstAssistant(ctx, s.DB, &model.PolicySnapshot{}, "tenant_id = ? AND id = ?", tenantID, id)
}

func (s *store) CreateSnapshotManifest(ctx context.Context, manifest *model.SnapshotManifest) error {
	return s.WithContext(ctx).Create(manifest).Error
}

func (s *store) GetSnapshotManifest(ctx context.Context, tenantID, id string) (*model.SnapshotManifest, error) {
	return firstAssistant(ctx, s.DB, &model.SnapshotManifest{}, "tenant_id = ? AND id = ?", tenantID, id)
}

func (s *store) CreateReleaseOperation(ctx context.Context, operation *model.ReleaseOperation) error {
	return s.WithContext(ctx).Create(operation).Error
}

func (s *store) GetReleaseOperation(ctx context.Context, tenantID, id string) (*model.ReleaseOperation, error) {
	return firstAssistant(ctx, s.DB, &model.ReleaseOperation{}, "tenant_id = ? AND id = ?", tenantID, id)
}

func (s *store) GetReleaseOperationByIdempotencyKey(ctx context.Context, tenantID, key string) (*model.ReleaseOperation, error) {
	return firstAssistant(ctx, s.DB, &model.ReleaseOperation{}, "tenant_id = ? AND idempotency_key = ?", tenantID, key)
}

func (s *store) ListReleaseOperations(ctx context.Context, tenantID, releaseID, operationType, state string, page, pageSize int) ([]model.ReleaseOperation, int64, error) {
	offset, limit := paginate(page, pageSize)
	query := s.WithContext(ctx).Model(&model.ReleaseOperation{}).Where("tenant_id = ?", tenantID)
	if releaseID != "" {
		query = query.Where("release_id = ? OR source_release_id = ? OR target_release_id = ?", releaseID, releaseID, releaseID)
	}
	if operationType != "" {
		query = query.Where("operation_type = ?", operationType)
	}
	if state != "" {
		query = query.Where("operation_state = ?", state)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.ReleaseOperation
	err := query.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (s *store) UpdateReleaseOperation(ctx context.Context, operation *model.ReleaseOperation) error {
	return s.WithContext(ctx).Model(&model.ReleaseOperation{}).
		Where("tenant_id = ? AND id = ?", operation.TenantID, operation.ID).
		Updates(map[string]interface{}{
			"attempt":                      operation.Attempt,
			"operation_state":              operation.OperationState,
			"current_step":                 operation.CurrentStep,
			"error_code":                   operation.ErrorCode,
			"error_message":                operation.ErrorMessage,
			"provider_operation_refs_json": operation.ProviderOperationRefsJSON,
			"started_at":                   operation.StartedAt,
			"finished_at":                  operation.FinishedAt,
			"updated_at":                   time.Now().UTC(),
		}).Error
}

func (s *store) UpsertRuntimeHealth(ctx context.Context, health *model.RuntimeHealth) error {
	existing, err := firstAssistant(ctx, s.DB, &model.RuntimeHealth{}, "tenant_id = ? AND assistant_release_id = ? AND binding_id = ?", health.TenantID, health.AssistantReleaseID, health.BindingID)
	if err != nil {
		return err
	}
	if existing == nil {
		return s.WithContext(ctx).Create(health).Error
	}
	health.ID = existing.ID
	health.CreatedAt = existing.CreatedAt
	return s.WithContext(ctx).Model(existing).Updates(health).Error
}

func (s *store) GetRuntimeHealth(ctx context.Context, tenantID, releaseID string) (*model.RuntimeHealth, error) {
	return firstAssistant(ctx, s.DB, &model.RuntimeHealth{}, "tenant_id = ? AND assistant_release_id = ?", tenantID, releaseID)
}

func (s *store) CreateDependencyHealthSignal(ctx context.Context, signal *model.DependencyHealthSignal) error {
	return s.WithContext(ctx).Create(signal).Error
}

func (s *store) ListDependencyHealthSignals(ctx context.Context, tenantID, releaseID string, page, pageSize int) ([]model.DependencyHealthSignal, int64, error) {
	offset, limit := paginate(page, pageSize)
	query := s.WithContext(ctx).Model(&model.DependencyHealthSignal{}).Where("tenant_id = ?", tenantID)
	if releaseID != "" {
		query = query.Where("assistant_release_id = ?", releaseID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.DependencyHealthSignal
	err := query.Order("observed_at DESC, id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (s *store) CreateRolloutPolicy(ctx context.Context, policy *model.RolloutPolicy) error {
	return s.WithContext(ctx).Create(policy).Error
}

func (s *store) GetActiveRolloutPolicy(ctx context.Context, tenantID, assistantID string) (*model.RolloutPolicy, error) {
	return firstAssistant(ctx, s.DB, &model.RolloutPolicy{}, "tenant_id = ? AND assistant_id = ? AND status IN ?", tenantID, assistantID, []string{model.RolloutStagePending, model.RolloutStageRunning})
}

func (s *store) GetRolloutPolicy(ctx context.Context, tenantID, id string) (*model.RolloutPolicy, error) {
	return firstAssistant(ctx, s.DB, &model.RolloutPolicy{}, "tenant_id = ? AND id = ?", tenantID, id)
}

// ListRolloutPolicies returns the assistant's policy history, newest first.
func (s *store) ListRolloutPolicies(ctx context.Context, tenantID, assistantID string, page, pageSize int) ([]model.RolloutPolicy, int64, error) {
	offset, limit := paginate(page, pageSize)
	query := s.WithContext(ctx).Model(&model.RolloutPolicy{}).
		Where("tenant_id = ? AND assistant_id = ?", tenantID, assistantID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.RolloutPolicy
	err := query.Order("created_at DESC, id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}

func (s *store) UpdateRolloutPolicy(ctx context.Context, policy *model.RolloutPolicy, expectedVersion int64) error {
	result := s.WithContext(ctx).Model(&model.RolloutPolicy{}).
		Where("tenant_id = ? AND id = ? AND optimistic_version = ?", policy.TenantID, policy.ID, expectedVersion).
		Updates(map[string]interface{}{
			"stage":              policy.Stage,
			"percentage":         policy.Percentage,
			"targeting_json":     policy.TargetingJSON,
			"start_at":           policy.StartAt,
			"end_at":             policy.EndAt,
			"rollback_condition": policy.RollbackCondition,
			"status":             policy.Status,
			"optimistic_version": expectedVersion + 1,
			"updated_at":         time.Now().UTC(),
		})
	if result.Error == nil && result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return result.Error
}

func (s *store) CreateAuthorizationSnapshot(ctx context.Context, snapshot *model.AuthorizationSnapshot) error {
	return s.WithContext(ctx).Create(snapshot).Error
}

func (s *store) ListAuthorizationSnapshots(ctx context.Context, tenantID, releaseID, traceID string, page, pageSize int) ([]model.AuthorizationSnapshot, int64, error) {
	offset, limit := paginate(page, pageSize)
	query := s.WithContext(ctx).Model(&model.AuthorizationSnapshot{}).Where("tenant_id = ?", tenantID)
	if releaseID != "" {
		query = query.Where("assistant_release_id = ?", releaseID)
	}
	if traceID != "" {
		query = query.Where("trace_id = ?", traceID)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.AuthorizationSnapshot
	err := query.Order("decision_time DESC, id DESC").Offset(offset).Limit(limit).Find(&items).Error
	return items, total, err
}
