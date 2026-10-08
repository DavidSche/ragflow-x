package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

type SearchDatasetRequest struct {
	Question                    string                 `json:"question"`
	DocumentIDs                 []string               `json:"doc_ids,omitempty"`
	Page                        int                    `json:"page,omitempty"`
	Size                        int                    `json:"size,omitempty"`
	TopK                        int                    `json:"top_k,omitempty"`
	SimilarityThreshold         *float64               `json:"similarity_threshold,omitempty"`
	VectorSimilarityWeight      *float64               `json:"vector_similarity_weight,omitempty"`
	UseKG                       bool                   `json:"use_kg,omitempty"`
	CrossLanguages              []string               `json:"cross_languages,omitempty"`
	Keyword                     bool                   `json:"keyword,omitempty"`
	MetadataFilter              map[string]interface{} `json:"meta_data_filter,omitempty"`
	IncludeKnowledgeCompilation bool                   `json:"include_knowledge_compilation"`
}

type SearchDatasetResult struct {
	Chunks []map[string]interface{} `json:"chunks"`
	Total  int64                    `json:"total"`
	Labels []string                 `json:"labels"`
}

type searchDatasetResponse struct {
	Chunks []map[string]interface{} `json:"chunks"`
	Total  int64                    `json:"total"`
	Labels []string                 `json:"labels"`
}

func validateSearchDatasetRequest(datasetID string, req SearchDatasetRequest) error {
	datasetID = strings.TrimSpace(datasetID)
	if strings.TrimSpace(datasetID) == "" {
		return protocolError("dataset_id is required")
	}
	if strings.TrimSpace(req.Question) == "" {
		return protocolError("question is required")
	}
	for _, documentID := range req.DocumentIDs {
		if strings.TrimSpace(documentID) == "" {
			return protocolError("doc_ids must not contain an empty id")
		}
	}
	if req.Page < 0 {
		return protocolError("page must not be negative")
	}
	if req.Size < 0 {
		return protocolError("size must not be negative")
	}
	if req.TopK < 0 {
		return protocolError("top_k must not be negative")
	}
	if req.SimilarityThreshold != nil &&
		(*req.SimilarityThreshold < 0 || *req.SimilarityThreshold > 1) {
		return protocolError("similarity_threshold must be between 0 and 1")
	}
	if req.VectorSimilarityWeight != nil &&
		(*req.VectorSimilarityWeight < 0 || *req.VectorSimilarityWeight > 1) {
		return protocolError("vector_similarity_weight must be between 0 and 1")
	}
	for _, language := range req.CrossLanguages {
		if strings.TrimSpace(language) == "" {
			return protocolError("cross_languages must not contain an empty language")
		}
	}
	return nil
}

func (c *HTTPClient) SearchDataset(
	ctx context.Context, datasetID string, req SearchDatasetRequest,
) (*SearchDatasetResult, error) {
	datasetID = strings.TrimSpace(datasetID)
	if err := validateSearchDatasetRequest(datasetID, req); err != nil {
		return nil, err
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, wrapError("encode ragflow dataset search request", err)
	}
	path := "/datasets/" + url.PathEscape(datasetID) + "/search"
	var out searchDatasetResponse
	if err := c.do(ctx, http.MethodPost, path, bytes.NewReader(body), "application/json", &out); err != nil {
		return nil, err
	}
	if out.Chunks == nil {
		out.Chunks = []map[string]interface{}{}
	}
	if out.Labels == nil {
		out.Labels = []string{}
	}
	if out.Total == 0 {
		out.Total = int64(len(out.Chunks))
	}
	return &SearchDatasetResult{Chunks: out.Chunks, Total: out.Total, Labels: out.Labels}, nil
}
