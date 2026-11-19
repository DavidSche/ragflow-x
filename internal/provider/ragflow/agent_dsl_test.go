package ragflow

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// canvasGenerateDsl is the shape the canvas stores for a Generate node: the two
// prompt fields carry the form's names ("system" / "prompt").
func canvasGenerateDsl() map[string]interface{} {
	return map[string]interface{}{
		"graph": map[string]interface{}{
			"nodes": []interface{}{
				map[string]interface{}{
					"id":   "retr",
					"data": map[string]interface{}{"label": "Knowledge", "form": map[string]interface{}{"query": "{sys.query}"}},
				},
				map[string]interface{}{
					"id": "llm",
					"data": map[string]interface{}{
						"label": "Generate",
						"form": map[string]interface{}{
							"llm_id":      "m1",
							"system":      "只依据片段回答",
							"prompt":      "{retr@formalized_content}",
							"temperature": 0.1,
						},
					},
				},
			},
		},
		"components": map[string]interface{}{
			"llm": map[string]interface{}{
				"obj": map[string]interface{}{
					"component_name": "LLM",
					"params": map[string]interface{}{
						"llm_id": "m1",
						"system": "只依据片段回答",
						"prompt": "{retr@formalized_content}",
					},
				},
				"upstream": []interface{}{"retr"},
			},
		},
	}
}

func formOf(t *testing.T, dsl map[string]interface{}, nodeID string) map[string]interface{} {
	t.Helper()
	graph, _ := dsl["graph"].(map[string]interface{})
	nodes, _ := graph["nodes"].([]interface{})
	for _, raw := range nodes {
		node, _ := raw.(map[string]interface{})
		if node["id"] != nodeID {
			continue
		}
		data, _ := node["data"].(map[string]interface{})
		form, _ := data["form"].(map[string]interface{})
		return form
	}
	t.Fatalf("node %q not found", nodeID)
	return nil
}

func paramsOf(t *testing.T, dsl map[string]interface{}, componentID string) map[string]interface{} {
	t.Helper()
	components, _ := dsl["components"].(map[string]interface{})
	component, _ := components[componentID].(map[string]interface{})
	obj, _ := component["obj"].(map[string]interface{})
	params, _ := obj["params"].(map[string]interface{})
	return params
}

// The bug this guards: both prompt fields were filled in on the canvas, and the
// engine still answered `invalid param "user_prompt": at least one of user_prompt
// or system_prompt must be set` (visible to users as 50289). Sending the canvas
// names upstream is the whole failure; the engine has no alias for "system" and
// its "prompt" alias lives on a stub that cannot call a model.
func TestEngineAgentDslSendsPromptNamesTheEngineReads(t *testing.T) {
	got := engineAgentDsl(canvasGenerateDsl())

	form := formOf(t, got, "llm")
	if form["system_prompt"] != "只依据片段回答" {
		t.Fatalf("form.system_prompt = %v, want the system prompt text", form["system_prompt"])
	}
	if form["user_prompt"] != "{retr@formalized_content}" {
		t.Fatalf("form.user_prompt = %v, want the user prompt template", form["user_prompt"])
	}
	if _, stale := form["system"]; stale {
		t.Fatal("form.system must not be sent: the engine ignores it")
	}
	if _, stale := form["prompt"]; stale {
		t.Fatal("form.prompt must not be sent: only the non-runnable v1 stub reads it")
	}
	if form["temperature"] != 0.1 || form["llm_id"] != "m1" {
		t.Fatalf("unrelated params changed: %v", form)
	}

	params := paramsOf(t, got, "llm")
	if params["system_prompt"] == nil || params["user_prompt"] == nil {
		t.Fatalf("components params not renamed: %v", params)
	}

	// A Knowledge node's params are not prompt fields and must be left alone.
	knowledgeForm := formOf(t, got, "retr")
	if knowledgeForm["query"] != "{sys.query}" {
		t.Fatalf("unrelated node changed: %v", knowledgeForm)
	}
}

func TestCanvasAgentDslHandsTheFormItsOwnNames(t *testing.T) {
	engine := engineAgentDsl(canvasGenerateDsl())
	back := canvasAgentDsl(engine)

	form := formOf(t, back, "llm")
	if form["system"] != "只依据片段回答" || form["prompt"] != "{retr@formalized_content}" {
		t.Fatalf("round trip lost the prompts: %v", form)
	}
	for _, engineName := range []string{"system_prompt", "user_prompt"} {
		if _, stale := form[engineName]; stale {
			t.Fatalf("form still carries %q after the reverse rename", engineName)
		}
	}
}

func TestAgentDslRenameLeavesTheCallerCopyAlone(t *testing.T) {
	original := canvasGenerateDsl()
	_ = engineAgentDsl(original)
	if original["graph"] == nil {
		t.Fatal("graph vanished")
	}
	form := formOf(t, original, "llm")
	if form["system"] == nil || form["prompt"] == nil {
		t.Fatalf("engineAgentDsl mutated its input: %v", form)
	}
}

func TestAgentDslRenameNeverOverwritesTheEngineKey(t *testing.T) {
	dsl := canvasGenerateDsl()
	form := formOf(t, dsl, "llm")
	form["user_prompt"] = "already explicit"
	got := engineAgentDsl(dsl)
	if form := formOf(t, got, "llm"); form["user_prompt"] != "already explicit" {
		t.Fatalf("explicit user_prompt was overwritten by the legacy prompt alias: %v", form["user_prompt"])
	}
}

func TestAgentDslRenameIsNilSafe(t *testing.T) {
	if got := engineAgentDsl(nil); got != nil {
		t.Fatalf("engineAgentDsl(nil) = %v, want nil", got)
	}
	if got := canvasAgentDsl(nil); got != nil {
		t.Fatalf("canvasAgentDsl(nil) = %v, want nil", got)
	}
	if got := engineAgentDsl(map[string]interface{}{}); len(got) != 0 {
		t.Fatalf("engineAgentDsl({}) = %v, want empty", got)
	}
}

// Wire-level guard: the rename has to survive JSON encoding, and a read has to
// come back in the canvas' shape, otherwise the editor shows two empty textareas
// while the agent runs fine.
func TestHTTPClient_AgentPromptsTravelUnderEngineNames(t *testing.T) {
	var createBody map[string]interface{}
	var updateBody map[string]interface{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/api/v1/agents":
			_ = json.Unmarshal(raw, &createBody)
			writeEnvelope(t, w, map[string]interface{}{"id": "ag1", "title": "a"})
		case "/api/v1/agents/ag1":
			if r.Method == http.MethodPut {
				_ = json.Unmarshal(raw, &updateBody)
				writeEnvelope(t, w, map[string]interface{}{"update_time": 1})
				return
			}
			// The engine answers with its own param names.
			writeEnvelope(t, w, map[string]interface{}{
				"id": "ag1",
				"dsl": map[string]interface{}{
					"components": map[string]interface{}{
						"llm": map[string]interface{}{
							"obj": map[string]interface{}{
								"component_name": "LLM",
								"params": map[string]interface{}{
									"system_prompt": "只依据片段回答",
									"user_prompt":   "问题：{sys.query}",
								},
							},
						},
					},
				},
			})
		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := NewHTTPClient(srv.URL, "key", 5*time.Second, 1)
	ctx := context.Background()

	if _, err := client.CreateAgent(ctx, CreateAgentRequest{Title: "a", Dsl: canvasGenerateDsl()}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	wireParams := nested(t, createBody, "dsl", "components", "llm", "obj", "params")
	if wireParams["system_prompt"] == nil || wireParams["user_prompt"] == nil {
		t.Fatalf("create body did not use the engine's prompt names: %v", wireParams)
	}
	if wireParams["system"] != nil || wireParams["prompt"] != nil {
		t.Fatalf("create body still carries canvas names: %v", wireParams)
	}

	if err := client.UpdateAgent(ctx, "ag1", UpdateAgentRequest{Dsl: canvasGenerateDsl()}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	updateParams := nested(t, updateBody, "dsl", "components", "llm", "obj", "params")
	if updateParams["user_prompt"] == nil {
		t.Fatalf("update body did not use the engine's prompt names: %v", updateParams)
	}

	agent, err := client.GetAgent(ctx, "ag1")
	if err != nil {
		t.Fatalf("GetAgent: %v", err)
	}
	readParams := nested(t, agent.Dsl, "components", "llm", "obj", "params")
	if readParams["system"] != "只依据片段回答" || readParams["prompt"] != "问题：{sys.query}" {
		t.Fatalf("read path did not restore the canvas' names: %v", readParams)
	}
}

func nested(t *testing.T, root map[string]interface{}, path ...string) map[string]interface{} {
	t.Helper()
	current := root
	for _, key := range path {
		next, ok := current[key].(map[string]interface{})
		if !ok {
			t.Fatalf("%v: %q missing in %v", path, key, current)
		}
		current = next
	}
	return current
}
