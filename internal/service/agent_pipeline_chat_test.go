package service

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// A data-pipeline canvas has no conversation. Chatting with one used to stream
// the engine's node-level failure (`component "File" invoke: File: file: inputs
// missing doc_id or file[0].name`) into the answer bubble, which reads like the
// assistant said it. Both the streaming and the non-streaming entry points must
// refuse with a translatable 4xx instead (doc/45 defect ㊳).
func TestAgentChatRefusesPipelineCanvas(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := svc.CreateAgent(ctx, tenant.ID, "pipeline", map[string]interface{}{"x": 1}, false, "dataflow_canvas")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := svc.CreateAgent(ctx, tenant.ID, "conversation", map[string]interface{}{"x": 1}, false, "agent_canvas")
	if err != nil {
		t.Fatal(err)
	}

	messages := []ragflow.Message{{Role: "user", Content: "hi"}}

	_, chatErr := svc.AgentChatCompletion(ctx, tenant.ID, pipeline.ID, "user-1", messages, nil, "req-pipeline", "")
	if chatErr == nil {
		t.Fatal("non-stream chat with a pipeline canvas must be refused")
	}
	var httpErr *httperr.Error
	if !errors.As(chatErr, &httpErr) || httpErr.Code != 40162 || httpErr.Status != 400 {
		t.Fatalf("want 400/40162, got %#v", chatErr)
	}

	streamErr := svc.StreamAgentChatCompletion(ctx, tenant.ID, pipeline.ID, "user-1", messages, nil, "req-pipeline-stream", "", io.Discard)
	if streamErr == nil {
		t.Fatal("stream chat with a pipeline canvas must be refused")
	}
	if !errors.As(streamErr, &httpErr) || httpErr.Code != 40162 {
		t.Fatalf("want 40162 on the stream path, got %#v", streamErr)
	}

	// A conversational canvas keeps working: the guard must not become a
	// blanket refusal for every agent.
	if _, err := svc.AgentChatCompletion(ctx, tenant.ID, conversation.ID, "user-1", messages, nil, "req-chat", ""); err != nil {
		t.Fatalf("conversational canvas must still answer: %v", err)
	}
}
