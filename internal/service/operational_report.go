package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type OperationalReportFilter = repository.OperationalReportFilter

// UsageFilterOptions returns the dropdown values for the usage reports. The
// same permission gate as the reports themselves applies: read usage, with the
// cross-tenant option only for governance readers.
func (s *Service) UsageFilterOptions(ctx context.Context, actorID, tenantID, search string) (repository.UsageFilterOptions, error) {
	if err := s.Authorize(ctx, actorID, "read", "usage"); err != nil {
		return repository.UsageFilterOptions{}, err
	}
	scopeAll := s.Authorize(ctx, actorID, "governance.read", "tenant") == nil
	return s.Store.UsageFilterOptions(ctx, tenantID, scopeAll, search)
}

func (s *Service) OperationalAttributionReport(ctx context.Context, actorID, tenantID string, filter OperationalReportFilter) ([]model.OperationalAttributionRow, error) {
	if err := s.Authorize(ctx, actorID, "read", "usage"); err != nil {
		return nil, err
	}
	scopeAll := s.Authorize(ctx, actorID, "governance.read", "tenant") == nil
	return s.Store.SummarizeOperationsByAttribution(ctx, tenantID, scopeAll, filter)
}
