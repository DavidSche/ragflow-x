package service

import (
	"encoding/json"
	"testing"
)

func citRef(docID string, sim float64) map[string]interface{} {
	m := map[string]interface{}{"document_id": docID, "chunk_id": docID + "-c1", "similarity": sim}
	return m
}

func TestEvaluateAnswerabilityBranches(t *testing.T) {
	const threshold = 0.30
	tests := []struct {
		name       string
		answer     string
		citations  []map[string]interface{}
		threshold  float64
		override   float64
		wantAnswer bool
		wantReason string
	}{
		{
			name:       "empty answer -> no_answer",
			answer:     "",
			citations:  []map[string]interface{}{citRef("doc-a", 0.7)},
			threshold:  threshold,
			wantAnswer: false,
			wantReason: AbstainReasonNoAnswer,
		},
		{
			name:       "citations present + refused -> self_abstain",
			answer:     "知识库暂未覆盖该主题，无法给出确认结论。",
			citations:  []map[string]interface{}{citRef("doc-a", 0.45)},
			threshold:  threshold,
			wantAnswer: false,
			wantReason: AbstainReasonSelfAbstain,
		},
		{
			name:       "citations present + confident but weak sim -> low_similarity",
			answer:     "依据相关文件，该限额为 500 万元。",
			citations:  []map[string]interface{}{citRef("doc-a", 0.12), citRef("doc-b", 0.08)},
			threshold:  threshold,
			wantAnswer: true,
			wantReason: AbstainReasonLowSimilarity,
		},
		{
			name:       "citations present + strong sim -> none",
			answer:     "依据第 12 条，该流程需经审批。",
			citations:  []map[string]interface{}{citRef("doc-a", 0.62)},
			threshold:  threshold,
			wantAnswer: true,
			wantReason: AbstainReasonNone,
		},
		{
			name:       "no citations + refused -> self_abstain (still honest)",
			answer:     "未找到相关内容，无法回答。",
			citations:  nil,
			threshold:  threshold,
			wantAnswer: false,
			wantReason: AbstainReasonSelfAbstain,
		},
		{
			name:       "no citations + states conclusion -> no_evidence deviation",
			answer:     "该企业安全生产目标为全年零事故。",
			citations:  nil,
			threshold:  threshold,
			wantAnswer: false,
			wantReason: AbstainReasonNoEvidence,
		},
		{
			name:       "threshold zero disables low_similarity gate",
			answer:     "依据相关文件，该限额为 500 万元。",
			citations:  []map[string]interface{}{citRef("doc-a", 0.12)},
			threshold:  0,
			wantAnswer: true,
			wantReason: AbstainReasonNone,
		},
		{
			name:       "topSim override lifts confidence",
			answer:     "依据相关文件，该限额为 500 万元。",
			citations:  []map[string]interface{}{citRef("doc-a", 0.12)},
			threshold:  threshold,
			override:   0.55,
			wantAnswer: true,
			wantReason: AbstainReasonNone,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claim := EvaluateAnswerability(tc.answer, tc.citations, tc.threshold, tc.override, 3)
			if claim.Answerable != tc.wantAnswer {
				t.Fatalf("Answerable=%v want %v (reason=%q)", claim.Answerable, tc.wantAnswer, claim.AbstainReason)
			}
			if claim.AbstainReason != tc.wantReason {
				t.Fatalf("AbstainReason=%q want %q", claim.AbstainReason, tc.wantReason)
			}
		})
	}
}

func TestConvergeEvidenceDedupAndCap(t *testing.T) {
	citations := []map[string]interface{}{
		citRef("doc-a", 0.5), citRef("doc-a", 0.55), citRef("doc-a", 0.6),
		citRef("doc-b", 0.7), citRef("doc-b", 0.61),
		citRef("doc-c", 0.4),
	}
	metrics := convergeEvidence(citations)
	if metrics.total != 6 {
		t.Fatalf("total=%d want 6", metrics.total)
	}
	if metrics.distinct != 3 {
		t.Fatalf("distinct=%d want 3", metrics.distinct)
	}
	if metrics.topSim != 0.7 {
		t.Fatalf("topSim=%v want 0.7", metrics.topSim)
	}
	if len(metrics.documentIDs) != 3 {
		t.Fatalf("documentIDs=%v want 3 entries", metrics.documentIDs)
	}
	if metrics.source == "" || !containsDoc(metrics.source, "doc-b") {
		t.Fatalf("source=%q should include doc-b", metrics.source)
	}
}

func TestConvergeEvidenceAliases(t *testing.T) {
	citations := []map[string]interface{}{
		{"doc_id": "d1", "score": 0.4, "title": "t1"},
		{"name": "d2", "similarity": 0.8},
		{"name": "d2", "similarity": 0.8},
	}
	metrics := convergeEvidence(citations)
	if metrics.distinct != 2 || metrics.total != 3 {
		t.Fatalf("distinct=%d total=%d want 2/3", metrics.distinct, metrics.total)
	}
	if metrics.topSim != 0.8 {
		t.Fatalf("topSim=%v want 0.8", metrics.topSim)
	}
}

func TestAnswerabilityClaimJSONRoundTrip(t *testing.T) {
	claim := EvaluateAnswerability("知识库暂未覆盖该数值。", []map[string]interface{}{citRef("doc-a", 0.5)}, 0.3, 0, 3)
	raw, err := json.Marshal(claim)
	if err != nil {
		t.Fatal(err)
	}
	var decoded AnswerabilityClaim
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Answerable != false || decoded.AbstainReason != AbstainReasonSelfAbstain {
		t.Fatalf("round trip mismatch: %+v", decoded)
	}
	if decoded.DistinctDocs != 1 || decoded.CitationCount != 1 {
		t.Fatalf("count mismatch: %+v", decoded)
	}
}

func containsDoc(source, doc string) bool {
	for i := 0; i+len(doc) <= len(source); i++ {
		found := true
		for j := range doc {
			if source[i+j] != doc[j] {
				found = false
				break
			}
		}
		if found {
			return true
		}
	}
	return false
}
