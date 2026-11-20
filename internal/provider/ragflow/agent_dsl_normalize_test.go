package ragflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// customerOpsDsl is the shape the customer file uses: canvas labels in
// component_name, hyphenated node ids, and references that point at them.
func customerOpsDsl() map[string]interface{} {
	return map[string]interface{}{
		"components": map[string]interface{}{
			"begin": map[string]interface{}{
				"obj":        map[string]interface{}{"component_name": "Begin", "params": map[string]interface{}{}},
				"upstream":   []interface{}{},
				"downstream": []interface{}{"node-b5dd407ff4d5"},
			},
			"node-b5dd407ff4d5": map[string]interface{}{
				"obj": map[string]interface{}{
					"component_name": "Knowledge",
					"params": map[string]interface{}{
						"kb_ids": []interface{}{"kb1"},
						"query":  "{sys.query}",
					},
				},
				"upstream":   []interface{}{"begin"},
				"downstream": []interface{}{"node-c5b4d7237ffb"},
			},
			"node-c5b4d7237ffb": map[string]interface{}{
				"obj": map[string]interface{}{
					"component_name": "Generate",
					"params": map[string]interface{}{
						"llm_id": "m1",
						"system": "只依据片段回答",
						"prompt": "片段：{node-b5dd407ff4d5@formalized_content}",
					},
				},
				"upstream":   []interface{}{"node-b5dd407ff4d5"},
				"downstream": []interface{}{"node-a01b2d6fc27a"},
			},
			"node-a01b2d6fc27a": map[string]interface{}{
				"obj": map[string]interface{}{
					"component_name": "Answer",
					"params": map[string]interface{}{
						"content": []interface{}{"{node-c5b4d7237ffb@content}"},
					},
				},
				"upstream":   []interface{}{"node-c5b4d7237ffb"},
				"downstream": []interface{}{},
			},
		},
		"graph": map[string]interface{}{
			"nodes": []interface{}{
				map[string]interface{}{
					"id":   "begin",
					"type": "beginNode",
					"data": map[string]interface{}{"label": "Begin", "form": map[string]interface{}{}},
				},
				map[string]interface{}{
					"id":   "node-b5dd407ff4d5",
					"type": "retrievalNode",
					"data": map[string]interface{}{"label": "Knowledge", "form": map[string]interface{}{"query": "{sys.query}"}},
				},
				map[string]interface{}{
					"id":   "node-c5b4d7237ffb",
					"type": "generalNode",
					"data": map[string]interface{}{
						"label": "Generate",
						"form": map[string]interface{}{
							"llm_id": "m1",
							"system": "只依据片段回答",
							"prompt": "片段：{node-b5dd407ff4d5@formalized_content}",
						},
					},
				},
				map[string]interface{}{
					"id":   "node-a01b2d6fc27a",
					"type": "generalNode",
					"data": map[string]interface{}{
						"label": "Answer",
						"form":  map[string]interface{}{"content": []interface{}{"{node-c5b4d7237ffb@content}"}},
					},
				},
			},
			"edges": []interface{}{
				map[string]interface{}{"id": "e1", "source": "begin", "target": "node-b5dd407ff4d5"},
				map[string]interface{}{"id": "e2", "source": "node-b5dd407ff4d5", "target": "node-c5b4d7237ffb"},
				map[string]interface{}{"id": "e3", "source": "node-c5b4d7237ffb", "target": "node-a01b2d6fc27a"},
			},
		},
	}
}

// The bug this guards: the customer's exported DSL compiled to
// `unknown component "Answer"`, which the API surfaces as "Internal storage
// error while accessing the agent."
func TestEngineAgentDslResolvesCanvasLabelsToEngineComponents(t *testing.T) {
	got := engineAgentDsl(customerOpsDsl())

	want := map[string]string{
		"begin":             "Begin",
		"node_b5dd407ff4d5": "retrieval",
		"node_c5b4d7237ffb": "llm",
		"node_a01b2d6fc27a": "message",
	}
	for id, name := range want {
		components, _ := got["components"].(map[string]interface{})
		component, ok := components[id].(map[string]interface{})
		if !ok {
			t.Fatalf("component %q missing: %v", id, components)
		}
		obj, _ := component["obj"].(map[string]interface{})
		if obj["component_name"] != name {
			t.Fatalf("component %q name = %v, want %q", id, obj["component_name"], name)
		}
	}
}

func TestEngineAgentDslReadsTheLabelWhenComponentNameIsMissing(t *testing.T) {
	dsl := customerOpsDsl()
	components, _ := dsl["components"].(map[string]interface{})
	obj := components["node-a01b2d6fc27a"].(map[string]interface{})["obj"].(map[string]interface{})
	delete(obj, "component_name")

	got := engineAgentDsl(dsl)
	component := got["components"].(map[string]interface{})["node_a01b2d6fc27a"].(map[string]interface{})
	if name := component["obj"].(map[string]interface{})["component_name"]; name != "message" {
		t.Fatalf("component_name = %v, want the label's engine name", name)
	}
}

func TestEngineAgentDslRekeysHyphenatedNodeIDs(t *testing.T) {
	got := engineAgentDsl(customerOpsDsl())
	components, _ := got["components"].(map[string]interface{})

	for _, stale := range []string{"node-b5dd407ff4d5", "node-c5b4d7237ffb", "node-a01b2d6fc27a"} {
		if _, ok := components[stale]; ok {
			t.Fatalf("component key %q survived: the engine cannot reference it", stale)
		}
	}
	knowledge := components["node_b5dd407ff4d5"].(map[string]interface{})
	if upstream := knowledge["upstream"].([]interface{}); upstream[0] != "begin" {
		t.Fatalf("upstream = %v", upstream)
	}
	if downstream := knowledge["downstream"].([]interface{}); downstream[0] != "node_c5b4d7237ffb" {
		t.Fatalf("downstream = %v", downstream)
	}

	graph, _ := got["graph"].(map[string]interface{})
	for _, raw := range graph["nodes"].([]interface{}) {
		node := raw.(map[string]interface{})
		if id := node["id"].(string); agentNodeIDForbidden.MatchString(id) {
			t.Fatalf("node id %q still holds a character the engine rejects", id)
		}
	}
	edges := graph["edges"].([]interface{})
	first := edges[1].(map[string]interface{})
	if first["source"] != "node_b5dd407ff4d5" || first["target"] != "node_c5b4d7237ffb" {
		t.Fatalf("edge not remapped: %v", first)
	}
}

// Without this, the engine happily copies "{node-c5b4d7237ffb@content}" into the
// answer instead of erroring — the failure mode that looks like a model problem.
func TestEngineAgentDslRewritesReferencesToTheNewIDs(t *testing.T) {
	got := engineAgentDsl(customerOpsDsl())

	params := paramsOf(t, got, "node_c5b4d7237ffb")
	if params["user_prompt"] != "片段：{node_b5dd407ff4d5@formalized_content}" {
		t.Fatalf("user_prompt = %v", params["user_prompt"])
	}
	answerParams := paramsOf(t, got, "node_a01b2d6fc27a")
	content := answerParams["content"].([]interface{})
	if content[0] != "{node_c5b4d7237ffb@content}" {
		t.Fatalf("content = %v", content)
	}
	form := formOf(t, got, "node_c5b4d7237ffb")
	if form["user_prompt"] != "片段：{node_b5dd407ff4d5@formalized_content}" {
		t.Fatalf("form.user_prompt = %v", form["user_prompt"])
	}
}

// The engine reads a node's links off `obj`, and the canvas writes them there.
// Remapping only the component-level copy left obj.upstream pointing at an id
// that no longer existed, so a re-keyed canvas died with "unknown upstream".
func TestEngineAgentDslRewritesObjLevelUpstreamAndDownstream(t *testing.T) {
	dsl := customerOpsDsl()
	obj := dsl["components"].(map[string]interface{})["node-a01b2d6fc27a"].(map[string]interface{})["obj"].(map[string]interface{})
	obj["upstream"] = []interface{}{"node-c5b4d7237ffb"}
	obj["downstream"] = []interface{}{}
	other := dsl["components"].(map[string]interface{})["node-c5b4d7237ffb"].(map[string]interface{})["obj"].(map[string]interface{})
	other["downstream"] = []interface{}{"node-a01b2d6fc27a"}

	got := engineAgentDsl(dsl)
	node := got["components"].(map[string]interface{})["node_a01b2d6fc27a"].(map[string]interface{})
	nodeObj := node["obj"].(map[string]interface{})
	upstream, _ := nodeObj["upstream"].([]interface{})
	if len(upstream) != 1 || upstream[0] != "node_c5b4d7237ffb" {
		t.Fatalf("obj.upstream = %v, want [node_c5b4d7237ffb]", nodeObj["upstream"])
	}
	peer := got["components"].(map[string]interface{})["node_c5b4d7237ffb"].(map[string]interface{})["obj"].(map[string]interface{})
	downstream, _ := peer["downstream"].([]interface{})
	if len(downstream) != 1 || downstream[0] != "node_a01b2d6fc27a" {
		t.Fatalf("obj.downstream = %v, want [node_a01b2d6fc27a]", peer["downstream"])
	}
}

func TestEngineAgentDslLeavesSystemAndEnvReferencesAlone(t *testing.T) {
	dsl := customerOpsDsl()
	params := paramsOf(t, dsl, "node-c5b4d7237ffb")
	params["prompt"] = "{sys.query} / {env.KB} / {item} / {index} / {{ node-b5dd407ff4d5@content }}"

	got := engineAgentDsl(dsl)
	text := paramsOf(t, got, "node_c5b4d7237ffb")["user_prompt"].(string)
	want := "{sys.query} / {env.KB} / {item} / {index} / {{ node_b5dd407ff4d5@content }}"
	if text != want {
		t.Fatalf("user_prompt = %q, want %q", text, want)
	}
}

func TestEngineAgentDslFollowsParentIDsOfIterationChildren(t *testing.T) {
	dsl := map[string]interface{}{
		"components": map[string]interface{}{
			"node-iter": map[string]interface{}{
				"obj":        map[string]interface{}{"component_name": "Iteration", "params": map[string]interface{}{}},
				"upstream":   []interface{}{},
				"downstream": []interface{}{"node-item"},
			},
			"node-item": map[string]interface{}{
				"obj":       map[string]interface{}{"component_name": "IterationItem", "params": map[string]interface{}{}},
				"upstream":  []interface{}{},
				"parent_id": "node-iter",
			},
		},
		"graph": map[string]interface{}{
			"nodes": []interface{}{
				map[string]interface{}{"id": "node-iter", "data": map[string]interface{}{"label": "Iteration"}},
				map[string]interface{}{"id": "node-item", "parentId": "node-iter", "data": map[string]interface{}{"label": "IterationItem"}},
			},
		},
	}

	got := engineAgentDsl(dsl)
	components := got["components"].(map[string]interface{})
	if parent := components["node_item"].(map[string]interface{})["parent_id"]; parent != "node_iter" {
		t.Fatalf("parent_id = %v, want node_iter", parent)
	}
	nodes := got["graph"].(map[string]interface{})["nodes"].([]interface{})
	if parent := nodes[1].(map[string]interface{})["parentId"]; parent != "node_iter" {
		t.Fatalf("graph parentId = %v, want node_iter", parent)
	}
}

func TestEngineAgentDslDisambiguatesIDsThatCollideAfterFolding(t *testing.T) {
	dsl := map[string]interface{}{
		"components": map[string]interface{}{
			"node-a": map[string]interface{}{"obj": map[string]interface{}{"component_name": "Begin", "params": map[string]interface{}{}}},
			"node_a": map[string]interface{}{"obj": map[string]interface{}{"component_name": "Message", "params": map[string]interface{}{"content": []interface{}{"x"}}}},
		},
	}

	got := engineAgentDsl(dsl)
	components := got["components"].(map[string]interface{})
	if len(components) != 2 {
		t.Fatalf("a rename swallowed a component: %v", components)
	}
	if _, ok := components["node_a"]; !ok {
		t.Fatalf("the already-legal id must keep its name: %v", components)
	}
	if _, ok := components["node_a_2"]; !ok {
		t.Fatalf("the folded id needed a suffix: %v", components)
	}
}

func TestEngineAgentDslIsIdempotent(t *testing.T) {
	once := engineAgentDsl(customerOpsDsl())
	twice := engineAgentDsl(once)

	before, _ := json.Marshal(once)
	after, _ := json.Marshal(twice)
	if string(before) != string(after) {
		t.Fatalf("second pass changed the DSL:\n once=%s\n twice=%s", before, after)
	}
}

func TestEngineAgentDslNormalizationLeavesTheCallerCopyAlone(t *testing.T) {
	original := customerOpsDsl()
	_ = engineAgentDsl(original)

	components := original["components"].(map[string]interface{})
	if _, ok := components["node-b5dd407ff4d5"]; !ok {
		t.Fatal("engineAgentDsl rekeyed its input")
	}
	obj := components["node-c5b4d7237ffb"].(map[string]interface{})["obj"].(map[string]interface{})
	if obj["component_name"] != "Generate" {
		t.Fatalf("engineAgentDsl renamed its input: %v", obj["component_name"])
	}
}

func TestEngineAgentDslOnlyRenamesPromptsOnPromptCarryingComponents(t *testing.T) {
	dsl := customerOpsDsl()
	// A Message node with a stray "prompt" key must keep it: only LLM nodes read
	// the prompt fields, and the canvas form of a Message has no such field.
	params := paramsOf(t, dsl, "node-a01b2d6fc27a")
	params["prompt"] = "untouched"

	got := engineAgentDsl(dsl)
	gotParams := paramsOf(t, got, "node_a01b2d6fc27a")
	if gotParams["prompt"] != "untouched" {
		t.Fatalf("message params rewritten: %v", gotParams)
	}
	if _, ok := gotParams["user_prompt"]; ok {
		t.Fatalf("message params gained a user_prompt: %v", gotParams)
	}
}

func TestCanvasAgentDslKeepsNodeIDsAsTheEngineStoredThem(t *testing.T) {
	got := canvasAgentDsl(engineAgentDsl(customerOpsDsl()))
	components, _ := got["components"].(map[string]interface{})
	if _, ok := components["node_c5b4d7237ffb"]; !ok {
		t.Fatalf("read path must hand back the engine's ids: %v", components)
	}
	if name := components["node_c5b4d7237ffb"].(map[string]interface{})["obj"].(map[string]interface{})["component_name"]; name != "llm" {
		t.Fatalf("read path changed the component name to %v", name)
	}
}

// Wire guard: the body that leaves for the engine is the corrected one — names,
// ids and references — and it stays corrected for updates too.
func TestHTTPClient_SendsACorrectedDslToTheEngine(t *testing.T) {
	var createBody, updateBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		switch {
		case r.URL.Path == "/api/v1/agents" && r.Method == http.MethodPost:
			_ = json.Unmarshal(raw, &createBody)
			writeEnvelope(t, w, map[string]interface{}{"id": "ag1", "title": "a"})
		case r.URL.Path == "/api/v1/agents/ag1" && r.Method == http.MethodPut:
			_ = json.Unmarshal(raw, &updateBody)
			writeEnvelope(t, w, map[string]interface{}{"update_time": 1})
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 1)
	ctx := context.Background()

	if _, err := client.CreateAgent(ctx, CreateAgentRequest{Title: "a", Dsl: customerOpsDsl()}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	wire := nested(t, createBody, "dsl", "components", "node_a01b2d6fc27a", "obj")
	if wire["component_name"] != "message" {
		t.Fatalf("create body component_name = %v", wire["component_name"])
	}
	params := nested(t, createBody, "dsl", "components", "node_c5b4d7237ffb", "obj", "params")
	if params["user_prompt"] != "片段：{node_b5dd407ff4d5@formalized_content}" {
		t.Fatalf("create body prompt = %v", params["user_prompt"])
	}

	if err := client.UpdateAgent(ctx, "ag1", UpdateAgentRequest{Dsl: customerOpsDsl()}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	updateParams := nested(t, updateBody, "dsl", "components", "node_c5b4d7237ffb", "obj", "params")
	if updateParams["system_prompt"] != "只依据片段回答" {
		t.Fatalf("update body system_prompt = %v", updateParams)
	}
}

// A palette Answer node has no editable fields, so it is saved with empty
// params and the engine rejects the whole agent with
// `[Message] content does not support empty value`.
func TestEngineAgentDslGivesABareAnswerNodeItsUpstreamContent(t *testing.T) {
	dsl := customerOpsDsl()
	params := paramsOf(t, dsl, "node-a01b2d6fc27a")
	delete(params, "content")

	got := engineAgentDsl(dsl)
	answerParams := paramsOf(t, got, "node_a01b2d6fc27a")
	content, _ := answerParams["content"].([]interface{})
	if len(content) != 1 || content[0] != "{node_c5b4d7237ffb@content}" {
		t.Fatalf("content = %v, want the LLM upstream's content", answerParams["content"])
	}
}

// The canvas keeps a node's links inside obj, and that is where the engine reads
// them. Looking only at the component level made the auto-forward a silent no-op
// for every DSL the editor produces: the answer stayed empty and the engine
// rejected the whole agent with "content does not support empty value".
func TestEngineAgentDslReadsTheAnswerUpstreamFromObj(t *testing.T) {
	dsl := customerOpsDsl()
	components := dsl["components"].(map[string]interface{})
	answer := components["node-a01b2d6fc27a"].(map[string]interface{})
	answerObj := answer["obj"].(map[string]interface{})
	delete(answerObj["params"].(map[string]interface{}), "content")
	delete(answer, "upstream")
	answerObj["upstream"] = []interface{}{"node-c5b4d7237ffb"}

	got := engineAgentDsl(dsl)
	answerParams := paramsOf(t, got, "node_a01b2d6fc27a")
	content, _ := answerParams["content"].([]interface{})
	if len(content) != 1 || content[0] != "{node_c5b4d7237ffb@content}" {
		t.Fatalf("content = %v, want the obj-level upstream's content", answerParams["content"])
	}
}

// The engine walks a workflow from the component-level `downstream`. A DSL that
// keeps its links only inside obj parses fine, runs zero nodes and answers with
// an empty string, so the boundary mirrors them up instead of shipping a
// silently dead agent.
func TestEngineAgentDslMirrorsLinksThatOnlyLiveInsideObj(t *testing.T) {
	dsl := customerOpsDsl()
	components := dsl["components"].(map[string]interface{})
	for _, raw := range components {
		component, _ := raw.(map[string]interface{})
		obj, _ := component["obj"].(map[string]interface{})
		for _, field := range []string{"upstream", "downstream"} {
			if links, ok := component[field]; ok {
				obj[field] = links
			}
			delete(component, field)
		}
	}

	got := engineAgentDsl(dsl)
	begin := got["components"].(map[string]interface{})["begin"].(map[string]interface{})
	if len(begin["downstream"].([]interface{})) != 1 {
		t.Fatalf("begin downstream = %v, want the obj-level link mirrored up", begin["downstream"])
	}
	// Idempotent: a second pass must not change anything.
	again := engineAgentDsl(got)
	againBegin := again["components"].(map[string]interface{})["begin"].(map[string]interface{})
	if fmt.Sprint(againBegin["downstream"]) != fmt.Sprint(begin["downstream"]) {
		t.Fatalf("mirroring is not idempotent: %v vs %v", againBegin["downstream"], begin["downstream"])
	}
	// A DSL that already declares component-level links keeps them untouched.
	// (node ids are folded on the way out, so compare against the folded form)
	declared := customerOpsDsl()
	want := fmt.Sprint(declared["components"].(map[string]interface{})["node-a01b2d6fc27a"].(map[string]interface{})["upstream"])
	want = strings.ReplaceAll(want, "node-", "node_")
	untouched := engineAgentDsl(declared)
	upstream := untouched["components"].(map[string]interface{})["node_a01b2d6fc27a"].(map[string]interface{})["upstream"]
	if fmt.Sprint(upstream) != want {
		t.Fatalf("declared links were rewritten: %v, want %v", upstream, want)
	}
}

func TestEngineAgentDslPrefersTheLLMUpstreamForABareAnswer(t *testing.T) {
	dsl := customerOpsDsl()
	components := dsl["components"].(map[string]interface{})
	answer := components["node-a01b2d6fc27a"].(map[string]interface{})
	delete(answer["obj"].(map[string]interface{})["params"].(map[string]interface{}), "content")
	// retrieval and llm both feed the answer; the llm is the one to echo.
	answer["upstream"] = []interface{}{"node-b5dd407ff4d5", "node-c5b4d7237ffb"}

	got := engineAgentDsl(dsl)
	content := paramsOf(t, got, "node_a01b2d6fc27a")["content"].([]interface{})
	if content[0] != "{node_c5b4d7237ffb@content}" {
		t.Fatalf("content = %v, want the LLM upstream", content)
	}
}

func TestEngineAgentDslLeavesAnAnswerWithOnlyABeginUpstreamAlone(t *testing.T) {
	dsl := customerOpsDsl()
	components := dsl["components"].(map[string]interface{})
	answer := components["node-a01b2d6fc27a"].(map[string]interface{})
	answer["obj"].(map[string]interface{})["params"] = map[string]interface{}{}
	answer["upstream"] = []interface{}{"begin"}

	got := engineAgentDsl(dsl)
	params := paramsOf(t, got, "node_a01b2d6fc27a")
	if _, ok := params["content"]; ok {
		t.Fatalf("invented content from a Begin node: %v", params)
	}
}

func TestEngineAgentDslKeepsAUserAuthoredAnswerTemplate(t *testing.T) {
	dsl := customerOpsDsl()
	params := paramsOf(t, dsl, "node-a01b2d6fc27a")
	params["content"] = []interface{}{"回答：", "{node-c5b4d7237ffb@content}"}

	got := engineAgentDsl(dsl)
	content := paramsOf(t, got, "node_a01b2d6fc27a")["content"].([]interface{})
	if len(content) != 2 || content[0] != "回答：" {
		t.Fatalf("content = %v, want the user's template", content)
	}
}
