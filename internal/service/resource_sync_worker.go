package service

import (
	"context"
	"encoding/json"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

type ragflowImportWorker struct {
	svc *Service
}

func (w *ragflowImportWorker) Kind() string { return model.JobKindRAGFlowImport }

func (w *ragflowImportWorker) Run(ctx context.Context, job *model.Job) error {
	var payload struct {
		RunID  string `json:"run_id"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
		return err
	}
	if payload.RunID == "" {
		payload.RunID = job.TenantID
	}
	userID := payload.UserID
	if userID == "" {
		userID = job.TenantID
	}
	_, err := w.svc.ImportResourceSync(ctx, payload.RunID, userID, ResourceSyncImportRequest{})
	return err
}

type ragflowReconcileWorker struct {
	svc *Service
}

func (w *ragflowReconcileWorker) Kind() string { return model.JobKindRAGFlowReconcile }

func (w *ragflowReconcileWorker) Run(ctx context.Context, job *model.Job) error {
	var payload struct {
		ResourceTypes []string `json:"resource_types"`
		Scheduled     bool     `json:"scheduled"`
	}
	if job.Payload != "" {
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			return err
		}
	}
	if payload.Scheduled {
		setting, err := w.svc.GetResourceSyncSetting(ctx, "")
		if err != nil {
			return err
		}
		if setting == nil || !setting.Enabled || !setting.ScheduledReconcileEnabled {
			return nil
		}
		armed, err := w.svc.ScheduleResourceSyncReconcile(ctx, job.ID)
		if err != nil {
			return err
		}
		if !armed {
			return nil
		}
		setting, err = w.svc.GetResourceSyncSetting(ctx, "")
		if err != nil {
			return err
		}
		if setting == nil || !setting.Enabled || !setting.ScheduledReconcileEnabled {
			return nil
		}
		if err := w.svc.recoverStaleSyncRuns(ctx); err != nil {
			return err
		}
		activeRuns, err := w.svc.Store.CountActiveSyncRuns(ctx, ResourceSyncSourceID)
		if err != nil {
			return err
		}
		if activeRuns > 0 {
			return nil
		}
	}
	_, err := w.svc.ReconcileResourceSync(ctx, job.TenantID, model.SyncTriggerScheduledReconcile, payload.ResourceTypes)
	return err
}
