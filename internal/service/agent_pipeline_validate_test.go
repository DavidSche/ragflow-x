package service

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// The dataflow (ingestion) canvas used to be exempt from every parameter check:
// only the structural rules ran and they were satisfied by a canvas that could
// not be compiled. These tests pin the two things that were missing — the shape
// the engine refuses to run (one terminal, reachable from File) and the node
// parameters whose factory rejects the canvas.

type pipelineNodeSpec struct {
	id         string
	component  string
	label      string
	params     map[string]interface{}
	upstream   []string
	downstream []string
}

func pipelineDsl(specs ...pipelineNodeSpec) map[string]interface{} {
	components := map[string]interface{}{}
	nodes := []interface{}{}
	edges := []interface{}{}
	for _, spec := range specs {
		obj := map[string]interface{}{"component_name": spec.component, "params": spec.params}
		entry := map[string]interface{}{"obj": obj}
		if len(spec.upstream) > 0 {
			entry["upstream"] = stringsToAny(spec.upstream)
			obj["upstream"] = stringsToAny(spec.upstream)
		}
		if len(spec.downstream) > 0 {
			entry["downstream"] = stringsToAny(spec.downstream)
			obj["downstream"] = stringsToAny(spec.downstream)
		}
		components[spec.id] = entry
		label := spec.label
		if label == "" {
			label = spec.component
		}
		nodes = append(nodes, map[string]interface{}{"id": spec.id, "data": map[string]interface{}{"label": label}})
		for _, down := range spec.downstream {
			edges = append(edges, map[string]interface{}{"source": spec.id, "target": down})
		}
	}
	return map[string]interface{}{
		"components": components,
		"graph":      map[string]interface{}{"nodes": nodes, "edges": edges},
	}
}

func stringsToAny(values []string) []interface{} {
	out := make([]interface{}, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func runnablePipelineDsl() map[string]interface{} {
	return pipelineDsl(
		pipelineNodeSpec{id: "file", component: "File", label: "File", params: map[string]interface{}{}, downstream: []string{"parser"}},
		pipelineNodeSpec{
			id: "parser", component: "Parser", params: map[string]interface{}{
				"pdf":   map[string]interface{}{"parse_method": "deepdoc"},
				"image": map[string]interface{}{"ocr_enabled": true},
			},
			upstream: []string{"file"}, downstream: []string{"chunk"},
		},
		pipelineNodeSpec{
			id: "chunk", component: "TokenChunker", params: map[string]interface{}{
				"delimiter_mode":     "delimiter",
				"chunk_token_size":   512.0,
				"overlapped_percent": 10.0,
			},
			upstream: []string{"parser"}, downstream: []string{"index"},
		},
		pipelineNodeSpec{
			id: "index", component: "Tokenizer", params: map[string]interface{}{"search_method": stringsToAny([]string{"embedding", "full_text"})},
			upstream: []string{"chunk"},
		},
	)
}

func TestValidateAgentDSLAcceptsARunnablePipeline(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "TenantPipelineOK")
	if err != nil {
		t.Fatal(err)
	}
	// The mock engine resolves a vision model for every type, so the parser's
	// vision branch is checkable here. Seed a compilation template too: the
	// compiler rule must not accuse a canvas that names a real one.
	seedPipelineCatalog(t, svc, "tree")

	dsl := pipelineDsl(
		pipelineNodeSpec{id: "file", component: "File", label: "File", params: map[string]interface{}{}, downstream: []string{"parser"}},
		pipelineNodeSpec{
			id: "parser", component: "Parser", params: map[string]interface{}{
				"pdf":   map[string]interface{}{"parse_method": "deepdoc"},
				"image": map[string]interface{}{"ocr_enabled": true},
			},
			upstream: []string{"file"}, downstream: []string{"chunk"},
		},
		pipelineNodeSpec{
			id: "chunk", component: "TokenChunker", params: map[string]interface{}{
				"delimiter_mode":     "delimiter",
				"chunk_token_size":   512.0,
				"overlapped_percent": 10.0,
			},
			upstream: []string{"parser"}, downstream: []string{"extract"},
		},
		pipelineNodeSpec{
			id: "extract", component: "Extractor",
			params:   map[string]interface{}{"llm_id": "mock-chat-model"},
			upstream: []string{"chunk"}, downstream: []string{"compile"},
		},
		pipelineNodeSpec{
			id: "compile", component: "Compiler",
			params:   map[string]interface{}{"compilation_template_id": "tree"},
			upstream: []string{"extract"}, downstream: []string{"index"},
		},
		pipelineNodeSpec{id: "index", component: "Tokenizer", params: map[string]interface{}{"search_method": stringsToAny([]string{"embedding", "full_text"})}, upstream: []string{"compile"}},
	)

	report := svc.ValidateAgentDSL(context.Background(), agentScope(tenant.ID), dsl, model.AgentCanvasCategoryDataflow)
	if !report.OK {
		t.Fatalf("a runnable pipeline must validate, got %v", issueCodes(report.Errors))
	}
}

func TestValidateAgentDSLReportsPipelineParameterMistakes(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "TenantPipelineBroken")
	if err != nil {
		t.Fatal(err)
	}
	seedPipelineCatalog(t, svc, "tree")

	dsl := pipelineDsl(
		pipelineNodeSpec{id: "file", component: "File", params: map[string]interface{}{}, downstream: []string{"parser"}},
		pipelineNodeSpec{
			id: "parser", component: "Parser", params: map[string]interface{}{
				"pdf":   map[string]interface{}{"parse_method": ""},
				"image": map[string]interface{}{"ocr_enabled": false},
				"vlm":   map[string]interface{}{"llm_id": "no-such-vision-model"},
			},
			upstream: []string{"file"}, downstream: []string{"chunk"},
		},
		pipelineNodeSpec{
			id: "chunk", component: "TokenChunker", params: map[string]interface{}{
				"delimiter_mode":     "one",
				"chunk_token_size":   0.0,
				"overlapped_percent": 95.0,
			},
			upstream: []string{"parser"}, downstream: []string{"extract"},
		},
		pipelineNodeSpec{
			id: "extract", component: "Extractor", params: map[string]interface{}{
				"llm_id":   "no-such-model@none@none",
				"keywords": map[string]interface{}{"enabled": true, "top_n": 0.0},
			},
			upstream: []string{"chunk"}, downstream: []string{"compile"},
		},
		pipelineNodeSpec{
			id: "compile", component: "Compiler", params: map[string]interface{}{},
			upstream: []string{"extract"}, downstream: []string{"index"},
		},
		pipelineNodeSpec{id: "index", component: "Tokenizer", params: map[string]interface{}{"search_method": stringsToAny([]string{"embedding", "full_text"})}, upstream: []string{"compile"}},
	)

	report := svc.ValidateAgentDSL(context.Background(), agentScope(tenant.ID), dsl, model.AgentCanvasCategoryDataflow)
	for _, code := range []string{
		"parser_pdf_method_missing",
		"parser_image_no_text_source",
		"parser_vision_model_unknown",
		"chunker_delimiter_mode",
		"chunker_token_size",
		"chunker_overlap_percent",
		"extractor_model_unknown",
		"extractor_toggle_top_n",
		"compiler_template_missing",
	} {
		if !containsCode(report.Errors, code) {
			t.Fatalf("missing %q in %v", code, issueCodes(report.Errors))
		}
	}
	if report.OK {
		t.Fatal("a pipeline with unusable node parameters must not validate")
	}
}

// Tokenizer and chunker guards: both are engine-side `Validate()` calls that
// only fire when the canvas runs, so saving one has to report them.
func TestValidateAgentDSLReportsTokenizerAndDelimiterMistakes(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "TenantPipelineTokenizer")
	if err != nil {
		t.Fatal(err)
	}
	dsl := pipelineDsl(
		pipelineNodeSpec{id: "file", component: "File", params: map[string]interface{}{}, downstream: []string{"chunk"}},
		pipelineNodeSpec{
			id: "chunk", component: "TokenChunker", params: map[string]interface{}{
				"delimiter_mode": "delimiter",
				"delimiters":     stringsToAny([]string{"\n", ""}),
			},
			upstream: []string{"file"}, downstream: []string{"index"},
		},
		pipelineNodeSpec{id: "index", component: "Tokenizer", params: map[string]interface{}{}, upstream: []string{"chunk"}},
	)

	report := svc.ValidateAgentDSL(context.Background(), agentScope(tenant.ID), dsl, model.AgentCanvasCategoryDataflow)
	for _, code := range []string{"chunker_delimiter_empty", "tokenizer_search_method_missing"} {
		if !containsCode(report.Errors, code) {
			t.Fatalf("missing %q in %v", code, issueCodes(report.Errors))
		}
	}

	// An unknown retrieval mode is rejected too (`schema.TokenizerParam.Validate`).
	fixed := pipelineDsl(
		pipelineNodeSpec{id: "file", component: "File", params: map[string]interface{}{}, downstream: []string{"index"}},
		pipelineNodeSpec{
			id: "index", component: "Tokenizer", params: map[string]interface{}{"search_method": stringsToAny([]string{"semantic"})},
			upstream: []string{"file"},
		},
	)
	report = svc.ValidateAgentDSL(context.Background(), agentScope(tenant.ID), fixed, model.AgentCanvasCategoryDataflow)
	if !containsCode(report.Errors, "tokenizer_search_method_invalid") {
		t.Fatalf("missing tokenizer_search_method_invalid in %v", issueCodes(report.Errors))
	}
}

// The engine walks the graph from the File node and demands exactly one sink;
// a second sink is "the run returned nothing" with no hint why.
func TestValidateAgentDSLRequiresExactlyOnePipelineTerminal(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "TenantPipelineTerminals")
	if err != nil {
		t.Fatal(err)
	}
	dsl := pipelineDsl(
		pipelineNodeSpec{id: "file", component: "File", params: map[string]interface{}{}, downstream: []string{"parser"}},
		pipelineNodeSpec{
			id: "parser", component: "Parser", params: map[string]interface{}{
				"pdf": map[string]interface{}{"parse_method": "deepdoc"},
			},
			upstream: []string{"file"}, downstream: []string{"chunk", "index"},
		},
		pipelineNodeSpec{
			id: "chunk", component: "TokenChunker", params: map[string]interface{}{"delimiter_mode": "delimiter"},
			upstream: []string{"parser"},
		},
		pipelineNodeSpec{id: "index", component: "Tokenizer", params: map[string]interface{}{"search_method": stringsToAny([]string{"embedding", "full_text"})}, upstream: []string{"parser"}},
	)
	report := svc.ValidateAgentDSL(context.Background(), agentScope(tenant.ID), dsl, model.AgentCanvasCategoryDataflow)
	if !containsCode(report.Errors, "pipeline_terminal_count") {
		t.Fatalf("expected pipeline_terminal_count, got %v", issueCodes(report.Errors))
	}
	for _, issue := range report.Errors {
		if issue.Code == "pipeline_terminal_count" && issue.Params["count"] != "2" {
			t.Fatalf("the report must name the terminal count: %+v", issue)
		}
	}
}

func TestValidateAgentDSLRequiresAPipelineSource(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "TenantPipelineSource")
	if err != nil {
		t.Fatal(err)
	}
	dsl := pipelineDsl(
		pipelineNodeSpec{id: "parser", component: "Parser", params: map[string]interface{}{}, downstream: []string{"index"}},
		pipelineNodeSpec{id: "index", component: "Tokenizer", params: map[string]interface{}{"search_method": stringsToAny([]string{"embedding", "full_text"})}, upstream: []string{"parser"}},
	)
	report := svc.ValidateAgentDSL(context.Background(), agentScope(tenant.ID), dsl, model.AgentCanvasCategoryDataflow)
	if !containsCode(report.Errors, "pipeline_source_missing") {
		t.Fatalf("expected pipeline_source_missing, got %v", issueCodes(report.Errors))
	}
}

func TestValidateAgentDSLFlagsAnUnknownCompilationTemplate(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "TenantPipelineTemplate")
	if err != nil {
		t.Fatal(err)
	}
	seedPipelineCatalog(t, svc, "tree")

	dsl := pipelineDsl(
		pipelineNodeSpec{id: "file", component: "File", params: map[string]interface{}{}, downstream: []string{"compile"}},
		pipelineNodeSpec{
			id: "compile", component: "Compiler",
			params:   map[string]interface{}{"compilation_template_id": "removed-template"},
			upstream: []string{"file"}, downstream: []string{"index"},
		},
		pipelineNodeSpec{id: "index", component: "Tokenizer", params: map[string]interface{}{"search_method": stringsToAny([]string{"embedding", "full_text"})}, upstream: []string{"compile"}},
	)
	report := svc.ValidateAgentDSL(context.Background(), agentScope(tenant.ID), dsl, model.AgentCanvasCategoryDataflow)
	if !containsCode(report.Errors, "compiler_template_unknown") {
		t.Fatalf("expected compiler_template_unknown, got %v", issueCodes(report.Errors))
	}
}

func TestPublishAgentRefusesAnUnrunnablePipeline(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantPipelineGate")
	if err != nil {
		t.Fatal(err)
	}
	dsl := pipelineDsl(
		pipelineNodeSpec{id: "file", component: "File", params: map[string]interface{}{}, downstream: []string{"chunk"}},
		pipelineNodeSpec{
			id: "chunk", component: "TokenChunker",
			params:   map[string]interface{}{"delimiter_mode": "one"},
			upstream: []string{"file"}, downstream: []string{"index"},
		},
		pipelineNodeSpec{id: "index", component: "Tokenizer", params: map[string]interface{}{"search_method": stringsToAny([]string{"embedding", "full_text"})}, upstream: []string{"chunk"}},
	)
	agent, err := svc.CreateAgent(ctx, tenant.ID, "broken-pipeline", dsl, false, model.AgentCanvasCategoryDataflow)
	if err != nil {
		t.Fatalf("saving a broken pipeline must stay possible: %v", err)
	}
	_, err = svc.PublishAgent(ctx, tenant.ID, agent.ID, true, false)
	if err == nil {
		t.Fatal("publishing a pipeline the engine would reject must fail")
	}
	he, ok := err.(*httperr.Error)
	if !ok {
		t.Fatalf("unexpected error type %T (%v)", err, err)
	}
	if he.Code != 40099 {
		t.Fatalf("pipeline publish refusal must use 40099, got %d (%s)", he.Code, he.Message)
	}
}

// seedPipelineCatalog gives the mock engine one compilation template and one
// vision model so the compiler and parser vision rules are checkable.
func seedPipelineCatalog(t *testing.T, svc *Service, templateID string) {
	t.Helper()
	mock, ok := svc.RAGFlow.(*ragflow.Mock)
	if !ok {
		t.Fatalf("expected the in-memory engine mock, got %T", svc.RAGFlow)
	}
	mock.SetCompilationTemplates(ragflow.CompilationTemplateSourceBuiltins, []ragflow.CompilationTemplate{{
		ID:          templateID,
		Kind:        "tree",
		DisplayName: "Tree",
	}})
	mock.SetCompilationTemplateGroups(nil)
}
