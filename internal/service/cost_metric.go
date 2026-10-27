package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ListUsageDetail returns a page of per-request metering detail rows. For
// non-platform callers (or a tenant without scope=all) rows are scoped to the
// caller's tenant; platform admins may pass scopeAll to see every tenant.
func (s *Service) ListUsageDetail(ctx context.Context, tenantID string, scopeAll bool, page, pageSize int, filter repository.CostMetricFilter) ([]model.CostMetric, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return s.Store.ListCostMetrics(ctx, tenantID, scopeAll, page, pageSize, filter)
}
