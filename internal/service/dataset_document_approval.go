package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func approvalDocumentSnapshot(document ragflow.Document) map[string]any {
	return map[string]any{
		"name": document.Name, "status": document.Status,
		"enabled": string(document.Enabled), "chunk_count": document.ChunkCount,
		"token_count": document.TokenCount, "update_time": document.UpdateTime,
	}
}

func approvalChunkSnapshot(chunk ragflow.Chunk) map[string]any {
	return map[string]any{
		"content_hash": actingSHA256Hex(chunk.Content), "available": chunk.Available,
		"document_id": chunk.DocumentID, "doc_name": chunk.DocName,
	}
}

func approvalSnapshotMatches(current, approved map[string]any) error {
	for key, approvedValue := range approved {
		currentValue, exists := current[key]
		if !exists || fmt.Sprint(approvedValue) != fmt.Sprint(currentValue) {
			return httperr.New(409, 40972, "approval resource snapshot has changed")
		}
	}
	return nil
}

func (s *Service) approvalDocumentSnapshot(ctx context.Context, tenantID, datasetID, documentID string) (*model.DatasetLink, map[string]any, map[string]any, error) {
	dataset, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		return nil, nil, nil, err
	}
	if dataset == nil {
		return nil, nil, nil, httperr.NotFound("dataset not found")
	}
	documents, err := s.RAGFlow.ListDocuments(ctx, dataset.RAGFlowDatasetID)
	if err != nil {
		return nil, nil, nil, httperr.New(502, 50202, "ragflow list documents failed")
	}
	for _, document := range documents {
		if document.ID == documentID {
			return dataset, approvalDocumentSnapshot(document), map[string]any{"document_name": document.Name}, nil
		}
	}
	return nil, nil, nil, httperr.NotFound("document not found")
}

func (s *Service) approvalChunkSnapshot(ctx context.Context, tenantID, datasetID, documentID, chunkID string) (*model.DatasetLink, map[string]any, map[string]any, error) {
	dataset, err := s.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		return nil, nil, nil, err
	}
	if dataset == nil {
		return nil, nil, nil, httperr.NotFound("dataset not found")
	}
	for page := 1; ; page++ {
		chunks, total, err := s.RAGFlow.ListDocumentChunks(ctx, dataset.RAGFlowDatasetID, documentID, page, 100)
		if err != nil {
			return nil, nil, nil, httperr.New(502, 50210, "ragflow list document chunks failed")
		}
		for _, chunk := range chunks {
			if chunk.ID == chunkID {
				return dataset, approvalChunkSnapshot(chunk), map[string]any{"chunk_id": chunk.ID}, nil
			}
		}
		if int64(page*100) >= total || len(chunks) == 0 {
			break
		}
	}
	return nil, nil, nil, httperr.NotFound("chunk not found")
}

func validateApprovalDocumentAction(ctx context.Context, svc *Service, approval *model.Approval) error {
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return err
	}
	payload, err := approvalPayload(approval)
	if err != nil {
		return err
	}
	datasetID, _ := payload["dataset_id"].(string)
	chunkID, _ := payload["chunk_id"].(string)
	if strings.TrimSpace(datasetID) == "" {
		return httperr.BadRequest(40089, "dataset_id is required")
	}
	if chunkID != "" {
		_, current, _, err := svc.approvalChunkSnapshot(ctx, targetTenantID, datasetID, payloadString(payload, "document_id"), approval.ObjectID)
		if err != nil {
			return err
		}
		approved := map[string]any{}
		if err := decodeSnapshot(approval.SnapshotJSON, &approved); err != nil {
			return err
		}
		return approvalSnapshotMatches(current, approved)
	}
	_, current, _, err := svc.approvalDocumentSnapshot(ctx, targetTenantID, datasetID, approval.ObjectID)
	if err != nil {
		return err
	}
	approved := map[string]any{}
	if err := decodeSnapshot(approval.SnapshotJSON, &approved); err != nil {
		return err
	}
	return approvalSnapshotMatches(current, approved)
}

func validateApprovalDocumentChunkAction(ctx context.Context, svc *Service, approval *model.Approval) error {
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return err
	}
	payload, err := approvalPayload(approval)
	if err != nil {
		return err
	}
	datasetID := payloadString(payload, "dataset_id")
	documentID := payloadString(payload, "document_id")
	if strings.TrimSpace(datasetID) == "" || strings.TrimSpace(documentID) == "" {
		return httperr.BadRequest(40089, "dataset_id and document_id are required")
	}
	_, current, _, err := svc.approvalChunkSnapshot(ctx, targetTenantID, datasetID, documentID, approval.ObjectID)
	if err != nil {
		return err
	}
	approved := map[string]any{}
	if err := decodeSnapshot(approval.SnapshotJSON, &approved); err != nil {
		return err
	}
	return approvalSnapshotMatches(current, approved)
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func decodeSnapshot(raw string, value *map[string]any) error {
	if raw == "" || raw == "{}" {
		*value = map[string]any{}
		return nil
	}
	if err := json.Unmarshal([]byte(raw), value); err != nil {
		return httperr.BadRequest(40089, "invalid approval snapshot")
	}
	return nil
}

func (s *Service) executeDocumentAction(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	datasetID := payloadString(payload, "dataset_id")
	documentID := payloadString(payload, "document_id")
	if strings.TrimSpace(datasetID) == "" || strings.TrimSpace(documentID) == "" {
		return nil, httperr.BadRequest(40089, "dataset_id and document_id are required")
	}
	switch approval.Action {
	case model.ApprovalActionUpdate:
		metadata, _ := payload["metadata"].(map[string]any)
		if metadata == nil {
			return nil, httperr.BadRequest(40089, "metadata is required")
		}
		if err := s.UpdateDocumentMetadata(ctx, targetTenantID, datasetID, documentID, metadata); err != nil {
			return nil, err
		}
		return map[string]any{"document_id": documentID, "updated": true}, nil
	case model.ApprovalActionDelete:
		if err := s.DeleteApprovedDocuments(ctx, targetTenantID, datasetID, []string{documentID}); err != nil {
			return nil, err
		}
		return map[string]any{"document_id": documentID, "deleted": true}, nil
	case model.ApprovalActionParse:
		if err := s.ParseDocuments(ctx, targetTenantID, datasetID, []string{documentID}); err != nil {
			return nil, err
		}
		return map[string]any{"document_id": documentID, "parsed": true}, nil
	case model.ApprovalActionStop:
		if err := s.StopDocuments(ctx, targetTenantID, datasetID, []string{documentID}); err != nil {
			return nil, err
		}
		return map[string]any{"document_id": documentID, "stopped": true}, nil
	case model.ApprovalActionEnable, model.ApprovalActionDisable:
		enabled := approval.Action == model.ApprovalActionEnable
		if value, exists := payload["enabled"].(bool); exists {
			enabled = value
		}
		if err := s.SetDocumentsStatus(ctx, targetTenantID, datasetID, []string{documentID}, enabled); err != nil {
			return nil, err
		}
		return map[string]any{"document_id": documentID, "enabled": enabled}, nil
	default:
		return nil, httperr.BadRequest(40089, "unsupported document action")
	}
}

func (s *Service) executeDocumentChunkAction(ctx context.Context, _ *Service, approval *model.Approval) (map[string]any, error) {
	payload, err := approvalPayload(approval)
	if err != nil {
		return nil, err
	}
	targetTenantID, err := approvalTargetTenant(approval)
	if err != nil {
		return nil, err
	}
	datasetID := payloadString(payload, "dataset_id")
	documentID := payloadString(payload, "document_id")
	chunkID := approval.ObjectID
	if strings.TrimSpace(datasetID) == "" || strings.TrimSpace(documentID) == "" || strings.TrimSpace(chunkID) == "" {
		return nil, httperr.BadRequest(40089, "dataset_id, document_id and chunk_id are required")
	}
	switch approval.Action {
	case model.ApprovalActionDelete:
		if err := s.DeleteChunks(ctx, targetTenantID, datasetID, documentID, []string{chunkID}); err != nil {
			return nil, err
		}
		return map[string]any{"chunk_id": chunkID, "deleted": true}, nil
	case model.ApprovalActionEnable, model.ApprovalActionDisable:
		enabled := approval.Action == model.ApprovalActionEnable
		if value, exists := payload["enabled"].(bool); exists {
			enabled = value
		}
		if err := s.SetChunksAvailable(ctx, targetTenantID, datasetID, documentID, []string{chunkID}, enabled); err != nil {
			return nil, err
		}
		return map[string]any{"chunk_id": chunkID, "enabled": enabled}, nil
	default:
		return nil, httperr.BadRequest(40089, "unsupported chunk action")
	}
}
