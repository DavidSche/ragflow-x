package service

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// liveMemoryTaskInput fetches the authoritative outbound payload RAGFlow
// recorded for a memory message. The Governed message list API is
// metadata-only, so the closed-loop drill verifies the redaction placeholder
// (and, for exemptions, the raw PII) directly against memory_task.input.
// RGX_PRIVACY_LIVE_MYSQL_DSN (e.g. root:pass4mysql@tcp(192.168.4.151:3306)/rag_flow)
// is required; without it the outbound payload assertions are skipped.
func liveMemoryTaskInput(t *testing.T, memoryID string, sourceID int64) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("RGX_PRIVACY_LIVE_MYSQL_DSN"))
	if dsn == "" {
		t.Skip("RGX_PRIVACY_LIVE_MYSQL_DSN is not set; outbound payload assertions skipped")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open live mysql: %v", err)
	}
	defer func() { _ = db.Close() }()
	var input string
	deadline := time.Now().Add(30 * time.Second)
	for {
		err = db.QueryRow(
			"SELECT input FROM memory_task WHERE memory_id = ? AND source_id = ?",
			memoryID, sourceID,
		).Scan(&input)
		if err == nil {
			return input
		}
		if err != sql.ErrNoRows {
			t.Fatalf("query live memory_task: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("live memory_task row for source %d never appeared", sourceID)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// TestLiveMemoryPrivacyClosedLoopE2E drills the plan-137 privacy closed loop
// against the real RAGFlow deployment (Milestone 199 closing drill for the
// Milestone 198 recorded gap):
//  1. an api_key write is BLOCKed before any provider call;
//  2. a phone number write is REDACTed and the outbound payload RAGFlow
//     recorded carries [REDACTED:phone_cn#1] instead of raw PII;
//  3. a tenant exemption for the exact phone number flips the action to
//     ALLOW and the outbound payload keeps the raw number; deleting the
//     exemption restores enforcement;
//  4. the admin compliance delete forgets the external message and stamps
//     forget_at on the governing operation.
func TestLiveMemoryPrivacyClosedLoopE2E(t *testing.T) {
	svc, client, target, policy, memoryID, _ := newLiveMemoryE2EFixture(t, "privacy-closed-loop")
	ctx := context.Background()
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("load admin: user=%+v err=%v", admin, err)
	}
	phone := "13812345678"
	session := "privacy-loop-session"

	dispatch := func(requestID, idempotencyKey, sessionID, userInput string) *model.MemoryOperation {
		t.Helper()
		operation, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
			PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
			RequestID: requestID, IdempotencyKey: idempotencyKey,
			UserID: target.ID, AssistantID: policy.AssistantID, SessionID: sessionID,
			UserInput: userInput, AgentResponse: "privacy drill answer",
		})
		if err != nil {
			t.Fatalf("dispatch %s: %v", requestID, err)
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

	externalMessages := func(sessionID string) []ragflow.MemoryMessage {
		t.Helper()
		messages, err := client.Client.ListMemoryMessagesFiltered(ctx, memoryID, ragflow.MemoryMessageListFilter{
			AgentID: policy.AssistantID, SessionID: sessionID,
			Page: 1, PageSize: 100,
		})
		if err != nil {
			t.Fatalf("list live privacy messages: %v", err)
		}
		return messages
	}

	// Step 1: high-risk api_key content must be blocked before dispatch.
	blockSession := "privacy-block-session"
	addCallsBefore := client.addCalls
	if _, err := svc.CreateMemoryDispatchOperation(ctx, policy.TenantID, target.ID, MemoryDispatchOperationInput{
		PolicyID: policy.ID, MemoryID: memoryID, OperationType: "write",
		RequestID: "request-privacy-block", IdempotencyKey: "idempotency-privacy-block",
		UserID: target.ID, AssistantID: policy.AssistantID, SessionID: blockSession,
		UserInput: "my key is sk-abcdefghij123456789", AgentResponse: "ok",
	}); err == nil {
		t.Fatal("api_key write must be blocked")
	} else if he, ok := err.(*httperr.Error); !ok || he.Code != 42220 {
		t.Fatalf("api_key block must map to 42220: %v", err)
	}
	if client.addCalls != addCallsBefore {
		t.Fatalf("blocked writes must not reach the provider: before=%d after=%d", addCallsBefore, client.addCalls)
	}
	if messages := externalMessages(blockSession); len(messages) != 0 {
		t.Fatalf("blocked session must not create external messages: %+v", messages)
	}

	// Step 2: phone number content is REDACTed end to end.
	operation := dispatch(
		"request-privacy-redact", "idempotency-privacy-redact", session,
		"call me at "+phone+" about the invoice",
	)
	if operation.State != model.MemoryOperationExecuted ||
		operation.PIIAction != MemoryPrivacyActionRedact || operation.RagflowMessageID == "" {
		t.Fatalf("live redact dispatch mismatch: %+v", operation)
	}
	externalID, err := strconv.ParseInt(operation.RagflowMessageID, 10, 64)
	if err != nil || externalID <= 0 {
		t.Fatalf("invalid live message id %q: %v", operation.RagflowMessageID, err)
	}
	redactMessageFound := false
	for _, message := range externalMessages(session) {
		if message.MessageID == externalID {
			redactMessageFound = true
			break
		}
	}
	if !redactMessageFound {
		t.Fatalf("redacted external message %d not found: %+v", externalID, externalMessages(session))
	}
	redactedTaskInput := liveMemoryTaskInput(t, memoryID, externalID)
	if strings.Contains(redactedTaskInput, phone) {
		t.Fatalf("raw phone number must not be recorded by RAGFlow: %q", redactedTaskInput)
	}
	if !strings.Contains(redactedTaskInput, "[REDACTED:phone_cn#1]") {
		t.Fatalf("redaction placeholder must be recorded by RAGFlow: %q", redactedTaskInput)
	}

	// Step 3: a tenant exemption for the exact phone flips the action to
	// ALLOW and the raw number reaches RAGFlow untouched.
	exemption, err := svc.CreateMemoryPrivacyExemption(ctx, policy.TenantID, admin.ID, MemoryPrivacyExemptionInput{
		Label: "phone_cn", Pattern: phone,
	})
	if err != nil {
		t.Fatalf("create live exemption: %v", err)
	}
	allowOperation := dispatch(
		"request-privacy-allow", "idempotency-privacy-allow", session,
		"call me at "+phone+" about the receipt",
	)
	if allowOperation.State != model.MemoryOperationExecuted ||
		allowOperation.PIIAction != MemoryPrivacyActionAllow {
		t.Fatalf("live exemption dispatch mismatch: %+v", allowOperation)
	}
	allowExternalID, err := strconv.ParseInt(allowOperation.RagflowMessageID, 10, 64)
	if err != nil || allowExternalID <= 0 {
		t.Fatalf("invalid live exemption message id %q: %v", allowOperation.RagflowMessageID, err)
	}
	allowMessageFound := false
	for _, message := range externalMessages(session) {
		if message.MessageID == allowExternalID {
			allowMessageFound = true
			break
		}
	}
	if !allowMessageFound {
		t.Fatalf("exempted external message %d not found", allowExternalID)
	}
	allowTaskInput := liveMemoryTaskInput(t, memoryID, allowExternalID)
	if !strings.Contains(allowTaskInput, phone) {
		t.Fatalf("exempted phone number must reach RAGFlow verbatim: %q", allowTaskInput)
	}
	if err := svc.DeleteMemoryPrivacyExemption(ctx, policy.TenantID, admin.ID, exemption.ID); err != nil {
		t.Fatalf("delete live exemption: %v", err)
	}

	// Step 4: after the exemption is gone, the same number is redacted again.
	postDeleteOperation := dispatch(
		"request-privacy-post-delete", "idempotency-privacy-post-delete", session,
		"call me at "+phone+" about the refund",
	)
	if postDeleteOperation.State != model.MemoryOperationExecuted ||
		postDeleteOperation.PIIAction != MemoryPrivacyActionRedact {
		t.Fatalf("post-exemption dispatch must restore redaction: %+v", postDeleteOperation)
	}

	// Step 5: the admin compliance delete forgets the redacted external
	// message and stamps forget_at on the governing operation.
	if err := svc.ComplianceDeleteMemoryMessage(ctx, policy.TenantID, memoryID, externalID, "privacy live drill", false); err != nil {
		t.Fatalf("live compliance delete: %v", err)
	}
	// The session still holds other active messages (allow/post-delete), so
	// poll the specific message until its forget_at is recorded.
	deadline := time.Now().Add(30 * time.Second)
	for {
		forgotten := true
		for _, message := range externalMessages(session) {
			if message.MessageID == externalID && memoryMessageActive(message) {
				forgotten = false
			}
		}
		if forgotten {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("live external message %d remained active after compliance delete", externalID)
		}
		time.Sleep(500 * time.Millisecond)
	}
	stored, err := svc.Store.GetMemoryOperation(ctx, policy.TenantID, operation.ID)
	if err != nil || stored == nil {
		t.Fatal(err)
	}
	if stored.ForgetAt == nil {
		t.Fatalf("live compliance delete must stamp forget_at: %+v", stored)
	}
}
