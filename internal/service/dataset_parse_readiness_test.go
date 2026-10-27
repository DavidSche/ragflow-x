package service

import (
	"context"
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

type parseReadinessClient struct {
	*ragflow.Mock
	configs map[string]ragflow.DatasetConfig
	models  []ragflow.AddedModel
	updates []ragflow.DatasetConfigUpdate
}

func (c *parseReadinessClient) Name() string { return "http" }

func (c *parseReadinessClient) GetDatasetConfig(_ context.Context, datasetID string) (*ragflow.DatasetConfig, error) {
	config := c.configs[datasetID]
	return &config, nil
}

func (c *parseReadinessClient) ListModels(_ context.Context, modelType string) ([]ragflow.AddedModel, error) {
	if modelType != "embedding" {
		return nil, nil
	}
	return append([]ragflow.AddedModel(nil), c.models...), nil
}

func (c *parseReadinessClient) UpdateDatasetConfig(_ context.Context, _ string, update ragflow.DatasetConfigUpdate) error {
	c.updates = append(c.updates, update)
	return nil
}

func TestUploadDocumentRepairsLegacyEmbeddingReferenceBeforeParse(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Parse Readiness Tenant")
	if err != nil {
		t.Fatal(err)
	}
	baseMock := ragflow.NewMock()
	svc.RAGFlow = baseMock
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "Readiness Docs")
	if err != nil {
		t.Fatal(err)
	}
	engineDatasetID := dataset.RAGFlowDatasetID
	client := &parseReadinessClient{
		Mock: baseMock,
		configs: map[string]ragflow.DatasetConfig{
			engineDatasetID: {ID: engineDatasetID, EmbeddingModel: "qwen3-embedding-0.6b@deepseek-v4-flash-0731@OpenAI-API-Compatible"},
		},
		models: []ragflow.AddedModel{{
			ModelID:  "qwen3-embedding-0.6b@gpustack-local@OpenAI-API-Compatible",
			Name:     "qwen3-embedding-0.6b",
			Type:     []string{"embedding"},
			Provider: "OpenAI-API-Compatible",
			Instance: "gpustack-local",
		}},
	}
	svc.RAGFlow = client

	document, err := svc.UploadDocument(ctx, tenant.ID, dataset.ID, "admin", "handbook.md", []byte("# Readiness"))
	if err != nil {
		t.Fatalf("upload should repair readiness before parse: %v", err)
	}
	if len(client.updates) != 1 || client.updates[0].EmbeddingModel == nil || *client.updates[0].EmbeddingModel != client.models[0].ModelID {
		t.Fatalf("expected canonical embedding repair, updates=%+v", client.updates)
	}
	if document.ID == "" {
		t.Fatalf("unexpected document: %+v", document)
	}
}

func TestUploadDocumentRejectsAmbiguousEmbeddingReference(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Ambiguous Embedding Tenant")
	if err != nil {
		t.Fatal(err)
	}
	baseMock := ragflow.NewMock()
	svc.RAGFlow = baseMock
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "Ambiguous Docs")
	if err != nil {
		t.Fatal(err)
	}
	svc.RAGFlow = &parseReadinessClient{
		Mock: baseMock,
		configs: map[string]ragflow.DatasetConfig{
			dataset.RAGFlowDatasetID: {ID: dataset.RAGFlowDatasetID, EmbeddingModel: "qwen3-embedding-0.6b@missing@OpenAI-API-Compatible"},
		},
		models: []ragflow.AddedModel{
			{ModelID: "embedding-a", Name: "qwen3-embedding-0.6b", Type: []string{"embedding"}},
			{ModelID: "embedding-b", Name: "qwen3-embedding-0.6b", Type: []string{"embedding"}},
		},
	}
	_, err = svc.UploadDocument(ctx, tenant.ID, dataset.ID, "admin", "blocked.md", []byte("blocked"))
	if err == nil {
		t.Fatal("expected ambiguous embedding reference to block upload")
	}
	var apiErr *httperr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != 40299 {
		t.Fatalf("expected 40299 readiness error, got %v", err)
	}
}

func TestResolveDatasetEmbeddingReference(t *testing.T) {
	models := []ragflow.AddedModel{
		{ModelID: "embedding-a", Name: "qwen3-embedding-0.6b", Type: []string{"embedding"}},
	}
	if resolved, ok := resolveDatasetEmbeddingReference(models, "qwen3-embedding-0.6b@missing@provider"); !ok || resolved.ModelID != "embedding-a" {
		t.Fatalf("legacy reference did not resolve: %+v %v", resolved, ok)
	}
	if _, ok := resolveDatasetEmbeddingReference(models, ""); ok {
		t.Fatal("empty reference must not resolve")
	}
}

func TestUpdateDatasetConfigCanonicalizesEmbeddingModel(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Dataset Embedding Update Tenant")
	if err != nil {
		t.Fatal(err)
	}
	baseMock := ragflow.NewMock()
	svc.RAGFlow = baseMock
	dataset, err := svc.CreateDataset(ctx, tenant.ID, "Embedding Update Docs")
	if err != nil {
		t.Fatal(err)
	}
	client := &parseReadinessClient{
		Mock: baseMock,
		models: []ragflow.AddedModel{{
			ModelID: "qwen3-embedding-0.6b@gpustack-local@OpenAI-API-Compatible",
			Name:    "qwen3-embedding-0.6b",
			Type:    []string{"embedding"},
		}},
	}
	svc.RAGFlow = client
	model := "qwen3-embedding-0.6b"
	if err := svc.UpdateDatasetConfig(ctx, tenant.ID, dataset.ID, ragflow.DatasetConfigUpdate{EmbeddingModel: &model}); err != nil {
		t.Fatal(err)
	}
	if len(client.updates) != 1 || client.updates[0].EmbeddingModel == nil || *client.updates[0].EmbeddingModel != client.models[0].ModelID {
		t.Fatalf("expected canonical model_id, updates=%+v", client.updates)
	}
	unknown := "missing-model"
	if err := svc.UpdateDatasetConfig(ctx, tenant.ID, dataset.ID, ragflow.DatasetConfigUpdate{EmbeddingModel: &unknown}); err == nil {
		t.Fatal("expected unknown embedding model to fail")
	}
}
