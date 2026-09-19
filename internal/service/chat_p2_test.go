package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func TestChatFiltersAndBatchStatus(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	ta, _ := svc.CreateTenant(ctx, "TenantA")
	c1, _ := svc.CreateChat(ctx, ta.ID, "kb-a", []string{"d1"})
	c2, _ := svc.CreateChat(ctx, ta.ID, "kb-b", []string{"d2"})
	c1.OwnerID = "u-owner"
	c2.OwnerID = "u-other"
	if err := svc.Store.UpsertChatShadow(ctx, c1); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.UpsertChatShadow(ctx, c2); err != nil {
		t.Fatal(err)
	}
	_ = svc.Store.IncChatMessageCount(ctx, c1.ID)
	_ = svc.Store.IncChatMessageCount(ctx, c1.ID)

	byName, total, err := svc.ListChats(ctx, ta.ID, false, repository.ChatFilter{Name: "kb-a"}, 1, 20)
	if err != nil || total != 1 || byName[0].ID != c1.ID {
		t.Fatalf("name filter: total=%d err=%v", total, err)
	}
	byOwner, total, err := svc.ListChats(ctx, ta.ID, false, repository.ChatFilter{OwnerID: "u-owner"}, 1, 20)
	if err != nil || total != 1 || byOwner[0].ID != c1.ID {
		t.Fatalf("owner filter: total=%d err=%v", total, err)
	}
	byMsgs, total, err := svc.ListChats(ctx, ta.ID, false, repository.ChatFilter{MinMessages: 2}, 1, 20)
	if err != nil || total != 1 || byMsgs[0].ID != c1.ID {
		t.Fatalf("min messages filter: total=%d err=%v", total, err)
	}

	if err := svc.BatchUpdateChatStatus(ctx, ta.ID, false, []string{c1.ID}, model.TenantStatusDisabled); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.GetChat(ctx, ta.ID, c1.ID, false)
	if got.Status != model.TenantStatusDisabled {
		t.Fatalf("batch status not applied: %s", got.Status)
	}
	byStatus, total, err := svc.ListChats(ctx, ta.ID, false, repository.ChatFilter{Status: model.TenantStatusDisabled}, 1, 20)
	if err != nil || total != 1 || byStatus[0].ID != c1.ID {
		t.Fatalf("status filter: total=%d err=%v", total, err)
	}
}

func TestChatUsagePerChat(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	svc.RAGFlow = ragflow.NewMock()
	ta, _ := svc.CreateTenant(ctx, "TenantA")
	chat, _ := svc.CreateChat(ctx, ta.ID, "kb", []string{"d1"})
	key := &model.APIKey{TenantID: ta.ID, UserID: "u1"}
	if _, err := svc.ChatAppCompletion(ctx, key, chat.ID, "", []byte(`{"messages":[{"role":"user","content":"hi"}]}`), "req1"); err != nil {
		t.Fatal(err)
	}
	agg, err := svc.ChatUsage(ctx, ta.ID, chat.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if agg.TokensIn != 10 || agg.Requests != 1 {
		t.Fatalf("unexpected chat usage: %+v", agg)
	}
	got, _ := svc.GetChat(ctx, ta.ID, chat.ID, false)
	if got.MessageCount != 1 {
		t.Fatalf("message count not bumped: %d", got.MessageCount)
	}
}
