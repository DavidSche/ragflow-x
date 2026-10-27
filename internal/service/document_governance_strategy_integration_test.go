package service

import (
	"context"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

func strategyDataset(t *testing.T, svc *Service, tenant string) (string, string) {
	t.Helper()
	dataset := &model.DatasetLink{
		ID: id.New(), TenantID: tenant, RAGFlowDatasetID: "ragflow-gov-" + id.New()[:8],
		Name: "Governance Exec Dataset", ProjectID: id.New(), PushdownEnabled: true,
	}
	if err := svc.Store.CreateDatasetLink(context.Background(), dataset); err != nil {
		t.Fatal(err)
	}
	input := knowledgeStrategyInput(dataset.ID, model.KnowledgeStrategySemantic)
	input.FallbackStrategy = model.KnowledgeStrategyLexical
	strategy, err := svc.CreateKnowledgeStrategy(context.Background(), tenant, id.New(), input)
	if err != nil {
		t.Fatal(err)
	}
	strategy.ProbeStatus = model.KnowledgeProbeStatusPassed
	if err := svc.Store.UpdateKnowledgeStrategy(context.Background(), strategy); err != nil {
		t.Fatal(err)
	}
	return dataset.ID, strategy.ID
}

func insertAnnotationDirect(
	t *testing.T, svc *Service, tenantID, datasetID, documentID, level, status string,
) {
	t.Helper()
	now := time.Now().UTC()
	by := id.New()
	g := &model.DocumentGovernance{
		ID: id.New(), TenantID: tenantID, DatasetID: datasetID, DocumentID: documentID,
		Level: level, Authority: model.GovernanceDefaultAuthority,
		Status: status, Source: model.GovernanceSourceManual,
		ApprovalState: model.GovernanceApprovalApproved,
		CreatedBy:     by, ApprovedBy: &by, ApprovedAt: &now,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := svc.Store.UpsertDocumentGovernance(context.Background(), g); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteKnowledgeStrategyEmptySetGuardShortCircuitsBeforeEngine(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	dsID, strategyID := strategyDataset(t, svc, tenant)
	insertAnnotationDirect(t, svc, tenant, dsID, "doc-secret", model.GovernanceLevelTopSecret, model.GovernanceStatusPublished)
	insertAnnotationDirect(t, svc, tenant, dsID, "doc-conf", model.GovernanceLevelConfidential, model.GovernanceStatusPublished)

	stub := &knowledgeExecutionStub{chunks: []map[string]interface{}{{"document_id": "doc-secret"}}}
	svc.RAGFlow = stub

	// user has default internal clearance; retrieval domain is fully excluded.
	result, err := svc.ExecuteKnowledgeStrategy(ctx, tenant, user, strategyID, KnowledgeStrategyRetrieveInput{
		Question:    "What is sensitive?",
		DocumentIDs: []string{"doc-secret", "doc-conf"},
		PageSize:    intPtr(10),
	})
	if err != nil {
		t.Fatalf("empty-set guard must NOT surface an error: %v", err)
	}
	if !result.GovernanceFilteredOut {
		t.Fatalf("expected governance_filtered_out=true, got %+v", result)
	}
	if result.Total != 0 || len(result.Chunks) != 0 {
		t.Fatalf("empty-set guard must return no chunks, got %+v", result)
	}
	if result.GovernanceEmptyMessage != GovernanceEmptySetMessage {
		t.Fatalf("expected empty-set message, got %q", result.GovernanceEmptyMessage)
	}
	if len(stub.requests) != 0 {
		t.Fatalf("empty-set guard must short-circuit BEFORE the engine, requests=%d", len(stub.requests))
	}
}

func TestExecuteKnowledgeStrategyGovernancePushdownAndSecondaryFilter(t *testing.T) {
	ctx := context.Background()
	svc := newToolRegistryService(t)
	tenant, user := id.New(), id.New()
	dsID, strategyID := strategyDataset(t, svc, tenant)
	insertAnnotationDirect(t, svc, tenant, dsID, "doc-a", model.GovernanceLevelInternal, model.GovernanceStatusPublished)
	insertAnnotationDirect(t, svc, tenant, dsID, "doc-b", model.GovernanceLevelTopSecret, model.GovernanceStatusPublished)

	stub := &knowledgeExecutionStub{chunks: []map[string]interface{}{
		{"document_id": "doc-a", "content": "visible"},
		{"document_id": "doc-b", "content": "leaked-by-engine"},
	}}
	svc.RAGFlow = stub

	result, err := svc.ExecuteKnowledgeStrategy(ctx, tenant, user, strategyID, KnowledgeStrategyRetrieveInput{
		Question: "What changed?",
		PageSize: intPtr(10),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.GovernanceFilteredOut {
		t.Fatalf("doc-a remains visible, must not be empty-filtered")
	}
	if !result.GovernanceApplied {
		t.Fatalf("expected governance applied, got %+v", result)
	}
	if result.GovernanceDropped != 1 {
		t.Fatalf("secondary filter must drop the leaked doc-b, dropped=%d", result.GovernanceDropped)
	}
	if len(result.Chunks) != 1 || result.Chunks[0]["document_id"] != "doc-a" {
		t.Fatalf("result must contain only doc-a, got %+v", result.Chunks)
	}
	// Engine request must carry the governance condition merged in.
	if len(stub.requests) != 1 {
		t.Fatalf("expected exactly one engine request, got %d", len(stub.requests))
	}
	req := stub.requests[0]
	var found bool
	if req.MetadataCondition != nil {
		for _, op := range req.MetadataCondition.Conditions {
			if op.Name == metadataKeyGovernanceLevel && op.ComparisonOperator == operatorNotEqual && op.Value == model.GovernanceLevelTopSecret {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("engine request must carry rgx_governance_level != top_secret pushdown, got %+v", req.MetadataCondition)
	}
}
