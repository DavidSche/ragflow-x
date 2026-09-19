package repository

import (
	"context"
	"errors"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"gorm.io/gorm"
)

// TeamProjectRepo persists team↔project authorization bindings.
type TeamProjectRepo interface {
	SetTeamProjects(ctx context.Context, tenantID, teamID string, projectIDs []string) error
	ListTeamProjects(ctx context.Context, tenantID, teamID string) ([]model.Project, error)
	ListUserTeamIDs(ctx context.Context, userID string) ([]string, error)
	ListTeamIDsOwnedBy(ctx context.Context, userID string) ([]string, error)
	IsTeamOwner(ctx context.Context, userID, teamID string) (bool, error)
	TeamProjectCounts(ctx context.Context, teamIDs []string) (map[string]int64, error)
	ListProjectIDsBoundToTeams(ctx context.Context, teamIDs []string) ([]string, error)
	ListTeamIDsByProject(ctx context.Context, projectID string) ([]string, error)
	SetProjectTeams(ctx context.Context, tenantID, projectID string, teamIDs []string) error
	IsProjectBoundToTeams(ctx context.Context, projectID string, teamIDs []string) (bool, error)
	ListProjectUsersViaTeams(ctx context.Context, projectID string) ([]model.User, error)
	DeleteTeamProjectsByProject(ctx context.Context, projectID string) error
	DeleteTeamProjectsByTeam(ctx context.Context, teamID string) error
}

// SetTeamProjects replaces a team's bound projects.
func (s *store) SetTeamProjects(ctx context.Context, tenantID, teamID string, projectIDs []string) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var teamCount int64
		if err := tx.Model(&model.Team{}).
			Where("tenant_id = ? AND id = ?", tenantID, teamID).
			Count(&teamCount).Error; err != nil {
			return err
		}
		if teamCount != 1 {
			return errors.New("team not found in tenant")
		}

		ids := uniqueStringIDs(projectIDs)
		if len(ids) != len(projectIDs) {
			return errors.New("duplicate project bindings are not allowed")
		}
		if len(ids) > 0 {
			var projectCount int64
			if err := tx.Model(&model.Project{}).
				Where("tenant_id = ? AND id IN ?", tenantID, ids).
				Count(&projectCount).Error; err != nil {
				return err
			}
			if projectCount != int64(len(ids)) {
				return errors.New("one or more projects not found in tenant")
			}
		}

		if err := tx.Where("team_id = ?", teamID).Delete(&model.TeamProject{}).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		rows := make([]model.TeamProject, 0, len(ids))
		for _, pid := range ids {
			rows = append(rows, model.TeamProject{TeamID: teamID, ProjectID: pid})
		}
		return tx.Create(&rows).Error
	})
}

func (s *store) ListTeamProjects(ctx context.Context, tenantID, teamID string) ([]model.Project, error) {
	var list []model.Project
	err := s.WithContext(ctx).Model(&model.Project{}).
		Joins("JOIN rgx_team_project ON rgx_team_project.project_id = rgx_project.id").
		Where("rgx_team_project.team_id = ? AND rgx_project.tenant_id = ?", teamID, tenantID).
		Order("rgx_project.created_at DESC").Scan(&list).Error
	return list, err
}

func (s *store) ListUserTeamIDs(ctx context.Context, userID string) ([]string, error) {
	var ids []string
	err := s.WithContext(ctx).Model(&model.UserTeam{}).
		Where("user_id = ?", userID).Pluck("team_id", &ids).Error
	return ids, err
}

func (s *store) ListTeamIDsOwnedBy(ctx context.Context, userID string) ([]string, error) {
	var ids []string
	err := s.WithContext(ctx).Model(&model.Team{}).
		Where("owner_id = ?", userID).Pluck("id", &ids).Error
	return ids, err
}

func (s *store) IsTeamOwner(ctx context.Context, userID, teamID string) (bool, error) {
	var n int64
	err := s.WithContext(ctx).Model(&model.Team{}).Where("id = ? AND owner_id = ?", teamID, userID).Count(&n).Error
	return n > 0, err
}
func (s *store) ListProjectIDsBoundToTeams(ctx context.Context, teamIDs []string) ([]string, error) {
	if len(teamIDs) == 0 {
		return []string{}, nil
	}
	var rows []model.TeamProject
	if err := s.WithContext(ctx).Where("team_id IN ?", teamIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		if seen[r.ProjectID] {
			continue
		}
		seen[r.ProjectID] = true
		ids = append(ids, r.ProjectID)
	}
	return ids, nil
}

// TeamProjectCounts returns the number of bound projects per team id.
func (s *store) TeamProjectCounts(ctx context.Context, teamIDs []string) (map[string]int64, error) {
	out := map[string]int64{}
	if len(teamIDs) == 0 {
		return out, nil
	}
	type countRow struct {
		TeamID string
		Cnt    int64
	}
	var rows []countRow
	err := s.WithContext(ctx).Model(&model.TeamProject{}).
		Select("team_id, COUNT(*) AS cnt").
		Where("team_id IN ?", teamIDs).Group("team_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.TeamID] = r.Cnt
	}
	return out, nil
}
func (s *store) IsProjectBoundToTeams(ctx context.Context, projectID string, teamIDs []string) (bool, error) {
	if len(teamIDs) == 0 {
		return false, nil
	}
	var n int64
	err := s.WithContext(ctx).Model(&model.TeamProject{}).
		Where("project_id = ? AND team_id IN ?", projectID, teamIDs).Count(&n).Error
	return n > 0, err
}

func (s *store) ListProjectUsersViaTeams(ctx context.Context, projectID string) ([]model.User, error) {
	var users []model.User
	err := s.WithContext(ctx).Model(&model.User{}).
		Joins("JOIN rgx_user_team ON rgx_user_team.user_id = rgx_user.id").
		Joins("JOIN rgx_team_project ON rgx_team_project.team_id = rgx_user_team.team_id").
		Where("rgx_team_project.project_id = ?", projectID).
		Distinct().Select("rgx_user.*").
		Order("rgx_user.created_at DESC").
		Scan(&users).Error
	return users, err
}

func (s *store) DeleteTeamProjectsByProject(ctx context.Context, projectID string) error {
	return s.WithContext(ctx).Where("project_id = ?", projectID).Delete(&model.TeamProject{}).Error
}

func (s *store) DeleteTeamProjectsByTeam(ctx context.Context, teamID string) error {
	return s.WithContext(ctx).Where("team_id = ?", teamID).Delete(&model.TeamProject{}).Error
}

// ListProjectsByIDs returns the tenant projects matching the given ids.
func (s *store) ListProjectsByIDs(ctx context.Context, tenantID string, ids []string) ([]model.Project, error) {
	var list []model.Project
	err := s.WithContext(ctx).Where("tenant_id = ? AND id IN ?", tenantID, ids).Order("created_at DESC").Find(&list).Error
	return list, err
}

func (s *store) ListTeamIDsByProject(ctx context.Context, projectID string) ([]string, error) {
	var rows []model.TeamProject
	if err := s.WithContext(ctx).Where("project_id = ?", projectID).Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.TeamID)
	}
	return ids, nil
}

// SetProjectTeams replaces a project's bound teams (authorization mirror of
// SetTeamProjects, driven from the project management page).
func (s *store) SetProjectTeams(ctx context.Context, tenantID, projectID string, teamIDs []string) error {
	return s.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var projectCount int64
		if err := tx.Model(&model.Project{}).
			Where("tenant_id = ? AND id = ?", tenantID, projectID).
			Count(&projectCount).Error; err != nil {
			return err
		}
		if projectCount != 1 {
			return errors.New("project not found in tenant")
		}

		ids := uniqueStringIDs(teamIDs)
		if len(ids) != len(teamIDs) {
			return errors.New("duplicate team bindings are not allowed")
		}
		if len(ids) > 0 {
			var teamCount int64
			if err := tx.Model(&model.Team{}).
				Where("tenant_id = ? AND id IN ?", tenantID, ids).
				Count(&teamCount).Error; err != nil {
				return err
			}
			if teamCount != int64(len(ids)) {
				return errors.New("one or more teams not found in tenant")
			}
		}

		if err := tx.Where("project_id = ?", projectID).Delete(&model.TeamProject{}).Error; err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		rows := make([]model.TeamProject, 0, len(ids))
		for _, tid := range ids {
			rows = append(rows, model.TeamProject{TeamID: tid, ProjectID: projectID})
		}
		return tx.Create(&rows).Error
	})
}

func uniqueStringIDs(values []string) []string {
	seen := make(map[string]bool, len(values))
	ids := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		ids = append(ids, value)
	}
	return ids
}
