package service

import (
	"context"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-PROJ-001
func TestP0_PROJ_001_AuthorizeProjectABAC(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := svc.CreateTenant(ctx, "TenantB")
	if err != nil {
		t.Fatal(err)
	}
	adminA, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "adminA", Password: "secret123", Role: "tenant_admin"})
	opMember, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "opMember", Password: "secret123", Role: "operator"})
	opOther, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "opOther", Password: "secret123", Role: "operator"})
	foreign, _ := svc.CreateUser(ctx, tb.ID, "", CreateUserRequest{Username: "foreign", Password: "secret123", Role: "tenant_admin"})

	proj, err := svc.CreateProject(ctx, ta.ID, "P1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddProjectUser(ctx, ta.ID, proj.ID, opMember.ID); err != nil {
		t.Fatal(err)
	}

	// tenant admin manages all tenant projects
	if err := svc.AuthorizeProject(ctx, adminA.ID, "read", "dataset", ta.ID, proj.ID); err != nil {
		t.Fatalf("tenant admin should manage project: %v", err)
	}
	// project member has access
	if err := svc.AuthorizeProject(ctx, opMember.ID, "read", "dataset", ta.ID, proj.ID); err != nil {
		t.Fatalf("project member should have access: %v", err)
	}
	// same-tenant non-member denied (ABAC project ownership)
	if err := svc.AuthorizeProject(ctx, opOther.ID, "read", "dataset", ta.ID, proj.ID); err == nil {
		t.Fatal("non-member should be forbidden")
	}
	// cross-tenant denied
	if err := svc.AuthorizeProject(ctx, foreign.ID, "read", "dataset", ta.ID, proj.ID); err == nil {
		t.Fatal("cross-tenant should be forbidden")
	}

	// bind a dataset to the project
	ds, err := svc.CreateDataset(ctx, ta.ID, "kb")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.BindDatasetProject(ctx, ta.ID, ds.ID, proj.ID); err != nil {
		t.Fatalf("bind dataset: %v", err)
	}
}

func TestAuditHashChain(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, _ := svc.CreateTenant(ctx, "TenantA")
	u, _ := svc.CreateUser(ctx, ta.ID, "", CreateUserRequest{Username: "op", Password: "secret123", Role: "operator"})

	_ = svc.RecordAudit(ctx, &model.AuditLog{TenantID: ta.ID, UserID: u.ID, Action: "dataset.create", Resource: "dataset"})
	_ = svc.RecordAudit(ctx, &model.AuditLog{TenantID: ta.ID, UserID: u.ID, Action: "dataset.delete", Resource: "dataset", ActingContextID: "acting-context-1"})

	rows, err := svc.Store.ListAuditsAll(ctx, ta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) < 2 {
		t.Fatalf("expected at least 2 audit rows, got %d", len(rows))
	}
	if rows[0].Hash == "" || rows[1].Hash == "" || rows[1].PrevHash != rows[0].Hash {
		t.Fatalf("audit chain broken: rows=%+v", rows)
	}
	if rows[1].ActingContextID != "acting-context-1" {
		t.Fatalf("acting context identity missing from audit: %+v", rows[1])
	}
	valid, err := svc.VerifyAuditChain(ctx, ta.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !valid {
		t.Fatal("audit chain should verify as intact")
	}
}

// ScenarioID: SC-SECRET-001
func TestP0_SECRET_001_AuditSecretRedaction(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, _ := svc.CreateTenant(ctx, "Audit Secret Workspace")
	if err := svc.RecordAudit(ctx, &model.AuditLog{
		TenantID: tenant.ID, Action: "provider.test", Resource: "model-provider",
		DetailJSON: `{"api_key":"sk-live-secret","nested":{"password":"plain"},"safe":"ok"}`,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.Store.ListAuditsAll(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one audit row, got %d", len(rows))
	}
	detail := rows[0].DetailJSON
	if strings.Contains(detail, "sk-live-secret") || strings.Contains(detail, "plain") {
		t.Fatalf("audit secret leaked: %s", detail)
	}
	if !strings.Contains(detail, `[REDACTED]`) || !strings.Contains(detail, `"safe":"ok"`) {
		t.Fatalf("safe audit detail was not preserved: %s", detail)
	}
}
