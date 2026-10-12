package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func setEnhancementBudget(t *testing.T, svc *Service, tenantID string, budget int64) *model.Tenant {
	t.Helper()
	tenant, err := svc.Store.GetTenant(context.Background(), tenantID)
	if err != nil || tenant == nil {
		if err != nil {
			t.Fatalf("load tenant: %v", err)
		}
		tenant = &model.Tenant{ID: tenantID, Name: "Enhancement Budget Workspace", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err := svc.Store.CreateTenant(context.Background(), tenant); err != nil {
			t.Fatalf("create budget tenant: %v", err)
		}
	}
	tenant.CompilationTokenBudget = budget
	if err := svc.Store.UpdateTenant(context.Background(), tenant); err != nil {
		t.Fatalf("update tenant budget: %v", err)
	}
	return tenant
}

func TestParseKnowledgeProfileEnhancementBudgetRejectsNegative(t *testing.T) {
	if _, err := parseKnowledgeProfileEnhancementBudget(`{"enhancement_budget":{"run_tokens":-1}}`); err == nil {
		t.Fatal("expected negative run budget failure")
	}
	budget, err := parseKnowledgeProfileEnhancementBudget(`{
		"enhancement_budget":{
			"profile_monthly_tokens":400,"run_tokens":200,
			"estimated_tokens_per_document":100
		}
	}`)
	if err != nil {
		t.Fatal(err)
	}
	if budget.ProfileMonthlyTokens == nil || *budget.ProfileMonthlyTokens != 400 ||
		budget.RunTokens == nil || *budget.RunTokens != 200 ||
		budget.EstimatedTokensPerDocument == nil || *budget.EstimatedTokensPerDocument != 100 {
		t.Fatalf("unexpected budget: %+v", budget)
	}
}

func TestBuildKnowledgeProfileValidatesEnhancementBudget(t *testing.T) {
	_, err := buildKnowledgeProfile("tenant", "user", KnowledgeProfileInput{
		DatasetID: "dataset", Name: "Invalid Budget", ProfileType: "page_index",
		Config: `{"enhancement_budget":{"run_tokens":-1}}`,
	}, time.Now().UTC())
	if err == nil {
		t.Fatal("expected negative profile enhancement budget failure")
	}
}

func TestCompilationBudgetRejectedBeforeRunCreated(t *testing.T) {
	ctx := context.Background()
	svc, _, tenantID, datasetID, documentID := createCompilationFixture(t)
	svc.SetupWorker(fastWorkerConfig())
	setEnhancementBudget(t, svc, tenantID, 50)
	dataset, err := svc.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		t.Fatal(err)
	}
	dataset.CompilationTokenBudget = 100
	if err := svc.Store.UpdateDatasetLinkCompilationBudget(ctx, tenantID, datasetID, 100); err != nil {
		t.Fatal(err)
	}
	profile := &model.KnowledgeProfile{
		ID: id.New(), TenantID: tenantID, ProjectID: dataset.ProjectID, DatasetID: datasetID,
		Name: "Budget Profile", ProfileType: model.CompilationKindPageIndex,
		Config: `{"enhancement_budget":{"profile_monthly_tokens":300,"run_tokens":200,"estimated_tokens_per_document":100}}`,
		Status: model.KnowledgeProfileStatusActive, CreatedBy: "requester",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateKnowledgeProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	_, err = svc.TriggerCompilationRun(ctx, tenantID, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "tenant-budget-exhausted", ProfileID: profile.ID,
		Kind: model.CompilationKindPageIndex, DocumentIDs: []string{documentID},
	})
	if httpErr, ok := err.(*httperr.Error); !ok || httpErr.Status != 429 || httpErr.Code != 42930 {
		t.Fatalf("expected 429/42930, got %v", err)
	}
	runs, _, err := svc.Store.ListCompilationRuns(ctx, tenantID, repository.CompilationRunFilter{DatasetID: datasetID}, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("budget rejection must not create a run: %+v", runs)
	}
}

func TestCompilationBudgetSnapshotMeteringAndAttribution(t *testing.T) {
	ctx := context.Background()
	svc, _, tenantID, datasetID, documentID := createCompilationFixture(t)
	svc.SetupWorker(fastWorkerConfig())
	tenant := setEnhancementBudget(t, svc, tenantID, 1000)
	dataset, err := svc.Store.GetDatasetLink(ctx, tenantID, datasetID)
	if err != nil {
		t.Fatal(err)
	}
	dataset.CompilationTokenBudget = 500
	if err := svc.Store.UpdateDatasetLinkCompilationBudget(ctx, tenantID, datasetID, 500); err != nil {
		t.Fatal(err)
	}
	profile := &model.KnowledgeProfile{
		ID: id.New(), TenantID: tenantID, ProjectID: dataset.ProjectID, DatasetID: datasetID,
		Name: "Metered Profile", ProfileType: model.CompilationKindPageIndex,
		Config: `{"enhancement_budget":{"profile_monthly_tokens":400,"run_tokens":200,"estimated_tokens_per_document":100}}`,
		Status: model.KnowledgeProfileStatusActive, CreatedBy: "requester",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateKnowledgeProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	run, err := svc.TriggerCompilationRun(ctx, tenantID, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "budget-metering", ProfileID: profile.ID,
		Kind: model.CompilationKindPageIndex, DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatalf("trigger within budget: %v", err)
	}
	var snapshot map[string]interface{}
	if err := json.Unmarshal([]byte(run.BudgetSnapshot), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["tenant_budget"].(float64) != 1000 || snapshot["dataset_budget"].(float64) != 500 ||
		snapshot["profile_budget"].(float64) != 400 || snapshot["run_budget"].(float64) != 200 ||
		snapshot["estimated"].(float64) != 100 {
		t.Fatalf("budget snapshot was not frozen: %+v", snapshot)
	}
	if run.QuotaReservationID == "" {
		t.Fatal("reservation id missing")
	}
	reservation, err := svc.Store.GetQuotaReservation(ctx, run.QuotaReservationID)
	if err != nil || reservation == nil || reservation.Estimated != 100 {
		t.Fatalf("reservation missing: %+v err=%v", reservation, err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenantID, datasetID, ""); err != nil {
		t.Fatalf("process dispatched run: %v", err)
	}
	reservation, err = svc.Store.GetQuotaReservation(ctx, run.QuotaReservationID)
	if err != nil || reservation == nil || reservation.Status != model.QuotaReservationFinalized {
		t.Fatalf("reservation was not finalized: %+v err=%v", reservation, err)
	}
	metrics, _, err := svc.Store.ListCostMetrics(ctx, tenantID, false, 1, 10, repository.CostMetricFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 || metrics[0].XRunID != run.ID || metrics[0].XDatasetID != datasetID ||
		metrics[0].XProfileID != profile.ID || metrics[0].TokensIn != 100 || !metrics[0].Estimated {
		t.Fatalf("unexpected cost metric: %+v err=%v", metrics, err)
	}
	summary, err := svc.KnowledgeOpsSummary(ctx, tenantID, false, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range summary.CostAttribution {
		if row.RunID == run.ID {
			found = true
			if row.UsedTokens != 100 || row.BudgetTokenLimit != 500 || row.Status != "ok" {
				t.Fatalf("unexpected attribution row: %+v", row)
			}
		}
	}
	if !found {
		t.Fatalf("run missing from cost attribution: %+v", summary.CostAttribution)
	}
	datasetSummary, err := svc.KnowledgeOpsSummary(ctx, tenantID, false, "", "", datasetID)
	if err != nil {
		t.Fatal(err)
	}
	if len(datasetSummary.CostAttribution) != 1 || datasetSummary.CostAttribution[0].DatasetID != datasetID {
		t.Fatalf("dataset cost filter failed: %+v", datasetSummary.CostAttribution)
	}
	_ = tenant
}

func TestCompilationPipelineFailureReleasesReservation(t *testing.T) {
	ctx := context.Background()
	svc, mock, tenantID, datasetID, documentID := createCompilationFixture(t)
	svc.SetupWorker(fastWorkerConfig())
	dsl := compilationPipelineDSL()
	components := dsl["components"].(map[string]interface{})
	delete(components, "compiler")
	mock.SetPipelineTemplate(ragflow.PipelineTemplate{ID: "pipeline-1"}, dsl)
	setEnhancementBudget(t, svc, tenantID, 100)
	profile := &model.KnowledgeProfile{
		ID: id.New(), TenantID: tenantID, DatasetID: datasetID,
		Name: "Failed Pipeline Profile", ProfileType: model.CompilationKindPageIndex,
		Config: `{"enhancement_budget":{"profile_monthly_tokens":100,"run_tokens":100,"estimated_tokens_per_document":50}}`,
		Status: model.KnowledgeProfileStatusActive, CreatedBy: "requester",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateKnowledgeProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	run, err := svc.TriggerCompilationRun(ctx, tenantID, "requester", datasetID, CompilationRunTriggerInput{
		IdempotencyKey: "budget-release", ProfileID: profile.ID,
		Kind: model.CompilationKindPageIndex, DocumentIDs: []string{documentID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.ProcessCompilationRuns(ctx, tenantID, datasetID, ""); err != nil {
		t.Fatal(err)
	}
	reservation, err := svc.Store.GetQuotaReservation(ctx, run.QuotaReservationID)
	if err != nil || reservation == nil || reservation.Status != model.QuotaReservationReleased {
		t.Fatalf("failed pipeline must release reservation: %+v err=%v", reservation, err)
	}
}
