package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

type TraceRunInput struct {
	TraceID            string         `json:"trace_id"`
	SpanID             string         `json:"span_id"`
	OtelSpanID         string         `json:"otel_span_id"`
	RequestID          string         `json:"request_id"`
	SessionID          string         `json:"session_id"`
	UserID             string         `json:"user_id"`
	ProjectID          string         `json:"project_id"`
	AssistantID        string         `json:"assistant_id"`
	AssistantReleaseID string         `json:"assistant_release_id"`
	AppType            string         `json:"app_type"`
	AppID              string         `json:"app_id"`
	Channel            string         `json:"channel"`
	Status             string         `json:"status"`
	RouteSummary       map[string]any `json:"route_summary"`
	RetrievalSummary   map[string]any `json:"retrieval_summary"`
	ModelSummary       map[string]any `json:"model_summary"`
	ToolSummary        map[string]any `json:"tool_summary"`
	GovernanceSummary  map[string]any `json:"governance_summary"`
	QualitySummary     map[string]any `json:"quality_summary"`
	EvidencePointers   map[string]any `json:"evidence_pointers"`
}

var traceSummaryFields = map[string]map[string]bool{
	"route": {
		"candidate_count": true, "policy": true, "selection": true,
		"bootstrap": true, "decision_pointer": true,
	},
	"retrieval": {
		"dataset_binding_version": true, "topn_count": true,
		"citation_ids": true, "error_code": true,
	},
	"model": {
		"model_version": true, "error_code": true,
		"duration_summary": true, "token_summary_pointer": true,
	},
	"tool": {
		"tool_version": true, "side_effect_risk": true, "result_status": true,
		"input_pointer": true, "output_pointer": true,
	},
	"governance": {
		"approval_id": true, "quota_summary": true, "rate_limit_summary": true,
		"permission_decision": true, "sensitive_policy_version": true,
	},
	"quality": {
		"feedback_id": true, "feedback_rating": true, "badcause": true, "attribution": true,
		"eval_case_id": true, "assistant_release_id": true, "knowledge_ops_event_id": true,
		"citations_count": true, "duration_ms": true, "tokens_in": true, "tokens_out": true,
	},
}

func validateTraceSummaryValue(value any) error {
	switch value := value.(type) {
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
	case string:
		if len(value) > 512 {
			return httperr.BadRequest(40099, "trace run summary value is too long")
		}
	case []any:
		if len(value) > 100 {
			return httperr.BadRequest(40099, "trace run summary list is too long")
		}
		for _, item := range value {
			pointer, ok := item.(string)
			if !ok || len(pointer) > 128 {
				return httperr.BadRequest(40099, "trace run summary lists must contain pointers of 128 characters or fewer")
			}
		}
	default:
		return httperr.BadRequest(40099, "trace run summary must contain scalar or string pointer values")
	}
	return nil
}

func marshalTraceSummary(kind string, value map[string]any) (string, error) {
	if value == nil {
		return "", nil
	}
	if kind == "evidence" {
		for key, pointer := range value {
			if len(key) > 64 {
				return "", httperr.BadRequest(40099, "trace run evidence source is too long")
			}
			pointerText, ok := pointer.(string)
			if !ok || pointerText == "" || len(pointerText) > 512 {
				return "", httperr.BadRequest(40099, "trace run evidence pointers must be non-empty strings")
			}
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return "", httperr.BadRequest(40099, "invalid trace run summary")
		}
		if len(raw) > 65535 {
			return "", httperr.BadRequest(40099, "trace run summary is too large")
		}
		return string(raw), nil
	}
	allowed := traceSummaryFields[kind]
	for key, item := range value {
		if !allowed[key] {
			return "", httperr.BadRequest(40099, "trace run summary contains unsupported field "+kind+"."+key)
		}
		if err := validateTraceSummaryValue(item); err != nil {
			return "", err
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", httperr.BadRequest(40099, "invalid trace run summary")
	}
	if len(raw) > 65535 {
		return "", httperr.BadRequest(40099, "trace run summary is too large")
	}
	return string(raw), nil
}

func (s *Service) CreateTraceRun(ctx context.Context, actorID, tenantID string, input TraceRunInput) (*model.TraceRun, error) {
	if err := s.Authorize(ctx, actorID, "manage", "knowledge-ops"); err != nil {
		return nil, err
	}
	input.TraceID = strings.TrimSpace(input.TraceID)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.AppID = strings.TrimSpace(input.AppID)
	if input.TraceID == "" || len(input.TraceID) > 64 || input.RequestID == "" || len(input.RequestID) > 64 || input.AppID == "" {
		return nil, httperr.BadRequest(40099, "trace_id, request_id and app_id are required")
	}
	switch input.AppType {
	case "chat", "search", "agent":
	default:
		input.AppType = "chat"
	}
	if input.Status == "" {
		input.Status = model.TraceRunCompleted
	}
	switch input.Status {
	case model.TraceRunCompleted, model.TraceRunNoAnswer, model.TraceRunFailed:
	default:
		return nil, httperr.BadRequest(40099, "trace run status must be completed, no_answer or failed")
	}
	if input.SpanID == "" {
		input.SpanID = "root"
	}
	if err := validateOtelSpanID(input.OtelSpanID); err != nil {
		return nil, err
	}
	if input.Channel == "" {
		input.Channel = "web"
	}
	if input.UserID == "" {
		input.UserID = actorID
	}
	route, err := marshalTraceSummary("route", input.RouteSummary)
	if err != nil {
		return nil, err
	}
	retrieval, err := marshalTraceSummary("retrieval", input.RetrievalSummary)
	if err != nil {
		return nil, err
	}
	modelSummary, err := marshalTraceSummary("model", input.ModelSummary)
	if err != nil {
		return nil, err
	}
	tool, err := marshalTraceSummary("tool", input.ToolSummary)
	if err != nil {
		return nil, err
	}
	governance, err := marshalTraceSummary("governance", input.GovernanceSummary)
	if err != nil {
		return nil, err
	}
	quality, err := marshalTraceSummary("quality", input.QualitySummary)
	if err != nil {
		return nil, err
	}
	evidence, err := marshalTraceSummary("evidence", input.EvidencePointers)
	if err != nil {
		return nil, err
	}
	if input.OtelSpanID != "" {
		input.SpanID = input.OtelSpanID
		evidence, err = withOTelEvidencePointers(evidence, input.TraceID, input.OtelSpanID)
		if err != nil {
			return nil, err
		}
	}
	run := &model.TraceRun{
		ID: id.New(), TenantID: tenantID, TraceID: input.TraceID, SpanID: input.SpanID,
		RequestID: input.RequestID, SessionID: strings.TrimSpace(input.SessionID), UserID: input.UserID,
		ProjectID: strings.TrimSpace(input.ProjectID), AssistantID: strings.TrimSpace(input.AssistantID),
		AssistantReleaseID: strings.TrimSpace(input.AssistantReleaseID), AppType: input.AppType, AppID: input.AppID,
		Channel: strings.TrimSpace(input.Channel), Status: input.Status, RouteSummaryJSON: route,
		RetrievalSummaryJSON: retrieval, ModelSummaryJSON: modelSummary, ToolSummaryJSON: tool,
		GovernanceSummaryJSON: governance, QualitySummaryJSON: quality, EvidencePointersJSON: evidence,
	}
	if err := s.Store.UpsertTraceRun(ctx, run); err != nil {
		return nil, err
	}
	return run, nil
}

func (s *Service) ListTraceRuns(ctx context.Context, actorID, tenantID string, page, pageSize int, filter repository.TraceRunFilter) ([]model.TraceRun, int64, error) {
	if err := s.Authorize(ctx, actorID, "read", "knowledge-ops"); err != nil {
		return nil, 0, err
	}
	return s.Store.ListTraceRuns(ctx, tenantID, s.KnowledgeOpsScopeAll(ctx, actorID), page, pageSize, filter)
}

func (s *Service) GetTraceRun(ctx context.Context, actorID, tenantID, traceID string) (*model.TraceRun, error) {
	if err := s.Authorize(ctx, actorID, "read", "knowledge-ops"); err != nil {
		return nil, err
	}
	traceID = strings.TrimSpace(traceID)
	if traceID == "" || len(traceID) > 64 {
		return nil, httperr.BadRequest(40099, "trace_id must be 1-64 characters")
	}
	run, err := s.Store.GetTraceRunByTraceID(ctx, tenantID, traceID, s.KnowledgeOpsScopeAll(ctx, actorID))
	if err != nil {
		return nil, err
	}
	if run.ID == "" {
		return nil, httperr.NotFound("trace run not found")
	}
	return run, nil
}

// validateOtelSpanID enforces the W3C 16-byte-wide span id format so a
// malformed upstream value can never poison the deep link (doc/125 §3.1).
func validateOtelSpanID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if len(value) != 16 {
		return httperr.BadRequest(40099, "otel_span_id must be 16 hex characters")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return httperr.BadRequest(40099, "otel_span_id must be 16 hex characters")
	}
	return nil
}

// withOTelEvidencePointers merges the otel deep-link triple into the evidence
// pointer JSON. The collector host and UI link come from the process
// observability config; when absent the UI hides the link and only shows ids.
func withOTelEvidencePointers(evidenceJSON, traceID, spanID string) (string, error) {
	pointers := map[string]any{}
	if strings.TrimSpace(evidenceJSON) != "" {
		if err := json.Unmarshal([]byte(evidenceJSON), &pointers); err != nil {
			pointers = map[string]any{}
		}
	}
	pointers["otel_trace_id"] = traceID
	pointers["otel_span_id"] = spanID
	if host := obs.OTelEndpointHost(); host != "" {
		pointers["otel_endpoint"] = host
	}
	if link := obs.OTelTraceUILink(traceID, spanID); link != "" {
		pointers["otel_ui_link"] = link
	}
	return marshalTraceSummary("evidence", pointers)
}

func knowledgeOpsTraceRun(ctx context.Context, event *model.KnowledgeOpsEvent) *model.TraceRun {
	quality := map[string]any{
		"knowledge_ops_event_id": event.ID, "feedback_id": event.FeedbackID,
		"feedback_rating": event.FeedbackRating, "feedback_attribution": event.FeedbackAttribution,
		"citations_count": event.CitationsCount, "duration_ms": event.DurationMs,
		"tokens_in": event.TokensIn, "tokens_out": event.TokensOut,
	}
	status := model.TraceRunCompleted
	switch event.Status {
	case model.KnowledgeOpsNoAnswer:
		status = model.TraceRunNoAnswer
	case model.KnowledgeOpsFailed:
		status = model.TraceRunFailed
	}
	run := &model.TraceRun{
		TenantID: event.TenantID, TraceID: event.RequestID, SpanID: "root", RequestID: event.RequestID,
		SessionID: event.SessionID, UserID: event.UserID, AppType: event.AppType, AppID: event.AppID,
		Status: status, QualitySummaryJSON: mustMarshalTraceSummary(quality),
		EvidencePointersJSON: mustMarshalTraceSummary(map[string]any{"knowledge_ops_event_id": event.ID}),
	}
	// Stamp the live span context when tracing is on: SpanID becomes the real
	// span instead of "root" and the evidence pointers gain the otel deep-link
	// triple. Without a valid span the legacy shape is kept so the UI hides
	// the deep link (doc/125 §3.1).
	if traceID, spanID, valid := obs.SpanContextFromContext(ctx); valid {
		run.SpanID = spanID
		pointers := map[string]any{
			"knowledge_ops_event_id": event.ID,
			"otel_trace_id":          traceID,
			"otel_span_id":           spanID,
		}
		if host := obs.OTelEndpointHost(); host != "" {
			pointers["otel_endpoint"] = host
		}
		if link := obs.OTelTraceUILink(traceID, spanID); link != "" {
			pointers["otel_ui_link"] = link
		}
		run.EvidencePointersJSON = mustMarshalTraceSummary(pointers)
	}
	return run
}

func mustMarshalTraceSummary(value map[string]any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(raw)
}
