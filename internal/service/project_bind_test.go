package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestProjectBindingManagement(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.CreateProject(ctx, ta.ID, "P1", "domain")
	if err != nil {
		t.Fatal(err)
	}
	team, err := svc.CreateTeam(ctx, ta.ID, "TeamA", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{ID: "ds-1", TenantID: ta.ID, RAGFlowDatasetID: "rf-1", Name: "KB"}); err != nil {
		t.Fatal(err)
	}

	// Initially nothing is bound.
	datasets, err := svc.ListProjectDatasets(ctx, ta.ID, p.ID)
	if err != nil || len(datasets) != 1 || datasets[0].Bound {
		t.Fatalf("dataset should be unbound: %+v err=%v", datasets, err)
	}
	teams, err := svc.ListProjectTeams(ctx, ta.ID, p.ID)
	if err != nil || len(teams) != 1 || teams[0].Bound {
		t.Fatalf("team should be unbound: %+v err=%v", teams, err)
	}

	// Bind a dataset and a team to the project.
	if err := svc.BindProjectDatasets(ctx, ta.ID, p.ID, []string{"ds-1"}); err != nil {
		t.Fatalf("bind dataset: %v", err)
	}
	if err := svc.BindProjectTeams(ctx, ta.ID, p.ID, []string{team.ID}); err != nil {
		t.Fatalf("bind team: %v", err)
	}

	datasets, _ = svc.ListProjectDatasets(ctx, ta.ID, p.ID)
	if len(datasets) != 1 || !datasets[0].Bound {
		t.Fatalf("dataset should now be bound: %+v", datasets)
	}
	teams, _ = svc.ListProjectTeams(ctx, ta.ID, p.ID)
	if len(teams) != 1 || !teams[0].Bound {
		t.Fatalf("team should now be bound: %+v", teams)
	}

	// Unbind by leaving the set empty.
	if err := svc.BindProjectDatasets(ctx, ta.ID, p.ID, nil); err != nil {
		t.Fatal(err)
	}
	datasets, _ = svc.ListProjectDatasets(ctx, ta.ID, p.ID)
	if len(datasets) != 1 || datasets[0].Bound {
		t.Fatalf("dataset should be unbound after empty set: %+v", datasets)
	}

	// Team-scoped member derivation: user in the bound team is a project member.
	member, err := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "member", Password: "secret123", Role: model.RoleOperator})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.BindProjectTeams(ctx, ta.ID, p.ID, []string{team.ID}); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddUserToTeam(ctx, ta.ID, team.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	members, err := svc.ListProjectUsers(ctx, ta.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !containsUser(members, member.ID) {
		t.Fatalf("team member should be derived as project member: %+v", members)
	}

	// Cross-tenant isolation.
	tb, err := svc.CreateTenant(ctx, "TenantB")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListProjectDatasets(ctx, tb.ID, p.ID); err == nil {
		t.Fatal("tenant B must not read tenant A's project datasets")
	}
	if err := svc.BindProjectTeams(ctx, tb.ID, p.ID, []string{team.ID}); err == nil {
		t.Fatal("tenant B must not bind teams to tenant A's project")
	}
}

func containsUser(users []model.User, id string) bool {
	for _, u := range users {
		if u.ID == id {
			return true
		}
	}
	return false
}
