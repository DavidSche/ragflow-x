package ragflow

import (
	"context"
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
		return nil, err
	}
	return &pipeline, nil
}
