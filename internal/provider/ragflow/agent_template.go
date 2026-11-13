package ragflow

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// AgentTemplate is one entry of RAGFlow's canvas template catalogue
// (`GET /api/v1/agents/templates`). The upstream endpoint returns rows of the
// `canvas_template` table: an i18n title/description map, a canvas category
// (agent_canvas / dataflow_canvas / ...) and the engineering DSL that cloning
// the template produces.
//
// Scenario Pack 2.0 uses this catalogue as the "upstream skeleton" read-only
// source (doc/276 §8.4): skeletons are shown and previewed, and importing one
// produces a *draft* that must pass schema + governance validation before it
// can be published. They are never instantiated directly.
type AgentTemplate struct {
	ID             string                 `json:"id"`
	Title          map[string]interface{} `json:"title"`
	Description    map[string]interface{} `json:"description"`
	CanvasType     string                 `json:"canvas_type,omitempty"`
	CanvasTypes    []interface{}          `json:"canvas_types,omitempty"`
	CanvasCategory string                 `json:"canvas_category"`
	DSL            map[string]interface{} `json:"dsl,omitempty"`
}

// AgentTemplateTitle resolves the i18n title for one locale, falling back to
// zh-CN then to the first non-empty value. Upstream stores `{"en": "...",
// "zh": "..."}` (or an empty object), so callers must never assume a key.
func (t AgentTemplate) AgentTemplateTitle(locale string) string {
	return localizedCanvasText(t.Title, locale)
}

// AgentTemplateDescription resolves the i18n description with the same fallback
// order as the title.
func (t AgentTemplate) AgentTemplateDescription(locale string) string {
	return localizedCanvasText(t.Description, locale)
}

// PickCanvasType returns the canvas type the template produces, preferring the
// scalar `canvas_type` and falling back to the first entry of `canvas_types`.
func (t AgentTemplate) PickCanvasType() string {
	if value := strings.TrimSpace(t.CanvasType); value != "" {
		return value
	}
	for _, entry := range t.CanvasTypes {
		if text, ok := entry.(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func localizedCanvasText(values map[string]interface{}, locale string) string {
	if len(values) == 0 {
		return ""
	}
	order := []string{locale, "zh", "zh-CN", "en"}
	for _, key := range order {
		if key == "" {
			continue
		}
		if text, ok := values[key].(string); ok && text != "" {
			return text
		}
	}
	for _, value := range values {
		if text, ok := value.(string); ok && text != "" {
			return text
		}
	}
	return ""
}

// GetAgentTemplate reads one canvas template including its DSL.
//
// The single-row read is the preferred path: the list endpoint may omit `dsl` to
// keep the payload small, so import re-reads the row before materialising a
// draft. RAGFlow v1.0.0 however does not implement that route — a live drill on
// 4.151 answered 404 for every id while the catalogue itself listed them with
// `dsl` — so a not-found is answered from the catalogue instead of turning a
// working feature into a 50303. Any other failure is still returned as-is.
func (c *HTTPClient) GetAgentTemplate(ctx context.Context, id string) (AgentTemplate, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return AgentTemplate{}, protocolError("agent template id is required")
	}
	var template AgentTemplate
	err := c.do(ctx, http.MethodGet, "/agents/templates/"+url.PathEscape(id), nil, "", &template)
	if err == nil {
		return template, nil
	}
	if !isNotFoundError(err) {
		return AgentTemplate{}, err
	}
	templates, listErr := c.ListAgentTemplates(ctx)
	if listErr != nil {
		// Keep the original single-read error: it names the route we tried.
		return AgentTemplate{}, err
	}
	for _, candidate := range templates {
		if candidate.ID == id {
			return candidate, nil
		}
	}
	return AgentTemplate{}, &Error{
		Type: ErrorTypeProtocol, Code: http.StatusNotFound, HTTPStatus: http.StatusNotFound,
		Message: "agent template " + id + " not found in the upstream catalogue",
	}
}

// isNotFoundError reports whether err is upstream's "route not found" answer.
// RAGFlow returns the framework's 404 envelope in that case, so both the HTTP
// status and the business code are checked.
func isNotFoundError(err error) bool {
	var upstream *Error
	if !errors.As(err, &upstream) {
		return false
	}
	return upstream.HTTPStatus == http.StatusNotFound || upstream.Code == http.StatusNotFound
}

// ListAgentTemplates reads the canvas template catalogue. The endpoint sits
// behind the tenant login but has no dataset/agent side effect, so it is safe to
// call from the read-only skeleton aggregation.
func (c *HTTPClient) ListAgentTemplates(ctx context.Context) ([]AgentTemplate, error) {
	var templates []AgentTemplate
	if err := c.do(ctx, http.MethodGet, "/agents/templates", nil, "", &templates); err != nil {
		return nil, err
	}
	if templates == nil {
		templates = []AgentTemplate{}
	}
	return templates, nil
}
