package service

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type attachmentAgentClient struct {
	ragflow.Mock
	completionRequest *ragflow.CompletionRequest
}

func (c *attachmentAgentClient) AgentChatCompletion(ctx context.Context, request ragflow.CompletionRequest) (*ragflow.CompletionResponse, error) {
	c.completionRequest = &request
	return &ragflow.CompletionResponse{Choices: []ragflow.CompletionChoice{{Message: ragflow.Message{Content: "ok"}}}}, nil
}

func (c *attachmentAgentClient) StreamAgentChatCompletion(ctx context.Context, request ragflow.CompletionRequest, _ io.Writer) error {
	c.completionRequest = &request
	return nil
}

func TestAgentCompletionResolvesAttachmentTickets(t *testing.T) {
	ctx := context.Background()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "a.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	client := &attachmentAgentClient{Mock: *ragflow.NewMock()}
	svc := New(repository.NewStore(gdb), client, jwt.NewManager("secret", 24), "key")
	tenant, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := svc.CreateAgent(ctx, tenant.ID, "assistant", map[string]interface{}{"x": 1}, false, "agent_canvas")
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := svc.UploadAgentFile(ctx, tenant.ID, agent.ID, "user-1", "notes.txt", []byte("attachment"))
	if err != nil {
		t.Fatal(err)
	}
	files := []map[string]interface{}{{"id": "forged", "created_by": "forged", "upload_ticket": uploaded.UploadTicket}}
	if _, err := svc.AgentChatCompletion(ctx, tenant.ID, agent.ID, "user-1", []ragflow.Message{{Role: "user", Content: "hi"}}, files, "request-1", ""); err != nil {
		t.Fatal(err)
	}
	if client.completionRequest == nil || len(client.completionRequest.Files) != 1 {
		t.Fatalf("completion request did not contain resolved files: %+v", client.completionRequest)
	}
	if client.completionRequest.Files[0]["id"] != uploaded.ID || client.completionRequest.Files[0]["created_by"] != uploaded.CreatedBy {
		t.Fatalf("unexpected provider files: %+v", client.completionRequest.Files)
	}
	if err := svc.StreamAgentChatCompletion(ctx, tenant.ID, agent.ID, "user-1", []ragflow.Message{{Role: "user", Content: "hi"}}, files, "request-2", "", io.Discard); err != nil {
		t.Fatal(err)
	}
	if client.completionRequest == nil || len(client.completionRequest.Files) != 1 {
		t.Fatalf("stream request did not contain resolved files: %+v", client.completionRequest)
	}
}

func TestAgentAttachmentTickets(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := svc.CreateAgent(ctx, tenant.ID, "assistant", map[string]interface{}{"x": 1}, false, "agent_canvas")
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := svc.UploadAgentFile(ctx, tenant.ID, agent.ID, "user-1", "notes.txt", []byte("attachment"))
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := svc.resolveAgentAttachmentTickets(tenant.ID, agent.ID, "user-1", []map[string]interface{}{{
		"id": "forged", "created_by": "forged", "upload_ticket": uploaded.UploadTicket,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resolved[0]["id"] != uploaded.ID || resolved[0]["created_by"] != uploaded.CreatedBy {
		t.Fatalf("server metadata was not restored: %+v", resolved)
	}
}

func TestAgentAttachmentTicketRejectsForgedAndMisboundReferences(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := svc.CreateAgent(ctx, tenant.ID, "assistant", map[string]interface{}{"x": 1}, false, "agent_canvas")
	if err != nil {
		t.Fatal(err)
	}
	uploaded, err := svc.UploadAgentFile(ctx, tenant.ID, agent.ID, "user-1", "notes.txt", []byte("attachment"))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name     string
		tenantID string
		agentID  string
		userID   string
		files    []map[string]interface{}
	}{
		{name: "missing ticket", tenantID: tenant.ID, agentID: agent.ID, userID: "user-1", files: []map[string]interface{}{{"id": uploaded.ID}}},
		{name: "forged signature", tenantID: tenant.ID, agentID: agent.ID, userID: "user-1", files: []map[string]interface{}{{"upload_ticket": uploaded.UploadTicket + "x"}}},
		{name: "wrong tenant", tenantID: "tenant-2", agentID: agent.ID, userID: "user-1", files: []map[string]interface{}{{"upload_ticket": uploaded.UploadTicket}}},
		{name: "wrong agent", tenantID: tenant.ID, agentID: "agent-2", userID: "user-1", files: []map[string]interface{}{{"upload_ticket": uploaded.UploadTicket}}},
		{name: "wrong user", tenantID: tenant.ID, agentID: agent.ID, userID: "user-2", files: []map[string]interface{}{{"upload_ticket": uploaded.UploadTicket}}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			_, err := svc.resolveAgentAttachmentTickets(item.tenantID, item.agentID, item.userID, item.files)
			if err == nil {
				t.Fatal("expected attachment reference to be rejected")
			}
		})
	}
}

func TestAgentAttachmentTicketExpiry(t *testing.T) {
	svc := newAuthzSvc(t)
	ticket := svc.signAgentAttachment(agentAttachmentClaims{
		FileID: "file-1", CreatedBy: "tenant-1", AgentID: "agent-1", TenantID: "tenant-1", UserID: "user-1",
		ExpiresAt: 1,
	})
	_, err := svc.resolveAgentAttachmentTickets("tenant-1", "agent-1", "user-1", []map[string]interface{}{{"upload_ticket": ticket}})
	if he := err.(*httperr.Error); he.Status != 403 {
		t.Fatalf("expected 403 for expired ticket, got %+v", err)
	}
}
