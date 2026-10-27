package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestOperationalAttributionReportAuthorization(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Operational Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "op-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	viewerRole, err := svc.CreateRole(ctx, admin.ID, admin.Role, CreateRoleRequest{
		Name: "no-usage", Scope: model.RoleScopeTenant,
	})
	if err != nil {
		t.Fatal(err)
	}
	viewer, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "op-viewer", Password: "secret123", Role: viewerRole.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertKnowledgeOpsEvent(ctx, &model.KnowledgeOpsEvent{
		RequestID: "operational-req", TenantID: tenant.ID, UserID: admin.ID,
		AppType: "chat", AppID: "chat-1", Status: model.KnowledgeOpsCompleted,
		DurationMs: 80, TokensIn: 10, TokensOut: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store.RecordCostMetric(ctx, &model.CostMetric{
		RequestID: "operational-req", TenantID: tenant.ID, UserID: admin.ID,
		Date: "2026-09-20", Model: "qwen-max", Scenario: "chat",
		TokensIn: 10, TokensOut: 5, EstimatedCost: 0.003,
		Estimated: true, EstimationPolicyVersion: "v1",
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := svc.OperationalAttributionReport(ctx, admin.ID, tenant.ID, repository.OperationalReportFilter{})
	if err != nil {
		t.Fatalf("tenant admin report: %v", err)
	}
	if len(rows) != 1 || rows[0].Requests != 1 || rows[0].TokensIn != 10 ||
		rows[0].EstimatedCost != 0.003 {
		t.Fatalf("unexpected operational report: %+v", rows)
	}
	if _, err := svc.OperationalAttributionReport(ctx, viewer.ID, tenant.ID, repository.OperationalReportFilter{}); err == nil {
		t.Fatal("user without usage read must be forbidden")
	}
}
