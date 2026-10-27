package repository

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ScenarioID: SC-TENANT-001
func TestP0_TENANT_001_ShadowResourcesEnforceTenantScope(t *testing.T) {
	store := newTeamProjectStore(t)
	ctx := context.Background()
	seededAt := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

	chats := []*model.ChatShadow{
		{ID: "chat-a", TenantID: "tenant-a", Name: "A", OwnerID: "owner-a", CreatedAt: seededAt, UpdatedAt: seededAt},
		{ID: "chat-b", TenantID: "tenant-b", Name: "B", OwnerID: "owner-b", CreatedAt: seededAt, UpdatedAt: seededAt},
	}
	for _, chat := range chats {
		if err := store.UpsertChatShadow(ctx, chat); err != nil {
			t.Fatal(err)
		}
	}
	if list, total, err := store.ListChatShadows(ctx, "tenant-a", false, ChatFilter{}, 1, 20); err != nil || total != 1 || len(list) != 1 || list[0].ID != "chat-a" {
		t.Fatalf("tenant chat list: total=%d list=%+v err=%v", total, list, err)
	}
	if _, total, err := store.ListChatShadows(ctx, "", false, ChatFilter{}, 1, 20); err != nil || total != 0 {
		t.Fatalf("empty tenant must fail closed: total=%d err=%v", total, err)
	}
	if got, err := store.GetChatShadow(ctx, "tenant-b", "chat-a", false); err != nil || got != nil {
		t.Fatalf("wrong tenant get: got=%+v err=%v", got, err)
	}
	if got, err := store.GetChatShadow(ctx, "", "chat-a", false); err != nil || got != nil {
		t.Fatalf("empty tenant get: got=%+v err=%v", got, err)
	}
	if got, err := store.GetChatShadowForScope(ctx, false, nil, "chat-a"); err != nil || got != nil {
		t.Fatalf("empty scope get: got=%+v err=%v", got, err)
	}
	if err := store.BatchUpdateChatStatus(ctx, "tenant-a", false, []string{"chat-a", "chat-b"}, "disabled"); err != nil {
		t.Fatal(err)
	}
	if chat, _ := store.GetChatShadow(ctx, "tenant-a", "chat-a", false); chat == nil || chat.Status != "disabled" {
		t.Fatalf("tenant chat not updated: %+v", chat)
	}
	if chat, _ := store.GetChatShadow(ctx, "tenant-b", "chat-b", false); chat == nil || chat.Status != "active" {
		t.Fatalf("foreign chat must not be updated: %+v", chat)
	}

	memories := []*model.MemoryShadow{
		{ID: "memory-a", TenantID: "tenant-a", Name: "A", MemoryType: "vector", OwnerID: "owner-a", CreatedAt: seededAt, UpdatedAt: seededAt},
		{ID: "memory-b", TenantID: "tenant-b", Name: "B", MemoryType: "vector", OwnerID: "owner-b", CreatedAt: seededAt, UpdatedAt: seededAt},
	}
	for _, memory := range memories {
		if err := store.UpsertMemoryShadow(ctx, memory); err != nil {
			t.Fatal(err)
		}
	}
	if list, total, err := store.ListMemoryShadows(ctx, "tenant-a", false, MemoryFilter{}, 1, 20); err != nil || total != 1 || len(list) != 1 || list[0].ID != "memory-a" {
		t.Fatalf("tenant memory list: total=%d list=%+v err=%v", total, list, err)
	}
	if got, err := store.GetMemoryShadow(ctx, "tenant-b", "memory-a", false); err != nil || got != nil {
		t.Fatalf("wrong tenant memory get: got=%+v err=%v", got, err)
	}
	if got, err := store.GetMemoryShadow(ctx, "", "memory-a", false); err != nil || got != nil {
		t.Fatalf("empty tenant memory get: got=%+v err=%v", got, err)
	}

	agents := []*model.AgentShadow{
		{ID: "agent-a", TenantID: "tenant-a", Title: "A", OwnerID: "owner-a", CreatedAt: seededAt, UpdatedAt: seededAt},
		{ID: "agent-b", TenantID: "tenant-b", Title: "B", OwnerID: "owner-b", CreatedAt: seededAt, UpdatedAt: seededAt},
	}
	for _, agent := range agents {
		if err := store.UpsertAgentShadow(ctx, agent); err != nil {
			t.Fatal(err)
		}
	}
	if list, total, err := store.ListAgentShadows(ctx, "tenant-a", false, AgentFilter{}, 1, 20); err != nil || total != 1 || len(list) != 1 || list[0].ID != "agent-a" {
		t.Fatalf("tenant agent list: total=%d list=%+v err=%v", total, list, err)
	}
	if got, err := store.GetAgentShadow(ctx, "tenant-b", "agent-a", false); err != nil || got != nil {
		t.Fatalf("wrong tenant agent get: got=%+v err=%v", got, err)
	}
	if got, err := store.GetAgentShadow(ctx, "", "agent-a", false); err != nil || got != nil {
		t.Fatalf("empty tenant agent get: got=%+v err=%v", got, err)
	}

	searchApps := []*model.SearchAppShadow{
		{ID: "app-a", TenantID: "tenant-a", Name: "A", OwnerID: "owner-a", CreatedAt: seededAt, UpdatedAt: seededAt},
		{ID: "app-b", TenantID: "tenant-b", Name: "B", OwnerID: "owner-b", CreatedAt: seededAt, UpdatedAt: seededAt},
	}
	for _, app := range searchApps {
		if err := store.UpsertSearchAppShadow(ctx, app); err != nil {
			t.Fatal(err)
		}
	}
	if list, total, err := store.ListSearchAppShadows(ctx, "tenant-a", false, SearchAppFilter{}, 1, 20); err != nil || total != 1 || len(list) != 1 || list[0].ID != "app-a" {
		t.Fatalf("tenant search app list: total=%d list=%+v err=%v", total, list, err)
	}
	if got, err := store.GetSearchAppShadow(ctx, "tenant-b", "app-a", false); err != nil || got != nil {
		t.Fatalf("wrong tenant search app get: got=%+v err=%v", got, err)
	}
	if got, err := store.GetSearchAppShadow(ctx, "", "app-a", false); err != nil || got != nil {
		t.Fatalf("empty tenant search app get: got=%+v err=%v", got, err)
	}
}

// ScenarioID: SC-AUDIT-001
func TestP0_AUDIT_001_AuditAnchorsEnforceTenantScopeAndIdempotency(t *testing.T) {
	store := newTeamProjectStore(t)
	ctx := context.Background()
	anchorAt := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	anchor := &model.AuditAnchor{
		TenantID: "tenant-a", LastSeq: 10, LastHash: "hash-a", AnchorAt: anchorAt, CreatedAt: anchorAt,
	}
	created, err := store.CreateAuditAnchor(ctx, anchor)
	if err != nil || !created {
		t.Fatalf("first anchor: created=%v err=%v", created, err)
	}
	if anchor.ID == "" || anchor.Algorithm != model.AuditAnchorAlgorithm {
		t.Fatalf("anchor defaults not applied: %+v", anchor)
	}
	created, err = store.CreateAuditAnchor(ctx, &model.AuditAnchor{
		TenantID: "tenant-a", LastSeq: 10, LastHash: "hash-a", AnchorAt: anchorAt, CreatedAt: anchorAt,
	})
	if err != nil || created {
		t.Fatalf("duplicate anchor: created=%v err=%v", created, err)
	}
	if _, err := store.CreateAuditAnchor(ctx, &model.AuditAnchor{LastSeq: 11, LastHash: "hash-b", AnchorAt: anchorAt}); err == nil {
		t.Fatal("expected empty tenant to be rejected")
	}

	list, total, err := store.ListAuditAnchors(ctx, "tenant-a", false, 1, 20)
	if err != nil || total != 1 || len(list) != 1 || list[0].TenantID != "tenant-a" {
		t.Fatalf("tenant anchor list: total=%d list=%+v err=%v", total, list, err)
	}
	if _, total, err = store.ListAuditAnchors(ctx, "tenant-b", false, 1, 20); err != nil || total != 0 {
		t.Fatalf("foreign tenant anchor list: total=%d err=%v", total, err)
	}
	all, err := store.ListAuditAnchorsAll(ctx, "tenant-a", false)
	if err != nil || len(all) != 1 || all[0].TenantID != "tenant-a" {
		t.Fatalf("tenant anchor all: all=%+v err=%v", all, err)
	}
}
