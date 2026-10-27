package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// recordCitationReferences persists a best-effort audit snapshot when the engine
// provides document/chunk identifiers. Missing identifiers are ignored rather
// than inventing references.
func (s *Service) recordCitationReferences(ctx context.Context, tenantID, chatID, sessionID, requestID string, citations []map[string]interface{}) {
	if len(citations) == 0 || requestID == "" {
		return
	}
	now := time.Now().UTC()
	chatDatasetIDs := s.datasetIDsForChat(ctx, tenantID, chatID)
	authorizedDatasetIDs := make(map[string]*model.DatasetLink, len(chatDatasetIDs)*2)
	authorizedDatasetLinks := make([]*model.DatasetLink, 0, len(chatDatasetIDs))
	for _, datasetID := range chatDatasetIDs {
		link, err := s.Store.GetRAGFlowDatasetLinkForScope(ctx, false, []string{tenantID}, datasetID)
		if err != nil {
			logger.Warn("citation dataset lookup failed", "dataset_id", datasetID, "error", err)
			continue
		}
		if link == nil {
			link, err = s.Store.GetDatasetLink(ctx, tenantID, datasetID)
			if err != nil {
				logger.Warn("citation dataset lookup failed", "dataset_id", datasetID, "error", err)
				continue
			}
		}
		if link == nil {
			continue
		}
		authorizedDatasetIDs[link.RAGFlowDatasetID] = link
		authorizedDatasetIDs[link.ID] = link
		authorizedDatasetLinks = append(authorizedDatasetLinks, link)
	}
	for _, citation := range citations {
		documentID := stringFromAny(citation["document_id"])
		if documentID == "" {
			documentID = stringFromAny(citation["doc_id"])
		}
		chunkID := stringFromAny(citation["chunk_id"])
		if chunkID == "" {
			chunkID = stringFromAny(citation["id"])
		}
		if documentID == "" || chunkID == "" {
			continue
		}
		datasetID := stringFromAny(citation["dataset_id"])
		if datasetID == "" {
			datasetID = stringFromAny(citation["dataset"])
		}
		var dataset *model.DatasetLink
		if datasetID != "" {
			dataset = authorizedDatasetIDs[datasetID]
		} else if len(authorizedDatasetLinks) == 1 {
			dataset = authorizedDatasetLinks[0]
		}
		if dataset == nil {
			continue
		}
		datasetID = dataset.ID
		revision := int64(0)
		if ledger, err := s.Store.GetIncrementalLedgerByDocument(ctx, tenantID, documentID); err != nil {
			logger.Warn("citation revision lookup failed", "document_id", documentID, "error", err)
		} else if ledger != nil {
			if ledger.DatasetID != dataset.ID {
				continue
			}
			revision = ledger.Revision
		}
		content := stringFromAny(citation["content"])
		keywords := rawKeywords(citation)
		reference := &model.CitationReference{
			ID: id.New(), TenantID: tenantID, ChatID: chatID, SessionID: sessionID,
			RequestID: requestID, DatasetID: datasetID, DocumentID: documentID,
			ChunkID: chunkID, Revision: revision, Content: content,
			Keywords: keywords, RetrievedAt: now,
		}
		if err := s.Store.CreateCitationReference(ctx, reference); err != nil {
			logger.Warn("citation reference persistence failed", "request_id", requestID, "chunk_id", chunkID, "error", err)
		}
	}
}

func (s *Service) datasetIDsForChat(ctx context.Context, tenantID, chatID string) []string {
	if chatID == "" {
		return nil
	}
	chat, err := s.Store.GetChatShadow(ctx, tenantID, chatID, false)
	if err != nil || chat == nil {
		return nil
	}
	values := make([]string, 0, 8)
	start := 0
	for index, char := range chat.DatasetIDs {
		if char != ',' {
			continue
		}
		if value := trimSpace(chat.DatasetIDs[start:index]); value != "" {
			values = append(values, value)
		}
		start = index + 1
	}
	if value := trimSpace(chat.DatasetIDs[start:]); value != "" {
		values = append(values, value)
	}
	return values
}

func rawKeywords(citation map[string]interface{}) string {
	value, ok := citation["keywords"].([]interface{})
	if !ok {
		return ""
	}
	raw, _ := json.Marshal(value)
	return string(raw)
}

func stringFromAny(value interface{}) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func trimSpace(value string) string {
	return strings.TrimSpace(value)
}
