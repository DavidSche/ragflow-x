package service

import (
	"context"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// resolveDatasetEmbeddingReference resolves the RAGFlow engine's configured
// embedding reference against models actually registered for the tenant.
// Legacy dataset configs may contain a display name or a stale
// model@instance@provider value; when the model name resolves to exactly one
// registered model we return its canonical model_id.
func resolveDatasetEmbeddingReference(models []ragflow.AddedModel, reference string) (ragflow.AddedModel, bool) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return ragflow.AddedModel{}, false
	}
	for _, candidate := range models {
		if candidate.ModelID == reference {
			return candidate, true
		}
	}
	nameMatches := make([]ragflow.AddedModel, 0, 1)
	for _, candidate := range models {
		if candidate.Name == reference {
			nameMatches = append(nameMatches, candidate)
		}
	}
	if len(nameMatches) == 1 {
		return nameMatches[0], true
	}
	if !strings.Contains(reference, "@") {
		return ragflow.AddedModel{}, false
	}
	baseName := strings.Split(reference, "@")[0]
	if strings.TrimSpace(baseName) == "" {
		return ragflow.AddedModel{}, false
	}
	matches := make([]ragflow.AddedModel, 0, 1)
	for _, candidate := range models {
		if candidate.Name == baseName {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		return ragflow.AddedModel{}, false
	}
	return matches[0], true
}

// prepareDatasetParseReadiness validates and repairs the embedding reference
// before any document is uploaded or submitted to the engine parser. This keeps
// provider/model drift from becoming an unrecoverable upstream parse failure.
func (s *Service) prepareDatasetParseReadiness(ctx context.Context, dataset *model.DatasetLink) error {
	if s.RAGFlow.Name() == "mock" {
		return nil
	}
	config, err := s.RAGFlow.GetDatasetConfig(ctx, dataset.RAGFlowDatasetID)
	if err != nil {
		return httperr.New(502, 50296, "verify dataset parse readiness failed")
	}
	models, err := s.RAGFlow.ListModels(ctx, "embedding")
	if err != nil {
		return httperr.New(502, 50297, "list embedding models failed")
	}
	if resolved, ok := resolveDatasetEmbeddingReference(models, config.EmbeddingModel); ok {
		if resolved.ModelID == config.EmbeddingModel {
			return nil
		}
		canonical := resolved.ModelID
		if err := s.RAGFlow.UpdateDatasetConfig(ctx, dataset.RAGFlowDatasetID, ragflow.DatasetConfigUpdate{
			EmbeddingModel: &canonical,
		}); err != nil {
			return httperr.New(502, 50298, "repair dataset embedding model failed")
		}
		return nil
	}
	if config.EmbeddingModel == "" {
		defaults, err := s.RAGFlow.ListDefaultModels(ctx)
		if err == nil {
			for _, candidate := range defaults {
				if candidate.Type != "embedding" {
					continue
				}
				if _, ok := resolveDatasetEmbeddingReference(models, candidate.ModelID); ok {
					return nil
				}
			}
		}
		if len(models) == 1 {
			canonical := models[0].ModelID
			if err := s.RAGFlow.UpdateDatasetConfig(ctx, dataset.RAGFlowDatasetID, ragflow.DatasetConfigUpdate{
				EmbeddingModel: &canonical,
			}); err != nil {
				return httperr.New(502, 50298, "select dataset embedding model failed")
			}
			return nil
		}
	}
	return httperr.New(409, 40299, "dataset embedding model is unavailable; configure a registered embedding model before upload")
}

// canonicalizeDatasetEmbeddingUpdate rejects unknown or ambiguous model
// references before RAGFlow accepts a dataset configuration change.
func (s *Service) canonicalizeDatasetEmbeddingUpdate(ctx context.Context, cfg *ragflow.DatasetConfigUpdate) error {
	if s.RAGFlow.Name() == "mock" || cfg == nil || cfg.EmbeddingModel == nil || strings.TrimSpace(*cfg.EmbeddingModel) == "" {
		return nil
	}
	models, err := s.RAGFlow.ListModels(ctx, "embedding")
	if err != nil {
		return httperr.New(502, 50297, "list embedding models failed")
	}
	resolved, ok := resolveDatasetEmbeddingReference(models, *cfg.EmbeddingModel)
	if !ok {
		return httperr.New(409, 40299, "dataset embedding model is unavailable; select a registered embedding model")
	}
	cfg.EmbeddingModel = &resolved.ModelID
	return nil
}
