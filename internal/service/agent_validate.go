package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// Save-time agent validation.
//
// The engine boundary (provider/ragflow/agent_dsl.go + agent_dsl_normalize.go)
// already repairs the mistakes that have a single correct answer: canvas labels
// become engine component names, impossible node ids are folded, prompt keys are
// renamed, an empty Answer forwards its upstream. Everything else is a decision
// only the author can make — a retrieval node with no knowledge base, an LLM
// node with no model, an answer nobody can reach — and used to surface for the
// first time as "Internal storage error while accessing the agent" (or worse, as
// a good-looking empty answer) the first time a user opened the chat.
//
// This validator turns those into a structured report at authoring time. It is
// deliberately split into three buckets:
//
//   - errors: publishing is refused (Save/Publish return 40099);
//   - warnings: it will run, but the answer quality is suspect;
//   - auto_fixed: what the boundary already corrected, for transparency.
//
// Severity is about *runnability*, not style: `errors` must be things the engine
// would reject or answer from an empty input.

// AgentDslIssue is one finding of the authoring validator.
type AgentDslIssue struct {
	// Code is a stable identifier the UI localises (agents.validation.<code>).
	Code string `json:"code"`
	// Severity is "error" (blocks publish) or "warning".
	Severity string `json:"severity"`
	NodeID   string `json:"node_id,omitempty"`
	// NodeLabel is the canvas label, which is what the author actually saw.
	NodeLabel string `json:"node_label,omitempty"`
	Field     string `json:"field,omitempty"`
	// Params carries the interpolation values for the localised message.
	Params map[string]string `json:"params,omitempty"`
	// Message is a ready-to-read English sentence for CLI/API consumers.
	Message string `json:"message"`
}

// AgentDslValidation is the save-time report for one canvas DSL.
type AgentDslValidation struct {
	OK             bool                   `json:"ok"`
	CanvasCategory string                 `json:"canvas_category"`
	NodeCount      int                    `json:"node_count"`
	AutoFixed      []ragflow.AgentDslFix  `json:"auto_fixed"`
	Errors         []AgentDslIssue        `json:"errors"`
	Warnings       []AgentDslIssue        `json:"warnings"`
	Unchecked      []string               `json:"unchecked,omitempty"`
	Normalized     map[string]interface{} `json:"-"`
}

const (
	agentIssueError   = "error"
	agentIssueWarning = "warning"
)

// agentScope builds the scope the create/update/publish paths authorize against:
// the caller's own workspace. Cross-tenant paths resolve a real scope in the
// handler and call the validator directly.
func agentScope(tenantID string) TenantScope {
	return TenantScope{Kind: TenantScopeCurrent, ActorTenantID: tenantID}
}

// ValidateAgentDSL checks a canvas DSL the way it will actually run: the input is
// pushed through the same boundary normalization the engine call uses, so a DSL
// that only fails because it is spelled in canvas vocabulary does not get
// reported as broken.
func (s *Service) ValidateAgentDSL(ctx context.Context, scope TenantScope, dsl map[string]interface{}, canvasCategory string) *AgentDslValidation {
	category := strings.TrimSpace(canvasCategory)
	if category == "" {
		category = "agent_canvas"
	}
	report := &AgentDslValidation{
		CanvasCategory: category,
		Errors:         []AgentDslIssue{},
		Warnings:       []AgentDslIssue{},
		AutoFixed:      []ragflow.AgentDslFix{},
	}
	if len(dsl) == 0 {
		report.Errors = append(report.Errors, AgentDslIssue{
			Code:     "dsl_empty",
			Severity: agentIssueError,
			Message:  "the canvas has no DSL: add nodes before saving",
		})
		return report
	}

	// Normalise first: everything below inspects the runnable form.
	normalized := ragflow.PrepareAgentDslForEngine(dsl)
	report.Normalized = normalized
	report.AutoFixed = ragflow.DescribeAgentDslFixes(dsl, normalized)
	if report.AutoFixed == nil {
		report.AutoFixed = []ragflow.AgentDslFix{}
	}

	graph := agentGraphOf(normalized)
	report.NodeCount = len(graph.nodes)
	if report.NodeCount == 0 {
		report.Errors = append(report.Errors, AgentDslIssue{
			Code:     "canvas_empty",
			Severity: agentIssueError,
			Message:  "the canvas has no nodes: add at least a Start node and an Answer node",
		})
		return report
	}

	// Live catalog + authoring options: each failure degrades to "unchecked",
	// never to a false finding.
	catalog := s.agentComponentCatalog(ctx, report)
	options := s.AgentAuthoringOptions(ctx, scope)
	modelNames, modelsChecked := agentChatModelNames(options, report)
	datasetIDs, datasetsChecked := s.agentDatasetIDs(ctx, report)

	for _, issue := range agentStructuralIssues(graph, catalog, category) {
		if issue.Severity == agentIssueWarning {
			report.Warnings = append(report.Warnings, issue)
			continue
		}
		report.Errors = append(report.Errors, issue)
	}
	if category == model.AgentCanvasCategoryDataflow {
		templates := s.agentTemplateCatalog(ctx, graph, report)
		visionModelNames, visionChecked := s.agentVisionModelNames(ctx, graph, report)
		issues, warnings := agentPipelineIssues(graph, modelNames, modelsChecked, templates, visionModelNames, visionChecked)
		report.Errors = append(report.Errors, issues...)
		report.Warnings = append(report.Warnings, warnings...)
	} else {
		issues, warnings := agentChatCanvasIssues(graph, modelNames, modelsChecked, datasetIDs, datasetsChecked)
		report.Errors = append(report.Errors, issues...)
		report.Warnings = append(report.Warnings, warnings...)
	}

	report.OK = len(report.Errors) == 0
	return report
}

// ValidateStoredAgentDSL validates what the engine currently holds for an agent.
// It is what the editor calls on open, so a canvas saved before this validator
// existed still gets flagged without being re-saved.
func (s *Service) ValidateStoredAgentDSL(ctx context.Context, scope TenantScope, agentID string) (*AgentDslValidation, error) {
	live, err := s.GetAgentDetailForScope(ctx, scope, agentID)
	if err != nil {
		return nil, err
	}
	category := live.CanvasCategory
	if category == "" {
		category = model.AgentCanvasCategoryWorkflow
	}
	return s.ValidateAgentDSL(ctx, scope, live.Dsl, category), nil
}

// EnforceAgentDSL is the publish gate: it refuses a canvas whose errors would
// only be discovered at chat time. `auto_fixed` corrections do not block, by
// design — the boundary already repaired them.
func (s *Service) EnforceAgentDSL(ctx context.Context, scope TenantScope, dsl map[string]interface{}, canvasCategory string) error {
	if !agentDslIsCanvas(dsl) {
		// Only a canvas is judged by canvas rules. Callers also store opaque
		// per-agent payloads (plan steps, routing fixtures, engine shapes this
		// platform only forwards), and refusing those would be inventing policy
		// for data we do not interpret. `POST /agents/validate` still reports them
		// as "no canvas" when the author explicitly asks.
		return nil
	}
	report := s.ValidateAgentDSL(ctx, scope, dsl, canvasCategory)
	if report.OK {
		return nil
	}
	parts := make([]string, 0, 3)
	for index, issue := range report.Errors {
		if index == 3 {
			parts = append(parts, fmt.Sprintf("and %d more", len(report.Errors)-3))
			break
		}
		parts = append(parts, agentIssueSummary(issue))
	}
	return httperr.BadRequest(40099, "agent DSL has blocking issues: "+strings.Join(parts, "; "))
}

// agentDslIsCanvas reports whether the payload is a canvas at all: both the
// editor and the engine keep nodes under `components`.
func agentDslIsCanvas(dsl map[string]interface{}) bool {
	if len(dsl) == 0 {
		return false
	}
	_, ok := dsl["components"].(map[string]interface{})
	return ok
}

func agentIssueSummary(issue AgentDslIssue) string {
	if issue.NodeLabel != "" || issue.NodeID != "" {
		node := issue.NodeLabel
		if node == "" {
			node = issue.NodeID
		}
		return fmt.Sprintf("%s (%s)", issue.Message, node)
	}
	return issue.Message
}

// ── graph model ─────────────────────────────────────────────────────────────

type agentGraphNode struct {
	id        string
	label     string
	component string
	params    map[string]interface{}
	// upstream/downstream are the component-level links the engine builds the
	// workflow from; graph.edges mirrors them and is checked too.
	upstream   []string
	downstream []string
}

type agentGraph struct {
	nodes map[string]*agentGraphNode
	order []string
	edges [][2]string
	// out/in merge graph.edges with the component-level upstream/downstream
	// links, because either one alone can leave a node looking orphaned.
	out map[string][]string
	in  map[string][]string
}

func agentGraphOf(dsl map[string]interface{}) agentGraph {
	out := agentGraph{
		nodes: map[string]*agentGraphNode{},
		out:   map[string][]string{},
		in:    map[string][]string{},
	}
	components, _ := dsl["components"].(map[string]interface{})
	ids := make([]string, 0, len(components))
	for id := range components {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		component, ok := components[id].(map[string]interface{})
		if !ok {
			continue
		}
		obj, _ := component["obj"].(map[string]interface{})
		node := &agentGraphNode{id: id, params: map[string]interface{}{}}
		if obj != nil {
			node.component = strings.ToLower(strings.TrimSpace(stringFromAny(obj["component_name"])))
			if params, ok := obj["params"].(map[string]interface{}); ok {
				node.params = params
			}
			node.upstream = agentIDList(obj["upstream"])
			node.downstream = agentIDList(obj["downstream"])
		}
		out.nodes[id] = node
		out.order = append(out.order, id)
	}
	// Labels live on the canvas graph, not on the components.
	if graphMap, ok := dsl["graph"].(map[string]interface{}); ok {
		if nodes, ok := graphMap["nodes"].([]interface{}); ok {
			for _, raw := range nodes {
				entry, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				id := stringFromAny(entry["id"])
				node, ok := out.nodes[id]
				if !ok {
					continue
				}
				if data, ok := entry["data"].(map[string]interface{}); ok {
					node.label = stringFromAny(data["label"])
				}
			}
		}
		if edges, ok := graphMap["edges"].([]interface{}); ok {
			for _, raw := range edges {
				entry, ok := raw.(map[string]interface{})
				if !ok {
					continue
				}
				out.edges = append(out.edges, [2]string{stringFromAny(entry["source"]), stringFromAny(entry["target"])})
			}
		}
	}
	for _, node := range out.nodes {
		if node.label == "" {
			node.label = node.component
		}
		for _, down := range node.downstream {
			out.out[node.id] = append(out.out[node.id], down)
			out.in[down] = append(out.in[down], node.id)
		}
		for _, up := range node.upstream {
			out.in[node.id] = append(out.in[node.id], up)
			out.out[up] = append(out.out[up], node.id)
		}
	}
	for _, edge := range out.edges {
		out.out[edge[0]] = append(out.out[edge[0]], edge[1])
		out.in[edge[1]] = append(out.in[edge[1]], edge[0])
	}
	return out
}

func agentIDList(value interface{}) []string {
	list, ok := value.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if id := strings.TrimSpace(stringFromAny(item)); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func (g agentGraph) has(id string) bool {
	_, ok := g.nodes[id]
	return ok
}

// reachable walks edges + upstream links from the given roots.
func (g agentGraph) reachable(roots []string) map[string]bool {
	seen := map[string]bool{}
	queue := append([]string{}, roots...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		node, ok := g.nodes[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		queue = append(queue, node.downstream...)
		queue = append(queue, g.out[id]...)
	}
	return seen
}

// ── rules ───────────────────────────────────────────────────────────────────

func agentStructuralIssues(graph agentGraph, catalog map[string]bool, category string) []AgentDslIssue {
	issues := []AgentDslIssue{}

	// component names
	for _, id := range graph.order {
		node := graph.nodes[id]
		if node.component == "" {
			issues = append(issues, agentNodeIssue("component_missing", agentIssueError, node,
				"component_name", "node has no component: pick one of the engine's components"))
			continue
		}
		// The engine's registry lookup is case-insensitive, so "Retrieval" is fine.
		if catalog != nil && !catalog[strings.ToLower(strings.TrimSpace(node.component))] {
			issues = append(issues, agentNodeIssue("component_unknown", agentIssueError, node,
				"component_name", fmt.Sprintf("the engine does not register a %q component", node.component)))
		}
	}

	// begin node (chat canvas needs a Start; pipelines start from their inputs)
	begins := []*agentGraphNode{}
	for _, node := range graph.nodes {
		if node.component == "begin" {
			begins = append(begins, node)
		}
	}
	if len(begins) == 0 && category != model.AgentCanvasCategoryDataflow {
		issues = append(issues, AgentDslIssue{
			Code:     "begin_missing",
			Severity: agentIssueError,
			Message:  "the canvas has no Start (Begin) node: nothing can trigger it",
		})
	}
	if len(begins) > 1 {
		issues = append(issues, AgentDslIssue{
			Code:     "begin_duplicate",
			Severity: agentIssueError,
			Params:   map[string]string{"count": fmt.Sprint(len(begins))},
			Message:  fmt.Sprintf("the canvas has %d Start nodes, only one is allowed", len(begins)),
		})
	}

	// dangling references
	for _, id := range graph.order {
		node := graph.nodes[id]
		for _, up := range node.upstream {
			if !graph.has(up) {
				issues = append(issues, agentNodeIssue("edge_dangling", agentIssueError, node,
					"upstream", fmt.Sprintf("node points at upstream %q which does not exist", up)))
			}
		}
		for _, down := range node.downstream {
			if !graph.has(down) {
				issues = append(issues, agentNodeIssue("edge_dangling", agentIssueError, node,
					"downstream", fmt.Sprintf("node points at downstream %q which does not exist", down)))
			}
		}
	}
	for _, edge := range graph.edges {
		source, target := edge[0], edge[1]
		if !graph.has(source) || !graph.has(target) {
			missing := source
			if graph.has(source) {
				missing = target
			}
			issues = append(issues, AgentDslIssue{
				Code:     "edge_dangling",
				Severity: agentIssueError,
				Field:    "graph.edges",
				Params:   map[string]string{"node": missing},
				Message:  fmt.Sprintf("an edge refers to node %q which does not exist", missing),
			})
		}
	}

	// unreachable nodes are the classic "I edited a copy" symptom
	if len(begins) > 0 {
		roots := make([]string, 0, len(begins))
		for _, node := range begins {
			roots = append(roots, node.id)
		}
		seen := graph.reachable(roots)
		for _, id := range graph.order {
			if seen[id] {
				continue
			}
			node := graph.nodes[id]
			issues = append(issues, agentNodeIssue("node_unreachable", agentIssueWarning, node,
				"", "node is not connected to Start: it will never run"))
		}
	}
	return issues
}

// agentTemplateCatalog carries the compilation-template ids a dataflow Compiler
// node may name, plus whether the engine actually answered. An unanswered
// catalog must never turn into "unknown template", so `checked` gates the rule.
type agentTemplateCatalog struct {
	checked bool
	ids     map[string]bool
	groups  map[string]bool
}

// agentPipelineIssues checks a dataflow (ingestion) canvas against the contract
// the engine enforces when the pipeline is compiled and run.
//
// Every rule below mirrors a real engine rejection, so an error here means "the
// run would die", not "we would have done it differently":
//
//   - the run needs exactly one terminal (pipeline.ExtractPayload);
//   - it starts at the File node and only walks connected components
//     (pipeline.TerminalComponentIDs);
//   - Parser.Check() rejects an empty pdf parse method, a non-boolean
//     ocr_enabled, and an image with no text source;
//   - the chunker rejects delimiter_mode outside {delimiter, token_size},
//     chunk_token_size < 1 and overlapped_percent outside 0..90;
//   - the extractor rejects an enabled group with top_n < 1;
//   - the compiler refuses to run without a compilation template id;
//   - the tokenizer needs at least one known retrieval mode;
//   - a zero-length delimiter entry makes the engine refuse the save.
func agentPipelineIssues(
	graph agentGraph,
	modelNames map[string]bool,
	modelsChecked bool,
	templates agentTemplateCatalog,
	visionModelNames map[string]bool,
	visionChecked bool,
) ([]AgentDslIssue, []AgentDslIssue) {
	errors := []AgentDslIssue{}
	warnings := []AgentDslIssue{}

	sources := []string{}
	for _, id := range graph.order {
		switch graph.nodes[id].component {
		case "file", "begin":
			sources = append(sources, id)
		}
	}
	if len(sources) == 0 {
		errors = append(errors, AgentDslIssue{
			Code:     "pipeline_source_missing",
			Severity: agentIssueError,
			Message:  "the pipeline has no File node: a run has nothing to read the document from",
		})
	}

	// Terminals: the engine walks from the File node and requires exactly one
	// connected component without a downstream. A second sink (or a branch that
	// never merges) is the classic "the run returns nothing" pipeline.
	reachable := map[string]bool{}
	if len(sources) > 0 {
		reachable = graph.reachable(sources)
	}
	terminals := []string{}
	for _, id := range graph.order {
		if len(sources) > 0 && !reachable[id] {
			continue
		}
		if len(graph.out[id]) > 0 {
			continue
		}
		terminals = append(terminals, id)
	}
	if len(sources) > 0 && len(terminals) != 1 {
		labels := make([]string, 0, len(terminals))
		for _, id := range terminals {
			labels = append(labels, nodeDisplayName(graph.nodes[id]))
		}
		errors = append(errors, AgentDslIssue{
			Code:     "pipeline_terminal_count",
			Severity: agentIssueError,
			Params: map[string]string{
				"count":     fmt.Sprint(len(terminals)),
				"terminals": strings.Join(labels, ", "),
			},
			Message: fmt.Sprintf("the pipeline needs exactly 1 end node, it has %d (%s): connect the branches into a single last stage",
				len(terminals), strings.Join(labels, ", ")),
		})
	}
	if len(sources) > 0 {
		for _, id := range graph.order {
			if reachable[id] {
				continue
			}
			warnings = append(warnings, agentNodeIssue("node_unreachable", agentIssueWarning, graph.nodes[id],
				"", "node is not connected to the File node: it will never run"))
		}
	}

	stagePresent := false
	for _, id := range graph.order {
		node := graph.nodes[id]
		switch node.component {
		case "parser":
			stagePresent = true
			errors = append(errors, agentParserPipelineIssues(node, visionModelNames, visionChecked)...)
		case "chunker", "tokenchunker", "generalchunker", "titlechunker", "grouptitlechunker",
			"hierarchytitlechunker", "manualchunker", "onechunker", "pagechunker", "qachunker",
			"tablechunker":
			stagePresent = true
			errors = append(errors, agentChunkerPipelineIssues(node)...)
		case "tokenizer":
			stagePresent = true
			errors = append(errors, agentTokenizerPipelineIssues(node)...)
		case "extractor":
			stagePresent = true
			errors = append(errors, agentExtractorPipelineIssues(node, modelNames, modelsChecked)...)
		case "compiler":
			stagePresent = true
			errors = append(errors, agentCompilerPipelineIssues(node, templates)...)
		}
	}
	if !stagePresent {
		warnings = append(warnings, AgentDslIssue{
			Code:     "pipeline_stage_missing",
			Severity: agentIssueWarning,
			Message:  "the pipeline has no parser/chunker stage: documents would pass through unparsed",
		})
	}
	return errors, warnings
}

// agentParserPipelineIssues mirrors ParserComponent.Check plus the two typed
// params its constructor rejects before Check even runs.
func agentParserPipelineIssues(node *agentGraphNode, visionModelNames map[string]bool, visionChecked bool) []AgentDslIssue {
	issues := []AgentDslIssue{}
	setups := agentParserSetups(node.params)

	if pdf, ok := setups["pdf"]; ok {
		if strings.TrimSpace(stringFromAny(pdf["parse_method"])) == "" {
			issues = append(issues, agentNodeIssue("parser_pdf_method_missing", agentIssueError, node,
				"parse_method", "PDF parsing has no method: the engine refuses the node with \"parse method abnormal\""))
		}
	}

	visionFlag, hasVisionFlag := node.params["enable_vision_enhancement"]
	visionEnabled := false
	if hasVisionFlag {
		switch typed := visionFlag.(type) {
		case bool:
			visionEnabled = typed
		default:
			issues = append(issues, agentNodeIssue("parser_vision_flag_type", agentIssueError, node,
				"enable_vision_enhancement", fmt.Sprintf("enable_vision_enhancement is %q, the engine only accepts true/false", stringFromAny(visionFlag))))
		}
	}

	if image, ok := setups["image"]; ok {
		ocrRaw, hasOCRFlag := image["ocr_enabled"]
		switch typed := ocrRaw.(type) {
		case bool:
			if !typed && !visionEnabled {
				issues = append(issues, agentNodeIssue("parser_image_no_text_source", agentIssueError, node,
					"image.ocr_enabled", "image parsing would produce no text: OCR is off and vision enhancement is disabled"))
			}
		case nil:
			if !hasOCRFlag {
				// Legacy shape: parse_method decides. Only an explicit non-OCR
				// method turns OCR off.
				method := strings.TrimSpace(stringFromAny(image["parse_method"]))
				if method != "" && !strings.EqualFold(method, "ocr") && !visionEnabled {
					issues = append(issues, agentNodeIssue("parser_image_no_text_source", agentIssueError, node,
						"image.parse_method", fmt.Sprintf("image parsing uses %q and vision enhancement is disabled: the item would carry no text", method)))
				}
			}
		default:
			issues = append(issues, agentNodeIssue("parser_image_ocr_flag_type", agentIssueError, node,
				"image.ocr_enabled", fmt.Sprintf("image.ocr_enabled is %q, the engine only accepts true/false", stringFromAny(ocrRaw))))
		}
	}

	if visionChecked {
		if vlm, ok := node.params["vlm"].(map[string]interface{}); ok {
			model := strings.TrimSpace(stringFromAny(vlm["llm_id"]))
			if model != "" && !agentModelAllowed(model, visionModelNames) {
				issues = append(issues, agentNodeIssue("parser_vision_model_unknown", agentIssueError, node,
					"vlm.llm_id", fmt.Sprintf("vision model %q is not an image2text/ocr model on this engine", model)))
			}
		}
	}
	return issues
}

// agentParserSetups lifts the per-format setups of a Parser node, accepting the
// Python-era nesting under `setups` the engine still flattens.
func agentParserSetups(params map[string]interface{}) map[string]map[string]interface{} {
	out := map[string]map[string]interface{}{}
	if nested, ok := params["setups"].(map[string]interface{}); ok {
		for key, raw := range nested {
			if entry, ok := raw.(map[string]interface{}); ok {
				out[strings.ToLower(strings.TrimSpace(key))] = entry
			}
		}
	}
	for _, key := range []string{"pdf", "spreadsheet", "image", "doc", "docx", "markdown", "text&code", "audio", "video", "email", "ppt"} {
		if entry, ok := params[key].(map[string]interface{}); ok {
			merged := out[key]
			if merged == nil {
				merged = map[string]interface{}{}
			}
			for field, value := range entry {
				merged[field] = value
			}
			out[key] = merged
		}
	}
	return out
}

func agentChunkerPipelineIssues(node *agentGraphNode) []AgentDslIssue {
	issues := []AgentDslIssue{}
	// component.validateDynamicParams rejects a zero-length delimiter entry on
	// save ("[Chunker] delimiters does not support empty entries"). A
	// whitespace-only delimiter is a real token, so only "" is an error.
	for _, field := range []string{"delimiters", "children_delimiters"} {
		values, ok := node.params[field].([]interface{})
		if !ok {
			continue
		}
		for _, value := range values {
			if text, ok := value.(string); ok && text == "" {
				issues = append(issues, agentNodeIssue("chunker_delimiter_empty", agentIssueError, node, field,
					fmt.Sprintf("%s has an empty entry: the engine refuses to save the canvas", field)))
				break
			}
		}
	}
	if raw, ok := node.params["delimiter_mode"]; ok {
		mode := strings.ToLower(strings.TrimSpace(stringFromAny(raw)))
		// "token_size" is the pre-2025 spelling the engine normalises to
		// "delimiter" instead of rejecting, so it stays legal here.
		if mode != "delimiter" && mode != "token_size" {
			issues = append(issues, agentNodeIssue("chunker_delimiter_mode", agentIssueError, node,
				"delimiter_mode", fmt.Sprintf("delimiter_mode %q is not supported: the engine only accepts \"delimiter\"", stringFromAny(raw))))
		}
	}
	if value, ok := agentFloat(node.params["chunk_token_size"]); ok && value < 1 {
		issues = append(issues, agentNodeIssue("chunker_token_size", agentIssueError, node,
			"chunk_token_size", "chunk_token_size must be at least 1: the engine refuses the node otherwise"))
	}
	if value, ok := agentFloat(node.params["overlapped_percent"]); ok {
		if value > 0 && value < 1 {
			// Fraction spelling: the engine scales it to a percentage.
			value = value * 100
		}
		if value < 0 || value > 90 {
			issues = append(issues, agentNodeIssue("chunker_overlap_percent", agentIssueError, node,
				"overlapped_percent", fmt.Sprintf("overlapped_percent is %g, the engine only accepts 0-90", value)))
		}
	}
	return issues
}

// agentTokenizerPipelineIssues mirrors schema.TokenizerParam.Validate: the
// engine refuses the node without at least one retrieval mode, and it only
// knows full_text and embedding.
func agentTokenizerPipelineIssues(node *agentGraphNode) []AgentDslIssue {
	issues := []AgentDslIssue{}
	methods := agentStringList(node.params["search_method"])
	if len(methods) == 0 {
		return append(issues, agentNodeIssue("tokenizer_search_method_missing", agentIssueError, node,
			"search_method", "the tokenizer has no retrieval mode: select keyword search, vector search, or both"))
	}
	for _, method := range methods {
		if method != "full_text" && method != "embedding" {
			issues = append(issues, agentNodeIssue("tokenizer_search_method_invalid", agentIssueError, node,
				"search_method", fmt.Sprintf("search method %q is not supported: the engine only accepts full_text and embedding", method)))
		}
	}
	return issues
}

func agentExtractorPipelineIssues(node *agentGraphNode, modelNames map[string]bool, modelsChecked bool) []AgentDslIssue {
	issues := []AgentDslIssue{}
	if modelsChecked {
		if model := strings.TrimSpace(stringFromAny(node.params["llm_id"])); model != "" && !agentModelAllowed(model, modelNames) {
			issues = append(issues, agentNodeIssue("extractor_model_unknown", agentIssueError, node,
				"llm_id", fmt.Sprintf("model %q is not configured in this workspace", model)))
		}
	}
	for _, group := range []string{"keywords", "questions", "tags"} {
		entry, ok := node.params[group].(map[string]interface{})
		if !ok {
			continue
		}
		enabled := false
		switch typed := entry["enabled"].(type) {
		case bool:
			// An explicit switch is authoritative; without it the engine derives
			// the switch from top_n, so this rule must not fire.
			enabled = typed
		case nil:
			continue
		default:
			enabled = strings.TrimSpace(stringFromAny(typed)) == "1" || strings.EqualFold(strings.TrimSpace(stringFromAny(typed)), "true")
		}
		if !enabled {
			continue
		}
		topN, _ := agentFloat(entry["top_n"])
		if topN < 1 {
			issues = append(issues, agentNodeIssue("extractor_toggle_top_n", agentIssueError, node,
				group+".top_n", fmt.Sprintf("%s extraction is on with top_n 0: the engine rejects the node, set top_n to 1 or more", group)))
		}
	}
	return issues
}

func agentCompilerPipelineIssues(node *agentGraphNode, templates agentTemplateCatalog) []AgentDslIssue {
	issues := []AgentDslIssue{}
	templateID := strings.TrimSpace(stringFromAny(node.params["compilation_template_id"]))
	groupID := strings.TrimSpace(stringFromAny(node.params["compilation_template_group_id"]))
	if templateID == "" && groupID == "" {
		return append(issues, agentNodeIssue("compiler_template_missing", agentIssueError, node,
			"compilation_template_id", "compiler node has no compilation template: the engine refuses the run without one"))
	}
	if !templates.checked {
		return issues
	}
	if templateID != "" && !templates.ids[templateID] {
		issues = append(issues, agentNodeIssue("compiler_template_unknown", agentIssueError, node,
			"compilation_template_id", fmt.Sprintf("compilation template %q does not exist on the engine", templateID)))
	}
	if groupID != "" && !templates.groups[groupID] {
		issues = append(issues, agentNodeIssue("compiler_template_unknown", agentIssueError, node,
			"compilation_template_group_id", fmt.Sprintf("compilation template group %q does not exist on the engine", groupID)))
	}
	return issues
}

// agentTemplateCatalog loads the compilation template ids when a canvas actually
// has a Compiler node, so a chat canvas never pays for the lookup.
func (s *Service) agentTemplateCatalog(ctx context.Context, graph agentGraph, report *AgentDslValidation) agentTemplateCatalog {
	needed := false
	for _, node := range graph.nodes {
		if node.component == "compiler" {
			needed = true
			break
		}
	}
	if !needed {
		return agentTemplateCatalog{}
	}
	templates, err := s.AgentCompilationTemplates(ctx)
	if err != nil {
		report.Unchecked = append(report.Unchecked, "compilation_templates")
		return agentTemplateCatalog{}
	}
	groups, err := s.AgentCompilationTemplateGroups(ctx)
	if err != nil {
		report.Unchecked = append(report.Unchecked, "compilation_template_groups")
		return agentTemplateCatalog{}
	}
	catalog := agentTemplateCatalog{checked: true, ids: map[string]bool{}, groups: map[string]bool{}}
	for _, template := range templates {
		catalog.ids[template.ID] = true
	}
	for _, group := range groups {
		catalog.groups[group.ID] = true
	}
	return catalog
}

// agentVisionModelNames loads the image2text/ocr catalog when a canvas has a
// Parser node, and degrades to "unchecked" when the engine cannot answer.
func (s *Service) agentVisionModelNames(ctx context.Context, graph agentGraph, report *AgentDslValidation) (map[string]bool, bool) {
	needed := false
	for _, node := range graph.nodes {
		if node.component == "parser" {
			needed = true
			break
		}
	}
	if !needed {
		return nil, false
	}
	models, err := s.agentVisionModels(ctx)
	if err != nil || len(models) == 0 {
		report.Unchecked = append(report.Unchecked, "vision_models")
		return nil, false
	}
	names := map[string]bool{}
	for _, model := range models {
		for _, raw := range []string{model.Name, model.ModelID} {
			name := strings.ToLower(strings.TrimSpace(raw))
			if name != "" {
				names[name] = true
			}
		}
	}
	return names, true
}

func agentChatCanvasIssues(
	graph agentGraph,
	modelNames map[string]bool,
	modelsChecked bool,
	datasetIDs map[string]bool,
	datasetsChecked bool,
) ([]AgentDslIssue, []AgentDslIssue) {
	errors := []AgentDslIssue{}
	warnings := []AgentDslIssue{}

	answers := 0
	for _, id := range graph.order {
		node := graph.nodes[id]
		switch node.component {
		case "message":
			answers++
			if !agentMessageContentUsable(node.params) {
				errors = append(errors, agentNodeIssue("answer_template_missing", agentIssueError, node,
					"content", "answer node has no template and no upstream node to forward"))
			}
		case "retrieval":
			errors = append(errors, agentRetrievalIssues(node, datasetIDs, datasetsChecked)...)
			warnings = append(warnings, agentRetrievalWarnings(node)...)
		case "llm", "generate":
			nodeErrors, nodeWarnings := agentLLMIssues(node, modelNames, modelsChecked)
			errors = append(errors, nodeErrors...)
			warnings = append(warnings, nodeWarnings...)
		}
	}
	if answers == 0 {
		errors = append(errors, AgentDslIssue{
			Code:     "answer_missing",
			Severity: agentIssueError,
			Message:  "the canvas has no Answer node: a conversation has nothing to reply with",
		})
	}
	return errors, warnings
}

func agentRetrievalIssues(node *agentGraphNode, datasetIDs map[string]bool, datasetsChecked bool) []AgentDslIssue {
	issues := []AgentDslIssue{}
	from := strings.ToLower(strings.TrimSpace(stringFromAny(node.params["retrieval_from"])))
	// "knowledgebase" is the default: retrieve from the bound kb ids.
	if from != "" && from != "knowledgebase" && from != "kb" {
		return issues
	}
	bound := agentStringList(node.params["kb_ids"])
	if len(bound) == 0 {
		bound = agentStringList(node.params["dataset_ids"])
	}
	if len(bound) == 0 {
		issues = append(issues, agentNodeIssue("retrieval_dataset_missing", agentIssueError, node,
			"kb_ids", "knowledge node has no knowledge base selected: every question fails with \"No dataset is selected\""))
		return issues
	}
	if datasetsChecked {
		unknown := []string{}
		for _, id := range bound {
			if !datasetIDs[id] {
				unknown = append(unknown, id)
			}
		}
		if len(unknown) > 0 {
			issues = append(issues, agentNodeIssue("retrieval_dataset_unknown", agentIssueError, node,
				"kb_ids", fmt.Sprintf("knowledge base %q does not exist on the engine: retrieval fails with \"No dataset is selected\"", strings.Join(unknown, ", "))))
		}
	}
	return issues
}

func agentRetrievalWarnings(node *agentGraphNode) []AgentDslIssue {
	issues := []AgentDslIssue{}
	if value, ok := agentFloat(node.params["top_n"]); ok && value <= 0 {
		issues = append(issues, agentNodeIssue("retrieval_top_n", agentIssueWarning, node,
			"top_n", "top_n is 0: the node will retrieve nothing"))
	}
	if value, ok := agentFloat(node.params["similarity_threshold"]); ok && (value < 0 || value > 1) {
		issues = append(issues, agentNodeIssue("retrieval_threshold", agentIssueWarning, node,
			"similarity_threshold", "similarity_threshold must be between 0 and 1"))
	}
	return issues
}

func agentLLMIssues(node *agentGraphNode, modelNames map[string]bool, modelsChecked bool) ([]AgentDslIssue, []AgentDslIssue) {
	errors := []AgentDslIssue{}
	warnings := []AgentDslIssue{}

	model := strings.TrimSpace(stringFromAny(node.params["llm_id"]))
	if model == "" {
		model = strings.TrimSpace(stringFromAny(node.params["model_id"]))
	}
	switch {
	case model == "":
		errors = append(errors, agentNodeIssue("llm_model_missing", agentIssueError, node,
			"llm_id", "model node has no model selected"))
	case modelsChecked && !agentModelAllowed(model, modelNames):
		errors = append(errors, agentNodeIssue("llm_model_unknown", agentIssueError, node,
			"llm_id", fmt.Sprintf("model %q is not configured in this workspace", model)))
	}

	if !agentPromptPresent(node.params) {
		errors = append(errors, agentNodeIssue("llm_prompt_missing", agentIssueError, node,
			"user_prompt", "model node has neither a system prompt nor a user prompt: the engine rejects the node"))
	}

	if tokens, ok := agentFloat(node.params["max_tokens"]); ok {
		switch {
		case tokens > 0 && tokens < 512:
			errors = append(errors, agentNodeIssue("llm_max_tokens_blocked", agentIssueError, node,
				"max_tokens", fmt.Sprintf("max_tokens is %d: reasoning models spend the whole budget before writing an answer", int(tokens))))
		case tokens > 0 && tokens < 2048:
			warnings = append(warnings, agentNodeIssue("llm_max_tokens_small", agentIssueWarning, node,
				"max_tokens", fmt.Sprintf("max_tokens is %d: a long answer may be cut off, 2048+ is recommended", int(tokens))))
		}
	}
	return errors, warnings
}

// ── option lookups (each one degrades, never guesses) ───────────────────────

func (s *Service) agentComponentCatalog(ctx context.Context, report *AgentDslValidation) map[string]bool {
	cataloger, ok := s.RAGFlow.(interface {
		ComponentCatalog(context.Context) (map[string]bool, error)
	})
	if !ok {
		report.Unchecked = append(report.Unchecked, "components")
		return nil
	}
	catalog, err := cataloger.ComponentCatalog(ctx)
	if err != nil || len(catalog) == 0 {
		report.Unchecked = append(report.Unchecked, "components")
		return nil
	}
	return catalog
}

func agentChatModelNames(options *AgentAuthoringOptions, report *AgentDslValidation) (map[string]bool, bool) {
	if options == nil || options.ModelsError != "" || len(options.Models.Chat) == 0 {
		report.Unchecked = append(report.Unchecked, "models")
		return nil, false
	}
	names := map[string]bool{}
	for _, model := range options.Models.Chat {
		for _, raw := range []string{model.Name, model.ModelID} {
			name := strings.ToLower(strings.TrimSpace(raw))
			if name != "" {
				names[name] = true
			}
		}
	}
	return names, true
}

// agentDatasetIDs answers the only question retrieval actually cares about: does
// the engine still have this knowledge base. It deliberately does not use the
// tenant-scoped picker list — that list hides other tenants' linked libraries,
// which the shared engine account can still retrieve from, so checking against
// it rejected perfectly runnable canvases with "not available to this workspace".
func (s *Service) agentDatasetIDs(ctx context.Context, report *AgentDslValidation) (map[string]bool, bool) {
	if s == nil || s.RAGFlow == nil {
		report.Unchecked = append(report.Unchecked, "datasets")
		return nil, false
	}
	datasets, err := s.RAGFlow.ListDatasets(ctx)
	if err != nil {
		report.Unchecked = append(report.Unchecked, "datasets")
		return nil, false
	}
	ids := make(map[string]bool, len(datasets))
	for _, dataset := range datasets {
		if id := strings.TrimSpace(dataset.ID); id != "" {
			ids[id] = true
		}
	}
	return ids, true
}

// agentModelAllowed accepts the composite `model@instance@provider` id the panel
// stores as well as the bare model name the engine also understands.
func agentModelAllowed(model string, names map[string]bool) bool {
	key := strings.ToLower(strings.TrimSpace(model))
	if key == "" {
		return false
	}
	if names[key] {
		return true
	}
	head, _, _ := strings.Cut(key, "@")
	return names[head]
}

func agentPromptPresent(params map[string]interface{}) bool {
	for _, key := range []string{"user_prompt", "system_prompt", "sys_prompt", "prompt", "system", "messages"} {
		if value := strings.TrimSpace(stringFromAny(params[key])); value != "" {
			return true
		}
	}
	return false
}

func agentMessageContentUsable(params map[string]interface{}) bool {
	if value := strings.TrimSpace(stringFromAny(params["text"])); value != "" {
		return true
	}
	switch content := params["content"].(type) {
	case string:
		return strings.TrimSpace(content) != ""
	case []interface{}:
		for _, item := range content {
			if strings.TrimSpace(stringFromAny(item)) != "" {
				return true
			}
		}
	}
	return false
}

func agentNodeIssue(code, severity string, node *agentGraphNode, field, message string) AgentDslIssue {
	params := map[string]string{"node": nodeDisplayName(node)}
	if field != "" {
		params["field"] = field
	}
	return AgentDslIssue{
		Code:      code,
		Severity:  severity,
		NodeID:    node.id,
		NodeLabel: node.label,
		Field:     field,
		Params:    params,
		Message:   message,
	}
}

func nodeDisplayName(node *agentGraphNode) string {
	if node == nil {
		return ""
	}
	if strings.TrimSpace(node.label) != "" {
		return node.label
	}
	return node.id
}

func agentStringList(value interface{}) []string {
	list, ok := value.([]interface{})
	if !ok {
		if text := strings.TrimSpace(stringFromAny(value)); text != "" {
			return []string{text}
		}
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if text := strings.TrimSpace(stringFromAny(item)); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func agentFloat(value interface{}) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		var parsed float64
		if _, err := fmt.Sscanf(trimmed, "%g", &parsed); err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}
