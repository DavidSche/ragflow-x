package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// TestMemoryTenantIsolation verifies memories are owned by a single platform
// tenant and that cross-tenant reads/writes are rejected unless scopeAll.
func TestMemoryTenantIsolation(t *testing.T) {
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

	mem, err := svc.CreateMemory(ctx, ta.ID, "mem-a", []string{"raw"}, "e", "l")
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}
	if mem.ID == "" || mem.TenantID != ta.ID {
		t.Fatalf("unexpected memory shadow: %+v", mem)
	}

	// Tenant B must not read/config/update/delete/list messages of A's memory.
	if _, err := svc.GetMemory(ctx, tb.ID, mem.ID, false); err == nil {
		t.Fatal("tenant B must not read tenant A's memory")
	}
	if _, err := svc.GetMemoryConfig(ctx, tb.ID, mem.ID, false); err == nil {
		t.Fatal("tenant B must not read tenant A's memory config")
	}
	var tname = "x"
	if _, err := svc.UpdateMemory(ctx, tb.ID, mem.ID, ragflow.UpdateMemoryRequest{Name: &tname}, false); err == nil {
		t.Fatal("tenant B must not update tenant A's memory")
	}
	if err := svc.DeleteMemory(ctx, tb.ID, mem.ID, false); err == nil {
		t.Fatal("tenant B must not delete tenant A's memory")
	}
	if _, err := svc.ListMemoryMessages(ctx, tb.ID, mem.ID, false); err == nil {
		t.Fatal("tenant B must not list tenant A's memory messages")
	}
	if err := svc.AddMemoryMessage(ctx, tb.ID, mem.ID, "a", "s", "q", "a", false); err == nil {
		t.Fatal("tenant B must not add messages to tenant A's memory")
	}

	items, total, err := svc.ListMemories(ctx, tb.ID, false, repository.MemoryFilter{}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("tenant B leaked tenant A memories: total=%d", total)
	}

	// Owner can operate; platform admin (scopeAll) sees it too.
	if got, err := svc.GetMemory(ctx, ta.ID, mem.ID, false); err != nil || got.ID != mem.ID {
		t.Fatalf("owner must read own memory: err=%v got=%+v", err, got)
	}
	if err := svc.AddMemoryMessage(ctx, ta.ID, mem.ID, "a", "s", "q", "a", false); err != nil {
		t.Fatalf("owner add message: %v", err)
	}
	msgs, err := svc.ListMemoryMessages(ctx, ta.ID, mem.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].UserInput != "q" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}
	if _, err := svc.SearchMemoryMessages(ctx, tb.ID, mem.ID, "q", 0.2, 0.7, 5, false); err == nil {
		t.Fatal("tenant B must not search tenant A's memory")
	}
	if _, err := svc.GetMemoryMessageContent(ctx, tb.ID, mem.ID, msgs[0].MessageID, false); err == nil {
		t.Fatal("tenant B must not read tenant A's memory message content")
	}
	hits, err := svc.SearchMemoryMessages(ctx, ta.ID, mem.ID, "q", 0.2, 0.7, 5, false)
	if err != nil {
		t.Fatalf("owner search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("owner search expected 1 hit, got %d", len(hits))
	}
	if _, err := svc.GetMemoryMessageContent(ctx, ta.ID, mem.ID, hits[0].MessageID, false); err != nil {
		t.Fatalf("owner content: %v", err)
	}
	if err := svc.DeleteMemoryMessage(ctx, ta.ID, mem.ID, msgs[0].MessageID, false); err != nil {
		t.Fatalf("owner forget message: %v", err)
	}
	if err := svc.UpdateMemoryMessageStatus(ctx, ta.ID, mem.ID, 999, true, false); err == nil {
		t.Fatal("status update on missing message should error")
	}

	_, total, err = svc.ListMemories(ctx, "any", true, repository.MemoryFilter{}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total < 1 {
		t.Fatalf("scopeAll should include the memory, total=%d", total)
	}
	if _, err := svc.GetMemory(ctx, tb.ID, mem.ID, true); err != nil {
		t.Fatalf("scopeAll read should succeed: %v", err)
	}
}

// TestMemoryDeleteCascade verifies deleting a memory also removes its shadow and
// makes its messages and config inaccessible (cascade cleanup).
func TestMemoryDeleteCascade(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, err := svc.CreateTenant(ctx, "TenantA")
	if err != nil {
		t.Fatal(err)
	}
	mem, err := svc.CreateMemory(ctx, ta.ID, "mem", []string{"raw"}, "e", "l")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.AddMemoryMessage(ctx, ta.ID, mem.ID, "a", "s", "q", "a", false); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteMemory(ctx, ta.ID, mem.ID, false); err != nil {
		t.Fatalf("delete memory: %v", err)
	}
	if _, err := svc.GetMemory(ctx, ta.ID, mem.ID, false); err == nil {
		t.Fatal("memory should be gone after delete")
	}
	if _, err := svc.GetMemoryConfig(ctx, ta.ID, mem.ID, false); err == nil {
		t.Fatal("memory config should be gone after delete")
	}
	if _, err := svc.ListMemoryMessages(ctx, ta.ID, mem.ID, false); err == nil {
		t.Fatal("messages should be gone after delete")
	}
	items, total, err := svc.ListMemories(ctx, ta.ID, false, repository.MemoryFilter{}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(items) != 0 {
		t.Fatalf("shadow not cleaned: total=%d", total)
	}
}
