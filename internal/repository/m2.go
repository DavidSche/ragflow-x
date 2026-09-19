package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RoleRepo persists RBAC roles and their permissions.
type RoleRepo interface {
	GetRole(ctx context.Context, id string) (*model.Role, error)
	GetRoleByName(ctx context.Context, name, excludeID string) (*model.Role, error)
	ListAllRoles(ctx context.Context, filter RoleFilter) ([]model.Role, error)
	GetPermissions(ctx context.Context, roleID string) ([]model.Permission, error)
	ListRolesByUser(ctx context.Context, userID string) ([]model.Role, error)
	AssignUserRole(ctx context.Context, userID, roleID string) error
	RemoveUserRoles(ctx context.Context, userID string) error
	RemoveUserRole(ctx context.Context, userID, roleID string) error
	SetUserPrimaryRole(ctx context.Context, userID, roleID string) error
	CreateRole(ctx context.Context, r *model.Role) error
	DeleteRole(ctx context.Context, roleID string) error
	SetPermissions(ctx context.Context, roleID string, perms []model.Permission) error
	UpdateRole(ctx context.Context, r *model.Role) error
	RoleUserCount(ctx context.Context, roleID string) (int64, error)
	RoleAsParentCount(ctx context.Context, roleID string) (int64, error)
}

// ModelProviderRepo persists unified LLM providers.
type ModelProviderRepo interface {
	CreateModelProvider(ctx context.Context, p *model.ModelProvider) error
	GetModelProvider(ctx context.Context, tenantID, id string) (*model.ModelProvider, error)
	ListModelProviders(ctx context.Context, tenantID string) ([]model.ModelProvider, error)
	UpdateModelProvider(ctx context.Context, p *model.ModelProvider) error
	DeleteModelProvider(ctx context.Context, tenantID, id string) error
	ListModelProvidersForScope(ctx context.Context, scopeAll bool, tenantIDs []string, filter ModelProviderFilter) ([]model.ModelProvider, error)
	CountModelProviderResourcesForScope(ctx context.Context, scopeAll bool, tenantIDs []string) (map[string]ModelProviderResourceCount, map[string]ModelProviderResourceCount, error)

	// Instances (named connection configurations under a provider).
	CreateModelProviderInstance(ctx context.Context, i *model.ModelProviderInstance) error
	GetModelProviderInstance(ctx context.Context, tenantID, providerID, id string) (*model.ModelProviderInstance, error)
	// CreateModelProviderInstanceDefault creates an instance and sets it as the
	// provider gateway default in one transaction, but only when the provider has
	// no default yet, so concurrent first-instance creates cannot race.
	CreateModelProviderInstanceDefault(ctx context.Context, i *model.ModelProviderInstance) error
	CreateModelProviderInstanceWithModels(ctx context.Context, i *model.ModelProviderInstance, models []*model.ModelProviderModel) error
	GetModelProviderInstanceByName(ctx context.Context, tenantID, providerID, name string) (*model.ModelProviderInstance, error)
	ListModelProviderInstances(ctx context.Context, tenantID, providerID string) ([]model.ModelProviderInstance, error)
	UpdateModelProviderInstance(ctx context.Context, i *model.ModelProviderInstance) error
	UpdateModelProviderInstanceWithModels(ctx context.Context, provider *model.ModelProvider, instance *model.ModelProviderInstance, models []*model.ModelProviderModel, removeIDs []string) error
	DeleteModelProviderInstancesWithModels(ctx context.Context, provider *model.ModelProvider, ids []string) error
	RestoreModelProviderInstancesWithModels(ctx context.Context, provider *model.ModelProvider, instances []*model.ModelProviderInstance, models []*model.ModelProviderModel) error
	DeleteModelProviderInstances(ctx context.Context, tenantID, providerID string, ids []string) error
	DeleteInstancesByProviderIDs(ctx context.Context, tenantID string, providerIDs []string) error
	ListModelProviderInstancesByTenant(ctx context.Context, tenantID string) ([]model.ModelProviderInstance, error)

	// Models configured on instances.
	CreateModelProviderModel(ctx context.Context, m *model.ModelProviderModel) error
	GetModelProviderModel(ctx context.Context, tenantID, instanceID, id string) (*model.ModelProviderModel, error)
	GetModelProviderModelByName(ctx context.Context, tenantID, providerID, instanceID, name string) (*model.ModelProviderModel, error)
	ListModelProviderModels(ctx context.Context, tenantID, instanceID string) ([]model.ModelProviderModel, error)
	UpdateModelProviderModel(ctx context.Context, m *model.ModelProviderModel) error
	DeleteModelProviderModels(ctx context.Context, tenantID, instanceID string, ids []string) error
	DeleteModelsByInstanceIDs(ctx context.Context, tenantID string, instanceIDs []string) error
	ListModelProviderModelsByInstanceIDs(ctx context.Context, tenantID string, instanceIDs []string) ([]model.ModelProviderModel, error)
}

// ModelProviderFilter carries governance list criteria.
type ModelProviderFilter struct {
	Name     string
	TenantID string
}

// ModelProviderResourceCount is an aggregate by provider id.
type ModelProviderResourceCount struct {
	ProviderID string
	TenantID   string
	Count      int64
}

// ModelRouteRepo persists model routing rules.
type ModelRouteRepo interface {
	CreateModelRoute(ctx context.Context, r *model.ModelRoute) error
	ListModelRoutes(ctx context.Context, tenantID string) ([]model.ModelRoute, error)
	ListModelRoutesByConnection(ctx context.Context, tenantIDs []string, connectionID string) ([]model.ModelRoute, error)
	ListRoutesByProvider(ctx context.Context, tenantID, providerID string) ([]model.ModelRoute, error)
	UpdateModelRoute(ctx context.Context, r *model.ModelRoute) error
	GetModelRoute(ctx context.Context, tenantID, id string) (*model.ModelRoute, error)
	DeleteModelRoute(ctx context.Context, tenantID, id string) error
}

// APIKeyRepo persists gateway API keys (hash only).
type APIKeyRepo interface {
	GetAPIKey(ctx context.Context, tenantID, id string) (*model.APIKey, error)
	CreateAPIKey(ctx context.Context, k *model.APIKey) error
	GetAPIKeyByHash(ctx context.Context, hash string) (*model.APIKey, error)
	ListAPIKeys(ctx context.Context, tenantID string) ([]model.APIKey, error)
	RevokeAPIKey(ctx context.Context, tenantID, id string) error
	TouchAPIKey(ctx context.Context, id string) error
}

// AuditRepo persists append-only audit records.
type AuditRepo interface {
	CreateAudit(ctx context.Context, log *model.AuditLog) error
	ListAudits(ctx context.Context, tenantID string, page, pageSize int, filter AuditFilter) ([]model.AuditLog, int64, error)
	ListAuditsForScope(ctx context.Context, scopeAll bool, tenantIDs []string, page, pageSize int, filter AuditFilter) ([]model.AuditLog, int64, error)
	LastAudit(ctx context.Context, tenantID string) (*model.AuditLog, error)
	ListAuditsAll(ctx context.Context, tenantID string) ([]model.AuditLog, error)
}

func (s *store) CountTenants(ctx context.Context) (int64, error) {
	var n int64
	err := s.WithContext(ctx).Model(&model.Tenant{}).Count(&n).Error
	return n, err
}

func (s *store) ListAllTenants(ctx context.Context) ([]model.Tenant, error) {
	var list []model.Tenant
	err := s.WithContext(ctx).Model(&model.Tenant{}).Order("created_at DESC").Find(&list).Error
	return list, err
}

func (s *store) DeleteTenant(ctx context.Context, tenantID string) error {
	return s.WithContext(ctx).Where("id = ?", tenantID).Delete(&model.Tenant{}).Error
}

func (s *store) ListUsersByTenant(ctx context.Context, tenantID string, page, pageSize int, filter UserFilter) ([]model.User, int64, error) {
	var users []model.User
	var total int64
	q := s.WithContext(ctx).Model(&model.User{}).Where("tenant_id = ?", tenantID)
	if filter.Username != "" {
		q = q.Where("username LIKE ? ESCAPE '\\'", likePattern(filter.Username))
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.Role != "" {
		q = q.Where("role = ?", filter.Role)
	}
	if t, ok := parseFilterDate(filter.CreatedFrom); ok {
		q = q.Where("created_at >= ?", t)
	}
	if t, ok := parseFilterDate(filter.CreatedTo); ok {
		if filter.CreatedTo != t.Format(time.RFC3339) {
			t = t.AddDate(0, 0, 1)
		}
		q = q.Where("created_at < ?", t)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&users).Error
	return users, total, err
}

// CountActiveUsersByTenantAndRole counts active users holding the exact
// primary role within a tenant, used to protect the last admin.
func (s *store) CountActiveUsersByTenantAndRole(ctx context.Context, tenantID, role string) (int64, error) {
	var n int64
	err := s.WithContext(ctx).Model(&model.User{}).Where("tenant_id = ? AND role = ? AND status = ?", tenantID, role, model.UserStatusActive).Count(&n).Error
	return n, err
}
func (s *store) UpdateUser(ctx context.Context, u *model.User) error {
	return s.WithContext(ctx).Model(&model.User{}).Where("id = ?", u.ID).Select("role", "status", "email", "username", "password_hash").Updates(u).Error
}

func (s *store) CountUsers(ctx context.Context) (int64, error) {
	var n int64
	err := s.WithContext(ctx).Model(&model.User{}).Count(&n).Error
	return n, err
}

func (s *store) CountUsersByTenant(ctx context.Context, tenantID string) (int64, error) {
	var n int64
	err := s.WithContext(ctx).Model(&model.User{}).Where("tenant_id = ?", tenantID).Count(&n).Error
	return n, err
}

func (s *store) DeleteUser(ctx context.Context, userID string) error {
	return s.WithContext(ctx).Where("id = ?", userID).Delete(&model.User{}).Error
}

func (s *store) GetRole(ctx context.Context, id string) (*model.Role, error) {
	var r model.Role
	err := s.WithContext(ctx).Where("id = ?", id).First(&r).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &r, err
}

func (s *store) GetRoleByName(ctx context.Context, name, excludeID string) (*model.Role, error) {
	var r model.Role
	q := s.WithContext(ctx).Where("LOWER(name) = LOWER(?)", name)
	if excludeID != "" {
		q = q.Where("id <> ?", excludeID)
	}
	err := q.First(&r).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &r, err
}

func (s *store) ListRolesByUser(ctx context.Context, userID string) ([]model.Role, error) {
	var roles []model.Role
	err := s.WithContext(ctx).Model(&model.Role{}).
		Joins("JOIN rgx_user_role ON rgx_user_role.role_id = rgx_role.id").
		Where("rgx_user_role.user_id = ?", userID).
		Order("rgx_role.id").Scan(&roles).Error
	return roles, err
}

func (s *store) AssignUserRole(ctx context.Context, userID, roleID string) error {
	var n int64
	if err := s.WithContext(ctx).Model(&model.UserRole{}).Where("user_id = ? AND role_id = ?", userID, roleID).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	return s.WithContext(ctx).Create(&model.UserRole{UserID: userID, RoleID: roleID}).Error
}

func (s *store) RemoveUserRole(ctx context.Context, userID, roleID string) error {
	return s.WithContext(ctx).Where("user_id = ? AND role_id = ?", userID, roleID).Delete(&model.UserRole{}).Error
}
func (s *store) RemoveUserRoles(ctx context.Context, userID string) error {
	return s.WithContext(ctx).Where("user_id = ?", userID).Delete(&model.UserRole{}).Error
}

// SetUserPrimaryRole atomically sets both the denormalized rgx_user.role
// marker and the rgx_user_role association in one transaction, so the two
// sources of truth can never diverge.
func (s *store) SetUserPrimaryRole(ctx context.Context, userID, roleID string) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.User{}).Where("id = ?", userID).Update("role", roleID).Error; err != nil {
			return err
		}
		if err := tx.Where("user_id = ?", userID).Delete(&model.UserRole{}).Error; err != nil {
			return err
		}
		return tx.Create(&model.UserRole{UserID: userID, RoleID: roleID}).Error
	})
}

func (s *store) CreateRole(ctx context.Context, r *model.Role) error {
	return s.WithContext(ctx).Create(r).Error
}

func (s *store) DeleteRole(ctx context.Context, roleID string) error {
	return s.WithContext(ctx).Where("id = ?", roleID).Delete(&model.Role{}).Error
}

func (s *store) SetPermissions(ctx context.Context, roleID string, perms []model.Permission) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", roleID).Delete(&model.Permission{}).Error; err != nil {
			return err
		}
		for i := range perms {
			perms[i].RoleID = roleID
			if perms[i].ID == "" {
				perms[i].ID = id.New()
			}
			if err := tx.Create(&perms[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *store) ListAllRoles(ctx context.Context, filter RoleFilter) ([]model.Role, error) {
	var roles []model.Role
	q := s.WithContext(ctx).Model(&model.Role{})
	if filter.Name != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likePattern(filter.Name))
	}
	if filter.Scope != "" {
		q = q.Where("scope = ?", filter.Scope)
	}
	err := q.Order("scope, id").Find(&roles).Error
	return roles, err
}

func (s *store) GetPermissions(ctx context.Context, roleID string) ([]model.Permission, error) {
	var perms []model.Permission
	err := s.WithContext(ctx).Where("role_id = ?", roleID).Find(&perms).Error
	return perms, err
}

func (s *store) CreateModelProvider(ctx context.Context, p *model.ModelProvider) error {
	return s.WithContext(ctx).Create(p).Error
}

func (s *store) GetModelProvider(ctx context.Context, tenantID, id string) (*model.ModelProvider, error) {
	var p model.ModelProvider
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&p).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &p, err
}

func (s *store) ListModelProviders(ctx context.Context, tenantID string) ([]model.ModelProvider, error) {
	var list []model.ModelProvider
	err := s.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&list).Error
	return list, err
}

func (s *store) ListModelProvidersForScope(ctx context.Context, scopeAll bool, tenantIDs []string, filter ModelProviderFilter) ([]model.ModelProvider, error) {
	var list []model.ModelProvider
	q := s.WithContext(ctx).Model(&model.ModelProvider{})
	if !scopeAll {
		if len(tenantIDs) == 0 {
			return list, nil
		}
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	if filter.TenantID != "" {
		q = q.Where("tenant_id = ?", filter.TenantID)
	}
	if filter.Name != "" {
		q = q.Where("(name LIKE ? ESCAPE '\\' OR provider_type LIKE ? ESCAPE '\\')",
			likePattern(filter.Name), likePattern(filter.Name))
	}
	err := q.Order("tenant_id, created_at DESC").Find(&list).Error
	return list, err
}

func (s *store) CountModelProviderResourcesForScope(ctx context.Context, scopeAll bool, tenantIDs []string) (map[string]ModelProviderResourceCount, map[string]ModelProviderResourceCount, error) {
	instances := map[string]ModelProviderResourceCount{}
	models := map[string]ModelProviderResourceCount{}
	base := s.WithContext(ctx).Model(&model.ModelProviderInstance{}).Select("provider_id, tenant_id, count(*) as count")
	if !scopeAll {
		if len(tenantIDs) == 0 {
			return instances, models, nil
		}
		base = base.Where("tenant_id IN ?", tenantIDs)
	}
	var instanceRows []ModelProviderResourceCount
	if err := base.Group("provider_id, tenant_id").Scan(&instanceRows).Error; err != nil {
		return nil, nil, err
	}
	for _, row := range instanceRows {
		instances[row.ProviderID] = row
	}

	modelBase := s.WithContext(ctx).Model(&model.ModelProviderModel{}).Select("provider_id, tenant_id, count(*) as count")
	if !scopeAll {
		modelBase = modelBase.Where("tenant_id IN ?", tenantIDs)
	}
	var modelRows []ModelProviderResourceCount
	if err := modelBase.Group("provider_id, tenant_id").Scan(&modelRows).Error; err != nil {
		return nil, nil, err
	}
	for _, row := range modelRows {
		models[row.ProviderID] = row
	}
	return instances, models, nil
}

func (s *store) UpdateModelProvider(ctx context.Context, p *model.ModelProvider) error {
	return s.WithContext(ctx).Model(&model.ModelProvider{}).Where("id = ? AND tenant_id = ?", p.ID, p.TenantID).
		Select("name", "base_url", "api_key_enc", "models_json", "enabled", "status").Updates(p).Error
}

func (s *store) DeleteModelProvider(ctx context.Context, tenantID, id string) error {
	// Cascade delete: models, then instances, then the provider row itself.
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id = ? AND provider_id = ?", tenantID, id).Delete(&model.ModelProviderModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ? AND provider_id = ?", tenantID, id).Delete(&model.ModelProviderInstance{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&model.ModelProvider{}).Error
	})
}

func (s *store) CreateModelRoute(ctx context.Context, r *model.ModelRoute) error {
	return s.WithContext(ctx).Create(r).Error
}

func (s *store) ListModelRoutes(ctx context.Context, tenantID string) ([]model.ModelRoute, error) {
	var list []model.ModelRoute
	err := s.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&list).Error
	return list, err
}

func (s *store) ListModelRoutesByConnection(ctx context.Context, tenantIDs []string, connectionID string) ([]model.ModelRoute, error) {
	var list []model.ModelRoute
	q := s.WithContext(ctx).
		Table("rgx_model_route").
		Joins("JOIN rgx_model_route_enterprise_pin pin ON pin.tenant_id = rgx_model_route.tenant_id AND pin.route_id = rgx_model_route.id AND pin.pin_id = rgx_model_route.current_pin_id AND pin.version = rgx_model_route.current_pin_version").
		Where("pin.connection_id = ?", connectionID)
	if len(tenantIDs) > 0 {
		q = q.Where("rgx_model_route.tenant_id IN ?", tenantIDs)
	}
	err := q.Order("rgx_model_route.created_at DESC").Find(&list).Error
	return list, err
}

func (s *store) ListRoutesByProvider(ctx context.Context, tenantID, providerID string) ([]model.ModelRoute, error) {
	var list []model.ModelRoute
	err := s.WithContext(ctx).Where("tenant_id = ? AND provider_id = ?", tenantID, providerID).Find(&list).Error
	return list, err
}

func (s *store) UpdateModelRoute(ctx context.Context, r *model.ModelRoute) error {
	return s.WithContext(ctx).Model(&model.ModelRoute{}).Where("id = ? AND tenant_id = ?", r.ID, r.TenantID).
		Select("scenario", "model_alias", "target_model", "enabled", "provider_id").Updates(r).Error
}

func (s *store) GetModelRoute(ctx context.Context, tenantID, id string) (*model.ModelRoute, error) {
	var r model.ModelRoute
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&r).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &r, err
}

func (s *store) DeleteModelRoute(ctx context.Context, tenantID, id string) error {
	return s.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&model.ModelRoute{}).Error
}

func (s *store) CreateAPIKey(ctx context.Context, k *model.APIKey) error {
	return s.WithContext(ctx).Create(k).Error
}

func (s *store) GetAPIKeyByHash(ctx context.Context, hash string) (*model.APIKey, error) {
	var k model.APIKey
	err := s.WithContext(ctx).Where("key_hash = ?", hash).First(&k).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &k, err
}

func (s *store) GetAPIKey(ctx context.Context, tenantID, id string) (*model.APIKey, error) {
	var k model.APIKey
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&k).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &k, err
}

func (s *store) ListAPIKeys(ctx context.Context, tenantID string) ([]model.APIKey, error) {
	var list []model.APIKey
	err := s.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&list).Error
	return list, err
}

func (s *store) RevokeAPIKey(ctx context.Context, tenantID, id string) error {
	return s.WithContext(ctx).Model(&model.APIKey{}).Where("id = ? AND tenant_id = ?", id, tenantID).
		Update("enabled", false).Error
}

func (s *store) TouchAPIKey(ctx context.Context, id string) error {
	now := time.Now().UTC()
	return s.WithContext(ctx).Model(&model.APIKey{}).Where("id = ?", id).
		Update("last_used_at", now).Error
}

// auditAppendMaxRetries bounds the retry loop that resolves cross-process
// races for a tenant's chain tail and transient database-lock contention.
const auditAppendMaxRetries = 5

// isRetryableAppendErr reports whether a failed append should be retried:
// unique-index losses (another writer took the tail slot) and transient
// database locks are both worth another attempt with fresh state.
func isRetryableAppendErr(err error) bool {
	if isDuplicateKeyErr(err) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLITE_BUSY") || // SQLite write lock
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "could not serialize") || // PostgreSQL serialization
		strings.Contains(msg, "deadlock detected")
}

// CreateAudit appends one record to the tenant's tamper-evident hash chain.
//
// Concurrency safety is layered:
//  1. An in-process per-tenant mutex serializes same-tenant writers here,
//     eliminating the historical read-tail-then-write race within one replica.
//  2. The append runs in a transaction that reads the chain tail (by seq) and
//     inserts with seq = tail.Seq + 1. The unique (tenant_id, seq) index makes
//     a forked chain impossible across processes: a racing writer's INSERT
//     violates the index, and it retries against the winner's committed row.
func (s *store) CreateAudit(ctx context.Context, log *model.AuditLog) error {
	// Stamp identity fields defensively so the row is insertable even when
	// callers bypass service.RecordAudit.
	if log.ID == "" {
		log.ID = id.New()
	}
	if log.At.IsZero() {
		log.At = time.Now().UTC()
	}

	unlock := s.lockTenant(log.TenantID)
	defer unlock()

	var lastErr error
	for attempt := 0; attempt < auditAppendMaxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff keeps racing writers from thundering on the
			// same tail slot (or a contended database write lock).
			delay := time.Duration(1<<uint(attempt-1)) * 5 * time.Millisecond
			if delay > 100*time.Millisecond {
				delay = 100 * time.Millisecond
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			last, err := lastAuditTx(tx, log.TenantID)
			if err != nil {
				return err
			}
			if last != nil {
				log.Seq = last.Seq + 1
				log.PrevHash = last.Hash
			} else {
				log.Seq = 1
				log.PrevHash = ""
			}
			log.Hash = model.AuditHash(log.PrevHash, log)
			return tx.Create(log).Error
		})
		if err == nil {
			return nil
		}
		if !isRetryableAppendErr(err) {
			return err
		}
		lastErr = err // tail slot lost or transient lock: re-read state and retry
	}
	return fmt.Errorf("append audit record after %d attempts (persistent chain contention): %w", auditAppendMaxRetries, lastErr)
}

// isDuplicateKeyErr reports whether err is a unique-constraint violation.
// GORM is opened without TranslateError, so dialect messages are matched too.
func isDuplicateKeyErr(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || // SQLite
		strings.Contains(msg, "duplicate key value") || // PostgreSQL
		strings.Contains(msg, "Duplicate entry") // MySQL
}

// lastAuditTx returns the current tail of the tenant's hash chain. Seq is
// gapless per tenant, so ordering by it is total — unlike (at, id), which two
// concurrent writers could tie on.
func lastAuditTx(tx *gorm.DB, tenantID string) (*model.AuditLog, error) {
	var last model.AuditLog
	err := tx.Where("tenant_id = ?", tenantID).Order("seq DESC").First(&last).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &last, nil
}

func (s *store) ListAudits(ctx context.Context, tenantID string, page, pageSize int, filter AuditFilter) ([]model.AuditLog, int64, error) {
	var list []model.AuditLog
	var total int64
	q := s.WithContext(ctx).Model(&model.AuditLog{}).Where("tenant_id = ?", tenantID)
	if filter.UserID != "" {
		q = q.Where("user_id = ?", filter.UserID)
	}
	if filter.Action != "" {
		q = q.Where("action LIKE ? ESCAPE '\\'", likePattern(filter.Action))
	}
	if filter.Resource != "" {
		q = q.Where("resource LIKE ? ESCAPE '\\'", likePattern(filter.Resource))
	}
	if filter.TargetTenant != "" {
		q = q.Where("target_tenant_id = ?", filter.TargetTenant)
	}
	if filter.Result != "" {
		q = q.Where("result = ?", filter.Result)
	}
	if filter.ApprovalID != "" {
		q = q.Where("approval_id = ?", filter.ApprovalID)
	}
	if filter.ActingContextID != "" {
		q = q.Where("acting_context_id = ?", filter.ActingContextID)
	}
	if t, ok := parseFilterDate(filter.CreatedFrom); ok {
		q = q.Where("at >= ?", t)
	}
	if t, ok := parseFilterDate(filter.CreatedTo); ok {
		if filter.CreatedTo != t.Format(time.RFC3339) {
			t = t.AddDate(0, 0, 1)
		}
		q = q.Where("at < ?", t)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) ListAuditsForScope(ctx context.Context, scopeAll bool, tenantIDs []string, page, pageSize int, filter AuditFilter) ([]model.AuditLog, int64, error) {
	var list []model.AuditLog
	var total int64
	q := s.WithContext(ctx).Model(&model.AuditLog{})
	if !scopeAll {
		if len(tenantIDs) == 0 {
			return list, 0, nil
		}
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	if filter.UserID != "" {
		q = q.Where("user_id = ?", filter.UserID)
	}
	if filter.Action != "" {
		q = q.Where("action LIKE ? ESCAPE '\\'", likePattern(filter.Action))
	}
	if filter.Resource != "" {
		q = q.Where("resource LIKE ? ESCAPE '\\'", likePattern(filter.Resource))
	}
	if filter.TargetTenant != "" {
		q = q.Where("target_tenant_id = ?", filter.TargetTenant)
	}
	if filter.Result != "" {
		q = q.Where("result = ?", filter.Result)
	}
	if filter.ApprovalID != "" {
		q = q.Where("approval_id = ?", filter.ApprovalID)
	}
	if filter.ActingContextID != "" {
		q = q.Where("acting_context_id = ?", filter.ActingContextID)
	}
	if t, ok := parseFilterDate(filter.CreatedFrom); ok {
		q = q.Where("at >= ?", t)
	}
	if t, ok := parseFilterDate(filter.CreatedTo); ok {
		if filter.CreatedTo != t.Format(time.RFC3339) {
			t = t.AddDate(0, 0, 1)
		}
		q = q.Where("at < ?", t)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) LastAudit(ctx context.Context, tenantID string) (*model.AuditLog, error) {
	return lastAuditTx(s.WithContext(ctx), tenantID)
}

func (s *store) ListAuditsAll(ctx context.Context, tenantID string) ([]model.AuditLog, error) {
	var list []model.AuditLog
	// Chain order is defined by seq; timestamp ties can no longer reorder it.
	err := s.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("seq ASC").Find(&list).Error
	return list, err
}

// TaskRepo persists operator-facing queues.
type TaskRepo interface {
	CreateTask(ctx context.Context, t *model.Task) error
	UpdateTask(ctx context.Context, t *model.Task) error
	ListTasks(ctx context.Context, tenantID string, page, pageSize int) ([]model.Task, int64, error)
	ListTasksByDataset(ctx context.Context, tenantID, datasetID string, page, pageSize int) ([]model.Task, int64, error)
	ListTasksByIDs(ctx context.Context, tenantID string, ids []string) ([]model.Task, error)
	DeleteTerminalTasks(ctx context.Context, tenantID string, ids []string) (int64, error)
	SyncParseTask(ctx context.Context, docID, status string, progress int, detail string) error
}

// QuotaRepo persists metered usage rows.
type QuotaRepo interface {
	UpsertUsage(ctx context.Context, u *model.QuotaUsage) error
	SummarizeUsage(ctx context.Context, tenantID string, date string) ([]model.QuotaUsage, error)
	RecordUsage(ctx context.Context, u *model.QuotaUsage) (bool, error)
	RecordCostMetric(ctx context.Context, m *model.CostMetric) (bool, error)
	ListCostMetrics(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter CostMetricFilter) ([]model.CostMetric, int64, error)
	SumCostMetricByChat(ctx context.Context, tenantID, chatID string) (*ChatUsageAgg, error)
	SummarizeUsageRange(ctx context.Context, tenantID string, scopeAll bool, dateFrom, dateTo string) ([]model.QuotaUsage, error)

	// Gateway quota preauthorization (doc/33 A1): per-key monthly token budget
	// with an atomic in-flight reservation guard and an idempotent reservation
	// ledger keyed by request_id.
	UpsertQuotaLimit(ctx context.Context, q *model.QuotaLimit) error
	GetQuotaLimit(ctx context.Context, keyID, periodStart string) (*model.QuotaLimit, error)
	AddQuotaReservation(ctx context.Context, r *model.QuotaReservation) (bool, error)
	ReserveQuotaTokens(ctx context.Context, keyID, periodStart, requestID string, est int64, reserveRequest bool) (bool, int64, int64, error)
	GetQuotaReservation(ctx context.Context, requestID string) (*model.QuotaReservation, error)
	FinalizeQuotaReservation(ctx context.Context, requestID string, actual int64) error
	ReleaseQuotaReservation(ctx context.Context, requestID string) error
	ReapStaleQuotaReservations(ctx context.Context, keyID, periodStart string, before time.Time) (int64, error)

	// Gateway idempotency is caller-facing, unlike MeterRequest which only
	// prevents duplicate usage accounting for the same execution request.
	CreateGatewayIdempotency(ctx context.Context, r *model.GatewayIdempotency) (bool, error)
	GetGatewayIdempotency(ctx context.Context, tenantID, principalID, idempotencyKey string) (*model.GatewayIdempotency, error)
	CompleteGatewayIdempotency(ctx context.Context, id, requestID, responseRef string) error
	FailGatewayIdempotency(ctx context.Context, id, requestID, responseRef string) error
}

func (s *store) CreateTask(ctx context.Context, t *model.Task) error {
	return s.WithContext(ctx).Create(t).Error
}

func (s *store) UpdateTask(ctx context.Context, t *model.Task) error {
	return s.WithContext(ctx).Model(&model.Task{}).Where("id = ?", t.ID).Select("status", "progress", "detail").Updates(t).Error
}

func (s *store) ListTasks(ctx context.Context, tenantID string, page, pageSize int) ([]model.Task, int64, error) {
	return s.listTasks(ctx, nil, tenantID, page, pageSize)
}

func (s *store) ListTasksByDataset(ctx context.Context, tenantID, datasetID string, page, pageSize int) ([]model.Task, int64, error) {
	return s.listTasks(ctx, &datasetID, tenantID, page, pageSize)
}

func (s *store) ListTasksByIDs(ctx context.Context, tenantID string, ids []string) ([]model.Task, error) {
	var list []model.Task
	err := s.WithContext(ctx).
		Where("tenant_id = ? AND id IN ?", tenantID, ids).
		Find(&list).Error
	return list, err
}

func (s *store) listTasks(ctx context.Context, datasetID *string, tenantID string, page, pageSize int) ([]model.Task, int64, error) {
	var list []model.Task
	var total int64
	q := s.WithContext(ctx).Model(&model.Task{}).Where("tenant_id = ?", tenantID)
	if datasetID != nil {
		q = q.Where("dataset_id = ?", *datasetID)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

func (s *store) SyncParseTask(ctx context.Context, docID, status string, progress int, detail string) error {
	return s.WithContext(ctx).Model(&model.Task{}).
		Where("task_type = ? AND doc_id = ? AND status IN ?", model.TaskTypeParse, docID, []string{model.TaskStatusQueued, model.TaskStatusRunning}).
		Updates(map[string]interface{}{"status": status, "progress": progress, "detail": detail, "updated_at": time.Now().UTC()}).Error
}

// DeleteTerminalTasks deletes selected task projections while preserving
// active rows. The tenant filter prevents cross-tenant deletion even if a
// caller submits ids collected from another tenant.
func (s *store) DeleteTerminalTasks(ctx context.Context, tenantID string, ids []string) (int64, error) {
	result := s.WithContext(ctx).
		Where("tenant_id = ? AND id IN ? AND status IN ?", tenantID, ids, []string{
			model.TaskStatusDone, model.TaskStatusFailed, model.TaskStatusStopped,
		}).
		Delete(&model.Task{})
	return result.RowsAffected, result.Error
}

// upsertUsageTx atomically increments the daily aggregate using a database
// upsert (ON CONFLICT DO UPDATE SET col = col + EXCLUDED.col), eliminating the
// read-modify-write race between concurrent requests.
func upsertUsageTx(tx *gorm.DB, u *model.QuotaUsage) error {
	if u.ID == "" {
		u.ID = id.New()
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "user_id"}, {Name: "key_id"}, {Name: "date"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"tokens_in":  gorm.Expr("rgx_quota_usage.tokens_in + EXCLUDED.tokens_in"),
			"tokens_out": gorm.Expr("rgx_quota_usage.tokens_out + EXCLUDED.tokens_out"),
			"requests":   gorm.Expr("rgx_quota_usage.requests + EXCLUDED.requests"),
			"cost":       gorm.Expr("rgx_quota_usage.cost + EXCLUDED.cost"),
			"updated_at": time.Now().UTC(),
		}),
	}).Create(u).Error
}

func (s *store) UpsertUsage(ctx context.Context, u *model.QuotaUsage) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return upsertUsageTx(tx, u)
	})
}

// RecordUsage meters a request idempotently: it first inserts into the
// rgx_meter_request ledger (only the first insert wins on the primary key), so
// re-delivering the same request_id never double-counts usage, then atomically
// bumps the daily aggregate.
func (s *store) RecordUsage(ctx context.Context, u *model.QuotaUsage) (bool, error) {
	if u.RequestID == "" {
		return false, errors.New("request_id is required for idempotent metering")
	}
	var metered bool
	err := s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.MeterRequest{
			RequestID: u.RequestID, TenantID: u.TenantID, UserID: u.UserID, KeyID: u.KeyID, Date: u.Date,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			metered = false // already recorded for this request_id
			return nil
		}
		if err := upsertUsageTx(tx, u); err != nil {
			return err
		}
		metered = true
		return nil
	})
	return metered, err
}

func (s *store) SummarizeUsage(ctx context.Context, tenantID, date string) ([]model.QuotaUsage, error) {
	var list []model.QuotaUsage
	q := s.WithContext(ctx).Model(&model.QuotaUsage{}).Where("tenant_id = ?", tenantID)
	if date != "" {
		q = q.Where("date = ?", date)
	}
	err := q.Order("date DESC, created_at DESC").Find(&list).Error
	return list, err
}

// SummarizeUsageRange returns usage rows for a tenant (or all tenants when
// scopeAll is set) within an optional date range. Used by usage reports.
func (s *store) SummarizeUsageRange(ctx context.Context, tenantID string, scopeAll bool, dateFrom, dateTo string) ([]model.QuotaUsage, error) {
	q := s.WithContext(ctx).Model(&model.QuotaUsage{})
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	}
	if dateFrom != "" {
		q = q.Where("date >= ?", dateFrom)
	}
	if dateTo != "" {
		q = q.Where("date <= ?", dateTo)
	}
	var list []model.QuotaUsage
	err := q.Order("date DESC").Find(&list).Error
	return list, err
}

func (s *store) CreateGatewayIdempotency(ctx context.Context, r *model.GatewayIdempotency) (bool, error) {
	if r.TenantID == "" || r.PrincipalID == "" || r.IdempotencyKey == "" || r.RequestFingerprint == "" {
		return false, errors.New("tenant, principal, idempotency key and fingerprint are required")
	}
	res := s.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(r)
	return res.RowsAffected > 0, res.Error
}

func (s *store) GetGatewayIdempotency(ctx context.Context, tenantID, principalID, idempotencyKey string) (*model.GatewayIdempotency, error) {
	var record model.GatewayIdempotency
	err := s.WithContext(ctx).Where(
		"tenant_id = ? AND principal_id = ? AND idempotency_key = ?",
		tenantID, principalID, idempotencyKey,
	).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &record, err
}

func (s *store) CompleteGatewayIdempotency(ctx context.Context, id, requestID, responseRef string) error {
	return s.WithContext(ctx).Model(&model.GatewayIdempotency{}).
		Where("id = ? AND request_id = ? AND status = ?", id, requestID, model.IdempotencyInProgress).
		Updates(map[string]interface{}{
			"status": model.IdempotencyCompleted, "response_ref": responseRef,
			"completed_at": time.Now().UTC(),
		}).Error
}

func (s *store) FailGatewayIdempotency(ctx context.Context, id, requestID, responseRef string) error {
	return s.WithContext(ctx).Model(&model.GatewayIdempotency{}).
		Where("id = ? AND request_id = ? AND status = ?", id, requestID, model.IdempotencyInProgress).
		Updates(map[string]interface{}{
			"status": model.IdempotencyFailed, "response_ref": responseRef,
			"completed_at": time.Now().UTC(),
		}).Error
}

func (s *store) UpdateRole(ctx context.Context, r *model.Role) error {
	return s.WithContext(ctx).Model(&model.Role{}).Where("id = ?", r.ID).Select("name", "description", "scope", "parent_id").Updates(r).Error
}

func (s *store) RoleUserCount(ctx context.Context, roleID string) (int64, error) {
	var n int64
	err := s.WithContext(ctx).Model(&model.UserRole{}).Where("role_id = ?", roleID).Count(&n).Error
	return n, err
}

func (s *store) RoleAsParentCount(ctx context.Context, roleID string) (int64, error) {
	var n int64
	err := s.WithContext(ctx).Model(&model.Role{}).Where("parent_id = ?", roleID).Count(&n).Error
	return n, err
}

// RefreshTokenRepo persists and revokes rotating refresh tokens.
type RefreshTokenRepo interface {
	CreateRefreshToken(ctx context.Context, t *model.RefreshToken) error
	FindRefreshTokenByHash(ctx context.Context, hash string) (*model.RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, id string) error
	// RevokeActiveRefreshToken atomically claims an unrevoked token. It returns
	// false when another refresh request has already claimed the token.
	RevokeActiveRefreshToken(ctx context.Context, id string, now time.Time) (bool, error)
}

func (s *store) CreateRefreshToken(ctx context.Context, t *model.RefreshToken) error {
	return s.WithContext(ctx).Create(t).Error
}

func (s *store) FindRefreshTokenByHash(ctx context.Context, hash string) (*model.RefreshToken, error) {
	var t model.RefreshToken
	err := s.WithContext(ctx).Where("token_hash = ?", hash).First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &t, err
}

func (s *store) RevokeRefreshToken(ctx context.Context, id string) error {
	now := time.Now()
	return s.WithContext(ctx).Model(&model.RefreshToken{}).Where("id = ?", id).Updates(map[string]interface{}{"revoked_at": now}).Error
}

func (s *store) RevokeActiveRefreshToken(ctx context.Context, id string, now time.Time) (bool, error) {
	res := s.WithContext(ctx).Model(&model.RefreshToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Updates(map[string]interface{}{"revoked_at": now})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (s *store) CreateModelProviderInstance(ctx context.Context, i *model.ModelProviderInstance) error {
	return s.WithContext(ctx).Create(i).Error
}

func (s *store) CreateModelProviderInstanceDefault(ctx context.Context, i *model.ModelProviderInstance) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(i).Error; err != nil {
			return err
		}
		// Atomically claim the gateway default only while the provider still has
		// none, so concurrent first-instance inserts never point the snapshot at
		// the wrong instance.
		return tx.Model(&model.ModelProvider{}).
			Where("id = ? AND tenant_id = ? AND (base_url = '' OR base_url IS NULL OR api_key_enc = '' OR api_key_enc IS NULL)", i.ProviderID, i.TenantID).
			Updates(map[string]interface{}{"base_url": i.BaseURL, "api_key_enc": i.APIKeyEnc}).Error
	})
}

func (s *store) CreateModelProviderInstanceWithModels(ctx context.Context, i *model.ModelProviderInstance, models []*model.ModelProviderModel) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(i).Error; err != nil {
			return err
		}
		if len(models) > 0 {
			if err := tx.Create(&models).Error; err != nil {
				return err
			}
		}
		return tx.Model(&model.ModelProvider{}).
			Where("id = ? AND tenant_id = ? AND (base_url = '' OR base_url IS NULL OR api_key_enc = '' OR api_key_enc IS NULL)", i.ProviderID, i.TenantID).
			Updates(map[string]interface{}{"base_url": i.BaseURL, "api_key_enc": i.APIKeyEnc}).Error
	})
}

func (s *store) GetModelProviderInstance(ctx context.Context, tenantID, providerID, id string) (*model.ModelProviderInstance, error) {
	var i model.ModelProviderInstance
	err := s.WithContext(ctx).Where("tenant_id = ? AND provider_id = ? AND id = ?", tenantID, providerID, id).First(&i).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &i, err
}

func (s *store) GetModelProviderInstanceByName(ctx context.Context, tenantID, providerID, name string) (*model.ModelProviderInstance, error) {
	var i model.ModelProviderInstance
	err := s.WithContext(ctx).Where("tenant_id = ? AND provider_id = ? AND instance_name = ?", tenantID, providerID, name).First(&i).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &i, err
}

func (s *store) ListModelProviderInstances(ctx context.Context, tenantID, providerID string) ([]model.ModelProviderInstance, error) {
	var list []model.ModelProviderInstance
	err := s.WithContext(ctx).Where("tenant_id = ? AND provider_id = ?", tenantID, providerID).Order("created_at DESC").Find(&list).Error
	return list, err
}

func (s *store) UpdateModelProviderInstance(ctx context.Context, i *model.ModelProviderInstance) error {
	return s.WithContext(ctx).Model(&model.ModelProviderInstance{}).Where("id = ? AND tenant_id = ?", i.ID, i.TenantID).
		Select("instance_name", "api_key_enc", "base_url", "region", "status", "extra_json").Updates(i).Error
}

func (s *store) DeleteModelProviderInstances(ctx context.Context, tenantID, providerID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.WithContext(ctx).Where("tenant_id = ? AND provider_id = ? AND id IN ?", tenantID, providerID, ids).Delete(&model.ModelProviderInstance{}).Error
}

func (s *store) DeleteInstancesByProviderIDs(ctx context.Context, tenantID string, providerIDs []string) error {
	if len(providerIDs) == 0 {
		return nil
	}
	return s.WithContext(ctx).Where("tenant_id = ? AND provider_id IN ?", tenantID, providerIDs).Delete(&model.ModelProviderInstance{}).Error
}

func (s *store) ListModelProviderInstancesByTenant(ctx context.Context, tenantID string) ([]model.ModelProviderInstance, error) {
	var list []model.ModelProviderInstance
	err := s.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&list).Error
	return list, err
}

func (s *store) CreateModelProviderModel(ctx context.Context, m *model.ModelProviderModel) error {
	return s.WithContext(ctx).Create(m).Error
}

func (s *store) GetModelProviderModel(ctx context.Context, tenantID, instanceID, id string) (*model.ModelProviderModel, error) {
	var m model.ModelProviderModel
	err := s.WithContext(ctx).Where("tenant_id = ? AND instance_id = ? AND id = ?", tenantID, instanceID, id).First(&m).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &m, err
}

func (s *store) GetModelProviderModelByName(ctx context.Context, tenantID, providerID, instanceID, name string) (*model.ModelProviderModel, error) {
	var m model.ModelProviderModel
	err := s.WithContext(ctx).Where("tenant_id = ? AND provider_id = ? AND instance_id = ? AND model_name = ?", tenantID, providerID, instanceID, name).First(&m).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &m, err
}

func (s *store) ListModelProviderModels(ctx context.Context, tenantID, instanceID string) ([]model.ModelProviderModel, error) {
	var list []model.ModelProviderModel
	err := s.WithContext(ctx).Where("tenant_id = ? AND instance_id = ?", tenantID, instanceID).Order("created_at DESC").Find(&list).Error
	return list, err
}

func (s *store) UpdateModelProviderModel(ctx context.Context, m *model.ModelProviderModel) error {
	return s.WithContext(ctx).Model(&model.ModelProviderModel{}).Where("id = ? AND tenant_id = ?", m.ID, m.TenantID).
		Select("model_type", "status", "max_tokens", "is_tools", "thinking", "verify", "extra_json").Updates(m).Error
}

func (s *store) UpdateModelProviderInstanceWithModels(ctx context.Context, provider *model.ModelProvider, instance *model.ModelProviderInstance, models []*model.ModelProviderModel, removeIDs []string) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.ModelProviderInstance{}).
			Where("id = ? AND tenant_id = ?", instance.ID, instance.TenantID).
			Select("instance_name", "api_key_enc", "base_url", "region", "status", "extra_json").
			Updates(instance).Error; err != nil {
			return err
		}
		if len(removeIDs) > 0 {
			if err := tx.Where("tenant_id = ? AND instance_id = ?", instance.TenantID, instance.ID).
				Delete(&model.ModelProviderModel{}, removeIDs).Error; err != nil {
				return err
			}
		}
		for _, modelValue := range models {
			if err := tx.Save(modelValue).Error; err != nil {
				return err
			}
		}
		if provider != nil {
			var count int64
			if err := tx.Model(&model.ModelProviderInstance{}).
				Where("tenant_id = ? AND provider_id = ?", provider.TenantID, provider.ID).
				Count(&count).Error; err != nil {
				return err
			}
			if count == 1 {
				if err := tx.Model(&model.ModelProvider{}).
					Where("id = ? AND tenant_id = ?", provider.ID, provider.TenantID).
					Select("base_url", "api_key_enc").
					Updates(provider).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *store) DeleteModelProviderInstancesWithModels(ctx context.Context, provider *model.ModelProvider, ids []string) error {
	if provider == nil || len(ids) == 0 {
		return nil
	}
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			var locked model.ModelProvider
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Select("id").
				Where("id = ? AND tenant_id = ?", provider.ID, provider.TenantID).
				First(&locked).Error; err != nil {
				return err
			}
		}
		var remaining int64
		if err := tx.Model(&model.ModelProviderInstance{}).
			Where("tenant_id = ? AND provider_id = ? AND id NOT IN ?", provider.TenantID, provider.ID, ids).
			Count(&remaining).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ? AND instance_id IN ?", provider.TenantID, ids).
			Delete(&model.ModelProviderModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ? AND provider_id = ? AND id IN ?", provider.TenantID, provider.ID, ids).
			Delete(&model.ModelProviderInstance{}).Error; err != nil {
			return err
		}
		if remaining == 0 {
			return tx.Model(&model.ModelProvider{}).
				Where("id = ? AND tenant_id = ?", provider.ID, provider.TenantID).
				Updates(map[string]interface{}{"base_url": "", "api_key_enc": ""}).Error
		}
		var nextDefault model.ModelProviderInstance
		if err := tx.Where("tenant_id = ? AND provider_id = ? AND id NOT IN ?", provider.TenantID, provider.ID, ids).
			Order("created_at DESC, id DESC").First(&nextDefault).Error; err != nil {
			return err
		}
		return tx.Model(&model.ModelProvider{}).
			Where("id = ? AND tenant_id = ?", provider.ID, provider.TenantID).
			Updates(map[string]interface{}{"base_url": nextDefault.BaseURL, "api_key_enc": nextDefault.APIKeyEnc}).Error
	})
}

func (s *store) RestoreModelProviderInstancesWithModels(ctx context.Context, provider *model.ModelProvider, instances []*model.ModelProviderInstance, models []*model.ModelProviderModel) error {
	if provider == nil {
		return errors.New("provider is required")
	}
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, instance := range instances {
			if err := tx.Create(instance).Error; err != nil {
				return err
			}
		}
		for _, modelValue := range models {
			if err := tx.Create(modelValue).Error; err != nil {
				return err
			}
		}
		return reconcileModelProviderDefaultAfterRestore(tx, provider)
	})
}

func reconcileModelProviderDefaultAfterRestore(tx *gorm.DB, provider *model.ModelProvider) error {
	locked := model.ModelProvider{}
	providerQuery := tx.Select("id").Where("id = ? AND tenant_id = ?", provider.ID, provider.TenantID)
	if tx.Dialector.Name() == "postgres" {
		providerQuery = providerQuery.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := providerQuery.First(&locked).Error; err != nil {
		return err
	}
	var nextDefault model.ModelProviderInstance
	err := tx.Where("tenant_id = ? AND provider_id = ?", provider.TenantID, provider.ID).
		Order("created_at DESC, id DESC").First(&nextDefault).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return tx.Model(&model.ModelProvider{}).
			Where("id = ? AND tenant_id = ?", provider.ID, provider.TenantID).
			Updates(map[string]interface{}{"base_url": "", "api_key_enc": ""}).Error
	}
	if err != nil {
		return err
	}
	return tx.Model(&model.ModelProvider{}).
		Where("id = ? AND tenant_id = ?", provider.ID, provider.TenantID).
		Updates(map[string]interface{}{"base_url": nextDefault.BaseURL, "api_key_enc": nextDefault.APIKeyEnc}).Error
}

func (s *store) DeleteModelProviderModels(ctx context.Context, tenantID, instanceID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	return s.WithContext(ctx).Where("tenant_id = ? AND instance_id = ? AND id IN ?", tenantID, instanceID, ids).Delete(&model.ModelProviderModel{}).Error
}

func (s *store) DeleteModelsByInstanceIDs(ctx context.Context, tenantID string, instanceIDs []string) error {
	if len(instanceIDs) == 0 {
		return nil
	}
	return s.WithContext(ctx).Where("tenant_id = ? AND instance_id IN ?", tenantID, instanceIDs).Delete(&model.ModelProviderModel{}).Error
}

func (s *store) ListModelProviderModelsByInstanceIDs(ctx context.Context, tenantID string, instanceIDs []string) ([]model.ModelProviderModel, error) {
	if len(instanceIDs) == 0 {
		return nil, nil
	}
	var list []model.ModelProviderModel
	err := s.WithContext(ctx).Where("tenant_id = ? AND instance_id IN ?", tenantID, instanceIDs).Order("created_at DESC").Find(&list).Error
	return list, err
}

// RecordCostMetric records a per-request usage detail row idempotently. A
// retried request_id is ignored so reconciliation never double counts.
func (s *store) RecordCostMetric(ctx context.Context, m *model.CostMetric) (bool, error) {
	if m.RequestID == "" {
		return false, errors.New("request_id is required for cost metering")
	}
	if m.ID == "" {
		m.ID = id.New()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	res := s.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(m)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ListCostMetrics returns a page of usage detail rows. Non-platform callers
// (scopeAll=false) are constrained to their own tenant.
func (s *store) ListCostMetrics(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter CostMetricFilter) ([]model.CostMetric, int64, error) {
	var list []model.CostMetric
	var total int64
	q := s.WithContext(ctx).Model(&model.CostMetric{})
	if !scopeAll {
		q = q.Where("tenant_id = ?", tenantID)
	} else if filter.TenantID != "" {
		q = q.Where("tenant_id = ?", filter.TenantID)
	}
	if filter.UserID != "" {
		q = q.Where("user_id = ?", filter.UserID)
	}
	if filter.KeyID != "" {
		q = q.Where("key_id = ?", filter.KeyID)
	}
	if filter.ChatID != "" {
		q = q.Where("chat_id = ?", filter.ChatID)
	}
	if filter.Model != "" {
		q = q.Where("model = ?", filter.Model)
	}
	if filter.Scenario != "" {
		q = q.Where("scenario = ?", filter.Scenario)
	}
	if filter.SessionID != "" {
		q = q.Where("session_id = ?", filter.SessionID)
	}
	if t, ok := parseFilterDate(filter.DateFrom); ok {
		q = q.Where("created_at >= ?", t)
	}
	if t, ok := parseFilterDate(filter.DateTo); ok {
		// created_to/DateTo is exclusive: date-only input runs through end of day.
		if filter.DateTo != t.Format(time.RFC3339) {
			t = t.AddDate(0, 0, 1)
		}
		q = q.Where("created_at < ?", t)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&list).Error
	return list, total, err
}

// ChatUsageAgg is a per-chat aggregation of metered cost detail.
type ChatUsageAgg struct {
	Requests  int64   `json:"requests"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
	Cost      float64 `json:"cost"`
}

func (s *store) SumCostMetricByChat(ctx context.Context, tenantID, chatID string) (*ChatUsageAgg, error) {
	var row struct {
		R  int64   `gorm:"column:requests"`
		Ti int64   `gorm:"column:tokens_in"`
		To int64   `gorm:"column:tokens_out"`
		C  float64 `gorm:"column:cost"`
	}
	err := s.WithContext(ctx).Model(&model.CostMetric{}).
		Select("COUNT(*) as requests, COALESCE(SUM(tokens_in),0) as tokens_in, COALESCE(SUM(tokens_out),0) as tokens_out").
		Where("tenant_id = ? AND chat_id = ?", tenantID, chatID).Scan(&row).Error
	if err != nil {
		return nil, err
	}
	return &ChatUsageAgg{Requests: row.R, TokensIn: row.Ti, TokensOut: row.To, Cost: row.C}, nil
}
