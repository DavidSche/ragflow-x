package ragflow

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type CompilationStatus struct {
	State           string     `json:"state"`
	Error           string     `json:"error,omitempty"`
	Inflight        int        `json:"inflight"`
	Backlog         int        `json:"backlog"`
	LastCompletedAt *time.Time `json:"last_completed_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type CompilationTemplate struct {
	ID          string                 `json:"id"`
	Kind        string                 `json:"kind,omitempty"`
	DisplayName string                 `json:"display_name,omitempty"`
	Description string                 `json:"description,omitempty"`
	Config      map[string]interface{} `json:"config,omitempty"`
}

type CompilationTemplateSource string

const (
	CompilationTemplateSourceBuiltins    CompilationTemplateSource = "builtins"
	CompilationTemplateSourceWikiPresets CompilationTemplateSource = "wiki-presets"
)

func (c *HTTPClient) GetCompilationStatus(ctx context.Context, datasetID string) (*CompilationStatus, error) {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return nil, protocolError("dataset_id is required")
	}
	path := "/datasets/" + url.PathEscape(datasetID) + "/compilation/status"
	var status CompilationStatus
	if err := c.do(ctx, http.MethodGet, path, nil, "", &status); err != nil {
		return nil, err
	}
	if status.State == "" {
		status.State = "idle"
	}
	return &status, nil
}

func (c *HTTPClient) ListCompilationTemplates(
	ctx context.Context, source CompilationTemplateSource,
) ([]CompilationTemplate, error) {
	switch source {
	case CompilationTemplateSourceBuiltins:
	case CompilationTemplateSourceWikiPresets:
	default:
		return nil, protocolError("compilation template source must be builtins or wiki-presets")
	}
	path := "/compilation-templates/" + url.PathEscape(string(source))
	var templates []CompilationTemplate
	if err := c.do(ctx, http.MethodGet, path, nil, "", &templates); err != nil {
		return nil, err
	}
	if templates == nil {
		templates = []CompilationTemplate{}
	}
	return templates, nil
}
