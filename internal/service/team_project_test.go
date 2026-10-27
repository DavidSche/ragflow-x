package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ScenarioID: SC-PROJ-001
func TestP0_PROJ_001_TeamProjectBindingMembershipDerivation(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	p1, err := svc.CreateProject(ctx, ta.ID, "P1", "team-bound")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := svc.CreateProject(ctx, ta.ID, "P2", "unbound")
	if err != nil {
		t.Fatal(err)
	}

	member, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "member", Password: "secret123", Role: model.RoleOperator})
	outsider, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "outsider", Password: "secret123", Role: model.RoleOperator})

	team, err := svc.CreateTeam(ctx, ta.ID, "Core", member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddUserToTeam(ctx, ta.ID, team.ID, member.ID); err != nil {
		t.Fatal(err)
	}

	// No binding yet -> member sees nothing.
	none, err := svc.ListProjects(ctx, ta.ID, member.ID, model.RoleOperator)
	if err != nil || len(none) != 0 {
		t.Fatalf("bound before binding: %+v err=%v", none, err)
	}

	// Bind team to p1.
	if err := svc.SetTeamProjects(ctx, ta.ID, team.ID, []string{p1.ID}); err != nil {
		t.Fatal(err)
	}
	bound, err := svc.ListTeamProjects(ctx, ta.ID, team.ID)
	if err != nil || len(bound) != 1 || bound[0].ID != p1.ID {
		t.Fatalf("ListTeamProjects: %+v err=%v", bound, err)
	}
	teamViews, err := svc.ListTeams(ctx, ta.ID, member.ID, model.RoleOperator, repository.TeamFilter{})
	if err != nil || len(teamViews) != 1 || teamViews[0].ProjectCount != 1 {
		t.Fatalf("team view project count: %+v err=%v", teamViews, err)
	}

	// membership derivation through the team
	if ids, _ := svc.Store.ListUserTeamIDs(ctx, member.ID); len(ids) != 1 {
		t.Fatalf("ListUserTeamIDs unexpected: %v", ids)
	}
	seen, err := svc.ListProjects(ctx, ta.ID, member.ID, model.RoleOperator)
	if err != nil || len(seen) != 1 || seen[0].ID != p1.ID {
		t.Fatalf("member should see team-bound project only: %+v err=%v", seen, err)
	}
	outsiderList, err := svc.ListProjects(ctx, ta.ID, outsider.ID, model.RoleOperator)
	if err != nil || len(outsiderList) != 0 {
		t.Fatalf("outsider should see nothing: %+v err=%v", outsiderList, err)
	}

	mem1, err := svc.projectMember(ctx, member.ID, p1.ID)
	if err != nil || !mem1 {
		t.Fatalf("member should access p1 via team: %v err=%v", mem1, err)
	}
	mem2, err := svc.projectMember(ctx, member.ID, p2.ID)
	if err != nil || mem2 {
		t.Fatalf("member must not access unbound p2: %v err=%v", mem2, err)
	}
	if oo, _ := svc.projectMember(ctx, outsider.ID, p1.ID); oo {
		t.Fatal("outsider must not access p1")
	}

	users, err := svc.ListProjectUsers(ctx, ta.ID, p1.ID)
	if err != nil || !hasUser(users, member.ID) {
		t.Fatalf("team member should appear in project members: %+v err=%v", users, err)
	}

	// Direct + team union: also make member a direct member of p2.
	if err := svc.AddProjectUser(ctx, ta.ID, p2.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	all, err := svc.ListProjects(ctx, ta.ID, member.ID, model.RoleOperator)
	if err != nil || len(all) != 2 {
		t.Fatalf("member should see team-bound and directly-owned projects: %+v err=%v", all, err)
	}

	// Unbind -> member loses access to p1 once more.
	if err := svc.SetTeamProjects(ctx, ta.ID, team.ID, nil); err != nil {
		t.Fatal(err)
	}
	if m1, _ := svc.projectMember(ctx, member.ID, p1.ID); m1 {
		t.Fatal("member should lose access to p1 after unbinding")
	}
}

func hasUser(users []model.User, id string) bool {
	for _, u := range users {
		if u.ID == id {
			return true
		}
	}
	return false
}
