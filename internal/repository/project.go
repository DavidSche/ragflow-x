package repository

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

// ProjectRepo persists projects (knowledge domains) and their members.
type ProjectRepo interface {
	CreateProject(ctx context.Context, p *model.Project) error
	GetProject(ctx context.Context, tenantID, id string) (*model.Project, error)
	GetProjectByName(ctx context.Context, tenantID, name, excludeID string) (*model.Project, error)
	ListProjects(ctx context.Context, tenantID string) ([]model.Project, error)
	ListProjectsByMember(ctx context.Context, tenantID, userID string) ([]model.Project, error)
	ListProjectsByIDs(ctx context.Context, tenantID string, ids []string) ([]model.Project, error)
	DeleteProject(ctx context.Context, tenantID, id string) error
	UpdateProject(ctx context.Context, p *model.Project) error
	AddProjectMember(ctx context.Context, userID, projectID string) error
	RemoveProjectMember(ctx context.Context, projectID, userID string) error
	ListProjectUsers(ctx context.Context, projectID string) ([]model.User, error)
	IsProjectMember(ctx context.Context, userID, projectID string) (bool, error)
}

func (s *store) CreateProject(ctx context.Context, p *model.Project) error {
	return s.WithContext(ctx).Create(p).Error
}

func (s *store) GetProject(ctx context.Context, tenantID, id string) (*model.Project, error) {
	var p model.Project
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&p).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &p, err
}

func (s *store) GetProjectByName(ctx context.Context, tenantID, name, excludeID string) (*model.Project, error) {
	var p model.Project
	q := s.WithContext(ctx).Where("tenant_id = ? AND LOWER(name) = LOWER(?)", tenantID, name)
	if excludeID != "" {
		q = q.Where("id <> ?", excludeID)
	}
	err := q.First(&p).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &p, err
}

func (s *store) ListProjects(ctx context.Context, tenantID string) ([]model.Project, error) {
	var list []model.Project
	err := s.WithContext(ctx).Where("tenant_id = ?", tenantID).Order("created_at DESC").Find(&list).Error
	return list, err
}

// ListProjectsByMember returns the tenant projects the user is a member of,
// used to scope project visibility for non-admin roles.
func (s *store) ListProjectsByMember(ctx context.Context, tenantID, userID string) ([]model.Project, error) {
	var list []model.Project
	err := s.WithContext(ctx).Model(&model.Project{}).
		Joins("JOIN rgx_project_member ON rgx_project_member.project_id = rgx_project.id").
		Where("rgx_project.tenant_id = ? AND rgx_project_member.user_id = ?", tenantID, userID).
		Order("rgx_project.created_at DESC").Scan(&list).Error
	return list, err
}
func (s *store) DeleteProject(ctx context.Context, tenantID, id string) error {
	return s.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&model.Project{}).Error
}
func (s *store) UpdateProject(ctx context.Context, p *model.Project) error {
	return s.WithContext(ctx).Model(&model.Project{}).
		Where("id = ? AND tenant_id = ?", p.ID, p.TenantID).
		Updates(map[string]interface{}{"name": p.Name, "description": p.Description}).Error
}

func (s *store) AddProjectMember(ctx context.Context, userID, projectID string) error {
	return s.WithContext(ctx).Create(&model.ProjectMember{UserID: userID, ProjectID: projectID}).Error
}

func (s *store) RemoveProjectMember(ctx context.Context, projectID, userID string) error {
	return s.WithContext(ctx).Where("project_id = ? AND user_id = ?", projectID, userID).Delete(&model.ProjectMember{}).Error
}

func (s *store) ListProjectUsers(ctx context.Context, projectID string) ([]model.User, error) {
	var users []model.User
	err := s.WithContext(ctx).Model(&model.User{}).
		Joins("JOIN rgx_project_member ON rgx_project_member.user_id = rgx_user.id").
		Where("rgx_project_member.project_id = ?", projectID).
		Order("rgx_user.created_at DESC").Scan(&users).Error
	return users, err
}

func (s *store) IsProjectMember(ctx context.Context, userID, projectID string) (bool, error) {
	var n int64
	err := s.WithContext(ctx).Model(&model.ProjectMember{}).
		Where("user_id = ? AND project_id = ?", userID, projectID).Count(&n).Error
	return n > 0, err
}
