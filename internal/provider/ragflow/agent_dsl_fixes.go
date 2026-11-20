package ragflow

import (
	"fmt"
	"sort"
	"strings"
)

// AgentDslFix describes one correction the engine boundary made on the way in.
//
// The boundary rewrites silently (a customer file must run without being edited
// by hand), but "silent" is not the same as "invisible": the authoring UI shows
// these so an operator learns why their canvas suddenly says `llm` where they
// typed `Generate`, and so a future export/re-import does not surprise them.
type AgentDslFix struct {
	// Kind is component_name | node_id | node_link | prompt_param | message_content.
	Kind string `json:"kind"`
	// NodeID is the node the fix applies to (post-rename id for node_id fixes).
	NodeID string `json:"node_id,omitempty"`
	// Label is the canvas label of that node, when one was available.
	Label string `json:"label,omitempty"`
	// From/To are the before/after spellings for the fixed value.
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Message is a ready-to-display sentence (English; the UI localises by Kind).
	Message string `json:"message"`
}

// DescribeAgentDslFixes compares the DSL a caller sent with the DSL the engine
// boundary produced and reports what changed. Both arguments are read-only.
func DescribeAgentDslFixes(before, after map[string]interface{}) []AgentDslFix {
	if before == nil || after == nil {
		return nil
	}
	labels := agentNodeLabels(before)
	beforeComponents, _ := before["components"].(map[string]interface{})
	afterComponents, _ := after["components"].(map[string]interface{})
	// The boundary re-keys components whose id had illegal characters, so every
	// lookup below has to follow the id map instead of assuming the keys match.
	idMap := agentNodeIDMap(before)
	afterID := func(id string) string {
		if mapped, ok := idMap[id]; ok {
			return mapped
		}
		return id
	}

	var fixes []AgentDslFix

	// 1. component_name: canvas label -> engine component.
	ids := sortedKeys(beforeComponents)
	for _, id := range ids {
		src, ok := beforeComponents[id].(map[string]interface{})
		if !ok {
			continue
		}
		srcObj, _ := src["obj"].(map[string]interface{})
		was := agentObjComponentName(srcObj)
		if was == "" {
			was = labels[id]
		}
		dst, ok := afterComponents[afterID(id)].(map[string]interface{})
		if !ok {
			// The component was re-keyed; its new id is reported by the id fix.
			continue
		}
		dstObj, _ := dst["obj"].(map[string]interface{})
		now := agentObjComponentName(dstObj)
		if was == "" || now == "" || strings.EqualFold(was, now) {
			continue
		}
		fixes = append(fixes, AgentDslFix{
			Kind: "component_name", NodeID: id, Label: labels[id], From: was, To: now,
			Message: fmt.Sprintf("node %q used the canvas label %q as its component; it must run as %q", nodeDisplay(labels[id], id), was, now),
		})
	}

	// 2. node ids: characters outside [A-Za-z0-9_:] folded to "_".
	beforeIDs := sortedKeys(beforeComponents)
	if idMap := agentNodeIDMap(before); len(idMap) > 0 {
		for _, oldID := range beforeIDs {
			newID, ok := idMap[oldID]
			if !ok || newID == oldID {
				continue
			}
			fixes = append(fixes, AgentDslFix{
				Kind: "node_id", NodeID: newID, Label: labels[oldID], From: oldID, To: newID,
				Message: fmt.Sprintf("node id %q contains characters the engine cannot reference; renamed to %q and every reference was rewritten", oldID, newID),
			})
		}
	}

	// 3. links that lived only inside obj: mirrored up so the engine walks them.
	for _, id := range sortedKeys(afterComponents) {
		src, ok := beforeComponents[beforeIDOf(idMap, id)].(map[string]interface{})
		if !ok {
			continue
		}
		dst, ok := afterComponents[id].(map[string]interface{})
		if !ok {
			continue
		}
		srcObj, _ := src["obj"].(map[string]interface{})
		if srcObj == nil {
			continue
		}
		for _, field := range agentNodeLinkFields {
			if len(agentStringList(src[field])) > 0 {
				continue
			}
			mirrored := agentStringList(dst[field])
			if len(mirrored) == 0 {
				continue
			}
			fixes = append(fixes, AgentDslFix{
				Kind: "node_link", NodeID: id, Label: labels[id], From: "obj." + field, To: field,
				Message: fmt.Sprintf("node %q declared its %s inside obj; the engine walks the component-level %s, so it was mirrored up", nodeDisplay(labels[id], id), field, field),
			})
		}
	}

	// 4. prompt param names: system/prompt -> system_prompt/user_prompt.
	for _, id := range sortedKeys(afterComponents) {
		src, ok := beforeComponents[beforeIDOf(idMap, id)].(map[string]interface{})
		if !ok {
			continue
		}
		srcObj, _ := src["obj"].(map[string]interface{})
		dst, ok := afterComponents[id].(map[string]interface{})
		if !ok {
			continue
		}
		dstObj, _ := dst["obj"].(map[string]interface{})
		srcParams, _ := srcObj["params"].(map[string]interface{})
		dstParams, _ := dstObj["params"].(map[string]interface{})
		if srcParams == nil || dstParams == nil {
			continue
		}
		for from, to := range agentPromptParamNames {
			if _, had := srcParams[from]; !had {
				continue
			}
			if _, has := dstParams[to]; !has {
				continue
			}
			fixes = append(fixes, AgentDslFix{
				Kind: "prompt_param", NodeID: id, Label: labels[id], From: from, To: to,
				Message: fmt.Sprintf("node %q stored its prompt as %q; the engine reads %q", nodeDisplay(labels[id], id), from, to),
			})
		}
	}

	// 5. empty Answer/Message nodes: content filled from the upstream node.
	for _, id := range sortedKeys(afterComponents) {
		dst, ok := afterComponents[id].(map[string]interface{})
		if !ok {
			continue
		}
		dstObj, _ := dst["obj"].(map[string]interface{})
		if agentObjComponentName(dstObj) != "message" {
			continue
		}
		dstParams, _ := dstObj["params"].(map[string]interface{})
		if !agentMessageTemplateUsable(dstParams) {
			continue
		}
		var srcParams map[string]interface{}
		if src, ok := beforeComponents[beforeIDOf(idMap, id)].(map[string]interface{}); ok {
			if srcObj, ok := src["obj"].(map[string]interface{}); ok {
				srcParams, _ = srcObj["params"].(map[string]interface{})
			}
		}
		if srcParams != nil && agentMessageTemplateUsable(srcParams) {
			continue
		}
		fixes = append(fixes, AgentDslFix{
			Kind: "message_content", NodeID: id, Label: labels[id],
			To:      firstAgentMessageTemplate(dstParams["content"]),
			Message: fmt.Sprintf("node %q had no template; its answer now forwards the upstream node's content", nodeDisplay(labels[id], id)),
		})
	}

	sort.SliceStable(fixes, func(i, j int) bool {
		if fixes[i].Kind != fixes[j].Kind {
			return fixes[i].Kind < fixes[j].Kind
		}
		return fixes[i].NodeID < fixes[j].NodeID
	})
	return fixes
}

// beforeIDOf reverses an id map: the pre-correction id for a post-correction one.
func beforeIDOf(idMap map[string]string, newID string) string {
	for oldID, mapped := range idMap {
		if mapped == newID {
			return oldID
		}
	}
	return newID
}

// agentObjComponentName reads component_name straight off a component's obj
// block (lowercased, the way the engine's registry compares it).
func agentObjComponentName(obj map[string]interface{}) string {
	name, _ := obj["component_name"].(string)
	return strings.ToLower(strings.TrimSpace(name))
}

func nodeDisplay(label, id string) string {
	if strings.TrimSpace(label) != "" {
		return label
	}
	return id
}

func firstAgentMessageTemplate(value interface{}) string {
	if list, ok := value.([]interface{}); ok {
		for _, item := range list {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
