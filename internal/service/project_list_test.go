package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestProjectListMemberVisible(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	p1, err := svc.CreateProject(ctx, ta.ID, "P1", "domain one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateProject(ctx, ta.ID, "P2", "domain two"); err != nil {
		t.Fatal(err)
	}

	admin, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "ta1", Password: "secret123", Role: model.RoleTenantAdmin})
	member, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "member", Password: "secret123", Role: model.RoleOperator})
	outsider, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "outsider", Password: "secret123", Role: model.RoleOperator})
	if err := svc.AddProjectUser(ctx, ta.ID, p1.ID, member.ID); err != nil {
		t.Fatal(err)
	}

	// tenant admin sees all tenant projects.
	all, err := svc.ListProjects(ctx, ta.ID, admin.ID, model.RoleTenantAdmin)
	if err != nil || len(all) != 2 {
		t.Fatalf("tenant admin should see all projects: n=%d err=%v", len(all), err)
	}

	// A member only sees the project they belong to.
	seen, err := svc.ListProjects(ctx, ta.ID, member.ID, model.RoleOperator)
	if err != nil || len(seen) != 1 || seen[0].ID != p1.ID {
		t.Fatalf("member should only see own project: %+v err=%v", seen, err)
	}

	// A non-member sees no projects at all.
	none, err := svc.ListProjects(ctx, ta.ID, outsider.ID, model.RoleOperator)
	if err != nil || len(none) != 0 {
		t.Fatalf("non-member should see no projects: %+v err=%v", none, err)
	}
}

func TestProjectUpdate(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.CreateProject(ctx, ta.ID, "P1", "domain one")
	if err != nil {
		t.Fatal(err)
	}

	updated, err := svc.UpdateProject(ctx, ta.ID, p.ID, "P1 renamed", "domain two")
	if err != nil {
		t.Fatalf("update project: %v", err)
	}
	if updated.Name != "P1 renamed" || updated.Description != "domain two" {
		t.Fatalf("unexpected updated project: %+v", updated)
	}
	got, err := svc.GetProject(ctx, ta.ID, p.ID)
	if err != nil || got.Name != "P1 renamed" {
		t.Fatalf("get after update: %+v err=%v", got, err)
	}

	if _, err := svc.UpdateProject(ctx, ta.ID, p.ID, "  ", "x"); err == nil {
		t.Fatal("empty project name should be rejected")
	}
	if _, err := svc.UpdateProject(ctx, ta.ID, "missing", "P", "x"); err == nil {
		t.Fatal("updating a missing project should fail")
	}
	if _, err := svc.GetProject(ctx, ta.ID, "missing"); err == nil {
		t.Fatal("getting a missing project should fail")
	}

	// Tenant isolation: another tenant must not see or update this project.
	tb, err := svc.CreateTenant(ctx, "TenantB")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := svc.GetProject(ctx, tb.ID, p.ID); err == nil || got != nil {
		t.Fatalf("tenant B must not read tenant A's project: got=%+v err=%v", got, err)
	}
	if _, err := svc.UpdateProject(ctx, tb.ID, p.ID, "stolen", "x"); err == nil {
		t.Fatal("tenant B must not update tenant A's project")
	}
}
