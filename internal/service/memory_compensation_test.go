package service

import (
	"context"
	"errors"
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

func newMemoryCompensationFixture(
	t *testing.T, tenantName string,
) (*Service, *gorm.DB, *ragflow.Mock, *model.User, *model.MemoryPolicy, string) {
	t.Helper()
	gdb, err := db.Open(config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "memory-compensation.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	store := repository.NewStore(gdb)
	svc := New(store, ragflow.NewMock(), jwt.NewManager("secret", 24), "key")
	ctx := context.Background()
	if err := svc.BootstrapAdmin(ctx, "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Store.Close() })
	tenant, err := svc.CreateTenant(ctx, tenantName)
	if err != nil {
		t.Fatal(err)
	}
	target, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "memory-compensation-" + id.New(), Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Fatalf("expected mock provider, got %T", svc.RAGFlow)
	}
	memoryID, err := mock.CreateMemory(ctx, ragflow.CreateMemoryRequest{
		Name: "memory-compensation", MemoryType: []string{"raw"},
		EmbdID: "embedding-model", LLMID: "chat-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: user=%+v err=%v", admin, err)
	}
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, UserID: target.ID, AssistantID: "assistant-1",
		RagflowMemoryID: memoryID, State: model.MemoryPolicyActive,
		ReadPolicy: "owner", WritePolicy: "owner", PolicyVersion: 1,
		PolicyGeneration: 1, ConsentVersion: 1, Config: "{}",
		CreatedBy: admin.ID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	return svc, gdb, mock, target, policy, memoryID
}

func makeMemoryOperationTombstoned(
	t *testing.T, svc *Service, target *model.User, policy *model.MemoryPolicy,
	memoryID, requestID, idempotencyKey string,
) *model.MemoryOperation {
	t.Helper()
	ctx := context.Background()
	operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: requestID, IdempotencyKey: idempotencyKey,
		UserID: target.ID, AssistantID: "assistant-1",
		UserInput: "private user question", AgentResponse: "private assistant answer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatal(err)
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operation.ID)
	if err != nil || stored == nil {
		t.Fatalf("load dispatched operation: operation=%+v err=%v", stored, err)
	}
	if stored.State != model.MemoryOperationExecuted || stored.RagflowMessageID == "" {
		t.Fatalf("successful dispatch must bind external message: %+v", stored)
	}
	tombstoned, err := svc.TombstoneMemoryOperation(ctx, policy.TenantID, target.ID, stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	return tombstoned
}

func ageMemoryTombstone(t *testing.T, gdb *gorm.DB, operation *model.MemoryOperation) {
	t.Helper()
	visibleAt := time.Now().UTC().Add(-memoryCompensationVisibilityGrace - time.Minute)
	if err := gdb.Model(&model.MemoryOperation{}).
		Where("tenant_id = ? AND id = ?", operation.TenantID, operation.ID).
		Update("tombstone_created_at", visibleAt).Error; err != nil {
		t.Fatal(err)
	}
}

func findMemoryCompensationAudit(audits []model.AuditLog, operationID string) *model.AuditLog {
	for index := range audits {
		if audits[index].Action == "memory_operation.tombstone_compensated" &&
			audits[index].ResourceID == operationID {
			return &audits[index]
		}
	}
	return nil
}

func TestMemoryCompensationDeletesExternalMessageByContentFingerprint(t *testing.T) {
	svc, gdb, mock, target, policy, memoryID := newMemoryCompensationFixture(
		t, "Memory Compensation Success",
	)
	ctx := context.Background()
	if err := mock.AddMemoryMessage(ctx, ragflow.AddMessageRequest{
		MemoryIDs: []string{memoryID}, AgentID: "assistant-1",
		SessionID: "session-1", UserID: "rgx:" + policy.TenantID + ":" + target.ID,
		UserInput: "private user question", AgentResponse: "private assistant answer",
	}); err != nil {
		t.Fatal(err)
	}
	operation := makeMemoryOperationTombstoned(
		t, svc, target, policy, memoryID, "request-compensation", "idempotency-compensation",
	)
	ageMemoryTombstone(t, gdb, operation)

	summary, err := svc.ProcessMemoryCompensations(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Scanned != 1 || summary.Compensated != 1 || summary.Absent != 0 ||
		summary.Pending != 0 || summary.Skipped != 0 {
		t.Fatalf("unexpected compensation summary: %+v", summary)
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operation.ID)
	if err != nil || stored == nil {
		t.Fatalf("load compensated operation: operation=%+v err=%v", stored, err)
	}
	if stored.State != model.MemoryOperationReconciled ||
		stored.ReasonCode != "memory_tombstone_compensated" ||
		stored.ClaimOwner != "" {
		t.Fatalf("compensation did not reconcile: %+v", stored)
	}
	messages, err := mock.ListMemoryMessages(ctx, memoryID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("compensation must disable only the bound external message: messages=%d err=%v", len(messages), err)
	}
	for _, message := range messages {
		if message.MessageID == 1 && !memoryMessageActive(message) {
			t.Fatalf("compensation must preserve the historical duplicate: %+v", message)
		}
		if message.MessageID != 1 && memoryMessageActive(message) {
			t.Fatalf("compensation must forget the bound external message: %+v", message)
		}
	}
	audits, err := svc.Store.ListAuditsAll(ctx, policy.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	audit := findMemoryCompensationAudit(audits, stored.ID)
	if audit == nil || audit.Result != "SUCCESS" {
		t.Fatalf("compensation audit missing: %+v", audit)
	}
}

func TestMemoryCompensationMarksAbsentOnlyAfterCompleteScan(t *testing.T) {
	svc, gdb, mock, target, policy, memoryID := newMemoryCompensationFixture(
		t, "Memory Compensation Absent",
	)
	ctx := context.Background()
	operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-absent", IdempotencyKey: "idempotency-absent",
		UserID: target.ID, AssistantID: "assistant-1",
		UserInput: "private absent question", AgentResponse: "private absent answer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatal(err)
	}
	messages, err := mock.ListMemoryMessages(ctx, memoryID)
	if err != nil || len(messages) != 1 {
		t.Fatalf("expected external message before absence drill: messages=%+v err=%v", messages, err)
	}
	if err := mock.DeleteMemoryMessage(ctx, memoryID, messages[0].MessageID); err != nil {
		t.Fatal(err)
	}
	if err := gdb.Model(&model.MemoryOperation{}).
		Where("tenant_id = ? AND id = ?", policy.TenantID, operation.ID).
		Update("ragflow_message_id", "").Error; err != nil {
		t.Fatal(err)
	}
	tombstoned, err := svc.TombstoneMemoryOperation(ctx, policy.TenantID, target.ID, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	ageMemoryTombstone(t, gdb, tombstoned)

	summary, err := svc.ProcessMemoryCompensations(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Scanned != 1 || summary.Absent != 1 || summary.Pending != 0 {
		t.Fatalf("unexpected absent summary: %+v", summary)
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operation.ID)
	if err != nil || stored == nil {
		t.Fatal(err)
	}
	if stored.State != model.MemoryOperationReconciled ||
		stored.ReasonCode != "memory_tombstone_external_absent" {
		t.Fatalf("absent operation must reconcile safely: %+v", stored)
	}
}

func TestMemoryCompensationClaimFencingRejectsStaleCompletion(t *testing.T) {
	svc, _, _, _, policy, _ := newMemoryCompensationFixture(t, "Memory Compensation Fencing")
	store := svc.Store
	ctx := context.Background()
	now := time.Now().UTC()
	tombstoneAt := now.Add(-memoryCompensationVisibilityGrace - time.Minute)
	operation := &model.MemoryOperation{
		ID: id.New(), TenantID: policy.TenantID, PolicyID: "policy-1", MemoryID: "memory-1",
		AssistantID: "assistant-1", UserID: "user-1", OutboxEventID: "event-1",
		RequestID: "request-1", OperationType: "write", IdempotencyKey: "idempotency-1",
		State: model.MemoryOperationExecuted, PolicyVersion: 1, PolicyGeneration: 1,
		ConsentVersion: 1, ExternalMessageHash: "hash", TombstoneActive: true,
		TombstoneCreatedAt: &tombstoneAt, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateMemoryOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}

	first, err := store.ClaimDueMemoryCompensationOperations(
		ctx, now.Add(-time.Minute), now, now.Add(time.Minute), "owner-1", 10,
	)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim: operations=%+v err=%v", first, err)
	}
	second, err := store.ClaimDueMemoryCompensationOperations(
		ctx, now.Add(-time.Minute), now, now.Add(time.Minute), "owner-2", 10,
	)
	if err != nil || len(second) != 0 {
		t.Fatalf("second claim must be fenced: operations=%+v err=%v", second, err)
	}
	completed, err := store.CompleteMemoryCompensationOperation(
		ctx, operation.TenantID, operation.ID, "owner-2", "2",
		"memory_tombstone_compensated", now,
	)
	if err != nil || completed {
		t.Fatalf("stale completion must be rejected: completed=%v err=%v", completed, err)
	}
	completed, err = store.CompleteMemoryCompensationOperation(
		ctx, "other-tenant", operation.ID, "owner-1", "2",
		"memory_tombstone_compensated", now,
	)
	if err != nil || completed {
		t.Fatalf("cross-tenant completion must be rejected: completed=%v err=%v", completed, err)
	}
}

func TestMemoryCompensationFailureEmitsDurableAlert(t *testing.T) {
	svc, gdb, mock, target, policy, memoryID := newMemoryCompensationFixture(
		t, "Memory Compensation Failure",
	)
	ctx := context.Background()
	operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID:      "request-compensation-failure",
		IdempotencyKey: "idempotency-compensation-failure",
		UserID:         target.ID, AssistantID: "assistant-1",
		UserInput: "private user question", AgentResponse: "private assistant answer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatal(err)
	}
	tombstoned, err := svc.TombstoneMemoryOperation(ctx, policy.TenantID, target.ID, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	ageMemoryTombstone(t, gdb, tombstoned)
	alerts := startMemoryAlertCapture(t, svc)
	mock.SetMemoryListMessagesFilteredError(errors.New("memory list unavailable"))

	summary, err := svc.ProcessMemoryCompensations(ctx, 10)
	if err == nil {
		t.Fatal("compensation provider failure must be returned")
	}
	if summary.Scanned != 1 || summary.Compensated != 0 || summary.Absent != 0 ||
		summary.Pending != 0 || summary.Skipped != 0 {
		t.Fatalf("failed pass must count only scanned: %+v", summary)
	}
	alert := waitForMemoryAlert(t, alerts)
	if alert.ID != "memory-compensation-failed:"+operation.ID ||
		alert.Type != "memory.compensation.failed" {
		t.Fatalf("unexpected compensation failure alert: %+v", alert)
	}
	if alert.Fields["operation_id"] != operation.ID ||
		alert.Fields["request_id"] != operation.RequestID ||
		alert.Fields["policy_id"] != policy.ID {
		t.Fatalf("failure alert metadata mismatch: %+v", alert.Fields)
	}
	if strings.Contains(strings.ToLower(alert.Detail), "private") {
		t.Fatalf("failure alert leaked content: %+v", alert)
	}
	if mock.MemoryAddCalls() != 1 {
		t.Fatalf("compensation failure must not retry dispatch: add=%d", mock.MemoryAddCalls())
	}
}

func TestSetupWorkerRegistersAndSchedulesMemoryCompensation(t *testing.T) {
	svc, _, _, _, _, _ := newMemoryCompensationFixture(t, "Memory Compensation Worker")
	runner := svc.SetupWorker(DefaultWorkerConfig())
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runner.Stop(stopCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	}()
	active, err := svc.Store.CountActiveJobsExcept(
		context.Background(), model.JobKindMemoryCompensation, SystemTenantID, "",
	)
	if err != nil || active != 1 {
		t.Fatalf("memory compensation recurring job not scheduled: active=%d err=%v", active, err)
	}
}
