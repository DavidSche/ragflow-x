package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// KnowledgeOpsAppTypeRetrievalTest marks knowledge-ops events produced by the
// interactive dataset retrieval test panel (221 §5.4 C2). It is distinct from
// chat | search | agent so panel debugging stays auditable without polluting
// app traffic statistics.
const KnowledgeOpsAppTypeRetrievalTest = "retrieval_test"

const (
	datasetRetrievalTestDefaultPageSize  = 5
	datasetRetrievalTestMaxPageSize      = 30
	datasetRetrievalTestDefaultThreshold = 0.2
	datasetRetrievalTestDefaultWeight    = 0.3
)

// DatasetRetrievalTestInput carries the interactive retrieval test form.
// Threshold/weight are optional pointers so an explicit 0 (vector-only or
// recall-max tuning) survives instead of collapsing into the defaults.
type DatasetRetrievalTestInput struct {
	Question               string   `json:"question"`
	PageSize               int      `json:"page_size"`
	SimilarityThreshold    *float64 `json:"similarity_threshold"`
	VectorSimilarityWeight *float64 `json:"vector_similarity_weight"`
	Keyword                bool     `json:"keyword"`
	RerankID               string   `json:"rerank_id"`
	DocumentIDs            []string `json:"document_ids"`
}

// DatasetRetrievalTestResult echoes the executed parameters together with the
// upstream chunks so the panel can display exactly what ran.
type DatasetRetrievalTestResult struct {
	Question               string                   `json:"question"`
	PageSize               int                      `json:"page_size"`
	SimilarityThreshold    float64                  `json:"similarity_threshold"`
	VectorSimilarityWeight float64                  `json:"vector_similarity_weight"`
	Keyword                bool                     `json:"keyword"`
	RerankID               string                   `json:"rerank_id"`
	Total                  int64                    `json:"total"`
	Chunks                 []map[string]interface{} `json:"chunks"`
	DurationMs             int64                    `json:"duration_ms"`
}

// TestDatasetRetrieval runs a one-shot interactive retrieval against a single
// dataset (RAGFlow /retrieval proxy) and records every execution as a
// knowledge-ops event so panel debugging stays inside the governance loop.
// Read-path: no approval flow; cross-tenant reads ride the resolved
// TenantScope like every other scoped read.
func (s *Service) TestDatasetRetrieval(
	ctx context.Context, actorID string, scope TenantScope, datasetID string, input DatasetRetrievalTestInput,
) (*DatasetRetrievalTestResult, error) {
	question := strings.TrimSpace(input.Question)
	if question == "" {
		return nil, httperr.BadRequest(40120, "question is required")
	}
	pageSize := input.PageSize
	if pageSize <= 0 {
		pageSize = datasetRetrievalTestDefaultPageSize
	}
	if pageSize > datasetRetrievalTestMaxPageSize {
		return nil, httperr.BadRequest(40120, "page_size must not exceed 30")
	}
	threshold := datasetRetrievalTestDefaultThreshold
	if input.SimilarityThreshold != nil {
		threshold = *input.SimilarityThreshold
	}
	if threshold < 0 || threshold > 1 {
		return nil, httperr.BadRequest(40120, "similarity_threshold must be between 0 and 1")
	}
	weight := datasetRetrievalTestDefaultWeight
	if input.VectorSimilarityWeight != nil {
		weight = *input.VectorSimilarityWeight
	}
	if weight < 0 || weight > 1 {
		return nil, httperr.BadRequest(40120, "vector_similarity_weight must be between 0 and 1")
	}
	rerankID := strings.TrimSpace(input.RerankID)
	documentIDs := make([]string, 0, len(input.DocumentIDs))
	for _, documentID := range input.DocumentIDs {
		documentID = strings.TrimSpace(documentID)
		if documentID == "" {
			return nil, httperr.BadRequest(40120, "document_ids must not contain an empty id")
		}
		documentIDs = append(documentIDs, documentID)
	}

	found, err := s.Store.GetDatasetLinkForScope(ctx, scope.ScopeAll(), scope.RepositoryTenantIDs(), datasetID)
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, httperr.NotFound("dataset not found")
	}

	event := &model.KnowledgeOpsEvent{
		RequestID: id.New(), TenantID: found.TenantID, UserID: actorID,
		AppType: KnowledgeOpsAppTypeRetrievalTest, AppID: found.ID,
		Question:     truncateKnowledgeText(question, 2048),
		QuestionHash: normalizeKnowledgeQuestion(question),
	}
	request := ragflow.RetrieveDatasetsRequest{
		DatasetIDs:             []string{found.RAGFlowDatasetID},
		Question:               question,
		DocumentIDs:            documentIDs,
		Page:                   1,
		PageSize:               pageSize,
		SimilarityThreshold:    &threshold,
		VectorSimilarityWeight: &weight,
		Keyword:                input.Keyword,
		RerankID:               rerankID,
	}
	startedAt := time.Now()
	result, retrievalErr := s.RAGFlow.RetrieveDatasets(ctx, request)
	event.DurationMs = time.Since(startedAt).Milliseconds()
	if retrievalErr != nil {
		event.Status = "failed"
		s.recordKnowledgeEvent(ctx, event)
		return nil, retrievalErr
	}
	if result == nil {
		result = &ragflow.RetrieveDatasetsResult{}
	}
	if result.Chunks == nil {
		result.Chunks = []map[string]interface{}{}
	}
	event.Status = "completed"
	event.CitationsCount = len(result.Chunks)
	if len(result.Chunks) > 0 {
		if content, ok := result.Chunks[0]["content"].(string); ok {
			event.AnswerExcerpt = truncateKnowledgeText(content, 1024)
		}
	}
	s.recordKnowledgeEvent(ctx, event)
	return &DatasetRetrievalTestResult{
		Question: question, PageSize: pageSize,
		SimilarityThreshold: threshold, VectorSimilarityWeight: weight,
		Keyword: input.Keyword, RerankID: rerankID,
		Total: result.Total, Chunks: result.Chunks, DurationMs: event.DurationMs,
	}, nil
}

// GetDatasetGraphForScope returns the read-only knowledge graph for one
// dataset (C6). The graph itself is produced by the engine's compilation
// pipeline; this panel only renders it, so no knowledge-ops event is recorded.
func (s *Service) GetDatasetGraphForScope(ctx context.Context, scope TenantScope, datasetID string) (*ragflow.DatasetKnowledgeGraph, error) {
	dataset, err := s.GetDatasetForScope(ctx, scope, datasetID)
	if err != nil {
		return nil, err
	}
	graph, err := s.RAGFlow.GetDatasetGraph(ctx, dataset.RAGFlowDatasetID)
	if err != nil {
		var re *ragflow.Error
		if errors.As(err, &re) && re.HTTPStatus == http.StatusNotFound {
			return &ragflow.DatasetKnowledgeGraph{Nodes: []ragflow.DatasetKnowledgeGraphNode{}, Edges: []ragflow.DatasetKnowledgeGraphEdge{}}, nil
		}
		return nil, httperr.New(502, 50295, "ragflow get dataset graph failed")
	}
	if graph.Nodes == nil {
		graph.Nodes = []ragflow.DatasetKnowledgeGraphNode{}
	}
	if graph.Edges == nil {
		graph.Edges = []ragflow.DatasetKnowledgeGraphEdge{}
	}
	return graph, nil
}
