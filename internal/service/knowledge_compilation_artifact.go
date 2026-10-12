package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func (s *Service) checkCompilationSourceDuringExecution(
	ctx context.Context, run *model.CompilationRun, dataset *model.DatasetLink,
) error {
	documentIDs, err := decodeCompilationRunDocumentIDs(run.DocumentIDs)
	if err != nil {
		return err
	}
	documents, err := s.RAGFlow.ListDocuments(ctx, dataset.RAGFlowDatasetID)
	if err != nil {
		return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "source_manifest_probe_failed", map[string]interface{}{
			"source_consistency": model.CompilationSourceConsistencyUnknown,
		}, time.Now().UTC(), nil)
	}
	selected, err := selectCompilationDocuments(documents, documentIDs)
	if err != nil {
		if markErr := s.markCompilationArtifactsStale(ctx, run); markErr != nil {
			return markErr
		}
		return s.storeCompilationAudit(ctx, run, model.CompilationRunFailed, "source_manifest_changed_during_execution", map[string]interface{}{
			"source_consistency": model.CompilationSourceConsistencyChanged,
		}, time.Now().UTC(), nil)
	}
	currentManifest := compilationDocumentManifestHash(selected)
	if currentManifest != run.ManifestHash {
		if markErr := s.markCompilationArtifactsStale(ctx, run); markErr != nil {
			return markErr
		}
		return s.storeCompilationAudit(ctx, run, model.CompilationRunFailed, "source_manifest_changed_during_execution", map[string]interface{}{
			"source_consistency":            model.CompilationSourceConsistencyChanged,
			"post_run_source_manifest_hash": currentManifest,
		}, time.Now().UTC(), nil)
	}
	return nil
}

func (s *Service) verifyCompilationPostRunSource(
	ctx context.Context, run *model.CompilationRun, dataset *model.DatasetLink,
) error {
	now := time.Now().UTC()
	documentIDs, err := decodeCompilationRunDocumentIDs(run.DocumentIDs)
	if err != nil {
		return err
	}
	documents, err := s.RAGFlow.ListDocuments(ctx, dataset.RAGFlowDatasetID)
	if err != nil {
		return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "post_run_source_probe_failed", map[string]interface{}{
			"source_consistency": model.CompilationSourceConsistencyUnknown,
		}, now, nil)
	}
	selected, err := selectCompilationDocuments(documents, documentIDs)
	if err != nil {
		if markErr := s.markCompilationArtifactsStale(ctx, run); markErr != nil {
			return markErr
		}
		return s.storeCompilationAudit(ctx, run, model.CompilationRunFailed, "post_run_source_manifest_invalid", map[string]interface{}{
			"source_consistency": model.CompilationSourceConsistencyChanged,
		}, now, nil)
	}
	currentManifest := compilationDocumentManifestHash(selected)
	if currentManifest != run.ManifestHash {
		if markErr := s.markCompilationArtifactsStale(ctx, run); markErr != nil {
			return markErr
		}
		return s.storeCompilationAudit(ctx, run, model.CompilationRunFailed, "post_run_source_manifest_changed", map[string]interface{}{
			"source_consistency":            model.CompilationSourceConsistencyChanged,
			"post_run_source_manifest_hash": currentManifest,
		}, now, nil)
	}
	if !compilationManifestComplete(selected) {
		return s.storeCompilationAudit(ctx, run, model.CompilationRunUnknown, "post_run_source_manifest_incomplete", map[string]interface{}{
			"source_consistency": model.CompilationSourceConsistencyUnverified,
		}, now, nil)
	}
	auditErr := s.storeCompilationAudit(ctx, run, run.State, "post_run_source_manifest_consistent", map[string]interface{}{
		"source_consistency":            model.CompilationSourceConsistencyConsistent,
		"post_run_source_manifest_hash": currentManifest,
		"downstream_source_snapshot_id": "",
	}, now, nil)
	if auditErr != nil {
		return auditErr
	}
	return auditErr
}

func (s *Service) markCompilationArtifactsStale(ctx context.Context, run *model.CompilationRun) error {
	for page := 1; ; page++ {
		projections, total, err := s.Store.ListArtifactProjections(ctx, run.TenantID, run.DatasetID, run.ID, page, 100)
		if err != nil {
			return err
		}
		for index := range projections {
			projection := &projections[index]
			projection.State = model.ArtifactProjectionStale
			projection.UpdatedAt = time.Now().UTC()
			if err := s.Store.UpsertArtifactProjection(ctx, projection); err != nil {
				return err
			}
		}
		if int64(page)*100 >= total {
			return nil
		}
	}
}

func artifactProjectionFromRun(
	run *model.CompilationRun, artifact ragflow.DatasetArtifact, detail *ragflow.DatasetArtifactDetail,
) (*model.ArtifactProjection, error) {
	now := time.Now().UTC()
	contentHash := ""
	snapshot := map[string]interface{}{
		"artifact_id": artifact.Slug, "title": artifact.Title, "page_type": artifact.PageType,
		"run_id": run.ID, "kind": run.Kind, "state": model.ArtifactProjectionUnverified,
		"hash_semantics": "projection_metadata_v1", "content_hash_semantics": "",
		"upstream_content_hash": false, "upstream_run_id": false,
		"run_binding": false, "content_hash_binding": false, "evidence_state": "unverified",
		"matched_document_ids": []string{},
	}
	state := model.ArtifactProjectionUnverified
	if detail != nil {
		if detail.Slug != artifact.Slug || detail.PageType != artifact.PageType {
			return nil, httperr.Internal("artifact detail identity mismatch")
		}
		documentIDs, err := decodeCompilationRunDocumentIDs(run.DocumentIDs)
		if err != nil {
			return nil, err
		}
		matchedDocumentIDs := make([]string, 0, len(documentIDs))
		sourceDocuments := make(map[string]struct{}, len(detail.SourceDocIDs))
		for _, documentID := range detail.SourceDocIDs {
			sourceDocuments[documentID] = struct{}{}
		}
		for _, documentID := range documentIDs {
			if _, exists := sourceDocuments[documentID]; exists {
				matchedDocumentIDs = append(matchedDocumentIDs, documentID)
			}
		}
		snapshot["source_document_ids"] = detail.SourceDocIDs
		snapshot["source_chunk_ids"] = detail.SourceChunkIDs
		snapshot["matched_document_ids"] = matchedDocumentIDs
		if len(matchedDocumentIDs) > 0 && strings.TrimSpace(detail.ContentMDRendered) != "" {
			payload := map[string]interface{}{
				"algorithm":            "artifact_payload_sha256_v1",
				"content_md_rendered":  detail.ContentMDRendered,
				"source_chunk_ids":     detail.SourceChunkIDs,
				"source_doc_ids":       detail.SourceDocIDs,
				"matched_document_ids": matchedDocumentIDs,
				"artifact_id":          artifact.Slug,
				"page_type":            artifact.PageType,
				"run_id":               run.ID,
			}
			var hashErr error
			contentHash, hashErr = hashStableJSON(payload)
			if hashErr != nil {
				return nil, httperr.Internal("encode artifact content hash failed")
			}
			snapshot["content_hash"] = contentHash
			snapshot["content_hash_semantics"] = "artifact_payload_sha256_v1"
			snapshot["content_hash_binding"] = true
			snapshot["run_binding"] = true
			snapshot["evidence_state"] = "payload_hash_verified"
			state = model.ArtifactProjectionReady
		}
	}
	snapshot["state"] = state
	artifactHash, err := hashStableJSON(snapshot)
	if err != nil {
		return nil, httperr.Internal("encode artifact projection failed")
	}
	snapshot["artifact_hash"] = artifactHash
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, httperr.Internal("encode artifact projection failed")
	}
	return &model.ArtifactProjection{
		ID: id.New(), TenantID: run.TenantID, DatasetID: run.DatasetID, RunID: run.ID,
		ArtifactID: artifact.Slug, Kind: run.Kind, State: state,
		ArtifactHash: artifactHash, ArtifactJSON: string(encoded),
		ContentHash: contentHash, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func artifactProjectionFromRetrievalChunk(
	run *model.CompilationRun, retrievalChunk map[string]interface{},
) (*model.ArtifactProjection, error) {
	artifactID := stringFromRetrievalChunk(retrievalChunk, "id")
	content, _ := retrievalChunk["content"].(string)
	if artifactID == "" || strings.TrimSpace(content) == "" {
		return nil, nil
	}
	var payload map[string]interface{}
	if json.Unmarshal([]byte(content), &payload) != nil {
		return nil, nil
	}
	payloadType, _ := payload["type"].(string)
	sourceChunkIDs := stringSliceFromRetrievalChunk(retrievalChunk, payload, "source_chunk_ids")
	if payloadType == "" || len(sourceChunkIDs) == 0 {
		return nil, nil
	}
	documentID := stringFromRetrievalChunk(retrievalChunk, "document_id")
	if documentID == "" {
		documentID = stringFromRetrievalChunk(retrievalChunk, "doc_id")
	}
	sourceDocIDs := []string{}
	if documentID != "" {
		sourceDocIDs = append(sourceDocIDs, documentID)
	}
	sourceDocIDs = append(sourceDocIDs, stringSliceFromRetrievalChunk(retrievalChunk, payload, "source_doc_ids")...)
	documentIDs, err := decodeCompilationRunDocumentIDs(run.DocumentIDs)
	if err != nil {
		return nil, err
	}
	sourceDocumentSet := make(map[string]struct{}, len(sourceDocIDs))
	for _, sourceDocID := range sourceDocIDs {
		sourceDocumentSet[sourceDocID] = struct{}{}
	}
	matchedDocumentIDs := make([]string, 0, len(documentIDs))
	for _, documentID := range documentIDs {
		if _, exists := sourceDocumentSet[documentID]; exists {
			matchedDocumentIDs = append(matchedDocumentIDs, documentID)
		}
	}
	if len(matchedDocumentIDs) == 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	snapshot := map[string]interface{}{
		"artifact_id": artifactID, "title": payload["name"], "page_type": run.Kind,
		"run_id": run.ID, "kind": run.Kind, "state": model.ArtifactProjectionReady,
		"hash_semantics": "projection_metadata_v1", "content_hash_semantics": "artifact_payload_sha256_v1",
		"upstream_content_hash": false, "upstream_run_id": false,
		"run_binding": true, "content_hash_binding": true, "evidence_state": "payload_hash_verified",
		"evidence_source": "ragflow_compilation_retrieval", "source_document_ids": sourceDocIDs,
		"source_chunk_ids": sourceChunkIDs, "matched_document_ids": matchedDocumentIDs,
	}
	payloadForHash := map[string]interface{}{
		"algorithm":            "artifact_payload_sha256_v1",
		"content_md_rendered":  content,
		"source_chunk_ids":     sourceChunkIDs,
		"source_doc_ids":       sourceDocIDs,
		"matched_document_ids": matchedDocumentIDs,
		"artifact_id":          artifactID,
		"page_type":            run.Kind,
		"run_id":               run.ID,
	}
	contentHash, err := hashStableJSON(payloadForHash)
	if err != nil {
		return nil, httperr.Internal("encode artifact content hash failed")
	}
	snapshot["content_hash"] = contentHash
	artifactHash, err := hashStableJSON(snapshot)
	if err != nil {
		return nil, httperr.Internal("encode artifact projection failed")
	}
	snapshot["artifact_hash"] = artifactHash
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, httperr.Internal("encode artifact projection failed")
	}
	return &model.ArtifactProjection{
		ID: id.New(), TenantID: run.TenantID, DatasetID: run.DatasetID, RunID: run.ID,
		ArtifactID: artifactID, Kind: run.Kind, State: model.ArtifactProjectionReady,
		ArtifactHash: artifactHash, ArtifactJSON: string(encoded),
		ContentHash: contentHash, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func stringFromRetrievalChunk(chunk map[string]interface{}, key string) string {
	switch value := chunk[key].(type) {
	case string:
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func stringSliceFromRetrievalChunk(
	chunk map[string]interface{}, payload map[string]interface{}, key string,
) []string {
	source := payload
	if source == nil {
		source = chunk
	}
	switch value := source[key].(type) {
	case []string:
		return append([]string(nil), value...)
	case []interface{}:
		values := make([]string, 0, len(value))
		for _, item := range value {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				values = append(values, strings.TrimSpace(text))
			}
		}
		return values
	default:
		return []string{}
	}
}
