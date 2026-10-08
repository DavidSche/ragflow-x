package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func newFactGuardService(t *testing.T, mode string) *Service {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "fact-guard.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), nil, nil, "test-encryption-key")
	svc.SetFactGuardConfig(config.FactGuard{Mode: mode})
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc
}

func createAnswerRun(t *testing.T, svc *Service, tenantID, requestID string) *model.AnswerRun {
	t.Helper()
	run := &model.AnswerRun{
		ID: id.New(), TenantID: tenantID, SessionID: "session-fact", AssistantID: "assistant-fact",
		QuestionRef: "question", RequestID: requestID, LifecycleState: model.AnswerLifecycleCompleted,
		AnswerStatus: model.AnswerStatusAnswered, CompletionReason: model.CompletionReasonNormal,
		CreatedAt: time.Now().UTC(),
	}
	if err := svc.Store.CreateAnswerRun(context.Background(), run); err != nil {
		t.Fatalf("create answer run: %v", err)
	}
	return run
}

func factInput(value string, mutate func(*EvidenceFactInput)) EvidenceFactInput {
	input := EvidenceFactInput{
		FactKey: "revenue", Value: value, Unit: "usd", TimeRange: "FY2025",
		SourceDocID: "doc-1", ClaimType: model.FactClaimTypeExtracted, Confidence: 0.9,
	}
	if mutate != nil {
		mutate(&input)
	}
	return input
}

func TestRegisterEvidenceFactsTemporalConflictResolvedByVersion(t *testing.T) {
	ctx := context.Background()
	svc := newFactGuardService(t, model.ClaimActionKeep)
	tenant := id.New()
	run := createAnswerRun(t, svc, tenant, "request-temporal-"+id.New())
	documentID := id.New()
	if err := svc.Store.CreateLogicalDocument(ctx, &model.LogicalDocument{
		ID: documentID, TenantID: tenant, DatasetID: id.New(), Name: "Revenue", CreatedBy: id.New(),
	}); err != nil {
		t.Fatalf("create logical document: %v", err)
	}
	activeID, supersededID := id.New(), id.New()
	now := time.Now().UTC()
	versions := []model.DocumentVersion{
		{ID: activeID, TenantID: tenant, LogicalDocumentID: documentID, RAGFlowDocumentID: "rag-active", Version: 2, Status: model.DocumentVersionActive, EffectiveFrom: now.Add(-time.Hour), ContentHash: "hash-active", CreatedBy: tenant},
		{ID: supersededID, TenantID: tenant, LogicalDocumentID: documentID, RAGFlowDocumentID: "rag-superseded", Version: 1, Status: model.DocumentVersionSuperseded, EffectiveFrom: now.Add(-2 * time.Hour), EffectiveTo: &now, ContentHash: "hash-superseded", CreatedBy: tenant},
	}
	for index := range versions {
		if err := svc.Store.CreateDocumentVersion(ctx, &versions[index]); err != nil {
			t.Fatalf("create document version: %v", err)
		}
	}
	result, err := svc.RegisterEvidenceFacts(ctx, tenant, tenant, RegisterEvidenceFactsInput{
		AnswerRunID: run.ID,
		Facts: []EvidenceFactInput{
			factInput("10 million", func(input *EvidenceFactInput) {
				input.DocumentVersionID = activeID
				input.LogicalDocumentID = documentID
			}),
			factInput("8 million", func(input *EvidenceFactInput) {
				input.DocumentVersionID = supersededID
				input.LogicalDocumentID = documentID
			}),
		},
	})
	if err != nil {
		t.Fatalf("register evidence facts: %v", err)
	}
	if len(result.Conflicts) != 1 || result.Conflicts[0].ConflictType != model.FactConflictTemporal ||
		result.Conflicts[0].Resolution != model.EvidenceConflictResolvedByVersion {
		t.Fatalf("unexpected conflicts: %+v", result.Conflicts)
	}
	for _, fact := range result.Facts {
		want := model.FactConflictTemporal
		if fact.DocumentVersionID == activeID {
			want = model.FactConflictNone
		}
		if fact.ConflictStatus != want {
			t.Fatalf("fact %s conflict status = %s, want %s", fact.DocumentVersionID, fact.ConflictStatus, want)
		}
	}
}

func TestRegisterEvidenceFactsDuplicateAndMaxLimit(t *testing.T) {
	ctx := context.Background()
	svc := newFactGuardService(t, factGuardModeWarn)
	tenant := id.New()
	run := createAnswerRun(t, svc, tenant, "request-limits-"+id.New())
	input := RegisterEvidenceFactsInput{AnswerRunID: run.ID, Facts: []EvidenceFactInput{factInput("10,000,000", nil), factInput("10000000", nil)}}
	if _, err := svc.RegisterEvidenceFacts(ctx, tenant, tenant, input); err == nil {
		t.Fatal("duplicate numeric signature must fail")
	}
	input.Facts = make([]EvidenceFactInput, maxEvidenceFacts+1)
	for index := range input.Facts {
		value := "10 million"
		input.Facts[index] = factInput(value, func(fact *EvidenceFactInput) { fact.FactKey = "fact-" + id.New() })
	}
	if _, err := svc.RegisterEvidenceFacts(ctx, tenant, tenant, input); err == nil {
		t.Fatal("more than max facts must fail")
	}
}

func TestValidateEvidenceClaimStatesAndActions(t *testing.T) {
	ctx := context.Background()
	svc := newFactGuardService(t, factGuardModeStrict)
	tenant := id.New()
	run := createAnswerRun(t, svc, tenant, "request-states-"+id.New())
	result, err := svc.RegisterEvidenceFacts(ctx, tenant, tenant, RegisterEvidenceFactsInput{
		AnswerRunID: run.ID, Facts: []EvidenceFactInput{factInput("10,000,000", nil), factInput("12000000", func(input *EvidenceFactInput) { input.FactKey = "cost" })},
	})
	if err != nil {
		t.Fatalf("register evidence facts: %v", err)
	}
	revenue := result.Facts[0]
	cost := result.Facts[1]

	supported, err := svc.ValidateEvidenceClaim(ctx, tenant, tenant, EvidenceClaimInput{
		AnswerRunID: run.ID, LLMClaim: "revenue was ten million", FactIDs: []string{revenue.ID},
		FactKey: "revenue", Value: "10000000", Unit: "USD", TimeRange: "fy2025",
	})
	if err != nil {
		t.Fatalf("validate supported claim: %v", err)
	}
	if supported.ValidationStatus != model.ClaimValidationSupported || supported.Action != model.ClaimActionKeep {
		t.Fatalf("supported claim = %+v", supported)
	}

	contradicted, err := svc.ValidateEvidenceClaim(ctx, tenant, tenant, EvidenceClaimInput{
		AnswerRunID: run.ID, LLMClaim: "revenue was eight million", FactIDs: []string{revenue.ID, cost.ID},
		FactKey: "revenue", Value: "8 million", Unit: "USD", TimeRange: "FY2025",
	})
	if err != nil {
		t.Fatalf("validate contradicted claim: %v", err)
	}
	if contradicted.ValidationStatus != model.ClaimValidationContradicted || contradicted.Action != model.ClaimActionBlock {
		t.Fatalf("contradicted claim = %+v", contradicted)
	}

	unsupported, err := svc.ValidateEvidenceClaim(ctx, tenant, tenant, EvidenceClaimInput{
		AnswerRunID: run.ID, LLMClaim: "profit was ten million", FactIDs: []string{cost.ID},
		FactKey: "profit", Value: "10 million", Unit: "USD", TimeRange: "FY2025",
	})
	if err != nil {
		t.Fatalf("validate unsupported claim: %v", err)
	}
	if unsupported.ValidationStatus != model.ClaimValidationUnsupported || unsupported.Action != model.ClaimActionBlock {
		t.Fatalf("unsupported claim = %+v", unsupported)
	}
}

func TestValidateEvidenceClaimRejectsCrossTenantFacts(t *testing.T) {
	ctx := context.Background()
	svc := newFactGuardService(t, factGuardModeOff)
	tenant := id.New()
	otherTenant := id.New()
	run := createAnswerRun(t, svc, tenant, "request-cross-tenant-"+id.New())
	otherRun := createAnswerRun(t, svc, otherTenant, "request-cross-tenant-other-"+id.New())
	other, err := svc.RegisterEvidenceFacts(ctx, otherTenant, otherTenant, RegisterEvidenceFactsInput{
		AnswerRunID: otherRun.ID, Facts: []EvidenceFactInput{factInput("10 million", nil)},
	})
	if err != nil {
		t.Fatalf("register evidence facts: %v", err)
	}
	if _, err := svc.ValidateEvidenceClaim(ctx, tenant, tenant, EvidenceClaimInput{
		AnswerRunID: run.ID, LLMClaim: "invalid source", FactIDs: []string{other.Facts[0].ID},
		Value: "10 million",
	}); err == nil {
		t.Fatal("cross-tenant fact validation must fail")
	}
}

func TestValidateFactGuardConfig(t *testing.T) {
	if err := ValidateFactGuardConfig("production", factGuardModeOff); err == nil {
		t.Fatal("production off must fail")
	}
	if err := ValidateFactGuardConfig("production", factGuardModeWarn); err != nil {
		t.Fatalf("production warn: %v", err)
	}
	if err := ValidateFactGuardConfig("development", factGuardModeOff); err != nil {
		t.Fatalf("development off: %v", err)
	}
	if err := ValidateFactGuardConfig("development", "invalid"); err == nil {
		t.Fatal("invalid mode must fail")
	}
}

func TestEvidenceConflictClassification(t *testing.T) {
	base := model.FactRegistry{FactKey: "revenue", Value: "10,000,000", Unit: "USD", TimeRange: "FY2025", SourceDocID: "doc-1", LogicalDocumentID: "logical-1", DocumentVersionID: "version-1"}
	tests := []struct {
		name  string
		right model.FactRegistry
		want  string
	}{
		{name: "temporal", right: func() model.FactRegistry {
			fact := base
			fact.DocumentVersionID = "version-2"
			return fact
		}(), want: model.FactConflictTemporal},
		{name: "source", right: func() model.FactRegistry {
			fact := base
			fact.SourceDocID = "doc-2"
			fact.LogicalDocumentID = ""
			fact.DocumentVersionID = "version-2"
			return fact
		}(), want: model.FactConflictSource},
		{name: "value", right: func() model.FactRegistry {
			fact := base
			fact.LogicalDocumentID = ""
			fact.DocumentVersionID = ""
			return fact
		}(), want: model.FactConflictValue},
	}
	for _, item := range tests {
		t.Run(item.name, func(t *testing.T) {
			right := item.right
			right.Value = "8,000,000"
			if !evidenceFactConflict(&base, &right) {
				t.Fatalf("%s facts must conflict", item.name)
			}
			if got := evidenceConflictType(&base, &right); got != item.want {
				t.Fatalf("conflict type = %s, want %s", got, item.want)
			}
		})
	}
}

func TestResolveTemporalFactConflictUsingAsOf(t *testing.T) {
	ctx := context.Background()
	svc := newFactGuardService(t, factGuardModeOff)
	tenant := id.New()
	documentID := id.New()
	if err := svc.Store.CreateLogicalDocument(ctx, &model.LogicalDocument{
		ID: documentID, TenantID: tenant, DatasetID: id.New(), Name: "Policy", CreatedBy: id.New(),
	}); err != nil {
		t.Fatalf("create logical document: %v", err)
	}
	effectiveFrom := time.Now().UTC().Add(-time.Hour)
	effectiveTo := effectiveFrom.Add(30 * time.Minute)
	newTo := effectiveTo.Add(time.Hour)
	versions := []model.DocumentVersion{
		{ID: id.New(), TenantID: tenant, LogicalDocumentID: documentID, RAGFlowDocumentID: "rag-old", Version: 1, Status: model.DocumentVersionSuperseded, EffectiveFrom: effectiveFrom, EffectiveTo: &effectiveTo, ContentHash: "hash-old", CreatedBy: tenant},
		{ID: id.New(), TenantID: tenant, LogicalDocumentID: documentID, RAGFlowDocumentID: "rag-new", Version: 2, Status: model.DocumentVersionActive, EffectiveFrom: effectiveTo, EffectiveTo: &newTo, ContentHash: "hash-new", CreatedBy: tenant},
	}
	for index := range versions {
		if err := svc.Store.CreateDocumentVersion(ctx, &versions[index]); err != nil {
			t.Fatalf("create document version: %v", err)
		}
	}
	left := &model.FactRegistry{ID: id.New(), TenantID: tenant, LogicalDocumentID: documentID, DocumentVersionID: versions[0].ID, FactKey: "revenue", Value: "8,000,000", Unit: "USD", TimeRange: "FY2025", SourceDocID: "doc-1", ClaimType: model.FactClaimTypeExtracted}
	right := &model.FactRegistry{ID: id.New(), TenantID: tenant, LogicalDocumentID: documentID, DocumentVersionID: versions[1].ID, FactKey: "revenue", Value: "10,000,000", Unit: "USD", TimeRange: "FY2025", SourceDocID: "doc-1", ClaimType: model.FactClaimTypeExtracted}
	asOf := effectiveTo.Add(time.Minute)
	resolved, selected, err := svc.resolveTemporalFactConflict(ctx, tenant, left, right, "as_of", &asOf)
	if err != nil || !resolved || selected != right {
		t.Fatalf("as_of resolution = %v, %+v, %v", resolved, selected, err)
	}
	afterAsOf := newTo.Add(time.Minute)
	resolved, selected, err = svc.resolveTemporalFactConflict(ctx, tenant, left, right, "as_of", &afterAsOf)
	if err != nil || resolved || selected != nil {
		t.Fatalf("non-overlap must remain unresolved: %v, %+v, %v", resolved, selected, err)
	}
}
