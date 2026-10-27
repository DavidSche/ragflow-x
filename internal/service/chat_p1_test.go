package service

import (
	"context"
	"strings"
	"testing"
)

func TestChatAuthoringRoundTrip(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	a := ChatAuthoring{PromptConfig: map[string]interface{}{"system": "请简洁作答"}, TopN: 5, SimilarityThreshold: 0.3}
	cs, err := svc.CreateChatWithConfig(ctx, ta.ID, "kb", []string{"d1"}, a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cs.ConfigJSON, "prompt_config") || !strings.Contains(cs.ConfigJSON, "top_n") {
		t.Fatalf("authoring config not persisted: %s", cs.ConfigJSON)
	}
	got, err := svc.GetChat(ctx, ta.ID, cs.ID, false)
	if err != nil || got.ConfigJSON != cs.ConfigJSON {
		t.Fatalf("config round trip mismatch: err=%v got=%s want=%s", err, got.ConfigJSON, cs.ConfigJSON)
	}
}

func TestChatSessionRenameAndDelete(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, _ := svc.CreateTenant(ctx, "TenantA")
	tb, _ := svc.CreateTenant(ctx, "TenantB")
	chat, err := svc.CreateChat(ctx, ta.ID, "kb", []string{"d1"})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := svc.CreateChatSession(ctx, ta.ID, chat.ID, "s1", false)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := svc.UpdateChatSession(ctx, ta.ID, chat.ID, sess.ID, "renamed", false)
	if err != nil || renamed.Name != "renamed" {
		t.Fatalf("rename: err=%v got=%+v", err, renamed)
	}
	if _, err := svc.UpdateChatSession(ctx, tb.ID, chat.ID, sess.ID, "x", false); err == nil {
		t.Fatal("tenant B must not rename tenant A's session")
	}
	if err := svc.DeleteChatSessions(ctx, ta.ID, chat.ID, []string{sess.ID}, false); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := svc.GetChatSession(ctx, ta.ID, chat.ID, sess.ID, false); err == nil {
		t.Fatal("session should be gone after delete")
	}
}
