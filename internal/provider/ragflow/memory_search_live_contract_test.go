package ragflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveMemorySearchFilterContract performs the Phase 0 P0-4 read-only
// RAGFlow drill. It does not create Memory content and skips unless the live
// deployment already exposes at least one Memory with one retrievable message.
func TestLiveMemorySearchFilterContract(t *testing.T) {
	baseURL := os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL")
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is not set")
	}
	client := NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 15*time.Second, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	memories, total, err := client.ListMemories(ctx, ListMemoriesFilter{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list live memories: %v", err)
	}
	if total == 0 || len(memories) == 0 {
		t.Skip("live Memory search filter drill requires an existing Memory fixture")
	}
	memoryID := ""
	query := ""
	for _, memory := range memories {
		messages, err := client.ListMemoryMessages(ctx, memory.ID)
		if err != nil {
			continue
		}
		for _, message := range messages {
			query = strings.TrimSpace(message.UserInput)
			if query == "" {
				query = strings.TrimSpace(message.Content)
			}
			if query != "" {
				memoryID = memory.ID
				break
			}
		}
		if memoryID != "" {
			break
		}
	}
	if memoryID == "" {
		t.Skip("no existing live Memory fixture has searchable message text")
	}

	// The docs define absent attribution filters as "None"; assert that the
	// unfiltered call still works so a default-side error cannot be mistaken
	// for an empty authorized-filter result.
	defaultRows, err := client.SearchMemoryMessages(ctx, memoryID, MemorySearchParams{
		Query: query, SimilarityThreshold: 0.2, KeywordsSimilarityWeight: 0.7, TopN: 5,
	})
	if err != nil {
		t.Fatalf("default live memory search: %v", err)
	}
	missingRows, err := client.SearchMemoryMessages(ctx, memoryID, MemorySearchParams{
		Query: "rgx-phase0-p0-4", AgentID: "rgx-phase0-missing-agent",
		SessionID: "rgx-phase0-missing-session", UserID: "rgx-phase0-missing-user",
		SimilarityThreshold: 0.2, KeywordsSimilarityWeight: 0.7, TopN: 5,
	})
	if err != nil {
		t.Fatalf("filtered live memory search: %v", err)
	}
	if len(missingRows) != 0 {
		t.Fatalf("live memory search returned %d rows for impossible AND filters; filters are not enforced as AND", len(missingRows))
	}
	t.Logf("live memory search default rows=%d; impossible AND filter rows=%d", len(defaultRows), len(missingRows))

	// Preserve the raw unknown-parameter observation for the Phase 0 report;
	// RAGFlow-X rejects unknown search parameters before forwarding.
	unknownQuery := url.Values{}
	unknownQuery.Set("memory_id", memoryID)
	unknownQuery.Set("query", query)
	unknownQuery.Set("rgx_unknown_contract_param", "1")
	var unknownRows []MemoryMessage
	if err := client.do(ctx, "GET", "/messages/search?"+unknownQuery.Encode(), nil, "", &unknownRows); err != nil {
		t.Fatalf("unknown-parameter live memory search: %v", err)
	}
	raw, _ := json.Marshal(map[string]interface{}{"rows": len(unknownRows)})
	t.Logf("unknown-parameter upstream observation: %s", string(raw))
}

// TestLiveMemorySearchFilterContractWithFixture performs the only sanctioned
// live write drill for P0-4. It is disabled unless RGX_ALLOW_MEMORY_FIXTURE=1,
// creates a uniquely named temporary Memory, waits for RAGFlow's asynchronous
// message task, validates AND/default search behavior, and always deletes the
// Memory in cleanup. The keep flag exists only for incident investigation and
// must not be used in ordinary validation.
func TestLiveMemorySearchFilterContractWithFixture(t *testing.T) {
	baseURL := os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL")
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is not set")
	}
	if os.Getenv("RGX_ALLOW_MEMORY_FIXTURE") != "1" {
		t.Skip("RGX_ALLOW_MEMORY_FIXTURE=1 is required for the temporary Memory live drill")
	}

	client := NewHTTPClient(baseURL, os.Getenv("RGX_RAGFLOW_LIVE_API_KEY"), 15*time.Second, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	chatID := strings.TrimSpace(os.Getenv("RGX_MEMORY_FIXTURE_LLM_ID"))
	embeddingID := strings.TrimSpace(os.Getenv("RGX_MEMORY_FIXTURE_EMBD_ID"))
	if chatID == "" {
		chatID = fixtureModelID(t, client, ctx, "chat")
	}
	if embeddingID == "" {
		embeddingID = fixtureModelID(t, client, ctx, "embedding")
	}
	name := fmt.Sprintf("rgx_memory_search_fixture_%d", time.Now().UnixMilli())
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

	const (
		agentID   = "rgx-live-fixture-agent"
		sessionID = "rgx-live-fixture-session"
		userID    = "rgx:phase0:tenant:user"
	)
	query := "rgx-live-only pineapple fixture 2026"
	if err := client.AddMemoryMessage(ctx, AddMessageRequest{
		MemoryIDs:     []string{memoryID},
		AgentID:       agentID,
		SessionID:     sessionID,
		UserID:        userID,
		UserInput:     query,
		AgentResponse: "The live fixture confirms attribution filters.",
	}); err != nil {
		t.Fatalf("add temporary memory message: %v", err)
	}

	waitForFixtureMessage(t, client, ctx, memoryID, agentID)

	messages, err := client.ListMemoryMessages(ctx, memoryID)
	if err != nil {
		t.Fatalf("list temporary memory messages: %v", err)
	}
	var stored *MemoryMessage
	for index := range messages {
		if messages[index].AgentID == agentID && messages[index].UserID == userID {
			stored = &messages[index]
			break
		}
	}
	if stored == nil {
		t.Fatalf("stored message did not preserve canonical tenant-prefixed attribution user_id %q", userID)
	}
	t.Logf("canonical tenant-prefixed attribution accepted: user_id=%q message_id=%d", stored.UserID, stored.MessageID)

	defaultRows, err := client.SearchMemoryMessages(ctx, memoryID, MemorySearchParams{Query: query})
	if err != nil {
		t.Fatalf("default live memory search: %v", err)
	}
	if len(defaultRows) == 0 {
		t.Fatalf("default live memory search returned no rows; defaults did not include the fixture")
	}

	exactRows, err := client.SearchMemoryMessages(ctx, memoryID, MemorySearchParams{
		Query: query, AgentID: agentID, SessionID: sessionID, UserID: userID, TopN: 5,
	})
	if err != nil {
		t.Fatalf("exact AND live memory search: %v", err)
	}
	if len(exactRows) == 0 {
		t.Fatalf("exact AND live memory search returned no rows")
	}

	for _, tc := range []struct {
		name        string
		agentID     string
		sessionID   string
		userID      string
		wantNonZero bool
	}{
		{name: "wrong agent", agentID: agentID + "-wrong", sessionID: sessionID, userID: userID},
		{name: "wrong session", agentID: agentID, sessionID: sessionID + "-wrong", userID: userID},
		{name: "wrong user", agentID: agentID, sessionID: sessionID, userID: userID + "-wrong"},
	} {
		rows, err := client.SearchMemoryMessages(ctx, memoryID, MemorySearchParams{
			Query: query, AgentID: tc.agentID, SessionID: tc.sessionID, UserID: tc.userID, TopN: 5,
		})
		if err != nil {
			t.Fatalf("%s live memory search: %v", tc.name, err)
		}
		if (len(rows) > 0) != tc.wantNonZero {
			t.Fatalf("%s live memory search returned %d rows; want nonzero=%t", tc.name, len(rows), tc.wantNonZero)
		}
	}

	unknownQuery := url.Values{}
	unknownQuery.Set("memory_id", memoryID)
	unknownQuery.Set("query", query)
	unknownQuery.Set("rgx_unknown_contract_param", "1")
	var unknownRows []MemoryMessage
	if err := client.do(ctx, http.MethodGet, "/messages/search?"+unknownQuery.Encode(), nil, "", &unknownRows); err != nil {
		t.Fatalf("unknown-parameter live memory search: %v", err)
	}
	t.Logf("upstream unknown-parameter observation: rows=%d (RAGFlow-X rejects unknown parameters before forwarding)", len(unknownRows))
}

func fixtureModelID(t *testing.T, client *HTTPClient, ctx context.Context, modelType string) string {
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
		t.Logf("selected %s model %s", modelType, modelID)
		return modelID
	}
	t.Skipf("live Memory fixture drill requires an enabled %s model", modelType)
	return ""
}

func waitForFixtureMessage(t *testing.T, client *HTTPClient, ctx context.Context, memoryID, agentID string) {
	deadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		deadline = time.Now().Add(5 * time.Minute)
	}
	for time.Now().Before(deadline) {
		messages, err := client.ListMemoryMessages(ctx, memoryID)
		if err == nil {
			for _, message := range messages {
				if message.AgentID == agentID {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("temporary Memory message did not become searchable before timeout: %v", ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	t.Fatal("temporary Memory message did not become searchable")
}
