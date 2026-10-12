package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveMemoryDispatchIdempotencyObservationWithFixture performs the Phase 0
// P0-5 duplicate POST observation. It is disabled unless both fixture flags
// are set, creates a uniquely named temporary Memory, sends the same payload
// twice, and always deletes the Memory in cleanup.
func TestLiveMemoryDispatchIdempotencyObservation(t *testing.T) {
	baseURL := os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL")
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is not set")
	}
	if os.Getenv("RGX_ALLOW_MEMORY_FIXTURE") != "1" ||
		os.Getenv("RGX_ALLOW_MEMORY_DISPATCH_FIXTURE") != "1" {
		t.Skip("RGX_ALLOW_MEMORY_FIXTURE=1 and RGX_ALLOW_MEMORY_DISPATCH_FIXTURE=1 are required")
	}

	client := NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 15*time.Second, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	chatID := fixtureModelID(t, client, ctx, "chat")
	embeddingID := fixtureModelID(t, client, ctx, "embedding")
	name := fmt.Sprintf("rgx_memory_dispatch_idempotency_%d", time.Now().UnixMilli())
	memoryID, err := client.CreateMemory(ctx, CreateMemoryRequest{
		Name:       name,
		MemoryType: []string{"raw"},
		EmbdID:     embeddingID,
		LLMID:      chatID,
	})
	if err != nil {
		t.Fatalf("create temporary memory (llm=%s embedding=%s): %v", chatID, embeddingID, err)
	}
	t.Cleanup(func() {
		if os.Getenv("RGX_KEEP_MEMORY_FIXTURE") == "1" {
			t.Logf("RGX_KEEP_MEMORY_FIXTURE=1; retaining memory %s (%s)", memoryID, name)
			return
		}
		if err := client.DeleteMemory(context.Background(), memoryID); err != nil {
			t.Errorf("delete temporary memory %s: %v", memoryID, err)
		}
	})
	t.Logf("created temporary memory %s (%s)", memoryID, name)

	const agentID = "rgx-dispatch-idempotency-agent"
	const sessionID = "rgx-dispatch-idempotency-session"
	const userID = "rgx:phase0:tenant:user"
	query := fmt.Sprintf("rgx dispatch idempotency fixture %d", time.Now().UnixMilli())
	payload := AddMessageRequest{
		MemoryIDs:     []string{memoryID},
		AgentID:       agentID,
		SessionID:     sessionID,
		UserID:        userID,
		UserInput:     query,
		AgentResponse: "Phase 0 duplicate dispatch observation.",
	}

	firstBody, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var firstEnvelope struct {
		Code    int             `json:"code"`
		Message json.RawMessage `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := client.do(ctx, "POST", "/messages", bytes.NewReader(firstBody), "application/json", &firstEnvelope); err != nil {
		t.Fatalf("first live dispatch: %v", err)
	}
	firstResponse, _ := json.Marshal(map[string]json.RawMessage{
		"code": mustMarshalInt(t, firstEnvelope.Code), "message": firstEnvelope.Message, "data": firstEnvelope.Data,
	})
	t.Logf("first dispatch response (no durable task/message id contract): %s", string(firstResponse))

	if err := client.AddMemoryMessage(ctx, payload); err != nil {
		t.Fatalf("duplicate live dispatch: %v", err)
	}

	messages, count := waitForDispatchMessages(t, client, ctx, memoryID, agentID)
	if count == 0 {
		t.Fatalf("live dispatch produced no retrievable message for memory %s", memoryID)
	}
	for _, message := range messages {
		if message.UserID != userID {
			t.Fatalf("live dispatch changed attribution user_id: want %q got %q", userID, message.UserID)
		}
		if message.SessionID != sessionID {
			t.Fatalf("live dispatch changed session_id: want %q got %q", sessionID, message.SessionID)
		}
	}
	switch count {
	case 1:
		t.Logf("duplicate exact POST produced 1 message; observed deduplication but no explicit business idempotency key")
	case 2:
		t.Logf("duplicate exact POST produced 2 messages; RAGFlow has no native business idempotency contract")
	default:
		t.Fatalf("duplicate dispatch must not create more than two messages; got %d", count)
	}
	if strings.TrimSpace(query) == "" {
		t.Fatal("dispatch fixture query must be non-empty")
	}
}

func mustMarshalInt(t *testing.T, value int) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestLiveMemoryCompensationContractWithFixture verifies the exact fields used
// by post-admission compensation: filtered metadata discovery plus one
// content read. It never logs message content and always deletes the memory.
func TestLiveMemoryCompensationContractWithFixture(t *testing.T) {
	baseURL := os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL")
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is not set")
	}
	if os.Getenv("RGX_ALLOW_MEMORY_FIXTURE") != "1" ||
		os.Getenv("RGX_ALLOW_MEMORY_DISPATCH_FIXTURE") != "1" {
		t.Skip("RGX_ALLOW_MEMORY_FIXTURE=1 and RGX_ALLOW_MEMORY_DISPATCH_FIXTURE=1 are required")
	}

	client := NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 15*time.Second, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	chatID := fixtureModelID(t, client, ctx, "chat")
	embeddingID := fixtureModelID(t, client, ctx, "embedding")
	name := fmt.Sprintf("rgx_memory_compensation_contract_%d", time.Now().UnixMilli())
	memoryID, err := client.CreateMemory(ctx, CreateMemoryRequest{
		Name: name, MemoryType: []string{"raw"}, EmbdID: embeddingID, LLMID: chatID,
	})
	if err != nil {
		t.Fatalf("create temporary memory: %v", err)
	}
	t.Cleanup(func() {
		if err := client.DeleteMemory(context.Background(), memoryID); err != nil {
			t.Errorf("delete temporary memory %s: %v", memoryID, err)
		}
	})

	const agentID = "rgx-compensation-contract-agent"
	const sessionID = "rgx-compensation-contract-session"
	const userID = "rgx:phase0:tenant:user"
	query := fmt.Sprintf("rgx compensation contract fixture %d", time.Now().UnixMilli())
	if err := client.AddMemoryMessage(ctx, AddMessageRequest{
		MemoryIDs: []string{memoryID}, AgentID: agentID, SessionID: sessionID, UserID: userID,
		UserInput: query, AgentResponse: "Live compensation contract response.",
	}); err != nil {
		t.Fatalf("live memory dispatch: %v", err)
	}
	messages, count := waitForDispatchMessages(t, client, ctx, memoryID, agentID)
	if count != 1 {
		t.Fatalf("compensation contract expects one message, got %d", count)
	}
	filtered, err := client.ListMemoryMessagesFiltered(ctx, memoryID, MemoryMessageListFilter{
		AgentID: agentID, SessionID: sessionID, UserID: userID, Page: 1, PageSize: 100,
	})
	if err != nil || len(filtered) != 1 {
		t.Fatalf("filtered live metadata discovery: messages=%d err=%v", len(filtered), err)
	}
	if filtered[0].MessageID != messages[0].MessageID || filtered[0].MemoryID != memoryID ||
		filtered[0].UserID != userID {
		t.Fatalf("filtered live metadata mismatch: %+v", filtered[0])
	}
	raw, err := client.GetMemoryMessageContent(ctx, memoryID, filtered[0].MessageID)
	if err != nil {
		t.Fatalf("live content probe: %v", err)
	}
	var content struct {
		UserInput     string `json:"user_input"`
		AgentResponse string `json:"agent_response"`
		Content       string `json:"content"`
	}
	if err := json.Unmarshal(raw, &content); err != nil {
		t.Fatalf("decode live content probe: %v", err)
	}
	if content.UserInput == "" && content.AgentResponse == "" && content.Content == "" {
		t.Fatal("live content probe returned no compensation fingerprint source")
	}
	t.Logf("live compensation content probe passed for message %s:%d", memoryID, filtered[0].MessageID)
}

func waitForDispatchMessages(
	t *testing.T, client *HTTPClient, ctx context.Context, memoryID, agentID string,
) ([]MemoryMessage, int) {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Minute)
	}
	var stableMessages []MemoryMessage
	stableCount := -1
	stableSince := time.Time{}
	for time.Now().Before(deadline) {
		messages, err := client.ListMemoryMessages(ctx, memoryID)
		if err == nil {
			matching := make([]MemoryMessage, 0, len(messages))
			for _, message := range messages {
				if message.AgentID == agentID {
					matching = append(matching, message)
				}
			}
			count := len(matching)
			if count >= 2 {
				return matching, count
			}
			if count == stableCount {
				if stableSince.IsZero() {
					stableSince = time.Now()
				}
				if count > 0 && time.Since(stableSince) >= 20*time.Second {
					return matching, count
				}
			} else {
				stableCount = count
				stableSince = time.Now()
				if count > 0 {
					stableMessages = matching
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	return stableMessages, len(stableMessages)
}
