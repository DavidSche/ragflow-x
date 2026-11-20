package model

import "time"

// Chat citation audit (doc/301 §5.10 QA-2 / T19).
//
// QA-2 is the *sampling* arm of the citation discipline: every week, per tenant,
// a bounded number of chat turns that carried citations are re-checked against the
// evidence they claim to rest on, and the turns whose citations do not support the
// answer become knowledge-ops badcase candidates.
//
// Two boundaries are deliberately drawn here:
//
//   - The audit writes **no `rgx_report_*` row**. Reports and chat turns are two
//     populations; borrowing the report tables would make one table answer two
//     questions and would corrupt the report assurance metrics' denominator
//     (doc/301 T19 DoD).
//   - The audit is **idempotent per (tenant, ISO week)**. A row in
//     `rgx_chat_citation_audit_run` *is* the idempotency guard, so a replayed
//     weekly job cannot sample the same week twice and cannot double-count.

// ChatCitationAuditRunStatuses is the closed set of `rgx_chat_citation_audit_run.
// status`. `budget_exhausted` is a *completed* run in the accounting sense: every
// sampled turn was judged, but the semantic arm stopped early because the token
// cap was reached. It is a separate value rather than a flag because a reader
// looking at the run list must be able to tell "we could not afford to finish"
// from "we finished" without opening the counters.
func ChatCitationAuditRunStatuses() []string {
	return []string{
		ChatCitationAuditRunRunning,
		ChatCitationAuditRunCompleted,
		ChatCitationAuditRunBudgetExhausted,
		ChatCitationAuditRunFailed,
	}
}

const (
	ChatCitationAuditRunRunning         = "running"
	ChatCitationAuditRunCompleted       = "completed"
	ChatCitationAuditRunBudgetExhausted = "budget_exhausted"
	ChatCitationAuditRunFailed          = "failed"
)

// ChatCitationAuditTurnStatuses is the closed set of the per-turn disposition
// written back onto `rgx_knowledge_ops_event.citation_audit_status`.
//
//   - `passed`  — every citation of the turn is corroborated by the answer;
//   - `failed`  — at least one citation is unsupported (a badcase candidate);
//   - `skipped` — the turn could not be judged, with `citation_audit_reason`
//     saying why. A skip is never a pass: "we did not look" and "we looked and it
//     was fine" must not share a value, or the pass rate becomes a lie.
const (
	ChatCitationAuditPassed  = "passed"
	ChatCitationAuditFailed  = "failed"
	ChatCitationAuditSkipped = "skipped"
)

// ChatCitationAuditTurnStatuses returns the closed set above, in display order.
func ChatCitationAuditTurnStatuses() []string {
	return []string{ChatCitationAuditPassed, ChatCitationAuditFailed, ChatCitationAuditSkipped}
}

// ChatCitationFindingVerdicts is the closed set of one citation's verdict.
// `unverifiable` is a real answer (the mechanical layer could not decide and the
// semantic arm was off or unaffordable); it never counts as support.
const (
	ChatCitationVerdictSupported    = "supported"
	ChatCitationVerdictUnsupported  = "unsupported"
	ChatCitationVerdictUnverifiable = "unverifiable"
)

// ChatCitationFindingVerdicts returns the closed set above.
func ChatCitationFindingVerdicts() []string {
	return []string{
		ChatCitationVerdictSupported,
		ChatCitationVerdictUnsupported,
		ChatCitationVerdictUnverifiable,
	}
}

// Chat citation finding methods. `deterministic` is the always-on mechanical
// layer (no model, no cost); `semantic` is the opt-in model arm whose spend is
// capped by `knowledge_audit.max_tokens`.
const (
	ChatCitationMethodDeterministic = "deterministic"
	ChatCitationMethodSemantic      = "semantic"
)

// Chat citation audit reason codes. They are a closed set because the queue
// filters and the board aggregates on them: a free-text reason would make
// "why was this turn skipped" unanswerable in aggregate.
const (
	ChatCitationReasonAnswerContainsQuote  = "answer_contains_quote"
	ChatCitationReasonAnswerOverlaps       = "answer_overlaps_excerpt"
	ChatCitationReasonSemanticSupported    = "semantic_supported"
	ChatCitationReasonSemanticUnsupported  = "semantic_unsupported"
	ChatCitationReasonSemanticInconclusive = "semantic_inconclusive"
	ChatCitationReasonExcerptAbsent        = "excerpt_absent_from_answer"
	ChatCitationReasonExcerptHashMismatch  = "excerpt_hash_mismatch"
	ChatCitationReasonWeakOverlap          = "weak_overlap_unjudged"
	ChatCitationReasonBudgetExhausted      = "budget_exhausted"
	ChatCitationReasonNoAnswerText         = "no_answer_text"
	ChatCitationReasonNoCitations          = "no_citations"
	ChatCitationReasonEvidenceMissing      = "evidence_missing"
	ChatCitationReasonExcerptEmpty         = "excerpt_empty"
	ChatCitationReasonSampleEmpty          = "sample_empty"
)

// ChatCitationAuditReasons returns the closed set of reason codes.
func ChatCitationAuditReasons() []string {
	return []string{
		ChatCitationReasonAnswerContainsQuote, ChatCitationReasonAnswerOverlaps,
		ChatCitationReasonSemanticSupported, ChatCitationReasonSemanticUnsupported,
		ChatCitationReasonSemanticInconclusive, ChatCitationReasonExcerptAbsent,
		ChatCitationReasonExcerptHashMismatch, ChatCitationReasonWeakOverlap,
		ChatCitationReasonBudgetExhausted, ChatCitationReasonNoAnswerText,
		ChatCitationReasonNoCitations, ChatCitationReasonEvidenceMissing,
		ChatCitationReasonExcerptEmpty, ChatCitationReasonSampleEmpty,
	}
}

// IsChatCitationAuditReason reports whether value belongs to the closed set.
func IsChatCitationAuditReason(value string) bool {
	for _, reason := range ChatCitationAuditReasons() {
		if reason == value {
			return true
		}
	}
	return false
}

// IsChatCitationAuditTurnStatus reports whether value belongs to the closed set.
func IsChatCitationAuditTurnStatus(value string) bool {
	for _, status := range ChatCitationAuditTurnStatuses() {
		if status == value {
			return true
		}
	}
	return false
}

// IsChatCitationFindingVerdict reports whether value belongs to the closed set.
func IsChatCitationFindingVerdict(value string) bool {
	for _, verdict := range ChatCitationFindingVerdicts() {
		if verdict == value {
			return true
		}
	}
	return false
}

// ChatCitationAuditRun is one weekly sample of one tenant. The counters are raw
// counts, never rates: every rate is derived by the caller, so "no denominator"
// stays representable instead of collapsing to 0%.
type ChatCitationAuditRun struct {
	ID          string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID    string    `gorm:"column:tenant_id;size:32;not null;uniqueIndex:uidx_chat_citation_audit_run_period,priority:1;index:idx_chat_citation_audit_run_scope,priority:1" json:"tenant_id"`
	PeriodStart string    `gorm:"column:period_start;size:10;not null;uniqueIndex:uidx_chat_citation_audit_run_period,priority:2;index:idx_chat_citation_audit_run_scope,priority:2" json:"period_start"`
	WindowFrom  time.Time `gorm:"column:window_from;not null" json:"window_from"`
	WindowTo    time.Time `gorm:"column:window_to;not null" json:"window_to"`
	// MethodVersion names the judgement rules the run used. A later rule change
	// must not silently reinterpret a past week's counters.
	MethodVersion string `gorm:"column:method_version;size:16;not null" json:"method_version"`
	SampleTarget  int    `gorm:"column:sample_target;not null;default:0" json:"sample_target"`
	Sampled       int    `gorm:"column:sampled;not null;default:0" json:"sampled"`
	Judged        int    `gorm:"column:judged;not null;default:0" json:"judged"`
	Passed        int    `gorm:"column:passed;not null;default:0" json:"passed"`
	Failed        int    `gorm:"column:failed;not null;default:0" json:"failed"`
	Skipped       int    `gorm:"column:skipped;not null;default:0" json:"skipped"`
	Findings      int    `gorm:"column:findings;not null;default:0" json:"findings"`
	TokensIn      int64  `gorm:"column:tokens_in;not null;default:0" json:"tokens_in"`
	TokensOut     int64  `gorm:"column:tokens_out;not null;default:0" json:"tokens_out"`
	// BudgetTokens is the *frozen* cap the run was started with (0 = mechanical
	// layer only). A later settings change must not rewrite what this run could
	// afford, or the `budget_exhausted` status would become unexplainable.
	BudgetTokens   int64      `gorm:"column:budget_tokens;not null;default:0" json:"budget_tokens"`
	Semantic       bool       `gorm:"column:semantic;not null;default:false" json:"semantic"`
	Status         string     `gorm:"column:status;size:16;not null;index" json:"status"`
	FailureReason  string     `gorm:"column:failure_reason;size:512" json:"failure_reason,omitempty"`
	IdempotencyKey string     `gorm:"column:idempotency_key;size:128;not null;uniqueIndex" json:"idempotency_key"`
	TriggeredBy    string     `gorm:"column:triggered_by;size:32;index" json:"triggered_by,omitempty"`
	StartedAt      time.Time  `gorm:"column:started_at;not null" json:"started_at"`
	FinishedAt     *time.Time `gorm:"column:finished_at" json:"finished_at,omitempty"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null;index" json:"created_at"`
}

// TableName is the physical table name.
func (ChatCitationAuditRun) TableName() string { return "rgx_chat_citation_audit_run" }

// ChatCitationAuditFinding is one citation's verdict inside one run.
//
// The excerpt itself is *not* copied here: the snapshot is the evidence, and a
// second copy would be a second thing to keep in sync. What is copied is the hash
// of the excerpt *as the audit saw it*, so the finding can prove later that it
// judged exactly the text it recorded judging.
type ChatCitationAuditFinding struct {
	ID            string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID      string    `gorm:"column:tenant_id;size:32;not null;index:idx_chat_citation_finding_run,priority:1" json:"tenant_id"`
	RunID         string    `gorm:"column:run_id;size:32;not null;index:idx_chat_citation_finding_run,priority:2" json:"run_id"`
	EventID       string    `gorm:"column:event_id;size:32;not null;index:idx_chat_citation_finding_event" json:"event_id"`
	RequestID     string    `gorm:"column:request_id;size:64;not null" json:"request_id"`
	CitationID    string    `gorm:"column:citation_id;size:64" json:"citation_id,omitempty"`
	DocumentID    string    `gorm:"column:document_id;size:64" json:"document_id,omitempty"`
	ChunkID       string    `gorm:"column:chunk_id;size:64" json:"chunk_id,omitempty"`
	Verdict       string    `gorm:"column:verdict;size:24;not null;index" json:"verdict"`
	Method        string    `gorm:"column:method;size:16;not null" json:"method"`
	Score         float64   `gorm:"column:score;not null;default:0" json:"score"`
	Reason        string    `gorm:"column:reason;size:48;not null;index" json:"reason"`
	ExcerptHash   string    `gorm:"column:excerpt_hash;size:64" json:"excerpt_hash,omitempty"`
	ExcerptHashOK bool      `gorm:"column:excerpt_hash_ok;not null;default:false" json:"excerpt_hash_ok"`
	CreatedAt     time.Time `gorm:"column:created_at;not null;index" json:"created_at"`
}

// TableName is the physical table name.
func (ChatCitationAuditFinding) TableName() string { return "rgx_chat_citation_audit_finding" }

// ChatCitationAuditTurn is one sampled turn plus the evidence the audit judges.
// It is a join projection, not a table: the ledger row stays the queue record and
// the answer snapshot stays the evidence.
type ChatCitationAuditTurn struct {
	EventID       string
	RequestID     string
	AppType       string
	Question      string
	Status        string
	Answer        string
	CitationsJSON string
	CreatedAt     time.Time
}
