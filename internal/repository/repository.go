// Package repository abstracts the operational (shadow) store access.
package repository

import (
	"context"
	"sync"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store groups the repository interfaces used by services.
type Store interface {
	TenantRepo
	UserRepo
	OIDCRepo
	DatasetRepo
	Close() error
	WithinTransaction(ctx context.Context, operation func(tx Store) error) error
	RoleRepo
	ModelProviderRepo
	ModelRouteRepo
	APIKeyRepo
	AuditRepo
	TaskRepo
	JobRepo
	QuotaRepo
	TeamRepo
	UserTeamRepo
	ProjectRepo
	RefreshTokenRepo
	TeamProjectRepo
	ChatShadowRepo
	FeedbackRepo
	KnowledgeOpsRepo
	SearchAppShadowRepo
	MemoryShadowRepo
	AgentShadowRepo
	AssistantCatalogRepo
	ConversationRouteRepo
	RetentionRepo
	ApprovalRepo
	ActingContextRepo
	ApprovalOperationRepo
	AlertRepo
	AuditAnchorRepo
	GovernanceRepo
	ReleaseGovernanceRepo
	EnterpriseConnectionRepo
	ModelRoutePinRepo
	ResourceSyncRepo
	SettingRevisionRepo
}

// ConversationAgentListFilter is kept beside the repository aggregate to avoid
// service-layer knowledge of provider pagination fields.
func ConversationAgentListFilter(page, pageSize int) ragflow.ListAgentsFilter {
	return ragflow.ListAgentsFilter{Page: page, PageSize: pageSize}
}

// TenantRepo persists tenants.
type TenantRepo interface {
	CreateTenant(ctx context.Context, t *model.Tenant) error
	GetTenant(ctx context.Context, id string) (*model.Tenant, error)
	GetTenantByName(ctx context.Context, name string) (*model.Tenant, error)
	ListTenants(ctx context.Context, page, pageSize int, filter TenantFilter) ([]model.Tenant, int64, error)
	ListTenantsForScope(ctx context.Context, scopeAll bool, tenantIDs []string, page, pageSize int, filter TenantFilter) ([]model.Tenant, int64, error)
	UpdateTenant(ctx context.Context, t *model.Tenant) error
	CountTenants(ctx context.Context) (int64, error)
	ListAllTenants(ctx context.Context) ([]model.Tenant, error)
	DeleteTenant(ctx context.Context, tenantID string) error
}

// UserRepo persists users.
type UserRepo interface {
	CreateUser(ctx context.Context, u *model.User) error
	GetUserByUsername(ctx context.Context, username string) (*model.User, error)
	GetUserByEmail(ctx context.Context, email string) (*model.User, error)
	GetUser(ctx context.Context, id string) (*model.User, error)
	ListUsers(ctx context.Context, page, pageSize int, filter UserFilter) ([]model.User, int64, error)
	ListUsersByTenant(ctx context.Context, tenantID string, page, pageSize int, filter UserFilter) ([]model.User, int64, error)
	CountActiveUsersByTenantAndRole(ctx context.Context, tenantID, role string) (int64, error)
	UpdateUser(ctx context.Context, u *model.User) error
	CountUsers(ctx context.Context) (int64, error)
	CountUsersByTenant(ctx context.Context, tenantID string) (int64, error)
	DeleteUser(ctx context.Context, userID string) error
}

// DatasetRepo persists RAGFlow dataset links.
type DatasetRepo interface {
	CreateDatasetLink(ctx context.Context, d *model.DatasetLink) error
	GetDatasetLink(ctx context.Context, tenantID, id string) (*model.DatasetLink, error)
	GetDatasetLinkByName(ctx context.Context, tenantID, name, excludeID string) (*model.DatasetLink, error)
	GetDatasetLinkForScope(ctx context.Context, scopeAll bool, tenantIDs []string, id string) (*model.DatasetLink, error)
	GetRAGFlowDatasetLinkForScope(ctx context.Context, scopeAll bool, tenantIDs []string, ragflowID string) (*model.DatasetLink, error)
	ListByTenant(ctx context.Context, tenantID string, filter DatasetFilter) ([]model.DatasetLink, error)
	ListDatasetLinksForScope(ctx context.Context, scopeAll bool, tenantIDs []string, filter DatasetFilter) ([]model.DatasetLink, error)
	ListDatasetLinksForScopePage(ctx context.Context, scopeAll bool, tenantIDs []string, filter DatasetFilter, page, pageSize int) ([]model.DatasetLink, int64, error)
	DeleteDatasetLink(ctx context.Context, id string) error
	ListAllDatasetLinks(ctx context.Context, filter DatasetFilter) ([]model.DatasetLink, error)
	DeleteDatasetLinkByTenant(ctx context.Context, tenantID, id string) error
	SetDatasetProject(ctx context.Context, tenantID, id, projectID string) error
	UpdateDatasetLinkName(ctx context.Context, tenantID, id, name string) error
	UpsertDatasetLink(ctx context.Context, d *model.DatasetLink) error
	CreateDocumentOwnership(ctx context.Context, ownership *model.DocumentOwnershipLink) error
	ListDocumentOwners(ctx context.Context, tenantID, datasetID string, documentIDs []string) ([]model.DocumentOwnershipLink, error)
	CountDocumentOwnership(ctx context.Context, tenantID, datasetID, uploaderID string, documentIDs []string) (int64, error)
	DeleteDocumentOwnership(ctx context.Context, tenantID, datasetID string, documentIDs []string) error
	DeleteDocumentOwnershipByDataset(ctx context.Context, tenantID, datasetID string) error
	GetIncrementalLedger(ctx context.Context, tenantID, datasetID, sourceKey string) (*model.IncrementalLedger, error)
	ListIncrementalLedger(ctx context.Context, tenantID, datasetID string) ([]model.IncrementalLedger, error)
	CreateIncrementalLedger(ctx context.Context, ledger *model.IncrementalLedger) error
	UpdateIncrementalLedger(ctx context.Context, ledger *model.IncrementalLedger) error
	GetIncrementalLedgerByDocument(ctx context.Context, tenantID, ragflowDocumentID string) (*model.IncrementalLedger, error)
	CreateCitationReference(ctx context.Context, reference *model.CitationReference) error
	ListCitationReferences(ctx context.Context, tenantID, requestID string) ([]model.CitationReference, error)
}

type store struct {
	*gorm.DB
	// auditLocks serializes hash-chain appends per tenant within this
	// process. Cross-process safety comes from the (tenant_id, seq) unique
	// index plus the retry loop in CreateAudit.
	auditLocks sync.Map // map[tenantID]*sync.Mutex
}

func (s *store) WithinTransaction(ctx context.Context, operation func(tx Store) error) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return operation(&store{DB: tx})
	})
}

const (
	defaultPageSize = 20
	maxPageSize     = 200
)

// paginate clamps page/pageSize defensively (defense in depth below the
// service layer) and returns the SQL offset and limit.
func paginate(page, pageSize int) (offset, limit int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return (page - 1) * pageSize, pageSize
}

// NewStore builds a Store backed by the given GORM connection.
func NewStore(db *gorm.DB) Store { return &store{DB: db} }

// lockTenant returns the unlock func for the tenant's audit-append mutex.
func (s *store) lockTenant(tenantID string) func() {
	v, _ := s.auditLocks.LoadOrStore(tenantID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *store) Close() error {
	raw, err := s.DB.DB()
	if err != nil {
		return err
	}
	return raw.Close()
}

func (s *store) CreateTenant(ctx context.Context, t *model.Tenant) error {
	return s.WithContext(ctx).Create(t).Error
}

func (s *store) GetTenant(ctx context.Context, id string) (*model.Tenant, error) {
	var t model.Tenant
	err := s.WithContext(ctx).Where("id = ?", id).First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &t, err
}

func (s *store) GetTenantByName(ctx context.Context, name string) (*model.Tenant, error) {
	var t model.Tenant
	err := s.WithContext(ctx).Where("LOWER(name) = LOWER(?)", name).First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &t, err
}

func (s *store) ListTenants(ctx context.Context, page, pageSize int, filter TenantFilter) ([]model.Tenant, int64, error) {
	var tenants []model.Tenant
	var total int64
	q := s.WithContext(ctx).Model(&model.Tenant{})
	if filter.Type == "" {
		q = q.Where("type <> ?", model.TenantTypePlatform)
	} else {
		q = q.Where("type = ?", filter.Type)
	}
	if filter.Name != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likePattern(filter.Name))
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if t, ok := parseFilterDate(filter.CreatedFrom); ok {
		q = q.Where("created_at >= ?", t)
	}
	if t, ok := parseFilterDate(filter.CreatedTo); ok {
		// created_to is exclusive: shift to end of day for date-only input
		if filter.CreatedTo != t.Format(time.RFC3339) {
			t = t.AddDate(0, 0, 1)
		}
		q = q.Where("created_at < ?", t)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset, limit := paginate(page, pageSize)
	err := q.
		Order("created_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&tenants).Error
	return tenants, total, err
}

func (s *store) ListTenantsForScope(ctx context.Context, scopeAll bool, tenantIDs []string, page, pageSize int, filter TenantFilter) ([]model.Tenant, int64, error) {
	var tenants []model.Tenant
	var total int64
	q := s.WithContext(ctx).Model(&model.Tenant{}).Where("type <> ?", model.TenantTypePlatform)
	if !scopeAll {
		if len(tenantIDs) == 0 {
			return tenants, 0, nil
		}
		q = q.Where("id IN ?", tenantIDs)
	}
	if filter.Name != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likePattern(filter.Name))
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
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
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&tenants).Error
	return tenants, total, err
}

func (s *store) UpdateTenant(ctx context.Context, t *model.Tenant) error {
	return s.WithContext(ctx).Save(t).Error
}

func (s *store) CreateUser(ctx context.Context, u *model.User) error {
	return s.WithContext(ctx).Create(u).Error
}

func (s *store) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	var u model.User
	err := s.WithContext(ctx).Where("LOWER(username) = LOWER(?)", username).First(&u).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &u, err
}

func (s *store) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	var u model.User
	err := s.WithContext(ctx).Where("LOWER(email) = LOWER(?)", email).First(&u).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &u, err
}

func (s *store) GetUser(ctx context.Context, id string) (*model.User, error) {
	var u model.User
	err := s.WithContext(ctx).Where("id = ?", id).First(&u).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &u, err
}

func (s *store) ListUsers(ctx context.Context, page, pageSize int, filter UserFilter) ([]model.User, int64, error) {
	var users []model.User
	var total int64
	q := s.WithContext(ctx).Model(&model.User{})
	if filter.TenantID != "" {
		q = q.Where("tenant_id = ?", filter.TenantID)
	}
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
	err := q.
		Order("created_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&users).Error
	return users, total, err
}

func (s *store) CreateDatasetLink(ctx context.Context, d *model.DatasetLink) error {
	return s.WithContext(ctx).Create(d).Error
}

func (s *store) GetDatasetLinkByName(ctx context.Context, tenantID, name, excludeID string) (*model.DatasetLink, error) {
	var d model.DatasetLink
	q := s.WithContext(ctx).Where("tenant_id = ? AND LOWER(name) = LOWER(?)", tenantID, name)
	if excludeID != "" {
		q = q.Where("id <> ?", excludeID)
	}
	err := q.First(&d).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &d, err
}

func (s *store) GetDatasetLink(ctx context.Context, tenantID, id string) (*model.DatasetLink, error) {
	var d model.DatasetLink
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&d).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &d, err
}

func (s *store) GetDatasetLinkForScope(ctx context.Context, scopeAll bool, tenantIDs []string, id string) (*model.DatasetLink, error) {
	var d model.DatasetLink
	q := s.WithContext(ctx).Where("id = ?", id)
	if !scopeAll {
		if len(tenantIDs) == 0 {
			return nil, nil
		}
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	if err := q.First(&d).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &d, nil
}
func (s *store) GetRAGFlowDatasetLinkForScope(
	ctx context.Context, scopeAll bool, tenantIDs []string, ragflowID string,
) (*model.DatasetLink, error) {
	var d model.DatasetLink
	q := s.WithContext(ctx).Where("ragflow_dataset_id = ?", ragflowID)
	if !scopeAll {
		if len(tenantIDs) == 0 {
			return nil, nil
		}
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	err := q.Order("tenant_id ASC").First(&d).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &d, err
}

func (s *store) ListByTenant(ctx context.Context, tenantID string, filter DatasetFilter) ([]model.DatasetLink, error) {
	var links []model.DatasetLink
	q := s.WithContext(ctx).Where("tenant_id = ?", tenantID)
	if filter.Name != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likePattern(filter.Name))
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
	err := q.Find(&links).Error
	return links, err
}

func (s *store) ListDatasetLinksForScope(ctx context.Context, scopeAll bool, tenantIDs []string, filter DatasetFilter) ([]model.DatasetLink, error) {
	var links []model.DatasetLink
	q := s.WithContext(ctx).Model(&model.DatasetLink{})
	if !scopeAll {
		if len(tenantIDs) == 0 {
			return links, nil
		}
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	if filter.Name != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likePattern(filter.Name))
	}
	if filter.TenantID != "" {
		q = q.Where("tenant_id = ?", filter.TenantID)
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
	err := q.Find(&links).Error
	return links, err
}

func (s *store) ListDatasetLinksForScopePage(ctx context.Context, scopeAll bool, tenantIDs []string, filter DatasetFilter, page, pageSize int) ([]model.DatasetLink, int64, error) {
	var links []model.DatasetLink
	var total int64
	q := s.WithContext(ctx).Model(&model.DatasetLink{})
	if !scopeAll {
		if len(tenantIDs) == 0 {
			return links, 0, nil
		}
		q = q.Where("tenant_id IN ?", tenantIDs)
	}
	if filter.Name != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likePattern(filter.Name))
	}
	if filter.TenantID != "" {
		q = q.Where("tenant_id = ?", filter.TenantID)
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
	err := q.Order("created_at DESC").Offset(offset).Limit(limit).Find(&links).Error
	return links, total, err
}

func (s *store) DeleteDatasetLink(ctx context.Context, id string) error {
	return s.WithContext(ctx).Where("id = ?", id).Delete(&model.DatasetLink{}).Error
}

func (s *store) ListAllDatasetLinks(ctx context.Context, filter DatasetFilter) ([]model.DatasetLink, error) {
	var list []model.DatasetLink
	q := s.WithContext(ctx).Model(&model.DatasetLink{})
	if filter.Name != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likePattern(filter.Name))
	}
	if filter.TenantID != "" {
		q = q.Where("tenant_id = ?", filter.TenantID)
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
	err := q.Find(&list).Error
	return list, err
}

func (s *store) DeleteDatasetLinkByTenant(ctx context.Context, tenantID, id string) error {
	return s.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&model.DatasetLink{}).Error
}

func (s *store) SetDatasetProject(ctx context.Context, tenantID, id, projectID string) error {
	return s.WithContext(ctx).Model(&model.DatasetLink{}).
		Where("id = ? AND tenant_id = ?", id, tenantID).Update("project_id", projectID).Error
}

func (s *store) UpdateDatasetLinkName(ctx context.Context, tenantID, id, name string) error {
	return s.WithContext(ctx).Model(&model.DatasetLink{}).
		Where("id = ? AND tenant_id = ?", id, tenantID).Update("name", name).Error
}

func (s *store) UpsertDatasetLink(ctx context.Context, d *model.DatasetLink) error {
	return s.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"tenant_id":          d.TenantID,
			"ragflow_dataset_id": d.RAGFlowDatasetID,
			"name":               d.Name,
			"document_count":     d.DocumentCount,
			"updated_at":         d.UpdatedAt,
		}),
	}).Create(d).Error
}

func (s *store) CreateDocumentOwnership(ctx context.Context, ownership *model.DocumentOwnershipLink) error {
	return s.WithContext(ctx).Create(ownership).Error
}

func (s *store) ListDocumentOwners(ctx context.Context, tenantID, datasetID string, documentIDs []string) ([]model.DocumentOwnershipLink, error) {
	if len(documentIDs) == 0 {
		return nil, nil
	}
	owners := make([]model.DocumentOwnershipLink, 0, len(documentIDs))
	err := s.WithContext(ctx).Where(
		"tenant_id = ? AND dataset_id = ? AND document_id IN ?",
		tenantID, datasetID, documentIDs,
	).Find(&owners).Error
	return owners, err
}

func (s *store) CountDocumentOwnership(ctx context.Context, tenantID, datasetID, uploaderID string, documentIDs []string) (int64, error) {
	if len(documentIDs) == 0 {
		return 0, nil
	}
	var count int64
	err := s.WithContext(ctx).Model(&model.DocumentOwnershipLink{}).Where(
		"tenant_id = ? AND dataset_id = ? AND uploader_id = ? AND document_id IN ?",
		tenantID, datasetID, uploaderID, documentIDs,
	).Count(&count).Error
	return count, err
}

func (s *store) DeleteDocumentOwnership(ctx context.Context, tenantID, datasetID string, documentIDs []string) error {
	if len(documentIDs) == 0 {
		return nil
	}
	return s.WithContext(ctx).Where(
		"tenant_id = ? AND dataset_id = ? AND document_id IN ?",
		tenantID, datasetID, documentIDs,
	).Delete(&model.DocumentOwnershipLink{}).Error
}

func (s *store) DeleteDocumentOwnershipByDataset(ctx context.Context, tenantID, datasetID string) error {
	return s.WithContext(ctx).Where("tenant_id = ? AND dataset_id = ?", tenantID, datasetID).
		Delete(&model.DocumentOwnershipLink{}).Error
}
