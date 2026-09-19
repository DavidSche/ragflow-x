package service

import (
	"context"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

func assertDisplayNameConflict(t *testing.T, err error, resource string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s name conflict", resource)
	}
	conflict, ok := err.(*httperr.Error)
	if !ok || conflict.Status != 409 || conflict.Code != 40911 {
		t.Fatalf("expected 409/40911 for %s, got %v", resource, err)
	}
}

func TestDisplayNameValidationAndUniqueness(t *testing.T) {
	ctx := context.Background()
	svc := newUninitializedSvc(t)

	if _, err := svc.CreateTenant(ctx, "  Demo Workspace  "); err != nil {
		t.Fatal(err)
	}
	tenant, err := svc.Store.GetTenantByName(ctx, "demo workspace")
	if err != nil || tenant == nil {
		t.Fatalf("trimmed workspace not found: %v", err)
	}
	if _, err := svc.CreateTenant(ctx, "demo workspace"); err == nil {
		t.Fatal("expected duplicate workspace name to fail")
	} else {
		assertDisplayNameConflict(t, err, "workspace")
	}

	if _, err := svc.CreateDataset(ctx, tenant.ID, strings.Repeat("长", 129)); err == nil {
		t.Fatal("expected over-long dataset name to fail")
	}
	if _, err := svc.CreateDataset(ctx, tenant.ID, "Policy KB"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateDataset(ctx, tenant.ID, " policy kb "); err == nil {
		t.Fatal("expected duplicate dataset name to fail")
	} else {
		assertDisplayNameConflict(t, err, "dataset")
	}

	dsl := map[string]interface{}{"nodes": []interface{}{}}
	if _, err := svc.CreateAgent(ctx, tenant.ID, "Risk Agent", dsl, false, model.AgentCanvasCategoryWorkflow); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateAgent(ctx, tenant.ID, "risk agent", dsl, false, model.AgentCanvasCategoryWorkflow); err == nil {
		t.Fatal("expected duplicate agent title to fail")
	} else {
		assertDisplayNameConflict(t, err, "agent")
	}

	if _, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "Alice", Password: "secret123", Email: "alice@example.com", Role: model.RoleOperator,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "alice", Password: "secret123", Role: model.RoleOperator,
	}); err == nil {
		t.Fatal("expected duplicate username to fail")
	}
	if _, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "alice-2", Password: "secret123", Email: "ALICE@example.com", Role: model.RoleOperator,
	}); err == nil {
		t.Fatal("expected duplicate email to fail")
	}

	if _, err := svc.CreateProject(ctx, tenant.ID, "Ops Project", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateProject(ctx, tenant.ID, " ops project ", ""); err == nil {
		t.Fatal("expected duplicate project name to fail")
	} else {
		assertDisplayNameConflict(t, err, "project")
	}
	if _, err := svc.CreateTeam(ctx, tenant.ID, "Ops Team", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateTeam(ctx, tenant.ID, "ops team", ""); err == nil {
		t.Fatal("expected duplicate team name to fail")
	} else {
		assertDisplayNameConflict(t, err, "team")
	}
}
