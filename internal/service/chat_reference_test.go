package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestGetChatChunkOwnershipAndFetch(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	m := ragflow.NewMock()
	svc.RAGFlow = m
	ta, _ := svc.CreateTenant(ctx, "TenantA")
	tb, _ := svc.CreateTenant(ctx, "TenantB")

	ds, err := m.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "kb"})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := m.CreateDocument(ctx, ds.ID, &ragflow.DocumentUpload{Name: "doc.pdf", Content: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{ID: "link1", TenantID: ta.ID, RAGFlowDatasetID: ds.ID, Name: "kb"}); err != nil {
		t.Fatal(err)
	}

	ck, err := svc.GetChatChunk(ctx, ta.ID, ds.ID, doc.ID, "chunkA", false)
	if err != nil {
		t.Fatalf("owner fetch chunk: %v", err)
	}
	if ck.Content == "" || ck.ID != "chunkA" {
		t.Fatalf("unexpected chunk: %+v", ck)
	}

	if _, err := svc.GetChatChunk(ctx, tb.ID, ds.ID, doc.ID, "chunkA", false); err == nil {
		t.Fatal("tenant B must not read tenant A chunk")
	}
	if _, err := svc.GetChatChunk(ctx, ta.ID, "unknown-ds", doc.ID, "chunkA", false); err == nil {
		t.Fatal("unknown dataset must be rejected")
	}
}
