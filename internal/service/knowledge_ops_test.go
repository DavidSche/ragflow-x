package service

import (
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestKnowledgeOpsQuestionAndStatusHelpers(t *testing.T) {
	question := lastUserQuestion([]ragflow.Message{
		{Role: "system", Content: "ignored"},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "  报销  流程  "},
	})
	if question != "  报销  流程  " {
		t.Fatalf("unexpected user question: %q", question)
	}
	if knowledgeStatus("") != model.KnowledgeOpsNoAnswer {
		t.Fatal("empty answer must be classified as no_answer")
	}
	if knowledgeStatus("有依据的答案") != model.KnowledgeOpsCompleted {
		t.Fatal("non-empty answer must be classified as completed")
	}
	if normalized := normalizeKnowledgeQuestion("  报销\n流程  "); len(normalized) != 64 {
		t.Fatalf("expected sha256 hex, got %q", normalized)
	}
	long := strings.Repeat("长", 1200)
	if got := truncateKnowledgeText(long, 1024); len([]rune(got)) != 1027 {
		t.Fatalf("unexpected truncation rune count: %d", len([]rune(got)))
	}
}

func TestKnowledgeEventCapturesLatency(t *testing.T) {
	event := knowledgeEvent("t1", "u1", "chat", "chat-1", "session-1", "request-1", "问题", "答案", 2, 10, 20, 350)
	if event.DurationMs != 350 {
		t.Fatalf("unexpected duration: %+v", event)
	}
	if event.RequestID != "request-1" || event.CitationsCount != 2 || event.Status != model.KnowledgeOpsCompleted {
		t.Fatalf("unexpected event core fields: %+v", event)
	}
}

func TestKnowledgeOpsStreamCaptureAccumulatesSSE(t *testing.T) {
	var output strings.Builder
	capture := &knowledgeOpsStreamCapture{w: &output}
	frames := []string{
		`data: {"data":{"content":"答案","reference":[{"chunk":1}]}}` + "\n\n",
		`data: {"usage":{"prompt_tokens":12,"completion_tokens":8}}` + "\n\n",
		"data: [DONE]\n\n",
	}
	for _, frame := range frames {
		if _, err := capture.Write([]byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	if output.String() != strings.Join(frames, "") {
		t.Fatalf("SSE passthrough changed: %q", output.String())
	}
	if capture.answer.String() != "答案" {
		t.Fatalf("unexpected captured answer: %q", capture.answer.String())
	}
	if capture.citations != 1 || capture.tokensIn != 12 || capture.tokensOut != 8 {
		t.Fatalf("unexpected captured metadata: %+v", capture)
	}
}

func TestKnowledgeOpsStreamCaptureSuppressesThinkingSSE(t *testing.T) {
	var output strings.Builder
	capture := &knowledgeOpsStreamCapture{w: &output}
	frames := []string{
		`data: {"data":{"start_to_think":true,"answer":""}}` + "\n",
		`data: {"data":{"answer":"private reasoning"}}` + "\n",
		`data: {"data":{"end_to_think":true,"answer":""}}` + "\n",
		`data: {"data":{"answer":"最终答案","reference":[{"chunk":1}]}}` + "\n",
		"data: [DONE]\n",
	}
	for _, frame := range frames {
		if _, err := capture.Write([]byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	if err := capture.Flush(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "private reasoning") {
		t.Fatalf("stream retained thinking: %q", output.String())
	}
	if !strings.Contains(output.String(), `data: {"data":{"answer":"最终答案","reference":[{"chunk":1}]}}`+"\n") {
		t.Fatalf("visible stream answer changed: %q", output.String())
	}
	if capture.answer.String() != "最终答案" {
		t.Fatalf("unexpected captured answer: %q", capture.answer.String())
	}
	if capture.citations != 1 {
		t.Fatalf("unexpected citations: %+v", capture)
	}
}

func TestKnowledgeOpsStreamCaptureSuppressesOpenAIThinkingSSE(t *testing.T) {
	var output strings.Builder
	capture := &knowledgeOpsStreamCapture{w: &output}
	frames := []string{
		`data: {"start_to_think":true}` + "\n",
		`data: {"choices":[{"delta":{"content":"private reasoning"}}]}` + "\n",
		`data: {"end_to_think":true}` + "\n",
		`data: {"choices":[{"delta":{"content":"最终答案"}}]}` + "\n",
	}
	for _, frame := range frames {
		if _, err := capture.Write([]byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	if err := capture.Flush(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "private reasoning") {
		t.Fatalf("stream retained thinking: %q", output.String())
	}
	if !strings.Contains(output.String(), `data: {"choices":[{"delta":{"content":"最终答案"}}]}`+"\n") {
		t.Fatalf("visible stream answer changed: %q", output.String())
	}
	if capture.answer.String() != "最终答案" {
		t.Fatalf("unexpected captured answer: %q", capture.answer.String())
	}
}

func TestKnowledgeOpsStreamCaptureFlushDropsIncompleteThinking(t *testing.T) {
	var output strings.Builder
	capture := &knowledgeOpsStreamCapture{w: &output}
	if _, err := capture.Write([]byte(`data: {"data":{"start_to_think":true,"answer":"private`)); err != nil {
		t.Fatal(err)
	}
	if err := capture.Flush(); err != nil {
		t.Fatal(err)
	}
	if output.String() != "" || capture.answer.String() != "" {
		t.Fatalf("incomplete thinking leaked: output=%q answer=%q", output.String(), capture.answer.String())
	}
}
