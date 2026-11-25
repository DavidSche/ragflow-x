package service

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// ScheduleMeteringRecovery enqueues the next metering recovery pass and is itself
// idempotent: an already queued pass means the cadence is healthy, so a second
// trigger is dropped instead of queued behind it (same posture as the daily
// reconciliation and the memory compensation sweep).
func (s *Service) ScheduleMeteringRecovery(ctx context.Context, excludeID string) (bool, error) {
	if s.Runner == nil {
		return false, nil
	}
	active, err := s.Store.CountActiveJobsExcept(
		ctx, model.JobKindMeteringRecovery, SystemTenantID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	runAfter := time.Now().UTC().Add(meteringRecoveryInterval)
	return s.Runner.Enqueue(ctx, model.JobKindMeteringRecovery,
		"metering_recovery:"+time.Now().UTC().Format("20060102T150405Z"),
		SystemTenantID, "", runAfter, 0)
}

// meteringRecoveryWorker replays unfinished metering intents then re-arms itself.
type meteringRecoveryWorker struct{ svc *Service }

func (w *meteringRecoveryWorker) Kind() string {
	return model.JobKindMeteringRecovery
}

func (w *meteringRecoveryWorker) Run(ctx context.Context, job *model.Job) error {
	recovered, open, err := w.svc.RecoverGatewayMetering(ctx, meteringRecoveryBatch)
	if err != nil {
		logger.Warn("metering recovery sweep failed", "error", err)
		return err
	}
	if recovered > 0 || open > 0 {
		logger.Info("metering recovery sweep finished", "recovered", recovered, "still_open", open)
	}
	if _, err := w.svc.ScheduleMeteringRecovery(ctx, job.ID); err != nil {
		logger.Warn("metering recovery reschedule failed", "job_id", job.ID, "error", err)
		return err
	}
	return nil
}
