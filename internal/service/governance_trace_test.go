package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/obs"
)

func TestKnowledgeOpsEventProjectsTraceRun(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Trace Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "trace-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	event := &model.KnowledgeOpsEvent{
		RequestID: "request-trace", TenantID: tenant.ID, UserID: admin.ID, AppType: "chat", AppID: "chat-1",
		SessionID: "session-trace", Question: "bad", QuestionHash: "hash", AnswerExcerpt: "",
		Status: model.KnowledgeOpsCompleted, CitationsCount: 3, DurationMs: 250,
		TokensIn: 30, TokensOut: 60,
	}
	svc.recordKnowledgeEvent(ctx, event)
	run, err := svc.GetTraceRun(ctx, admin.ID, tenant.ID, event.RequestID)
	if err != nil || run.TraceID != event.RequestID || run.Status != model.TraceRunCompleted {
		t.Fatalf("projected trace run: run=%+v err=%v", run, err)
	}
	var quality map[string]any
	if err := json.Unmarshal([]byte(run.QualitySummaryJSON), &quality); err != nil {
		t.Fatal(err)
	}
	if quality["citations_count"] != float64(3) || quality["knowledge_ops_event_id"] != event.ID {
		t.Fatalf("unexpected trace quality summary: %+v", quality)
	}
	if _, err := svc.GetTraceRun(ctx, admin.ID, "other-tenant", event.RequestID); err == nil {
		t.Fatal("cross-tenant trace lookup must fail")
	}
}

func TestKnowledgeOpsTraceRunStampsOTelSpan(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Trace OTel Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "trace-otel-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	newEvent := func(requestID string) *model.KnowledgeOpsEvent {
		return &model.KnowledgeOpsEvent{
			RequestID: requestID, TenantID: tenant.ID, UserID: admin.ID, AppType: "chat", AppID: "chat-otel",
			Question: "otel?", QuestionHash: "hash-otel", Status: model.KnowledgeOpsCompleted,
		}
	}

	// Without a span in ctx the projection keeps the legacy "root" shape.
	svc.recordKnowledgeEvent(ctx, newEvent("request-no-span"))
	run, err := svc.GetTraceRun(ctx, admin.ID, tenant.ID, "request-no-span")
	if err != nil {
		t.Fatal(err)
	}
	if run.SpanID != "root" {
		t.Fatalf("span id must stay root without a span ctx: %q", run.SpanID)
	}
	var evidence map[string]any
	if err := json.Unmarshal([]byte(run.EvidencePointersJSON), &evidence); err != nil {
		t.Fatal(err)
	}
	if _, ok := evidence["otel_trace_id"]; ok {
		t.Fatalf("otel pointers must be absent without a span ctx: %+v", evidence)
	}

	// With a live span the projection stamps the real span and otel pointers.
	tracer := trace.NewTracerProvider().Tracer("test")
	spanCtx, span := tracer.Start(ctx, "test-span")
	defer span.End()
	svc.recordKnowledgeEvent(spanCtx, newEvent("request-with-span"))
	stamped, err := svc.GetTraceRun(ctx, admin.ID, tenant.ID, "request-with-span")
	if err != nil {
		t.Fatal(err)
	}
	expectedSpan := span.SpanContext().SpanID().String()
	if stamped.SpanID != expectedSpan || stamped.SpanID == "root" {
		t.Fatalf("span id must be the live span: got %q want %q", stamped.SpanID, expectedSpan)
	}
	evidence = map[string]any{}
	if err := json.Unmarshal([]byte(stamped.EvidencePointersJSON), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence["otel_trace_id"] != span.SpanContext().TraceID().String() || evidence["otel_span_id"] != expectedSpan {
		t.Fatalf("otel pointers missing: %+v", evidence)
	}
	if _, ok := evidence["knowledge_ops_event_id"]; !ok {
		t.Fatalf("original evidence pointer must be preserved: %+v", evidence)
	}
}

func TestKnowledgeOpsTraceRunStampsConfiguredUILink(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Trace UI Link Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "trace-uilink-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Configure the deep-link template (doc/104 §15) on the process
	// observability instance, then project an event under a live span.
	obs.Set(obs.New(config.Observability{
		TracingEnabled: true, OTLPEndpoint: "http://collector:4318",
		TraceUIURLTemplate: "https://grafana.example.com/trace/{trace_id}?span={span_id}",
	}))
	defer obs.Set(obs.New(config.Observability{}))

	tracer := trace.NewTracerProvider().Tracer("test-uilink")
	spanCtx, span := tracer.Start(ctx, "test-span-uilink")
	defer span.End()
	event := &model.KnowledgeOpsEvent{
		RequestID: "request-uilink", TenantID: tenant.ID, UserID: admin.ID, AppType: "chat", AppID: "chat-uilink",
		Question: "ui link?", QuestionHash: "hash-uilink", Status: model.KnowledgeOpsCompleted,
	}
	svc.recordKnowledgeEvent(spanCtx, event)
	run, err := svc.GetTraceRun(ctx, admin.ID, tenant.ID, "request-uilink")
	if err != nil {
		t.Fatal(err)
	}
	var evidence map[string]any
	if err := json.Unmarshal([]byte(run.EvidencePointersJSON), &evidence); err != nil {
		t.Fatal(err)
	}
	want := "https://grafana.example.com/trace/" + span.SpanContext().TraceID().String() + "?span=" + span.SpanContext().SpanID().String()
	if evidence["otel_ui_link"] != want {
		t.Fatalf("otel_ui_link mismatch: got %v want %q", evidence["otel_ui_link"], want)
	}
}

func TestCreateTraceRunOtelSpanIDValidation(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Trace Otel Validation Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "trace-otel-validate-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	input := TraceRunInput{TraceID: "trace-otel", RequestID: "request-otel", AppID: "chat-1"}
	for _, bad := range []string{"short", "zzzzzzzzzzzzzzzz", "0123456789abcdef0"} {
		invalid := input
		invalid.OtelSpanID = bad
		if _, err := svc.CreateTraceRun(ctx, admin.ID, tenant.ID, invalid); err == nil {
			t.Fatalf("invalid otel_span_id %q must be rejected", bad)
		}
	}
	valid := input
	valid.OtelSpanID = "0123456789abcdef"
	run, err := svc.CreateTraceRun(ctx, admin.ID, tenant.ID, valid)
	if err != nil {
		t.Fatalf("valid otel_span_id rejected: %v", err)
	}
	if run.SpanID != "0123456789abcdef" {
		t.Fatalf("otel span id must become the span id: %q", run.SpanID)
	}
	var evidence map[string]any
	if err := json.Unmarshal([]byte(run.EvidencePointersJSON), &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence["otel_span_id"] != "0123456789abcdef" || evidence["otel_trace_id"] != "trace-otel" {
		t.Fatalf("otel evidence pointers missing: %+v", evidence)
	}
}

func TestCreateTraceRunValidation(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "Trace Create Tenant")
	if err != nil {
		t.Fatal(err)
	}
	admin, err := svc.CreateUser(ctx, tenant.ID, "platform_admin", CreateUserRequest{
		Username: "trace-create-admin", Password: "secret123", Role: "tenant_admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateTraceRun(ctx, admin.ID, tenant.ID, TraceRunInput{RequestID: "request-1", AppID: "chat-1"}); err == nil {
		t.Fatal("missing trace_id must be rejected")
	}
	if _, err := svc.CreateTraceRun(ctx, admin.ID, tenant.ID, TraceRunInput{
		TraceID: "trace-raw", RequestID: "request-raw", AppID: "chat-1",
		RetrievalSummary: map[string]any{"chunks": []map[string]any{{"content": "raw chunk"}}},
	}); err == nil {
		t.Fatal("raw retrieval chunks must be rejected")
	}
	if _, err := svc.CreateTraceRun(ctx, admin.ID, tenant.ID, TraceRunInput{
		TraceID: "trace-tool", RequestID: "request-tool", AppID: "chat-1",
		ToolSummary: map[string]any{"request_body": "raw tool request"},
	}); err == nil {
		t.Fatal("raw tool fields must be rejected")
	}
	if _, err := svc.CreateTraceRun(ctx, admin.ID, tenant.ID, TraceRunInput{
		TraceID: "trace-oversize", RequestID: "request-oversize", AppID: "chat-1",
		RouteSummary: map[string]any{"selection": strings.Repeat("raw", 300)},
	}); err == nil {
		t.Fatal("oversized raw summary text must be rejected")
	}
	run, err := svc.CreateTraceRun(ctx, admin.ID, tenant.ID, TraceRunInput{
		TraceID: "trace-1", RequestID: "request-1", AppType: "chat", AppID: "chat-1",
		RouteSummary:     map[string]any{"selection": "finance-assistant"},
		EvidencePointers: map[string]any{"otel_span_id": "span-1"},
	})
	if err != nil {
		t.Fatalf("create trace run: %v", err)
	}
	if run.Status != model.TraceRunCompleted || run.RouteSummaryJSON == "" || run.EvidencePointersJSON == "" {
		t.Fatalf("unexpected trace run: %+v", run)
	}
	found, err := svc.GetTraceRun(ctx, admin.ID, tenant.ID, "trace-1")
	if err != nil || found.ID != run.ID {
		t.Fatalf("lookup trace run: run=%+v err=%v", found, err)
	}
}
