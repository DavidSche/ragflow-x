package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// TestAgentCreateValidationCodes verifies the create payload validation uses
// codes that do not collide with the memory model-validation codes
// (40097/40098 are owned by memory; agents use 40087/40088).
func TestAgentCreateValidationCodes(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	if _, err := svc.CreateAgent(ctx, "t", "", map[string]interface{}{"x": 1}, false, ""); err == nil {
		t.Fatal("empty title must be rejected")
	} else if he := err.(*httperr.Error); he.Code != 40087 {
		t.Fatalf("empty title expected code 40087, got %d (%s)", he.Code, he.Message)
	}
	if _, err := svc.CreateAgent(ctx, "t", "A", map[string]interface{}{}, false, ""); err == nil {
		t.Fatal("empty dsl must be rejected")
	} else if he := err.(*httperr.Error); he.Code != 40088 {
		t.Fatalf("empty dsl expected code 40088, got %d (%s)", he.Code, he.Message)
	}
}

// TestAgentTenantIsolation verifies agents are owned by a single platform tenant
// and that cross-tenant reads/writes are rejected unless scopeAll.
func TestAgentTenantIsolation(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	tb, err := svc.CreateTenant(ctx, "TenantB")
	if err != nil {
		t.Fatal(err)
	}

	agent, err := svc.CreateAgent(ctx, ta.ID, "agent-a", map[string]interface{}{"x": 1}, true, "agent_canvas")
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if agent.ID == "" || agent.TenantID != ta.ID || !agent.Release {
		t.Fatalf("unexpected agent shadow: %+v", agent)
	}

	// Tenant B must not read/detail/update/publish/delete/versions/sessions.
	if _, err := svc.GetAgent(ctx, tb.ID, agent.ID, false); err == nil {
		t.Fatal("tenant B must not read tenant A's agent")
	}
	if _, err := svc.GetAgentDetail(ctx, tb.ID, agent.ID, false); err == nil {
		t.Fatal("tenant B must not read tenant A's agent detail")
	}
	if _, err := svc.UpdateAgent(ctx, tb.ID, agent.ID, "x", nil, nil, false); err == nil {
		t.Fatal("tenant B must not update tenant A's agent")
	}
	if _, err := svc.PublishAgent(ctx, tb.ID, agent.ID, false, false); err == nil {
		t.Fatal("tenant B must not publish/unpublish tenant A's agent")
	}
	if err := svc.DeleteAgent(ctx, tb.ID, agent.ID, false); err == nil {
		t.Fatal("tenant B must not delete tenant A's agent")
	}
	if _, err := svc.ListAgentVersions(ctx, tb.ID, agent.ID, false); err == nil {
		t.Fatal("tenant B must not list tenant A's agent versions")
	}
	if _, _, err := svc.ListAgentSessions(ctx, tb.ID, agent.ID, false, ragflow.SessionListOptions{}); err == nil {
		t.Fatal("tenant B must not list tenant A's agent sessions")
	}
	if _, err := svc.CreateAgentSession(ctx, tb.ID, agent.ID, "S", false); err == nil {
		t.Fatal("tenant B must not create a session on tenant A's agent")
	}

	items, total, err := svc.ListAgents(ctx, tb.ID, false, repository.AgentFilter{}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("tenant B leaked tenant A agents: total=%d", total)
	}

	// Owner can operate.
	if got, err := svc.GetAgent(ctx, ta.ID, agent.ID, false); err != nil || got.ID != agent.ID {
		t.Fatalf("owner must read own agent: err=%v got=%+v", err, got)
	}
	sess, err := svc.CreateAgentSession(ctx, ta.ID, agent.ID, "S1", false)
	if err != nil {
		t.Fatalf("owner create session: %v", err)
	}
	if _, err := svc.GetAgentSession(ctx, ta.ID, agent.ID, sess.ID, false); err != nil {
		t.Fatalf("owner get session: %v", err)
	}
	if err := svc.DeleteAgentSession(ctx, ta.ID, agent.ID, sess.ID, false); err != nil {
		t.Fatalf("owner delete session: %v", err)
	}
	if _, err := svc.AgentChatCompletion(ctx, ta.ID, agent.ID, "", []ragflow.Message{{Role: "user", Content: "hi"}}, nil, "req-1", ""); err != nil {
		t.Fatalf("owner completion: %v", err)
	}

	// scopeAll lets platform admins see it.
	_, total, err = svc.ListAgents(ctx, "any", true, repository.AgentFilter{}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total < 1 {
		t.Fatalf("scopeAll should include the agent, total=%d", total)
	}
	if _, err := svc.GetAgent(ctx, tb.ID, agent.ID, true); err != nil {
		t.Fatalf("scopeAll read should succeed: %v", err)
	}
}

func TestListAgentsExposesConversationCategory(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)

	workflow, err := svc.CreateAgent(ctx, "t", "workflow", map[string]interface{}{"x": 1}, false, "agent_canvas")
	if err != nil {
		t.Fatal(err)
	}
	dataflow, err := svc.CreateAgent(ctx, "t", "dataflow", map[string]interface{}{"x": 1}, false, "dataflow_canvas")
	if err != nil {
		t.Fatal(err)
	}

	items, total, err := svc.ListAgents(ctx, "t", false, repository.AgentFilter{}, 1, 20)
	if err != nil || total != 2 || len(items) != 2 {
		t.Fatalf("list agents: total=%d items=%+v err=%v", total, items, err)
	}
	categories := map[string]string{}
	for _, item := range items {
		categories[item.ID] = item.CanvasCategory
	}
	if categories[workflow.ID] != "agent_canvas" || categories[dataflow.ID] != "dataflow_canvas" {
		t.Fatalf("live categories were not mapped: %+v", categories)
	}
}

func TestAgentUploadOwnership(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, _ := svc.CreateTenant(ctx, "TenantA")
	tb, _ := svc.CreateTenant(ctx, "TenantB")
	agent, err := svc.CreateAgent(ctx, ta.ID, "assistant", map[string]interface{}{"x": 1}, false, "agent_canvas")
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := svc.UploadAgentFile(ctx, ta.ID, agent.ID, "user-a", "notes.txt", []byte("agent attachment"))
	if err != nil {
		t.Fatal(err)
	}
	if uploaded.ID == "" || uploaded.Name != "notes.txt" {
		t.Fatalf("unexpected upload: %+v", uploaded)
	}
	if uploaded.UploadTicket == "" {
		t.Fatal("agent upload must issue an attachment reference")
	}
	if _, err := svc.UploadAgentFile(ctx, tb.ID, agent.ID, "user-b", "notes.txt", []byte("agent attachment")); err == nil {
		t.Fatal("tenant B must not upload to tenant A's agent")
	}
}
