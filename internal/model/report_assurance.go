package model

import "time"

// Report assurance (doc/301 §6). The assurance surface answers a different
// question from the report surface: not "what does this revision say" but "is
// each claim in it supported by the evidence it cites". It therefore gets its own
// append-only tables instead of new columns on `rgx_report_claim`: a verdict is a
// *measurement with a method*, several runs of the same revision must coexist, and
// a published revision may never be rewritten (doc/301 D4 / §6.3 invariant 1).
//
// Three boundary rules decide the shape of everything below:
//
//   - `support_verdict` (semantic) and `quote_status` (mechanical) are separate
//     columns with separate vocabularies. Collapsing them would make "the model
//     quoted text that is not in the snapshot" indistinguishable from "the text is
//     there but says something else" (doc/301 §5.1).
//   - a conflicting *candidate* is never evidence. It lives in its own table and
//     may only appear in a finding's `contradiction_evidence_ids_json`, never in
//     `rgx_report_citation` (doc/301 §5.3.1, gate G-A6).
//   - `requested_policy` (what was asked, frozen at creation) and
//     `release_eligibility` (what was reached, computed only after the run ends)
//     are two fields. One `tier` field would answer "which gate did this reach"
//     with a value decided before any measurement existed (doc/301 §5.5).

// Assurance run statuses (`rgx_report_assurance_run.status`).
const (
	ReportAssuranceStatusQueued    = "queued"
	ReportAssuranceStatusRunning   = "running"
	ReportAssuranceStatusSucceeded = "succeeded"
	// `degraded` means the run finished but at least one stage fell back (no
	// review model, no candidates, budget trimmed). It is not a pass: the UI must
	// show what degraded, and `release_eligibility` decides the consequence.
	ReportAssuranceStatusDegraded  = "degraded"
	ReportAssuranceStatusFailed    = "failed"
	ReportAssuranceStatusCancelled = "cancelled"
)

// ReportAssuranceTerminal reports whether a status is final.
func ReportAssuranceTerminal(status string) bool {
	switch status {
	case ReportAssuranceStatusSucceeded, ReportAssuranceStatusDegraded,
		ReportAssuranceStatusFailed, ReportAssuranceStatusCancelled:
		return true
	}
	return false
}

// Assurance run modes (`mode`). `citation_audit` is QA-2's offline sample: it
// reuses the verdict engine but writes no report-domain rows (doc/301 §5.9).
const (
	ReportAssuranceModeSemantic      = "semantic"
	ReportAssuranceModeCitationAudit = "citation_audit"
	ReportAssuranceModeFull          = "full"
)

// Assurance policies (`requested_policy`). `required` is refused by the settings
// validator in v0.7.0 (doc/301 §5.5 / gate G-A8) — it exists so v0.7.1 can open
// blocking without a second schema change.
const (
	ReportAssurancePolicyOff      = "off"
	ReportAssurancePolicyAdvisory = "advisory"
	ReportAssurancePolicyRequired = "required"
)

// Release eligibility (`release_eligibility`). `pending` is the only legal value
// before the run ends: it is what makes "queued + tier_1" unrepresentable
// (doc/301 §5.5, second review P0-5).
const (
	ReportAssuranceEligibilityPending = "pending"
	ReportAssuranceEligibilityTier0   = "tier_0"
	ReportAssuranceEligibilityTier1   = "tier_1"
	ReportAssuranceEligibilityTier2   = "tier_2"
	ReportAssuranceEligibilityBlocked = "blocked"
)

// Claim verdicts and their reasons (doc/301 §5.2). The six values that already
// exist on `section.evidence_status` keep their spelling (`supported`,
// `unsupported`, `conflicting`, `outdated`, `insufficient_evidence`,
// `not_required`); the three that are new here are `partially_supported`,
// `abstained` and `not_evaluated`.
const (
	ReportVerdictSupported          = "supported"
	ReportVerdictPartiallySupported = "partially_supported"
	ReportVerdictUnsupported        = "unsupported"
	ReportVerdictConflicting        = "conflicting"
	ReportVerdictOutdated           = "outdated"
	ReportVerdictInsufficient       = "insufficient_evidence"
	ReportVerdictAbstained          = "abstained"
	ReportVerdictNotEvaluated       = "not_evaluated"
	ReportVerdictNotRequired        = "not_required"
)

// ReportVerdicts is the closed set, in the order the dashboard prints it.
func ReportVerdicts() []string {
	return []string{
		ReportVerdictSupported, ReportVerdictPartiallySupported,
		ReportVerdictUnsupported, ReportVerdictConflicting,
		ReportVerdictOutdated, ReportVerdictInsufficient,
		ReportVerdictAbstained, ReportVerdictNotEvaluated,
		ReportVerdictNotRequired,
	}
}

// ReportVerdictScored reports whether a verdict counts as "a fact judgement was
// produced". It is *the same* set as the coverage numerator and the
// verdict-accuracy denominator, which is why one predicate serves both
// (doc/301 §4.3).
//
// The three exclusions each close a way to cheat: `not_required` (nothing was
// asked), `not_evaluated` (nothing was measured) and `abstained` (the evaluator
// refused). Counting `abstained` here would let a run raise its accuracy by
// declining the hard claims — coverage is the counterweight only if abstaining
// also lowers it.
func ReportVerdictScored(verdict string) bool {
	switch verdict {
	case ReportVerdictSupported, ReportVerdictPartiallySupported,
		ReportVerdictUnsupported, ReportVerdictConflicting,
		ReportVerdictOutdated, ReportVerdictInsufficient:
		return true
	}
	return false
}

// Abstain reasons: the evaluator ran and refused to conclude.
const (
	ReportAbstainModelUncertain = "model_uncertain"
	ReportAbstainInputInvalid   = "input_invalid"
	ReportAbstainQuoteNotFound  = "quote_not_found"
	ReportAbstainNoQuote        = "no_quote"
)

// ReportAbstainReasons is the closed set. `cache_hit` is deliberately absent: a
// cache hit *did* run (earlier), it just cost nothing, so it is an independent
// boolean, not an abstain reason (doc/301 §5.2, second review P0-3).
func ReportAbstainReasons() []string {
	return []string{
		ReportAbstainModelUncertain, ReportAbstainInputInvalid,
		ReportAbstainQuoteNotFound, ReportAbstainNoQuote,
	}
}

// Not-evaluated reasons: the evaluator never ran.
const (
	ReportNotEvaluatedBudget   = "budget_exhausted"
	ReportNotEvaluatedPolicy   = "policy_skipped"
	ReportNotEvaluatedTrigger  = "not_triggered"
	ReportNotEvaluatedCacheHit = "cache_hit"
)

// ReportNotEvaluatedReasons is the closed set. `cache_hit` is retained only as a
// migration guard: the second review moved it out of this vocabulary, and a test
// asserts it is never written (doc/301 §5.2).
func ReportNotEvaluatedReasons() []string {
	return []string{ReportNotEvaluatedBudget, ReportNotEvaluatedPolicy, ReportNotEvaluatedTrigger}
}

// Insufficient-evidence reasons (doc/301 §5.2.1).
const (
	ReportInsufficientNoCitation   = "no_citation"
	ReportInsufficientExpired      = "evidence_expired"
	ReportInsufficientTruncated    = "truncated"
	ReportInsufficientTimeUnknown  = "time_unknown"
	ReportQuoteStatusVerified      = "verified"
	ReportQuoteStatusNotFound      = "not_found"
	ReportQuoteStatusAmbiguous     = "ambiguous"
	ReportQuoteStatusNotProvided   = "not_provided"
	ReportRelevanceOnTopic         = "on_topic"
	ReportRelevanceOffTopic        = "off_topic"
	ReportStructurePassed          = "passed"
	ReportStructureFailed          = "failed"
	ReportStructureNotApplicable   = "not_applicable"
	ReportEvaluationMethodRule     = "deterministic"
	ReportEvaluationMethodSemantic = "semantic"
	ReportEvaluationMethodCross    = "cross_model"
	ReportEvaluationMethodHuman    = "human"
)

// ReportInsufficientReasons is the closed set for `insufficient_reason`.
func ReportInsufficientReasons() []string {
	return []string{
		ReportInsufficientNoCitation, ReportInsufficientExpired,
		ReportInsufficientTruncated, ReportInsufficientTimeUnknown,
	}
}

// Quote statuses. `verified` is a *necessary* but never a *sufficient* condition
// for `supported`: verified text can still contradict the claim (doc/301 §5.1.1).
func ReportQuoteStatuses() []string {
	return []string{
		ReportQuoteStatusVerified, ReportQuoteStatusNotFound,
		ReportQuoteStatusAmbiguous, ReportQuoteStatusNotProvided,
	}
}

// Independence scale (doc/301 §5.4). Ordered strongest to weakest; the stored
// value is the weakest claim the deployment can actually prove.
const (
	ReportIndependenceSeparateProvider = "independent_provider"
	ReportIndependenceDifferentFamily  = "different_model_family"
	ReportIndependenceSameFamily       = "same_model_family"
	ReportIndependenceSameModel        = "same_model"
	ReportIndependenceUnknown          = "unknown"
)

// ReportIndependenceLevels is the closed set, strongest first.
func ReportIndependenceLevels() []string {
	return []string{
		ReportIndependenceSeparateProvider, ReportIndependenceDifferentFamily,
		ReportIndependenceSameFamily, ReportIndependenceSameModel,
		ReportIndependenceUnknown,
	}
}

// Finding statuses (doc/301 §7.3). `accepted`/`rejected` are *not* used: they
// read as "the problem was accepted/rejected" while the intended meaning is "I
// acknowledge this problem exists" / "this is a false positive".
const (
	ReportFindingStatusOpen         = "open"
	ReportFindingStatusAcknowledged = "acknowledged"
	ReportFindingStatusDismissed    = "dismissed"
	ReportFindingStatusResolved     = "resolved"
)

// ReportFindingStatuses is the closed set.
func ReportFindingStatuses() []string {
	return []string{
		ReportFindingStatusOpen, ReportFindingStatusAcknowledged,
		ReportFindingStatusDismissed, ReportFindingStatusResolved,
	}
}

// Finding categories. `contradiction` (evidence contradicts the claim) and
// `source_conflict` (two cited sources contradict each other) both end in the
// `conflicting` verdict, but they need different fixes: change the conclusion vs
// change the source (doc/301 §5.3.2).
const (
	ReportFindingMissingEvidence   = "missing_evidence"
	ReportFindingUnsupported       = "unsupported"
	ReportFindingContradiction     = "contradiction"
	ReportFindingSourceConflict    = "source_conflict"
	ReportFindingOverreach         = "overreach"
	ReportFindingOmission          = "omission"
	ReportFindingNumericMismatch   = "numeric_mismatch"
	ReportFindingStructure         = "structure"
	ReportFindingOutdated          = "outdated"
	ReportFindingTraceability      = "traceability"
	ReportFindingQuoteUnverifiable = "quote_unverifiable"
	ReportFindingDisputed          = "disputed"
)

// ReportFindingSources is the closed set for `source`.
func ReportFindingSources() []string {
	return []string{"semantic", "cross_model", "human", "rule", "traceability"}
}

// Finding severities.
const (
	ReportSeverityHigh   = "high"
	ReportSeverityMedium = "medium"
	ReportSeverityLow    = "low"
)

// ReportAssuranceRun is one assurance batch of one immutable revision.
type ReportAssuranceRun struct {
	ID         string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID   string `gorm:"column:tenant_id;size:32;not null;index:idx_report_assurance_run_scope,priority:1" json:"tenant_id"`
	ReportID   string `gorm:"column:report_id;size:32;not null" json:"report_id"`
	RevisionID string `gorm:"column:revision_id;size:32;not null;index:idx_report_assurance_run_rev,priority:1" json:"revision_id"`
	Revision   int64  `gorm:"column:revision;not null" json:"revision"`
	Status     string `gorm:"column:status;size:16;not null;index:idx_report_assurance_run_scope,priority:2" json:"status"`
	Mode       string `gorm:"column:mode;size:16;not null" json:"mode"`
	// RequestedPolicy is resolved from tenant settings + template payload at
	// creation and frozen here. A client cannot set it (doc/301 §5.5).
	RequestedPolicy string `gorm:"column:requested_policy;size:16;not null" json:"requested_policy"`
	// ReleaseEligibility is written by exactly one pure function and never by a
	// request handler. `pending` until the run ends (doc/301 §5.5).
	ReleaseEligibility string `gorm:"column:release_eligibility;size:16;not null;default:pending" json:"release_eligibility"`
	PolicyJSON         string `gorm:"column:policy_json;type:text" json:"policy_json"`
	MethodVersion      string `gorm:"column:method_version;size:32" json:"method_version"`
	GenerationModelID  string `gorm:"column:generation_model_id;size:64" json:"generation_model_id"`
	ReviewModelID      string `gorm:"column:review_model_id;size:64" json:"review_model_id"`
	ReviewProvider     string `gorm:"column:review_provider;size:64" json:"review_provider"`
	ReviewDeployment   string `gorm:"column:review_deployment;size:64" json:"review_deployment"`
	Independence       string `gorm:"column:independence;size:24" json:"independence"`
	PromptHash         string `gorm:"column:prompt_hash;size:64" json:"prompt_hash"`

	// Per-verdict counts. Stored, not derived at read time: a run's numbers must
	// survive a vocabulary change in a later version (doc/301 §6.3 invariant 1).
	CountEvaluated     int `gorm:"column:evaluated;not null;default:0" json:"evaluated"`
	CountSupported     int `gorm:"column:supported;not null;default:0" json:"supported"`
	CountPartially     int `gorm:"column:partially;not null;default:0" json:"partially"`
	CountUnsupported   int `gorm:"column:unsupported;not null;default:0" json:"unsupported"`
	CountConflicting   int `gorm:"column:conflicting;not null;default:0" json:"conflicting"`
	CountOutdated      int `gorm:"column:outdated;not null;default:0" json:"outdated"`
	CountInsufficient  int `gorm:"column:insufficient;not null;default:0" json:"insufficient"`
	CountAbstained     int `gorm:"column:abstained;not null;default:0" json:"abstained"`
	CountNotEvaluated  int `gorm:"column:not_evaluated;not null;default:0" json:"not_evaluated"`
	CountNotRequired   int `gorm:"column:not_required;not null;default:0" json:"not_required"`
	AbstainedUncertain int `gorm:"column:abstained_model_uncertain;not null;default:0" json:"abstained_model_uncertain"`
	AbstainedInvalid   int `gorm:"column:abstained_input_invalid;not null;default:0" json:"abstained_input_invalid"`
	AbstainedNotFound  int `gorm:"column:abstained_quote_not_found;not null;default:0" json:"abstained_quote_not_found"`
	AbstainedNoQuote   int `gorm:"column:abstained_no_quote;not null;default:0" json:"abstained_no_quote"`
	NotEvaluatedBudget int `gorm:"column:not_evaluated_budget;not null;default:0" json:"not_evaluated_budget"`
	NotEvaluatedPolicy int `gorm:"column:not_evaluated_policy;not null;default:0" json:"not_evaluated_policy"`
	NotEvaluatedTrig   int `gorm:"column:not_evaluated_trigger;not null;default:0" json:"not_evaluated_trigger"`
	StructurePass      int `gorm:"column:structure_pass;not null;default:0" json:"structure_pass"`
	StructureFail      int `gorm:"column:structure_fail;not null;default:0" json:"structure_fail"`

	// MetricsJSON is the §4.3 snapshot: coverage with its denominator,
	// not_required_share, macro F1, per-reason breakdowns, cache replay result and
	// `release_eligibility_basis` (which conditions failed).
	MetricsJSON string `gorm:"column:metrics_json;type:text" json:"metrics_json"`

	BudgetTokens int64 `gorm:"column:budget_tokens;not null;default:0" json:"budget_tokens"`
	UsedTokens   int64 `gorm:"column:used_tokens;not null;default:0" json:"used_tokens"`
	// QuotaSettledTokens is quota actually *settled* for this run. There is no
	// `quota_reserved_tokens`: quota is reserved per model call, not per run
	// (doc/301 §6.5, second review P1-2).
	QuotaPeriod        string `gorm:"column:quota_period;size:7" json:"quota_period"`
	QuotaSettledTokens int64  `gorm:"column:quota_settled_tokens;not null;default:0" json:"quota_settled_tokens"`
	BudgetExhausted    bool   `gorm:"column:budget_exhausted;not null;default:false" json:"budget_exhausted"`

	FailureCode   string `gorm:"column:failure_code;size:16" json:"failure_code"`
	FailureReason string `gorm:"column:failure_reason;size:1024" json:"failure_reason"`
	// IdempotencyKey is scoped `assurance:${report}:${revision}:${user}:${client}`
	// (doc/301 §7.1): the unique index is tenant-wide, so the target must be part
	// of the key or two reports would collide.
	IdempotencyKey string     `gorm:"column:idempotency_key;size:128;uniqueIndex:uidx_rgx_report_assurance_run_idem,priority:2" json:"idempotency_key"`
	TriggeredBy    string     `gorm:"column:triggered_by;size:32" json:"triggered_by"`
	StartedAt      *time.Time `gorm:"column:started_at" json:"started_at"`
	FinishedAt     *time.Time `gorm:"column:finished_at" json:"finished_at"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null" json:"created_at"`
	ExpiresAt      *time.Time `gorm:"column:expires_at" json:"expires_at"`
}

func (ReportAssuranceRun) TableName() string { return "rgx_report_assurance_run" }

// ReportClaimEvaluation is one claim's verdict inside one run. Append-only: no
// update path, a re-run produces new rows (doc/301 §6.3 invariant 2).
type ReportClaimEvaluation struct {
	ID         string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID   string `gorm:"column:tenant_id;size:32;not null;index:idx_report_claim_eval_run,priority:1" json:"tenant_id"`
	RunID      string `gorm:"column:run_id;size:32;not null;uniqueIndex:uidx_rgx_report_claim_eval,priority:1" json:"run_id"`
	RevisionID string `gorm:"column:revision_id;size:32;not null" json:"revision_id"`
	SectionOrd int    `gorm:"column:section_ord;not null;index:idx_report_claim_eval_section,priority:1" json:"section_ord"`
	ClaimID    string `gorm:"column:claim_id;size:32;not null;uniqueIndex:uidx_rgx_report_claim_eval,priority:2" json:"claim_id"`
	ClaimType  string `gorm:"column:claim_type;size:24" json:"claim_type"`

	Method        string `gorm:"column:method;size:16;not null" json:"method"`
	MethodVersion string `gorm:"column:method_version;size:32" json:"method_version"`
	ModelID       string `gorm:"column:model_id;size:64" json:"model_id"`
	PromptHash    string `gorm:"column:prompt_hash;size:64" json:"prompt_hash"`
	Independence  string `gorm:"column:independence;size:24" json:"independence"`

	SupportVerdict   string `gorm:"column:support_verdict;size:24;not null" json:"support_verdict"`
	StructureVerdict string `gorm:"column:structure_verdict;size:24" json:"structure_verdict"`
	Relevance        string `gorm:"column:relevance;size:16" json:"relevance"`

	// QuoteStatus is the mechanical layer's answer about *existence*, kept apart
	// from the semantic verdict (doc/301 §5.1).
	QuoteStatus       string   `gorm:"column:quote_status;size:16" json:"quote_status"`
	QuoteText         string   `gorm:"column:quote_text;type:text" json:"quote_text"`
	QuoteSpanStart    int      `gorm:"column:quote_span_start" json:"quote_span_start"`
	QuoteSpanEnd      int      `gorm:"column:quote_span_end" json:"quote_span_end"`
	MatchedSnapshotID string   `gorm:"column:matched_snapshot_id;size:32" json:"matched_snapshot_id"`
	Confidence        *float64 `gorm:"column:confidence;type:numeric(4,3)" json:"confidence"`
	Rationale         string   `gorm:"column:rationale;type:text" json:"rationale"`

	EvidenceIDsJSON              string `gorm:"column:evidence_ids_json;type:text" json:"evidence_ids_json"`
	EvidenceQuote                string `gorm:"column:evidence_quote;type:text" json:"evidence_quote"`
	ContradictionEvidenceIDsJSON string `gorm:"column:contradiction_evidence_ids_json;type:text" json:"contradiction_evidence_ids_json"`
	ScanScope                    string `gorm:"column:scan_scope;size:32" json:"scan_scope"`

	AbstainReason      string `gorm:"column:abstain_reason;size:32" json:"abstain_reason"`
	NotEvaluatedReason string `gorm:"column:not_evaluated_reason;size:32" json:"not_evaluated_reason"`
	InsufficientReason string `gorm:"column:insufficient_reason;size:32" json:"insufficient_reason"`

	TokensIn  int64 `gorm:"column:tokens_in;not null;default:0" json:"tokens_in"`
	TokensOut int64 `gorm:"column:tokens_out;not null;default:0" json:"tokens_out"`
	CacheHit  bool  `gorm:"column:cache_hit;not null;default:false" json:"cache_hit"`
	Attempt   int   `gorm:"column:attempt;not null;default:1;uniqueIndex:uidx_rgx_report_claim_eval,priority:3" json:"attempt"`
	// CacheKey covers the full verdict input set (doc/301 §6.1). Plain index, not
	// unique: a cache *hit* also writes a row, so two rows share the key by design.
	CacheKey string `gorm:"column:cache_key;size:64;index:idx_report_claim_eval_cache,priority:1" json:"cache_key"`
	// CacheSourceEvaluationID is an audit soft pointer, not a foreign key: the
	// source row may be purged by retention, and the verdict was copied into this
	// row, so correctness never depends on it (doc/301 §6.1).
	CacheSourceEvaluationID *string   `gorm:"column:cache_source_evaluation_id;size:32" json:"cache_source_evaluation_id"`
	CreatedAt               time.Time `gorm:"column:created_at;not null" json:"created_at"`
}

func (ReportClaimEvaluation) TableName() string { return "rgx_report_claim_evaluation" }

// ReportReviewFinding is one problem a run (or a human) raised against a
// revision. Independent of the revision's content: fixing it mints a new
// revision, it never edits this one (doc/301 §5.4 rule 4).
type ReportReviewFinding struct {
	ID         string  `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID   string  `gorm:"column:tenant_id;size:32;not null;index:idx_report_finding_scope,priority:1" json:"tenant_id"`
	ReportID   string  `gorm:"column:report_id;size:32;not null" json:"report_id"`
	RevisionID string  `gorm:"column:revision_id;size:32;not null;index:idx_report_finding_scope,priority:2" json:"revision_id"`
	RunID      *string `gorm:"column:run_id;size:32;index:idx_report_finding_run" json:"run_id"`
	SectionOrd int     `gorm:"column:section_ord;not null" json:"section_ord"`
	// ClaimID is nullable, SectionOrd is not: a finding must be locatable
	// (doc/301 §6.3 invariant 8), but an omission finding points at a section.
	ClaimID  *string `gorm:"column:claim_id;size:32" json:"claim_id"`
	Source   string  `gorm:"column:source;size:16;not null" json:"source"`
	Category string  `gorm:"column:category;size:32;not null" json:"category"`
	Severity string  `gorm:"column:severity;size:8;not null;index:idx_report_finding_scope,priority:3" json:"severity"`
	Status   string  `gorm:"column:status;size:16;not null;index:idx_report_finding_scope,priority:4" json:"status"`
	// ResolvedRevisionID stores `rgx_report_revision.id` (not the revision number)
	// and is required once status is `resolved`; the service layer asserts it, no
	// DB constraint (doc/301 §7.3 keeps migration 183 purely additive).
	ResolvedRevisionID *string    `gorm:"column:resolved_revision_id;size:32" json:"resolved_revision_id"`
	Title              string     `gorm:"column:title;size:255" json:"title"`
	Detail             string     `gorm:"column:detail;type:text" json:"detail"`
	SuggestedAction    string     `gorm:"column:suggested_action;type:text" json:"suggested_action"`
	EvidenceIDsJSON    string     `gorm:"column:evidence_ids_json;type:text" json:"evidence_ids_json"`
	ModelID            string     `gorm:"column:model_id;size:64" json:"model_id"`
	PromptHash         string     `gorm:"column:prompt_hash;size:64" json:"prompt_hash"`
	ReviewedBy         string     `gorm:"column:reviewed_by;size:32" json:"reviewed_by"`
	ReviewedAt         *time.Time `gorm:"column:reviewed_at" json:"reviewed_at"`
	ReviewNote         string     `gorm:"column:review_note;type:text" json:"review_note"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null" json:"updated_at"`
	ExpiresAt          *time.Time `gorm:"column:expires_at" json:"expires_at"`
}

func (ReportReviewFinding) TableName() string { return "rgx_report_review_finding" }

// ReportConflictCandidate is one *uncited* chunk the section's own retrieval
// returned, frozen at generation time so a later review reads yesterday's pool
// instead of today's index (doc/301 §5.3.1).
//
// It is not evidence: it may appear in a finding's contradiction list and nothing
// else. It is also not a retention-free copy of the corpus — it carries the same
// redaction, the same excerpt window and a clock that may not outlive the cited
// snapshot's.
type ReportConflictCandidate struct {
	ID                 string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string `gorm:"column:tenant_id;size:32;not null" json:"tenant_id"`
	ReportID           string `gorm:"column:report_id;size:32;not null" json:"report_id"`
	RevisionID         string `gorm:"column:revision_id;size:32;not null;uniqueIndex:uidx_rgx_report_conflict_candidate,priority:1" json:"revision_id"`
	SectionOrd         int    `gorm:"column:section_ord;not null;uniqueIndex:uidx_rgx_report_conflict_candidate,priority:2" json:"section_ord"`
	SectionID          string `gorm:"column:section_id;size:32;not null" json:"section_id"`
	DatasetID          string `gorm:"column:dataset_id;size:64" json:"dataset_id"`
	DocumentID         string `gorm:"column:document_id;size:64" json:"document_id"`
	ChunkID            string `gorm:"column:chunk_id;size:64;not null;uniqueIndex:uidx_rgx_report_conflict_candidate,priority:3" json:"chunk_id"`
	ChunkOrd           int    `gorm:"column:chunk_ord" json:"chunk_ord"`
	Content            string `gorm:"column:content;type:text" json:"content"`
	ContentHash        string `gorm:"column:content_hash;size:64;not null;uniqueIndex:uidx_rgx_report_conflict_candidate,priority:4" json:"content_hash"`
	ContentSize        int64  `gorm:"column:content_size" json:"content_size"`
	Truncated          bool   `gorm:"column:truncated;not null;default:false" json:"truncated"`
	TruncationStrategy string `gorm:"column:truncation_strategy;size:32" json:"truncation_strategy"`
	PoolRank           int    `gorm:"column:pool_rank;not null;default:0" json:"pool_rank"`
	// ACLScopeJSON freezes the retrieval request's tenant/dataset/document scope.
	// Reads re-check the caller against the *current* ACL (doc/301 §5.3.1).
	ACLScopeJSON string     `gorm:"column:acl_scope_json;type:text" json:"acl_scope_json"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null" json:"created_at"`
	ExpiresAt    *time.Time `gorm:"column:expires_at" json:"expires_at"`
}

func (ReportConflictCandidate) TableName() string { return "rgx_report_conflict_candidate" }

// Assurance rebuild-projection statuses on the revision (`assurance_status`).
// They are a projection, not the truth: "was this revision assured" is answered by
// `rgx_report_assurance_run` rows (doc/301 §6.2).
const (
	ReportAssuranceProjectionNotRun = "not_run"
)
