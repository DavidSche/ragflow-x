package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func TestApprovalGateDisabledRemainsOptionalForWorkspaceWrites(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{Enabled: false})
	approval, hold, _ := svc.ApprovalGate(context.Background(), "admin", model.ApprovalObjectDataset, model.ApprovalActionDelete, "ds", nil, "id-1")
	if hold || approval != nil {
		t.Fatalf("disabled gate must not hold: hold=%t approval=%+v", hold, approval)
	}
}

func TestApprovalGateCrossTenantPolicyMissingFailsClosed(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{Enabled: true})
	tenant, err := svc.CreateTenant(context.Background(), "Approval Target")
	if err != nil {
		t.Fatal(err)
	}
	_, hold, err := svc.ApprovalGateForTenant(context.Background(), "admin", tenant.ID, model.ApprovalObjectDataset, model.ApprovalActionDelete, "ds", nil, "missing-policy-1")
	if err == nil || hold {
		t.Fatalf("missing policy must fail closed: hold=%t err=%v", hold, err)
	}
}

func TestApprovalSubmitRequiresIdempotencyKey(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{Enabled: true})
	_, err := svc.SubmitApproval(context.Background(), "admin", ApprovalSubmitRequest{
		ObjectType: model.ApprovalObjectDataset, Action: model.ApprovalActionDelete, ObjectID: "ds",
	})
	if err == nil {
		t.Fatal("empty idempotency key must be rejected")
	}
}

func TestApprovalPolicyCacheInvalidation(t *testing.T) {
	svc := newAuthzSvc(t)
	svc.SetApprovalConfig(config.Approval{Enabled: true, PolicyCacheTTLSec: 60})
	ctx := context.Background()
	policy := &model.ApprovalPolicy{
		ID: "policy-1", TenantID: "tenant-1", ObjectType: model.ApprovalObjectDataset,
		Action: model.ApprovalActionDelete, Enabled: true, Priority: 100,
		ConditionsJSON: "{}", StepsJSON: `[{"step_no":1,"name":"one","approver_type":"role","approver_value":"tenant_admin"}]`,
		ExpireHours: 1, Version: 1, CreatedBy: "test", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.UpsertApprovalPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	first, err := svc.matchApprovalPolicy(ctx, "tenant-1", model.ApprovalObjectDataset, model.ApprovalActionDelete, nil)
	if err != nil || first == nil {
		t.Fatalf("first match: policy=%+v err=%v", first, err)
	}
	policy.Enabled = false
	if err := svc.Store.UpsertApprovalPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err := svc.invalidateApprovalPolicyCache("tenant-1", model.ApprovalObjectDataset, model.ApprovalActionDelete); err != nil {
		t.Fatal(err)
	}
	second, err := svc.matchApprovalPolicy(ctx, "tenant-1", model.ApprovalObjectDataset, model.ApprovalActionDelete, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second != nil {
		t.Fatalf("disabled policy must not match after cache invalidation: %+v", second)
	}
}

type fakeApprovalSharedCache struct {
	entries  map[string][]model.ApprovalPolicy
	loadHits int
	saves    int
	deletes  int
	failLoad bool
}

func (c *fakeApprovalSharedCache) LoadPolicies(_ context.Context, key string) ([]model.ApprovalPolicy, bool, error) {
	if c.failLoad {
		return nil, false, errors.New("shared cache unavailable")
	}
	policies, ok := c.entries[key]
	if ok {
		c.loadHits++
	}
	return policies, ok, nil
}

func (c *fakeApprovalSharedCache) SavePolicies(_ context.Context, key string, policies []model.ApprovalPolicy, _ time.Duration) error {
	c.saves++
	c.entries[key] = policies
	return nil
}

func (c *fakeApprovalSharedCache) Delete(_ context.Context, key string) error {
	c.deletes++
	delete(c.entries, key)
	return nil
}

func (c *fakeApprovalSharedCache) Close() error { return nil }

func TestApprovalPolicySharedCacheIsInvalidated(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	shared := &fakeApprovalSharedCache{entries: map[string][]model.ApprovalPolicy{}}
	svc.SetApprovalPolicySharedCache(shared)
	svc.SetApprovalConfig(config.Approval{Enabled: true, PolicyCacheTTLSec: 60})
	policy := &model.ApprovalPolicy{
		ID: "shared-policy", TenantID: "tenant-1", ObjectType: model.ApprovalObjectDataset,
		Action: model.ApprovalActionDelete, Enabled: true, Priority: 100,
		ConditionsJSON: "{}", StepsJSON: `[{"step_no":1,"name":"one","approver_type":"role","approver_value":"tenant_admin"}]`,
		ExpireHours: 1, Version: 1, CreatedBy: "test", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.UpsertApprovalPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if first, err := svc.matchApprovalPolicy(ctx, policy.TenantID, policy.ObjectType, policy.Action, nil); err != nil || first == nil {
		t.Fatalf("first shared match: policy=%+v err=%v", first, err)
	}
	if shared.saves != 1 {
		t.Fatalf("shared save count = %d, want 1", shared.saves)
	}
	if err := svc.invalidateApprovalPolicyCache(policy.TenantID, policy.ObjectType, policy.Action); err != nil {
		t.Fatal(err)
	}
	if shared.deletes != 1 || len(shared.entries) != 0 {
		t.Fatalf("shared invalidation: deletes=%d entries=%d", shared.deletes, len(shared.entries))
	}
	policy.Enabled = false
	if err := svc.Store.UpsertApprovalPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	second, err := svc.matchApprovalPolicy(ctx, policy.TenantID, policy.ObjectType, policy.Action, nil)
	if err != nil || second != nil {
		t.Fatalf("policy after shared invalidation: policy=%+v err=%v", second, err)
	}
}

func TestApprovalPolicySharedCacheReadFailureFailsClosed(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	shared := &fakeApprovalSharedCache{entries: map[string][]model.ApprovalPolicy{}, failLoad: true}
	svc.SetApprovalPolicySharedCache(shared)
	svc.SetApprovalConfig(config.Approval{Enabled: true, PolicyCacheTTLSec: 60})
	if _, err := svc.matchApprovalPolicy(ctx, "tenant-1", model.ApprovalObjectDataset, model.ApprovalActionDelete, nil); err == nil {
		t.Fatal("shared cache read failure must fail closed")
	}
}
