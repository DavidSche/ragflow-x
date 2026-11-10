package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

type PipelineTemplate struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Filename    string `json:"filename,omitempty"`
}

type PipelineTemplateDetail struct {
	DSL map[string]interface{} `json:"dsl"`
}

// PipelineVersion is an immutable canvas snapshot published from a dataflow
// canvas (agent). RAGFlow v1.0.0 no longer serves user pipelines from
// /pipelines (built-in templates only) and rejects Pipeline-mode dataset
// creation unless the request pins `pipeline_version_id` to one of these rows,
// so every Pipeline-mode dataset must publish the canvas first.
type PipelineVersion struct {
	ID           string                 `json:"id"`
	UserCanvasID string                 `json:"user_canvas_id"`
	Title        string                 `json:"title,omitempty"`
	Alias        string                 `json:"alias,omitempty"`
	Release      bool                   `json:"release"`
	DSL          map[string]interface{} `json:"dsl,omitempty"`
}

// PublishPipelineVersion publishes the canvas DSL as a new immutable version
// row (POST /agents/{canvas_id}/publish) and returns it. The returned ID is the
// `pipeline_version_id` that Pipeline-mode dataset creation requires.
func (c *HTTPClient) PublishPipelineVersion(
	ctx context.Context, canvasID, title string, dsl map[string]interface{},
) (*PipelineVersion, error) {
	canvasID = strings.TrimSpace(canvasID)
	if canvasID == "" {
		return nil, protocolError("canvas id is required to publish a pipeline version")
	}
	payload := map[string]interface{}{}
	if strings.TrimSpace(title) != "" {
		payload["title"] = title
	}
	if len(dsl) > 0 {
		payload["dsl"] = dsl
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, wrapError("encode pipeline version publish", err)
	}
	var out PipelineVersion
	path := "/agents/" + url.PathEscape(canvasID) + "/publish"
	if err := c.do(ctx, http.MethodPost, path, bytes.NewReader(body), "application/json", &out); err != nil {
		return nil, err
	}
	if strings.TrimSpace(out.ID) == "" {
		return nil, protocolError("ragflow pipeline version publish returned an empty version id")
	}
	return &out, nil
}

type pipelineListResponse struct {
	Canvas []PipelineTemplate `json:"canvas"`
}

func (c *HTTPClient) ListPipelines(ctx context.Context) ([]PipelineTemplate, error) {
	var response pipelineListResponse
	if err := c.do(ctx, http.MethodGet, "/pipelines", nil, "", &response); err != nil {
		return nil, err
	}
	if response.Canvas == nil {
		response.Canvas = []PipelineTemplate{}
	}
	return response.Canvas, nil
}

func (c *HTTPClient) GetPipeline(ctx context.Context, pipelineID string) (*PipelineTemplateDetail, error) {
	pipelineID = strings.TrimSpace(pipelineID)
	if pipelineID == "" {
		return nil, protocolError("pipeline_id is required")
	}
	var pipeline PipelineTemplateDetail
	path := "/pipelines/" + url.PathEscape(pipelineID)
	if err := c.do(ctx, http.MethodGet, path, nil, "", &pipeline); err != nil {
		var providerErr *Error
		if !errors.As(err, &providerErr) || providerErr.Type != ErrorTypeBusiness ||
			(providerErr.HTTPStatus != http.StatusNotFound && providerErr.Code != 102) {
			return nil, err
		}
		return c.getUserPipeline(ctx, pipelineID)
	}
	return &pipeline, nil
}

type userPipelineResponse struct {
	ID  string                 `json:"id"`
	DSL map[string]interface{} `json:"dsl"`
}

func (c *HTTPClient) getUserPipeline(ctx context.Context, pipelineID string) (*PipelineTemplateDetail, error) {
	var response userPipelineResponse
	path := "/agents/" + url.PathEscape(pipelineID)
	if err := c.do(ctx, http.MethodGet, path, nil, "", &response); err != nil {
		return nil, err
	}
	if response.DSL == nil {
		return nil, &Error{
			Type:       ErrorTypeBusiness,
			HTTPStatus: http.StatusNotFound,
			Code:       102,
			Message:    "pipeline not found",
		}
	}
	return &PipelineTemplateDetail{DSL: response.DSL}, nil
}
