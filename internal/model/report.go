// Package model: report answer persistence (doc/288 §7.2).
//
// The report family is deliberately a separate sub-model from the chat/citation
// model: a report has a long-lived identity, immutable revisions, per-version
// sections and its own job ledger, because "regenerate" must never be ambiguous
// between two reports and two jobs (doc/288 §7.1 boundary 2).
package model

import (
	"strconv"
	"time"
)

// Report statuses (rgx_report.status). Soft delete uses deleted_at; there is no
// `deleted` status (doc/288 §7.2 closed set).
//
// `archived` is not a step towards deletion and not a retention change: it takes a
// finished report out of the day-to-day list and refuses every operation that would
// produce new content, while everything already delivered stays readable and
// downloadable (doc/297 §2). The two facts — "put away" and "recoverable" — are
// carried by two different columns on purpose, so restoring a deleted report cannot
// silently un-archive it and archiving cannot shorten the recovery window.
const (
	ReportStatusActive   = "active"
	ReportStatusArchived = "archived"
)

// ReportLifecycleStatuses is the closed set of `rgx_report.status`, in display
// order. The frontend mirrors it (`REPORT_LIFECYCLE_STATUSES`) and the i18n test
// asserts a label for every member, so an added value fails loudly in three places
// instead of rendering raw.
var ReportLifecycleStatuses = []string{ReportStatusActive, ReportStatusArchived}

// Report access policies (rgx_report.access_policy).
//
// P0-A only ever wrote `owner_only`; the other three were frozen in the contract
// and are opened by doc/296 §2.1 (P4). Two of them name explicit grantees, which
// live in `rgx_report_access_grant` rather than in the legacy
// `access_scope_json` array: a grant row is what the list query can join, and a
// JSON array cannot be used as a filter without either a scan or a
// dialect-specific operator (doc/296 A65).
const (
	ReportAccessOwnerOnly      = "owner_only"
	ReportAccessTenantMembers  = "tenant_members"
	ReportAccessSelectedUsers  = "selected_users"
	ReportAccessSelectedGroups = "selected_groups"
)

// ReportAccessPolicies is the closed set in display order.
var ReportAccessPolicies = []string{
	ReportAccessOwnerOnly, ReportAccessTenantMembers,
	ReportAccessSelectedUsers, ReportAccessSelectedGroups,
}

// Report grant kinds (rgx_report_access_grant.grant_kind).
const (
	ReportGrantUser = "user"
	ReportGrantTeam = "team"
)

// ReportAccessGrant is one named grantee of a `selected_*` report.
//
// The row exists instead of the `access_scope_json` array the contract froze so
// that the list query and the detail check can share one predicate: a grant is
// an equality test on `(tenant_id, grant_kind, grant_id)` that an index answers,
// while an array inside a text column would need a per-dialect operator and would
// silently disagree with the detail check on any whitespace difference.
//
// Grants are replaced wholesale on every policy change (`ReplaceReportAccessGrants`),
// so there is no `deleted_at` here: a revoked grant must not keep answering.
type ReportAccessGrant struct {
	ID        string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID  string    `gorm:"column:tenant_id;size:32;not null;index:idx_report_grant_lookup,priority:1" json:"tenant_id"`
	ReportID  string    `gorm:"column:report_id;size:32;not null;uniqueIndex:uidx_rgx_report_grant,priority:1" json:"report_id"`
	GrantKind string    `gorm:"column:grant_kind;size:16;not null;uniqueIndex:uidx_rgx_report_grant,priority:2" json:"grant_kind"`
	GrantID   string    `gorm:"column:grant_id;size:32;not null;uniqueIndex:uidx_rgx_report_grant,priority:3;index:idx_report_grant_lookup,priority:2" json:"grant_id"`
	CreatedBy string    `gorm:"column:created_by;size:32" json:"created_by"`
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"created_at"`
}

func (ReportAccessGrant) TableName() string { return "rgx_report_access_grant" }

// Report access-scope payload carried by `access_scope_json`.
//
// The grants themselves are rows; this payload keeps the *request* the owner
// made (which ids, in which order) so the read surface can explain a policy
// without a second round trip, and so an audit record can show what was asked
// for even after a grant is revoked. It is never used to authorize anything
// (doc/296 A65): a stale copy must not be able to widen access.
type ReportAccessScope struct {
	UserIDs []string `json:"user_ids,omitempty"`
	TeamIDs []string `json:"team_ids,omitempty"`
}

// Report budget policies (rgx_tenant_report_policy.budget_policy, doc/296 §2.2).
//
// `enforce` is the P0-A behaviour: a call that would cross the frozen envelope is
// refused, the sections already written stay published and the rest are disclosed
// as `budget_exhausted` gaps. `observe` records every call and never refuses one —
// it exists so a workspace can *measure* what its templates really cost before it
// picks a `max_tokens` number, which is the only honest way to choose one.
const (
	ReportBudgetEnforce = "enforce"
	ReportBudgetObserve = "observe"
)

// ReportBudgetPolicies is the closed set in display order.
var ReportBudgetPolicies = []string{ReportBudgetEnforce, ReportBudgetObserve}

// Report outline auto-confirm policies (rgx_tenant_report_policy.auto_confirm_policy,
// doc/296 §2.3). `self` lets the report owner make the choice the template already
// allows; `approval` (doc/288 §8.4 ③) says the workspace wants a governance stop
// before a report skips its structure review, and this release answers that with
// a refusal rather than a silent `required` — see `requireReportOutlineConfirmation`.
const (
	ReportAutoConfirmSelf     = "self"
	ReportAutoConfirmApproval = "approval"
)

// ReportAutoConfirmPolicies is the closed set in display order.
var ReportAutoConfirmPolicies = []string{ReportAutoConfirmSelf, ReportAutoConfirmApproval}

// Report revision statuses (rgx_report_revision.status).
const (
	ReportRevisionDraft           = "draft"
	ReportRevisionGenerating      = "generating"
	ReportRevisionAwaitingOutline = "awaiting_outline"
	ReportRevisionPublished       = "published"
	ReportRevisionFailed          = "failed"
	ReportRevisionCancelled       = "cancelled"
	ReportRevisionSuperseded      = "superseded"
)

// Report completeness (rgx_report_revision.completeness). Computed once by
// finalize in the same transaction as the terminal state; readers never
// recompute it (doc/288 §7.10).
const (
	ReportCompletenessFull    = "full"
	ReportCompletenessPartial = "partial"
)

// Report job statuses (rgx_report_job.status). Terminal states are written only
// by finalize (doc/288 §8.2/§8.4).
const (
	ReportJobQueued          = "queued"
	ReportJobRunning         = "running"
	ReportJobAwaitingOutline = "awaiting_outline"
	ReportJobFinalizing      = "finalizing"
	ReportJobSucceeded       = "succeeded"
	ReportJobPartial         = "partial"
	ReportJobFailed          = "failed"
	ReportJobCancelRequested = "cancel_requested"
	ReportJobCancelled       = "cancelled"
)

// Report job kinds (rgx_report_job.job_kind). Export is an rgx_export_job, so
// there is no `export` kind (doc/288 §7.2 / K59).
const (
	ReportJobKindGenerate     = "generate"
	ReportJobKindSectionRetry = "section_retry"
)

// Research depth (rgx_report_revision.research_depth, doc/295 §2.2 / R18).
//
// `standard` is one retrieval per section — the P0/P1 behaviour. `deep` lets the
// runner ask a planner for sub-queries first, then retrieve each one. The switch
// is per revision because the two runs are different conditions, and a report
// that says "deep" must be reproducible from its own snapshot.
const (
	ReportResearchDepthStandard = "standard"
	ReportResearchDepthDeep     = "deep"
)

// Report revision triggers (rgx_report_revision.trigger, doc/295 §2.4).
//
// `manual` covers everything a human asked for, including a regeneration and the
// section-retry re-draft; `schedule` is the only value a robot can write. It is a
// closed set so "who spent this budget" is answerable from one column.
const (
	ReportTriggerManual   = "manual"
	ReportTriggerSchedule = "schedule"
)

// Report schedule cadence / update mode (rgx_report_schedule, doc/295 §2.4).
//
// `reuse_static` inherits only the base revision's non-evidence sections (the
// "背景/适用范围" kind of paragraph whose text does not depend on retrieval), and
// rewrites everything else. It is deliberately not "reuse everything": a
// scheduled report whose conclusion was copied from last month is the exact
// failure mode continuous updating exists to avoid.
const (
	ReportScheduleCadenceDaily  = "daily"
	ReportScheduleCadenceWeekly = "weekly"

	ReportScheduleUpdateFull        = "full"
	ReportScheduleUpdateReuseStatic = "reuse_static"
)

// Report schedule run outcomes (`last_status`, doc/295 §2.4). A skipped tick is
// not an error: it is the honest answer to "why is there no new version today".
const (
	ReportScheduleStatusCreated         = "created"
	ReportScheduleStatusSkippedBusy     = "skipped_busy"
	ReportScheduleStatusSkippedNoBase   = "skipped_no_base"
	ReportScheduleStatusSkippedTemplate = "skipped_template_missing"
	ReportScheduleStatusSkippedDisabled = "skipped_disabled"
	ReportScheduleStatusFailed          = "failed"
)

// Report section rewrite reasons (rgx_report_section.rewrite_reason; doc/288
// §7.10 line 367 closed set). Any content change — a user edit, a section
// retry, a quality rewrite, a regeneration — writes a *new* section row and
// supersedes the previous one, and this column says which kind of change it was.
const (
	ReportRewriteReasonUserEdit   = "user_edit"
	ReportRewriteReasonRetry      = "retry"
	ReportRewriteReasonQualityFix = "quality_fix"
	ReportRewriteReasonRegenerate = "regenerate"
	// ReportRewriteReasonReuse is R14's local carry-over (doc/288 §9.8): a new
	// revision inherits an older revision's section body and citation rows instead
	// of re-writing them. It is a distinct value rather than `regenerate` because
	// the reader's question is "was this paragraph written for this version?" and
	// "inherited from version 2" is a different answer.
	ReportRewriteReasonReuse = "reuse"
)

// Report job stages (rgx_report_job.stage). `exporting` is intentionally absent.
const (
	ReportStageOutlining        = "outlining"
	ReportStageSectionRetrieval = "section_retrieval"
	ReportStageSectionWriting   = "section_writing"
	ReportStageQualityCheck     = "quality_check"
	ReportStageAssembling       = "assembling"
)

// Report section execution statuses (rgx_report_section.status). Two of them
// are non-terminal (`pending`, `writing`): a finalize that sees one is looking at
// a job whose worker has not settled, and must wait rather than invent an
// outcome (doc/288 §8.4 "执行完成" criterion).
const (
	ReportSectionPending   = "pending"
	ReportSectionWriting   = "writing"
	ReportSectionSucceeded = "succeeded"
	ReportSectionFailed    = "failed"
	// ReportSectionSkipped marks a section the run deliberately did not write
	// (budget exhausted, or a dependency that never succeeded). A skipped section
	// is a *disclosed* gap, never a silent absence (doc/288 §7.10).
	ReportSectionSkipped = "skipped"
	// ReportSectionSuperseded marks a section row that a later version replaced
	// (a user edit, a section retry, a quality rewrite, a regeneration — any
	// §7.10 content change). Superseded rows are immutable history: the runner
	// filters them out of every status walk, and a §11.4 section retry can only
	// ever target the live version of an ord.
	ReportSectionSuperseded = "superseded"
)

// Outline confirmation modes on the content template (doc/288 §11.1). `auto` is
// a permission, not a default: P0-A templates only carry `required`.
const (
	ReportOutlineRequired = "required"
	ReportOutlineAuto     = "auto"
)

// Report content-template statuses (rgx_report_template.status). Retirement stops
// new reports from pinning a version; it never rewrites an existing revision's
// pinned `template_version` (doc/288 §11.1).
const (
	ReportTemplateStatusActive  = "active"
	ReportTemplateStatusRetired = "retired"
)

// Charge states (rgx_report_charge.state).
const (
	ReportChargeReserved = "reserved"
	ReportChargeSettled  = "settled"
	ReportChargeReleased = "released"
	ReportChargeOrphan   = "orphan"
)

// ReportLedgerKey builds the six-segment billing key (doc/288 §8.6):
// report:<report_job_id>:<attempt_seq>:<slice_seq>:<section_id>:<section_attempt>:<call_seq>
//
// It is built from business identity only — never from a clock or a random id —
// because the same call must produce the same key on a replayed slice, and a
// timestamp in the key would turn every replay into a second charge. The key
// lives in `model` rather than in the service so the writer and the reader cannot
// drift apart on segment order.
func ReportLedgerKey(jobID string, attemptSeq, sliceSeq int, sectionID string, sectionAttempt, callSeq int) string {
	return "report:" + jobID + ":" +
		strconv.Itoa(attemptSeq) + ":" +
		strconv.Itoa(sliceSeq) + ":" +
		sectionID + ":" +
		strconv.Itoa(sectionAttempt) + ":" +
		strconv.Itoa(callSeq)
}

// Charge reconcile reasons. `orphan` must carry one of these (doc/288 §8.6).
const (
	ReportReconcileStaleReserved       = "stale_reserved"
	ReportReconcileProviderUnconfirmed = "provider_unconfirmed"
)

// Charge reconcile outcomes; "" means not reconciled yet (four-value closed set).
const (
	ReportReconcileOutcomeSettled    = "settled"
	ReportReconcileOutcomeReleased   = "released"
	ReportReconcileOutcomeUnverified = "unverified"
)

// Gap reasons recorded in rgx_report_revision.gap_json (doc/288 §7.10).
const (
	ReportGapFailed          = "failed"
	ReportGapSkipped         = "skipped"
	ReportGapBudgetExhausted = "budget_exhausted"
)

// ReportGapImpactSectionMissing is the closed-set "影响范围" value of one gap
// entry. P0-A has exactly one impact: the section is not in the report. A
// second value (e.g. `thin_evidence`) is added only when the evidence slices
// can actually produce it, so the frontend banner never has to guess.
const ReportGapImpactSectionMissing = "section_missing"

// Report section evidence statuses (rgx_report_section.evidence_status, the six
// values of doc/288 §9.3). `unsupported` deliberately does NOT mean "the section
// is missing": existence is decided by `section_key`, so a required section with
// no evidence still exists and is shown with a warning (A3b).
const (
	ReportEvidenceNotRequired        = "not_required"
	ReportEvidenceSupported          = "supported"
	ReportEvidencePartiallySupported = "partially_supported"
	ReportEvidenceUnsupported        = "unsupported"
	ReportEvidenceConflicting        = "conflicting"
	ReportEvidenceOutdated           = "outdated"
)

// Report section quality statuses (rgx_report_section.quality_status).
// `needs_review` is a disclosure, not a failure: it never blocks publishing and
// never rewrites the execution status (doc/288 §7.4 / A3c).
const (
	ReportQualityNotChecked  = "not_checked"
	ReportQualityPassed      = "passed"
	ReportQualityNeedsReview = "needs_review"
	ReportQualityFailed      = "failed"
)

// Revision quality flags (metrics_json.quality_flag). The closed set is
// {ok, degraded} and it is orthogonal to per-section `quality_status` (K37):
// degrading the revision and flagging a section are different statements.
const (
	ReportQualityFlagOK       = "ok"
	ReportQualityFlagDegraded = "degraded"
)

// ReportTemplate is the content-structure template (sections, prompts, budget).
// It is not a rendering template: rgx_export_template_version keeps that role
// (doc/288 §7.2 "两类模板不要混").
type ReportTemplate struct {
	ID                      string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                string `gorm:"column:tenant_id;size:32;not null;uniqueIndex:uidx_rgx_report_template_version,priority:3" json:"tenant_id"`
	TemplateKey             string `gorm:"column:template_key;size:64;not null;uniqueIndex:uidx_rgx_report_template_version,priority:1" json:"template_key"`
	Version                 int64  `gorm:"column:version;not null;uniqueIndex:uidx_rgx_report_template_version,priority:2" json:"version"`
	NameJSON                string `gorm:"column:name_json;type:text;not null" json:"name_json"`
	OutputFormatsJSON       string `gorm:"column:output_formats_json;type:text;not null" json:"output_formats_json"`
	SectionsJSON            string `gorm:"column:sections_json;type:text;not null" json:"sections_json"`
	LimitsJSON              string `gorm:"column:limits_json;type:text;not null" json:"limits_json"`
	RetrievalJSON           string `gorm:"column:retrieval_json;type:text;not null" json:"retrieval_json"`
	EvidencePolicyJSON      string `gorm:"column:evidence_policy_json;type:text;not null" json:"evidence_policy_json"`
	OutlineConfirmation     string `gorm:"column:outline_confirmation;size:16;not null;default:required" json:"outline_confirmation"`
	AutoConfirmAllowedRoles string `gorm:"column:auto_confirm_allowed_roles;type:text" json:"auto_confirm_allowed_roles"`
	Status                  string `gorm:"column:status;size:16;not null;default:active" json:"status"`
	// Clone provenance: a tenant clone keeps its own version sequence and is
	// never overwritten by a platform upgrade (292 D4).
	SourceTemplateID      string     `gorm:"column:source_template_id;size:32" json:"source_template_id"`
	SourceTemplateVersion int64      `gorm:"column:source_template_version" json:"source_template_version"`
	SourceVersionPinned   bool       `gorm:"column:source_version_pinned;not null;default:false" json:"source_version_pinned"`
	CreatedBy             string     `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt             time.Time  `gorm:"column:created_at;not null;index" json:"created_at"`
	RetiredAt             *time.Time `gorm:"column:retired_at" json:"retired_at"`
}

func (ReportTemplate) TableName() string { return "rgx_report_template" }

// Report is the long-lived report identity. `current_revision_id` is rewritten
// only by the publish transaction (doc/288 R27).
type Report struct {
	ID                string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID          string `gorm:"column:tenant_id;size:32;not null;index:idx_report_owner,priority:1" json:"tenant_id"`
	Title             string `gorm:"column:title;size:255;not null" json:"title"`
	Question          string `gorm:"column:question;type:text" json:"question"`
	OwnerUserID       string `gorm:"column:owner_user_id;size:32;not null;index:idx_report_owner,priority:2" json:"owner_user_id"`
	OriginChatID      string `gorm:"column:origin_chat_id;size:32;index" json:"origin_chat_id"`
	OriginSessionID   string `gorm:"column:origin_session_id;size:64;index" json:"origin_session_id"`
	TemplateKey       string `gorm:"column:template_key;size:64;not null" json:"template_key"`
	CurrentRevisionID string `gorm:"column:current_revision_id;size:32;index" json:"current_revision_id"`
	AccessPolicy      string `gorm:"column:access_policy;size:24;not null;default:owner_only" json:"access_policy"`
	AccessScopeJSON   string `gorm:"column:access_scope_json;type:text" json:"access_scope_json"`
	Status            string `gorm:"column:status;size:16;not null;default:active" json:"status"`
	// ExpiresAt is the frozen report-class TTL (doc/288 §7.6): written once at
	// creation from the live policy, read verbatim by the janitor.
	ExpiresAt time.Time  `gorm:"column:expires_at;not null;index:idx_report_expires" json:"expires_at"`
	CreatedAt time.Time  `gorm:"column:created_at;not null;index:idx_report_owner,priority:3" json:"created_at"`
	UpdatedAt time.Time  `gorm:"column:updated_at;not null" json:"updated_at"`
	DeletedAt *time.Time `gorm:"column:deleted_at;index" json:"deleted_at"`
	// CleanupHoldUntil is the recovery window after a soft delete. It never
	// extends ExpiresAt (doc/288 §7.6).
	CleanupHoldUntil *time.Time `gorm:"column:cleanup_hold_until" json:"cleanup_hold_until"`
}

func (Report) TableName() string { return "rgx_report" }

// TenantReportPolicy is the tenant-level overlay on the report-class TTLs
// (doc/288 §7.6 / T20b).
//
// Every field is a pointer on purpose: `nil` means "inherit the system value",
// which is what keeps an operational change to `retention.report_days` flowing to
// every tenant that never asked for something else. Storing a copy of the system
// value would freeze inheritance at whatever the number was on the day the overlay
// row was written — the exact "改参数不生效" bug the inherited-value design avoids.
//
// The row is a *policy*, not a clock: it is read when a row is created and never
// again, because the resulting `expires_at` is frozen on that row (§7.6 invariant
// ①"改参数不追溯").
//
// `BudgetPolicy` and `AutoConfirmPolicy` (doc/296 §2.2/§2.3) are the two
// governance switches that belong to the same row: like the clocks, they are
// tenant-wide decisions an operator makes once, and putting them anywhere else
// would give the workspace two places to look for "what does this tenant allow".
type TenantReportPolicy struct {
	TenantID string `gorm:"column:tenant_id;primaryKey;size:32" json:"tenant_id"`
	// BudgetPolicy is `enforce` (default) or `observe`. Empty means inherit the
	// system default, which is `enforce`.
	BudgetPolicy string `gorm:"column:budget_policy;size:16" json:"budget_policy,omitempty"`
	// AutoConfirmPolicy is `self` (default) or `approval`: who may write
	// `outline_confirmation=auto` on a new revision.
	AutoConfirmPolicy     string    `gorm:"column:auto_confirm_policy;size:16" json:"auto_confirm_policy,omitempty"`
	ReportDays            *int      `gorm:"column:report_days" json:"report_days,omitempty"`
	ReportEvidenceDays    *int      `gorm:"column:report_evidence_days" json:"report_evidence_days,omitempty"`
	ReportArtifactDays    *int      `gorm:"column:report_artifact_days" json:"report_artifact_days,omitempty"`
	ReportEventDays       *int      `gorm:"column:report_event_days" json:"report_event_days,omitempty"`
	ReportCleanupHoldDays *int      `gorm:"column:report_cleanup_hold_days" json:"report_cleanup_hold_days,omitempty"`
	UpdatedBy             string    `gorm:"column:updated_by;size:32" json:"updated_by"`
	UpdatedAt             time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (TenantReportPolicy) TableName() string { return "rgx_tenant_report_policy" }

// ReportRevision is one immutable content version of a report. Editing sections
// mints a new revision; re-exporting the same revision mints a new artifact
// (doc/288 §7.1 boundary 1).
type ReportRevision struct {
	ID                  string `gorm:"column:id;primaryKey;size:32" json:"id"`
	ReportID            string `gorm:"column:report_id;size:32;not null;uniqueIndex:uidx_rgx_report_revision,priority:1" json:"report_id"`
	TenantID            string `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Revision            int64  `gorm:"column:revision;not null;uniqueIndex:uidx_rgx_report_revision,priority:2" json:"revision"`
	BasedOnRevision     int64  `gorm:"column:based_on_revision" json:"based_on_revision"`
	OutlineJSON         string `gorm:"column:outline_json;type:text" json:"outline_json"`
	OutlineConfirmation string `gorm:"column:outline_confirmation;size:16;not null" json:"outline_confirmation"`
	// DatasetsJSON is the knowledge binding frozen at creation (doc/288 §13):
	// `[{dataset_id, engine_dataset_id, role}]`. It is written before the outline
	// step because that step is the first one that needs to retrieve, and it is
	// the only durable record of "which datasets may this report cite" — the
	// execution snapshot is written *after* it and reads this column.
	DatasetsJSON string `gorm:"column:datasets_json;type:text" json:"datasets_json"`
	// ReusePlanJSON freezes R14's local carry-over request (doc/288 §9.8): the base
	// revision's section ords this revision asked to inherit. It is written once at
	// creation and never mutated, so an operator reading a surprising report can
	// tell "the user asked for reuse" from "the writer produced the same text".
	ReusePlanJSON string `gorm:"column:reuse_plan_json;type:text" json:"reuse_plan_json,omitempty"`
	// ResearchDepth freezes R18's planning switch (doc/295 §2.2). `standard` is the
	// P0/P1 behaviour (one retrieval per section); `deep` lets the runner plan
	// sub-queries before writing. It lives on the revision, not the report, because
	// a deep run and a standard rerun of the same report are different conditions.
	ResearchDepth string `gorm:"column:research_depth;size:16;not null;default:standard" json:"research_depth"`
	// Trigger and ScheduleID record who asked for this revision (doc/295 §2.4).
	// `manual` is every revision a human asked for — creation, regeneration, and the
	// retry path's re-drafting — so an empty value would be ambiguous rather than
	// "the old shape"; the migration backfills `manual`.
	Trigger               string `gorm:"column:trigger;size:16;not null;default:manual" json:"trigger"`
	ScheduleID            string `gorm:"column:schedule_id;size:32;index" json:"schedule_id,omitempty"`
	ExecutionSnapshotJSON string `gorm:"column:execution_snapshot_json;type:text" json:"execution_snapshot_json"`
	ExecutionSnapshotHash string `gorm:"column:execution_snapshot_hash;size:64" json:"execution_snapshot_hash"`
	TemplateKey           string `gorm:"column:template_key;size:64;not null" json:"template_key"`
	TemplateVersion       int64  `gorm:"column:template_version" json:"template_version"`
	EngineVersion         string `gorm:"column:engine_version;size:32" json:"engine_version"`
	MetricsJSON           string `gorm:"column:metrics_json;type:text" json:"metrics_json"`
	// GapJSON is written by finalize together with the terminal state; the
	// banner, the job.partial payload and the artifact disclosure only read it.
	// AssuranceRunID / AssuranceStatus / AssuranceSummaryJSON / AssuranceAckJSON
	// are a *rebuildable projection* of the latest assurance run (doc/301 §6.2).
	// They may be overwritten by a new run, so code must never answer "was this
	// revision assured when it was published" from them — the immutable answer is
	// an `rgx_report_assurance_run` row plus `assurance_ack_json`.
	AssuranceRunID       string `gorm:"column:assurance_run_id;size:32" json:"assurance_run_id,omitempty"`
	AssuranceStatus      string `gorm:"column:assurance_status;size:16;not null;default:not_run" json:"assurance_status"`
	AssuranceSummaryJSON string `gorm:"column:assurance_summary_json;type:text" json:"assurance_summary_json,omitempty"`
	// AssuranceAckJSON records the explicit "export anyway" acknowledgement: who,
	// when, and what they were shown. Renamed from the first draft's
	// `publish_ack_json`, which read as a *publish* confirmation (doc/301 §6.2).
	AssuranceAckJSON string `gorm:"column:assurance_ack_json;type:text" json:"assurance_ack_json,omitempty"`
	GapJSON          string `gorm:"column:gap_json;type:text" json:"gap_json"`
	Status           string `gorm:"column:status;size:24;not null" json:"status"`
	Completeness     string `gorm:"column:completeness;size:16" json:"completeness"`
	// No clock of its own: a revision lives and dies with its report (doc/288
	// §7.2 column list). The evidence-safe clock belongs to the snapshot.
	PublishedAt  *time.Time `gorm:"column:published_at" json:"published_at"`
	SupersededAt *time.Time `gorm:"column:superseded_at" json:"superseded_at"`
	CreatedBy    string     `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null" json:"created_at"`
}

func (ReportRevision) TableName() string { return "rgx_report_revision" }

// ReportJob is the business task and the only authoritative status. It is not
// the runtime job (rgx_job): one business job spans several runtime slices.
type ReportJob struct {
	ID                  string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID            string `gorm:"column:tenant_id;size:32;not null;index:idx_report_job_scope,priority:1" json:"tenant_id"`
	ReportID            string `gorm:"column:report_id;size:32;not null;index" json:"report_id"`
	RevisionID          string `gorm:"column:revision_id;size:32;not null;index" json:"revision_id"`
	JobKind             string `gorm:"column:job_kind;size:24;not null" json:"job_kind"`
	Status              string `gorm:"column:status;size:24;not null;index:idx_report_job_scope,priority:2" json:"status"`
	Stage               string `gorm:"column:stage;size:32" json:"stage"`
	AttemptSeq          int    `gorm:"column:attempt_seq;not null;default:0" json:"attempt_seq"`
	SliceSeq            int    `gorm:"column:slice_seq;not null;default:0" json:"slice_seq"`
	CurrentRuntimeJobID string `gorm:"column:current_runtime_job_id;size:32;index" json:"current_runtime_job_id"`
	EventSeq            int64  `gorm:"column:event_seq;not null;default:0" json:"event_seq"`
	ProgressDone        int    `gorm:"column:progress_done;not null;default:0" json:"progress_done"`
	ProgressTotal       int    `gorm:"column:progress_total;not null;default:0" json:"progress_total"`
	BudgetJSON          string `gorm:"column:budget_json;type:text" json:"budget_json"`
	// ReservedTokens is the envelope frozen at creation. `ReportBudgetPolicy` says
	// whether it is a limit or a measurement; both are frozen here so a tenant-wide
	// switch flipped mid-run cannot change a job that is already spending (doc/296
	// §2.2). An extension approved through governance raises this number and never
	// touches `rgx_report_charge`: the ledger records what was spent, this column
	// records what was allowed.
	ReservedTokens int64 `gorm:"column:reserved_tokens;not null;default:0" json:"reserved_tokens"`
	// ReportBudgetPolicy is `enforce` (refuse a call that would cross the envelope)
	// or `observe` (record it and keep going). Empty reads as `enforce` so every row
	// written before this column existed keeps its behaviour.
	ReportBudgetPolicy string `gorm:"column:budget_policy;size:16" json:"budget_policy"`
	ActualTokens       int64  `gorm:"column:actual_tokens;not null;default:0" json:"actual_tokens"`
	ChargedTokens      int64  `gorm:"column:charged_tokens;not null;default:0" json:"charged_tokens"`
	ReleasedTokens     int64  `gorm:"column:released_tokens;not null;default:0" json:"released_tokens"`
	OverageTokens      int64  `gorm:"column:overage_tokens;not null;default:0" json:"overage_tokens"`
	// PendingReconcileTokens keeps unconfirmable reservations visible instead of
	// reporting them as 0 (292 D7).
	PendingReconcileTokens int64      `gorm:"column:pending_reconcile_tokens;not null;default:0" json:"pending_reconcile_tokens"`
	IdempotencyKey         string     `gorm:"column:idempotency_key;size:128" json:"idempotency_key"`
	CancelRequestedAt      *time.Time `gorm:"column:cancel_requested_at" json:"cancel_requested_at"`
	LastCheckpointAt       *time.Time `gorm:"column:last_checkpoint_at" json:"last_checkpoint_at"`
	ErrorCode              string     `gorm:"column:error_code;size:16" json:"error_code"`
	StartedAt              *time.Time `gorm:"column:started_at" json:"started_at"`
	FinishedAt             *time.Time `gorm:"column:finished_at" json:"finished_at"`
	// FinalizedAt non-null means the terminal write already happened; it is the
	// idempotency criterion of finalize (doc/288 §8.2).
	FinalizedAt *time.Time `gorm:"column:finalized_at" json:"finalized_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;not null;index:idx_report_job_scope,priority:3" json:"updated_at"`
}

func (ReportJob) TableName() string { return "rgx_report_job" }

// ReportJobAttempt maps one business attempt/slice to one runtime job.
type ReportJobAttempt struct {
	ID            string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID      string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ReportJobID   string     `gorm:"column:report_job_id;size:32;not null;uniqueIndex:uidx_rgx_report_job_attempt_slice,priority:1" json:"report_job_id"`
	AttemptSeq    int        `gorm:"column:attempt_seq;not null;uniqueIndex:uidx_rgx_report_job_attempt_slice,priority:2" json:"attempt_seq"`
	SliceSeq      int        `gorm:"column:slice_seq;not null;uniqueIndex:uidx_rgx_report_job_attempt_slice,priority:3" json:"slice_seq"`
	RuntimeJobID  string     `gorm:"column:runtime_job_id;size:32;not null;uniqueIndex:uidx_rgx_report_job_attempt_runtime" json:"runtime_job_id"`
	RuntimeJobKey string     `gorm:"column:runtime_job_key;size:160;index" json:"runtime_job_key"`
	Status        string     `gorm:"column:status;size:24;not null" json:"status"`
	Outcome       string     `gorm:"column:outcome;size:24" json:"outcome"`
	ClaimedBy     string     `gorm:"column:claimed_by;size:64" json:"claimed_by"`
	StartedAt     *time.Time `gorm:"column:started_at" json:"started_at"`
	FinishedAt    *time.Time `gorm:"column:finished_at" json:"finished_at"`
	ErrorCode     string     `gorm:"column:error_code;size:16" json:"error_code"`
}

func (ReportJobAttempt) TableName() string { return "rgx_report_job_attempt" }

// ReportSchedule is R20's recurring plan (doc/295 §2.4): one per report, owned by
// the report's owner, expressed in the workspace's own wall clock via
// TZOffsetMin rather than a timezone name.
//
// Why one per report: "when does this report next run" must have exactly one
// answer, and two plans would spend the same budget twice at the same minute.
// Why an explicit offset instead of an IANA zone: the connector scheduling already
// stores an interval, and DST-correct zone databases are a dependency the report
// surface does not otherwise carry; an offset is a number the UI can show back.
//
// NextRunAt is authoritative: a tick claims `enabled AND next_run_at <= now` and
// advances it before doing any work, so a crash mid-trigger delays a run instead
// of duplicating it.
type ReportSchedule struct {
	ID       string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID string `gorm:"column:tenant_id;size:32;not null;index:idx_report_schedule_due,priority:1" json:"tenant_id"`
	ReportID string `gorm:"column:report_id;size:32;not null;uniqueIndex:uidx_rgx_report_schedule_report" json:"report_id"`
	OwnerID  string `gorm:"column:owner_id;size:32;not null" json:"owner_id"`
	Cadence  string `gorm:"column:cadence;size:16;not null" json:"cadence"`
	Hour     int    `gorm:"column:hour;not null;default:9" json:"hour"`
	Minute   int    `gorm:"column:minute;not null;default:0" json:"minute"`
	// Weekday is only meaningful for `weekly`: 0=Sunday..6=Saturday, matching
	// time.Weekday so the client and the server count the same day.
	Weekday     int        `gorm:"column:weekday;not null;default:1" json:"weekday"`
	TZOffsetMin int        `gorm:"column:tz_offset_min;not null;default:480" json:"tz_offset_min"`
	UpdateMode  string     `gorm:"column:update_mode;size:16;not null;default:full" json:"update_mode"`
	Enabled     bool       `gorm:"column:enabled;not null" json:"enabled"`
	NextRunAt   time.Time  `gorm:"column:next_run_at;not null;index:idx_report_schedule_due,priority:2" json:"next_run_at"`
	LastRunAt   *time.Time `gorm:"column:last_run_at" json:"last_run_at"`
	// LastRevision is the revision the last trigger minted; 0 means "none yet" and
	// is rendered as "暂无" rather than as version 0.
	LastRevision int64 `gorm:"column:last_revision;not null;default:0" json:"last_revision"`
	// LastStatus is the closed set above. Empty on a schedule that never ran, which
	// the UI must show as "尚未运行" and never as a success.
	LastStatus string `gorm:"column:last_status;size:32" json:"last_status"`
	// LastError carries the human reason behind `failed`; skipped reasons live in
	// LastStatus so a client does not have to parse prose.
	LastError string    `gorm:"column:last_error;type:text" json:"last_error"`
	CreatedBy string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (ReportSchedule) TableName() string { return "rgx_report_schedule" }

// ReportSection is one section of one revision version. Execution status and
// quality status are two dimensions and must not be folded (doc/288 R21).
type ReportSection struct {
	ID                    string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID              string `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	RevisionID            string `gorm:"column:revision_id;size:32;not null;uniqueIndex:uidx_rgx_report_section_version,priority:1;index:idx_report_section_rev_ord,priority:1" json:"revision_id"`
	Ord                   int    `gorm:"column:ord;not null;uniqueIndex:uidx_rgx_report_section_version,priority:2;index:idx_report_section_rev_ord,priority:2" json:"ord"`
	SectionKey            string `gorm:"column:section_key;size:64;not null" json:"section_key"`
	SectionVersion        int    `gorm:"column:section_version;not null;uniqueIndex:uidx_rgx_report_section_version,priority:3" json:"section_version"`
	BasedOnSectionVersion int    `gorm:"column:based_on_section_version" json:"based_on_section_version"`
	RewriteReason         string `gorm:"column:rewrite_reason;size:32" json:"rewrite_reason"`
	Title                 string `gorm:"column:title;size:255;not null" json:"title"`
	Query                 string `gorm:"column:query;type:text" json:"query"`
	DependsOnSectionsJSON string `gorm:"column:depends_on_sections_json;type:text" json:"depends_on_sections_json"`
	// No column default on purpose: gorm omits a zero-valued field that declares
	// one, so `false` would silently be stored as the default (`true`) and a
	// template saying "this section needs no evidence" could never take effect.
	RequiresEvidence bool `gorm:"column:requires_evidence;not null" json:"requires_evidence"`
	// EvidencePreviewJSON is the outline-time evidence盘面 (doc/288 §9.6): how many
	// chunks this section's query hit, the main source documents, and whether that
	// is thin against `min_evidence`. It is frozen when the outline is written, in
	// the same transaction, so the reviewer's confirmation is recorded next to the
	// evidence they saw. Advisory only — it is never a sufficiency verdict, which
	// is why `insufficient` can be null ("did not look").
	EvidencePreviewJSON string `gorm:"column:evidence_preview_json;type:text" json:"evidence_preview_json,omitempty"`
	// LocationHintsJSON is R17: compilation artifacts (page index / wiki / tree)
	// that help a reader find the source page. They are *navigation*, never
	// evidence — nothing here reaches `rgx_report_citation`, the evidence snapshot
	// or the claim support check (doc/295 A54). Frozen at outline time so the
	// reader sees the same locator the author did.
	LocationHintsJSON string `gorm:"column:location_hints_json;type:text" json:"location_hints_json,omitempty"`
	// ResearchPlanJSON is R18: the planner's sub-queries, how many chunks each
	// returned, and which fallback (if any) replaced the plan. Written with
	// `prompt_hash` at BeginReportSection because it describes that prompt's inputs.
	// PoolHitChunks / PoolInsufficient / PoolCandidateCount are the *writing-time*
	// retrieval pool of this section (doc/301 §5.8 item 1), the counterpart of the
	// outline-time `evidence_preview_json`. All three are nullable on purpose:
	// `NULL` means "this revision did not record it", which is not the same answer
	// as `0` (= the pool returned nothing) — the same discipline as
	// `evidence_preview_json.insufficient`.
	PoolHitChunks      *int   `gorm:"column:pool_hit_chunks" json:"pool_hit_chunks,omitempty"`
	PoolInsufficient   *bool  `gorm:"column:pool_insufficient" json:"pool_insufficient,omitempty"`
	PoolCandidateCount *int   `gorm:"column:pool_candidate_count" json:"pool_candidate_count,omitempty"`
	ResearchPlanJSON   string `gorm:"column:research_plan_json;type:text" json:"research_plan_json,omitempty"`
	Status             string `gorm:"column:status;size:24;not null;index" json:"status"`
	QualityStatus      string `gorm:"column:quality_status;size:24;not null;default:not_checked" json:"quality_status"`
	EvidenceStatus     string `gorm:"column:evidence_status;size:24" json:"evidence_status"`
	// GapReason is why the section ended without content, from the closed set
	// `failed | skipped | budget_exhausted` (doc/288 §7.8 `gap_json.reason`). It is
	// written by the runner that made the decision because the reason cannot be
	// reconstructed afterwards: `quality_status='failed'` is set both when the
	// budget bail-out skipped the section and when the model actually failed, and
	// the two are different promises to the reader ("buy more budget" vs "fix the
	// source"). Empty on a row that succeeded.
	GapReason     string     `gorm:"column:gap_reason;size:32" json:"gap_reason"`
	PromptHash    string     `gorm:"column:prompt_hash;size:64" json:"prompt_hash"`
	ContentMD     string     `gorm:"column:content_md;type:text" json:"content_md"`
	ContentHash   string     `gorm:"column:content_hash;size:64" json:"content_hash"`
	TokensIn      int64      `gorm:"column:tokens_in;not null;default:0" json:"tokens_in"`
	TokensOut     int64      `gorm:"column:tokens_out;not null;default:0" json:"tokens_out"`
	CitationCount int        `gorm:"column:citation_count;not null;default:0" json:"citation_count"`
	ClaimCount    int        `gorm:"column:claim_count;not null;default:0" json:"claim_count"`
	Attempt       int        `gorm:"column:attempt;not null;default:0" json:"attempt"`
	StartedAt     *time.Time `gorm:"column:started_at" json:"started_at"`
	FinishedAt    *time.Time `gorm:"column:finished_at" json:"finished_at"`
}

func (ReportSection) TableName() string { return "rgx_report_section" }

// Retrieval score types (rgx_report_citation.retrieval_score_type). The three
// values of doc/288 §7.5 are kept apart because the numbers are not comparable:
// a cosine similarity and a fused term/vector score both live in [0,1] and mean
// different things, so a reader who compares two citations needs to know which.
const (
	ReportRetrievalScoreVector = "vector"
	ReportRetrievalScoreTerm   = "term"
	ReportRetrievalScoreHybrid = "hybrid"
)

// Claim types (rgx_report_claim.claim_type, the four values of doc/288 §9.2).
// `fact` is the only type that asserts something about the sources, which is why
// it is the only type the L3 support check scores.
const (
	ReportClaimTypeFact           = "fact"
	ReportClaimTypeInference      = "inference"
	ReportClaimTypeRecommendation = "recommendation"
	ReportClaimTypeLimitation     = "limitation"
)

// Claim support statuses (rgx_report_claim.support_status).
//
// `not_required` exists because a recommendation or a stated limitation is not
// making a verifiable claim: scoring it would build a support rate out of
// sentences that never claimed support (§9.2 “推断与建议部分不代表已核实事实”).
const (
	ReportClaimSupportSupported          = "supported"
	ReportClaimSupportPartiallySupported = "partially_supported"
	ReportClaimSupportUnsupported        = "unsupported"
	ReportClaimSupportNotRequired        = "not_required"
)

// ReportClaim is a claim inside a section. P1 writes rows; P0 creates the table
// so no second migration is needed (doc/288 §7.2).
type ReportClaim struct {
	ID             string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID       string `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	RevisionID     string `gorm:"column:revision_id;size:32;not null;index:idx_report_claim_revision,priority:1" json:"revision_id"`
	SectionID      string `gorm:"column:section_id;size:32;not null;uniqueIndex:uidx_rgx_report_claim_section_ord,priority:1" json:"section_id"`
	SectionOrd     int    `gorm:"column:section_ord;not null;index:idx_report_claim_revision,priority:2" json:"section_ord"`
	SectionVersion int    `gorm:"column:section_version;not null" json:"section_version"`
	Ord            int    `gorm:"column:ord;not null;uniqueIndex:uidx_rgx_report_claim_section_ord,priority:2" json:"ord"`
	ClaimText      string `gorm:"column:claim_text;type:text;not null" json:"claim_text"`
	ClaimType      string `gorm:"column:claim_type;size:32" json:"claim_type"`
	SupportStatus  string `gorm:"column:support_status;size:24" json:"support_status"`
}

func (ReportClaim) TableName() string { return "rgx_report_claim" }

// ReportCitation binds a section to one evidence snapshot AND carries the
// per-reference audit context (EvidenceUsage). Duplicate content is deduplicated
// in the snapshot table; usage is never deduplicated (doc/288 §7.5.1).
type ReportCitation struct {
	ID                     string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID               string `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	RevisionID             string `gorm:"column:revision_id;size:32;not null;index:idx_report_citation_revision,priority:1" json:"revision_id"`
	SectionID              string `gorm:"column:section_id;size:32;not null;uniqueIndex:uidx_rgx_report_citation_section_ord,priority:1" json:"section_id"`
	SectionOrd             int    `gorm:"column:section_ord;not null;index:idx_report_citation_revision,priority:2" json:"section_ord"`
	SectionVersion         int    `gorm:"column:section_version;not null" json:"section_version"`
	Ord                    int    `gorm:"column:ord;not null;uniqueIndex:uidx_rgx_report_citation_section_ord,priority:2" json:"ord"`
	ClaimID                string `gorm:"column:claim_id;size:32" json:"claim_id"`
	EvidenceSnapshotID     string `gorm:"column:evidence_snapshot_id;size:32;not null;index" json:"evidence_snapshot_id"`
	SourceDatasetID        string `gorm:"column:source_dataset_id;size:32" json:"source_dataset_id"`
	SourceEngineDatasetID  string `gorm:"column:source_engine_dataset_id;size:64" json:"source_engine_dataset_id"`
	SourceDocumentID       string `gorm:"column:source_document_id;size:32" json:"source_document_id"`
	SourceDocumentRevision int64  `gorm:"column:source_document_revision" json:"source_document_revision"`
	// DocumentLabel is the human name the appendix prints (doc/288 §12: 证据附录
	// 列出「快照片段 + 文档名 + 分数类型」). The id alone is not a label a reviewer
	// can read, and re-resolving the name at export time would let a renamed or
	// deleted document change what an already-published report says about it.
	DocumentLabel             string    `gorm:"column:document_label;size:255" json:"document_label"`
	SourceChunkID             string    `gorm:"column:source_chunk_id;size:64" json:"source_chunk_id"`
	SourceContentHash         string    `gorm:"column:source_content_hash;size:64" json:"source_content_hash"`
	SourcePermissionScopeJSON string    `gorm:"column:source_permission_scope_json;type:text" json:"source_permission_scope_json"`
	ReferenceWindowJSON       string    `gorm:"column:reference_window_json;type:text" json:"reference_window_json"`
	ReferencedAt              time.Time `gorm:"column:referenced_at;not null" json:"referenced_at"`
	ReferencedBy              string    `gorm:"column:referenced_by;size:32" json:"referenced_by"`
	RetrievalScore            float64   `gorm:"column:retrieval_score" json:"retrieval_score"`
	RetrievalScoreType        string    `gorm:"column:retrieval_score_type;size:24" json:"retrieval_score_type"`
	RerankScore               float64   `gorm:"column:rerank_score" json:"rerank_score"`
	Snippet                   string    `gorm:"column:snippet;type:text" json:"snippet"`
	UsedInText                bool      `gorm:"column:used_in_text;not null;default:false" json:"used_in_text"`
}

func (ReportCitation) TableName() string { return "rgx_report_citation" }

// ReportCharge is the append-only per-call billing ledger. The six-segment
// ledger key is the only guard against double-charging a replayed slice
// (doc/288 §8.6 / 291 P0-2).
type ReportCharge struct {
	ID               string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID         string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ReportJobID      string     `gorm:"column:report_job_id;size:32;not null;index:idx_report_charge_job,priority:1" json:"report_job_id"`
	AttemptSeq       int        `gorm:"column:attempt_seq;not null" json:"attempt_seq"`
	SliceSeq         int        `gorm:"column:slice_seq;not null" json:"slice_seq"`
	SectionID        string     `gorm:"column:section_id;size:32;not null;index:idx_report_charge_section" json:"section_id"`
	SectionAttempt   int        `gorm:"column:section_attempt;not null" json:"section_attempt"`
	CallSeq          int        `gorm:"column:call_seq;not null" json:"call_seq"`
	LedgerKey        string     `gorm:"column:ledger_key;size:200;not null;uniqueIndex:uidx_rgx_report_charge_ledger" json:"ledger_key"`
	State            string     `gorm:"column:state;size:16;not null;default:reserved;index:idx_report_charge_job,priority:2" json:"state"`
	ReservedTokens   int64      `gorm:"column:reserved_tokens;not null;default:0" json:"reserved_tokens"`
	ActualTokens     int64      `gorm:"column:actual_tokens;not null;default:0" json:"actual_tokens"`
	ChargedTokens    int64      `gorm:"column:charged_tokens;not null;default:0" json:"charged_tokens"`
	ReleasedTokens   int64      `gorm:"column:released_tokens;not null;default:0" json:"released_tokens"`
	ModelID          string     `gorm:"column:model_id;size:64" json:"model_id"`
	RuntimeJobID     string     `gorm:"column:runtime_job_id;size:32" json:"runtime_job_id"`
	ReconcileReason  string     `gorm:"column:reconcile_reason;size:32" json:"reconcile_reason"`
	ReconcileOutcome string     `gorm:"column:reconcile_outcome;size:16" json:"reconcile_outcome"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null;index:idx_report_charge_stale" json:"created_at"`
	SettledAt        *time.Time `gorm:"column:settled_at" json:"settled_at"`
}

func (ReportCharge) TableName() string { return "rgx_report_charge" }

// ReportEvidenceSnapshot freezes evidence CONTENT for later verification. It
// deliberately stores no permission context: that belongs to each citation
// (doc/288 §7.4 / 291 P0-3).
//
// Retention clears `content` and writes a tombstone (`purged_at`/`purged_reason`)
// instead of deleting the row (doc/319 §9.2 方案 B, R-01). A hard delete destroyed
// the only fact that could distinguish "this snapshot expired" from "this snapshot
// never existed", so an assurance run could only report `not_provided` — blaming
// the author for a citation the product itself removed.
type ReportEvidenceSnapshot struct {
	ID                 string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string     `gorm:"column:tenant_id;size:32;not null;uniqueIndex:uidx_rgx_report_evidence_dedup,priority:1" json:"tenant_id"`
	DatasetID          string     `gorm:"column:dataset_id;size:32" json:"dataset_id"`
	DocumentID         string     `gorm:"column:document_id;size:32" json:"document_id"`
	DocumentRevision   int64      `gorm:"column:document_revision" json:"document_revision"`
	ChunkID            string     `gorm:"column:chunk_id;size:64;not null;uniqueIndex:uidx_rgx_report_evidence_dedup,priority:2" json:"chunk_id"`
	ChunkOrd           int        `gorm:"column:chunk_ord" json:"chunk_ord"`
	SourceContentHash  string     `gorm:"column:source_content_hash;size:64;not null;uniqueIndex:uidx_rgx_report_evidence_dedup,priority:3" json:"source_content_hash"`
	SourceContentSize  int64      `gorm:"column:source_content_size" json:"source_content_size"`
	Content            string     `gorm:"column:content;type:text;not null" json:"content"`
	ContentHash        string     `gorm:"column:content_hash;size:64;not null" json:"content_hash"`
	ContentSize        int64      `gorm:"column:content_size;not null" json:"content_size"`
	Truncated          bool       `gorm:"column:truncated;not null;default:false" json:"truncated"`
	TruncationStrategy string     `gorm:"column:truncation_strategy;size:32" json:"truncation_strategy"`
	SourceParsedAt     *time.Time `gorm:"column:source_parsed_at" json:"source_parsed_at"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null" json:"created_at"`
	ExpiresAt          time.Time  `gorm:"column:expires_at;not null;index:idx_report_evidence_expires" json:"expires_at"`
	// PurgedAt is when retention actually cleared the content; ExpiresAt is when it
	// became due. Two facts, two columns: inferring one from the other would let an
	// edited `retention.report_evidence_days` rewrite history.
	PurgedAt     *time.Time `gorm:"column:purged_at;index:idx_report_evidence_purged" json:"purged_at"`
	PurgedReason string     `gorm:"column:purged_reason;size:32" json:"purged_reason"`
}

// ReportEvidencePurgeReasons is the closed set for `purged_reason`.
const (
	// ReportEvidencePurgeRetentionExpired is the only reason written today: the
	// snapshot's own clock ran out (doc/288 §7.6).
	ReportEvidencePurgeRetentionExpired = "retention_expired"
)

// Purged reports whether retention already cleared this snapshot's content. The
// tombstone row stays readable so the *reason* a citation is unverifiable remains
// answerable long after the content is gone.
func (s ReportEvidenceSnapshot) Purged() bool { return s.PurgedAt != nil }

// Evidence truncation strategies (doc/288 §7.5 / A33). The set is closed for the
// same reason every other report enum is: a free-form string in the column would let
// a reader guess whether "" means "not truncated" or "unknown strategy".
const (
	// ReportEvidenceTruncationNone is the empty strategy of a snapshot that stores
	// the chunk verbatim.
	ReportEvidenceTruncationNone = ""
	// ReportEvidenceTruncationExcerptWindow is a prefix window of the chunk, sized
	// to the per-snapshot budget and guaranteed to contain the citation snippet.
	ReportEvidenceTruncationExcerptWindow = "excerpt_window"
)

func (ReportEvidenceSnapshot) TableName() string { return "rgx_report_evidence_snapshot" }

// ReportJobEvent is the replayable event stream of one report job. `seq` is
// allocated by rgx_report_job.event_seq (doc/288 R23).
type ReportJobEvent struct {
	ID          string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID    string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	JobID       string    `gorm:"column:job_id;size:32;not null;uniqueIndex:uidx_rgx_report_job_event_seq,priority:1" json:"job_id"`
	Seq         int64     `gorm:"column:seq;not null;uniqueIndex:uidx_rgx_report_job_event_seq,priority:2" json:"seq"`
	Type        string    `gorm:"column:type;size:48;not null;index" json:"type"`
	PayloadJSON string    `gorm:"column:payload_json;type:text" json:"payload_json"`
	CreatedAt   time.Time `gorm:"column:created_at;not null" json:"created_at"`
	// ExpiresAt is the frozen event-class TTL; the event stream is the cheapest
	// class to lose (doc/288 §7.6).
	ExpiresAt time.Time `gorm:"column:expires_at;not null;index:idx_report_event_expires" json:"expires_at"`
}

func (ReportJobEvent) TableName() string { return "rgx_report_job_event" }
