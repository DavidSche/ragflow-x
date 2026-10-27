package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// TeamView is a team enriched with its owning tenant name and leader name.
type TeamView struct {
	model.Team
	TenantName   string `json:"tenant_name,omitempty"`
	OwnerName    string `json:"owner_name,omitempty"`
	ProjectCount int64  `json:"projects_count"`
}

// CreateTeam creates a tenant-scoped team.
func (s *Service) CreateTeam(ctx context.Context, tenantID, name, ownerID string) (*model.Team, error) {
	name, err := normalizeDisplayName(name, "team name is required", 40050)
	if err != nil {
		return nil, err
	}
	existing, err := s.Store.GetTeamByName(ctx, tenantID, name, "")
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, displayNameConflict("team")
	}
	t := &model.Team{ID: id.New(), TenantID: tenantID, Name: name, OwnerID: ownerID}
	if err := s.Store.CreateTeam(ctx, t); err != nil {
		return nil, err
	}
	if ownerID != "" {
		if err := s.Store.AssignUserRole(ctx, ownerID, model.RoleTeamAdmin); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// ListTeams returns the tenant's teams with tenant/owner names attached.
// ListTeams returns the tenant's teams the caller may see. Non-admin callers
// only see the teams they own or are a member of.
func (s *Service) ListTeams(ctx context.Context, tenantID, userID, role string, filter repository.TeamFilter) ([]TeamView, error) {
	teams, err := s.Store.ListTeams(ctx, tenantID, filter)
	if err != nil {
		return nil, err
	}
	if role != model.RolePlatformAdmin && role != model.RoleTenantAdmin {
		mine := map[string]bool{}
		if owned, err := s.Store.ListTeamIDsOwnedBy(ctx, userID); err != nil {
			return nil, err
		} else {
			for _, id := range owned {
				mine[id] = true
			}
		}
		if member, err := s.Store.ListUserTeamIDs(ctx, userID); err != nil {
			return nil, err
		} else {
			for _, id := range member {
				mine[id] = true
			}
		}
		filtered := make([]model.Team, 0, len(teams))
		for _, t := range teams {
			if mine[t.ID] {
				filtered = append(filtered, t)
			}
		}
		teams = filtered
	}
	view, err := s.teamViews(ctx, teams)
	return view, err
}

// GetTeamView returns a single team with its tenant/owner names.
func (s *Service) GetTeamView(ctx context.Context, tenantID, id string) (*TeamView, error) {
	t, err := s.Store.GetTeam(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, httperr.NotFound("team not found")
	}
	views, err := s.teamViews(ctx, []model.Team{*t})
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

func (s *Service) teamViews(ctx context.Context, teams []model.Team) ([]TeamView, error) {
	tenantName := map[string]string{}
	if tenants, err := s.Store.ListAllTenants(ctx); err == nil {
		for _, t := range tenants {
			tenantName[t.ID] = t.Name
		}
	}
	ownerName := map[string]string{}
	for _, t := range teams {
		if t.OwnerID == "" {
			continue
		}
		if u, err := s.Store.GetUser(ctx, t.OwnerID); err == nil && u != nil {
			ownerName[t.OwnerID] = u.Username
		}
	}
	teamIDs := make([]string, 0, len(teams))
	for _, t := range teams {
		teamIDs = append(teamIDs, t.ID)
	}
	projectCounts, _ := s.Store.TeamProjectCounts(ctx, teamIDs)
	out := make([]TeamView, 0, len(teams))
	for _, t := range teams {
		out = append(out, TeamView{Team: t, TenantName: tenantName[t.TenantID], OwnerName: ownerName[t.OwnerID], ProjectCount: projectCounts[t.ID]})
	}
	return out, nil
}

// UpdateTeam renames a tenant team or reassigns its owner.
func (s *Service) UpdateTeam(ctx context.Context, tenantID, id, name, ownerID string) (*model.Team, error) {
	t, err := s.Store.GetTeam(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, httperr.NotFound("team not found")
	}
	if name != "" {
		name, err = normalizeDisplayName(name, "team name is required", 40050)
		if err != nil {
			return nil, err
		}
		existing, err := s.Store.GetTeamByName(ctx, tenantID, name, id)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return nil, displayNameConflict("team")
		}
		t.Name = name
	}
	oldOwner := t.OwnerID
	if ownerID != "" {
		t.OwnerID = ownerID
	}
	if err := s.Store.UpdateTeam(ctx, t); err != nil {
		return nil, err
	}
	if ownerID != "" && ownerID != oldOwner {
		if err := s.Store.AssignUserRole(ctx, ownerID, model.RoleTeamAdmin); err != nil {
			return nil, err
		}
	}
	if oldOwner != "" && oldOwner != t.OwnerID {
		if err := s.revokeTeamAdminIfUnused(ctx, oldOwner); err != nil {
			return nil, err
		}
	}
	return t, nil
}

// DeleteTeam removes a tenant team.
func (s *Service) DeleteTeam(ctx context.Context, tenantID, id string) error {
	if err := s.Store.DeleteTeam(ctx, tenantID, id); err != nil {
		return err
	}
	return s.Store.DeleteTeamProjectsByTeam(ctx, id)
}

// AddUserToTeam binds a tenant user to a tenant team.
func (s *Service) AddUserToTeam(ctx context.Context, tenantID, teamID, userID string) error {
	t, err := s.Store.GetTeam(ctx, tenantID, teamID)
	if err != nil {
		return err
	}
	if t == nil {
		return httperr.NotFound("team not found")
	}
	u, err := s.Store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u == nil || u.TenantID != tenantID {
		return httperr.NotFound("user not found")
	}
	return s.Store.AddUserToTeam(ctx, &model.UserTeam{UserID: userID, TeamID: teamID})
}

// RemoveUserFromTeam unbinds a user from a tenant team.
func (s *Service) RemoveUserFromTeam(ctx context.Context, tenantID, teamID, userID string) error {
	t, err := s.Store.GetTeam(ctx, tenantID, teamID)
	if err != nil {
		return err
	}
	if t == nil {
		return httperr.NotFound("team not found")
	}
	return s.Store.RemoveUserFromTeam(ctx, teamID, userID)
}

// ListTeamUsers returns the members of a tenant team.
func (s *Service) ListTeamUsers(ctx context.Context, tenantID, teamID string) ([]model.User, error) {
	t, err := s.Store.GetTeam(ctx, tenantID, teamID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, httperr.NotFound("team not found")
	}
	return s.Store.ListTeamUsers(ctx, tenantID, teamID)
}

// ListTeamProjects returns the projects bound to a tenant team.
func (s *Service) ListTeamProjects(ctx context.Context, tenantID, teamID string) ([]model.Project, error) {
	if _, err := s.requireTeam(ctx, tenantID, teamID); err != nil {
		return nil, err
	}
	return s.Store.ListTeamProjects(ctx, tenantID, teamID)
}

// SetTeamProjects replaces a team's bound projects. Every project must belong
// to the same tenant before it can be authorized to a team.
func (s *Service) SetTeamProjects(ctx context.Context, tenantID, teamID string, projectIDs []string) error {
	if _, err := s.requireTeam(ctx, tenantID, teamID); err != nil {
		return err
	}
	ids := uniqueStrings(projectIDs)
	if len(ids) > 0 {
		projects, err := s.Store.ListProjectsByIDs(ctx, tenantID, ids)
		if err != nil {
			return err
		}
		if len(projects) != len(ids) {
			return httperr.BadRequest(40060, "one or more projects not found in tenant")
		}
	}
	return s.Store.SetTeamProjects(ctx, tenantID, teamID, ids)
}

func (s *Service) requireTeam(ctx context.Context, tenantID, teamID string) (*model.Team, error) {
	t, err := s.Store.GetTeam(ctx, tenantID, teamID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, httperr.NotFound("team not found")
	}
	return t, nil
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func (s *Service) revokeTeamAdminIfUnused(ctx context.Context, userID string) error {
	owned, err := s.Store.ListTeamIDsOwnedBy(ctx, userID)
	if err != nil {
		return err
	}
	if len(owned) == 0 {
		return s.Store.RemoveUserRole(ctx, userID, model.RoleTeamAdmin)
	}
	return nil
}

// AuthorizeTeamManage checks the caller may manage a team: it must hold the
// manage team permission and, unless a platform/tenant admin, must be the
// team owner (the team_admin boundary).
func (s *Service) AuthorizeTeamManage(ctx context.Context, actorID, tenantID, teamID string) error {
	if err := s.Authorize(ctx, actorID, "manage", "team"); err != nil {
		return err
	}
	ac, err := s.authz(ctx, actorID)
	if err != nil {
		return err
	}
	if ac.platform || ac.user.Role == model.RoleTenantAdmin {
		return nil
	}
	owned, err := s.Store.IsTeamOwner(ctx, actorID, teamID)
	if err != nil {
		return err
	}
	if !owned {
		return ErrForbidden
	}
	return nil
}

// AuthorizeTeamRead checks the caller may view a team: the caller must hold
// the read team permission and, unless a platform/tenant admin, must own the
// team or be a member of it.
func (s *Service) AuthorizeTeamRead(ctx context.Context, actorID, tenantID, teamID string) error {
	if err := s.Authorize(ctx, actorID, "read", "team"); err != nil {
		return err
	}
	ac, err := s.authz(ctx, actorID)
	if err != nil {
		return err
	}
	if ac.platform || ac.user.Role == model.RoleTenantAdmin {
		return nil
	}
	owned, err := s.Store.IsTeamOwner(ctx, actorID, teamID)
	if err != nil {
		return err
	}
	if owned {
		return nil
	}
	member, err := s.Store.ListUserTeamIDs(ctx, actorID)
	if err != nil {
		return err
	}
	for _, id := range member {
		if id == teamID {
			return nil
		}
	}
	return ErrForbidden
}
