package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

const resourceSyncReconcileScheduleKeyPrefix = "ragflow_reconcile:"

// ScheduleResourceSyncReconcile arms the recurring RAGFlow reconciliation
// worker. The durable job ledger keeps one active schedule; excludeID lets the
// current running job arm the next cycle before it settles.
//
// The idempotency key is an id, never a timestamp: the ledger deduplicates on
// `key`, and Windows' wall clock repeats `time.Now().UnixNano()` between
// back-to-back calls (measured: 2000 of 2000 pairs identical on the 4.151 box).
// A time-based key therefore collapsed the "arm the next cycle" insert into the
// previous one, `Enqueue` reported "not inserted", and the worker returned before
// recovering stale runs — a silent flake on Windows (observed roughly one in six
// repeated runs), never on Linux, where the clock advances between two syscalls.
func (s *Service) ScheduleResourceSyncReconcile(ctx context.Context, excludeID string) (bool, error) {
	setting, err := s.GetResourceSyncSetting(ctx, "")
	if err != nil {
		return false, err
	}
	if setting == nil || !setting.Enabled || !setting.ScheduledReconcileEnabled {
		return false, nil
	}
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	resourceTypes, err := normalizeResourceTypes(settingResourceTypes(setting.ResourceTypesJSON))
	if err != nil {
		return false, err
	}
	active, err := s.Store.CountActiveJobsExcept(ctx, model.JobKindRAGFlowReconcile, ResourceSyncSourceID, excludeID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	payload, err := json.Marshal(map[string]interface{}{
		"resource_types": resourceTypes,
		"scheduled":      true,
	})
	if err != nil {
		return false, err
	}
	runAfter := time.Now().UTC().Add(time.Duration(setting.IntervalSeconds) * time.Second)
	return s.Runner.Enqueue(ctx, model.JobKindRAGFlowReconcile,
		resourceSyncReconcileScheduleKeyPrefix+id.New(),
		ResourceSyncSourceID, string(payload), runAfter, 0)
}

func settingResourceTypes(raw string) []string {
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}
