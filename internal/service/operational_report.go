package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type OperationalReportFilter = repository.OperationalReportFilter

func (s *Service) OperationalAttributionReport(ctx context.Context, actorID, tenantID string, filter OperationalReportFilter) ([]model.OperationalAttributionRow, error) {
	if err := s.Authorize(ctx, actorID, "read", "usage"); err != nil {
		return nil, err
	}
	scopeAll := s.Authorize(ctx, actorID, "governance.read", "tenant") == nil
	return s.Store.SummarizeOperationsByAttribution(ctx, tenantID, scopeAll, filter)
}
