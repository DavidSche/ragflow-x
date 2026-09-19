package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// GetChatChunk fetches a single source chunk referenced by a chat answer.
// The dataset is resolved by its RAGFlow id and must belong to the caller's
// tenant (unless a platform admin passes scopeAll), so cross-tenant references
// cannot be read.
func (s *Service) GetChatChunk(
	ctx context.Context, tenantID, ragflowDatasetID, docID, chunkID string, scopeAll bool,
) (*ragflow.Chunk, error) {
	if ragflowDatasetID == "" || docID == "" || chunkID == "" {
		return nil, httperr.BadRequest(40093, "dataset, doc and chunk are required")
	}
	link, err := s.Store.GetRAGFlowDatasetLinkForScope(ctx, scopeAll, []string{tenantID}, ragflowDatasetID)
	if err != nil {
		return nil, err
	}
	if link == nil {
		return nil, httperr.NotFound("dataset not found")
	}
	ck, err := s.RAGFlow.GetChunk(ctx, ragflowDatasetID, docID, chunkID)
	if err != nil {
		return nil, httperr.New(502, 50249, "ragflow get chunk failed")
	}
	return ck, nil
}

// ChatDocumentChunks lists the parsed chunks of a document referenced by a chat,
// proxying RAGFlow directly (the API key scopes to the owning tenant) so the
// "view full document" popup works for RAGFlow-native datasets too.
func (s *Service) ChatDocumentChunks(
	ctx context.Context, tenantID, ragflowDatasetID, docID string, page, pageSize int,
) ([]ragflow.Chunk, int64, error) {
	if ragflowDatasetID == "" || docID == "" {
		return nil, 0, httperr.BadRequest(40094, "dataset and doc are required")
	}
	link, err := s.Store.GetRAGFlowDatasetLinkForScope(ctx, false, []string{tenantID}, ragflowDatasetID)
	if err != nil {
		return nil, 0, err
	}
	if link == nil {
		return nil, 0, httperr.NotFound("dataset not found")
	}
	if err := s.assertRAGFlowDocument(ctx, ragflowDatasetID, docID); err != nil {
		return nil, 0, err
	}
	chunks, total, err := s.RAGFlow.ListDocumentChunks(ctx, ragflowDatasetID, docID, page, pageSize)
	if err != nil {
		return nil, 0, httperr.New(502, 50253, "ragflow list document chunks failed")
	}
	return chunks, total, nil
}

// ChatDocumentPreview streams the original file of a document referenced by a chat,
// proxying RAGFlow directly so PDFs/images with charts render in the viewer.
func (s *Service) ChatDocumentPreview(
	ctx context.Context, tenantID, ragflowDatasetID, documentID string,
) ([]byte, string, error) {
	if ragflowDatasetID == "" || documentID == "" {
		return nil, "", httperr.BadRequest(40095, "dataset and doc are required")
	}
	link, err := s.Store.GetRAGFlowDatasetLinkForScope(ctx, false, []string{tenantID}, ragflowDatasetID)
	if err != nil {
		return nil, "", err
	}
	if link == nil {
		return nil, "", httperr.NotFound("dataset not found")
	}
	if err := s.assertRAGFlowDocument(ctx, ragflowDatasetID, documentID); err != nil {
		return nil, "", err
	}
	data, ct, err := s.RAGFlow.GetDocumentContent(ctx, documentID)
	if err != nil {
		return nil, "", httperr.New(502, 50254, "ragflow get document content failed")
	}
	return data, ct, nil
}

// ChatImage streams a referenced chunk image from RAGFlow.
func (s *Service) ChatImage(
	ctx context.Context, tenantID, ragflowDatasetID, docID, chunkID, imageID string,
) ([]byte, string, error) {
	if ragflowDatasetID == "" || docID == "" || chunkID == "" || imageID == "" {
		return nil, "", httperr.BadRequest(40096, "dataset, doc, chunk and image are required")
	}
	link, err := s.Store.GetRAGFlowDatasetLinkForScope(ctx, false, []string{tenantID}, ragflowDatasetID)
	if err != nil {
		return nil, "", err
	}
	if link == nil {
		return nil, "", httperr.NotFound("dataset not found")
	}
	if err := s.assertRAGFlowDocument(ctx, ragflowDatasetID, docID); err != nil {
		return nil, "", err
	}
	data, ct, err := s.RAGFlow.GetChunkImage(ctx, imageID)
	if err != nil {
		return nil, "", httperr.New(502, 50255, "ragflow get chunk image failed")
	}
	return data, ct, nil
}

func (s *Service) assertRAGFlowDocument(ctx context.Context, ragflowDatasetID, documentID string) error {
	documents, err := s.RAGFlow.ListDocuments(ctx, ragflowDatasetID)
	if err != nil {
		return httperr.New(502, 50253, "ragflow list document chunks failed")
	}
	for _, document := range documents {
		if document.ID == documentID {
			return nil
		}
	}
	return httperr.NotFound("document not found")
}
