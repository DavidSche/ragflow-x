package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// CreateTask persists a queue entry with id/status defaults.
func (s *Service) CreateTask(ctx context.Context, task *model.Task) error {
	if task.ID == "" {
		task.ID = id.New()
	}
	if task.Status == "" {
		task.Status = model.TaskStatusQueued
	}
	return s.Store.CreateTask(ctx, task)
}

// ListTasks returns a page of the tenant's operator queue.
func (s *Service) ListTasks(ctx context.Context, tenantID string, page, pageSize int) ([]model.Task, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return s.Store.ListTasks(ctx, tenantID, page, pageSize)
}

// DeleteTasks removes terminal task projections for an operator-facing
// history cleanup. Running and queued tasks remain visible until they reach a
// terminal state because RAGFlow is still the source of truth for progress.
func (s *Service) DeleteTasks(ctx context.Context, tenantID string, ids []string) (int64, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return 0, httperr.BadRequest(40089, "task ids must be between 1 and 100")
	}
	requested := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			return 0, httperr.BadRequest(40089, "task ids must be unique and non-empty")
		}
		if _, exists := requested[id]; exists {
			return 0, httperr.BadRequest(40089, "task ids must be unique and non-empty")
		}
		requested[id] = struct{}{}
	}

	tasks, err := s.Store.ListTasksByIDs(ctx, tenantID, ids)
	if err != nil {
		return 0, err
	}
	for _, task := range tasks {
		if _, exists := requested[task.ID]; !exists {
			continue
		}
		if task.Status != model.TaskStatusDone && task.Status != model.TaskStatusFailed && task.Status != model.TaskStatusStopped {
			return 0, httperr.BadRequest(40089, "only terminal tasks can be deleted")
		}
		delete(requested, task.ID)
	}
	if len(requested) != 0 {
		return 0, httperr.NotFound("task not found")
	}

	deleted, err := s.Store.DeleteTerminalTasks(ctx, tenantID, ids)
	if err != nil {
		return 0, err
	}
	if deleted != int64(len(ids)) {
		return deleted, httperr.New(409, 40910, "task status changed during deletion")
	}
	return deleted, nil
}

// SyncTaskProgress polls RAGFlow's real document parsing state for a tenant
// (empty tenantID syncs every tenant) and pushes the derived status/progress
// back onto the operator queue. It runs as the async worker's first consumer.
func (s *Service) SyncTaskProgress(ctx context.Context, tenantID string) error {
	links, err := s.Store.ListAllDatasetLinks(ctx, repository.DatasetFilter{TenantID: tenantID})
	if err != nil {
		return err
	}
	var failures []error
	for _, link := range links {
		markTasksWithoutDatasetLink(ctx, s, link.TenantID)
		documents, err := s.RAGFlow.ListDocuments(ctx, link.RAGFlowDatasetID)
		if err != nil {
			err = fmt.Errorf("list documents for dataset %s: %w", link.RAGFlowDatasetID, err)
			logger.Warn("incremental task sync failed", "dataset_id", link.ID, "error", err)
			failures = append(failures, err)
			continue
		}
		for _, document := range documents {
			status, progress, detail := mapRAGFlowDocument(document)
			if err := s.Store.SyncParseTask(ctx, document.ID, status, progress, detail); err != nil {
				logger.Warn("parse task projection sync failed", "dataset_id", link.ID, "document_id", document.ID, "error", err)
				failures = append(failures, err)
			}
		}
		if err := s.syncIncrementalLedgerForDataset(ctx, link.TenantID, &link, documents); err != nil {
			logger.Warn("incremental ledger sync failed", "dataset_id", link.ID, "error", err)
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func mapRAGFlowDocument(document ragflow.Document) (status string, progress int, detail string) {
	run := strings.ToLower(strings.TrimSpace(document.Status))
	raw := int(math.Round(document.Progress * 100))
	if raw < 0 {
		raw = 0
	}
	if raw > 100 {
		raw = 100
	}
	detail = strings.TrimSpace(document.ProgressMsg)
	switch {
	case run == "3" || strings.HasPrefix(run, "done") || run == "parsed":
		return model.TaskStatusDone, 100, detail
	case run == "4" || strings.HasPrefix(run, "fail"):
		return model.TaskStatusFailed, raw, detail
	case run == "2" || strings.HasPrefix(run, "cancel"):
		return model.TaskStatusStopped, raw, detail
	case run == "0" || strings.HasPrefix(run, "unstart") || run == "" || strings.HasPrefix(run, "pending"):
		return model.TaskStatusQueued, raw, detail
	default:
		return model.TaskStatusRunning, raw, detail
	}
}

func markTasksWithoutDatasetLink(ctx context.Context, service *Service, tenantID string) {
	const pageSize = 200
	for page := 1; page <= 100; page++ {
		tasks, _, err := service.Store.ListTasks(ctx, tenantID, page, pageSize)
		if err != nil {
			return
		}
		for _, task := range tasks {
			if task.TaskType != model.TaskTypeParse ||
				(task.Status != model.TaskStatusQueued && task.Status != model.TaskStatusRunning) {
				continue
			}
			link, err := service.Store.GetDatasetLink(ctx, tenantID, task.DatasetID)
			if err == nil && link != nil {
				continue
			}
			task.Status = model.TaskStatusStopped
			task.Progress = 0
			task.Detail = "dataset link removed"
			_ = service.Store.UpdateTask(ctx, &task)
		}
		if len(tasks) < pageSize {
			return
		}
	}
}
