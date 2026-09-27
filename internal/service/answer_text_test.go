package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestVisibleRAGFlowAnswerStripsThinking(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "visible", input: "答案", want: "答案"},
		{name: "closed", input: thinkOpen + "reasoning" + thinkClose + "\n答案", want: "答案"},
		{name: "multiple", input: thinkOpen + "first" + thinkClose + "\nsecond", want: "second"},
		{name: "unclosed", input: thinkOpen + "reasoning", want: ""},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := visibleRAGFlowAnswer(item.input); got != item.want {
				t.Fatalf("visibleRAGFlowAnswer()=%q, want %q", got, item.want)
			}
		})
	}
}

func TestVisibleRAGFlowAnswerPreservesOrdinaryMarkdown(t *testing.T) {
	input := "# 结论\n\n```go\nfmt.Println(\"ok\")\n```\n"
	if got := visibleRAGFlowAnswer(input); !strings.Contains(got, "```go") {
		t.Fatalf("ordinary fenced markdown changed: %q", got)
	}
}

type thinkingAgentClient struct {
	ragflow.Mock
}

type thinkingChatClient struct {
	ragflow.Mock
}

type thinkingSearchClient struct {
	ragflow.Mock
}

func (c *thinkingSearchClient) SearchAppCompletion(context.Context, string, ragflow.SearchAppCompletionRequest) (*ragflow.SearchAppCompletionResult, error) {
	return &ragflow.SearchAppCompletionResult{
		Answer: thinkOpen + "private reasoning" + thinkClose + "visible search answer",
	}, nil
}

func (c *thinkingChatClient) ChatCompletion(context.Context, string, ragflow.CompletionRequest) (*ragflow.CompletionResponse, error) {
	return &ragflow.CompletionResponse{
		Answer: thinkOpen + "private reasoning" + thinkClose + "visible chat answer",
		Choices: []ragflow.CompletionChoice{{Message: ragflow.Message{
			Role: "assistant", Content: thinkOpen + "private reasoning" + thinkClose + "visible chat answer",
		}}},
	}, nil
}

func (c *thinkingAgentClient) AgentChatCompletion(context.Context, ragflow.CompletionRequest) (*ragflow.CompletionResponse, error) {
	return &ragflow.CompletionResponse{
		Choices: []ragflow.CompletionChoice{{Message: ragflow.Message{
			Role: "assistant", Content: thinkOpen + "private reasoning" + thinkClose + "visible answer",
		}}},
	}, nil
}

func TestAgentCompletionSuppressesThinkingInResponseAndSnapshot(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "thinking.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	client := &thinkingAgentClient{Mock: *ragflow.NewMock()}
	svc := New(repository.NewStore(gdb), client, jwt.NewManager("secret", 24), "key")
	tenant, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := svc.CreateAgent(ctx, tenant.ID, "assistant", map[string]interface{}{"x": 1}, false, "agent_canvas")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := svc.AgentChatCompletion(ctx, tenant.ID, agent.ID, "user-1", []ragflow.Message{{Role: "user", Content: "question"}}, nil, "request-thinking", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Choices[0].Message.Content; got != "visible answer" {
		t.Fatalf("response retained thinking: %q", got)
	}
	snapshot, err := svc.Store.GetAnswerSnapshotByRequest(ctx, tenant.ID, "request-thinking")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Content != "visible answer" {
		t.Fatalf("snapshot retained thinking: %q", snapshot.Content)
	}
}

func TestChatCompletionSuppressesThinkingInResponseAndSnapshot(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "thinking-chat.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	client := &thinkingChatClient{Mock: *ragflow.NewMock()}
	svc := New(repository.NewStore(gdb), client, jwt.NewManager("secret", 24), "key")
	tenant, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	chat, err := client.CreateChat(ctx, ragflow.CreateChatRequest{Name: "assistant"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := client.CreateChatSession(ctx, chat.ID, "session")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertChatShadow(ctx, &model.ChatShadow{ID: chat.ID, TenantID: tenant.ID, Name: chat.Name, Status: model.TenantStatusActive}); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"messages":[{"role":"user","content":"question"}]}`)
	result, err := svc.ChatAppCompletion(ctx, &model.APIKey{TenantID: tenant.ID, UserID: "user-1"}, chat.ID, session.ID, raw, "request-thinking-chat")
	if err != nil {
		t.Fatal(err)
	}
	var response ragflow.CompletionResponse
	if err := json.Unmarshal(result.Body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Answer != "visible chat answer" || response.Choices[0].Message.Content != "visible chat answer" {
		t.Fatalf("chat response retained thinking: %+v", response)
	}
	snapshot, err := svc.Store.GetAnswerSnapshotByRequest(ctx, tenant.ID, "request-thinking-chat")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Content != "visible chat answer" {
		t.Fatalf("chat snapshot retained thinking: %q", snapshot.Content)
	}
}

func TestSearchCompletionSuppressesThinkingInResponseAndSnapshot(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "thinking-search.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	client := &thinkingSearchClient{Mock: *ragflow.NewMock()}
	svc := New(repository.NewStore(gdb), client, jwt.NewManager("secret", 24), "key")
	tenant, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	app, err := svc.CreateSearchApp(ctx, tenant.ID, "assistant", &ragflow.SearchConfig{KbIDs: []string{"d1"}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := svc.SearchAppCompletion(ctx, tenant.ID, app.ID, "question", "request-thinking-search", false)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Answer != "visible search answer" {
		t.Fatalf("search response retained thinking: %q", resp.Answer)
	}
	snapshot, err := svc.Store.GetAnswerSnapshotByRequest(ctx, tenant.ID, "request-thinking-search")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Content != "visible search answer" {
		t.Fatalf("search snapshot retained thinking: %q", snapshot.Content)
	}
}
