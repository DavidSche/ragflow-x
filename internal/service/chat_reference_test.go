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
	m.SeedDocumentChunkWithImage(ds.ID, doc.ID, "chunkA", "chunk text", "imageA", true)

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

func TestChatImageRequiresChunkImageBinding(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	m := ragflow.NewMock()
	svc.RAGFlow = m
	tenant, _ := svc.CreateTenant(ctx, "Image Tenant")
	dataset, err := m.CreateDataset(ctx, ragflow.CreateDatasetRequest{Name: "image-kb"})
	if err != nil {
		t.Fatal(err)
	}
	document, err := m.CreateDocument(ctx, dataset.ID, &ragflow.DocumentUpload{Name: "chart.pdf", Content: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.CreateDatasetLink(ctx, &model.DatasetLink{ID: "image-link", TenantID: tenant.ID, RAGFlowDatasetID: dataset.ID, Name: "image-kb"}); err != nil {
		t.Fatal(err)
	}
	m.SeedDocumentChunkWithImage(dataset.ID, document.ID, "chunk-1", "chunk text", "image-1", true)

	data, _, err := svc.ChatImage(ctx, tenant.ID, dataset.ID, document.ID, "chunk-1", "image-1")
	if err != nil || len(data) == 0 {
		t.Fatalf("authorized chunk image: data=%d err=%v", len(data), err)
	}
	if _, _, err := svc.ChatImage(ctx, tenant.ID, dataset.ID, document.ID, "chunk-1", "forged-image"); err == nil {
		t.Fatal("chunk image mismatch must be rejected")
	}
}
