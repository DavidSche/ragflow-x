package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// TestAgentPublishWritesVersion pins the difference between "flip the release
// flag" and "publish". Only the latter writes a version row; unpublish must
// never add one. Before this test the mock invented a version on create and on
// every DSL update, so the unit surface agreed with the UI and both disagreed
// with the engine (version page empty, rollback without a target).
func TestAgentPublishWritesVersion(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantPublish")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := svc.CreateAgent(ctx, tenant.ID, "agent-publish",
		map[string]interface{}{"components": map[string]interface{}{}}, false, "agent_canvas")
	if err != nil {
		t.Fatal(err)
	}

	versions, err := svc.ListAgentVersions(ctx, tenant.ID, agent.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 0 {
		t.Fatalf("a new agent must not carry versions: %+v", versions)
	}

	// 下线（release=false）只翻标记：不能产生版本。
	if _, err := svc.PublishAgent(ctx, tenant.ID, agent.ID, false, false); err != nil {
		t.Fatal(err)
	}
	if versions, err = svc.ListAgentVersions(ctx, tenant.ID, agent.ID, false); err != nil {
		t.Fatal(err)
	} else if len(versions) != 0 {
		t.Fatalf("unpublish must not create a version: %+v", versions)
	}

	released, err := svc.PublishAgent(ctx, tenant.ID, agent.ID, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !released.Release {
		t.Fatal("publish must mark the shadow released")
	}
	versions, err = svc.ListAgentVersions(ctx, tenant.ID, agent.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Fatalf("publish must leave exactly one version, got %+v", versions)
	}
	version := versions[0]
	if version.ID == "" || version.UserCanvasID != agent.ID {
		t.Fatalf("version must point back at the agent: %+v", version)
	}
	if version.Dsl == nil {
		t.Fatal("version must snapshot the DSL (rollback needs it)")
	}
	// 引擎写毫秒。用秒会让界面把它当 1970 年附近的时刻。
	if version.UpdateTime < 1_000_000_000_000 {
		t.Fatalf("version update_time must be milliseconds, got %d", version.UpdateTime)
	}

	// 重复发布是允许的：每次发布都是一个可回滚的锚点。
	if _, err := svc.PublishAgent(ctx, tenant.ID, agent.ID, true, false); err != nil {
		t.Fatal(err)
	}
	if versions, err = svc.ListAgentVersions(ctx, tenant.ID, agent.ID, false); err != nil {
		t.Fatal(err)
	} else if len(versions) != 2 {
		t.Fatalf("re-publish must append a version, got %d", len(versions))
	}

	// 下线不删版本，也不新增。
	final, err := svc.PublishAgent(ctx, tenant.ID, agent.ID, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if final.Release {
		t.Fatal("unpublish must clear the release flag")
	}
	if versions, err = svc.ListAgentVersions(ctx, tenant.ID, agent.ID, false); err != nil {
		t.Fatal(err)
	} else if len(versions) != 2 {
		t.Fatalf("unpublish must not change the version history, got %d", len(versions))
	}
}

// notFoundProvider reports every version/session read as an upstream 404, which
// is what the engine does once the object is deleted.
type notFoundProvider struct {
	ragflow.Client
}

func (p *notFoundProvider) GetAgentSession(context.Context, string, string) (*ragflow.AgentSession, error) {
	return nil, &ragflow.Error{Type: ragflow.ErrorTypeBusiness, HTTPStatus: http.StatusNotFound, Message: "session not found"}
}

func (p *notFoundProvider) DeleteAgentSession(context.Context, string, string) error {
	return &ragflow.Error{Type: ragflow.ErrorTypeBusiness, HTTPStatus: http.StatusNotFound, Message: "session not found"}
}

func (p *notFoundProvider) GetAgentVersion(context.Context, string, string) (*ragflow.AgentVersion, error) {
	return nil, &ragflow.Error{Type: ragflow.ErrorTypeBusiness, HTTPStatus: http.StatusNotFound, Message: "version not found"}
}

// envelopeNotFoundProvider reproduces the engine's real wire shape for a
// missing session: HTTP 200 with `{"code":404,"message":"agent session not
// found"}`. The provider turns that into Error{HTTPStatus:200, Code:404} — a
// helper that only inspects HTTPStatus reports a gateway outage instead.
type envelopeNotFoundProvider struct {
	ragflow.Client
}

func (p *envelopeNotFoundProvider) GetAgentSession(context.Context, string, string) (*ragflow.AgentSession, error) {
	return nil, &ragflow.Error{
		Type:       ragflow.ErrorTypeBusiness,
		HTTPStatus: http.StatusOK,
		Code:       http.StatusNotFound,
		Message:    "agent session not found",
	}
}

// TestAgentUpstreamEnvelopeNotFoundStaysNotFound covers the envelope-404 shape.
func TestAgentUpstreamEnvelopeNotFoundStaysNotFound(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantEnvelopeNotFound")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := svc.CreateAgent(ctx, tenant.ID, "agent-envelope-notfound",
		map[string]interface{}{"components": map[string]interface{}{}}, false, "agent_canvas")
	if err != nil {
		t.Fatal(err)
	}
	svc.RAGFlow = &envelopeNotFoundProvider{Client: svc.RAGFlow}

	_, err = svc.GetAgentSession(ctx, tenant.ID, agent.ID, "gone", false)
	if err == nil {
		t.Fatal("get deleted session must fail")
	}
	he, ok := err.(*httperr.Error)
	if !ok {
		t.Fatalf("unexpected error type %T (%v)", err, err)
	}
	if he.Status != http.StatusNotFound || he.Code != http.StatusNotFound {
		t.Fatalf("expected 404/404 for an envelope 404, got %d/%d (%s)", he.Status, he.Code, he.Message)
	}
}

// TestAgentUpstreamNotFoundStaysNotFound keeps a deleted object a 404.
//
// Flattening the provider's 404 into 502/5028x told the client "the gateway
// broke" about a session the user had just deleted: the UI kept the row and
// printed a gateway error instead of removing it.
func TestAgentUpstreamNotFoundStaysNotFound(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantNotFound")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := svc.CreateAgent(ctx, tenant.ID, "agent-notfound",
		map[string]interface{}{"components": map[string]interface{}{}}, false, "agent_canvas")
	if err != nil {
		t.Fatal(err)
	}
	svc.RAGFlow = &notFoundProvider{Client: svc.RAGFlow}

	assertNotFound := func(label string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s must fail", label)
		}
		he, ok := err.(*httperr.Error)
		if !ok {
			t.Fatalf("%s: unexpected error type %T (%v)", label, err, err)
		}
		if he.Status != http.StatusNotFound {
			t.Fatalf("%s: expected 404, got %d (%s)", label, he.Status, he.Message)
		}
		if he.Code != http.StatusNotFound {
			t.Fatalf("%s: expected code 404, got %d (%s)", label, he.Code, he.Message)
		}
	}

	_, err = svc.GetAgentSession(ctx, tenant.ID, agent.ID, "gone", false)
	assertNotFound("get deleted session", err)
	err = svc.DeleteAgentSession(ctx, tenant.ID, agent.ID, "gone", false)
	assertNotFound("delete deleted session", err)
	_, _, err = svc.ListAgentSessionMessages(ctx, tenant.ID, agent.ID, "gone", false,
		ragflow.SessionMessagePageOptions{})
	assertNotFound("list messages of deleted session", err)
	_, err = svc.GetAgentVersion(ctx, tenant.ID, agent.ID, "gone", false)
	assertNotFound("get deleted version", err)
}
