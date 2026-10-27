package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// ScenarioID: SC-APPROVAL-001
func TestP0_APPROVAL_001_PolicyTenantTargetFence(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{Enabled: true, PolicyCacheTTLSec: 0})
	tenant, err := svc.CreateTenant(ctx, "Policy Target Workspace")
	if err != nil {
		t.Fatal(err)
	}
	otherTenant, err := svc.CreateTenant(ctx, "Policy Other Workspace")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load platform admin: admin=%+v err=%v", admin, err)
	}
	request := ApprovalPolicyRequest{
		TenantID:   tenant.ID,
		ObjectType: model.ApprovalObjectDataset,
		Action:     model.ApprovalActionDelete,
		Enabled:    true,
		Priority:   100,
		Steps: []model.ApprovalStepSpec{{
			StepNo: 1, Name: "workspace admin", ApproverType: model.ApprovalApproverRole,
			ApproverValue: model.RoleTenantAdmin,
		}},
	}

	created, err := svc.SaveApprovalPolicy(ctx, admin.ID, model.PlatformTenantID, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.TenantID != tenant.ID {
		t.Fatalf("policy tenant = %s, want %s", created.TenantID, tenant.ID)
	}

	missing := request
	missing.TenantID = "missing-" + created.ID
	if _, err := svc.SaveApprovalPolicy(ctx, admin.ID, model.PlatformTenantID, missing); err == nil {
		t.Fatal("missing policy tenant target must be rejected")
	} else {
		var httpErr *httperr.Error
		if !errors.As(err, &httpErr) || httpErr.Status != 400 || httpErr.Code != 40102 {
			t.Fatalf("missing tenant target error mismatch: %#v", err)
		}
	}

	mismatch := request
	mismatch.ID = created.ID
	mismatch.TenantID = otherTenant.ID
	if _, err := svc.SaveApprovalPolicy(ctx, admin.ID, model.PlatformTenantID, mismatch); err == nil {
		t.Fatal("changing policy tenant target must be rejected")
	} else {
		var httpErr *httperr.Error
		if !errors.As(err, &httpErr) || httpErr.Status != 409 || httpErr.Code != 40910 {
			t.Fatalf("tenant target mismatch error mismatch: %#v", err)
		}
	}
	current, err := svc.Store.GetApprovalPolicyByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.TenantID != tenant.ID || current.Version != 1 {
		t.Fatalf("rejected update changed policy: %+v", current)
	}

	global := request
	global.TenantID = ""
	globalPolicy, err := svc.SaveApprovalPolicy(ctx, admin.ID, model.PlatformTenantID, global)
	if err != nil {
		t.Fatal(err)
	}
	if globalPolicy.TenantID != "" {
		t.Fatalf("global policy tenant = %s, want empty", globalPolicy.TenantID)
	}
}
