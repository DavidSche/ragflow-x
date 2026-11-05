package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Answerability claim states (report §13.2 P0-B). A claim annotates each chat
// turn with how much evidence the answer rests on. It NEVER rewrites the
// engine answer - the honesty discipline is observable and auditable, not a
// second generation path.
const (
	AbstainReasonNone          = "none"           // answer rests on citations, similarity above the weak-evidence gate.
	AbstainReasonNoAnswer      = "no_answer"      // engine produced no visible answer at all.
	AbstainReasonSelfAbstain   = "self_abstain"   // engine itself refused (话术锚点命中): honest abstention.
	AbstainReasonLowSimilarity = "low_similarity" // citations exist but top similarity < no_evidence_similarity: evidence-weak flag.
	AbstainReasonNoEvidence    = "no_evidence"    // answer states a conclusion with zero citations: 无据下结论, discipline violation marker.
)

// abstainPhraseSignals are engine-side honest-refusal phrases. They are the
// observable contract of "知识库暂未覆盖 / 我无法从知识库确认" wording families.
// Matching is whole-answer containment (not word-boundary) because refusal
// sentences are long; a single anchor is enough to recognise the intent.
var abstainPhraseSignals = []string{
	"知识库暂未覆盖", "知识库中未找到", "知识库未覆盖", "知识库暂不支持", "知识库中没有",
	"未找到相关", "未找到对应", "未找到匹配", "未提供相关信息",
	"无法回答", "无法确定", "无法确认", "无法判断", "无法给出", "不能确认", "不能回答",
	"没有相关信息", "没有找到", "暂未找到", "没有相关资料", "不清楚是否",
	"cannot answer", "cannot confirm", "not found in the knowledge base", "no information available",
}

// AnswerabilityClaim is the per-turn honesty state stored into the knowledge
// ops ledger and surfaced in the conversation centre.
type AnswerabilityClaim struct {
	Answerable    bool     `json:"answerable"`
	Confidence    float64  `json:"confidence"`
	Source        string   `json:"source,omitempty"`
	DistinctDocs  int      `json:"distinct_docs"`
	CitationCount int      `json:"citation_count"`
	AbstainReason string   `json:"abstain_reason,omitempty"`
	DocumentIDs   []string `json:"document_ids,omitempty"`
}

// evidenceMetrics condenses the engine citations into what a claim needs.
type evidenceMetrics struct {
	total       int      // raw citation count (kept as-is for the audit).
	distinct    int      // unique evidence documents after same-document dedup.
	topSim      float64  // best citation similarity/score.
	source      string   // human-readable distinct document names, capped.
	documentIDs []string // stable document identifiers (deduped, capped).
}

// EvaluateAnswerability renders the honesty claim for one chat turn.
//
// Branch contract:
//   - empty answer                                     -> no_answer (nothing claimed).
//   - citations present, answer refused                -> self_abstain (honest refusal).
//   - citations present, answer not refused:
//   - topSim < threshold                          -> low_similarity (evidence-weak annotation, answer kept).
//   - else                                        -> none (answer rests on evidence).
//   - no citations,   answer refused                  -> self_abstain (still honest: echoed the refusal contract).
//   - no citations,   answer states something         -> no_evidence (无据下结论: deviation from the discipline).
//
// threshold comes from config.KnowledgeOps; passing 0 threshold disables the
// low_similarity gate the same way AnswerabilityEnabled=false does for the
// whole evaluation. topSimOverride, when > 0, replaces the claims' best
// similarity with a caller-provided value (e.g. an explicit retrieval score that
// the engine citations did not carry). maxPerDoc is retained on the public
// signature for API stability but does not currently cap the evidence set:
// same-document convergence is already achieved by dedup in convergeEvidence
// (each document contributes its best similarity exactly once).
func EvaluateAnswerability(answer string, citations []map[string]interface{}, threshold, topSimOverride float64, maxPerDoc int) AnswerabilityClaim {
	answer = strings.TrimSpace(answer)
	metrics := convergeEvidence(citations)
	confidence := metrics.topSim
	if topSimOverride > 0 {
		confidence = topSimOverride
	}
	claim := AnswerabilityClaim{
		Confidence:    confidence,
		Source:        metrics.source,
		DistinctDocs:  metrics.distinct,
		CitationCount: metrics.total,
		DocumentIDs:   metrics.documentIDs,
		AbstainReason: AbstainReasonNone,
	}
	if answer == "" {
		claim.Answerable = false
		claim.AbstainReason = AbstainReasonNoAnswer
		return claim
	}
	refused := containsAbstainSignal(answer)
	if metrics.total == 0 {
		claim.Confidence = 0
		if refused {
			claim.Answerable = false
			claim.AbstainReason = AbstainReasonSelfAbstain
			return claim
		}
		claim.Answerable = false
		claim.AbstainReason = AbstainReasonNoEvidence
		return claim
	}
	if refused {
		claim.Answerable = false
		claim.AbstainReason = AbstainReasonSelfAbstain
		return claim
	}
	claim.Answerable = true
	if threshold > 0 && claim.Confidence < threshold {
		claim.AbstainReason = AbstainReasonLowSimilarity
	}
	return claim
}

// bestReferenceSimilarity returns the highest retrieval similarity observed in
// the turn's engine references. For each reference the `similarity` key carries
// RAGFlow's score and a `score` alias is accepted for version tolerance; 0 means
// the engine returned no measurable similarity. This is the single canonical
// extraction used by both the honesty claim (convergeEvidence) and the P2
// operations observability (maxReferenceSimilarity), so the two surfaces cannot
// drift apart.
func bestReferenceSimilarity(reference []map[string]interface{}) float64 {
	best := 0.0
	for _, item := range reference {
		if item == nil {
			continue
		}
		value := floatValue(item["similarity"])
		if value == 0 {
			value = floatValue(item["score"])
		}
		if value > best {
			best = value
		}
	}
	return best
}

func containsAbstainSignal(answer string) bool {
	for _, signal := range abstainPhraseSignals {
		if strings.Contains(answer, signal) {
			return true
		}
	}
	return false
}

// convergeEvidence deduplicates citations by evidence document and derives the
// metrics a claim carries. The distinct-document set, source string and best
// similarity are converged: same-document chunks collapse to one entry (best
// similarity wins), and the human-readable source is capped to the first
// MaxSourceDocuments names (report §13.2 ② 引文按需收敛). metrics.total keeps
// the raw citation count as-is for the audit trail.
const MaxSourceDocuments = 5

func convergeEvidence(citations []map[string]interface{}) evidenceMetrics {
	var metrics evidenceMetrics
	metrics.total = len(citations)
	metrics.topSim = bestReferenceSimilarity(citations)

	index := make(map[string]float64) // document identity -> its best similarity.
	for _, citation := range citations {
		if citation == nil {
			continue
		}
		docID := documentIdentity(citation)
		if docID == "" {
			continue
		}
		if value := floatValue(citation["similarity"]); value > index[docID] {
			index[docID] = value
		}
		if value := floatValue(citation["score"]); value > index[docID] {
			index[docID] = value
		}
	}
	names := make([]string, 0, len(index))
	for docID := range index {
		names = append(names, docID)
	}
	sort.Strings(names)
	metrics.distinct = len(names)
	for _, name := range names {
		if name == "" {
			continue
		}
		if len(metrics.documentIDs) < MaxSourceDocuments {
			metrics.documentIDs = append(metrics.documentIDs, name)
		}
	}
	metrics.source = joinSource(metrics.documentIDs, metrics.distinct-len(metrics.documentIDs))
	return metrics
}

func documentIdentity(citation map[string]interface{}) string {
	for _, key := range []string{"document_id", "doc_id", "document", "name", "title"} {
		if value := stringFromAny(citation[key]); value != "" {
			return value
		}
	}
	return ""
}

func floatValue(value interface{}) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case json.Number:
		v, _ := typed.Float64()
		return v
	}
	return 0
}

func joinSource(names []string, omitted int) string {
	if len(names) == 0 {
		return ""
	}
	source := strings.Join(names, "、")
	if omitted > 0 {
		source += fmt.Sprintf(" 等%d篇", len(names)+omitted)
	}
	return source
}

// attachAnswerabilityClaim evaluates and persists the per-turn claim unless the
// deployment has answerability disabled (config.RGX_KNOWLEDGE_OPS_ANSWERABILITY).
// Observation only: a nil event or a serialisation failure never breaks the
// completion path.
func (s *Service) attachAnswerabilityClaim(event *model.KnowledgeOpsEvent, answer string, citations []map[string]interface{}) {
	if event == nil {
		return
	}
	cfg := s.currentKnowledgeOpsConfig()
	if !cfg.AnswerabilityEnabled {
		return
	}
	claim := EvaluateAnswerability(answer, citations, cfg.NoEvidenceSimilarity, 0, cfg.MaxCitationsPerDocument)
	raw, err := json.Marshal(claim)
	if err != nil {
		return
	}
	event.AnswerabilityJSON = string(raw)
}
