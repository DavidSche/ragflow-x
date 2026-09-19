package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestTeamAdminOwnerBoundary(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	ta, _ := svc.CreateTenant(ctx, "TenantA")
	ownerA, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "ownerA", Password: "secret123", Role: model.RoleOperator})
	ownerB, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "ownerB", Password: "secret123", Role: model.RoleOperator})

	teamA, err := svc.CreateTeam(ctx, ta.ID, "TeamA", ownerA.ID)
	if err != nil {
		t.Fatal(err)
	}
	teamB, err := svc.CreateTeam(ctx, ta.ID, "TeamB", ownerB.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Owner A is granted team_admin; member/owner boundary applies.
	roles, _ := svc.Store.ListRolesByUser(ctx, ownerA.ID)
	if !hasRole(roles, model.RoleTeamAdmin) {
		t.Fatal("team owner should be granted team_admin")
	}
	if err := svc.AuthorizeTeamManage(ctx, ownerA.ID, ta.ID, teamA.ID); err != nil {
		t.Fatalf("owner should manage own team: %v", err)
	}
	if err := svc.AuthorizeTeamManage(ctx, ownerA.ID, ta.ID, teamB.ID); err == nil {
		t.Fatal("owner must not manage another team")
	}

	// Non-owner operator has no manage-team permission at all.
	op, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "op", Password: "secret123", Role: model.RoleOperator})
	if err := svc.AuthorizeTeamManage(ctx, op.ID, ta.ID, teamA.ID); err == nil {
		t.Fatal("operator (non-owner) must not manage any team")
	}
	if err := svc.AuthorizeTeamRead(ctx, op.ID, ta.ID, teamA.ID); err == nil {
		t.Fatal("operator non-member must not read a team it does not belong to")
	}
	if err := svc.AuthorizeTeamRead(ctx, ownerA.ID, ta.ID, teamA.ID); err != nil {
		t.Fatalf("owner should read own team: %v", err)
	}
}

// ScenarioID: SC-PROJ-001
func TestP0_PROJ_001_TeamAdminBoundProjectManage(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	ta, _ := svc.CreateTenant(ctx, "TenantA")
	owner, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "owner", Password: "secret123", Role: model.RoleOperator})
	team, _ := svc.CreateTeam(ctx, ta.ID, "TeamA", owner.ID)
	p1, _ := svc.CreateProject(ctx, ta.ID, "P1", "bound")
	p2, _ := svc.CreateProject(ctx, ta.ID, "P2", "unbound")
	if err := svc.SetTeamProjects(ctx, ta.ID, team.ID, []string{p1.ID}); err != nil {
		t.Fatal(err)
	}

	// Team owner manages datasets in a project bound to their team (ABAC).
	if err := svc.AuthorizeABAC(ctx, owner.ID, "manage", "dataset", ta.ID, p1.ID, ""); err != nil {
		t.Fatalf("team admin should manage dataset in bound project: %v", err)
	}
	if err := svc.AuthorizeABAC(ctx, owner.ID, "manage", "dataset", ta.ID, p2.ID, ""); err == nil {
		t.Fatal("team admin must not manage dataset in unbound project")
	}

	// The team owner sees the bound project in their project list.
	seen, err := svc.ListProjects(ctx, ta.ID, owner.ID, model.RoleTeamAdmin)
	if err != nil || len(seen) != 1 || seen[0].ID != p1.ID {
		t.Fatalf("team owner should see bound project only: %+v err=%v", seen, err)
	}
}

func TestTeamAdminTransferAndRevoke(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	ta, _ := svc.CreateTenant(ctx, "TenantA")
	a, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "a", Password: "secret123", Role: model.RoleOperator})
	b, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "b", Password: "secret123", Role: model.RoleOperator})
	team, _ := svc.CreateTeam(ctx, ta.ID, "TeamA", a.ID)

	if _, err := svc.UpdateTeam(ctx, ta.ID, team.ID, "", b.ID); err != nil {
		t.Fatal(err)
	}
	rolesB, _ := svc.Store.ListRolesByUser(ctx, b.ID)
	if !hasRole(rolesB, model.RoleTeamAdmin) {
		t.Fatal("new owner should be granted team_admin")
	}
	rolesA, _ := svc.Store.ListRolesByUser(ctx, a.ID)
	if hasRole(rolesA, model.RoleTeamAdmin) {
		t.Fatal("former owner with no remaining teams should lose team_admin")
	}
	// tenant admin can still view the team details.
	if _, err := svc.GetTeamView(ctx, ta.ID, team.ID); err != nil {
		t.Fatal(err)
	}
}

func hasRole(roles []model.Role, id string) bool {
	for _, r := range roles {
		if r.ID == id {
			return true
		}
	}
	return false
}
