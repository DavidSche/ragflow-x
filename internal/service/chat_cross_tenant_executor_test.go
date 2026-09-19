package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func TestApprovalChatExecutorsUseTargetTenant(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "chat-target")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("admin not found: %+v err=%v", admin, err)
	}
	approval := func(action, objectID, payload string, snapshot map[string]any) *model.Approval {
		t.Helper()
		snapshotJSON := "{}"
		if snapshot != nil {
			raw, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			snapshotJSON = string(raw)
		}
		return &model.Approval{
			ID: id.New(), TenantID: tenant.ID, TargetTenantID: tenant.ID,
			RequesterID: admin.ID, ObjectType: model.ApprovalObjectChat,
			Action: action, ObjectID: objectID, PayloadJSON: payload,
			SnapshotJSON: snapshotJSON, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
	}

	createPayload, err := json.Marshal(map[string]any{
		"name": "approved chat", "dataset_ids": []string{},
		"authoring": map[string]any{"llm_id": "mock-llm"},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.executeChatCreate(ctx, nil, approval(model.ApprovalActionCreate, "new:approved chat", string(createPayload), nil))
	if err != nil {
		t.Fatal(err)
	}
	chat, err := svc.Store.GetChatShadow(ctx, tenant.ID, created["chat_id"].(string), false)
	if err != nil || chat == nil {
		t.Fatalf("created chat missing: %+v err=%v", chat, err)
	}
	if chat.OwnerID != admin.ID {
		t.Fatalf("chat owner mismatch: got %s", chat.OwnerID)
	}

	updatePayload, err := json.Marshal(map[string]any{
		"name": "approved chat v2", "dataset_ids": []string{},
		"authoring": map[string]any{"llm_id": "mock-llm"},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := map[string]any{
		"name": chat.Name, "status": chat.Status, "dataset_ids": chat.DatasetIDs,
		"owner_id": chat.OwnerID, "chat_updated_at": chat.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if err := validateApprovalChatAction(ctx, svc, approval(model.ApprovalActionUpdate, chat.ID, string(updatePayload), snapshot)); err != nil {
		t.Fatal(err)
	}
	updated, err := svc.executeChatUpdate(ctx, nil, approval(model.ApprovalActionUpdate, chat.ID, string(updatePayload), snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if updated["name"] != "approved chat v2" {
		t.Fatalf("update result mismatch: %+v", updated)
	}
	updatedChat, err := svc.Store.GetChatShadow(ctx, tenant.ID, chat.ID, false)
	if err != nil || updatedChat == nil {
		t.Fatalf("updated chat missing: %+v err=%v", updatedChat, err)
	}
	snapshot["chat_updated_at"] = updatedChat.UpdatedAt.UTC().Format(time.RFC3339Nano)
	if _, err := svc.executeChatDelete(ctx, nil, approval(model.ApprovalActionDelete, chat.ID, "{}", snapshot)); err != nil {
		t.Fatal(err)
	}
	deleted, err := svc.Store.GetChatShadow(ctx, tenant.ID, chat.ID, false)
	if err != nil || deleted != nil {
		t.Fatalf("deleted chat should not exist: %+v err=%v", deleted, err)
	}
}
