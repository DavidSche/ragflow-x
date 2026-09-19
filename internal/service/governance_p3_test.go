package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestAuditGovernanceFiltersUseStructuredColumns(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenantID := "audit-tenant"
	rows := []struct {
		action     string
		resource   string
		target     string
		result     string
		approvalID string
		actingID   string
	}{
		{action: "dataset.update", resource: "dataset", target: tenantID, result: "SUCCESS"},
		{action: "dataset.update", resource: "dataset", target: "other-workspace", result: "DENIED"},
		{action: "approval.decision", resource: "approval", target: tenantID, result: "SUCCESS", approvalID: "approval-1"},
		{action: "approval.executed", resource: "approval", target: tenantID, result: "SUCCESS", approvalID: "approval-2", actingID: "acting-context-9"},
	}
	for _, row := range rows {
		entry := &model.AuditLog{
			TenantID: tenantID, UserID: "actor", Action: row.action, Resource: row.resource,
			TargetTenantID: row.target, Result: row.result, ApprovalID: row.approvalID,
			ActingContextID: row.actingID,
		}
		if err := svc.RecordAudit(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	targeted, total, err := svc.ListAudits(ctx, tenantID, 1, 20, repository.AuditFilter{TargetTenant: "other-workspace"})
	if err != nil || total != 1 || len(targeted) != 1 || targeted[0].TargetTenantID != "other-workspace" {
		t.Fatalf("target filter: total=%d rows=%+v err=%v", total, targeted, err)
	}
	denied, total, err := svc.ListAudits(ctx, tenantID, 1, 20, repository.AuditFilter{Result: "DENIED"})
	if err != nil || total != 1 || len(denied) != 1 || denied[0].Result != "DENIED" {
		t.Fatalf("result filter: total=%d rows=%+v err=%v", total, denied, err)
	}
	approval, total, err := svc.ListAudits(ctx, tenantID, 1, 20, repository.AuditFilter{ApprovalID: "approval-1"})
	if err != nil || total != 1 || len(approval) != 1 || approval[0].ApprovalID != "approval-1" {
		t.Fatalf("approval filter: total=%d rows=%+v err=%v", total, approval, err)
	}
	acting, total, err := svc.ListAudits(ctx, tenantID, 1, 20, repository.AuditFilter{ActingContextID: "acting-context-9"})
	if err != nil || total != 1 || len(acting) != 1 || acting[0].ActingContextID != "acting-context-9" {
		t.Fatalf("acting context filter: total=%d rows=%+v err=%v", total, acting, err)
	}
}
