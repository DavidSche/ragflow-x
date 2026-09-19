package ragflow

import (
	"context"
	"net/http"
	"net/url"
)

// ListModels returns the tenant's added models of the given type
// ("" for all, otherwise "chat", "rerank", "embedding", ...).
func (c *HTTPClient) ListModels(ctx context.Context, modelType string) ([]AddedModel, error) {
	path := "/models"
	if modelType != "" {
		path += "?type=" + url.QueryEscape(modelType)
	}
	var out []AddedModel
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListDefaultModels returns the tenant's RAGFlow default model settings.
func (c *HTTPClient) ListDefaultModels(ctx context.Context) ([]DefaultModel, error) {
	var out struct {
		Models []DefaultModel `json:"models"`
	}
	if err := c.do(ctx, http.MethodGet, "/models/default", nil, "", &out); err != nil {
		return nil, err
	}
	return out.Models, nil
}
