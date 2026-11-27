package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// TestFailChatAppStreamWritesBookkeepingAfterTheClientLeft pins the fix for the
// 2026-11-27 log: six truncated streams each produced three `context canceled`
// WARN lines (quota release, knowledge-ops event, answer snapshot) and left no
// record of the failure at all. The inbound context is dead by definition when a
// stream ends badly, so the bookkeeping must not inherit it.
func TestFailChatAppStreamWritesBookkeepingAfterTheClientLeft(t *testing.T) {
	storeCtx := context.Background()
	svc, _, key, chatID := gatewayStreamChatApp(t)

	// Exactly the state of a request whose client closed the tab.
	deadCtx, cancel := context.WithCancel(storeCtx)
	cancel()

	capture := &knowledgeOpsStreamCapture{}
	startedAt := time.Now().Add(-2 * time.Second)
	err := svc.failChatAppStream(deadCtx, key, chatID, "", "req-stream-abandoned", "hi", capture, startedAt,
		errors.New("context canceled"), nil)
	if err == nil {
		t.Fatal("a failed stream must still surface an error")
	}

	snapshot, snapErr := svc.Store.GetAnswerSnapshotByRequest(storeCtx, key.TenantID, "req-stream-abandoned")
	if snapErr != nil {
		t.Fatalf("failed delivery must be recorded even though the client left: %v", snapErr)
	}
	if snapshot.AnswerStatus != model.AnswerStatusSystemFailed {
		t.Fatalf("an abandoned stream is a failed delivery, got %s", snapshot.AnswerStatus)
	}
	if !strings.Contains(snapshot.AdminReason, "context canceled") {
		t.Fatalf("the transport cause must reach the operator record: %q", snapshot.AdminReason)
	}
}
