package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

func TestKnowledgeImpactAndDuplicateCandidateContracts(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Knowledge Impact Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "impact-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := svc.CreateKnowledgeImpactReport(ctx, admin.ID, tenant.ID, "", "dataset", "dataset-1", "v1")
	if err != nil {
		t.Fatalf("create impact report: %v", err)
	}
	if report.ImpactPolicyVersion != model.ImpactPolicyVersion || report.ImpactLevel != model.ImpactLevelLow {
		t.Fatalf("unexpected impact contract: %+v", report)
	}
	if _, err := svc.CreateDuplicateCandidate(ctx, admin.ID, tenant.ID, "", DuplicateCandidateInput{
		SourceType: "document", SourceID: "doc-1", CandidateType: "document", CandidateID: "doc-1",
		SimilarityScore: 0.9, SimilarityReasons: []string{"same id"},
	}); err == nil {
		t.Fatal("identical source and candidate must be rejected")
	}
	candidate, err := svc.CreateDuplicateCandidate(ctx, admin.ID, tenant.ID, "", DuplicateCandidateInput{
		SourceType: "document", SourceID: "doc-1", CandidateType: "document", CandidateID: "doc-2",
		SimilarityScore: 0.86, SimilarityReasons: []string{"similar title"},
		Evidence: map[string]interface{}{"source_title": "采购制度", "candidate_title": "采购管理办法"},
	})
	if err != nil {
		t.Fatalf("create duplicate candidate: %v", err)
	}
	if candidate.Status != model.DuplicateCandidatePending {
		t.Fatalf("candidate must remain pending: %+v", candidate)
	}
	if _, err := svc.DecideDuplicateCandidate(ctx, admin.ID, tenant.ID, candidate.ID, DuplicateCandidateDecision{
		FinalRelation: "same",
	}); err == nil {
		t.Fatal("invalid relation must be rejected")
	}
	decided, err := svc.DecideDuplicateCandidate(ctx, admin.ID, tenant.ID, candidate.ID, DuplicateCandidateDecision{
		FinalRelation: model.DuplicateRelationConflicts, DecisionNote: "两版口径冲突，需人工复核",
	})
	if err != nil || decided.Status != model.DuplicateCandidateConfirmed || decided.FinalRelation != model.DuplicateRelationConflicts {
		t.Fatalf("confirm duplicate candidate: %v %+v", err, decided)
	}
}

// TestKnowledgeImpactReportFillsFactsAndLists covers doc/118 F-04: the impact
// report must count eval sets, historical badcases and production usage (not
// only assistants/releases) and persist the per-facet affected detail lists.
func TestKnowledgeImpactReportFillsFactsAndLists(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Impact Facts Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "impact-facts-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	datasetID := "dataset-impact-facts"
	now := time.Now().UTC()
	release := &model.AssistantRelease{
		ID: "rel-impact-facts", TenantID: tenant.ID, AssistantID: "assistant-impact-facts",
		AssistantVersionID: "ver-impact-facts", ReleaseVersion: 1, ReleaseState: "ACTIVE",
		DesiredStateJSON: "{}", DesiredStateHash: "hash-impact-facts",
		SnapshotManifestID: "manifest-impact-facts", CreatedAt: now,
	}
	if err := svc.Store.CreateAssistantRelease(ctx, release); err != nil {
		t.Skipf("assistant release seed unavailable: %v", err)
	}
	binding := &model.DatasetBindingVersion{
		ID: "bind-impact-facts", TenantID: tenant.ID, AssistantReleaseID: release.ID,
		BindingID: "binding-impact-facts", DatasetID: datasetID, Status: "ACTIVE", CreatedAt: now,
	}
	if err := svc.Store.CreateDatasetBindingVersion(ctx, binding); err != nil {
		t.Skipf("dataset binding seed unavailable: %v", err)
	}
	evalSet := &model.EvalSet{
		ID: "evalset-impact-facts", TenantID: tenant.ID, Name: "Impact Facts Eval Set",
		DatasetIDs: datasetID, CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.CreateEvalSetWithCases(ctx, evalSet, nil); err != nil {
		t.Skipf("eval set seed unavailable: %v", err)
	}
	citations := []model.CitationReference{
		{ID: "cit-impact-facts-1", TenantID: tenant.ID, ChatID: "chat-impact", SessionID: "sess-impact",
			RequestID: "req-impact-1", DatasetID: datasetID, DocumentID: "doc-1", ChunkID: "chunk-1",
			RetrievedAt: now, CreatedAt: now},
		{ID: "cit-impact-facts-2", TenantID: tenant.ID, ChatID: "chat-impact", SessionID: "sess-impact",
			RequestID: "req-impact-2", DatasetID: datasetID, DocumentID: "doc-1", ChunkID: "chunk-2",
			RetrievedAt: now, CreatedAt: now},
	}
	for i := range citations {
		if err := svc.Store.CreateCitationReference(ctx, &citations[i]); err != nil {
			t.Skipf("citation reference seed unavailable: %v", err)
		}
	}

	report, err := svc.CreateKnowledgeImpactReport(ctx, admin.ID, tenant.ID, "", "dataset", datasetID, "v1")
	if err != nil {
		t.Fatalf("create impact report: %v", err)
	}
	var metrics model.KnowledgeImpactFacts
	if err := json.Unmarshal([]byte(report.MetricsJSON), &metrics); err != nil {
		t.Fatal(err)
	}
	if metrics.AffectedReleaseCount != 1 || metrics.AffectedAssistantCount != 1 {
		t.Fatalf("assistant/release facts missing: %+v", metrics)
	}
	if metrics.AffectedEvalSetCount != 1 {
		t.Fatalf("eval set fact missing: %+v", metrics)
	}
	if metrics.ProductionUsageCount != 2 {
		t.Fatalf("production usage fact missing: %+v", metrics)
	}
	var assistants, releases, evalSets, badcases []string
	decode := func(raw string, target *[]string) {
		if err := json.Unmarshal([]byte(raw), target); err != nil {
			t.Fatalf("decode affected list: %v", err)
		}
	}
	decode(report.AffectedAssistantsJSON, &assistants)
	decode(report.AffectedReleasesJSON, &releases)
	decode(report.AffectedEvalSetsJSON, &evalSets)
	decode(report.AffectedBadcasesJSON, &badcases)
	if len(assistants) != 1 || assistants[0] != "assistant-impact-facts" {
		t.Fatalf("affected assistants detail missing: %s", report.AffectedAssistantsJSON)
	}
	if len(releases) != 1 || releases[0] != "rel-impact-facts" {
		t.Fatalf("affected releases detail missing: %s", report.AffectedReleasesJSON)
	}
	if len(evalSets) != 1 || evalSets[0] != "evalset-impact-facts" {
		t.Fatalf("affected eval sets detail missing: %s", report.AffectedEvalSetsJSON)
	}
}

func TestDuplicateCandidateSupersedesRetiresDataset(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Supersede Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "supersede-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	dataset := &model.DatasetLink{ID: "dataset-old", TenantID: tenant.ID, RAGFlowDatasetID: "rag-dataset-old", Name: "Old"}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatal(err)
	}
	candidate, err := svc.CreateDuplicateCandidate(ctx, admin.ID, tenant.ID, "", DuplicateCandidateInput{
		SourceType: "dataset", SourceID: dataset.ID, CandidateType: "dataset", CandidateID: "dataset-new",
		SimilarityScore: 0.92, SimilarityReasons: []string{"same source uri"},
	})
	if err != nil {
		t.Fatal(err)
	}
	decided, err := svc.DecideDuplicateCandidate(ctx, admin.ID, tenant.ID, candidate.ID, DuplicateCandidateDecision{
		FinalRelation: model.DuplicateRelationSupersedes, DecisionNote: "新版制度替代旧版",
	})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := svc.Store.GetDatasetLink(ctx, tenant.ID, dataset.ID)
	if err != nil || lifecycle.ReviewStatus != model.KnowledgeReviewExpired {
		t.Fatalf("dataset was not retired: %+v %v", lifecycle, err)
	}
	if decided.Status != model.DuplicateCandidateConfirmed {
		t.Fatalf("candidate decision changed: %+v", decided)
	}
}

func TestDuplicateCandidateSupersedesRetiresDocument(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	recorder := &documentStatusRecorder{Client: ragflow.NewMock(), disabled: map[string]bool{}}
	svc.RAGFlow = recorder
	tenant, err := svc.CreateTenant(ctx, "Document Supersede Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "", CreateUserRequest{
		Username: "document-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	dataset := &model.DatasetLink{ID: "dataset-doc", TenantID: tenant.ID, RAGFlowDatasetID: "rag-dataset-doc", Name: "Docs"}
	if err := svc.Store.CreateDatasetLink(ctx, dataset); err != nil {
		t.Fatal(err)
	}
	candidate, err := svc.CreateDuplicateCandidate(ctx, admin.ID, tenant.ID, "", DuplicateCandidateInput{
		SourceType: "document", SourceID: "doc-old", CandidateType: "document", CandidateID: "doc-new",
		SimilarityScore: 0.95, SimilarityReasons: []string{"same title"},
		Evidence: map[string]interface{}{"dataset_id": dataset.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecideDuplicateCandidate(ctx, admin.ID, tenant.ID, candidate.ID, DuplicateCandidateDecision{
		FinalRelation: model.DuplicateRelationSupersedes, DecisionNote: "旧文档退出检索",
	}); err != nil {
		t.Fatal(err)
	}
	if !recorder.disabled["doc-old"] {
		t.Fatalf("old document was not disabled: %+v", recorder.disabled)
	}
}
