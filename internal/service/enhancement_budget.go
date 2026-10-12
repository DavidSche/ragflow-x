package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type knowledgeProfileEnhancementBudget struct {
	ProfileMonthlyTokens       *int64 `json:"profile_monthly_tokens"`
	RunTokens                  *int64 `json:"run_tokens"`
	EstimatedTokensPerDocument *int64 `json:"estimated_tokens_per_document"`
}

type enhancementBudgetLimits struct {
	TenantBudget  int64
	DatasetBudget int64
	ProfileBudget int64
	RunBudget     int64
	Estimated     int64
}

func parseKnowledgeProfileEnhancementBudget(raw string) (*knowledgeProfileEnhancementBudget, error) {
	var envelope struct {
		EnhancementBudget knowledgeProfileEnhancementBudget `json:"enhancement_budget"`
	}
	if strings.TrimSpace(raw) == "" {
		return &knowledgeProfileEnhancementBudget{}, nil
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return nil, httperr.BadRequest(40147, "knowledge profile config must be valid JSON")
	}
	budget := envelope.EnhancementBudget
	for name, value := range map[string]*int64{
		"profile_monthly_tokens":        budget.ProfileMonthlyTokens,
		"run_tokens":                    budget.RunTokens,
		"estimated_tokens_per_document": budget.EstimatedTokensPerDocument,
	} {
		if value != nil && *value < 0 {
			return nil, httperr.BadRequest(40147, name+" must not be negative")
		}
	}
	return &budget, nil
}

func enhancementBudgetLimitsFor(
	tenant *model.Tenant,
	dataset *model.DatasetLink,
	budget *knowledgeProfileEnhancementBudget,
	documentCount int,
) (*enhancementBudgetLimits, error) {
	if dataset == nil {
		return nil, httperr.BadRequest(40147, "dataset is required for enhancement budgeting")
	}
	limits := &enhancementBudgetLimits{
		DatasetBudget: dataset.CompilationTokenBudget,
	}
	if tenant != nil {
		limits.TenantBudget = tenant.CompilationTokenBudget
	}
	if budget != nil {
		if budget.ProfileMonthlyTokens != nil {
			limits.ProfileBudget = *budget.ProfileMonthlyTokens
		}
		if budget.RunTokens != nil {
			limits.RunBudget = *budget.RunTokens
		}
		if budget.EstimatedTokensPerDocument != nil {
			limits.Estimated = *budget.EstimatedTokensPerDocument * int64(documentCount)
		}
	}
	return limits, nil
}

func compilationBudgetReservationRequestID(runID string) string {
	return "enhancement-budget:" + runID
}

func compilationCostMetricRequestID(runID string) string {
	return "compilation:" + runID + ":dispatch"
}

func (s *Service) reserveCompilationBudget(
	ctx context.Context,
	tx repository.Store,
	run *model.CompilationRun,
	tenant *model.Tenant,
	dataset *model.DatasetLink,
	profileConfig string,
	documentCount int,
) (string, error) {
	budget, err := parseKnowledgeProfileEnhancementBudget(profileConfig)
	if err != nil {
		return "", err
	}
	limits, err := enhancementBudgetLimitsFor(tenant, dataset, budget, documentCount)
	if err != nil {
		return "", err
	}
	snapshot, exceededScope, err := tx.ReserveEnhancementQuota(ctx, repository.EnhancementBudgetRequest{
		RequestID: compilationBudgetReservationRequestID(run.ID), TenantID: run.TenantID,
		ProjectID: run.ProjectID, DatasetID: run.DatasetID, ProfileID: run.ProfileID,
		RunID: run.ID, Estimated: limits.Estimated,
		TenantBudget: limits.TenantBudget, DatasetBudget: limits.DatasetBudget,
		ProfileBudget: limits.ProfileBudget, RunBudget: limits.RunBudget,
	})
	if err != nil {
		return "", err
	}
	if snapshot == nil {
		return "", httperr.New(429, 42930, "insufficient enhancement budget: "+exceededScope)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", httperr.Internal("encode enhancement budget snapshot failed")
	}
	run.BudgetSnapshot = string(encoded)
	run.QuotaReservationID = compilationBudgetReservationRequestID(run.ID)
	return run.QuotaReservationID, nil
}

func (s *Service) recordCompilationCostMetric(ctx context.Context, run *model.CompilationRun) error {
	if run == nil || run.QuotaReservationID == "" || run.BudgetSnapshot == "{}" {
		return nil
	}
	var snapshot repository.EnhancementBudgetSnapshot
	if err := json.Unmarshal([]byte(run.BudgetSnapshot), &snapshot); err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err := s.Store.RecordCostMetric(ctx, &model.CostMetric{
		RequestID: compilationCostMetricRequestID(run.ID), TenantID: run.TenantID,
		UserID: run.RequestedBy, KeyID: snapshot.QuotaKey, Date: now.Format("2006-01-02"),
		Model: "ragflow-compilation", Scenario: "compilation",
		ProjectID: run.ProjectID, XDatasetID: run.DatasetID, XProfileID: run.ProfileID,
		XRunID: run.ID, TokensIn: snapshot.Estimated, TokensOut: 0,
		Estimated: true, EstimationPolicyVersion: "enhancement-token-estimate-v1",
	})
	if err != nil {
		return err
	}
	return s.Store.FinalizeQuotaReservation(ctx, run.QuotaReservationID, snapshot.Estimated)
}

func (s *Service) releaseCompilationReservation(ctx context.Context, run *model.CompilationRun) error {
	if run == nil || run.QuotaReservationID == "" {
		return nil
	}
	return s.Store.ReleaseEnhancementQuota(ctx, run.QuotaReservationID)
}
