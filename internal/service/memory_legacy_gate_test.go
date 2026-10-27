package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// TestLegacyMemoryMessageGatePolicyBlocks verifies MEM-014: memories governed
// by an active policy must not be mutated through the legacy status/forget
// message endpoints.
func TestLegacyMemoryMessageGatePolicyBlocks(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Legacy Gate")
	if err != nil {
		t.Fatal(err)
	}
	mem, err := svc.CreateMemory(ctx, tenant.ID, "governed-memory", []string{"raw"}, "embedding-model", "chat-model")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, UserID: "", AssistantID: "assistant-legacy",
		RagflowMemoryID: mem.ID, State: model.MemoryPolicyActive,
		ReadPolicy: "project", WritePolicy: "member", PolicyVersion: 1,
		PolicyGeneration: 1, ConsentVersion: 0, Config: "{}",
		CreatedBy: "", CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}

	if err := svc.UpdateMemoryMessageStatus(ctx, tenant.ID, mem.ID, 42, true, false); err == nil {
		t.Fatal("legacy status toggle must be blocked for governed memory")
	} else if he, ok := err.(*httperr.Error); !ok || he.Code != 40957 {
		t.Fatalf("governed-memory status toggle must map to 40957: %v", err)
	}
	if err := svc.DeleteMemoryMessage(ctx, tenant.ID, mem.ID, 42, false); err == nil {
		t.Fatal("legacy forget must be blocked for governed memory")
	} else if he, ok := err.(*httperr.Error); !ok || he.Code != 40957 {
		t.Fatalf("governed-memory forget must map to 40957: %v", err)
	}
}

// TestLegacyMemoryMessageGateTombstoneAndForget verifies that tombstoned or
// already-forgotten messages refuse status=true, and that ungoverned messages
// are stamped forget_at before the legacy call proceeds.
func TestLegacyMemoryMessageGateTombstoneAndForget(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Legacy Tombstone Gate")
	if err != nil {
		t.Fatal(err)
	}
	mem, err := svc.CreateMemory(ctx, tenant.ID, "ungoverned-memory", []string{"raw"}, "embedding-model", "chat-model")
	if err != nil {
		t.Fatal(err)
	}

	operation := &model.MemoryOperation{
		ID: id.New(), TenantID: tenant.ID, PolicyID: "policy-x", MemoryID: mem.ID,
		AssistantID: "assistant-legacy", UserID: "user-x", OutboxEventID: "evt-x",
		RequestID: "req-legacy", OperationType: "write", State: model.MemoryOperationExecuted,
		PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1, MemoryType: "raw",
		RecordVersion: 1, PayloadHash: "payload-hash", ExternalMessageHash: "message-hash",
		PIIAction: "ALLOW", PIIRuleVersion: MemoryPrivacyRuleVersion,
		RagflowAuthPrincipalID: "principal", RagflowAttributionUserID: "attribution",
		RagflowMessageID: strconv.FormatInt(7, 10),
		CreatedAt:        time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateMemoryOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}

	// Tombstone active: status=true must be refused.
	if _, _, err := svc.Store.TombstoneMemoryOperation(
		ctx, tenant.ID, operation.ID, "scope-hash", "payload-hash", "message-hash", time.Now().UTC(),
	); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateMemoryMessageStatus(ctx, tenant.ID, mem.ID, 7, true, false); err == nil {
		t.Fatal("status=true on a tombstoned message must be refused")
	} else if he, ok := err.(*httperr.Error); !ok || he.Code != 40958 {
		t.Fatalf("tombstoned message must map to 40958: %v", err)
	}

	// forget_at already set: refused as well.
	if _, err := svc.Store.MarkMemoryOperationForgotten(ctx, tenant.ID, operation.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateMemoryMessageStatus(ctx, tenant.ID, mem.ID, 7, true, false); err == nil {
		t.Fatal("status=true on an already forgotten message must be refused")
	} else if he, ok := err.(*httperr.Error); !ok || he.Code != 40958 {
		t.Fatalf("forgotten message must map to 40958: %v", err)
	}
}

// TestLegacyMemoryMessageGateUnknownMessage verifies that messages without any
// memory operation record still pass the gate and reach the provider (legacy
// memories created outside the write path stay administrable).
func TestLegacyMemoryMessageGateUnknownMessage(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Legacy Unknown Message")
	if err != nil {
		t.Fatal(err)
	}
	mem, err := svc.CreateMemory(ctx, tenant.ID, "legacy-memory", []string{"raw"}, "embedding-model", "chat-model")
	if err != nil {
		t.Fatal(err)
	}
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Fatalf("expected mock provider, got %T", svc.RAGFlow)
	}
	if err := mock.AddMemoryMessage(ctx, ragflow.AddMessageRequest{
		MemoryIDs: []string{mem.ID}, AgentID: "assistant-legacy", SessionID: "session-legacy",
		UserInput: "legacy note", AgentResponse: "legacy answer",
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := mock.ListMemoryMessages(ctx, mem.ID)
	if err != nil || len(messages) != 1 {
		t.Fatalf("seed mock message: messages=%+v err=%v", messages, err)
	}
	messageID := messages[0].MessageID

	if err := svc.UpdateMemoryMessageStatus(ctx, tenant.ID, mem.ID, messageID, false, false); err != nil {
		t.Fatalf("legacy status toggle on unknown message must pass the gate: %v", err)
	}
	if err := svc.DeleteMemoryMessage(ctx, tenant.ID, mem.ID, messageID, false); err != nil {
		t.Fatalf("legacy forget on unknown message must pass the gate: %v", err)
	}
	after, err := mock.ListMemoryMessages(ctx, mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].Status {
		t.Fatalf("provider state must reflect legacy mutations: %+v", after)
	}
}

// TestComplianceDeleteGovernedMemory verifies the plan section 8.2 admin
// compliance delete: it works on active-policy-governed memories (unlike the
// legacy forget gate), deletes through the provider first, stamps forget_at on
// the governing operation afterwards, and refuses when a tombstone already
// owns the external deletion.
func TestComplianceDeleteGovernedMemory(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Compliance Delete")
	if err != nil {
		t.Fatal(err)
	}
	mem, err := svc.CreateMemory(ctx, tenant.ID, "compliance-memory", []string{"raw"}, "embedding-model", "chat-model")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	policy := &model.MemoryPolicy{
		ID: id.New(), TenantID: tenant.ID, UserID: "", AssistantID: "assistant-compliance",
		RagflowMemoryID: mem.ID, State: model.MemoryPolicyActive,
		ReadPolicy: "project", WritePolicy: "member", PolicyVersion: 1,
		PolicyGeneration: 1, ConsentVersion: 0, Config: "{}",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	operation := &model.MemoryOperation{
		ID: id.New(), TenantID: tenant.ID, PolicyID: policy.ID, MemoryID: mem.ID,
		AssistantID: "assistant-compliance", UserID: "user-x", OutboxEventID: "evt-compliance",
		RequestID: "req-compliance", OperationType: "write", State: model.MemoryOperationExecuted,
		PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1, MemoryType: "raw",
		RecordVersion: 1, PayloadHash: "payload-hash", ExternalMessageHash: "message-hash",
		PIIAction: "REDACT", PIIRuleVersion: MemoryPrivacyRuleVersion,
		RagflowAuthPrincipalID: "principal", RagflowAttributionUserID: "attribution",
		CreatedAt: now, UpdatedAt: now,
	}
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Fatalf("expected mock provider, got %T", svc.RAGFlow)
	}
	if err := mock.AddMemoryMessage(ctx, ragflow.AddMessageRequest{
		MemoryIDs: []string{mem.ID}, AgentID: "assistant-compliance", SessionID: "session-compliance",
		UserInput: "redacted note", AgentResponse: "redacted answer",
	}); err != nil {
		t.Fatal(err)
	}
	messages, err := mock.ListMemoryMessages(ctx, mem.ID)
	if err != nil || len(messages) != 1 {
		t.Fatalf("seed mock message: messages=%+v err=%v", messages, err)
	}
	messageID := messages[0].MessageID
	// Bind the operation to the external message id the mock actually assigned.
	operation.RagflowMessageID = strconv.FormatInt(messageID, 10)
	if err := svc.Store.CreateMemoryOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}

	// An active policy would block the legacy forget path; the compliance
	// delete is the sanctioned admin route and must succeed.
	if err := svc.ComplianceDeleteMemoryMessage(ctx, tenant.ID, mem.ID, messageID, "dsar request 42", false); err != nil {
		t.Fatalf("compliance delete on governed memory must succeed: %v", err)
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, tenant.ID, operation.ID)
	if err != nil || stored == nil {
		t.Fatal(err)
	}
	if stored.ForgetAt == nil {
		t.Fatalf("compliance delete must stamp forget_at on the governing operation: %+v", stored)
	}
	remaining, err := mock.ListMemoryMessages(ctx, mem.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range remaining {
		if message.MessageID == messageID && memoryMessageActive(message) {
			t.Fatalf("compliance delete must forget the external message: %+v", remaining)
		}
	}

	// Repeating the delete on an already-forgotten message stays idempotent.
	if err := svc.ComplianceDeleteMemoryMessage(ctx, tenant.ID, mem.ID, messageID, "", false); err != nil {
		t.Fatalf("compliance delete on a forgotten message must stay idempotent: %v", err)
	}

	// Over-long reason is rejected before any provider call.
	if err := svc.ComplianceDeleteMemoryMessage(ctx, tenant.ID, mem.ID, 999, strings.Repeat("r", 257), false); err == nil {
		t.Fatal("over-long compliance reason must be rejected")
	} else if he, ok := err.(*httperr.Error); !ok || he.Code != 40099 {
		t.Fatalf("over-long reason must map to 40099: %v", err)
	}
	if err := svc.ComplianceDeleteMemoryMessage(ctx, tenant.ID, "missing-memory", 1, "", false); err == nil {
		t.Fatal("unknown memory must 404")
	}
}

// TestComplianceDeleteTombstoneActive verifies that an active tombstone keeps
// ownership of the external deletion: the compliance delete must fail closed.
func TestComplianceDeleteTombstoneActive(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	tenant, err := svc.CreateTenant(ctx, "Compliance Tombstone")
	if err != nil {
		t.Fatal(err)
	}
	mem, err := svc.CreateMemory(ctx, tenant.ID, "tombstone-memory", []string{"raw"}, "embedding-model", "chat-model")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	operation := &model.MemoryOperation{
		ID: id.New(), TenantID: tenant.ID, PolicyID: "policy-t", MemoryID: mem.ID,
		AssistantID: "assistant-t", UserID: "user-t", OutboxEventID: "evt-t",
		RequestID: "req-t", OperationType: "write", State: model.MemoryOperationExecuted,
		PolicyVersion: 1, PolicyGeneration: 1, ConsentVersion: 1, MemoryType: "raw",
		RecordVersion: 1, PayloadHash: "payload-hash", ExternalMessageHash: "message-hash",
		PIIAction: "ALLOW", PIIRuleVersion: MemoryPrivacyRuleVersion,
		RagflowAuthPrincipalID: "principal", RagflowAttributionUserID: "attribution",
		RagflowMessageID: strconv.FormatInt(13, 10),
		CreatedAt:        now, UpdatedAt: now,
	}
	if err := svc.Store.CreateMemoryOperation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Store.TombstoneMemoryOperation(
		ctx, tenant.ID, operation.ID, "scope-hash", "payload-hash", "message-hash", now,
	); err != nil {
		t.Fatal(err)
	}
	if err := svc.ComplianceDeleteMemoryMessage(ctx, tenant.ID, mem.ID, 13, "", false); err == nil {
		t.Fatal("compliance delete must refuse while a tombstone is active")
	} else if he, ok := err.(*httperr.Error); !ok || he.Code != 40959 {
		t.Fatalf("active tombstone must map to 40959: %v", err)
	}
}
