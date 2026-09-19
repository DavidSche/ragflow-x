package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// CreateProject creates a tenant-scoped knowledge domain.
func (s *Service) CreateProject(ctx context.Context, tenantID, name, description string) (*model.Project, error) {
	name, err := normalizeDisplayName(name, "project name is required", 40070)
	if err != nil {
		return nil, err
	}
	existing, err := s.Store.GetProjectByName(ctx, tenantID, name, "")
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, displayNameConflict("project")
	}
	p := &model.Project{ID: id.New(), TenantID: tenantID, Name: name, Description: description}
	if err := s.Store.CreateProject(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// GetProject returns a single tenant project.
func (s *Service) GetProject(ctx context.Context, tenantID, id string) (*model.Project, error) {
	p, err := s.Store.GetProject(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, httperr.NotFound("project not found")
	}
	return p, nil
}

// UpdateProject updates a tenant project's name and description.
func (s *Service) UpdateProject(ctx context.Context, tenantID, id, name, description string) (*model.Project, error) {
	name, err := normalizeDisplayName(name, "project name is required", 40070)
	if err != nil {
		return nil, err
	}
	p, err := s.Store.GetProject(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, httperr.NotFound("project not found")
	}
	existing, err := s.Store.GetProjectByName(ctx, tenantID, name, id)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, displayNameConflict("project")
	}
	p.Name = name
	p.Description = description
	if err := s.Store.UpdateProject(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

// ListProjects returns the tenant's projects. Non-admin callers only see the
// projects they are a member of, so project metadata is not disclosed.
func (s *Service) ListProjects(ctx context.Context, tenantID, userID, role string) ([]model.Project, error) {
	if role == model.RolePlatformAdmin || role == model.RoleTenantAdmin {
		return s.Store.ListProjects(ctx, tenantID)
	}
	direct, err := s.Store.ListProjectsByMember(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	var bound, owned []model.Project
	if teamIDs, err := s.Store.ListUserTeamIDs(ctx, userID); err != nil {
		return nil, err
	} else if pids, err := s.Store.ListProjectIDsBoundToTeams(ctx, teamIDs); err != nil {
		return nil, err
	} else if len(pids) > 0 {
		if bound, err = s.Store.ListProjectsByIDs(ctx, tenantID, pids); err != nil {
			return nil, err
		}
	}
	if ownedTeamIDs, err := s.Store.ListTeamIDsOwnedBy(ctx, userID); err != nil {
		return nil, err
	} else if pids, err := s.Store.ListProjectIDsBoundToTeams(ctx, ownedTeamIDs); err != nil {
		return nil, err
	} else if len(pids) > 0 {
		if owned, err = s.Store.ListProjectsByIDs(ctx, tenantID, pids); err != nil {
			return nil, err
		}
	}
	return mergeProjects(direct, bound, owned), nil
}

// DeleteProject removes a tenant project.
func (s *Service) DeleteProject(ctx context.Context, tenantID, id string) error {
	p, err := s.Store.GetProject(ctx, tenantID, id)
	if err != nil {
		return err
	}
	if p == nil {
		return httperr.NotFound("project not found")
	}
	if err := s.Store.DeleteProject(ctx, tenantID, id); err != nil {
		return err
	}
	return s.Store.DeleteTeamProjectsByProject(ctx, id)
}

// AddProjectUser adds a tenant user to a project's membership (project ABAC).
func (s *Service) AddProjectUser(ctx context.Context, tenantID, projectID, userID string) error {
	p, err := s.Store.GetProject(ctx, tenantID, projectID)
	if err != nil {
		return err
	}
	if p == nil {
		return httperr.NotFound("project not found")
	}
	u, err := s.Store.GetUser(ctx, userID)
	if err != nil {
		return err
	}
	if u == nil || u.TenantID != tenantID {
		return httperr.NotFound("user not found")
	}
	return s.Store.AddProjectMember(ctx, userID, projectID)
}

// RemoveProjectUser removes a user from a project.
func (s *Service) RemoveProjectUser(ctx context.Context, tenantID, projectID, userID string) error {
	p, err := s.Store.GetProject(ctx, tenantID, projectID)
	if err != nil {
		return err
	}
	if p == nil {
		return httperr.NotFound("project not found")
	}
	return s.Store.RemoveProjectMember(ctx, projectID, userID)
}

// ListProjectUsers returns project members.
func (s *Service) ListProjectUsers(ctx context.Context, tenantID, projectID string) ([]model.User, error) {
	p, err := s.Store.GetProject(ctx, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, httperr.NotFound("project not found")
	}
	direct, err := s.Store.ListProjectUsers(ctx, projectID)
	if err != nil {
		return nil, err
	}
	viaTeams, err := s.Store.ListProjectUsersViaTeams(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return mergeUsers(direct, viaTeams), nil
}

// BindDatasetProject assigns a dataset to a tenant project.
func (s *Service) BindDatasetProject(ctx context.Context, tenantID, datasetID, projectID string) error {
	if projectID == "" {
		return s.Store.SetDatasetProject(ctx, tenantID, datasetID, "")
	}
	p, err := s.Store.GetProject(ctx, tenantID, projectID)
	if err != nil {
		return err
	}
	if p == nil {
		return httperr.NotFound("project not found")
	}
	return s.Store.SetDatasetProject(ctx, tenantID, datasetID, projectID)
}

// AuthorizeProject extends RBAC with project-level ABAC: the caller must hold
// the RBAC permission AND belong to the project (or the same tenant), unless a
// platform role is involved.
func (s *Service) AuthorizeProject(ctx context.Context, userID, action, resource, tenantID, projectID string) error {
	if err := s.Authorize(ctx, userID, action, resource); err != nil {
		return err
	}
	ac, err := s.authz(ctx, userID)
	if err != nil {
		return err
	}
	if ac.platform {
		return nil
	}
	if ac.user.TenantID != tenantID {
		return ErrForbidden
	}
	if projectID == "" {
		return nil
	}
	p, err := s.Store.GetProject(ctx, tenantID, projectID)
	if err != nil {
		return err
	}
	if p == nil {
		return httperr.NotFound("project not found")
	}
	// tenant admins manage all projects within their tenant
	if ac.user.Role == model.RoleTenantAdmin {
		return nil
	}
	if err != nil {
		return err
	}
	isMember, err := s.projectMember(ctx, userID, projectID)
	if err != nil {
		return err
	}
	if !isMember {
		return ErrForbidden
	}
	return nil
}

// projectMember reports whether the user is a project member through direct
// membership OR membership in a team that is bound to the project.
func (s *Service) projectMember(ctx context.Context, userID, projectID string) (bool, error) {
	ok, err := s.Store.IsProjectMember(ctx, userID, projectID)
	if err != nil || ok {
		return ok, err
	}
	teamIDs, err := s.Store.ListUserTeamIDs(ctx, userID)
	if err != nil {
		return false, err
	}
	return s.Store.IsProjectBoundToTeams(ctx, projectID, teamIDs)
}

// isProjectBoundToOwnedTeam reports whether the project is authorized to a
// team the user administers (owner boundary for team_admin).
func (s *Service) isProjectBoundToOwnedTeam(ctx context.Context, userID, projectID string) (bool, error) {
	teamIDs, err := s.Store.ListTeamIDsOwnedBy(ctx, userID)
	if err != nil {
		return false, err
	}
	return s.Store.IsProjectBoundToTeams(ctx, projectID, teamIDs)
}

func mergeProjects(parts ...[]model.Project) []model.Project {
	seen := map[string]bool{}
	out := make([]model.Project, 0)
	for _, part := range parts {
		for _, p := range part {
			if seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			out = append(out, p)
		}
	}
	return out
}

func mergeUsers(parts ...[]model.User) []model.User {
	seen := map[string]bool{}
	out := make([]model.User, 0)
	for _, part := range parts {
		for _, u := range part {
			if seen[u.ID] {
				continue
			}
			seen[u.ID] = true
			out = append(out, u)
		}
	}
	return out
}

// ProjectDatasetView is a tenant dataset link annotated with whether it is
// bound to the current project (drives the project's binding management page).
type ProjectDatasetView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ProjectID string `json:"project_id"`
	Bound     bool   `json:"bound"`
}

// ProjectTeamView is a tenant team annotated with its project binding state.
type ProjectTeamView struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Bound bool   `json:"bound"`
}

// ListProjectDatasets returns all tenant datasets with their binding to the
// given project so the UI can render bound/unbound checkboxes.
func (s *Service) ListProjectDatasets(ctx context.Context, tenantID, projectID string) ([]ProjectDatasetView, error) {
	p, err := s.Store.GetProject(ctx, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, httperr.NotFound("project not found")
	}
	links, err := s.Store.ListByTenant(ctx, tenantID, repository.DatasetFilter{})
	if err != nil {
		return nil, err
	}
	out := make([]ProjectDatasetView, 0, len(links))
	for _, l := range links {
		out = append(out, ProjectDatasetView{ID: l.ID, Name: l.Name, ProjectID: l.ProjectID, Bound: l.ProjectID == projectID})
	}
	return out, nil
}

// BindProjectDatasets assigns the selected datasets to the project (a dataset
// belongs to at most one project) and unbinds those that were bound to this
// project but are no longer selected.
func (s *Service) BindProjectDatasets(ctx context.Context, tenantID, projectID string, datasetIDs []string) error {
	p, err := s.Store.GetProject(ctx, tenantID, projectID)
	if err != nil {
		return err
	}
	if p == nil {
		return httperr.NotFound("project not found")
	}
	selected := map[string]bool{}
	for _, id := range datasetIDs {
		selected[id] = true
	}
	links, err := s.Store.ListByTenant(ctx, tenantID, repository.DatasetFilter{})
	if err != nil {
		return err
	}
	for _, l := range links {
		desired := ""
		if selected[l.ID] {
			desired = projectID
		}
		if l.ProjectID != desired {
			if err := s.Store.SetDatasetProject(ctx, tenantID, l.ID, desired); err != nil {
				return err
			}
		}
	}
	return nil
}

// ListProjectTeams returns all tenant teams annotated with their binding to
// the given project.
func (s *Service) ListProjectTeams(ctx context.Context, tenantID, projectID string) ([]ProjectTeamView, error) {
	p, err := s.Store.GetProject(ctx, tenantID, projectID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, httperr.NotFound("project not found")
	}
	teams, err := s.Store.ListTeams(ctx, tenantID, repository.TeamFilter{})
	if err != nil {
		return nil, err
	}
	boundIDs, err := s.Store.ListTeamIDsByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	bound := map[string]bool{}
	for _, id := range boundIDs {
		bound[id] = true
	}
	out := make([]ProjectTeamView, 0, len(teams))
	for _, t := range teams {
		out = append(out, ProjectTeamView{ID: t.ID, Name: t.Name, Bound: bound[t.ID]})
	}
	return out, nil
}

// BindProjectTeams replaces the project's bound teams (authorization mirror of
// the team page's bindings).
func (s *Service) BindProjectTeams(ctx context.Context, tenantID, projectID string, teamIDs []string) error {
	p, err := s.Store.GetProject(ctx, tenantID, projectID)
	if err != nil {
		return err
	}
	if p == nil {
		return httperr.NotFound("project not found")
	}
	for _, tid := range teamIDs {
		t, err := s.Store.GetTeam(ctx, tenantID, tid)
		if err != nil {
			return err
		}
		if t == nil {
			return httperr.NotFound("team not found")
		}
	}
	return s.Store.SetProjectTeams(ctx, tenantID, projectID, teamIDs)
}
