package ragflow

import "strings"

// The canvas stores the Generate / LLM node's two prompt fields under the names
// the editor form binds to: "system" and "prompt". The Go engine's LLM
// component reads "system_prompt" and "user_prompt" and nothing else — its only
// alias for the system prompt is "sys_prompt".
//
// The v1 "Generate" stub in the engine does read "prompt"/"system", but it is
// the same object as the LLM component under another name; the boundary maps
// Generate onto llm (see agent_dsl_normalize.go), so every prompt-carrying node
// on the wire is spelled the way the LLM component reads it. Keeping the canvas'
// names on the wire made an agent with both prompt fields plainly filled in fail
// every completion with
//
//	invalid param "user_prompt": at least one of user_prompt or system_prompt must be set
//
// surfaced to the user as 50289. The canvas 4-node template (Begin → Knowledge →
// Generate → Answer) could be saved, published and inspected, and only broke at
// the moment it was asked to answer.
//
// The names are therefore translated here, on the engine boundary, in both
// directions: writes carry the engine's names, reads hand the canvas back the
// names its form binds to. RAGFlow-X keeps one canonical shape internally, so the
// JSON tab, the version diff and imported templates all keep working.
var agentPromptParamNames = map[string]string{
	"system": "system_prompt",
	"prompt": "user_prompt",
}

// agentLLMComponentNames are the component_name values whose params hold the two
// prompt fields. Writes have already been normalized to "llm" by the time the
// rename runs; "generate" stays in the set so a DSL read back from an engine
// that still stores the v1 name is handed to the canvas in its own shape too.
var agentLLMComponentNames = map[string]bool{
	"llm":      true,
	"generate": true,
}

// engineAgentDsl returns a copy of dsl ready to be sent upstream: component
// names resolved to the engine's registry, node ids and references made legal
// for its variable grammar, then the prompt params renamed to the names the
// engine reads, then a bare Answer node pointed at its upstream. Call it before
// sending a DSL upstream.
func engineAgentDsl(dsl map[string]interface{}) map[string]interface{} {
	return normalizeAgentMessageContent(
		renameAgentPromptParams(
			mirrorAgentNodeLinks(normalizeAgentNodeIDs(normalizeAgentComponentNames(dsl))),
			agentPromptParamNames,
		),
	)
}

// PrepareAgentDslForEngine exposes the boundary normalization so authoring-time
// validation can inspect exactly what the engine will receive, instead of
// re-deriving the corrections (and drifting from them).
func PrepareAgentDslForEngine(dsl map[string]interface{}) map[string]interface{} {
	return engineAgentDsl(dsl)
}

// canvasAgentDsl is the inverse of engineAgentDsl: it returns a copy of dsl with
// the engine's prompt param names renamed to the ones the canvas form binds to.
// Call it after reading a DSL from upstream.
func canvasAgentDsl(dsl map[string]interface{}) map[string]interface{} {
	inverse := make(map[string]string, len(agentPromptParamNames))
	for canvasName, engineName := range agentPromptParamNames {
		inverse[engineName] = canvasName
	}
	return renameAgentPromptParams(dsl, inverse)
}

func renameAgentPromptParams(dsl map[string]interface{}, renames map[string]string) map[string]interface{} {
	if dsl == nil {
		return nil
	}
	// Deep copy: the service layer keeps the caller's DSL (it is what the version
	// diff and the response body are built from), so the wire shape must not
	// mutate it in place.
	out, ok := cloneJSONValue(dsl).(map[string]interface{})
	if !ok {
		return dsl
	}
	walkAgentPromptParams(out, renames)
	return out
}

func walkAgentPromptParams(node interface{}, renames map[string]string) {
	switch typed := node.(type) {
	case map[string]interface{}:
		// Canonical shape: components[id].obj = {component_name, params}.
		if obj, ok := typed["obj"].(map[string]interface{}); ok {
			if agentLLMComponentName(obj["component_name"]) {
				if params, ok := obj["params"].(map[string]interface{}); ok {
					renameAgentPromptKeys(params, renames)
				}
			}
		}
		// Graph shape: nodes[i].data = {label, form}.
		if data, ok := typed["data"].(map[string]interface{}); ok {
			if agentLLMLabel(data["label"]) {
				if form, ok := data["form"].(map[string]interface{}); ok {
					renameAgentPromptKeys(form, renames)
				}
			}
		}
		for _, child := range typed {
			walkAgentPromptParams(child, renames)
		}
	case []interface{}:
		for _, child := range typed {
			walkAgentPromptParams(child, renames)
		}
	}
}

func renameAgentPromptKeys(params map[string]interface{}, renames map[string]string) {
	for from, to := range renames {
		value, ok := params[from]
		if !ok {
			continue
		}
		if _, taken := params[to]; !taken {
			params[to] = value
		}
		delete(params, from)
	}
}

func agentLLMComponentName(value interface{}) bool {
	name, ok := value.(string)
	if !ok {
		return false
	}
	return agentLLMComponentNames[strings.ToLower(strings.TrimSpace(name))]
}

func agentLLMLabel(value interface{}) bool {
	return agentLLMComponentName(value)
}

func cloneJSONValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			out[key] = cloneJSONValue(child)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, child := range typed {
			out[i] = cloneJSONValue(child)
		}
		return out
	default:
		return value
	}
}
