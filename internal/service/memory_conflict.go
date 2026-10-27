package service

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode"

	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// Deterministic Memory/Knowledge conflict-downgrade narrow rule (doc 137 §6.3 #7/#8,
// §13.3 use case 11, review 141 P1-4 closure). No LLM contradiction judgment: the
// rule only consumes machine-readable Stable Fact annotations and knowledge fact
// annotations. The algorithm version is recorded as context_policy_version in the
// governed-search decision detail and in every conflict summary.
//
// Fail-closed semantics (§6.3 #8):
//   - Only items explicitly annotated as Stable Facts participate; everything else
//     stays plain "historical context / user preference" (§6.3 #7) and is never
//     downgraded (verdict not_applicable).
//   - A Stable Fact whose annotation cannot be deterministically evaluated is
//     downgraded (memory_conflict_unknown).
//   - A well-formed Stable Fact with decisive knowledge evidence on the same
//     normalized entity+attribute with a different value is downgraded
//     (memory_conflict_downgraded); a matching value counts as consistent.
const memoryConflictPolicyVersion = "rgx-memory-conflict-v1"

// Memory conflict verdicts recorded per message and in the summary.
const (
	// MemoryConflictNotApplicable: the message is not a Stable Fact; it remains
	// plain historical context and is never downgraded.
	MemoryConflictNotApplicable = "not_applicable"
	// MemoryConflictDowngraded: knowledge contradicts the Stable Fact on the same
	// normalized entity+attribute; the memory item must not be trusted.
	MemoryConflictDowngraded = "memory_conflict_downgraded"
	// MemoryConflictUnknown: the Stable Fact could not be deterministically
	// verified against knowledge evidence; fail-closed downgrade.
	MemoryConflictUnknown = "memory_conflict_unknown"
	// MemoryConflictConsistent: knowledge confirms the Stable Fact value.
	MemoryConflictConsistent = "consistent"
)

// stableFactAnnotation is the strict machine-readable Stable Fact shape. Only
// JSON objects with a "stable_fact" wrapper and three non-empty string fields
// qualify; anything else must not be treated as a Stable Fact.
type stableFactAnnotation struct {
	StableFact *struct {
		Entity    string `json:"entity"`
		Attribute string `json:"attribute"`
		Value     string `json:"value"`
	} `json:"stable_fact"`
	// KnowledgeChunkEvidence allows knowledge chunks to carry the same narrow
	// annotation under a "knowledge_fact" key so the comparison stays symmetric.
	KnowledgeFact *struct {
		Entity    string `json:"entity"`
		Attribute string `json:"attribute"`
		Value     string `json:"value"`
	} `json:"knowledge_fact"`
}

var stableFactWhitespace = regexp.MustCompile(`\s+`)

// normalizeFactToken canonicalizes entity/attribute/value tokens for the
// deterministic comparison: whitespace collapse and Unicode casefold. Full
// NFKC compatibility folding is intentionally not applied — a normalization
// gap only ever widens the fail-closed downgrade direction (unknown), never
// silently confirms a conflicting fact.
func normalizeFactToken(raw string) string {
	folded := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return unicode.ToLower(r)
	}, strings.TrimSpace(raw))
	return strings.TrimSpace(stableFactWhitespace.ReplaceAllString(folded, " "))
}

// parseMemoryStableFact extracts a Stable Fact annotation from a memory message
// body. The annotation must be a strict JSON object carrying a "stable_fact"
// wrapper. Plain JSON without the wrapper is NOT explicitly marked as a Stable
// Fact and stays plain historical context; a wrapper with missing fields is a
// malformed Stable Fact (caller downgrades as memory_conflict_unknown).
func parseMemoryStableFact(content string) (*stableFactAnnotation, bool, bool) {
	annotation, ok := parseFactAnnotation(content)
	if !ok || annotation.StableFact == nil {
		return nil, false, false
	}
	fact := *annotation.StableFact
	if normalizeFactToken(fact.Entity) == "" || normalizeFactToken(fact.Attribute) == "" ||
		normalizeFactToken(fact.Value) == "" {
		return nil, true, false
	}
	return &stableFactAnnotation{StableFact: &fact}, true, true
}

// parseFactAnnotation locates the strict annotation object inside an arbitrary
// text body: the whole content may be JSON, or a JSON object may appear after a
// narrative part. Candidate objects are extracted with a balanced-brace scan
// (string/escape aware) so nested wrappers and prose braces are handled.
func parseFactAnnotation(content string) (*stableFactAnnotation, bool) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, false
	}
	var found *stableFactAnnotation
	for _, candidate := range scanBalancedJSONObjects(trimmed) {
		var annotation stableFactAnnotation
		if err := json.Unmarshal([]byte(candidate), &annotation); err != nil {
			continue
		}
		if annotation.StableFact != nil || annotation.KnowledgeFact != nil {
			found = &annotation
		} else if found == nil {
			found = &annotation
		}
	}
	return found, found != nil
}

// scanBalancedJSONObjects returns the balanced top-level JSON object substrings
// of content in order of appearance. Depth tracking respects JSON strings and
// escapes so braces inside narrative text or values do not skew the balance.
func scanBalancedJSONObjects(content string) []string {
	var out []string
	depth, start := 0, -1
	inString, escaped := false, false
	for index := 0; index < len(content); index++ {
		char := content[index]
		if inString {
			switch {
			case escaped:
				escaped = false
			case char == '\\':
				escaped = true
			case char == '"':
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			if depth > 0 {
				inString = true
			}
		case '{':
			if depth == 0 {
				start = index
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 && start >= 0 {
					out = append(out, content[start:index+1])
					start = -1
				}
			}
		}
	}
	return out
}

// parseKnowledgeFact extracts a knowledge chunk fact annotation when present.
func parseKnowledgeFact(chunk string) (*stableFactAnnotation, bool) {
	annotation, ok := parseFactAnnotation(chunk)
	if !ok || annotation.KnowledgeFact == nil {
		return nil, false
	}
	fact := *annotation.KnowledgeFact
	if normalizeFactToken(fact.Entity) == "" || normalizeFactToken(fact.Attribute) == "" ||
		normalizeFactToken(fact.Value) == "" {
		return nil, false
	}
	return &stableFactAnnotation{KnowledgeFact: &fact}, true
}

// MemoryConflictItem is the per-message verdict recorded in the trace surface.
type MemoryConflictItem struct {
	MessageID      int64  `json:"message_id"`
	Verdict        string `json:"verdict"`
	Entity         string `json:"entity,omitempty"`
	Attribute      string `json:"attribute,omitempty"`
	MemoryValue    string `json:"memory_value,omitempty"`
	KnowledgeValue string `json:"knowledge_value,omitempty"`
	Detail         string `json:"detail,omitempty"`
}

// MemoryConflictSummary is the deterministic conflict evaluation result.
type MemoryConflictSummary struct {
	ContextPolicyVersion string               `json:"context_policy_version"`
	EvaluatedCount       int                  `json:"evaluated_count"`
	NotApplicableCount   int                  `json:"not_applicable_count"`
	DowngradedCount      int                  `json:"downgraded_count"`
	UnknownCount         int                  `json:"unknown_count"`
	ConsistentCount      int                  `json:"consistent_count"`
	Items                []MemoryConflictItem `json:"items"`
}

// evaluateMemoryConflicts applies the deterministic narrow rule to the memory
// search result against the top-k knowledge chunk set of the same answer run.
// When no knowledge chunk speaks to a Stable Fact's entity+attribute the item is
// downgraded as memory_conflict_unknown (fail-closed, doc 137 §6.3 #8).
func evaluateMemoryConflicts(messages []ragflow.MemoryMessage, knowledgeChunks []string) *MemoryConflictSummary {
	knowledgeFacts := make([]stableFactAnnotation, 0, len(knowledgeChunks))
	for _, chunk := range knowledgeChunks {
		if fact, ok := parseKnowledgeFact(chunk); ok {
			knowledgeFacts = append(knowledgeFacts, *fact)
		}
	}

	summary := &MemoryConflictSummary{ContextPolicyVersion: memoryConflictPolicyVersion, Items: []MemoryConflictItem{}}
	for _, message := range messages {
		verdict := MemoryConflictNotApplicable
		item := MemoryConflictItem{MessageID: message.MessageID, Verdict: verdict}
		fact, isStableFact, wellFormed := parseMemoryStableFact(message.Content)
		if isStableFact {
			summary.EvaluatedCount++
			if !wellFormed || fact == nil || fact.StableFact == nil {
				verdict = MemoryConflictUnknown
				item.Detail = "stable_fact_annotation_not_deterministic"
			} else {
				entity := normalizeFactToken(fact.StableFact.Entity)
				attribute := normalizeFactToken(fact.StableFact.Attribute)
				memoryValue := normalizeFactToken(fact.StableFact.Value)
				item.Entity = entity
				item.Attribute = attribute
				item.MemoryValue = memoryValue
				decisive := false
				for _, knowledge := range knowledgeFacts {
					if knowledge.KnowledgeFact == nil {
						continue
					}
					if normalizeFactToken(knowledge.KnowledgeFact.Entity) != entity ||
						normalizeFactToken(knowledge.KnowledgeFact.Attribute) != attribute {
						continue
					}
					knowledgeValue := normalizeFactToken(knowledge.KnowledgeFact.Value)
					if knowledgeValue == "" {
						continue
					}
					decisive = true
					if knowledgeValue != memoryValue {
						item.KnowledgeValue = knowledgeValue
						verdict = MemoryConflictDowngraded
					} else {
						verdict = MemoryConflictConsistent
					}
					break
				}
				if !decisive {
					verdict = MemoryConflictUnknown
					item.Detail = "no_decisive_knowledge_evidence"
				}
			}
			switch verdict {
			case MemoryConflictDowngraded:
				summary.DowngradedCount++
			case MemoryConflictUnknown:
				summary.UnknownCount++
			case MemoryConflictConsistent:
				summary.ConsistentCount++
			}
			item.Verdict = verdict
			summary.Items = append(summary.Items, item)
			continue
		}
		summary.NotApplicableCount++
		summary.Items = append(summary.Items, item)
	}
	return summary
}
