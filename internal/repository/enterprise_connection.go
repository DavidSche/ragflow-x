package repository

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

// EnterpriseConnectionRepo persists logical connection identities, immutable
// versions and workspace binding versions. Current-version pointers are moved
// in the same transaction that creates the new immutable version.
type EnterpriseConnectionRepo interface {
	CreateEnterpriseConnection(ctx context.Context, connection *model.EnterpriseConnection, version *model.EnterpriseConnectionVersion) error
	CreateEnterpriseConnectionVersion(ctx context.Context, connection *model.EnterpriseConnection, version *model.EnterpriseConnectionVersion) error
	CreateEnterpriseConnectionDraftVersion(ctx context.Context, version *model.EnterpriseConnectionVersion) error
	ActivateEnterpriseConnectionVersionWithHealthCheck(ctx context.Context, connection *model.EnterpriseConnection, version *model.EnterpriseConnectionVersion, check *model.EnterpriseConnectionHealthCheck) error
	ListEnterpriseConnections(ctx context.Context, tenantIDs []string, providerName, lifecycleStatus string, page, pageSize int) ([]model.EnterpriseConnection, int64, error)
	ListEnterpriseConnectionsByCursor(ctx context.Context, tenantIDs []string, providerName, lifecycleStatus string, cursor *EnterpriseConnectionCursor, limit int) ([]model.EnterpriseConnection, bool, error)
	GetEnterpriseConnection(ctx context.Context, id string) (*model.EnterpriseConnection, error)
	GetEnterpriseConnectionVersion(ctx context.Context, connectionID string, version int64) (*model.EnterpriseConnectionVersion, error)
	ListEnterpriseConnectionVersions(ctx context.Context, connectionIDs []string) ([]model.EnterpriseConnectionVersion, error)
	ListEnterpriseBindingsByConnection(ctx context.Context, connectionID string) ([]model.EnterpriseConnectionBinding, error)
	RecordEnterpriseConnectionHealthCheck(ctx context.Context, check *model.EnterpriseConnectionHealthCheck, health string, checkedAt time.Time) error
	GetLatestEnterpriseConnectionHealthCheck(ctx context.Context, connectionID string) (*model.EnterpriseConnectionHealthCheck, error)
	CreateEnterpriseBinding(ctx context.Context, binding *model.EnterpriseConnectionBinding, version *model.EnterpriseConnectionBindingVersion) error
	CreateEnterpriseBindingVersion(ctx context.Context, binding *model.EnterpriseConnectionBinding, version *model.EnterpriseConnectionBindingVersion) error
	ListEnterpriseBindings(ctx context.Context, tenantIDs []string, connectionID, lifecycleStatus string, page, pageSize int) ([]model.EnterpriseConnectionBinding, int64, error)
	GetEnterpriseBinding(ctx context.Context, bindingID string) (*model.EnterpriseConnectionBinding, error)
	GetEnterpriseBindingVersion(ctx context.Context, bindingID string, version int64) (*model.EnterpriseConnectionBindingVersion, error)
	ListEnterpriseBindingVersions(ctx context.Context, bindingIDs []string) ([]model.EnterpriseConnectionBindingVersion, error)
	SetEnterpriseBindingLifecycle(ctx context.Context, bindingID, status string) error
	RetireEnterpriseConnection(ctx context.Context, connectionID string) (bool, error)
	SetEnterpriseConnectionLifecycle(ctx context.Context, connectionID, lifecycleStatus string) (bool, error)
}

// EnterpriseConnectionCursor is a stable keyset cursor. CreatedAt and ID are
// both included so ordering remains deterministic when timestamps collide.
type EnterpriseConnectionCursor struct {
	CreatedAt time.Time
	ID        string
}

func (s *store) CreateEnterpriseConnection(ctx context.Context, connection *model.EnterpriseConnection, version *model.EnterpriseConnectionVersion) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(connection).Error; err != nil {
			return err
		}
		return tx.Create(version).Error
	})
}

func (s *store) CreateEnterpriseConnectionVersion(ctx context.Context, connection *model.EnterpriseConnection, version *model.EnterpriseConnectionVersion) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(version).Error; err != nil {
			return err
		}
		result := tx.Model(&model.EnterpriseConnection{}).
			Where("id = ? AND current_connection_version = ?", connection.ID, connection.CurrentConnectionVersion).
			Updates(map[string]interface{}{
				"current_connection_version": version.Version,
				"managed_by":                 version.ManagedBy,
				"visibility":                 version.Visibility,
				"usable_by":                  version.UsableBy,
				"credential_scope":           version.CredentialScope,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrDuplicatedKey
		}
		return nil
	})
}

func (s *store) CreateEnterpriseConnectionDraftVersion(ctx context.Context, version *model.EnterpriseConnectionVersion) error {
	return s.WithContext(ctx).Create(version).Error
}

func (s *store) ActivateEnterpriseConnectionVersionWithHealthCheck(ctx context.Context, connection *model.EnterpriseConnection, version *model.EnterpriseConnectionVersion, check *model.EnterpriseConnectionHealthCheck) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.EnterpriseConnection{}).
			Where("id = ? AND current_connection_version = ?", connection.ID, connection.CurrentConnectionVersion).
			Updates(map[string]interface{}{
				"current_connection_version": version.Version,
				"managed_by":                 version.ManagedBy,
				"visibility":                 version.Visibility,
				"usable_by":                  version.UsableBy,
				"credential_scope":           version.CredentialScope,
				"runtime_health":             model.RuntimeHealthHealthy,
				"last_health_check_at":       check.CreatedAt,
				"updated_at":                 check.CreatedAt,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrDuplicatedKey
		}
		return tx.Create(check).Error
	})
}

func (s *store) ListEnterpriseConnections(ctx context.Context, tenantIDs []string, providerName, lifecycleStatus string, page, pageSize int) ([]model.EnterpriseConnection, int64, error) {
	var list []model.EnterpriseConnection
	var total int64
	q := s.WithContext(ctx).Model(&model.EnterpriseConnection{}).
		Joins("JOIN rgx_enterprise_connection_version ON rgx_enterprise_connection_version.connection_id = rgx_enterprise_connection.id AND rgx_enterprise_connection_version.version = rgx_enterprise_connection.current_connection_version")
	if len(tenantIDs) > 0 {
		q = q.Where("rgx_enterprise_connection.owner_tenant_id IN ?", tenantIDs)
	}
	if providerName != "" {
		q = q.Where("rgx_enterprise_connection_version.provider_name LIKE ? ESCAPE '\\'", likePattern(providerName))
	}
	if lifecycleStatus != "" {
		q = q.Where("rgx_enterprise_connection.lifecycle_status = ?", lifecycleStatus)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	if err := q.Order("rgx_enterprise_connection.created_at DESC").
		Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (s *store) ListEnterpriseConnectionsByCursor(ctx context.Context, tenantIDs []string, providerName, lifecycleStatus string, cursor *EnterpriseConnectionCursor, limit int) ([]model.EnterpriseConnection, bool, error) {
	if limit < 1 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	var list []model.EnterpriseConnection
	q := s.WithContext(ctx).Model(&model.EnterpriseConnection{}).
		Joins("JOIN rgx_enterprise_connection_version ON rgx_enterprise_connection_version.connection_id = rgx_enterprise_connection.id AND rgx_enterprise_connection_version.version = rgx_enterprise_connection.current_connection_version")
	if len(tenantIDs) > 0 {
		q = q.Where("rgx_enterprise_connection.owner_tenant_id IN ?", tenantIDs)
	}
	if providerName != "" {
		q = q.Where("rgx_enterprise_connection_version.provider_name LIKE ? ESCAPE '\\'", likePattern(providerName))
	}
	if lifecycleStatus != "" {
		q = q.Where("rgx_enterprise_connection.lifecycle_status = ?", lifecycleStatus)
	}
	if cursor != nil && !cursor.CreatedAt.IsZero() && cursor.ID != "" {
		q = q.Where(
			"(rgx_enterprise_connection.created_at < ? OR (rgx_enterprise_connection.created_at = ? AND rgx_enterprise_connection.id < ?))",
			cursor.CreatedAt, cursor.CreatedAt, cursor.ID,
		)
	}
	if err := q.
		Order("rgx_enterprise_connection.created_at DESC, rgx_enterprise_connection.id DESC").
		Limit(limit + 1).
		Find(&list).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(list) > limit
	if hasMore {
		list = list[:limit]
	}
	return list, hasMore, nil
}

func (s *store) GetEnterpriseConnection(ctx context.Context, id string) (*model.EnterpriseConnection, error) {
	var connection model.EnterpriseConnection
	if err := s.WithContext(ctx).Where("id = ?", id).First(&connection).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &connection, nil
}

func (s *store) GetEnterpriseConnectionVersion(ctx context.Context, connectionID string, version int64) (*model.EnterpriseConnectionVersion, error) {
	var row model.EnterpriseConnectionVersion
	err := s.WithContext(ctx).Where("connection_id = ? AND version = ?", connectionID, version).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *store) ListEnterpriseConnectionVersions(ctx context.Context, connectionIDs []string) ([]model.EnterpriseConnectionVersion, error) {
	if len(connectionIDs) == 0 {
		return nil, nil
	}
	var rows []model.EnterpriseConnectionVersion
	err := s.WithContext(ctx).Where("connection_id IN ?", connectionIDs).Find(&rows).Error
	return rows, err
}

func (s *store) ListEnterpriseBindingsByConnection(ctx context.Context, connectionID string) ([]model.EnterpriseConnectionBinding, error) {
	var list []model.EnterpriseConnectionBinding
	err := s.WithContext(ctx).Where("connection_id = ?", connectionID).
		Order("created_at DESC").Find(&list).Error
	return list, err
}

func (s *store) RecordEnterpriseConnectionHealthCheck(ctx context.Context, check *model.EnterpriseConnectionHealthCheck, health string, checkedAt time.Time) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(check).Error; err != nil {
			return err
		}
		return tx.Model(&model.EnterpriseConnection{}).
			Where("id = ?", check.ConnectionID).
			Updates(map[string]interface{}{
				"runtime_health":       health,
				"last_health_check_at": checkedAt,
				"updated_at":           checkedAt,
			}).Error
	})
}

func (s *store) GetLatestEnterpriseConnectionHealthCheck(ctx context.Context, connectionID string) (*model.EnterpriseConnectionHealthCheck, error) {
	var check model.EnterpriseConnectionHealthCheck
	err := s.WithContext(ctx).Where("connection_id = ?", connectionID).
		Order("created_at DESC").First(&check).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &check, nil
}

func (s *store) CreateEnterpriseBinding(ctx context.Context, binding *model.EnterpriseConnectionBinding, version *model.EnterpriseConnectionBindingVersion) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(binding).Error; err != nil {
			return err
		}
		return tx.Create(version).Error
	})
}

func (s *store) CreateEnterpriseBindingVersion(ctx context.Context, binding *model.EnterpriseConnectionBinding, version *model.EnterpriseConnectionBindingVersion) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(version).Error; err != nil {
			return err
		}
		result := tx.Model(&model.EnterpriseConnectionBinding{}).
			Where("binding_id = ? AND current_binding_version = ?", binding.BindingID, binding.CurrentBindingVersion).
			Update("current_binding_version", version.Version)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrDuplicatedKey
		}
		return nil
	})
}

func (s *store) ListEnterpriseBindings(ctx context.Context, tenantIDs []string, connectionID, lifecycleStatus string, page, pageSize int) ([]model.EnterpriseConnectionBinding, int64, error) {
	var list []model.EnterpriseConnectionBinding
	var total int64
	q := s.WithContext(ctx).Model(&model.EnterpriseConnectionBinding{})
	if len(tenantIDs) > 0 {
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	if connectionID != "" {
		q = q.Where("connection_id = ?", connectionID)
	}
	if lifecycleStatus != "" {
		q = q.Where("lifecycle_status = ?", lifecycleStatus)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	if err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

func (s *store) GetEnterpriseBinding(ctx context.Context, bindingID string) (*model.EnterpriseConnectionBinding, error) {
	var binding model.EnterpriseConnectionBinding
	if err := s.WithContext(ctx).Where("binding_id = ?", bindingID).First(&binding).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &binding, nil
}

func (s *store) GetEnterpriseBindingVersion(ctx context.Context, bindingID string, version int64) (*model.EnterpriseConnectionBindingVersion, error) {
	var row model.EnterpriseConnectionBindingVersion
	err := s.WithContext(ctx).Where("binding_id = ? AND version = ?", bindingID, version).First(&row).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *store) ListEnterpriseBindingVersions(ctx context.Context, bindingIDs []string) ([]model.EnterpriseConnectionBindingVersion, error) {
	if len(bindingIDs) == 0 {
		return nil, nil
	}
	var rows []model.EnterpriseConnectionBindingVersion
	err := s.WithContext(ctx).Where("binding_id IN ?", bindingIDs).Find(&rows).Error
	return rows, err
}

func (s *store) SetEnterpriseBindingLifecycle(ctx context.Context, bindingID, status string) error {
	return s.WithContext(ctx).Model(&model.EnterpriseConnectionBinding{}).
		Where("binding_id = ?", bindingID).
		Update("lifecycle_status", status).Error
}

func (s *store) RetireEnterpriseConnection(ctx context.Context, connectionID string) (bool, error) {
	retired := false
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var activeBindings int64
		if err := tx.Model(&model.EnterpriseConnectionBinding{}).
			Where("connection_id = ? AND lifecycle_status = ?", connectionID, model.EnterpriseLifecycleActive).
			Count(&activeBindings).Error; err != nil {
			return err
		}
		if activeBindings > 0 {
			return nil
		}
		var pinnedRoutes int64
		if err := tx.Table("rgx_model_route").
			Joins("JOIN rgx_model_route_enterprise_pin pin ON pin.tenant_id = rgx_model_route.tenant_id AND pin.route_id = rgx_model_route.id AND pin.pin_id = rgx_model_route.current_pin_id AND pin.version = rgx_model_route.current_pin_version").
			Where("pin.connection_id = ?", connectionID).
			Count(&pinnedRoutes).Error; err != nil {
			return err
		}
		if pinnedRoutes > 0 {
			return nil
		}
		result := tx.Model(&model.EnterpriseConnection{}).
			Where("id = ? AND lifecycle_status <> ?", connectionID, model.EnterpriseLifecycleRetired).
			Update("lifecycle_status", model.EnterpriseLifecycleRetired)
		if result.Error != nil {
			return result.Error
		}
		retired = result.RowsAffected == 1
		return nil
	})
	return retired, err
}

func (s *store) SetEnterpriseConnectionLifecycle(ctx context.Context, connectionID, lifecycleStatus string) (bool, error) {
	result := s.WithContext(ctx).Model(&model.EnterpriseConnection{}).
		Where("id = ? AND lifecycle_status <> ?", connectionID, lifecycleStatus).
		Update("lifecycle_status", lifecycleStatus)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected == 1, nil
}
