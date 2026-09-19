package service

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ListSearchApps returns the tenant's Search Apps (or all tenants for scopeAll).
func (s *Service) ListSearchApps(ctx context.Context, tenantID string, scopeAll bool, filter repository.SearchAppFilter, page, pageSize int) ([]model.SearchAppShadow, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return s.Store.ListSearchAppShadows(ctx, tenantID, scopeAll, filter, page, pageSize)
}

// ListSearchAppsForScope is the Resolver-backed governance read path.
func (s *Service) ListSearchAppsForScope(ctx context.Context, scope TenantScope, filter repository.SearchAppFilter, page, pageSize int) ([]model.SearchAppShadow, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	return s.Store.ListSearchAppShadowsForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), filter, page, pageSize)
}

// GetSearchApp returns a Search App the caller may access.
func (s *Service) GetSearchApp(ctx context.Context, tenantID, searchAppID string, scopeAll bool) (*model.SearchAppShadow, error) {
	sa, err := s.Store.GetSearchAppShadow(ctx, tenantID, searchAppID, scopeAll)
	if err != nil {
		return nil, err
	}
	if sa == nil {
		return nil, httperr.NotFound("search app not found")
	}
	return sa, nil
}

// GetSearchAppForScope reads a Search App only within the resolved scope.
func (s *Service) GetSearchAppForScope(ctx context.Context, scope TenantScope, searchAppID string) (*model.SearchAppShadow, error) {
	sa, err := s.Store.GetSearchAppShadowForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), searchAppID)
	if err != nil {
		return nil, err
	}
	if sa == nil {
		return nil, httperr.NotFound("search app not found")
	}
	return sa, nil
}

// GetSearchAppConfig returns the live RAGFlow-side search_config so the
// config editor can prefill current retrieval/model settings.
func (s *Service) GetSearchAppConfig(ctx context.Context, tenantID, searchAppID string, scopeAll bool) (*ragflow.SearchApp, error) {
	sa, err := s.Store.GetSearchAppShadow(ctx, tenantID, searchAppID, scopeAll)
	if err != nil {
		return nil, err
	}
	if sa == nil {
		return nil, httperr.NotFound("search app not found")
	}
	live, err := s.RAGFlow.GetSearchApp(ctx, searchAppID)
	if err != nil {
		return nil, httperr.New(502, 50264, "ragflow get search app config failed")
	}
	if live == nil {
		return nil, httperr.NotFound("search app not found")
	}
	return live, nil
}

// GetSearchAppConfigForScope returns live configuration for governance reads.
func (s *Service) GetSearchAppConfigForScope(ctx context.Context, scope TenantScope, searchAppID string) (*ragflow.SearchApp, error) {
	if _, err := s.GetSearchAppForScope(ctx, scope, searchAppID); err != nil {
		return nil, err
	}
	live, err := s.RAGFlow.GetSearchApp(ctx, searchAppID)
	if err != nil {
		return nil, httperr.New(502, 50262, "ragflow get search app config failed")
	}
	if live == nil {
		return nil, httperr.NotFound("search app not found")
	}
	return live, nil
}

// CreateSearchApp creates a Search App in RAGFlow with the given config and
// records its ownership in the shadow table.
func (s *Service) CreateSearchApp(ctx context.Context, tenantID, name string, cfg *ragflow.SearchConfig) (*model.SearchAppShadow, error) {
	if strings.TrimSpace(name) == "" {
		return nil, httperr.BadRequest(40093, "search app name is required")
	}
	kbIDs, resolveErr := s.resolveSearchKBs(ctx, tenantID, cfg)
	if resolveErr != nil {
		return nil, resolveErr
	}
	if err := s.validateOwnedDatasets(ctx, kbIDs); err != nil {
		return nil, err
	}
	if cfg != nil {
		cfg.KbIDs = kbIDs
	}
	id, err := s.RAGFlow.CreateSearchApp(ctx, ragflow.CreateSearchAppRequest{Name: name})
	if err != nil {
		return nil, httperr.New(502, 50260, "ragflow create search app failed")
	}
	if cfg != nil && !searchConfigEmpty(cfg) {
		if _, err := s.RAGFlow.UpdateSearchApp(ctx, id, ragflow.UpdateSearchAppRequest{Name: name, SearchConfig: cfg}); err != nil {
			return nil, httperr.New(502, 50261, "ragflow configure search app failed")
		}
	}
	now := time.Now().UTC()
	sa := &model.SearchAppShadow{
		ID: id, TenantID: tenantID, Name: name, Status: "active",
		DatasetIDs: strings.Join(kbIDs, ","), CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Store.UpsertSearchAppShadow(ctx, sa); err != nil {
		return nil, err
	}
	return sa, nil
}

// UpdateSearchApp updates a Search App's name and search_config.
func (s *Service) UpdateSearchApp(ctx context.Context, tenantID, searchAppID, name string, cfg *ragflow.SearchConfig, scopeAll bool) (*model.SearchAppShadow, error) {
	sa, err := s.Store.GetSearchAppShadow(ctx, tenantID, searchAppID, scopeAll)
	if err != nil {
		return nil, err
	}
	if sa == nil {
		return nil, httperr.NotFound("search app not found")
	}
	if cfg != nil {
		kbIDs, resolveErr := s.resolveSearchKBs(ctx, tenantID, cfg)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if err := s.validateOwnedDatasets(ctx, kbIDs); err != nil {
			return nil, err
		}
		cfg.KbIDs = kbIDs
	}
	req := ragflow.UpdateSearchAppRequest{Name: name}
	if cfg != nil {
		req.SearchConfig = cfg
	}
	if _, err := s.RAGFlow.UpdateSearchApp(ctx, searchAppID, req); err != nil {
		return nil, httperr.New(502, 50262, "ragflow update search app failed")
	}
	if name != "" {
		sa.Name = name
	}
	if cfg != nil {
		sa.DatasetIDs = strings.Join(cfg.KbIDs, ",")
	}
	sa.UpdatedAt = time.Now().UTC()
	if err := s.Store.UpsertSearchAppShadow(ctx, sa); err != nil {
		return nil, err
	}
	return sa, nil
}

// DeleteSearchApp deletes a Search App the caller owns.
func (s *Service) DeleteSearchApp(ctx context.Context, tenantID, searchAppID string, scopeAll bool) error {
	sa, err := s.Store.GetSearchAppShadow(ctx, tenantID, searchAppID, scopeAll)
	if err != nil {
		return err
	}
	if sa == nil {
		return httperr.NotFound("search app not found")
	}
	if err := s.RAGFlow.DeleteSearchApp(ctx, searchAppID); err != nil {
		return httperr.New(502, 50263, "ragflow delete search app failed")
	}
	return s.Store.DeleteSearchAppShadow(ctx, searchAppID)
}

// SearchAppCompletion runs a retrieval against an owned Search App and meters
// the real consumption.
func (s *Service) SearchAppCompletion(ctx context.Context, tenantID, searchAppID, question, requestID string, scopeAll bool) (*ragflow.SearchAppCompletionResult, error) {
	sa, err := s.Store.GetSearchAppShadow(ctx, tenantID, searchAppID, scopeAll)
	if err != nil {
		return nil, err
	}
	if sa == nil {
		return nil, httperr.NotFound("search app not found")
	}
	if strings.TrimSpace(question) == "" {
		return nil, httperr.BadRequest(40094, "question is required")
	}
	startedAt := time.Now()
	kbIDs, err := s.searchAppKbIDs(ctx, sa)
	if err != nil {
		return nil, err
	}
	resp, err := s.RAGFlow.SearchAppCompletion(ctx, searchAppID, ragflow.SearchAppCompletionRequest{Question: question, KbIDs: kbIDs})
	if err != nil {
		s.recordKnowledgeEvent(ctx, knowledgeEvent(tenantID, sa.OwnerID, "search", searchAppID, "", requestID, question, "", 0, 0, 0, int64(time.Since(startedAt).Milliseconds())))
		return nil, httperr.New(502, 50265, "ragflow search completion failed")
	}
	answer, citations := "", int64(0)
	if resp != nil {
		answer = resp.Answer
		citations = citationCountFromMaps(resp.Reference)
	}
	s.recordKnowledgeEvent(ctx, knowledgeEvent(tenantID, sa.OwnerID, "search", searchAppID, "", requestID, question, answer, citations, int64(len([]rune(question))), int64(len([]rune(answer))), int64(time.Since(startedAt).Milliseconds())))
	s.MeterSearchCompletion(ctx, tenantID, sa.OwnerID, requestID, int64(len([]rune(question))))
	return resp, nil
}

// StreamSearchAppCompletion proxies the SSE completion to w after authorization.
func (s *Service) StreamSearchAppCompletion(ctx context.Context, tenantID, searchAppID, question, requestID string, scopeAll bool, w io.Writer) error {
	sa, err := s.Store.GetSearchAppShadow(ctx, tenantID, searchAppID, scopeAll)
	if err != nil {
		return err
	}
	if sa == nil {
		return httperr.NotFound("search app not found")
	}
	if strings.TrimSpace(question) == "" {
		return httperr.BadRequest(40094, "question is required")
	}
	startedAt := time.Now()
	if requestID == "" {
		requestID = id.New()
	}
	kbIDs, err := s.searchAppKbIDs(ctx, sa)
	if err != nil {
		return err
	}
	capture := &knowledgeOpsStreamCapture{w: w}
	err = s.RAGFlow.StreamSearchAppCompletion(ctx, searchAppID, ragflow.SearchAppCompletionRequest{Question: question, KbIDs: kbIDs}, capture)
	if err != nil {
		s.recordKnowledgeEvent(ctx, knowledgeEvent(tenantID, sa.OwnerID, "search", searchAppID, "", requestID, question, "", 0, 0, 0, int64(time.Since(startedAt).Milliseconds())))
		return err
	}
	s.recordKnowledgeEvent(ctx, knowledgeEvent(tenantID, sa.OwnerID, "search", searchAppID, "", requestID, question, capture.answer.String(), int64(capture.citations), int64(len([]rune(question))), capture.tokensOut, int64(time.Since(startedAt).Milliseconds())))
	s.MeterSearchCompletion(ctx, tenantID, sa.OwnerID, requestID, int64(len([]rune(question))))
	return nil
}

func (s *Service) searchAppKbIDs(ctx context.Context, sa *model.SearchAppShadow) ([]string, error) {
	ids := make([]string, 0)
	if strings.TrimSpace(sa.DatasetIDs) != "" {
		for _, datasetID := range strings.Split(sa.DatasetIDs, ",") {
			if strings.TrimSpace(datasetID) != "" {
				ids = append(ids, strings.TrimSpace(datasetID))
			}
		}
	}
	resolved, err := s.resolveRAGFlowDatasetIDs(ctx, sa.TenantID, ids)
	if err != nil {
		return nil, err
	}
	if err := s.validateOwnedDatasets(ctx, resolved); err != nil {
		return nil, err
	}
	if len(resolved) == 0 {
		return nil, httperr.BadRequest(40094, "search app has no configured datasets")
	}
	return resolved, nil
}

func (s *Service) resolveSearchKBs(ctx context.Context, tenantID string, cfg *ragflow.SearchConfig) ([]string, error) {
	if cfg == nil || len(cfg.KbIDs) == 0 {
		return nil, nil
	}
	resolved, err := s.resolveRAGFlowDatasetIDs(ctx, tenantID, cfg.KbIDs)
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

func searchConfigEmpty(cfg *ragflow.SearchConfig) bool {
	if cfg == nil {
		return true
	}
	return len(cfg.KbIDs) == 0 && len(cfg.DocIDs) == 0 && cfg.SimilarityThreshold == 0 &&
		cfg.VectorSimilarityWeight == 0 && !cfg.UseKG && cfg.RerankID == "" && cfg.TopK == 0 &&
		!cfg.Summary && cfg.ChatID == "" && len(cfg.LLMSetting) == 0
}

// MeterSearchCompletion records a real Search App retrieval so the operator
// sees the consumption in the daily aggregate and per-request detail.
func (s *Service) MeterSearchCompletion(ctx context.Context, tenantID, userID, requestID string, tIn int64) {
	if requestID == "" {
		requestID = id.New()
	}
	tOut := tIn / 2
	date := time.Now().UTC().Format("2006-01-02")
	_, _ = s.Store.RecordUsage(ctx, &model.QuotaUsage{
		TenantID: tenantID, UserID: userID, KeyID: "", RequestID: requestID,
		Date: date, TokensIn: tIn, TokensOut: tOut, Requests: 1,
	})
	_, _ = s.Store.RecordCostMetric(ctx, &model.CostMetric{
		TenantID: tenantID, UserID: userID, RequestID: requestID, KeyID: "",
		Date: date, Model: "search-app", Scenario: "search", TokensIn: tIn, TokensOut: tOut,
		EstimatedCost: (float64(tIn) + float64(tOut)) / 1000 * s.EstimatedCostPer1K,
	})
}
