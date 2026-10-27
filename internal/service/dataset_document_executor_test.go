package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func TestApprovalDocumentAndChunkExecutorsUseTargetTenant(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "document-target")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.Store.GetUserByUsername(ctx, "admin")
	if err != nil || admin == nil {
		t.Fatalf("admin not found: %+v err=%v", admin, err)
	}
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "approved docs")
	if err != nil {
		t.Fatal(err)
	}
	document, err := svc.UploadDocument(ctx, tenant.ID, dataset.ID, admin.ID, "policy.md", []byte("target tenant content"))
	if err != nil {
		t.Fatal(err)
	}

	approval := func(action, objectID, payload string) *model.Approval {
		return &model.Approval{
			ID: id.New(), TenantID: tenant.ID, TargetTenantID: tenant.ID,
			RequesterID: admin.ID, ObjectType: model.ApprovalObjectDocument,
			Action: action, ObjectID: objectID, PayloadJSON: payload,
			CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}
	}
	metadataPayload, err := json.Marshal(map[string]any{
		"dataset_id": dataset.ID, "document_id": document.ID,
		"metadata": map[string]any{"source": "governance"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.executeDocumentAction(ctx, nil, approval(model.ApprovalActionUpdate, document.ID, string(metadataPayload))); err != nil {
		t.Fatal(err)
	}
	simplePayload, err := json.Marshal(map[string]any{"dataset_id": dataset.ID, "document_id": document.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{model.ApprovalActionParse, model.ApprovalActionStop} {
		if _, err := svc.executeDocumentAction(ctx, nil, approval(action, document.ID, string(simplePayload))); err != nil {
			t.Fatal(err)
		}
	}
	enabledPayload, err := json.Marshal(map[string]any{"dataset_id": dataset.ID, "document_id": document.ID, "enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.executeDocumentAction(ctx, nil, approval(model.ApprovalActionEnable, document.ID, string(enabledPayload))); err != nil {
		t.Fatal(err)
	}
	chunkPayload, err := json.Marshal(map[string]any{
		"dataset_id": dataset.ID, "document_id": document.ID, "chunk_id": "chunk-1", "enabled": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	chunkApproval := approval(model.ApprovalActionDisable, "chunk-1", string(chunkPayload))
	chunkApproval.ObjectType = model.ApprovalObjectDocumentChunk
	if _, err := svc.executeDocumentChunkAction(ctx, nil, chunkApproval); err != nil {
		t.Fatal(err)
	}
	deleteChunkApproval := approval(model.ApprovalActionDelete, "chunk-1", string(chunkPayload))
	deleteChunkApproval.ObjectType = model.ApprovalObjectDocumentChunk
	if _, err := svc.executeDocumentChunkAction(ctx, nil, deleteChunkApproval); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.executeDocumentAction(ctx, nil, approval(model.ApprovalActionDelete, document.ID, string(simplePayload))); err != nil {
		t.Fatal(err)
	}
	docs, err := svc.ListDocuments(ctx, tenant.ID, dataset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("document should be deleted: %+v", docs)
	}
}
