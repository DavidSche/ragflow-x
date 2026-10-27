package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// ErrIncrementalLedgerConflict is returned when another worker changed the
// ledger between the read and conditional update. Callers retry on the next
// sync instead of overwriting newer state.
var ErrIncrementalLedgerConflict = errors.New("incremental ledger changed concurrently")

func (s *store) GetIncrementalLedger(ctx context.Context, tenantID, datasetID, sourceKey string) (*model.IncrementalLedger, error) {
	var ledger model.IncrementalLedger
	err := s.WithContext(ctx).Where(
		"tenant_id = ? AND dataset_id = ? AND source_key = ?", tenantID, datasetID, sourceKey,
	).Order("revision DESC").First(&ledger).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ledger, nil
}

func (s *store) ListIncrementalLedger(ctx context.Context, tenantID, datasetID string) ([]model.IncrementalLedger, error) {
	rows := make([]model.IncrementalLedger, 0)
	err := s.WithContext(ctx).Where(
		"tenant_id = ? AND dataset_id = ?", tenantID, datasetID,
	).Order("revision ASC").Find(&rows).Error
	return rows, err
}

func (s *store) CreateIncrementalLedger(ctx context.Context, ledger *model.IncrementalLedger) error {
	return s.WithContext(ctx).Create(ledger).Error
}

func (s *store) UpdateIncrementalLedger(ctx context.Context, ledger *model.IncrementalLedger) error {
	if ledger == nil {
		return gorm.ErrInvalidValue
	}
	updates := map[string]interface{}{
		"name":                 ledger.Name,
		"content_sha256":       ledger.ContentSHA256,
		"parser_policy_hash":   ledger.ParserPolicyHash,
		"embedding_ref":        ledger.EmbeddingRef,
		"ragflow_document_id":  ledger.RAGFlowDocumentID,
		"previous_document_id": ledger.PreviousDocumentID,
		"parse_task_id":        ledger.ParseTaskID,
		"state":                ledger.State,
		"failure_reason":       ledger.FailureReason,
		"verified_at":          ledger.VerifiedAt,
		"last_seen_at":         ledger.LastSeenAt,
		"tombstoned_at":        ledger.TombstonedAt,
		"purge_after_at":       ledger.PurgeAfterAt,
		"version":              ledger.Version + 1,
		"updated_at":           time.Now().UTC(),
	}
	result := s.WithContext(ctx).Model(&model.IncrementalLedger{}).
		Where("id = ? AND version = ?", ledger.ID, ledger.Version).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrIncrementalLedgerConflict
	}
	ledger.Version++
	return nil
}

func (s *store) GetIncrementalLedgerByDocument(ctx context.Context, tenantID, ragflowDocumentID string) (*model.IncrementalLedger, error) {
	var ledger model.IncrementalLedger
	err := s.WithContext(ctx).Where(
		"tenant_id = ? AND ragflow_document_id = ?", tenantID, ragflowDocumentID,
	).Order("revision DESC").First(&ledger).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ledger, nil
}
