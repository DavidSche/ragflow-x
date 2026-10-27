package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// newProjectSharedMemoryFixture builds a project memory policy with one
// opted-in member so consent governance paths can be exercised.
func newProjectSharedMemoryFixture(t *testing.T, name string) (*Service, *ragflow.Mock, *model.User, *model.User, *model.MemoryPolicy, string, *model.Project) {
	t.Helper()
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: user=%+v err=%v", admin, err)
	}
	member, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "project-member-" + id.New(), Password: "secret123",
		Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	project, err := svc.CreateProject(ctx, tenant.ID, name+"-project", "consent governance")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddProjectUser(ctx, tenant.ID, project.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Fatalf("expected mock provider, got %T", svc.RAGFlow)
	}
	memory, err := svc.CreateMemory(ctx, tenant.ID, name+"-memory", []string{"raw"}, "embedding-model", "chat-model")
	if err != nil {
		t.Fatal(err)
	}
	memoryID := memory.ID
	now := time.Now().UTC()
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, ProjectID: project.ID, UserID: "",
		AssistantID: "assistant-shared", RagflowMemoryID: memoryID,
		State: model.MemoryPolicyActive, ReadPolicy: "project", WritePolicy: "member",
		PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 0, Config: "{}",
		CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	return svc, mock, member, admin, policy, memoryID, project
}

// TestProjectSharedMemoryRequiresPersonalConsent verifies MEM-012: project
// enablement never substitutes for the member's own consent.
func TestProjectSharedMemoryRequiresPersonalConsent(t *testing.T) {
	svc, _, member, _, policy, memoryID, project := newProjectSharedMemoryFixture(t, "Consent Required")
	ctx := context.Background()

	// Active project policy but no personal consent: write must fail closed.
	if _, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, member.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write", ProjectID: project.ID,
		RequestID: "request-no-consent", IdempotencyKey: "idempotency-no-consent",
		UserID: member.ID, AssistantID: policy.AssistantID,
		UserInput: "shared note", AgentResponse: "ack",
	}); err == nil {
		t.Fatal("write without personal consent must fail")
	} else if he, ok := err.(*httperr.Error); !ok || he.Code != 40356 {
		t.Fatalf("missing personal consent must map to 40356: %v", err)
	}

	// Member opts in; the shared policy's own consent version stays untouched.
	if _, err := svc.SetMemoryPolicyConsent(ctx, policy.TenantID, member.ID, policy.ID, true); err != nil {
		t.Fatalf("member opt-in: %v", err)
	}
	stored, err := svc.GetMemoryPolicy(ctx, policy.TenantID, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ConsentVersion != 0 || stored.PolicyGeneration != 1 {
		t.Fatalf("personal opt-in must not move policy consent/generation: %+v", stored)
	}

	// Consent listing carries identity and version only.
	views, err := svc.ListMemoryPolicyConsents(ctx, policy.TenantID, policy.ID)
	if err != nil || len(views) != 1 {
		t.Fatalf("consent listing: views=%+v err=%v", views, err)
	}
	if views[0].UserID != member.ID || views[0].State != model.MemoryConsentActive || views[0].ConsentVersion != 1 {
		t.Fatalf("unexpected consent view: %+v", views[0])
	}
}

// TestProjectSharedMemoryConsentLifecycle verifies opt-in write success,
// opt-out admission blocking, stale version rejection, and member-exit
// revocation with policy generation bump.
func TestProjectSharedMemoryConsentLifecycle(t *testing.T) {
	svc, _, member, _, policy, memoryID, project := newProjectSharedMemoryFixture(t, "Consent Lifecycle")
	ctx := context.Background()

	if _, err := svc.SetMemoryPolicyConsent(ctx, policy.TenantID, member.ID, policy.ID, true); err != nil {
		t.Fatal(err)
	}
	operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, member.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write", ProjectID: project.ID,
		RequestID: "request-consent-ok", IdempotencyKey: "idempotency-consent-ok",
		UserID: member.ID, AssistantID: policy.AssistantID,
		UserInput: "shared note one", AgentResponse: "ack one",
	})
	if err != nil {
		t.Fatalf("opted-in member write must be accepted: %v", err)
	}
	if operation.ConsentVersion != 1 {
		t.Fatalf("operation must carry the member's consent version: %+v", operation)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("process outbox: %v", err)
	}
	executed, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operation.ID)
	if err != nil || executed == nil || executed.State != model.MemoryOperationExecuted {
		t.Fatalf("admitted operation must execute: %+v err=%v", executed, err)
	}

	// Opt-out: the member's own consent version moves; new writes fail.
	if _, err := svc.SetMemoryPolicyConsent(ctx, policy.TenantID, member.ID, policy.ID, false); err != nil {
		t.Fatal(err)
	}
	views, err := svc.ListMemoryPolicyConsents(ctx, policy.TenantID, policy.ID)
	if err != nil || views[0].State != model.MemoryConsentWithdrawn || views[0].ConsentVersion != 2 {
		t.Fatalf("opt-out must withdraw and bump consent version: %+v err=%v", views, err)
	}
	if _, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, member.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write", ProjectID: project.ID,
		RequestID: "request-consent-out", IdempotencyKey: "idempotency-consent-out",
		UserID: member.ID, AssistantID: policy.AssistantID,
		UserInput: "shared note two", AgentResponse: "ack two",
	}); err == nil {
		t.Fatal("write after opt-out must fail")
	}

	// Re-opt-in then member exit: consent withdrawn with reason, policy
	// generation bumped so stale accepted-but-unadmitted tasks block.
	if _, err := svc.SetMemoryPolicyConsent(ctx, policy.TenantID, member.ID, policy.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveProjectUser(ctx, policy.TenantID, project.ID, member.ID); err != nil {
		t.Fatalf("remove project member: %v", err)
	}
	views, err = svc.ListMemoryPolicyConsents(ctx, policy.TenantID, policy.ID)
	if err != nil || len(views) != 1 {
		t.Fatalf("consents after exit: views=%+v err=%v", views, err)
	}
	if views[0].State != model.MemoryConsentWithdrawn || views[0].WithdrawReason != "project_membership_revoked" {
		t.Fatalf("member exit must revoke consent with reason: %+v", views[0])
	}
	afterExit, err := svc.GetMemoryPolicy(ctx, policy.TenantID, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterExit.PolicyGeneration != 2 {
		t.Fatalf("member exit must bump policy generation: %+v", afterExit)
	}
}

// TestProjectSharedMemoryNonMemberRejected verifies the membership leg of the
// three-layer consent contract.
func TestProjectSharedMemoryNonMemberRejected(t *testing.T) {
	svc, _, _, _, policy, _, project := newProjectSharedMemoryFixture(t, "Consent Non Member")
	ctx := context.Background()
	outsider, err := svc.CreateUser(ctx, policy.TenantID, "", CreateUserRequest{
		Username: "outsider-" + id.New(), Password: "secret123",
		Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddProjectUser(ctx, policy.TenantID, project.ID, outsider.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.RemoveProjectUser(ctx, policy.TenantID, project.ID, outsider.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetMemoryPolicyConsent(ctx, policy.TenantID, outsider.ID, policy.ID, true); err == nil {
		t.Fatal("non-member opt-in must fail")
	}
}

// TestUserScopedPolicyConsentUnchanged verifies that user-scoped policies keep
// the policy-level consent contract while project policies moved to per-member
// consent.
func TestUserScopedPolicyConsentUnchanged(t *testing.T) {
	svc, _, target, policy, memoryID := newMemoryDispatchAlertFixture(t, "User Consent Unchanged")
	ctx := context.Background()

	// The shared fixture creates the RAGFlow-side memory only; backfill the
	// tenant shadow row so consent revalidation can see the binding.
	now0 := time.Now().UTC()
	if err := svc.Store.UpsertMemoryShadow(ctx, &model.MemoryShadow{
		ID: memoryID, TenantID: policy.TenantID, Name: "user-consent-memory",
		MemoryType: "raw", OwnerID: target.ID, CreatedAt: now0, UpdatedAt: now0,
	}); err != nil {
		t.Fatal(err)
	}

	if policy.ConsentVersion != 1 {
		t.Fatalf("fixture policy consent: %+v", policy)
	}
	if _, err := svc.SetMemoryPolicyConsent(ctx, policy.TenantID, target.ID, policy.ID, false); err != nil {
		t.Fatal(err)
	}
	paused, err := svc.GetMemoryPolicy(ctx, policy.TenantID, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.State != model.MemoryPolicyPaused || paused.ConsentVersion != 2 || paused.PolicyGeneration != 2 {
		t.Fatalf("user opt-out must pause and bump versions: %+v", paused)
	}
	if _, err := svc.SetMemoryPolicyConsent(ctx, policy.TenantID, target.ID, policy.ID, true); err != nil {
		t.Fatal(err)
	}
	resumed, err := svc.GetMemoryPolicy(ctx, policy.TenantID, policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.State != model.MemoryPolicyActive || resumed.ConsentVersion != 3 || resumed.PolicyGeneration != 3 {
		t.Fatalf("user opt-in must resume and bump versions: %+v", resumed)
	}

	// And a user-scoped write still flows through the PII gate with the
	// policy-level consent version.
	operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-user-consent", IdempotencyKey: "idempotency-user-consent",
		UserID: target.ID, AssistantID: policy.AssistantID,
		UserInput: "hello again", AgentResponse: "hello back",
	})
	if err != nil {
		t.Fatalf("user-scoped write must be accepted: %v", err)
	}
	if operation.ConsentVersion != 3 || operation.PIIAction == "" {
		t.Fatalf("user-scoped operation consent/pii metadata: %+v", operation)
	}
}
