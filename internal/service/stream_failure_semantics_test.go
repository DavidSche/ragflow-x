package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// doc/227 C3.3 ("双拼"): retrieval already produced content and the upstream
// then reports a failure in-band on the committed 200 stream. Before this
// contract the chat/search paths recorded the answer as ANSWERED and metered
// tokens, so a failed stream looked like a successful one in the delivery
// ledger. These tests pin the corrected semantics.

func gatewayStreamChatApp(t *testing.T) (*Service, *ragflow.Mock, *model.APIKey, string) {
	t.Helper()
	ctx := context.Background()
	svc, m := chatAppSvc(t)
	chat, err := m.CreateChat(ctx, ragflow.CreateChatRequest{Name: "kb-stream-fault", DatasetIDs: []string{"d1"}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := svc.Store.UpsertChatShadow(ctx, &model.ChatShadow{
		ID: chat.ID, TenantID: "t1", Name: chat.Name, Status: model.TenantStatusActive, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return svc, m, &model.APIKey{ID: "key-stream-fault", TenantID: "t1", UserID: "u1"}, chat.ID
}

func TestStreamChatAppInBandErrorFrameIsAFailedDelivery(t *testing.T) {
	ctx := context.Background()
	svc, m, key, chatID := gatewayStreamChatApp(t)
	m.ConfigureStreamFault(ragflow.StreamFault{
		ContentBeforeFault: "partial answer",
		ErrorFrame:         true,
		ErrorCode:          500,
		ErrorMessage:       "retrieval timed out",
	})

	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	var out bytes.Buffer
	_, _, err := svc.StreamChatAppCompletion(ctx, key, chatID, "", raw, &out, "req-stream-inband")
	if err == nil {
		t.Fatal("an in-band upstream error frame must fail the request")
	}
	if !IsReportedStreamFailure(err) {
		t.Fatalf("the upstream already reported the failure: %v", err)
	}
	var he *httperr.Error
	if !errors.As(err, &he) || he.Status != 502 || he.Code != 50231 {
		t.Fatalf("unexpected error contract: %#v", err)
	}

	body := out.String()
	if !strings.Contains(body, "partial answer") {
		t.Fatalf("partial content must still reach the client: %q", body)
	}
	if got := strings.Count(body, `"error"`); got != 1 {
		t.Fatalf("client must see exactly one terminal error signal, got %d: %q", got, body)
	}
	if strings.Contains(body, "mock stream") {
		t.Fatalf("the happy-path frame must not be emitted after a fault: %q", body)
	}
	if m.StreamFaultCalls() != 1 {
		t.Fatalf("fault must be applied to exactly one call, got %d", m.StreamFaultCalls())
	}

	// Nothing is billed for a stream that delivered no answer.
	usage, err := svc.Store.SummarizeUsage(ctx, "t1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 0 {
		t.Fatalf("a failed stream must not meter usage: %+v", usage)
	}
	if _, total, err := svc.Store.ListCostMetrics(ctx, "t1", false, 1, 20, repository.CostMetricFilter{}); err != nil || total != 0 {
		t.Fatalf("a failed stream must not write cost detail: total=%d err=%v", total, err)
	}

	snapshot, err := svc.Store.GetAnswerSnapshotByRequest(ctx, "t1", "req-stream-inband")
	if err != nil {
		t.Fatalf("failed delivery snapshot missing: %v", err)
	}
	if snapshot.AnswerStatus != model.AnswerStatusSystemFailed {
		t.Fatalf("in-band upstream failure must be SYSTEM_FAILED, got %s", snapshot.AnswerStatus)
	}
	if !strings.Contains(snapshot.AdminReason, "retrieval timed out") {
		t.Fatalf("the provider cause must be preserved for operators: %q", snapshot.AdminReason)
	}
}

func TestStreamChatAppTransportFaultAddsGatewayErrorFrame(t *testing.T) {
	ctx := context.Background()
	svc, m, key, chatID := gatewayStreamChatApp(t)
	m.ConfigureStreamFault(ragflow.StreamFault{
		ContentBeforeFault: "partial answer",
		TransportError:     "connection reset by peer",
	})

	raw := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)
	var out bytes.Buffer
	_, _, err := svc.StreamChatAppCompletion(ctx, key, chatID, "", raw, &out, "req-stream-transport")
	if err == nil {
		t.Fatal("a dropped upstream connection must fail the request")
	}
	if IsReportedStreamFailure(err) {
		t.Fatal("a transport failure was not reported in-band; the gateway must write the terminal frame")
	}
	var he *httperr.Error
	if !errors.As(err, &he) || he.Code != 50231 {
		t.Fatalf("unexpected error contract: %#v", err)
	}
	if body := out.String(); !strings.Contains(body, "partial answer") || strings.Contains(body, `"error"`) {
		t.Fatalf("only the partial content is client-visible at this layer: %q", body)
	}

	usage, err := svc.Store.SummarizeUsage(ctx, "t1", "")
	if err != nil || len(usage) != 0 {
		t.Fatalf("failed stream metered: %+v %v", usage, err)
	}
}

func TestStreamSearchAppInBandErrorFrameIsAFailedDelivery(t *testing.T) {
	ctx := context.Background()
	svc, tenant, user := newMemoryPolicySvc(t)
	mock := svc.RAGFlow.(*ragflow.Mock)
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "stream-fault-kb")
	if err != nil {
		t.Fatal(err)
	}
	searchApp, err := svc.CreateSearchApp(ctx, tenant.ID, "stream-fault-search", &ragflow.SearchConfig{
		KbIDs: []string{dataset.RAGFlowDatasetID},
	})
	if err != nil {
		t.Fatal(err)
	}
	mock.ConfigureStreamFault(ragflow.StreamFault{
		ContentBeforeFault: "retrieved chunk",
		ErrorFrame:         true,
		ErrorCode:          500,
		ErrorMessage:       "generation failed",
	})

	var out bytes.Buffer
	err = svc.StreamSearchAppCompletion(ctx, tenant.ID, user.ID, searchApp.ID, "question", "req-search-stream-inband", false, &out)
	if err == nil {
		t.Fatal("an in-band upstream error frame must fail the search stream")
	}
	if !IsReportedStreamFailure(err) {
		t.Fatalf("search in-band failure must be marked as already reported: %v", err)
	}
	var he *httperr.Error
	if !errors.As(err, &he) || he.Code != 50265 {
		t.Fatalf("unexpected search error contract: %#v", err)
	}
	body := out.String()
	if strings.Count(body, `"error"`) != 1 {
		t.Fatalf("client must see exactly one terminal error signal: %q", body)
	}
	snapshot, err := svc.Store.GetAnswerSnapshotByRequest(ctx, tenant.ID, "req-search-stream-inband")
	if err != nil {
		t.Fatalf("failed search delivery snapshot missing: %v", err)
	}
	if snapshot.AnswerStatus != model.AnswerStatusSystemFailed {
		t.Fatalf("search in-band failure must be SYSTEM_FAILED, got %s", snapshot.AnswerStatus)
	}
}

func TestStreamAgentInBandErrorFrameFailsTheRequest(t *testing.T) {
	ctx := context.Background()
	svc, tenant, user := newMemoryPolicySvc(t)
	mock := svc.RAGFlow.(*ragflow.Mock)
	agent, err := svc.CreateAgent(ctx, tenant.ID, "stream-fault-agent", map[string]interface{}{"x": 1}, true, "agent_canvas")
	if err != nil {
		t.Fatal(err)
	}
	mock.ConfigureStreamFault(ragflow.StreamFault{
		ErrorFrame:   true,
		ErrorCode:    500,
		ErrorMessage: "agent tool failed",
	})

	var out bytes.Buffer
	messages := []ragflow.Message{{Role: "user", Content: "hello"}}
	err = svc.StreamAgentChatCompletion(ctx, tenant.ID, agent.ID, user.ID, messages, nil, "req-agent-stream-inband", "agent-session-1", &out)
	if err == nil {
		t.Fatal("an in-band upstream error frame must fail the agent stream")
	}
	if !IsReportedStreamFailure(err) {
		t.Fatalf("agent in-band failure must be marked as already reported: %v", err)
	}
	var he *httperr.Error
	if !errors.As(err, &he) || he.Code != 50289 {
		t.Fatalf("unexpected agent error contract: %#v", err)
	}
	snapshot, err := svc.Store.GetAnswerSnapshotByRequest(ctx, tenant.ID, "req-agent-stream-inband")
	if err != nil {
		t.Fatalf("failed agent delivery snapshot missing: %v", err)
	}
	if snapshot.AnswerStatus != model.AnswerStatusSystemFailed {
		t.Fatalf("agent in-band failure must be SYSTEM_FAILED, got %s", snapshot.AnswerStatus)
	}
}

func TestGatewayStreamErrorFrameDoesNotDuplicateReportedFailure(t *testing.T) {
	// The handler-side rule is unit tested in internal/handler; this guards the
	// service wrapper contract it depends on.
	if IsReportedStreamFailure(nil) {
		t.Fatal("nil must not be classified as a reported stream failure")
	}
	if IsReportedStreamFailure(httperr.New(502, 50231, "boom")) {
		t.Fatal("a plain gateway error must still produce its own terminal frame")
	}
	if !IsReportedStreamFailure(reportedStreamFailure(httperr.New(502, 50231, "boom"))) {
		t.Fatal("wrapped failure must be recognised through errors.As")
	}
}
