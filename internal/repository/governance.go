package repository

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

// GovernanceFilter controls asset and lifecycle list queries.
type GovernanceFilter struct {
	Key                string
	Name               string
	Status             string
	AppType            string
	ScenarioTemplateID string
	Scope              string
	ObjectID           string
	Active             *bool
	OwnerID            string
	OwnerTeamID        string
	ReviewStatus       string
	Search             string
}

// GovernanceRepo persists reusable governance assets with tenant isolation.
type GovernanceRepo interface {
	CreateScenarioTemplate(ctx context.Context, asset *model.ScenarioTemplateAsset, version *model.ScenarioTemplateVersion) error
	UpdateScenarioTemplate(ctx context.Context, asset *model.ScenarioTemplateAsset, version *model.ScenarioTemplateVersion) error
	GetScenarioTemplate(ctx context.Context, tenantID, id string) (*model.ScenarioTemplateAsset, error)
	GetScenarioTemplateByKey(ctx context.Context, tenantID, key string) (*model.ScenarioTemplateAsset, error)
	ListScenarioTemplates(ctx context.Context, tenantID string, page, pageSize int, filter GovernanceFilter) ([]model.ScenarioTemplateAsset, int64, error)
	ListAllScenarioTemplates(ctx context.Context, page, pageSize int, filter GovernanceFilter) ([]model.ScenarioTemplateAsset, int64, error)
	ListScenarioTemplateVersions(ctx context.Context, tenantID, templateID string) ([]model.ScenarioTemplateVersion, error)
	GetScenarioTemplateVersion(ctx context.Context, tenantID, templateID string, version int64) (*model.ScenarioTemplateVersion, error)
	GetScenarioTemplateMaxVersion(ctx context.Context, tenantID, templateID string) (int64, error)
	SetScenarioTemplateSource(ctx context.Context, tenantID, templateID, source string) error
	UpdateScenarioTemplateStatus(ctx context.Context, tenantID, templateID, status string) error
	CreatePromptPolicy(ctx context.Context, policy *model.PromptPolicyVersion) error
	GetPromptPolicy(ctx context.Context, tenantID, id string) (*model.PromptPolicyVersion, error)
	GetActivePromptPolicy(ctx context.Context, tenantID, scope, objectID string) (*model.PromptPolicyVersion, error)
	ListPromptPolicies(ctx context.Context, tenantID string, page, pageSize int, filter GovernanceFilter) ([]model.PromptPolicyVersion, int64, error)
	ListAllPromptPolicies(ctx context.Context, page, pageSize int, filter GovernanceFilter) ([]model.PromptPolicyVersion, int64, error)
	DeactivatePromptPolicies(ctx context.Context, tenantID, scope, objectID, exceptID string) error
	ActivatePromptPolicy(ctx context.Context, tenantID, id string) error
	RollbackPromptPolicy(ctx context.Context, tenantID, id string) (bool, error)
	DeletePromptPolicy(ctx context.Context, tenantID, id string) (bool, error)
	ListKnowledgeLifecycle(ctx context.Context, tenantID string, filter GovernanceFilter) ([]model.DatasetLink, error)
	UpdateDatasetLifecycle(ctx context.Context, dataset *model.DatasetLink) (bool, error)
	CreateEvalSetWithCases(ctx context.Context, evalSet *model.EvalSet, cases []model.EvalCase) error
	UpdateEvalSetWithCases(ctx context.Context, evalSet *model.EvalSet, cases []model.EvalCase) error
	GetEvalSet(ctx context.Context, tenantID, id string) (*model.EvalSet, error)
	ListEvalSets(ctx context.Context, tenantID string, page, pageSize int, filter GovernanceFilter) ([]model.EvalSet, int64, error)
	ListAllEvalSets(ctx context.Context, page, pageSize int, filter GovernanceFilter) ([]model.EvalSet, int64, error)
	DeleteEvalSet(ctx context.Context, tenantID, id string) (bool, error)
	ListEvalCases(ctx context.Context, tenantID, evalSetID string) ([]model.EvalCase, error)
	CreateEvalCases(ctx context.Context, tenantID string, cases []model.EvalCase) error
	LinkKnowledgeOpsEvalCase(ctx context.Context, tenantID, eventID, evalSetID, evalCaseID string) (bool, error)
}

func (s *store) CreateScenarioTemplate(ctx context.Context, asset *model.ScenarioTemplateAsset, version *model.ScenarioTemplateVersion) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(asset).Error; err != nil {
			return err
		}
		return tx.Create(version).Error
	})
}

func (s *store) UpdateScenarioTemplate(ctx context.Context, asset *model.ScenarioTemplateAsset, version *model.ScenarioTemplateVersion) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("tenant_id = ? AND id = ?", asset.TenantID, asset.ID).
			Select("name", "description", "latest_version", "status", "payload_json", "updated_at").
			Updates(asset)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Create(version).Error
	})
}

func (s *store) GetScenarioTemplate(ctx context.Context, tenantID, templateID string) (*model.ScenarioTemplateAsset, error) {
	var asset model.ScenarioTemplateAsset
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, templateID).First(&asset).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &asset, err
}

func (s *store) GetScenarioTemplateByKey(ctx context.Context, tenantID, key string) (*model.ScenarioTemplateAsset, error) {
	var asset model.ScenarioTemplateAsset
	err := s.WithContext(ctx).Where("tenant_id = ? AND \"key\" = ?", tenantID, key).First(&asset).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &asset, err
}

func (s *store) ListScenarioTemplates(ctx context.Context, tenantID string, page, pageSize int, filter GovernanceFilter) ([]model.ScenarioTemplateAsset, int64, error) {
	var list []model.ScenarioTemplateAsset
	var total int64
	q := s.WithContext(ctx).Model(&model.ScenarioTemplateAsset{})
	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if filter.Key != "" {
		q = q.Where("\"key\" LIKE ? ESCAPE '\\'", likePattern(filter.Key))
	}
	if filter.Name != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likePattern(filter.Name))
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("updated_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) ListAllScenarioTemplates(ctx context.Context, page, pageSize int, filter GovernanceFilter) ([]model.ScenarioTemplateAsset, int64, error) {
	return s.ListScenarioTemplates(ctx, "", page, pageSize, filter)
}

func (s *store) ListScenarioTemplateVersions(ctx context.Context, tenantID, templateID string) ([]model.ScenarioTemplateVersion, error) {
	var list []model.ScenarioTemplateVersion
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND template_id = ?", tenantID, templateID).
		Order("version DESC").Find(&list).Error
	return list, err
}

func (s *store) GetScenarioTemplateVersion(ctx context.Context, tenantID, templateID string, version int64) (*model.ScenarioTemplateVersion, error) {
	var item model.ScenarioTemplateVersion
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND template_id = ? AND version = ?", tenantID, templateID, version).
		First(&item).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &item, err
}

func (s *store) GetScenarioTemplateMaxVersion(ctx context.Context, tenantID, templateID string) (int64, error) {
	var maxVersion int64
	err := s.WithContext(ctx).Model(&model.ScenarioTemplateVersion{}).
		Where("tenant_id = ? AND template_id = ?", tenantID, templateID).
		Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error
	return maxVersion, err
}

func (s *store) SetScenarioTemplateSource(ctx context.Context, tenantID, templateID, source string) error {
	return s.WithContext(ctx).Model(&model.ScenarioTemplateAsset{}).
		Where("tenant_id = ? AND id = ?", tenantID, templateID).
		Update("source", source).Error
}

func (s *store) UpdateScenarioTemplateStatus(ctx context.Context, tenantID, templateID, status string) error {
	return s.WithContext(ctx).Model(&model.ScenarioTemplateAsset{}).
		Where("tenant_id = ? AND id = ?", tenantID, templateID).
		Updates(map[string]interface{}{"status": status, "updated_at": time.Now().UTC()}).Error
}

func (s *store) CreatePromptPolicy(ctx context.Context, policy *model.PromptPolicyVersion) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := tx.Model(&model.PromptPolicyVersion{}).
			Where("tenant_id = ? AND scope = ? AND object_id = ?", policy.TenantID, policy.Scope, policy.ObjectID)
		if policy.ID != "" {
			q = q.Where("id <> ?", policy.ID)
		}
		if err := q.Updates(map[string]interface{}{"active": false}).Error; err != nil {
			return err
		}
		policy.Active = true
		return tx.Create(policy).Error
	})
}

func (s *store) GetPromptPolicy(ctx context.Context, tenantID, id string) (*model.PromptPolicyVersion, error) {
	var policy model.PromptPolicyVersion
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&policy).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &policy, err
}

func (s *store) GetActivePromptPolicy(ctx context.Context, tenantID, scope, objectID string) (*model.PromptPolicyVersion, error) {
	var policy model.PromptPolicyVersion
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND scope = ? AND object_id = ? AND active = ?", tenantID, scope, objectID, true).
		First(&policy).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &policy, err
}

func (s *store) ListPromptPolicies(ctx context.Context, tenantID string, page, pageSize int, filter GovernanceFilter) ([]model.PromptPolicyVersion, int64, error) {
	var list []model.PromptPolicyVersion
	var total int64
	q := s.WithContext(ctx).Model(&model.PromptPolicyVersion{})
	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if filter.Scope != "" {
		q = q.Where("scope = ?", filter.Scope)
	}
	if filter.ObjectID != "" {
		q = q.Where("object_id = ?", filter.ObjectID)
	}
	if filter.Active != nil {
		q = q.Where("active = ?", *filter.Active)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("scope ASC, object_id ASC, version DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) ListAllPromptPolicies(ctx context.Context, page, pageSize int, filter GovernanceFilter) ([]model.PromptPolicyVersion, int64, error) {
	return s.ListPromptPolicies(ctx, "", page, pageSize, filter)
}

func (s *store) DeactivatePromptPolicies(ctx context.Context, tenantID, scope, objectID, exceptID string) error {
	q := s.WithContext(ctx).Model(&model.PromptPolicyVersion{}).
		Where("tenant_id = ? AND scope = ? AND object_id = ?", tenantID, scope, objectID)
	if exceptID != "" {
		q = q.Where("id <> ?", exceptID)
	}
	return q.Updates(map[string]interface{}{"active": false}).Error
}

func (s *store) ActivatePromptPolicy(ctx context.Context, tenantID, id string) error {
	return s.WithContext(ctx).Model(&model.PromptPolicyVersion{}).
		Where("tenant_id = ? AND id = ?", tenantID, id).
		Update("active", true).Error
}

func (s *store) RollbackPromptPolicy(ctx context.Context, tenantID, id string) (bool, error) {
	found := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var policy model.PromptPolicyVersion
		err := tx.Where("tenant_id = ? AND id = ?", tenantID, id).First(&policy).Error
		if err != nil {
			return err
		}
		if err := tx.Model(&model.PromptPolicyVersion{}).
			Where("tenant_id = ? AND scope = ? AND object_id = ?", tenantID, policy.Scope, policy.ObjectID).
			Update("active", false).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.PromptPolicyVersion{}).
			Where("tenant_id = ? AND id = ?", tenantID, id).
			Update("active", true).Error; err != nil {
			return err
		}
		found = true
		return nil
	})
	return found, err
}

func (s *store) DeletePromptPolicy(ctx context.Context, tenantID, id string) (bool, error) {
	res := s.WithContext(ctx).Where("tenant_id = ? AND id = ? AND active = ?", tenantID, id, false).
		Delete(&model.PromptPolicyVersion{})
	return res.RowsAffected > 0, res.Error
}

func (s *store) ListKnowledgeLifecycle(ctx context.Context, tenantID string, filter GovernanceFilter) ([]model.DatasetLink, error) {
	var list []model.DatasetLink
	q := s.WithContext(ctx).Where("tenant_id = ?", tenantID)
	if filter.OwnerID != "" {
		q = q.Where("owner_id = ?", filter.OwnerID)
	}
	if filter.OwnerTeamID != "" {
		q = q.Where("owner_team_id = ?", filter.OwnerTeamID)
	}
	if filter.ReviewStatus != "" {
		q = q.Where("review_status = ?", filter.ReviewStatus)
	}
	if filter.Search != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likePattern(filter.Search))
	}
	err := q.Order("updated_at DESC").Find(&list).Error
	return list, err
}

func (s *store) UpdateDatasetLifecycle(ctx context.Context, dataset *model.DatasetLink) (bool, error) {
	now := time.Now().UTC()
	dataset.UpdatedAt = now
	res := s.WithContext(ctx).Model(&model.DatasetLink{}).
		Where("tenant_id = ? AND id = ?", dataset.TenantID, dataset.ID).
		Updates(map[string]interface{}{
			"owner_id":         dataset.OwnerID,
			"owner_team_id":    dataset.OwnerTeamID,
			"source_type":      dataset.SourceType,
			"business_domain":  dataset.BusinessDomain,
			"sensitivity":      dataset.Sensitivity,
			"effective_at":     dataset.EffectiveAt,
			"expires_at":       dataset.ExpiresAt,
			"last_reviewed_at": dataset.LastReviewedAt,
			"review_status":    dataset.ReviewStatus,
			"quality_score":    dataset.QualityScore,
			"updated_at":       now,
		})
	return res.RowsAffected > 0, res.Error
}

func (s *store) CreateEvalSetWithCases(ctx context.Context, evalSet *model.EvalSet, cases []model.EvalCase) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(evalSet).Error; err != nil {
			return err
		}
		return createEvalCases(tx, cases)
	})
}

func (s *store) UpdateEvalSetWithCases(ctx context.Context, evalSet *model.EvalSet, cases []model.EvalCase) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("tenant_id = ? AND id = ?", evalSet.TenantID, evalSet.ID).
			Select("name", "description", "app_type", "app_id", "scenario_template_id", "dataset_ids", "version", "item_count", "status", "updated_at").
			Updates(evalSet)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Where("tenant_id = ? AND eval_set_id = ?", evalSet.TenantID, evalSet.ID).
			Delete(&model.EvalCase{}).Error; err != nil {
			return err
		}
		return createEvalCases(tx, cases)
	})
}

func (s *store) GetEvalSet(ctx context.Context, tenantID, id string) (*model.EvalSet, error) {
	var evalSet model.EvalSet
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&evalSet).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &evalSet, err
}

func (s *store) ListEvalSets(ctx context.Context, tenantID string, page, pageSize int, filter GovernanceFilter) ([]model.EvalSet, int64, error) {
	var list []model.EvalSet
	var total int64
	q := s.WithContext(ctx).Model(&model.EvalSet{})
	if tenantID != "" {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if filter.Name != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likePattern(filter.Name))
	}
	if filter.AppType != "" {
		q = q.Where("app_type = ?", filter.AppType)
	}
	if filter.ScenarioTemplateID != "" {
		q = q.Where("scenario_template_id = ?", filter.ScenarioTemplateID)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("updated_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) ListAllEvalSets(ctx context.Context, page, pageSize int, filter GovernanceFilter) ([]model.EvalSet, int64, error) {
	return s.ListEvalSets(ctx, "", page, pageSize, filter)
}

func (s *store) DeleteEvalSet(ctx context.Context, tenantID, id string) (bool, error) {
	var deleted bool
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("tenant_id = ? AND id = ?", tenantID, id).Delete(&model.EvalSet{})
		if res.Error != nil {
			return res.Error
		}
		deleted = res.RowsAffected > 0
		if !deleted {
			return nil
		}
		return tx.Where("tenant_id = ? AND eval_set_id = ?", tenantID, id).Delete(&model.EvalCase{}).Error
	})
	return deleted, err
}

func (s *store) ListEvalCases(ctx context.Context, tenantID, evalSetID string) ([]model.EvalCase, error) {
	var list []model.EvalCase
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND eval_set_id = ?", tenantID, evalSetID).
		Order("created_at ASC").Find(&list).Error
	return list, err
}

func (s *store) CreateEvalCases(ctx context.Context, tenantID string, cases []model.EvalCase) error {
	if len(cases) == 0 {
		return nil
	}
	for i := range cases {
		cases[i].TenantID = tenantID
	}
	return s.WithContext(ctx).Create(&cases).Error
}

func (s *store) LinkKnowledgeOpsEvalCase(ctx context.Context, tenantID, eventID, evalSetID, evalCaseID string) (bool, error) {
	res := s.WithContext(ctx).Model(&model.KnowledgeOpsEvent{}).
		Where("tenant_id = ? AND id = ?", tenantID, eventID).
		Updates(map[string]interface{}{"eval_set_id": evalSetID, "eval_case_id": evalCaseID, "updated_at": time.Now().UTC()})
	return res.RowsAffected > 0, res.Error
}

func createEvalCases(tx *gorm.DB, cases []model.EvalCase) error {
	if len(cases) == 0 {
		return nil
	}
	return tx.Create(&cases).Error
}
