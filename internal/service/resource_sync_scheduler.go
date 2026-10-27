package service

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

const resourceSyncReconcileScheduleKeyPrefix = "ragflow_reconcile:"

// ScheduleResourceSyncReconcile arms the recurring RAGFlow reconciliation
// worker. The durable job ledger keeps one active schedule; excludeID lets the
// current running job arm the next cycle before it settles.
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
		resourceSyncReconcileScheduleKeyPrefix+strconv.FormatInt(time.Now().UTC().UnixNano(), 10),
		ResourceSyncSourceID, string(payload), runAfter, 0)
}

func settingResourceTypes(raw string) []string {
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}
