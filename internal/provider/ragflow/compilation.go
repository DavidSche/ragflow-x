package ragflow

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type CompilationStatus struct {
	Kind            string     `json:"kind,omitempty"`
	State           string     `json:"state"`
	Error           string     `json:"error,omitempty"`
	Progress        float64    `json:"progress"`
	CurrentPhase    string     `json:"current_phase,omitempty"`
	ProgressMsg     string     `json:"progress_msg,omitempty"`
	Inflight        int        `json:"inflight"`
	Backlog         int        `json:"backlog"`
	LastCompletedAt *time.Time `json:"last_completed_at,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// Compilation status states are frozen from RAGFlow v1.0.0-rc1
// internal/entity/knowledge_compile_doc.go. Error is diagnostic metadata, not
// a lifecycle state.
var knownCompilationStatusStates = map[string]struct{}{
	"idle":      {},
	"pending":   {},
	"running":   {},
	"completed": {},
}

func KnownCompilationStatusStates() []string {
	return []string{"idle", "pending", "running", "completed"}
}

func IsKnownCompilationStatusState(state string) bool {
	_, ok := knownCompilationStatusStates[state]
	return ok
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
	return c.GetCompilationStatusForKind(ctx, datasetID, "")
}

func (c *HTTPClient) GetCompilationStatusForKind(
	ctx context.Context, datasetID, kind string,
) (*CompilationStatus, error) {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return nil, protocolError("dataset_id is required")
	}
	kind = strings.TrimSpace(kind)
	path := "/datasets/" + url.PathEscape(datasetID) + "/compilation/status"
	if kind != "" {
		path += "?kind=" + url.QueryEscape(kind)
	}
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
