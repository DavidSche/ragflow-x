package repository

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

// TeamRepo persists tenant-scoped teams.
type TeamRepo interface {
	CreateTeam(ctx context.Context, t *model.Team) error
	GetTeam(ctx context.Context, tenantID, id string) (*model.Team, error)
	GetTeamByName(ctx context.Context, tenantID, name, excludeID string) (*model.Team, error)
	ListTeams(ctx context.Context, tenantID string, filter TeamFilter) ([]model.Team, error)
	UpdateTeam(ctx context.Context, t *model.Team) error
	DeleteTeam(ctx context.Context, tenantID, id string) error
}

func (s *store) CreateTeam(ctx context.Context, t *model.Team) error {
	return s.WithContext(ctx).Create(t).Error
}

func (s *store) GetTeam(ctx context.Context, tenantID, id string) (*model.Team, error) {
	var t model.Team
	err := s.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &t, err
}

func (s *store) GetTeamByName(ctx context.Context, tenantID, name, excludeID string) (*model.Team, error) {
	var t model.Team
	q := s.WithContext(ctx).Where("tenant_id = ? AND LOWER(name) = LOWER(?)", tenantID, name)
	if excludeID != "" {
		q = q.Where("id <> ?", excludeID)
	}
	err := q.First(&t).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &t, err
}

func (s *store) ListTeams(ctx context.Context, tenantID string, filter TeamFilter) ([]model.Team, error) {
	var list []model.Team
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
	err := q.Order("created_at DESC").Find(&list).Error
	return list, err
}

func (s *store) UpdateTeam(ctx context.Context, t *model.Team) error {
	return s.WithContext(ctx).Model(&model.Team{}).Where("id = ? AND tenant_id = ?", t.ID, t.TenantID).
		Select("name", "owner_id").Updates(t).Error
}

func (s *store) DeleteTeam(ctx context.Context, tenantID, id string) error {
	return s.WithContext(ctx).Where("id = ? AND tenant_id = ?", id, tenantID).Delete(&model.Team{}).Error
}

// UserTeamRepo manages team membership links.
type UserTeamRepo interface {
	AddUserToTeam(ctx context.Context, link *model.UserTeam) error
	RemoveUserFromTeam(ctx context.Context, teamID, userID string) error
	ListTeamUsers(ctx context.Context, tenantID, teamID string) ([]model.User, error)
	ListUserTeams(ctx context.Context, userID string) ([]model.Team, error)
}

func (s *store) AddUserToTeam(ctx context.Context, link *model.UserTeam) error {
	return s.WithContext(ctx).Create(link).Error
}

func (s *store) RemoveUserFromTeam(ctx context.Context, teamID, userID string) error {
	return s.WithContext(ctx).Where("team_id = ? AND user_id = ?", teamID, userID).Delete(&model.UserTeam{}).Error
}

func (s *store) ListTeamUsers(ctx context.Context, tenantID, teamID string) ([]model.User, error) {
	var users []model.User
	err := s.WithContext(ctx).Model(&model.User{}).
		Joins("JOIN rgx_user_team ON rgx_user_team.user_id = rgx_user.id").
		Where("rgx_user_team.team_id = ? AND rgx_user.tenant_id = ?", teamID, tenantID).
		Order("rgx_user.created_at DESC").Scan(&users).Error
	return users, err
}

func (s *store) ListUserTeams(ctx context.Context, userID string) ([]model.Team, error) {
	var teams []model.Team
	err := s.WithContext(ctx).Model(&model.Team{}).
		Joins("JOIN rgx_user_team ON rgx_user_team.team_id = rgx_team.id").
		Where("rgx_user_team.user_id = ?", userID).
		Order("rgx_team.created_at DESC").Scan(&teams).Error
	return teams, err
}
