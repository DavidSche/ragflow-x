package service

import (
	"encoding/json"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

// TestGatewayGovernanceForProjectsCitationsByIndex pins doc/301 §5.10 GW-1: the
// governance citation list is a pointer into `reference`, not a second copy of the
// chunk text. A copy could disagree with the body it annotates.
func TestGatewayGovernanceForProjectsCitationsByIndex(t *testing.T) {
	s := &Service{}
	s.SetKnowledgeOpsConfig(config.KnowledgeOps{AnswerabilityEnabled: true, NoEvidenceSimilarity: 0.3})

	reference := []map[string]interface{}{
		{"document_id": "doc-1", "document_name": "政策手册.md", "similarity": 0.82, "content_with_weight": "很长的正文"},
		{"document_id": "doc-2", "name": "FAQ.md", "score": 0.61},
		{"chunk_id": "c3"},
	}
	governance := s.GatewayGovernanceFor("据政策手册，年假为 15 天。[ID:1]", reference)
	if governance == nil {
		t.Fatal("governance must be present for a knowledge answer")
	}
	if governance.Schema != GovernanceSchema {
		t.Fatalf("schema = %q", governance.Schema)
	}
	if len(governance.Citations) != 3 {
		t.Fatalf("citations = %+v, want 3", governance.Citations)
	}
	if governance.Citations[0].Index != 0 || governance.Citations[0].DocumentID != "doc-1" || governance.Citations[0].DocumentName != "政策手册.md" {
		t.Fatalf("citation 0 = %+v", governance.Citations[0])
	}
	if governance.Citations[0].Similarity != 0.82 {
		t.Fatalf("citation 0 similarity = %v", governance.Citations[0].Similarity)
	}
	// The second entry has no `document_name`; `name` is the fallback.
	if governance.Citations[1].DocumentName != "FAQ.md" || governance.Citations[1].Similarity != 0.61 {
		t.Fatalf("citation 1 = %+v", governance.Citations[1])
	}
	// A citation with no identity keys keeps its index: "cited but unnamed" is a fact.
	if governance.Citations[2].Index != 2 || governance.Citations[2].DocumentID != "c3" {
		t.Fatalf("citation 2 = %+v", governance.Citations[2])
	}
	for _, citation := range governance.Citations {
		if citation.Index < 0 || citation.Index >= len(reference) {
			t.Fatalf("citation index %d does not resolve into reference", citation.Index)
		}
	}
	if governance.Answerability == nil {
		t.Fatal("answerability must be evaluated when the feature is on")
	}
	if governance.Degraded {
		t.Fatal("a complete governance block must not be marked degraded")
	}
}

// TestGatewayGovernanceForOmitsUnknownAnswerability is the anti-fabrication test: a
// deployment with answerability disabled must say the block is incomplete rather than
// emit `answerable: true` (which would be a claim nobody computed).
func TestGatewayGovernanceForOmitsUnknownAnswerability(t *testing.T) {
	s := &Service{}
	s.SetKnowledgeOpsConfig(config.KnowledgeOps{AnswerabilityEnabled: false})

	governance := s.GatewayGovernanceFor("一个没有引用也没有依据的结论", nil)
	if governance == nil {
		t.Fatal("degraded governance must still be reported")
	}
	if governance.Answerability != nil {
		t.Fatalf("answerability must be omitted when the feature is off, got %+v", governance.Answerability)
	}
	if !governance.Degraded || governance.DegradedReason != "answerability_disabled" {
		t.Fatalf("degraded = %v / %q", governance.Degraded, governance.DegradedReason)
	}
	if !governance.DegradedForHeader() {
		t.Fatal("X-RGX-Degraded must be emitted for a degraded block")
	}
	// And the wire form must not contain the omitted key at all.
	raw, err := json.Marshal(governance)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := decoded["answerability"]; present {
		t.Fatalf("answerability leaked into the wire form: %s", raw)
	}
	if _, present := decoded["citations"]; present {
		t.Fatalf("empty citations leaked into the wire form: %s", raw)
	}
}

// TestGatewayGovernanceForEmptyAnswer: no answer and no citations with the feature on
// still yields a claim (no_answer), so the block is not silently dropped.
func TestGatewayGovernanceForEmptyAnswer(t *testing.T) {
	s := &Service{}
	s.SetKnowledgeOpsConfig(config.KnowledgeOps{AnswerabilityEnabled: true, NoEvidenceSimilarity: 0.3})

	governance := s.GatewayGovernanceFor("   ", nil)
	if governance == nil || governance.Answerability == nil {
		t.Fatal("an empty answer still has an honest state (no_answer)")
	}
	if governance.Answerability.AbstainReason != AbstainReasonNoAnswer {
		t.Fatalf("abstain reason = %q, want %q", governance.Answerability.AbstainReason, AbstainReasonNoAnswer)
	}
	if governance.Degraded {
		t.Fatal("an evaluated claim is not a degraded block")
	}
}

// TestGatewayGovernanceCitationsCap pins the cap and, more importantly, that the cap
// never renumbers the pointers: index is the position in the response's `reference`.
func TestGatewayGovernanceCitationsCap(t *testing.T) {
	reference := make([]map[string]interface{}, gatewayGovernanceCitationCap+5)
	for index := range reference {
		reference[index] = map[string]interface{}{"document_id": "doc", "similarity": 0.5}
	}
	citations := gatewayGovernanceCitations(reference)
	if len(citations) != gatewayGovernanceCitationCap {
		t.Fatalf("citations = %d, want %d", len(citations), gatewayGovernanceCitationCap)
	}
	for index, citation := range citations {
		if citation.Index != index {
			t.Fatalf("citation[%d].Index = %d", index, citation.Index)
		}
	}
	if gatewayGovernanceCitations(nil) != nil {
		t.Fatal("no references must project to nil, not an empty slice")
	}
}

// TestGatewayDataShapeKeepsAnswerAndReference makes the compatibility promise explicit:
// the gateway wrappers keep the console field names, and `governance` is the only new
// key. A future field rename here would break every existing client.
func TestGatewayDataShapeKeepsAnswerAndReference(t *testing.T) {
	data := GatewaySearchAppData{Answer: "a", Reference: []map[string]interface{}{{"document_id": "d"}},
		Governance: &GatewayGovernance{Schema: GovernanceSchema}}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"answer", "reference", "governance"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("search-app gateway data lost key %q: %s", key, raw)
		}
	}
	if len(decoded) != 3 {
		t.Fatalf("search-app gateway data grew unexpected keys: %s", raw)
	}

	agent := GatewayAgentData{Governance: &GatewayGovernance{Schema: GovernanceSchema}}
	agent.Choices = nil
	raw, err = json.Marshal(agent)
	if err != nil {
		t.Fatalf("marshal agent: %v", err)
	}
	decoded = map[string]any{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal agent: %v", err)
	}
	// `choices` (not `data.choices`) is the OpenAI-compatible position the embedded
	// struct must keep.
	if _, ok := decoded["choices"]; !ok {
		t.Fatalf("agent gateway data lost top-level choices: %s", raw)
	}
	if _, ok := decoded["governance"]; !ok {
		t.Fatalf("agent gateway data lost governance: %s", raw)
	}
}
