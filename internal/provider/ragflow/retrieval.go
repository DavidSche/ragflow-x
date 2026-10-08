package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type RetrieveDatasetsRequest struct {
	DatasetIDs                  []string           `json:"dataset_ids"`
	Question                    string             `json:"question"`
	DocumentIDs                 []string           `json:"document_ids,omitempty"`
	MetadataCondition           *MetadataCondition `json:"metadata_condition,omitempty"`
	IncludeKnowledgeCompilation bool               `json:"include_knowledge_compilation"`
	Page                        int                `json:"page,omitempty"`
	PageSize                    int                `json:"page_size,omitempty"`
	VectorSimilarityWeight      *float64           `json:"vector_similarity_weight,omitempty"`
	Keyword                     bool               `json:"keyword,omitempty"`
	RerankID                    string             `json:"rerank_id,omitempty"`
}

type RetrieveDatasetsResult struct {
	Chunks []map[string]interface{} `json:"chunks"`
	Total  int64                    `json:"total"`
}

type retrievalResponse struct {
	Chunks []map[string]interface{} `json:"chunks"`
	Total  int64                    `json:"total"`
}

func validateRetrieveDatasetsRequest(req RetrieveDatasetsRequest) error {
	if len(req.DatasetIDs) == 0 {
		return protocolError("dataset_ids must not be empty")
	}
	for _, datasetID := range req.DatasetIDs {
		if strings.TrimSpace(datasetID) == "" {
			return protocolError("dataset_ids must not contain an empty id")
		}
	}
	if strings.TrimSpace(req.Question) == "" {
		return protocolError("question is required")
	}
	for _, documentID := range req.DocumentIDs {
		if strings.TrimSpace(documentID) == "" {
			return protocolError("document_ids must not contain an empty id")
		}
	}
	if req.VectorSimilarityWeight != nil &&
		(*req.VectorSimilarityWeight < 0 || *req.VectorSimilarityWeight > 1) {
		return protocolError("vector_similarity_weight must be between 0 and 1")
	}
	if strings.TrimSpace(req.RerankID) == "" && req.RerankID != "" {
		return protocolError("rerank_id must not be empty")
	}
	return nil
}

func protocolError(message string) error {
	return &Error{Type: ErrorTypeProtocol, Message: message}
}

func (c *HTTPClient) RetrieveDatasets(
	ctx context.Context, req RetrieveDatasetsRequest,
) (*RetrieveDatasetsResult, error) {
	if err := validateRetrieveDatasetsRequest(req); err != nil {
		return nil, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, wrapError("encode ragflow retrieval request", err)
	}
	var out retrievalResponse
	if err := c.do(ctx, http.MethodPost, "/retrieval", bytes.NewReader(body), "application/json", &out); err != nil {
		return nil, err
	}
	if out.Chunks == nil {
		out.Chunks = []map[string]interface{}{}
	}
	if out.Total == 0 {
		out.Total = int64(len(out.Chunks))
	}
	return &RetrieveDatasetsResult{Chunks: out.Chunks, Total: out.Total}, nil
}
