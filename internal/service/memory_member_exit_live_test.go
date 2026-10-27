package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
	"gorm.io/gorm"
)

// TestLiveProjectMemoryMemberExitInvalidationE2E closes the Milestone 198
// recorded gap "成员退出→generation 递增→在途 dispatch 失效的跨服务联动仅以
// mock 契约验证" against a real RAGFlow deployment (MEM-008 / MEM-012):
//
//  1. an opted-in project member's write is admitted and sent to the real
//     provider; the outcome is pinned to exactly one of the two contract
//     terminal paths — executed with an external message id, or governed
//     unknown with reason memory_dispatch_provider_unconfirmed (observed
//     live while the 192.168.31.211 model pool was unreachable from the
//     RAGFlow host, which is precisely the network-loss semantics the
//     unknown state exists for);
//  2. a second write is accepted but left unadmitted (outbox not processed);
//  3. member exit withdraws the personal consent (reason
//     project_membership_revoked) and bumps the project policy generation;
//  4. a post-exit write fails closed at creation (no longer a member);
//  5. the unadmitted operation blocks at admission with
//     memory_dispatch_policy_generation_stale and never reaches the provider;
//  6. the tombstone compensation reconciles the real external state:
//     executed content is deleted from the live memory and verified absent;
//     unconfirmed content reconciles as external absent. Both terminal paths
//     must end reconciled with zero provider re-dispatch.
func TestLiveProjectMemoryMemberExitInvalidationE2E(t *testing.T) {
	svc, client, gdb, member, project, policy, memoryID := newLiveProjectSharedMemoryFixture(t, "member-exit")
	ctx := context.Background()

	var dispatched *model.MemoryOperation
	var providerExecuted bool
	t.Run("opted-in member write reaches the real provider", func(t *testing.T) {
		dispatched = dispatchLiveProjectMemoryWrite(
			t, svc, member, policy, project.ID, memoryID,
			"live-exit-executed-session", "request-live-exit-executed", "idempotency-live-exit-executed",
		)
		if dispatched.PolicyGeneration != 1 || dispatched.ConsentVersion != 1 {
			t.Fatalf("dispatched operation must carry generation 1 and consent version 1: %+v", dispatched)
		}
		if dispatched.PayloadCiphertext != "" || dispatched.PayloadKeyID != "" || dispatched.PayloadHash == "" {
			t.Fatalf("dispatched live operation privacy contract mismatch: %+v", dispatched)
		}
		switch {
		case dispatched.State == model.MemoryOperationExecuted && dispatched.RagflowMessageID != "":
			providerExecuted = true
			waitForLiveProjectMessageCount(t, client, ctx, memoryID, policy.AssistantID,
				"live-exit-executed-session", 1)
			t.Logf("provider accepted the live write: message=%s", dispatched.RagflowMessageID)
		case dispatched.State == model.MemoryOperationUnknown &&
			dispatched.ReasonCode == "memory_dispatch_provider_unconfirmed":
			if client.lastAddErr == nil {
				t.Fatal("unconfirmed dispatch must record the provider error")
			}
			t.Logf("provider write unconfirmed (governed unknown): %v", client.lastAddErr)
		default:
			t.Fatalf("live dispatch must end executed or governed unknown, got %+v (%s)",
				dispatched, dispatched.ReasonCode)
		}
	})

	var pending *model.MemoryOperation
	t.Run("second write accepted but unadmitted", func(t *testing.T) {
		operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, member.ID, MemoryDispatchOperationInput{
			PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write", ProjectID: project.ID,
			RequestID: "request-live-exit-pending", IdempotencyKey: "idempotency-live-exit-pending",
			UserID: member.ID, AssistantID: policy.AssistantID, SessionID: "live-exit-pending-session",
			UserInput: "shared note written before member exit", AgentResponse: "ack before exit",
		})
		if err != nil {
			t.Fatalf("accepted pre-exit write: %v", err)
		}
		if operation.State != model.MemoryOperationAccepted {
			t.Fatalf("operation must stay accepted until the outbox is processed: %+v", operation)
		}
		pending = operation
	})

	t.Run("member exit revokes consent and bumps policy generation", func(t *testing.T) {
		if err := svc.RemoveProjectUser(ctx, policy.TenantID, project.ID, member.ID); err != nil {
			t.Fatalf("remove project member: %v", err)
		}
		views, err := svc.ListMemoryPolicyConsents(ctx, policy.TenantID, policy.ID)
		if err != nil || len(views) != 1 {
			t.Fatalf("consents after exit: views=%+v err=%v", views, err)
		}
		if views[0].State != model.MemoryConsentWithdrawn ||
			views[0].WithdrawReason != "project_membership_revoked" || views[0].ConsentVersion != 2 {
			t.Fatalf("member exit must revoke the personal consent with reason: %+v", views[0])
		}
		after, err := svc.Store.GetMemoryPolicy(ctx, policy.TenantID, policy.ID)
		if err != nil || after == nil {
			t.Fatalf("load policy after exit: %+v err=%v", after, err)
		}
		if after.PolicyGeneration != 2 {
			t.Fatalf("member exit must bump the project policy generation: %+v", after)
		}
	})

	t.Run("post-exit write fails closed at creation", func(t *testing.T) {
		_, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, member.ID, MemoryDispatchOperationInput{
			PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write", ProjectID: project.ID,
			RequestID: "request-live-exit-after", IdempotencyKey: "idempotency-live-exit-after",
			UserID: member.ID, AssistantID: policy.AssistantID, SessionID: "live-exit-after-session",
			UserInput: "shared note after exit", AgentResponse: "ack after exit",
		})
		if err == nil {
			t.Fatal("write after member exit must fail closed at creation")
		}
		if !strings.Contains(err.Error(), "not a project member") {
			t.Fatalf("post-exit write must fail on membership, got: %v", err)
		}
	})

	t.Run("unadmitted operation blocks at admission without provider contact", func(t *testing.T) {
		if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
			t.Fatalf("process outbox after member exit: %v", err)
		}
		stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, pending.ID)
		if err != nil || stored == nil {
			t.Fatalf("load pending operation: %+v err=%v", stored, err)
		}
		if stored.State != model.MemoryOperationBlocked ||
			stored.ReasonCode != "memory_dispatch_policy_generation_stale" {
			t.Fatalf("unadmitted operation must block on stale generation: %+v", stored)
		}
		if client.addCalls != 1 {
			t.Fatalf("blocked operation must never reach the provider: addCalls=%d", client.addCalls)
		}
		messages, err := client.Client.ListMemoryMessagesFiltered(ctx, memoryID, ragflow.MemoryMessageListFilter{
			AgentID: policy.AssistantID, SessionID: "live-exit-pending-session", Page: 1, PageSize: 100,
		})
		if err != nil {
			t.Fatalf("list pending-session messages: %v", err)
		}
		if len(messages) != 0 {
			t.Fatalf("blocked operation must not produce external content: %d messages", len(messages))
		}
	})

	t.Run("tombstone compensation reconciles the real external state", func(t *testing.T) {
		if providerExecuted {
			tombstoneAndCompensateLiveMemory(t, svc, gdb, member, dispatched, "compensated")
			waitForLiveProjectMessageCount(t, client, ctx, memoryID, policy.AssistantID,
				"live-exit-executed-session", 0)
		} else {
			tombstoneAndCompensateLiveMemory(t, svc, gdb, member, dispatched, "absent")
		}
		if client.addCalls != 1 {
			t.Fatalf("compensation must not re-dispatch: addCalls=%d", client.addCalls)
		}
	})
}

// newLiveProjectSharedMemoryFixture builds a real-RAGFlow project shared
// memory fixture: tenant, project, one opted-in member, a live memory owned
// by the member, and an active project memory policy bound to that memory.
func newLiveProjectSharedMemoryFixture(
	t *testing.T, scenario string,
) (*Service, *liveMemoryDispatchClient, *gorm.DB, *model.User, *model.Project, *model.MemoryPolicy, string) {
	t.Helper()
	baseURL := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL"))
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is not set")
	}
	if os.Getenv("RGX_ALLOW_MEMORY_FIXTURE") != "1" ||
		os.Getenv("RGX_ALLOW_MEMORY_DISPATCH_FIXTURE") != "1" ||
		os.Getenv("RGX_ALLOW_MEMORY_E2E_FIXTURE") != "1" {
		t.Skip("RGX_ALLOW_MEMORY_FIXTURE=1, RGX_ALLOW_MEMORY_DISPATCH_FIXTURE=1 and RGX_ALLOW_MEMORY_E2E_FIXTURE=1 are required")
	}
	gdb, err := db.Open(config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "memory-member-exit.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := repository.NewStore(gdb)
	live := ragflow.NewHTTPClientWithMiddleware(
		baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 15*time.Second, 4,
	)
	client := &liveMemoryDispatchClient{Client: live}
	svc := New(store, client, jwt.NewManager("secret", 24), "key")
	ctx := context.Background()
	if err := svc.BootstrapAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Store.Close() })
	tenant, err := svc.CreateTenant(ctx, "Live Member Exit "+scenario+" "+id.New())
	if err != nil {
		t.Fatal(err)
	}
	member, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "project-member-" + strings.ToLower(scenario) + "-" + id.New(),
		Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	project, err := svc.CreateProject(ctx, tenant.ID, "live-member-exit-"+scenario, "MEM-008 member exit live drill")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddProjectUser(ctx, tenant.ID, project.ID, member.ID); err != nil {
		t.Fatal(err)
	}
	chatModel := liveMemoryModelID(t, live, ctx, "chat")
	embeddingModel := liveMemoryModelID(t, live, ctx, "embedding")
	name := fmt.Sprintf("rgx_member_exit_%s_%d", strings.ToLower(scenario), time.Now().UnixMilli())
	memoryID, err := live.CreateMemory(ctx, ragflow.CreateMemoryRequest{
		Name: name, MemoryType: []string{"raw"},
		EmbdID: embeddingModel, LLMID: chatModel,
	})
	if err != nil {
		t.Fatalf("create live temporary memory: %v", err)
	}
	if err := svc.Store.UpsertMemoryShadow(ctx, &model.MemoryShadow{
		ID: memoryID, TenantID: tenant.ID, Name: name, MemoryType: "raw", OwnerID: member.ID,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record live temporary memory shadow: %v", err)
	}
	t.Cleanup(func() {
		if os.Getenv("RGX_KEEP_MEMORY_FIXTURE") == "1" {
			t.Logf("RGX_KEEP_MEMORY_FIXTURE=1; retaining memory %s (%s)", memoryID, name)
			return
		}
		if err := live.DeleteMemory(context.Background(), memoryID); err != nil {
			t.Errorf("delete live temporary memory %s: %v", memoryID, err)
		}
	})
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: user=%+v err=%v", admin, err)
	}
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, ProjectID: project.ID, UserID: "",
		AssistantID: "live-exit-agent", RagflowMemoryID: memoryID,
		State: model.MemoryPolicyActive, ReadPolicy: "project", WritePolicy: "member",
		PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 0, Config: "{}",
		CreatedBy: admin.ID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetMemoryPolicyConsent(ctx, tenant.ID, member.ID, policy.ID, true); err != nil {
		t.Fatalf("member opt-in: %v", err)
	}
	return svc, client, gdb, member, project, policy, memoryID
}

// dispatchLiveProjectMemoryWrite accepts a project shared memory write and
// processes the outbox so the operation reaches the real provider.
func dispatchLiveProjectMemoryWrite(
	t *testing.T, svc *Service, member *model.User, policy *model.MemoryPolicy,
	projectID, memoryID, sessionID, requestID, idempotencyKey string,
) *model.MemoryOperation {
	t.Helper()
	ctx := context.Background()
	operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, member.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write", ProjectID: projectID,
		RequestID: requestID, IdempotencyKey: idempotencyKey,
		UserID: member.ID, AssistantID: policy.AssistantID, SessionID: sessionID,
		UserInput: "project shared live drill user question", AgentResponse: "project shared live drill answer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatal(err)
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operation.ID)
	if err != nil || stored == nil {
		t.Fatal(err)
	}
	return stored
}

// waitForLiveProjectMessageCount polls the real memory until the session's
// active external messages reach the expected count.
func waitForLiveProjectMessageCount(
	t *testing.T, client *liveMemoryDispatchClient, ctx context.Context,
	memoryID, agentID, sessionID string, want int,
) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		messages, err := client.Client.ListMemoryMessagesFiltered(ctx, memoryID, ragflow.MemoryMessageListFilter{
			AgentID: agentID, SessionID: sessionID, Page: 1, PageSize: 100,
		})
		if err == nil {
			active := 0
			for _, message := range messages {
				if memoryMessageActive(message) {
					active++
				}
			}
			if active == want {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("live message count did not converge: %v", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	messages, _ := client.Client.ListMemoryMessagesFiltered(ctx, memoryID, ragflow.MemoryMessageListFilter{
		AgentID: agentID, SessionID: sessionID, Page: 1, PageSize: 100,
	})
	t.Fatalf("live message count never reached %d: %d messages", want, len(messages))
}
