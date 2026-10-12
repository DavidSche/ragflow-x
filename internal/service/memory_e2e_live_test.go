package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

type liveMemoryDispatchClient struct {
	ragflow.Client
	listCalls int
	addCalls  int
	failList  bool
}

func (c *liveMemoryDispatchClient) ListMemoryMessagesFiltered(
	ctx context.Context, memoryID string, filter ragflow.MemoryMessageListFilter,
) ([]ragflow.MemoryMessage, error) {
	c.listCalls++
	if c.failList {
		c.failList = false
		return nil, fmt.Errorf("injected live filtered-list failure")
	}
	return c.Client.ListMemoryMessagesFiltered(ctx, memoryID, filter)
}

func (c *liveMemoryDispatchClient) AddMemoryMessage(
	ctx context.Context, req ragflow.AddMessageRequest,
) error {
	c.addCalls++
	return c.Client.AddMemoryMessage(ctx, req)
}

func newLiveMemoryE2EFixture(
	t *testing.T, scenario string, middlewares ...ragflow.RoundTripperMiddleware,
) (*Service, *liveMemoryDispatchClient, *model.User, *model.MemoryPolicy, string, *gorm.DB) {
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
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "memory-e2e.db"),
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
		middlewares...,
	)
	client := &liveMemoryDispatchClient{Client: live}
	svc := New(store, client, jwt.NewManager("secret", 24), "key")
	ctx := context.Background()
	if err := svc.BootstrapAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Store.Close() })
	tenant, err := svc.CreateTenant(ctx, "Live Memory E2E "+scenario+" "+id.New())
	if err != nil {
		t.Fatal(err)
	}
	target, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "memory-e2e-" + strings.ToLower(scenario) + "-" + id.New(),
		Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	chatModel := liveMemoryModelID(t, live, ctx, "chat")
	embeddingModel := liveMemoryModelID(t, live, ctx, "embedding")
	name := fmt.Sprintf("rgx_memory_e2e_%s_%d", strings.ToLower(scenario), time.Now().UnixMilli())
	memoryID, err := live.CreateMemory(ctx, ragflow.CreateMemoryRequest{
		Name: name, MemoryType: []string{"raw"},
		EmbdID: embeddingModel, LLMID: chatModel,
	})
	if err != nil {
		t.Fatalf("create live temporary memory: %v", err)
	}
	if err := svc.Store.UpsertMemoryShadow(ctx, &model.MemoryShadow{
		ID: memoryID, TenantID: tenant.ID, Name: name, MemoryType: "raw", OwnerID: target.ID,
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
		ID: id.New(), TenantID: tenant.ID, UserID: target.ID, AssistantID: "live-e2e-agent",
		RagflowMemoryID: memoryID, State: model.MemoryPolicyActive,
		ReadPolicy: "owner", WritePolicy: "owner", PolicyVersion: 1,
		PolicyGeneration: 1, ConsentVersion: 1, Config: "{}",
		CreatedBy: admin.ID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	return svc, client, target, policy, memoryID, gdb
}

func liveMemoryModelID(
	t *testing.T, client *ragflow.HTTPClient, ctx context.Context, modelType string,
) string {
	t.Helper()
	models, err := client.ListModels(ctx, modelType)
	if err != nil {
		t.Fatalf("list live %s models: %v", modelType, err)
	}
	for _, candidate := range models {
		if strings.TrimSpace(candidate.Name) == "" || strings.TrimSpace(candidate.Provider) == "" {
			continue
		}
		modelID := candidate.Name + "@" + candidate.Provider
		if strings.TrimSpace(candidate.Instance) != "" {
			modelID = candidate.Name + "@" + candidate.Instance + "@" + candidate.Provider
		}
		t.Logf("selected live %s model %s", modelType, modelID)
		return modelID
	}
	t.Skipf("live Memory E2E requires an enabled %s model", modelType)
	return ""
}

func waitForLiveMemoryMessageGone(
	t *testing.T, client *liveMemoryDispatchClient, ctx context.Context,
	memoryID, sessionID string,
) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		messages, err := client.Client.ListMemoryMessagesFiltered(ctx, memoryID, ragflow.MemoryMessageListFilter{
			AgentID: "live-e2e-agent", SessionID: sessionID,
			Page: 1, PageSize: 100,
		})
		if err == nil && len(messages) == 0 {
			return
		}
		active := 0
		for _, message := range messages {
			if memoryMessageActive(message) {
				active++
			}
		}
		if active == 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("live message did not disappear before timeout: %v", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatal("live external message remained visible after delete")
}

func dispatchLiveMemoryWrite(
	t *testing.T, svc *Service, target *model.User, policy *model.MemoryPolicy,
	memoryID, sessionID, requestID, idempotencyKey string,
) *model.MemoryOperation {
	t.Helper()
	ctx := context.Background()
	operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: requestID, IdempotencyKey: idempotencyKey,
		UserID: target.ID, AssistantID: "live-e2e-agent", SessionID: sessionID,
		UserInput: "private live e2e user question", AgentResponse: "private live e2e answer",
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

func tombstoneAndCompensateLiveMemory(
	t *testing.T, svc *Service, gdb *gorm.DB, target *model.User,
	operation *model.MemoryOperation, wantResult string,
) MemoryCompensationSummary {
	t.Helper()
	tombstoned, err := svc.TombstoneMemoryOperation(
		context.Background(), operation.TenantID, target.ID, operation.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	visibleAt := time.Now().UTC().Add(-memoryCompensationVisibilityGrace - time.Minute)
	if err := gdb.Model(&model.MemoryOperation{}).
		Where("tenant_id = ? AND id = ?", tombstoned.TenantID, tombstoned.ID).
		Update("tombstone_created_at", visibleAt).Error; err != nil {
		t.Fatal(err)
	}
	summary, err := svc.ProcessMemoryCompensations(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := svc.Store.GetMemoryOperation(
		context.Background(), operation.TenantID, operation.ID,
	)
	if err != nil || stored == nil {
		t.Fatal(err)
	}
	switch wantResult {
	case "compensated":
		if summary.Compensated != 1 || stored.State != model.MemoryOperationReconciled ||
			stored.ReasonCode != "memory_tombstone_compensated" {
			t.Fatalf("live compensation did not complete: summary=%+v operation=%+v", summary, stored)
		}
	case "absent":
		if summary.Absent != 1 || stored.State != model.MemoryOperationReconciled ||
			stored.ReasonCode != "memory_tombstone_external_absent" {
			t.Fatalf("live absence was not reconciled: summary=%+v operation=%+v", summary, stored)
		}
	default:
		t.Fatalf("unsupported live compensation expectation: %s", wantResult)
	}
	return summary
}

func TestLiveMemoryDispatchAndCompensationE2E(t *testing.T) {
	svc, client, target, policy, memoryID, gdb := newLiveMemoryE2EFixture(t, "closed-loop")
	ctx := context.Background()

	t.Run("successful dispatch and tombstone compensation", func(t *testing.T) {
		operation := dispatchLiveMemoryWrite(
			t, svc, target, policy, memoryID, "e2e-success-session",
			"request-e2e-success", "idempotency-e2e-success",
		)
		if operation.State != model.MemoryOperationExecuted ||
			operation.RagflowMessageID == "" || client.addCalls != 1 {
			t.Fatalf("live successful dispatch mismatch: operation=%+v addCalls=%d", operation, client.addCalls)
		}
		if operation.PayloadCiphertext != "" || operation.PayloadKeyID != "" || operation.PayloadHash == "" {
			t.Fatalf("executed live operation privacy contract mismatch: %+v", operation)
		}
		tombstoneAndCompensateLiveMemory(
			t, svc, gdb, target, operation, "compensated",
		)
	})

	t.Run("provider write unconfirmed then compensation", func(t *testing.T) {
		client.failList = true
		operation := dispatchLiveMemoryWrite(
			t, svc, target, policy, memoryID, "e2e-unconfirmed-session",
			"request-e2e-unconfirmed", "idempotency-e2e-unconfirmed",
		)
		if operation.State != model.MemoryOperationUnknown ||
			operation.ReasonCode != "memory_dispatch_external_message_unconfirmed" ||
			client.addCalls != 2 {
			t.Fatalf("live unconfirmed dispatch mismatch: operation=%+v addCalls=%d", operation, client.addCalls)
		}
		if operation.PayloadCiphertext != "" || operation.PayloadKeyID != "" || operation.PayloadHash == "" {
			t.Fatalf("unknown live operation privacy contract mismatch: %+v", operation)
		}
		tombstoneAndCompensateLiveMemory(
			t, svc, gdb, target, operation, "compensated",
		)
	})

	t.Run("external absence reconciliation", func(t *testing.T) {
		operation := dispatchLiveMemoryWrite(
			t, svc, target, policy, memoryID, "e2e-absent-session",
			"request-e2e-absent", "idempotency-e2e-absent",
		)
		if operation.State != model.MemoryOperationExecuted ||
			operation.RagflowMessageID == "" || client.addCalls != 3 {
			t.Fatalf("live absent setup mismatch: operation=%+v addCalls=%d", operation, client.addCalls)
		}
		externalID, err := strconv.ParseInt(operation.RagflowMessageID, 10, 64)
		if err != nil || externalID <= 0 {
			t.Fatalf("invalid live message id %q: %v", operation.RagflowMessageID, err)
		}
		if err := client.Client.DeleteMemoryMessage(ctx, memoryID, externalID); err != nil {
			t.Fatalf("delete live external message: %v", err)
		}
		waitForLiveMemoryMessageGone(t, client, ctx, memoryID, "e2e-absent-session")
		if err := gdb.Model(&model.MemoryOperation{}).
			Where("tenant_id = ? AND id = ?", operation.TenantID, operation.ID).
			Update("ragflow_message_id", "").Error; err != nil {
			t.Fatal(err)
		}
		tombstoneAndCompensateLiveMemory(
			t, svc, gdb, target, operation, "absent",
		)
	})

	t.Run("dispatched recovery never resends", func(t *testing.T) {
		now := time.Now().UTC()
		operationID := id.New()
		eventID := id.New()
		operation := &model.MemoryOperation{
			ID: operationID, TenantID: policy.TenantID, PolicyID: policy.ID, MemoryID: memoryID,
			AssistantID: "live-e2e-agent", UserID: target.ID, OutboxEventID: eventID,
			RequestID: "request-e2e-recovery", OperationType: "write",
			IdempotencyKey: "idempotency-e2e-recovery", SessionID: "e2e-recovery-session",
			State: model.MemoryOperationDispatched, PolicyVersion: 1, PolicyGeneration: 1,
			ConsentVersion: 1, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
		}
		sealTestMemoryOperation(t, svc, operation)
		admittedAt := now
		operation.DispatchAdmittedAt = &admittedAt
		event := &model.OutboxEvent{
			ID: eventID, TenantID: policy.TenantID,
			EventType:     model.EventTypeMemoryOperationDispatch,
			AggregateType: model.AggregateTypeMemoryOperation, AggregateID: operationID,
			Payload:    `{"x_operation_id":"` + operationID + `","request_id":"request-e2e-recovery","x_policy_id":"` + policy.ID + `"}`,
			OccurredAt: now, CreatedAt: now, UpdatedAt: now,
		}
		if err := svc.Store.CreateMemoryOperation(ctx, operation); err != nil {
			t.Fatal(err)
		}
		if err := svc.Store.CreateOutboxEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
		if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
			t.Fatal(err)
		}
		stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operationID)
		if err != nil || stored == nil {
			t.Fatal(err)
		}
		if stored.State != model.MemoryOperationUnknown ||
			stored.ReasonCode != "memory_dispatch_provider_unconfirmed" || client.addCalls != 3 {
			t.Fatalf("dispatched recovery mismatch: operation=%+v addCalls=%d", stored, client.addCalls)
		}
		eventRow, err := svc.Store.GetMemoryDispatchOutboxEvent(ctx, policy.TenantID, eventID)
		if err != nil || eventRow == nil || eventRow.PublishedAt == nil {
			t.Fatalf("dispatched recovery outbox mismatch: event=%+v err=%v", eventRow, err)
		}
	})
}

func TestLiveMemoryPrincipalMappingIdentityGate(t *testing.T) {
	svc, client, targetA, policyA, memoryID, _ := newLiveMemoryE2EFixture(t, "identity-gate")
	ctx := context.Background()
	tenantB, err := svc.CreateTenant(ctx, "Live Memory Identity B "+id.New())
	if err != nil {
		t.Fatal(err)
	}
	targetB, err := svc.CreateUser(ctx, tenantB.ID, "", CreateUserRequest{
		Username: "memory-identity-b-" + id.New(), Password: "secret123",
		Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: user=%+v err=%v", admin, err)
	}
	policyB := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenantB.ID, UserID: targetB.ID,
		AssistantID: policyA.AssistantID, RagflowMemoryID: memoryID,
		State: model.MemoryPolicyActive, ReadPolicy: "owner", WritePolicy: "owner",
		PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1, Config: "{}",
		CreatedBy: admin.ID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policyB); err != nil {
		t.Fatal(err)
	}

	operationA := dispatchLiveMemoryWrite(
		t, svc, targetA, policyA, memoryID, "identity-a-session",
		"request-identity-a", "idempotency-identity-a",
	)
	operationB := dispatchLiveMemoryWrite(
		t, svc, targetB, policyB, memoryID, "identity-b-session",
		"request-identity-b", "idempotency-identity-b",
	)
	expectedA := "rgx:" + policyA.TenantID + ":" + targetA.ID
	expectedB := "rgx:" + policyB.TenantID + ":" + targetB.ID
	if operationA.State != model.MemoryOperationExecuted || operationA.RagflowAttributionUserID != expectedA {
		t.Fatalf("tenant A identity dispatch mismatch: %+v", operationA)
	}
	if operationB.State != model.MemoryOperationExecuted || operationB.RagflowAttributionUserID != expectedB {
		t.Fatalf("tenant B identity dispatch mismatch: %+v", operationB)
	}

	messagesA, err := client.Client.ListMemoryMessagesFiltered(ctx, memoryID, ragflow.MemoryMessageListFilter{
		AgentID: "live-e2e-agent", SessionID: "identity-a-session", UserID: expectedA,
		Page: 1, PageSize: 100,
	})
	if err != nil || len(messagesA) != 1 || messagesA[0].UserID != expectedA {
		t.Fatalf("tenant A attribution search failed: messages=%+v err=%v", messagesA, err)
	}
	messagesB, err := client.Client.ListMemoryMessagesFiltered(ctx, memoryID, ragflow.MemoryMessageListFilter{
		AgentID: "live-e2e-agent", SessionID: "identity-b-session", UserID: expectedB,
		Page: 1, PageSize: 100,
	})
	if err != nil || len(messagesB) != 1 || messagesB[0].UserID != expectedB {
		t.Fatalf("tenant B attribution search failed: messages=%+v err=%v", messagesB, err)
	}
	crossTenant, err := client.Client.ListMemoryMessagesFiltered(ctx, memoryID, ragflow.MemoryMessageListFilter{
		AgentID: "live-e2e-agent", SessionID: "identity-a-session", UserID: expectedB,
		Page: 1, PageSize: 100,
	})
	if err != nil || len(crossTenant) != 0 {
		t.Fatalf("cross-tenant attribution must not match: messages=%+v err=%v", crossTenant, err)
	}

	messageIDA, err := strconv.ParseInt(operationA.RagflowMessageID, 10, 64)
	if err != nil || messageIDA <= 0 {
		t.Fatalf("invalid tenant A live message id %q: %v", operationA.RagflowMessageID, err)
	}
	if err := client.Client.DeleteMemoryMessage(ctx, memoryID, messageIDA); err != nil {
		t.Fatalf("delete tenant A live message: %v", err)
	}
	waitForLiveMemoryMessageGone(t, client, ctx, memoryID, "identity-a-session")

	targetB.Status = model.UserStatusDisabled
	if err := svc.Store.UpdateUser(ctx, targetB); err != nil {
		t.Fatal(err)
	}
	addCallsBeforeDisabled := client.addCalls
	if _, err := svc.CreateMemoryDispatchOperation(ctx, policyB.TenantID, targetB.ID, MemoryDispatchOperationInput{
		PolicyID: policyB.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-identity-disabled", IdempotencyKey: "idempotency-identity-disabled",
		UserID: targetB.ID, AssistantID: policyB.AssistantID, SessionID: "identity-b-disabled-session",
		UserInput: "private live identity user question", AgentResponse: "private live identity answer",
	}); err == nil {
		t.Fatal("disabled live principal must not create an operation")
	}
	if client.addCalls != addCallsBeforeDisabled {
		t.Fatalf("disabled live principal must not dispatch: before=%d after=%d", addCallsBeforeDisabled, client.addCalls)
	}

	targetA.Username = "memory-identity-rebound-" + id.New()
	if err := svc.Store.UpdateUser(ctx, targetA); err != nil {
		t.Fatal(err)
	}
	operationA2 := dispatchLiveMemoryWrite(
		t, svc, targetA, policyA, memoryID, "identity-a-rebound-session",
		"request-identity-a-rebound", "idempotency-identity-a-rebound",
	)
	if operationA2.State != model.MemoryOperationExecuted ||
		operationA2.RagflowAttributionUserID != expectedA {
		t.Fatalf("username rebinding must preserve stable identity: %+v", operationA2)
	}
}
