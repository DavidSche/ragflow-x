package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// DashboardSummary aggregates operational counts for the control plane.
type DashboardSummary struct {
	Tenants   int64 `json:"tenants"`
	Users     int64 `json:"users"`
	Datasets  int   `json:"datasets"`
	Documents int64 `json:"documents"`
	Providers int   `json:"providers"`
	APIKeys   int   `json:"api_keys"`
}

// Dashboard returns an operational summary scoped to the caller's role.
// Platform admins see global counts; all other roles see their own tenant only.
func (s *Service) Dashboard(ctx context.Context, actorID, tenantID string) (*DashboardSummary, error) {
	isPlatform := s.Authorize(ctx, actorID, "governance.read", "tenant") == nil

	// ── Tenants ──
	var tenants int64
	if isPlatform {
		var err error
		tenants, err = s.Store.CountTenants(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		tenants = 1 // non-admin always belongs to exactly one tenant
	}

	// ── Users ──
	var users int64
	if isPlatform {
		var err error
		users, err = s.Store.CountUsers(ctx)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		users, err = s.Store.CountUsersByTenant(ctx, tenantID)
		if err != nil {
			return nil, err
		}
	}

	// ── Datasets (always tenant-scoped) ──
	links, err := s.Store.ListByTenant(ctx, tenantID, repository.DatasetFilter{})
	if err != nil {
		return nil, err
	}

	// ── Model Providers (always tenant-scoped) ──
	providers, err := s.Store.ListModelProviders(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// ── API Keys (always tenant-scoped) ──
	keys, err := s.Store.ListAPIKeys(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	// ── Documents (count matching RAGFlow datasets for this tenant) ──
	docs := int64(0)
	if live, err := s.RAGFlow.ListDatasets(ctx); err == nil {
		ids := make(map[string]bool, len(links))
		for _, l := range links {
			ids[l.RAGFlowDatasetID] = true
		}
		for _, ds := range live {
			if ids[ds.ID] {
				docs += ds.DocumentCount
			}
		}
	}

	return &DashboardSummary{
		Tenants: tenants, Users: users, Datasets: len(links),
		Documents: docs, Providers: len(providers), APIKeys: len(keys),
	}, nil
}

// UsageSummary returns metered usage rows for a tenant, optionally filtered by
// an ISO date (yyyy-mm-dd).
func (s *Service) UsageSummary(ctx context.Context, tenantID, date string) ([]model.QuotaUsage, error) {
	return s.Store.SummarizeUsage(ctx, tenantID, date)
}

// SystemHealth probes the RAGFlow engine and reports its dependency status.
func (s *Service) SystemHealth(ctx context.Context) (map[string]string, error) {
	h, err := s.RAGFlow.Health(ctx)
	if err != nil {
		return map[string]string{"engine": "down", "status": "error"}, httperr.New(502, 50210, "ragflow health check failed")
	}
	return map[string]string{
		"engine":     "up",
		"status":     h.Status,
		"db":         h.DB,
		"redis":      h.Redis,
		"doc_engine": h.DocEngine,
	}, nil
}
