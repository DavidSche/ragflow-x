package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/jwt"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func newRetrievalTestSvc(t *testing.T) *Service {
	t.Helper()
	gdb, err := db.Open(config.Database{Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "retrieval-test.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatal(err)
	}
	svc := New(repository.NewStore(gdb), ragflow.NewMock(), jwt.NewManager("secret", 24), "retrieval-test-secret")
	if err := svc.BootstrapAdmin(context.Background(), "admin", "admin123"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Store.Close() })
	return svc
}

func retrievalTestScope(svc *Service) (TenantScope, string) {
	tenant, err := svc.CreateTenant(context.Background(), "Retrieval Test Tenant")
	if err != nil {
		return TenantScope{}, ""
	}
	return TenantScope{
		Kind: TenantScopeCurrent, ActorTenantID: tenant.ID,
		TargetTenantID: tenant.ID, AllowedTenantIDs: []string{tenant.ID},
	}, tenant.ID
}

func TestDatasetRetrievalRecordsKnowledgeOpsEvent(t *testing.T) {
	svc := newRetrievalTestSvc(t)
	scope, _ := retrievalTestScope(svc)
	dataset, err := svc.CreateDataset(context.Background(), scope.ActorTenantID, "retrieval test ds")
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.TestDatasetRetrieval(context.Background(), "actor-1", scope, dataset.ID, DatasetRetrievalTestInput{
		Question: "what is the policy?", PageSize: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || len(result.Chunks) != 2 {
		t.Fatalf("retrieval result total=%d chunks=%d, want 2", result.Total, len(result.Chunks))
	}
	if result.SimilarityThreshold != datasetRetrievalTestDefaultThreshold ||
		result.VectorSimilarityWeight != datasetRetrievalTestDefaultWeight {
		t.Fatalf("defaults not applied: %+v", result)
	}
	events, _, err := svc.ListKnowledgeOpsEvents(context.Background(), scope.ActorTenantID, false, 1, 10, repository.KnowledgeOpsFilter{AppType: KnowledgeOpsAppTypeRetrievalTest})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("knowledge ops events = %d, want 1", len(events))
	}
	event := events[0]
	if event.AppID != dataset.ID || event.Status != "completed" || event.CitationsCount != 2 {
		t.Fatalf("unexpected event: app=%q appID=%q status=%q citations=%d",
			event.AppType, event.AppID, event.Status, event.CitationsCount)
	}
	if event.Question != "what is the policy?" || event.UserID != "actor-1" {
		t.Fatalf("unexpected event question/user: %q %q", event.Question, event.UserID)
	}
	// DurationMs is recorded on the event; on Windows the mock answers within
	// one clock tick so the value itself may legitimately be 0.
	if result.DurationMs < 0 || event.DurationMs < 0 {
		t.Fatalf("negative duration: event=%d result=%d", event.DurationMs, result.DurationMs)
	}
}

func TestDatasetRetrievalPassesTuningParamsToProvider(t *testing.T) {
	svc := newRetrievalTestSvc(t)
	scope, _ := retrievalTestScope(svc)
	dataset, err := svc.CreateDataset(context.Background(), scope.ActorTenantID, "tuning ds")
	if err != nil {
		t.Fatal(err)
	}
	threshold, weight := 0.5, 0.8
	if _, err := svc.TestDatasetRetrieval(context.Background(), "actor-1", scope, dataset.ID, DatasetRetrievalTestInput{
		Question: "tuning", PageSize: 10,
		SimilarityThreshold: &threshold, VectorSimilarityWeight: &weight,
		Keyword: true, RerankID: "rerank-1", DocumentIDs: []string{"doc-9"},
	}); err != nil {
		t.Fatal(err)
	}
	sent := svc.RAGFlow.(*ragflow.Mock).LastRetrievalRequest()
	if sent == nil {
		t.Fatal("provider did not receive the retrieval request")
	}
	if len(sent.DatasetIDs) != 1 || sent.DatasetIDs[0] == dataset.ID {
		t.Fatalf("must send the RAGFlow dataset id, got %v", sent.DatasetIDs)
	}
	if sent.SimilarityThreshold == nil || *sent.SimilarityThreshold != 0.5 ||
		sent.VectorSimilarityWeight == nil || *sent.VectorSimilarityWeight != 0.8 {
		t.Fatalf("threshold/weight not passed through: %+v", sent)
	}
	if sent.PageSize != 10 || !sent.Keyword || sent.RerankID != "rerank-1" {
		t.Fatalf("pagination/keyword/rerank not passed through: %+v", sent)
	}
	if len(sent.DocumentIDs) != 1 || sent.DocumentIDs[0] != "doc-9" {
		t.Fatalf("document_ids not passed through: %+v", sent)
	}
}

// failingRetrieveClient forces a provider-level retrieval error so the
// failed-event path can be exercised deterministically.
type failingRetrieveClient struct {
	ragflow.Mock
}

func (f *failingRetrieveClient) RetrieveDatasets(
	ctx context.Context, req ragflow.RetrieveDatasetsRequest,
) (*ragflow.RetrieveDatasetsResult, error) {
	return nil, &ragflow.Error{Type: ragflow.ErrorTypeProtocol, Message: "engine unavailable"}
}

func TestDatasetRetrievalFailureRecordsFailedEvent(t *testing.T) {
	svc := newRetrievalTestSvc(t)
	svc.RAGFlow = &failingRetrieveClient{Mock: *ragflow.NewMock()}
	scope, _ := retrievalTestScope(svc)
	dataset, err := svc.CreateDataset(context.Background(), scope.ActorTenantID, "failing ds")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TestDatasetRetrieval(context.Background(), "actor-1", scope, dataset.ID, DatasetRetrievalTestInput{
		Question: "does not matter",
	}); err == nil {
		t.Fatal("provider failure must propagate")
	}
	events, _, err := svc.ListKnowledgeOpsEvents(context.Background(), scope.ActorTenantID, false, 1, 10, repository.KnowledgeOpsFilter{AppType: KnowledgeOpsAppTypeRetrievalTest})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Status != "failed" {
		t.Fatalf("failed retrieval must emit one failed event, got %d", len(events))
	}
}

func TestDatasetRetrievalNotFoundAndValidation(t *testing.T) {
	svc := newRetrievalTestSvc(t)
	scope, tenantID := retrievalTestScope(svc)
	if _, err := svc.TestDatasetRetrieval(context.Background(), "actor-1", scope, "missing", DatasetRetrievalTestInput{Question: "q"}); err != nil {
		var httpErr *httperr.Error
		if !errors.As(err, &httpErr) || httpErr.Status != 404 {
			t.Fatalf("missing dataset must 404, got %v", err)
		}
	}
	cases := []struct {
		name  string
		input DatasetRetrievalTestInput
	}{
		{"blank question", DatasetRetrievalTestInput{Question: "   "}},
		{"page_size over 30", DatasetRetrievalTestInput{Question: "q", PageSize: 31}},
		{"threshold above one", func() DatasetRetrievalTestInput {
			v := 1.5
			return DatasetRetrievalTestInput{Question: "q", SimilarityThreshold: &v}
		}()},
		{"weight below zero", func() DatasetRetrievalTestInput {
			v := -0.1
			return DatasetRetrievalTestInput{Question: "q", VectorSimilarityWeight: &v}
		}()},
		{"blank document id", DatasetRetrievalTestInput{Question: "q", DocumentIDs: []string{" "}}},
	}
	dataset, err := svc.CreateDataset(context.Background(), tenantID, "validation ds")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.TestDatasetRetrieval(context.Background(), "actor-1", scope, dataset.ID, tc.input)
			var httpErr *httperr.Error
			if !errors.As(err, &httpErr) || httpErr.Status != 400 {
				t.Fatalf("expected 400, got %v", err)
			}
			if !strings.Contains(httpErr.Message, "must") && !strings.Contains(httpErr.Message, "required") {
				t.Fatalf("expected validation message, got %q", httpErr.Message)
			}
		})
	}
}

func TestGetDatasetGraphForScopeReturnsMockGraph(t *testing.T) {
	svc := newRetrievalTestSvc(t)
	scope, _ := retrievalTestScope(svc)
	dataset, err := svc.CreateDataset(context.Background(), scope.ActorTenantID, "graph ds")
	if err != nil {
		t.Fatal(err)
	}
	graph, err := svc.GetDatasetGraphForScope(context.Background(), scope, dataset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 2 || len(graph.Edges) != 1 {
		t.Fatalf("mock graph shape mismatch: %d nodes, %d edges", len(graph.Nodes), len(graph.Edges))
	}
}

func TestGetDatasetGraphForScopeRequiresDataset(t *testing.T) {
	svc := newRetrievalTestSvc(t)
	scope, _ := retrievalTestScope(svc)
	if _, err := svc.GetDatasetGraphForScope(context.Background(), scope, "missing-dataset"); err == nil {
		t.Fatal("expected error for missing dataset")
	}
}
