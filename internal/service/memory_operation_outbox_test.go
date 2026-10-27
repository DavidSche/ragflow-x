package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/notify"
	"github.com/ragflow-x/ragflow-x/internal/pkg/crypto"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type memoryAlertCapture struct {
	events chan notify.Event
}

func (c memoryAlertCapture) Send(_ context.Context, event notify.Event) error {
	c.events <- event
	return nil
}

func (c memoryAlertCapture) Name() string {
	return "memory-alert-capture"
}

func startMemoryAlertCapture(t *testing.T, svc *Service) <-chan notify.Event {
	t.Helper()
	events := make(chan notify.Event, 4)
	hub := notify.NewHubWithNotifiers([]notify.Notifier{
		memoryAlertCapture{events: events},
	}, 0)
	hub.SetSink(svc)
	notify.Set(hub)
	t.Cleanup(func() { notify.Set(nil) })
	return events
}

func waitForMemoryAlert(t *testing.T, events <-chan notify.Event) notify.Event {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("memory compensation alert was not emitted")
		return notify.Event{}
	}
}

func newMemoryDispatchAlertFixture(
	t *testing.T, name string,
) (*Service, *ragflow.Mock, *model.User, *model.MemoryPolicy, string) {
	t.Helper()
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	target, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "memory-dispatch-" + id.New(), Password: "secret123",
		Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Fatalf("expected mock provider, got %T", svc.RAGFlow)
	}
	memoryID, err := mock.CreateMemory(ctx, ragflow.CreateMemoryRequest{
		Name: name, MemoryType: []string{"raw"},
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
	return svc, mock, target, policy, memoryID
}

func TestMemoryDispatchWorkerExecutesWriteOnce(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Memory Write Worker")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: user=%+v err=%v", admin, err)
	}
	now := time.Now().UTC()
	target := &model.User{
		ID: id.New(), TenantID: tenant.ID, Username: "memory-dispatch-" + id.New(),
		Role: model.RoleBusinessUser, Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateUser(ctx, target); err != nil {
		t.Fatal(err)
	}
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Fatalf("expected mock provider, got %T", svc.RAGFlow)
	}
	memoryID, err := mock.CreateMemory(ctx, ragflow.CreateMemoryRequest{
		Name: "memory-write-worker", MemoryType: []string{"raw"},
		EmbdID: "embedding-model", LLMID: "chat-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, UserID: target.ID, AssistantID: "assistant-1",
		RagflowMemoryID: memoryID,
		State:           model.MemoryPolicyActive, ReadPolicy: "owner", WritePolicy: "owner",
		PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1,
		Config: "{}", CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	operation, err := svc.CreateMemoryDispatchOperation(ctx, tenant.ID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-write-once", IdempotencyKey: "idempotency-write-once",
		UserID: target.ID, AssistantID: "assistant-1",
		UserInput: "user question", AgentResponse: "assistant answer",
	})
	if err != nil {
		t.Fatalf("create memory dispatch operation: %v", err)
	}
	if operation.State != model.MemoryOperationAccepted || operation.OutboxEventID == "" {
		t.Fatalf("operation must start accepted with outbox reference: %+v", operation)
	}

	event, err := svc.Store.GetMemoryDispatchOutboxEvent(ctx, tenant.ID, operation.OutboxEventID)
	if err != nil || event == nil || event.PublishedAt != nil {
		t.Fatalf("durable event: event=%+v err=%v", event, err)
	}
	lowerPayload := strings.ToLower(event.Payload)
	if strings.Contains(lowerPayload, "answer") || strings.Contains(lowerPayload, "ciphertext") {
		t.Fatalf("outbox payload must not carry memory content or ciphertext: %s", event.Payload)
	}

	beforeMessages, _ := mock.ListMemoryMessages(ctx, operation.MemoryID)
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("process outbox: %v", err)
	}
	afterMessages, _ := mock.ListMemoryMessages(ctx, operation.MemoryID)
	if len(afterMessages) != len(beforeMessages)+1 || mock.MemoryAddCalls() != 1 {
		t.Fatalf("worker must dispatch exactly once: before=%d after=%d calls=%d",
			len(beforeMessages), len(afterMessages), mock.MemoryAddCalls())
	}
	if request := mock.LastMemoryAddRequest(); request == nil || len(request.MemoryIDs) != 1 ||
		request.MemoryIDs[0] != memoryID || request.UserID != operation.RagflowAttributionUserID {
		t.Fatalf("unexpected dispatch request: %+v", request)
	}

	stored, err := svc.Store.GetMemoryOperation(ctx, tenant.ID, operation.ID)
	if err != nil || stored == nil {
		t.Fatalf("load operation: operation=%+v err=%v", stored, err)
	}
	if stored.State != model.MemoryOperationExecuted || stored.ReasonCode != "memory_dispatch_external_accepted" {
		t.Fatalf("confirmed provider acceptance must execute: %+v", stored)
	}
	if stored.PayloadCiphertext != "" || stored.PayloadKeyID != "" || stored.PayloadHash == "" {
		t.Fatalf("terminal executed operation must delete envelope but retain hash: %+v", stored)
	}
	if stored.AdmissionPolicyGeneration == nil || *stored.AdmissionPolicyGeneration != policy.PolicyGeneration ||
		stored.DispatchAdmittedAt == nil {
		t.Fatalf("admission boundary was not recorded: %+v", stored)
	}
	completed, err := svc.Store.GetMemoryDispatchOutboxEvent(ctx, tenant.ID, operation.OutboxEventID)
	if err != nil || completed == nil || completed.PublishedAt == nil {
		t.Fatalf("terminal skipped operation must close outbox: event=%+v err=%v", completed, err)
	}

}

func TestMemoryDispatchSuccessRecordsCostAttribution(t *testing.T) {
	ctx := context.Background()
	svc, _, target, policy, memoryID := newMemoryDispatchAlertFixture(t, "Memory Cost Attribution")
	operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-memory-cost", IdempotencyKey: "idempotency-memory-cost",
		UserID: target.ID, AssistantID: policy.AssistantID,
		UserInput: "user question", AgentResponse: "assistant answer",
	})
	if err != nil {
		t.Fatalf("create memory dispatch: %v", err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("process memory dispatch: %v", err)
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operation.ID)
	if err != nil || stored == nil || stored.State != model.MemoryOperationExecuted {
		t.Fatalf("memory dispatch not executed: %+v err=%v", stored, err)
	}
	metrics, _, err := svc.Store.ListCostMetrics(ctx, policy.TenantID, false, 1, 10, repository.CostMetricFilter{
		Scenario: "memory",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 {
		t.Fatalf("expected one memory cost metric: %+v", metrics)
	}
	metric := metrics[0]
	if metric.RequestID != "memory:"+operation.ID || metric.Scenario != "memory" ||
		metric.ProjectID != policy.ProjectID || metric.XAssistantID != policy.AssistantID ||
		metric.TokensIn != 4 || metric.TokensOut != 4 || !metric.Estimated ||
		metric.EstimationPolicyVersion != "memory-dispatch-character-estimate-v1" {
		t.Fatalf("unexpected memory cost metric: %+v", metric)
	}
}

func TestMemoryDispatchOperationSealsPayloadBeforeAccepted(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Memory Sealed Payload")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	target := &model.User{
		ID: id.New(), TenantID: tenant.ID, Username: "memory-sealed-" + id.New(),
		Role: model.RoleBusinessUser, Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateUser(ctx, target); err != nil {
		t.Fatal(err)
	}
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, UserID: target.ID, AssistantID: "assistant-1",
		RagflowMemoryID: "memory-sealed",
		State:           model.MemoryPolicyActive, ReadPolicy: "owner", WritePolicy: "owner",
		PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1,
		Config: "{}", CreatedBy: target.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	operation, err := svc.CreateMemoryDispatchOperation(ctx, tenant.ID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: "memory-sealed", OperationType: "write",
		RequestID: "request-sealed", IdempotencyKey: "idempotency-sealed",
		UserID: target.ID, AssistantID: "assistant-1",
		UserInput: "private user question", AgentResponse: "private assistant answer",
	})
	if err != nil {
		t.Fatalf("create sealed memory dispatch: %v", err)
	}
	if operation.State != model.MemoryOperationAccepted {
		t.Fatalf("sealed operation must be accepted before admission: %+v", operation)
	}
	if operation.PayloadCiphertext == "" || operation.PayloadKeyID == "" ||
		!strings.HasPrefix(operation.PayloadHash, "hmac-sha256:v1:") {
		t.Fatalf("write operation must have sealed envelope evidence: %+v", operation)
	}
	if operation.RagflowAttributionUserID != "rgx:"+tenant.ID+":"+target.ID ||
		operation.RagflowAuthPrincipalID != "ragflow-tenant:"+tenant.ID {
		t.Fatalf("principal mapping was not persisted: %+v", operation)
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, tenant.ID, operation.ID)
	if err != nil || stored == nil {
		t.Fatalf("load sealed operation: operation=%+v err=%v", stored, err)
	}
	plaintext, err := crypto.Decrypt(svc.EncryptKey, stored.PayloadCiphertext)
	if err != nil {
		t.Fatalf("decrypt persisted envelope: %v", err)
	}
	var envelope MemoryOperationPayloadEnvelope
	if err := json.Unmarshal([]byte(plaintext), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != 1 || envelope.OperationID != operation.ID ||
		envelope.Provider.UserInput != "private user question" ||
		envelope.Provider.AgentResponse != "private assistant answer" {
		t.Fatalf("unexpected sealed envelope: %+v", envelope)
	}
	if strings.Contains(stored.PayloadCiphertext, "private user question") {
		t.Fatal("payload must not remain plaintext in ciphertext")
	}

	event, err := svc.Store.GetMemoryDispatchOutboxEvent(ctx, tenant.ID, stored.OutboxEventID)
	if err != nil || event == nil {
		t.Fatalf("load dispatch event: event=%+v err=%v", event, err)
	}
	lowerPayload := strings.ToLower(event.Payload)
	if strings.Contains(lowerPayload, "private user question") ||
		strings.Contains(lowerPayload, "payload_ciphertext") {
		t.Fatalf("outbox payload must not leak envelope: %s", event.Payload)
	}
}

func TestMemoryDispatchProviderFailureBecomesUnknownWithoutRetry(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Memory Provider Failure")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	target := &model.User{
		ID: id.New(), TenantID: tenant.ID, Username: "memory-failure-" + id.New(),
		Role: model.RoleBusinessUser, Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateUser(ctx, target); err != nil {
		t.Fatal(err)
	}
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Fatalf("expected mock provider, got %T", svc.RAGFlow)
	}
	memoryID, err := mock.CreateMemory(ctx, ragflow.CreateMemoryRequest{
		Name: "memory-provider-failure", MemoryType: []string{"raw"},
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
		CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	operation, err := svc.CreateMemoryDispatchOperation(ctx, tenant.ID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-failure", IdempotencyKey: "idempotency-failure",
		UserID: target.ID, AssistantID: "assistant-1",
		UserInput: "private user question", AgentResponse: "private assistant answer",
	})
	if err != nil {
		t.Fatal(err)
	}
	alerts := startMemoryAlertCapture(t, svc)
	var retryErr *httperr.Error
	if _, err := svc.RequestOutboxEventRetry(ctx, tenant.ID, target.ID, operation.OutboxEventID); !errors.As(err, &retryErr) || retryErr.Status != 409 {
		t.Fatalf("pending memory dispatch must deny manual retry: err=%v", err)
	}
	mock.SetMemoryAddError(errors.New("ragflow unavailable"))
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("provider failure must be classified: %v", err)
	}

	stored, err := svc.Store.GetMemoryOperation(ctx, tenant.ID, operation.ID)
	if err != nil || stored == nil {
		t.Fatalf("load operation: operation=%+v err=%v", stored, err)
	}
	if stored.State != model.MemoryOperationUnknown ||
		stored.ReasonCode != "memory_dispatch_provider_unconfirmed" {
		t.Fatalf("ambiguous provider result must be unknown: %+v", stored)
	}
	if stored.PayloadCiphertext != "" || stored.PayloadKeyID != "" || stored.PayloadHash == "" {
		t.Fatalf("unknown operation must clear envelope but retain hash: %+v", stored)
	}
	event, err := svc.Store.GetMemoryDispatchOutboxEvent(ctx, tenant.ID, operation.OutboxEventID)
	if err != nil || event == nil || event.PublishedAt == nil {
		t.Fatalf("unknown operation must close outbox: event=%+v err=%v", event, err)
	}
	if messages, _ := mock.ListMemoryMessages(ctx, memoryID); len(messages) != 0 {
		t.Fatalf("provider failure must not record a message: messages=%d", len(messages))
	}
	if mock.MemoryAddCalls() != 1 {
		t.Fatalf("provider failure must not automatically retry: calls=%d", mock.MemoryAddCalls())
	}
	alert := waitForMemoryAlert(t, alerts)
	if alert.ID != "memory-compensation-required:"+operation.ID ||
		alert.Type != "memory.compensation.required" {
		t.Fatalf("unexpected provider failure alert: %+v", alert)
	}
	if alert.Fields["request_id"] != operation.RequestID ||
		alert.Fields["reason"] != stored.ReasonCode {
		t.Fatalf("alert metadata mismatch: %+v", alert.Fields)
	}
	for key, value := range alert.Fields {
		if strings.Contains(strings.ToLower(key), "content") ||
			strings.Contains(strings.ToLower(value), "private") {
			t.Fatalf("alert leaked sensitive metadata: %s=%s", key, value)
		}
	}
}

func TestMemoryDispatchExternalListFailureRequiresCompensation(t *testing.T) {
	svc, mock, target, policy, memoryID := newMemoryDispatchAlertFixture(
		t, "Memory External List Failure",
	)
	ctx := context.Background()
	operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-list-failure", IdempotencyKey: "idempotency-list-failure",
		UserID: target.ID, AssistantID: "assistant-1",
		UserInput: "private user question", AgentResponse: "private assistant answer",
	})
	if err != nil {
		t.Fatal(err)
	}
	alerts := startMemoryAlertCapture(t, svc)
	mock.SetMemoryListMessagesFilteredError(errors.New("memory list unavailable"))
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatal(err)
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operation.ID)
	if err != nil || stored == nil {
		t.Fatal(err)
	}
	if stored.State != model.MemoryOperationUnknown ||
		stored.ReasonCode != "memory_dispatch_external_message_unconfirmed" {
		t.Fatalf("list failure must be unconfirmed: %+v", stored)
	}
	if mock.MemoryAddCalls() != 1 || mock.MemoryListMessagesFilteredCalls() != 1 {
		t.Fatalf("unexpected retry behavior: add=%d list=%d",
			mock.MemoryAddCalls(), mock.MemoryListMessagesFilteredCalls())
	}
	alert := waitForMemoryAlert(t, alerts)
	if alert.ID != "memory-compensation-required:"+operation.ID ||
		alert.Type != "memory.compensation.required" {
		t.Fatalf("unexpected list failure alert: %+v", alert)
	}
	if strings.Contains(strings.ToLower(alert.Detail), "private") {
		t.Fatalf("list failure alert leaked content: %+v", alert)
	}
}

func TestMemoryDispatchAbsentExternalMessageRequiresCompensation(t *testing.T) {
	svc, mock, target, policy, memoryID := newMemoryDispatchAlertFixture(
		t, "Memory External Message Absent",
	)
	ctx := context.Background()
	operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-absent", IdempotencyKey: "idempotency-absent",
		UserID: target.ID, AssistantID: "assistant-1",
		UserInput: "private user question", AgentResponse: "private assistant answer",
	})
	if err != nil {
		t.Fatal(err)
	}
	alerts := startMemoryAlertCapture(t, svc)
	mock.SetMemoryListMessagesSuppressNext(true)
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatal(err)
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operation.ID)
	if err != nil || stored == nil {
		t.Fatal(err)
	}
	if stored.State != model.MemoryOperationUnknown ||
		stored.ReasonCode != "memory_dispatch_external_message_absent" {
		t.Fatalf("missing external identity must be absent: %+v", stored)
	}
	if mock.MemoryAddCalls() != 1 || mock.MemoryListMessagesFilteredCalls() != 1 {
		t.Fatalf("unexpected retry behavior: add=%d list=%d",
			mock.MemoryAddCalls(), mock.MemoryListMessagesFilteredCalls())
	}
	alert := waitForMemoryAlert(t, alerts)
	if alert.ID != "memory-compensation-required:"+operation.ID ||
		alert.Type != "memory.compensation.required" {
		t.Fatalf("unexpected absent alert: %+v", alert)
	}
	if alert.Fields["operation_id"] != operation.ID ||
		alert.Fields["policy_id"] != policy.ID {
		t.Fatalf("absent alert metadata mismatch: %+v", alert.Fields)
	}
}

func TestMemoryDispatchRecoveredDispatchedStateClosesWithoutResend(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Memory Dispatched Recovery")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	target := &model.User{
		ID: id.New(), TenantID: tenant.ID, Username: "memory-recovery-" + id.New(),
		Role: model.RoleBusinessUser, Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateUser(ctx, target); err != nil {
		t.Fatal(err)
	}
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Fatalf("expected mock provider, got %T", svc.RAGFlow)
	}
	memoryID, err := mock.CreateMemory(ctx, ragflow.CreateMemoryRequest{
		Name: "memory-dispatched-recovery", MemoryType: []string{"raw"},
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
		CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	operationID := id.New()
	eventID := id.New()
	operation := &model.MemoryOperation{
		ID: operationID, TenantID: tenant.ID, PolicyID: policy.ID, MemoryID: memoryID,
		AssistantID: "assistant-1", UserID: target.ID, OutboxEventID: eventID,
		RequestID: "request-recovery", OperationType: "write", IdempotencyKey: "idempotency-recovery",
		State: model.MemoryOperationDispatched, PolicyVersion: 1, PolicyGeneration: 1,
		ConsentVersion: 1, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	sealTestMemoryOperation(t, svc, operation)
	admittedAt := now
	operation.DispatchAdmittedAt = &admittedAt
	event := &model.OutboxEvent{
		ID: eventID, TenantID: tenant.ID, EventType: model.EventTypeMemoryOperationDispatch,
		AggregateType: model.AggregateTypeMemoryOperation, AggregateID: operationID,
		Payload:    `{"x_operation_id":"` + operationID + `","request_id":"request-recovery","x_policy_id":"` + policy.ID + `"}`,
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

	stored, err := svc.Store.GetMemoryOperation(ctx, tenant.ID, operationID)
	if err != nil || stored == nil {
		t.Fatalf("load recovered operation: operation=%+v err=%v", stored, err)
	}
	if stored.State != model.MemoryOperationUnknown ||
		stored.ReasonCode != "memory_dispatch_provider_unconfirmed" {
		t.Fatalf("dispatched recovery must enter unknown: %+v", stored)
	}
	if mock.MemoryAddCalls() != 0 {
		t.Fatalf("dispatched recovery must never resend: calls=%d", mock.MemoryAddCalls())
	}
	if messages, _ := mock.ListMemoryMessages(ctx, memoryID); len(messages) != 0 {
		t.Fatalf("dispatched recovery must not create provider content: messages=%d", len(messages))
	}
	completed, err := svc.Store.GetMemoryDispatchOutboxEvent(ctx, tenant.ID, eventID)
	if err != nil || completed == nil || completed.PublishedAt == nil {
		t.Fatalf("dispatched recovery must close outbox: event=%+v err=%v", completed, err)
	}
}

func TestMemoryDispatchInactivePrincipalIsBlocked(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Memory Inactive Principal")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	target := &model.User{
		ID: id.New(), TenantID: tenant.ID, Username: "memory-inactive-" + id.New(),
		Role: model.RoleBusinessUser, Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateUser(ctx, target); err != nil {
		t.Fatal(err)
	}
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, UserID: target.ID, AssistantID: "assistant-1",
		RagflowMemoryID: "memory-inactive", State: model.MemoryPolicyActive,
		ReadPolicy: "owner", WritePolicy: "owner", PolicyVersion: 1,
		PolicyGeneration: 1, ConsentVersion: 1, Config: "{}",
		CreatedBy: target.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	operation, err := svc.CreateMemoryDispatchOperation(ctx, tenant.ID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: "memory-inactive", OperationType: "write",
		RequestID: "request-inactive", IdempotencyKey: "idempotency-inactive",
		UserID: target.ID, AssistantID: "assistant-1",
		UserInput: "private user question", AgentResponse: "private assistant answer",
	})
	if err != nil {
		t.Fatal(err)
	}
	target.Status = model.UserStatusDisabled
	target.UpdatedAt = now.Add(time.Second)
	if err := svc.Store.UpdateUser(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatal(err)
	}

	stored, err := svc.Store.GetMemoryOperation(ctx, tenant.ID, operation.ID)
	if err != nil || stored == nil {
		t.Fatalf("load operation: operation=%+v err=%v", stored, err)
	}
	if stored.State != model.MemoryOperationBlocked ||
		stored.ReasonCode != "memory_dispatch_principal_not_active" {
		t.Fatalf("inactive principal must block without HTTP: %+v", stored)
	}
	if mock, ok := svc.RAGFlow.(*ragflow.Mock); ok && mock.MemoryAddCalls() != 0 {
		t.Fatalf("blocked operation must not call provider: calls=%d", mock.MemoryAddCalls())
	}
	event, err := svc.Store.GetMemoryDispatchOutboxEvent(ctx, tenant.ID, operation.OutboxEventID)
	if err != nil || event == nil || event.PublishedAt == nil {
		t.Fatalf("blocked operation must close outbox: event=%+v err=%v", event, err)
	}
}

func TestMemoryDispatchPrincipalIdentityIsFailClosed(t *testing.T) {
	svc, mock, target, policy, _ := newMemoryDispatchAlertFixture(t, "Memory Identity Fail Closed")
	ctx := context.Background()

	authA, attributionA := memoryDispatchIdentity("tenant-a", "principal-1")
	authB, attributionB := memoryDispatchIdentity("tenant-b", "principal-1")
	if authA == authB || attributionA == attributionB {
		t.Fatal("tenant-scoped identity must not collide")
	}

	disabledTarget := *target
	disabledTarget.Status = model.UserStatusDisabled
	if err := svc.Store.UpdateUser(ctx, &disabledTarget); err != nil {
		t.Fatal(err)
	}
	beforeCalls := mock.MemoryAddCalls()
	if _, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, disabledTarget.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: policy.RagflowMemoryID, OperationType: "write",
		RequestID: "request-disabled", IdempotencyKey: "idempotency-disabled",
		UserID: disabledTarget.ID, AssistantID: policy.AssistantID,
		UserInput: "private user question", AgentResponse: "private assistant answer",
	}); err == nil {
		t.Fatal("disabled principal must not create an operation")
	}
	if mock.MemoryAddCalls() != beforeCalls {
		t.Fatalf("rejected principal must not call provider: before=%d after=%d", beforeCalls, mock.MemoryAddCalls())
	}

	activeTarget := *target
	activeTarget.Status = model.UserStatusActive
	if err := svc.Store.UpdateUser(ctx, &activeTarget); err != nil {
		t.Fatal(err)
	}
	operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: policy.RagflowMemoryID, OperationType: "write",
		RequestID: "request-identity", IdempotencyKey: "idempotency-identity",
		UserID: target.ID, AssistantID: policy.AssistantID,
		UserInput: "private user question", AgentResponse: "private assistant answer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if operation.RagflowAuthPrincipalID != "ragflow-tenant:"+policy.TenantID ||
		operation.RagflowAttributionUserID != "rgx:"+policy.TenantID+":"+target.ID {
		t.Fatalf("identity mapping mismatch: %+v", operation)
	}

	target.Username = "memory-rebound-" + id.New()
	if err := svc.Store.UpdateUser(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatal(err)
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operation.ID)
	if err != nil || stored == nil {
		t.Fatal(err)
	}
	if stored.State != model.MemoryOperationExecuted ||
		stored.RagflowAttributionUserID != "rgx:"+policy.TenantID+":"+target.ID {
		t.Fatalf("username rebinding must not change stable identity: %+v", stored)
	}
	if mock.LastMemoryAddRequest() == nil ||
		mock.LastMemoryAddRequest().UserID != stored.RagflowAttributionUserID {
		t.Fatalf("provider attribution must use canonical mapping: %+v", mock.LastMemoryAddRequest())
	}
}

func TestMemoryDispatchIdentityDriftIsBlocked(t *testing.T) {
	svc, mock, target, policy, _ := newMemoryDispatchAlertFixture(t, "Memory Identity Drift")
	ctx := context.Background()
	now := time.Now().UTC()
	operationID := id.New()
	eventID := id.New()
	operation := &model.MemoryOperation{
		ID: operationID, TenantID: policy.TenantID, PolicyID: policy.ID,
		MemoryID: policy.RagflowMemoryID, AssistantID: policy.AssistantID,
		UserID: target.ID, OutboxEventID: eventID, RequestID: "request-drift",
		OperationType: "write", IdempotencyKey: "idempotency-drift",
		State: model.MemoryOperationAccepted, PolicyVersion: policy.PolicyVersion,
		PolicyGeneration: policy.PolicyGeneration, ConsentVersion: policy.ConsentVersion,
		RagflowAuthPrincipalID:   "ragflow-tenant:" + policy.TenantID,
		RagflowAttributionUserID: "rgx:" + policy.TenantID + ":other",
		RecordVersion:            1, CreatedAt: now, UpdatedAt: now,
	}
	sealTestMemoryOperation(t, svc, operation)
	operation.RagflowAttributionUserID = "rgx:" + policy.TenantID + ":other"
	event := &model.OutboxEvent{
		ID: eventID, TenantID: policy.TenantID,
		EventType:     model.EventTypeMemoryOperationDispatch,
		AggregateType: model.AggregateTypeMemoryOperation, AggregateID: operationID,
		Payload:    `{"x_operation_id":"` + operationID + `","request_id":"request-drift","x_policy_id":"` + policy.ID + `","policy_generation":1,"consent_version":1}`,
		OccurredAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryOperationWithOutbox(ctx, operation, event); err != nil {
		t.Fatal(err)
	}
	beforeCalls := mock.MemoryAddCalls()
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatal(err)
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operation.ID)
	if err != nil || stored == nil {
		t.Fatal(err)
	}
	if stored.State != model.MemoryOperationBlocked ||
		stored.ReasonCode != "memory_dispatch_identity_drift" {
		t.Fatalf("identity drift must block admission: %+v", stored)
	}
	if mock.MemoryAddCalls() != beforeCalls {
		t.Fatalf("identity drift must not call provider: before=%d after=%d", beforeCalls, mock.MemoryAddCalls())
	}
}

func TestMemoryDispatchOperationPayloadIsFailClosed(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Memory Payload Contract")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	target := &model.User{
		ID: id.New(), TenantID: tenant.ID, Username: "memory-payload-" + id.New(),
		Role: model.RoleBusinessUser, Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateUser(ctx, target); err != nil {
		t.Fatal(err)
	}
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, UserID: target.ID, AssistantID: "assistant-1",
		RagflowMemoryID: "memory-admission", State: model.MemoryPolicyActive,
		PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1,
		Config: "{}", CreatedBy: target.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	base := MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: "memory-payload", RequestID: "request-payload",
		IdempotencyKey: "idempotency-payload", UserID: target.ID, AssistantID: "assistant-1",
	}
	missing := base
	missing.OperationType = "write"
	if _, err := svc.CreateMemoryDispatchOperation(ctx, tenant.ID, target.ID, missing); err == nil {
		t.Fatal("write operation must require a payload")
	}
	search := base
	search.OperationType = "status"
	search.UserInput = "unexpected"
	if _, err := svc.CreateMemoryDispatchOperation(ctx, tenant.ID, target.ID, search); err == nil {
		t.Fatal("non-write operation must reject payload content")
	}
	stored, total, err := svc.Store.ListMemoryOperations(ctx, tenant.ID, repository.MemoryOperationFilter{}, 1, 10)
	if err != nil || total != 0 || len(stored) != 0 {
		t.Fatalf("failed contract checks must not persist operations: items=%d total=%d err=%v", len(stored), total, err)
	}
}

func TestMemoryDispatchOperationIdempotencyIsUnique(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Memory Idempotency")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: user=%+v err=%v", admin, err)
	}
	now := time.Now().UTC()
	target := &model.User{
		ID: id.New(), TenantID: tenant.ID, Username: "memory-idempotent-" + id.New(),
		Role: model.RoleBusinessUser, Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateUser(ctx, target); err != nil {
		t.Fatal(err)
	}
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, UserID: target.ID, AssistantID: "assistant-1",
		State: model.MemoryPolicyActive, ReadPolicy: "owner", WritePolicy: "owner",
		PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1,
		Config: "{}", CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	input := MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: "memory-1", OperationType: "write",
		RequestID: "request-1", IdempotencyKey: "idempotency-1",
		UserID: target.ID, AssistantID: "assistant-1",
		UserInput: "private user question", AgentResponse: "private assistant answer",
	}
	if _, err := svc.CreateMemoryDispatchOperation(ctx, tenant.ID, target.ID, input); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateMemoryDispatchOperation(ctx, tenant.ID, target.ID, input); err == nil {
		t.Fatal("duplicate operation idempotency key must fail")
	}

	items, _, err := svc.Store.ListMemoryOperations(ctx, tenant.ID, repository.MemoryOperationFilter{}, 1, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("duplicate must not create second row: items=%d err=%v", len(items), err)
	}
}

func TestMemoryDispatchOperationPolicyOwnershipIsFailClosed(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Memory Dispatch Ownership")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: user=%+v err=%v", admin, err)
	}
	now := time.Now().UTC()
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, UserID: admin.ID, AssistantID: "assistant-1",
		State: model.MemoryPolicyActive, PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1,
		Config: "{}", CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	other := &model.User{
		ID: id.New(), TenantID: tenant.ID, Username: "memory-other-" + id.New(),
		Role: model.RoleBusinessUser, Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateUser(ctx, other); err != nil {
		t.Fatal(err)
	}
	input := MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: "memory-1", OperationType: "write",
		RequestID: "request-ownership", IdempotencyKey: "idempotency-ownership",
		UserID: other.ID, AssistantID: "assistant-1",
	}
	if _, err := svc.CreateMemoryDispatchOperation(ctx, tenant.ID, other.ID, input); err == nil {
		t.Fatal("another principal must not use an owner-scoped memory policy")
	}

	project := &model.Project{ID: id.New(), TenantID: tenant.ID, Name: "Dispatch Project", CreatedAt: now, UpdatedAt: now}
	if err := svc.Store.CreateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	projectPolicy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, ProjectID: project.ID, AssistantID: "assistant-1",
		State: model.MemoryPolicyActive, PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1,
		Config: "{}", CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, projectPolicy); err != nil {
		t.Fatal(err)
	}
	input.ProjectID = project.ID
	input.PolicyID = projectPolicy.ID
	input.RequestID = "request-project"
	input.IdempotencyKey = "idempotency-project"
	if _, err := svc.CreateMemoryDispatchOperation(ctx, tenant.ID, other.ID, input); err == nil {
		t.Fatal("non-member principal must not use a project memory policy")
	}
}

func TestMemoryDispatchAdmissionUsesCurrentPolicyAndActivePrincipal(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Memory Admission Boundary")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	target := &model.User{
		ID: id.New(), TenantID: tenant.ID, Username: "memory-admission-" + id.New(),
		Role: model.RoleBusinessUser, Status: model.UserStatusActive, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateUser(ctx, target); err != nil {
		t.Fatal(err)
	}
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, UserID: target.ID, AssistantID: "assistant-1",
		State: model.MemoryPolicyActive, PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1,
		Config: "{}", CreatedBy: target.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	operation, err := svc.CreateMemoryDispatchOperation(ctx, tenant.ID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: "memory-1", OperationType: "write",
		RequestID: "request-admission", IdempotencyKey: "idempotency-admission",
		UserID: target.ID, AssistantID: "assistant-1",
		UserInput: "private user question", AgentResponse: "private assistant answer",
	})
	if err != nil {
		t.Fatal(err)
	}

	policy.PolicyGeneration = 2
	policy.UpdatedAt = now.Add(time.Second)
	if err := svc.Store.UpdateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessOutboxEvents(ctx, 10); err != nil {
		t.Fatalf("deterministic admission rejection must close its event: %v", err)
	}
	stale, err := svc.Store.GetMemoryOperation(ctx, tenant.ID, operation.ID)
	if err != nil || stale == nil || stale.State != model.MemoryOperationBlocked ||
		stale.ReasonCode != "memory_dispatch_policy_generation_stale" {
		t.Fatalf("stale operation must be blocked: operation=%+v err=%v", stale, err)
	}

	policy.PolicyGeneration = 1
	if err := svc.Store.UpdateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	target.Status = model.UserStatusDisabled
	target.UpdatedAt = now.Add(2 * time.Second)
	if err := svc.Store.UpdateUser(ctx, target); err != nil {
		t.Fatal(err)
	}
	var retryErr *httperr.Error
	if _, err := svc.RequestOutboxEventRetry(ctx, tenant.ID, target.ID, operation.OutboxEventID); !errors.As(err, &retryErr) || retryErr.Status != 409 {
		t.Fatalf("memory dispatch must not permit manual retry: err=%v", err)
	}
}

func TestMemoryOperationOutboxCreationIsAtomic(t *testing.T) {
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "outbox-atomic.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), ragflow.NewMock(), nil, "key")
	t.Cleanup(func() { _ = svc.Store.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: "tenant-1", AssistantID: "assistant-1",
		State: model.MemoryPolicyActive, PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1,
		Config: "{}", CreatedBy: "admin", CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	operationID := id.New()
	operation := &model.MemoryOperation{
		ID: operationID, TenantID: policy.TenantID, PolicyID: policy.ID,
		MemoryID: "memory-1", AssistantID: policy.AssistantID, UserID: "user-1",
		OutboxEventID: "duplicate-event", RequestID: "request-atomic",
		OperationType: "write", IdempotencyKey: "idempotency-atomic",
		State: model.MemoryOperationAccepted, PolicyVersion: 1,
		PolicyGeneration: 1, ConsentVersion: 1, RecordVersion: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	sealTestMemoryOperation(t, svc, operation)
	event := &model.OutboxEvent{
		ID: "duplicate-event", TenantID: policy.TenantID,
		EventType:     model.EventTypeMemoryOperationDispatch,
		AggregateType: model.AggregateTypeMemoryOperation, AggregateID: operationID,
		Payload: "{}", OccurredAt: now, CreatedAt: now, UpdatedAt: now,
	}
	err = svc.Store.WithinTransaction(ctx, func(tx repository.Store) error {
		if err := tx.CreateMemoryOperationWithOutbox(ctx, operation, event); err != nil {
			return err
		}
		duplicate := *event
		duplicate.AggregateID = id.New()
		return tx.CreateMemoryOperationWithOutbox(ctx, operation, &duplicate)
	})
	if err == nil {
		t.Fatal("duplicate outbox event must fail")
	}
	if stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operationID); err != nil || stored != nil {
		t.Fatalf("failed outbox insert must roll back operation: operation=%+v err=%v", stored, err)
	}
	items, _, err := svc.Store.ListMemoryOperations(ctx, policy.TenantID, repository.MemoryOperationFilter{}, 1, 10)
	if err != nil || len(items) != 0 {
		t.Fatalf("failed outbox insert must roll back operation: items=%d err=%v", len(items), err)
	}
}

func TestMemoryOperationTombstoneCancelsAcceptedDispatch(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Memory Tombstone Accepted")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: user=%+v err=%v", admin, err)
	}
	now := time.Now().UTC()
	target, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "memory-tombstone-" + id.New(), Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, UserID: target.ID, AssistantID: "assistant-1",
		State: model.MemoryPolicyActive, ReadPolicy: "owner", WritePolicy: "owner",
		PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1,
		Config: "{}", CreatedBy: admin.ID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	operation, err := svc.CreateMemoryDispatchOperation(ctx, tenant.ID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: "memory-tombstone", OperationType: "write",
		RequestID: "request-tombstone", IdempotencyKey: "idempotency-tombstone",
		UserID: target.ID, AssistantID: "assistant-1",
		UserInput: "private user question", AgentResponse: "private assistant answer",
	})
	if err != nil {
		t.Fatal(err)
	}

	tombstoned, err := svc.TombstoneMemoryOperation(ctx, tenant.ID, target.ID, operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tombstoned.State != model.MemoryOperationSkipped ||
		tombstoned.ReasonCode != "memory_tombstone_cancelled" || !tombstoned.TombstoneActive {
		t.Fatalf("accepted operation must be cancelled: %+v", tombstoned)
	}
	if tombstoned.TombstoneScopeHash == "" || tombstoned.TombstonePayloadHash == "" ||
		tombstoned.TombstoneMessageHash == "" || tombstoned.TombstoneCreatedAt == nil {
		t.Fatalf("tombstone evidence must be durable: %+v", tombstoned)
	}
	if tombstoned.PayloadCiphertext != "" || tombstoned.PayloadKeyID != "" || tombstoned.PayloadHash == "" {
		t.Fatalf("cancelled accepted operation must delete envelope but retain hash: %+v", tombstoned)
	}
	event, err := svc.Store.GetMemoryDispatchOutboxEvent(ctx, tenant.ID, operation.OutboxEventID)
	if err != nil || event == nil || event.PublishedAt == nil {
		t.Fatalf("accepted tombstone must close pending outbox: event=%+v err=%v", event, err)
	}
	if mock, ok := svc.RAGFlow.(*ragflow.Mock); ok {
		messages, _ := mock.ListMemoryMessages(ctx, operation.MemoryID)
		if len(messages) != 0 {
			t.Fatalf("tombstone must not dispatch to RAGFlow: messages=%d", len(messages))
		}
	}
	audits, err := svc.Store.ListAuditsAll(ctx, tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	var tombstoneAudit *model.AuditLog
	for index := range audits {
		if audits[index].Action == "memory_operation.tombstone" && audits[index].ResourceID == operation.ID {
			tombstoneAudit = &audits[index]
			break
		}
	}
	if tombstoneAudit == nil || tombstoneAudit.AuthorizationPermission != "execute:memory-forget" {
		t.Fatalf("tombstone audit missing or incorrect: %+v", tombstoneAudit)
	}
}

func TestMemoryOperationTombstonePreservesAdmittedState(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Memory Tombstone Compensation")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	target, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "memory-admitted-" + id.New(), Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	operationID := id.New()
	eventID := id.New()
	operation := &model.MemoryOperation{
		ID: operationID, TenantID: tenant.ID, PolicyID: id.New(), MemoryID: "memory-admitted",
		AssistantID: "assistant-1", UserID: target.ID, OutboxEventID: eventID,
		RequestID: "request-admitted", OperationType: "write", IdempotencyKey: "idempotency-admitted",
		State: model.MemoryOperationAdmitted, PolicyVersion: 1, PolicyGeneration: 1,
		ConsentVersion: 1, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	sealTestMemoryOperation(t, svc, operation)
	admittedAt := now
	operation.DispatchAdmittedAt = &admittedAt
	event := &model.OutboxEvent{
		ID: eventID, TenantID: tenant.ID, EventType: model.EventTypeMemoryOperationDispatch,
		AggregateType: model.AggregateTypeMemoryOperation, AggregateID: operationID,
		Payload: `{"x_operation_id":"` + operationID + `"}`, OccurredAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryOperationWithOutbox(ctx, operation, event); err != nil {
		t.Fatal(err)
	}

	tombstoned, err := svc.TombstoneMemoryOperation(ctx, tenant.ID, target.ID, operationID)
	if err != nil {
		t.Fatal(err)
	}
	if tombstoned.State != model.MemoryOperationAdmitted || !tombstoned.TombstoneActive ||
		tombstoned.ReasonCode != "memory_tombstone_compensation_required" {
		t.Fatalf("post-admission tombstone must preserve state: %+v", tombstoned)
	}
	event, err = svc.Store.GetMemoryDispatchOutboxEvent(ctx, tenant.ID, eventID)
	if err != nil || event == nil || event.PublishedAt != nil {
		t.Fatalf("post-admission tombstone must not close outbox blindly: event=%+v err=%v", event, err)
	}
}

func TestMemoryOperationTombstoneIsOwnerOnly(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Memory Tombstone Owner")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	target, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "memory-owner-" + id.New(), Password: "secret123", Role: model.RoleBusinessUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	operationID := id.New()
	eventID := id.New()
	operation := &model.MemoryOperation{
		ID: operationID, TenantID: tenant.ID, PolicyID: id.New(), MemoryID: "memory-owner",
		AssistantID: "assistant-1", UserID: target.ID, OutboxEventID: eventID,
		RequestID: "request-owner", OperationType: "write", IdempotencyKey: "idempotency-owner",
		State: model.MemoryOperationAccepted, PolicyVersion: 1, PolicyGeneration: 1,
		ConsentVersion: 1, RecordVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	sealTestMemoryOperation(t, svc, operation)
	event := &model.OutboxEvent{
		ID: eventID, TenantID: tenant.ID, EventType: model.EventTypeMemoryOperationDispatch,
		AggregateType: model.AggregateTypeMemoryOperation, AggregateID: operationID,
		Payload: `{"x_operation_id":"` + operationID + `"}`, OccurredAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryOperationWithOutbox(ctx, operation, event); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.TombstoneMemoryOperation(ctx, tenant.ID, "not-the-owner", operationID); err == nil {
		t.Fatal("cross-principal tombstone must be denied")
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, tenant.ID, operationID)
	if err != nil || stored == nil || stored.TombstoneActive || stored.State != model.MemoryOperationAccepted {
		t.Fatalf("denied tombstone must not mutate operation: operation=%+v err=%v", stored, err)
	}
	pending, err := svc.Store.GetMemoryDispatchOutboxEvent(ctx, tenant.ID, eventID)
	if err != nil || pending == nil || pending.PublishedAt != nil {
		t.Fatalf("denied tombstone must not close outbox: event=%+v err=%v", pending, err)
	}
}

func sealTestMemoryOperation(t *testing.T, svc *Service, operation *model.MemoryOperation) {
	t.Helper()
	envelope := MemoryOperationPayloadEnvelope{
		SchemaVersion: 1, TenantID: operation.TenantID, OperationID: operation.ID,
		RequestID: operation.RequestID, PolicyID: operation.PolicyID,
		OperationType: operation.OperationType, MemoryID: operation.MemoryID,
		AssistantID: operation.AssistantID, SessionID: operation.SessionID,
		UserID: operation.UserID,
		Provider: ragflow.AddMessageRequest{
			MemoryIDs: []string{operation.MemoryID}, AgentID: operation.AssistantID,
			SessionID: operation.SessionID, UserID: "rgx:" + operation.TenantID + ":" + operation.UserID,
			UserInput: "private user question", AgentResponse: "private assistant answer",
		},
	}
	plaintext, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := crypto.Encrypt(svc.EncryptKey, string(plaintext))
	if err != nil {
		t.Fatal(err)
	}
	operation.PayloadCiphertext = ciphertext
	operation.PayloadKeyID = "test-envelope-key"
	operation.PayloadHash = "hmac-sha256:v1:test"
	operation.ExternalMessageHash = memoryExternalContentHash(
		svc.HMACKey, operation.MemoryID,
		envelope.Provider.UserInput, envelope.Provider.AgentResponse,
	)
	operation.RagflowAuthPrincipalID = "ragflow-tenant:" + operation.TenantID
	operation.RagflowAttributionUserID = "rgx:" + operation.TenantID + ":" + operation.UserID
}
