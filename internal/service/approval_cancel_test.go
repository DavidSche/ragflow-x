package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestCancelApprovalRequiresManagePermissionForNonRequester(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{Enabled: true, DefaultExpireHours: 72, PolicyCacheTTLSec: 60})
	ctx := context.Background()
	requester, _, tenantID, policyID := setupApprovalEnv(t, svc)
	other, err := svc.CreateUser(ctx, tenantID, "platform_admin", CreateUserRequest{
		Username: "cancel-operator", Password: "secret123", Role: "operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	approval := createApprovalDirectly(
		t, svc, tenantID, requester, policyID, model.ApprovalStatusPendingApproval,
		model.ApprovalApproverRole, "tenant_admin",
	)

	canceled, err := svc.CancelApproval(ctx, other.ID, tenantID, approval.ID, "operation canceled")
	if err == nil {
		t.Fatalf("operator without manage permission must not cancel another requester's approval: %+v", canceled)
	}
	updated, err := svc.Store.GetApproval(ctx, tenantID, approval.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated == nil || updated.Status != model.ApprovalStatusPendingApproval {
		t.Fatalf("approval must remain pending, got %+v", updated)
	}
}
