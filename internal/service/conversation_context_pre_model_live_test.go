package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

type liveInjectionObservation struct {
	mu sync.Mutex

	lastMemorySearch  ragflow.MemorySearchParams
	lastChatBody      string
	lastAgentBody     string
	lastChatResponse  string
	lastAgentResponse string
}

func (o *liveInjectionObservation) middleware() ragflow.RoundTripperMiddleware {
	return func(next http.RoundTripper) http.RoundTripper {
		return &liveInjectionRoundTripper{next: next, observation: o}
	}
}

func (o *liveInjectionObservation) memorySearch() ragflow.MemorySearchParams {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastMemorySearch
}

func (o *liveInjectionObservation) chatBody() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastChatBody
}

func (o *liveInjectionObservation) agentBody() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastAgentBody
}

func (o *liveInjectionObservation) chatResponse() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastChatResponse
}

func (o *liveInjectionObservation) agentResponse() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastAgentResponse
}

func (o *liveInjectionObservation) setMemorySearch(params ragflow.MemorySearchParams) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastMemorySearch = params
}

func (o *liveInjectionObservation) setChatBody(body string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastChatBody = body
}

func (o *liveInjectionObservation) setAgentBody(body string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastAgentBody = body
}

func (o *liveInjectionObservation) setChatResponse(body string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastChatResponse = body
}

func (o *liveInjectionObservation) setAgentResponse(body string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lastAgentResponse = body
}

type liveInjectionRoundTripper struct {
	next        http.RoundTripper
	observation *liveInjectionObservation
}

func (t *liveInjectionRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request != nil && request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/messages/search") {
		query := request.URL.Query()
		t.observation.setMemorySearch(ragflow.MemorySearchParams{
			Query:     query.Get("query"),
			AgentID:   query.Get("agent_id"),
			SessionID: query.Get("session_id"),
			UserID:    query.Get("user_id"),
		})
	}
	if request != nil && request.Method == http.MethodPost {
		switch {
		case strings.HasSuffix(request.URL.Path, "/agents/chat/completions"):
			body, _ := io.ReadAll(request.Body)
			_ = request.Body.Close()
			request.Body = io.NopCloser(bytes.NewReader(body))
			t.observation.setAgentBody(string(body))
		case strings.HasSuffix(request.URL.Path, "/chat/completions"):
			body, _ := io.ReadAll(request.Body)
			_ = request.Body.Close()
			request.Body = io.NopCloser(bytes.NewReader(body))
			t.observation.setChatBody(string(body))
		}
	}
	response, err := t.next.RoundTrip(request)
	if response == nil {
		return response, err
	}
	switch {
	case strings.HasSuffix(request.URL.Path, "/agents/chat/completions"):
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(body))
		t.observation.setAgentResponse(string(body))
	case strings.HasSuffix(request.URL.Path, "/chat/completions"):
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(body))
		t.observation.setChatResponse(string(body))
	}
	return response, err
}

type liveCompletionPayload struct {
	Messages []ragflow.Message `json:"messages"`
}

func TestLivePreModelMemoryInjectionChatAndAgent(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("RGX_RAGFLOW_LIVE_BASE_URL"))
	if baseURL == "" {
		t.Skip("RGX_RAGFLOW_LIVE_BASE_URL is not set")
	}
	if os.Getenv("RGX_ALLOW_MEMORY_FIXTURE") != "1" ||
		os.Getenv("RGX_ALLOW_MEMORY_DISPATCH_FIXTURE") != "1" ||
		os.Getenv("RGX_ALLOW_MEMORY_E2E_FIXTURE") != "1" {
		t.Skip("RGX_ALLOW_MEMORY_FIXTURE=1, RGX_ALLOW_MEMORY_DISPATCH_FIXTURE=1 and RGX_ALLOW_MEMORY_E2E_FIXTURE=1 are required")
	}
	ctx := context.Background()
	observation := &liveInjectionObservation{}
	svc, client, target, policy, memoryID, _ := newLiveMemoryE2EFixture(t, "injection", observation.middleware())
	attribution := "rgx:" + policy.TenantID + ":" + target.ID
	query := fmt.Sprintf("rgx live injection sentinel %d", time.Now().UnixMilli())
	answer := fmt.Sprintf("The live injected sentinel is %d.", time.Now().UnixMilli())

	liveClient := client.Client.(*ragflow.HTTPClient)
	chatModel := liveMemoryModelID(t, liveClient, ctx, "chat")
	chat, err := svc.CreateChatWithConfig(ctx, policy.TenantID, "Live Injection Chat "+id.New(), nil, ChatAuthoring{LLMID: chatModel})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := svc.DeleteChat(context.Background(), policy.TenantID, chat.ID, false); err != nil {
			t.Errorf("delete live injection chat: %v", err)
		}
	})
	chatSession, err := svc.CreateChatSession(ctx, policy.TenantID, chat.ID, "live injection", false)
	if err != nil {
		t.Fatal(err)
	}
	policy.AssistantID = chat.ID
	policy.UpdatedAt = time.Now().UTC()
	if err := svc.Store.UpdateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err := client.Client.AddMemoryMessage(ctx, ragflow.AddMessageRequest{
		MemoryIDs: []string{memoryID}, AgentID: chat.ID, SessionID: chatSession.ID,
		UserID: attribution, UserInput: query, AgentResponse: answer,
	}); err != nil {
		t.Fatal(err)
	}
	waitForLiveInjectionSearch(t, client, ctx, memoryID, chat.ID, chatSession.ID, attribution, query)

	chatKey := &model.APIKey{ID: id.New(), TenantID: policy.TenantID, UserID: target.ID}
	chatRequest := []byte(fmt.Sprintf(`{"messages":[{"role":"user","content":%q}]}`, query))
	chatNonStreamRequestID := "live-chat-nonstream-" + id.New()
	if _, err := svc.ChatAppCompletion(ctx, chatKey, chat.ID, chatSession.ID, chatRequest, chatNonStreamRequestID); err != nil {
		t.Fatalf("live chat nonstream completion: %v; response=%s", err, observation.chatResponse())
	}
	assertLivePreModelDecision(t, svc, policy.TenantID, target.ID, chatNonStreamRequestID)
	assertLiveInjectionFilters(t, observation.memorySearch(), policy.TenantID, target.ID, chat.ID, chatSession.ID, query)
	assertLiveInjectedCompletion(t, observation.chatBody(), query, answer)

	chatStreamRequestID := "live-chat-stream-" + id.New()
	if _, _, err := svc.StreamChatAppCompletion(ctx, chatKey, chat.ID, chatSession.ID, chatRequest, io.Discard, chatStreamRequestID); err != nil {
		t.Fatalf("live chat stream completion: %v; response=%s", err, observation.chatResponse())
	}
	assertLivePreModelDecision(t, svc, policy.TenantID, target.ID, chatStreamRequestID)
	assertLiveInjectionFilters(t, observation.memorySearch(), policy.TenantID, target.ID, chat.ID, chatSession.ID, query)
	assertLiveInjectedCompletion(t, observation.chatBody(), query, answer)

	agent, err := svc.CreateAgent(ctx, policy.TenantID, "Live Injection Agent "+id.New(), liveInjectionAgentDSL(), false, model.AgentCanvasCategoryWorkflow)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := svc.DeleteAgent(context.Background(), policy.TenantID, agent.ID, false); err != nil {
			t.Errorf("delete live injection agent: %v", err)
		}
	})
	agentSession, err := svc.CreateAgentSession(ctx, policy.TenantID, agent.ID, "live injection", false)
	if err != nil {
		t.Fatal(err)
	}
	policy.AssistantID = agent.ID
	policy.UpdatedAt = time.Now().UTC()
	if err := svc.Store.UpdateMemoryPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	if err := client.Client.AddMemoryMessage(ctx, ragflow.AddMessageRequest{
		MemoryIDs: []string{memoryID}, AgentID: agent.ID, SessionID: agentSession.ID,
		UserID: attribution, UserInput: query, AgentResponse: answer,
	}); err != nil {
		t.Fatal(err)
	}
	waitForLiveInjectionSearch(t, client, ctx, memoryID, agent.ID, agentSession.ID, attribution, query)

	messages := []ragflow.Message{{Role: "user", Content: query}}
	agentNonStreamRequestID := "live-agent-nonstream-" + id.New()
	if _, err := svc.AgentChatCompletion(ctx, policy.TenantID, agent.ID, target.ID, messages, nil, agentNonStreamRequestID, agentSession.ID); err != nil {
		t.Fatalf("live agent nonstream completion: %v; response=%s", err, observation.agentResponse())
	}
	assertLivePreModelDecision(t, svc, policy.TenantID, target.ID, agentNonStreamRequestID)
	assertLiveInjectionFilters(t, observation.memorySearch(), policy.TenantID, target.ID, agent.ID, agentSession.ID, query)
	assertLiveInjectedCompletion(t, observation.agentBody(), query, answer)

	agentStreamRequestID := "live-agent-stream-" + id.New()
	if err := svc.StreamAgentChatCompletion(ctx, policy.TenantID, agent.ID, target.ID, messages, nil, agentStreamRequestID, agentSession.ID, io.Discard); err != nil {
		t.Fatalf("live agent stream completion: %v; response=%s", err, observation.agentResponse())
	}
	assertLivePreModelDecision(t, svc, policy.TenantID, target.ID, agentStreamRequestID)
	assertLiveInjectionFilters(t, observation.memorySearch(), policy.TenantID, target.ID, agent.ID, agentSession.ID, query)
	assertLiveInjectedCompletion(t, observation.agentBody(), query, answer)
}

func liveInjectionAgentDSL() map[string]interface{} {
	return map[string]interface{}{
		"components": map[string]interface{}{
			"begin": map[string]interface{}{
				"obj":        map[string]interface{}{"component_name": "Begin", "params": map[string]interface{}{}},
				"downstream": []string{"message"},
				"upstream":   []string{},
			},
			"message": map[string]interface{}{
				"obj":        map[string]interface{}{"component_name": "Message", "params": map[string]interface{}{"content": []string{"{sys.query}"}}},
				"downstream": []string{},
				"upstream":   []string{"begin"},
			},
		},
		"history":   []interface{}{},
		"retrieval": []interface{}{},
		"path":      []interface{}{},
		"globals":   map[string]interface{}{},
		"variables": map[string]interface{}{},
	}
}

func waitForLiveInjectionSearch(
	t *testing.T, client *liveMemoryDispatchClient, ctx context.Context,
	memoryID, agentID, sessionID, attribution, query string,
) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		rows, err := client.Client.SearchMemoryMessages(ctx, memoryID, ragflow.MemorySearchParams{
			Query: query, AgentID: agentID, SessionID: sessionID, UserID: attribution, TopN: 5,
		})
		if err == nil && len(rows) > 0 {
			for _, row := range rows {
				if row.AgentID == agentID && row.SessionID == sessionID && row.UserID == attribution {
					return
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("live memory fixture stopped before search: %v", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatal("live memory fixture did not become searchable")
}

func assertLivePreModelDecision(
	t *testing.T, svc *Service, tenantID, userID, requestID string,
) {
	t.Helper()
	decision, err := svc.GetConversationContextDecisionByRequest(
		context.Background(), tenantID, userID, requestID,
	)
	if err != nil || decision == nil {
		t.Fatalf("load live context decision: decision=%+v err=%v", decision, err)
	}
	if decision.Decision != "memory_allowed" || decision.MemoryPolicyID == "" {
		t.Fatalf("unexpected live decision: %+v", decision)
	}
	var detail map[string]interface{}
	if err := json.Unmarshal([]byte(decision.Detail), &detail); err != nil {
		t.Fatalf("decode live decision detail: %v", err)
	}
	if detail["memory_retrieved"] != true || detail["memory_result_count"] != float64(1) {
		t.Fatalf("live decision retrieval evidence missing: %+v", detail)
	}
}

func assertLiveInjectionFilters(
	t *testing.T, params ragflow.MemorySearchParams, tenantID, userID, assistantID, sessionID, query string,
) {
	t.Helper()
	wantUserID := "rgx:" + tenantID + ":" + userID
	if params.Query != query || params.AgentID != assistantID ||
		params.SessionID != sessionID || params.UserID != wantUserID {
		t.Fatalf("unexpected live memory search filters: %+v", params)
	}
}

func assertLiveInjectedCompletion(t *testing.T, body, query, answer string) {
	t.Helper()
	if !strings.Contains(body, answer) {
		t.Fatal("governed memory evidence was not present in the live completion request")
	}
	var payload liveCompletionPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("decode live completion request: %v", err)
	}
	if len(payload.Messages) < 2 || payload.Messages[0].Role != "system" ||
		!strings.Contains(payload.Messages[0].Content, answer) {
		t.Fatalf("live completion did not begin with governed memory evidence: messages=%d", len(payload.Messages))
	}
	last := payload.Messages[len(payload.Messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, query) {
		t.Fatalf("live completion user message missing: role=%q", last.Role)
	}
}
