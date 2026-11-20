package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// validChatDsl is the smallest canvas the validator accepts: a Start node whose
// output reaches an Answer node. Publish-path tests use it so they exercise the
// publish mechanics instead of the (now blocking) canvas checks.
func validChatDsl() map[string]interface{} {
	return map[string]interface{}{
		"components": map[string]interface{}{
			"begin": map[string]interface{}{
				"obj": map[string]interface{}{"component_name": "Begin", "params": map[string]interface{}{}},
			},
			"answer": map[string]interface{}{
				"obj": map[string]interface{}{
					"component_name": "Answer",
					"params":         map[string]interface{}{"content": []interface{}{"{sys.query}"}},
					"upstream":       []interface{}{"begin"},
				},
			},
		},
		"graph": map[string]interface{}{
			"nodes": []interface{}{
				map[string]interface{}{"id": "begin", "data": map[string]interface{}{"label": "Begin"}},
				map[string]interface{}{"id": "answer", "data": map[string]interface{}{"label": "Answer"}},
			},
			"edges": []interface{}{
				map[string]interface{}{"source": "begin", "target": "answer"},
			},
		},
	}
}

// brokenChatDsl is a canvas that saves fine and then fails on the first question:
// retrieval with no knowledge base, an LLM with no model and no prompt, a tiny
// token budget, and an Answer with nothing to forward.
func brokenChatDsl() map[string]interface{} {
	return map[string]interface{}{
		"components": map[string]interface{}{
			"begin": map[string]interface{}{
				"obj": map[string]interface{}{"component_name": "Begin", "params": map[string]interface{}{}},
			},
			"retr": map[string]interface{}{
				"obj": map[string]interface{}{
					"component_name": "Knowledge",
					"params":         map[string]interface{}{"kb_ids": []interface{}{}},
					"upstream":       []interface{}{"begin"},
					"downstream":     []interface{}{"llm"},
				},
			},
			"llm": map[string]interface{}{
				"obj": map[string]interface{}{
					"component_name": "Generate",
					"params": map[string]interface{}{
						"llm_id":      "",
						"max_tokens":  float64(256),
						"user_prompt": "",
					},
					"upstream":   []interface{}{"retr"},
					"downstream": []interface{}{"answer"},
				},
			},
			// No upstream and no template: there is nothing for the answer to say
			// and nothing to forward, so the boundary cannot repair it either.
			"answer": map[string]interface{}{
				"obj": map[string]interface{}{
					"component_name": "Answer",
					"params":         map[string]interface{}{},
				},
			},
		},
		"graph": map[string]interface{}{
			"nodes": []interface{}{
				map[string]interface{}{"id": "begin", "data": map[string]interface{}{"label": "Begin"}},
				map[string]interface{}{"id": "retr", "data": map[string]interface{}{"label": "Knowledge"}},
				map[string]interface{}{"id": "llm", "data": map[string]interface{}{"label": "Generate"}},
				map[string]interface{}{"id": "answer", "data": map[string]interface{}{"label": "Answer"}},
			},
		},
	}
}

func issueCodes(issues []AgentDslIssue) []string {
	codes := make([]string, 0, len(issues))
	for _, issue := range issues {
		codes = append(codes, issue.Code)
	}
	return codes
}

func containsCode(issues []AgentDslIssue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestValidateAgentDSLAcceptsAConnectedCanvas(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "TenantValidateOK")
	if err != nil {
		t.Fatal(err)
	}
	report := svc.ValidateAgentDSL(context.Background(), agentScope(tenant.ID), validChatDsl(), "agent_canvas")
	if !report.OK {
		t.Fatalf("a start->answer canvas must validate, got errors %v", issueCodes(report.Errors))
	}
	if report.NodeCount != 2 {
		t.Fatalf("node count = %d, want 2", report.NodeCount)
	}
}

// The point of validation: every one of these used to be discovered as a runtime
// failure ("No dataset is selected", "at least one of user_prompt or
// system_prompt must be set", an empty answer) long after the author published.
func TestValidateAgentDSLReportsBlockingAuthoringMistakes(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "TenantValidateBroken")
	if err != nil {
		t.Fatal(err)
	}
	report := svc.ValidateAgentDSL(context.Background(), agentScope(tenant.ID), brokenChatDsl(), "agent_canvas")
	if report.OK {
		t.Fatal("a canvas with no kb, no model and no prompt must not validate")
	}
	for _, code := range []string{
		"retrieval_dataset_missing",
		"llm_model_missing",
		"llm_prompt_missing",
		"llm_max_tokens_blocked",
		"answer_template_missing",
	} {
		if !containsCode(report.Errors, code) {
			t.Fatalf("missing %q in %v", code, issueCodes(report.Errors))
		}
	}
	// Node attribution is what makes the report actionable in the inspector.
	for _, issue := range report.Errors {
		if issue.Code == "retrieval_dataset_missing" && (issue.NodeID != "retr" || issue.NodeLabel != "Knowledge") {
			t.Fatalf("dataset issue must name the node it belongs to: %+v", issue)
		}
	}
}

// The validator must judge the runnable form, not the raw file: canvas labels,
// hyphenated ids and the canvas' `prompt`/`system` keys are corrected at the
// boundary, so a DSL written that way is not "broken".
func TestValidateAgentDSLJudgesTheNormalizedDsl(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "TenantValidateNormalized")
	if err != nil {
		t.Fatal(err)
	}
	dsl := map[string]interface{}{
		"components": map[string]interface{}{
			"begin": map[string]interface{}{
				"obj": map[string]interface{}{"component_name": "Begin", "params": map[string]interface{}{}},
			},
			"node-a01b2d6fc27a": map[string]interface{}{
				"obj": map[string]interface{}{
					"component_name": "llm",
					"params": map[string]interface{}{
						"llm_id":     "mock-chat-model@mock-instance@Mock",
						"max_tokens": float64(2048),
						"prompt":     "answer the question",
					},
					"upstream": []interface{}{"begin"},
				},
			},
			"node-b5dd407ff4d5": map[string]interface{}{
				"obj": map[string]interface{}{
					"component_name": "Answer",
					"params":         map[string]interface{}{"content": []interface{}{"{node-a01b2d6fc27a@content}"}},
					"upstream":       []interface{}{"node-a01b2d6fc27a"},
				},
			},
		},
	}
	report := svc.ValidateAgentDSL(context.Background(), agentScope(tenant.ID), dsl, "agent_canvas")

	kinds := map[string]bool{}
	for _, fix := range report.AutoFixed {
		kinds[fix.Kind] = true
	}
	for _, kind := range []string{"node_id", "prompt_param"} {
		if !kinds[kind] {
			t.Fatalf("auto_fixed must report the %q correction, got %+v", kind, report.AutoFixed)
		}
	}
	// The reference to the hyphenated node was rewritten, so the edge must not be
	// reported as dangling.
	if containsCode(report.Errors, "edge_dangling") {
		t.Fatalf("rewritten references must resolve: %v", issueCodes(report.Errors))
	}
	if containsCode(report.Errors, "answer_template_missing") {
		t.Fatalf("the answer forwards its upstream: %v", issueCodes(report.Errors))
	}
}

func TestValidateAgentDSLWarnsAboutUnreachableNodes(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "TenantValidateOrphan")
	if err != nil {
		t.Fatal(err)
	}
	dsl := validChatDsl()
	components := dsl["components"].(map[string]interface{})
	components["orphan"] = map[string]interface{}{
		"obj": map[string]interface{}{
			"component_name": "Answer",
			"params":         map[string]interface{}{"content": []interface{}{"unused"}},
		},
	}
	report := svc.ValidateAgentDSL(context.Background(), agentScope(tenant.ID), dsl, "agent_canvas")
	if report.OK != true {
		t.Fatalf("an orphan Answer is a warning, not an error: %v", issueCodes(report.Errors))
	}
	if !containsCode(report.Warnings, "node_unreachable") {
		t.Fatalf("expected node_unreachable warning, got %v", issueCodes(report.Warnings))
	}
}

// unknownComponentProvider stands in for the live engine catalog so the rule can
// be tested without a deployment.
type unknownComponentProvider struct {
	ragflow.Client
	catalog map[string]bool
}

func (p *unknownComponentProvider) ComponentCatalog(context.Context) (map[string]bool, error) {
	return p.catalog, nil
}

func TestValidateAgentDSLUsesTheLiveComponentCatalog(t *testing.T) {
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(context.Background(), "TenantValidateCatalog")
	if err != nil {
		t.Fatal(err)
	}
	dsl := validChatDsl()
	components := dsl["components"].(map[string]interface{})
	components["weird"] = map[string]interface{}{
		"obj": map[string]interface{}{
			"component_name": "snowflake",
			"params":         map[string]interface{}{},
			"upstream":       []interface{}{"begin"},
		},
	}
	svc.RAGFlow = &unknownComponentProvider{
		Client:  svc.RAGFlow,
		catalog: map[string]bool{"begin": true, "message": true, "retrieval": true, "llm": true},
	}
	report := svc.ValidateAgentDSL(context.Background(), agentScope(tenant.ID), dsl, "agent_canvas")
	if !containsCode(report.Errors, "component_unknown") {
		t.Fatalf("expected component_unknown, got %v", issueCodes(report.Errors))
	}

	// And a name the boundary maps is not unknown: `Answer` becomes `message`.
	report = svc.ValidateAgentDSL(context.Background(), agentScope(tenant.ID), validChatDsl(), "agent_canvas")
	if containsCode(report.Errors, "component_unknown") {
		t.Fatalf("canvas labels are normalised before the check: %v", issueCodes(report.Errors))
	}
}

func TestPublishAgentRefusesACanvasThatCannotRun(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantPublishGate")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := svc.CreateAgent(ctx, tenant.ID, "broken-canvas", brokenChatDsl(), false, "agent_canvas")
	if err != nil {
		t.Fatalf("saving a broken canvas must stay possible: %v", err)
	}
	_, err = svc.PublishAgent(ctx, tenant.ID, agent.ID, true, false)
	if err == nil {
		t.Fatal("publishing a canvas with blocking issues must fail")
	}
	he, ok := err.(*httperr.Error)
	if !ok {
		t.Fatalf("unexpected error type %T (%v)", err, err)
	}
	if he.Code != 40099 {
		t.Fatalf("publish refusal must use 40099, got %d (%s)", he.Code, he.Message)
	}
	if got := svc.mustVersions(t, ctx, tenant.ID, agent.ID); got != 0 {
		t.Fatalf("a refused publish must not write a version, got %d", got)
	}

	// Fixing the canvas unblocks publishing.
	good, err := svc.CreateAgent(ctx, tenant.ID, "good-canvas", validChatDsl(), false, "agent_canvas")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PublishAgent(ctx, tenant.ID, good.ID, true, false); err != nil {
		t.Fatalf("a valid canvas must publish: %v", err)
	}
	if got := svc.mustVersions(t, ctx, tenant.ID, good.ID); got != 1 {
		t.Fatalf("publish must write exactly one version, got %d", got)
	}
}

func TestCreateAgentRefusesToReleaseABrokenCanvas(t *testing.T) {
	ctx := context.Background()
	svc := newAuthzSvc(t)
	tenant, err := svc.CreateTenant(ctx, "TenantCreateGate")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.CreateAgent(ctx, tenant.ID, "released-broken", brokenChatDsl(), true, "agent_canvas")
	if err == nil {
		t.Fatal("creating a released canvas with blocking issues must fail")
	}
	if got := fmt.Sprint(err); got == "" {
		t.Fatal("expected a message")
	}
	agents, _, err := svc.ListAgents(ctx, tenant.ID, false, repository.AgentFilter{}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range agents {
		if a.Title == "released-broken" {
			t.Fatal("a refused create must not leave an agent behind")
		}
	}
}

func (s *Service) mustVersions(t *testing.T, ctx context.Context, tenantID, agentID string) int {
	t.Helper()
	versions, err := s.ListAgentVersions(ctx, tenantID, agentID, false)
	if err != nil {
		t.Fatal(err)
	}
	return len(versions)
}
