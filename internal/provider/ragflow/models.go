package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
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

// SetDefaultModel upserts the tenant default for a model type.
//   - req with ModelID set  -> set default to that model
//   - req all-empty         -> clear the default (RAGFlow's correct clear path)
//
// The body mirrors RAGFlow's SetModelRequest exactly; RAGFlow business errors
// (code != 0, e.g. "model type decision is invalid" or a cross-type mismatch)
// surface through NewErrorFromResponse as *Error with ErrorTypeBusiness so the
// service layer can map them to 400xx / capability feedback without inventing
// a local error vocabulary (doc/269 §4.1).
func (c *HTTPClient) SetDefaultModel(ctx context.Context, req SetDefaultModelRequest) error {
	// Alias normalisation happens once, at the provider boundary (doc/269 §4.1):
	// asr -> speech2text, vision/img2txt -> image2text. Callers pass canonical
	// types; this is a defensive converge so one alias can never reach RAGFlow.
	if a, ok := normalizeDefaultModelAlias(req.ModelType); ok {
		req.ModelType = a
	}
	body, err := json.Marshal(req)
	if err != nil {
		return wrapError("encode set default model request", err)
	}
	return c.do(ctx, http.MethodPatch, "/models/default", bytes.NewReader(body), "application/json", nil)
}

// normalizeDefaultModelAlias maps RAGFlow's accepted aliases onto the canonical
// provider vocabulary, reporting ok=false for an unrecognised type (which must
// stay as-is so RAGFlow itself rejects it with a business error).
func normalizeDefaultModelAlias(t string) (string, bool) {
	switch t {
	case "asr":
		return "speech2text", true
	case "vision", "img2txt":
		return "image2text", true
	default:
		return t, false
	}
}
