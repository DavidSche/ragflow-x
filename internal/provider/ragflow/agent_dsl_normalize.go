package ragflow

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The engine boundary is the one place where the canvas' vocabulary has to
// become runnable. Three things get corrected here, all of them fatal if they
// reach the engine untouched:
//
//  1. component_name. The editor keeps friendly labels (Knowledge / Answer /
//     Generate …) in graph.nodes[].data.label, and older DSLs — including files
//     exported from the upstream UI — also carry them in obj.component_name.
//     The engine only knows its registry names, so compile fails with
//     `unknown component "Answer"`, which the API reports to users as the
//     useless "Internal storage error while accessing the agent."
//  2. node ids. The engine's variable reference grammar is
//     `[a-zA-Z:0-9_]+@[A-Za-z0-9_.-]+`, so an id like `node-b5dd407ff4d5`
//     makes `{node-b5dd407ff4d5@content}` unresolvable: the engine does not
//     error, it copies the literal braces into the answer. Ids therefore get
//     every character outside `[A-Za-z0-9_:]` folded to `_`, and every
//     reference (params, forms, upstream/downstream, parent_id) is rewritten to
//     match.
//  3. prompt param names. See agent_dsl.go — the rename happens after this pass
//     so it can key off the resolved component name.
//
// The pass is idempotent: running it on its own output changes nothing. That
// matters because the canvas re-sends whatever the engine handed back.

// agentComponentAliases maps the label (or legacy name) that reaches the
// boundary onto a component the engine registers. Every target below was
// verified against the running engine's component catalog; the rest of the
// canvas vocabulary (Begin, Message, Parser, Tokenizer, TokenChunker,
// TitleChunker, Extractor, Compiler, Switch, Loop, ExcelProcessor, …) is already
// spelled the way the engine resolves it — its registry lowercases on lookup —
// so those names pass through untouched. Anything unknown is left alone too: a
// genuinely missing component must still fail loudly instead of being silently
// rewritten into something else.
var agentComponentAliases = map[string]string{
	"generate":  "llm",       // the canvas' Generate node is the LLM component; the engine's "Generate" is a v1 stub that ignores user_prompt
	"knowledge": "retrieval", // canvas label
	"answer":    "message",   // canvas label
	"chunker":   "tokenizer", // canvas label
	"code":      "codeexec",  // canvas label
}

// canonicalAgentComponentName resolves a component name to its engine spelling.
// It returns "" when the name is already fine (or unknown to this table), which
// tells the caller to leave the DSL alone.
func canonicalAgentComponentName(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return ""
	}
	return agentComponentAliases[key]
}

// normalizeAgentComponentNames rewrites obj.component_name to the engine's
// spelling. A component whose name is missing borrows the label of its canvas
// node, because that is what the editor would have sent.
func normalizeAgentComponentNames(dsl map[string]interface{}) map[string]interface{} {
	if dsl == nil {
		return nil
	}
	out, ok := cloneJSONValue(dsl).(map[string]interface{})
	if !ok {
		return dsl
	}
	labels := agentNodeLabels(out)
	if components, ok := out["components"].(map[string]interface{}); ok {
		ids := sortedKeys(components)
		for _, id := range ids {
			component, ok := components[id].(map[string]interface{})
			if !ok {
				continue
			}
			obj, ok := component["obj"].(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := obj["component_name"].(string)
			if strings.TrimSpace(name) == "" {
				name = labels[id]
			}
			if canonical := canonicalAgentComponentName(name); canonical != "" {
				obj["component_name"] = canonical
			}
		}
	}
	return out
}

// agentNodeLabels indexes the canvas labels by node id. Only used as a fallback
// for components that lost their component_name.
func agentNodeLabels(dsl map[string]interface{}) map[string]string {
	labels := map[string]string{}
	graph, _ := dsl["graph"].(map[string]interface{})
	nodes, _ := graph["nodes"].([]interface{})
	for _, raw := range nodes {
		node, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := node["id"].(string)
		data, _ := node["data"].(map[string]interface{})
		label, _ := data["label"].(string)
		if strings.TrimSpace(id) != "" && strings.TrimSpace(label) != "" {
			labels[id] = label
		}
	}
	return labels
}

// agentNodeIDForbidden matches everything the engine's reference grammar does
// not accept inside a node id.
var agentNodeIDForbidden = regexp.MustCompile(`[^A-Za-z0-9_:]`)

// normalizeAgentNodeIDs folds illegal characters out of every node id and
// rewrites everything that points at a node: the components map keys, their
// upstream/downstream lists and parent_id, the graph nodes/edges, and every
// `{id@…}` reference inside param and form values.
func normalizeAgentNodeIDs(dsl map[string]interface{}) map[string]interface{} {
	if dsl == nil {
		return nil
	}
	out, ok := cloneJSONValue(dsl).(map[string]interface{})
	if !ok {
		return dsl
	}
	idMap := agentNodeIDMap(out)
	if len(idMap) == 0 {
		return out
	}
	applyAgentNodeIDMap(out, idMap)
	rewriteAgentVarRefsIn(out, idMap)
	return out
}

// agentNodeIDMap builds old→new for every id that has to change. Ids that are
// already legal are absent from the map, so a clean DSL (or a second pass over
// an already-normalized one) produces no edits at all.
//
// Collisions are resolved deterministically: legal ids keep their spelling, and
// a folded id that would land on a taken name gets a numeric suffix.
func agentNodeIDMap(dsl map[string]interface{}) map[string]string {
	ids := map[string]bool{}
	if components, ok := dsl["components"].(map[string]interface{}); ok {
		for id := range components {
			ids[id] = true
		}
	}
	graph, _ := dsl["graph"].(map[string]interface{})
	nodes, _ := graph["nodes"].([]interface{})
	for _, raw := range nodes {
		if node, ok := raw.(map[string]interface{}); ok {
			if id, ok := node["id"].(string); ok {
				ids[id] = true
			}
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)

	idMap := make(map[string]string, len(ordered))
	taken := make(map[string]bool, len(ordered))
	// Legal ids are reserved first: an id the engine can already reference is
	// never displaced by one that merely folds into the same spelling, and the
	// result does not depend on which of the two sorts first.
	for _, old := range ordered {
		if strings.TrimSpace(old) == old && old != "" && !agentNodeIDForbidden.MatchString(old) {
			taken[old] = true
		}
	}
	for _, old := range ordered {
		candidate := strings.TrimSpace(old)
		if candidate == "" {
			continue
		}
		candidate = agentNodeIDForbidden.ReplaceAllString(candidate, "_")
		if candidate == old {
			continue
		}
		base := candidate
		for suffix := 2; taken[candidate]; suffix++ {
			candidate = base + "_" + strconv.Itoa(suffix)
		}
		idMap[old] = candidate
		taken[candidate] = true
	}
	return idMap
}

func applyAgentNodeIDMap(dsl map[string]interface{}, idMap map[string]string) {
	if components, ok := dsl["components"].(map[string]interface{}); ok {
		rekeyed := make(map[string]interface{}, len(components))
		for id, component := range components {
			newID := remapAgentID(id, idMap)
			if entry, ok := component.(map[string]interface{}); ok {
				entry["upstream"] = remapAgentIDList(entry["upstream"], idMap)
				entry["downstream"] = remapAgentIDList(entry["downstream"], idMap)
				if parent, ok := entry["parent_id"].(string); ok {
					entry["parent_id"] = remapAgentID(parent, idMap)
				}
				// The engine reads the links off `obj`, and the canvas writes them
				// there too: remapping only the component-level copy left the obj
				// pointing at ids that no longer existed ("unknown upstream").
				if obj, ok := entry["obj"].(map[string]interface{}); ok {
					obj["upstream"] = remapAgentIDList(obj["upstream"], idMap)
					obj["downstream"] = remapAgentIDList(obj["downstream"], idMap)
					if parent, ok := obj["parent_id"].(string); ok {
						obj["parent_id"] = remapAgentID(parent, idMap)
					}
				}
			}
			rekeyed[newID] = component
		}
		dsl["components"] = rekeyed
	}
	graph, _ := dsl["graph"].(map[string]interface{})
	if graph == nil {
		return
	}
	if nodes, ok := graph["nodes"].([]interface{}); ok {
		for _, raw := range nodes {
			node, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			if id, ok := node["id"].(string); ok {
				node["id"] = remapAgentID(id, idMap)
			}
			if parent, ok := node["parentId"].(string); ok {
				node["parentId"] = remapAgentID(parent, idMap)
			}
		}
	}
	if edges, ok := graph["edges"].([]interface{}); ok {
		for _, raw := range edges {
			edge, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			if source, ok := edge["source"].(string); ok {
				edge["source"] = remapAgentID(source, idMap)
			}
			if target, ok := edge["target"].(string); ok {
				edge["target"] = remapAgentID(target, idMap)
			}
		}
	}
}

func remapAgentID(id string, idMap map[string]string) string {
	if mapped, ok := idMap[id]; ok {
		return mapped
	}
	return id
}

func remapAgentIDList(value interface{}, idMap map[string]string) interface{} {
	list, ok := value.([]interface{})
	if !ok {
		return value
	}
	out := make([]interface{}, len(list))
	for i, item := range list {
		if id, ok := item.(string); ok {
			out[i] = remapAgentID(id, idMap)
			continue
		}
		out[i] = item
	}
	return out
}

// agentVarRefIDPattern mirrors the engine's own reference grammar closely enough
// to find the node id in `{id@path}`, `{{ id@path }}` and `{+id@path+}`. system
// and env references (`{sys.query}`, `{env.foo}`) carry no `@`, so they never
// match.
var agentVarRefIDPattern = regexp.MustCompile(`\{+\s*([A-Za-z0-9_:.\-]+)\s*@`)

func rewriteAgentVarRefsIn(dsl map[string]interface{}, idMap map[string]string) {
	walkAgentValues(dsl, func(value string) string {
		return rewriteAgentVarRefs(value, idMap)
	})
}

func rewriteAgentVarRefs(value string, idMap map[string]string) string {
	if !strings.Contains(value, "@") {
		return value
	}
	return agentVarRefIDPattern.ReplaceAllStringFunc(value, func(match string) string {
		groups := agentVarRefIDPattern.FindStringSubmatch(match)
		mapped, ok := idMap[groups[1]]
		if !ok {
			return match
		}
		return strings.Replace(match, groups[1], mapped, 1)
	})
}

// walkAgentValues rewrites every string leaf in place (the caller passes an
// already-cloned DSL), leaving map keys alone.
func walkAgentValues(node interface{}, rewrite func(string) string) {
	switch typed := node.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if value, ok := child.(string); ok {
				typed[key] = rewrite(value)
				continue
			}
			walkAgentValues(child, rewrite)
		}
	case []interface{}:
		for i, child := range typed {
			if value, ok := child.(string); ok {
				typed[i] = rewrite(value)
				continue
			}
			walkAgentValues(child, rewrite)
		}
	}
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// normalizeAgentMessageContent gives a bare Answer node something to say.
//
// The palette's Answer operator has no editable fields (its form definition is
// empty), so a node dragged onto the canvas is saved with `params: {}` and the
// engine rejects the whole agent at compile time with
// `[Message] content does not support empty value` — from the outside, another
// opaque 500. The intent of an Answer node is unambiguous: emit the content of
// the node that feeds it, which is exactly what a hand-written DSL contains
// (`content: ["{llm@content}"]`).
//
// Only an empty template is filled in, so a user-authored Answer is never
// overwritten and a second pass changes nothing.
// mirrorAgentNodeLinks copies links that only exist inside obj up to the
// component level.
//
// The engine walks a workflow from a node's component-level `downstream`; the
// canvas (ours and RAGFlow's) writes links there too. A DSL that keeps them only
// inside obj parses fine, runs zero nodes and answers with an empty string —
// indistinguishable from a dead model. Mirroring is a per-direction fallback, so
// a DSL that declares its links properly is never rewritten.
func mirrorAgentNodeLinks(dsl map[string]interface{}) map[string]interface{} {
	if dsl == nil {
		return nil
	}
	out, ok := cloneJSONValue(dsl).(map[string]interface{})
	if !ok {
		return dsl
	}
	components, ok := out["components"].(map[string]interface{})
	if !ok {
		return out
	}
	for _, id := range sortedKeys(components) {
		component, ok := components[id].(map[string]interface{})
		if !ok {
			continue
		}
		obj, ok := component["obj"].(map[string]interface{})
		if !ok {
			continue
		}
		for _, field := range agentNodeLinkFields {
			if len(agentStringList(component[field])) > 0 {
				continue
			}
			if inner := agentStringList(obj[field]); len(inner) > 0 {
				component[field] = inner
			}
		}
	}
	return out
}

// agentNodeLinkFields are the per-node link fields the engine walks. `upstream`
// is legitimate to look at (nodes may only know their input); `downstream` alone
// would let a node declare outputs nothing consumes.
var agentNodeLinkFields = []string{"upstream", "downstream"}

// agentStringList flattens a link field: []interface{} of strings, or a lone
// string, or nil.
func agentStringList(value interface{}) []interface{} {
	switch typed := value.(type) {
	case []interface{}:
		out := make([]interface{}, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				out = append(out, text)
			}
		}
		return out
	case []string:
		out := make([]interface{}, 0, len(typed))
		for _, item := range typed {
			if strings.TrimSpace(item) != "" {
				out = append(out, item)
			}
		}
		return out
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []interface{}{typed}
	}
	return nil
}

func normalizeAgentMessageContent(dsl map[string]interface{}) map[string]interface{} {
	if dsl == nil {
		return nil
	}
	out, ok := cloneJSONValue(dsl).(map[string]interface{})
	if !ok {
		return dsl
	}
	components, ok := out["components"].(map[string]interface{})
	if !ok {
		return out
	}
	for _, id := range sortedKeys(components) {
		component, ok := components[id].(map[string]interface{})
		if !ok {
			continue
		}
		obj, ok := component["obj"].(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := obj["component_name"].(string)
		if !strings.EqualFold(strings.TrimSpace(name), "message") {
			continue
		}
		params, ok := obj["params"].(map[string]interface{})
		if !ok {
			if params == nil {
				params = map[string]interface{}{}
			} else {
				continue
			}
		}
		if agentMessageTemplateUsable(params) {
			obj["params"] = params
			continue
		}
		if source := agentMessageSource(components, component); source != "" {
			params["content"] = []interface{}{"{" + source + "@content}"}
			obj["params"] = params
		}
	}
	return out
}

// agentMessageTemplateUsable reports whether the params already carry text the
// engine can emit: a non-blank `text`, or `content` holding at least one
// non-blank string.
func agentMessageTemplateUsable(params map[string]interface{}) bool {
	if text, ok := params["text"].(string); ok && strings.TrimSpace(text) != "" {
		return true
	}
	switch content := params["content"].(type) {
	case string:
		return strings.TrimSpace(content) != ""
	case []interface{}:
		for _, item := range content {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				return true
			}
		}
	case []string:
		for _, item := range content {
			if strings.TrimSpace(item) != "" {
				return true
			}
		}
	}
	return false
}

// agentMessageSource picks the upstream node whose content the Answer should
// emit: an LLM first (the usual shape), then any node that is not the Begin, and
// only as a last resort the Begin itself — an answer built from the Begin node's
// content would be empty, so an unusable graph is left alone and fails loudly.
func agentMessageSource(
	components map[string]interface{},
	component map[string]interface{},
) string {
	// The engine reads a node's links off `obj`; the canvas writes them there, so
	// looking only at the component level made the auto-forward a no-op for every
	// DSL the editor produces.
	candidates := make([]string, 0, 4)
	for _, source := range []interface{}{objUpstream(component), component["upstream"]} {
		upstream, _ := source.([]interface{})
		for _, raw := range upstream {
			if id, ok := raw.(string); ok && strings.TrimSpace(id) != "" {
				candidates = append(candidates, id)
			}
		}
		if len(candidates) > 0 {
			break
		}
	}
	for _, id := range candidates {
		if agentComponentNameOf(components, id) == "llm" {
			return id
		}
	}
	for _, id := range candidates {
		if !strings.EqualFold(agentComponentNameOf(components, id), "begin") {
			return id
		}
	}
	return ""
}

// objUpstream returns the links the engine actually walks, if the node keeps
// them inside obj.
func objUpstream(component map[string]interface{}) interface{} {
	if obj, ok := component["obj"].(map[string]interface{}); ok {
		return obj["upstream"]
	}
	return nil
}

func agentComponentNameOf(components map[string]interface{}, id string) string {
	component, ok := components[id].(map[string]interface{})
	if !ok {
		return ""
	}
	obj, ok := component["obj"].(map[string]interface{})
	if !ok {
		return ""
	}
	name, _ := obj["component_name"].(string)
	return strings.ToLower(strings.TrimSpace(name))
}
