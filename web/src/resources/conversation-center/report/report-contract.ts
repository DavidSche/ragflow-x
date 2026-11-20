/**
 * T0 contract — the report-family closed sets shared with the Go backend.
 *
 * Source of truth lives in Go (doc/288 §16 T0):
 *   internal/model/report.go, internal/model/answer_delivery.go,
 *   internal/service/report_codes.go, internal/service/report_events.go.
 *
 * Every value below is a closed set: the server rejects anything outside it, so
 * the client must never invent a value either. Add a value ⇔ add it here AND in
 * the Go constants AND in the §8.8 event registry — a value added on one side
 * only is a contract break (doc/288 §8.8). The unit test in
 * report-contract.test.ts mirrors the Go registry guards.
 *
 * P0-A tenants only ever WRITE `owner_only`; the other access policies are
 * frozen here and opened in P1 (§13 / 292 D6). Being listed in this file is not
 * permission to write.
 */

export const REPORT_ACCESS_POLICIES = [
  "owner_only",
  "tenant_members",
  "selected_users",
  "selected_groups",
] as const;
export type ReportAccessPolicy = (typeof REPORT_ACCESS_POLICIES)[number];

/* ------------------------------------------------------------------ */
/* §7.2 closed sets (model/report.go)                                 */
/* ------------------------------------------------------------------ */

export const REPORT_STATUSES = ["active", "archived"] as const;
export type ReportStatus = (typeof REPORT_STATUSES)[number];

export const REPORT_REVISION_STATUSES = [
  "draft",
  "generating",
  "awaiting_outline",
  "published",
  "failed",
  "cancelled",
  "superseded",
] as const;
export type ReportRevisionStatus = (typeof REPORT_REVISION_STATUSES)[number];

export const REPORT_COMPLETENESS = ["full", "partial"] as const;
export type ReportCompleteness = (typeof REPORT_COMPLETENESS)[number];

export const REPORT_JOB_STATUSES = [
  "queued",
  "running",
  "awaiting_outline",
  "finalizing",
  "succeeded",
  "partial",
  "failed",
  "cancel_requested",
  "cancelled",
] as const;
export type ReportJobStatus = (typeof REPORT_JOB_STATUSES)[number];

export const REPORT_JOB_KINDS = ["generate", "section_retry"] as const;
export type ReportJobKind = (typeof REPORT_JOB_KINDS)[number];

export const REPORT_REWRITE_REASONS = [
  "user_edit",
  "retry",
  "quality_fix",
  "regenerate",
] as const;
export type ReportRewriteReason = (typeof REPORT_REWRITE_REASONS)[number];

export const REPORT_STAGES = [
  "outlining",
  "section_retrieval",
  "section_writing",
  "quality_check",
  "assembling",
] as const;
export type ReportStage = (typeof REPORT_STAGES)[number];

export const REPORT_SECTION_STATUSES = [
  "pending",
  "writing",
  "succeeded",
  "failed",
  "skipped",
  "superseded",
] as const;
export type ReportSectionStatus = (typeof REPORT_SECTION_STATUSES)[number];

export const REPORT_OUTLINE_MODES = ["required", "auto"] as const;
export type ReportOutlineMode = (typeof REPORT_OUTLINE_MODES)[number];

export const REPORT_TEMPLATE_STATUSES = ["active", "retired"] as const;
export type ReportTemplateStatus = (typeof REPORT_TEMPLATE_STATUSES)[number];

export const REPORT_EVIDENCE_STATUSES = [
  "not_required",
  "supported",
  "partially_supported",
  "unsupported",
  "conflicting",
  "outdated",
] as const;
export type ReportEvidenceStatus = (typeof REPORT_EVIDENCE_STATUSES)[number];

export const REPORT_QUALITY_STATUSES = [
  "not_checked",
  "passed",
  "needs_review",
  "failed",
] as const;
export type ReportQualityStatus = (typeof REPORT_QUALITY_STATUSES)[number];

export const REPORT_QUALITY_FLAGS = ["ok", "degraded"] as const;
export type ReportQualityFlag = (typeof REPORT_QUALITY_FLAGS)[number];

export const REPORT_GAP_REASONS = [
  "failed",
  "skipped",
  "budget_exhausted",
] as const;
export type ReportGapReason = (typeof REPORT_GAP_REASONS)[number];

/** P0-A has exactly one gap impact: the section is not in the report (§7.10). */
export const REPORT_GAP_IMPACT_SECTION_MISSING = "section_missing" as const;

export const REPORT_CHARGE_STATES = [
  "reserved",
  "settled",
  "released",
  "orphan",
] as const;
export type ReportChargeState = (typeof REPORT_CHARGE_STATES)[number];

/* ------------------------------------------------------------------ */
/* §12 export sets (model/answer_delivery.go)                         */
/* ------------------------------------------------------------------ */

export const EXPORT_FORMATS = [
  "markdown",
  "html",
  "json",
  "docx",
  "pdf",
  "xlsx",
] as const;
export type ExportFormat = (typeof EXPORT_FORMATS)[number];

/**
 * The formats a *report* delivery may ask for (`validReportExportFormat`).
 *
 * A subset of the model set, and deliberately so: `json` is absent because the
 * frozen payload is already persisted as `rgx_export_snapshot.payload_json`, so a
 * JSON delivery would be a second schema for the same bytes. Listing the subset
 * here rather than inline in the drawer is what keeps the buttons and the server's
 * gate from drifting: the drawer renders exactly this array.
 */
export const REPORT_EXPORT_FORMATS = [
  "markdown",
  "docx",
  "pdf",
  "html",
  "xlsx",
] as const;
export type ReportExportFormat = (typeof REPORT_EXPORT_FORMATS)[number];

export const EXPORT_JOB_STATUSES = [
  "QUEUED",
  "RUNNING",
  "SUCCEEDED",
  "PARTIAL",
  "FAILED",
  "EXPIRED",
  "CANCELLED",
] as const;
export type ExportJobStatus = (typeof EXPORT_JOB_STATUSES)[number];

export const EXPORT_SUBJECTS = ["answer", "report"] as const;
export type ExportSubject = (typeof EXPORT_SUBJECTS)[number];

export const EXPORT_ARTIFACT_STATUSES = [
  "pending",
  "ready",
  "failed",
  "aborted",
  "expired",
  "deleted",
] as const;
export type ExportArtifactStatus = (typeof EXPORT_ARTIFACT_STATUSES)[number];

/* ------------------------------------------------------------------ */
/* §8.8 broadcast events (report_events.go) — 15 values, terminal = 4 */
/* ------------------------------------------------------------------ */

export const REPORT_EVENT_TYPES = [
  "job.queued",
  "outline.ready",
  "outline.confirmed",
  "section.started",
  "section.succeeded",
  "section.failed",
  "job.progress",
  "job.quality_gate",
  "job.assembling",
  "job.finalizing",
  "artifact.ready",
  "job.succeeded",
  "job.partial",
  "job.failed",
  "job.cancelled",
] as const;
export type ReportEventType = (typeof REPORT_EVENT_TYPES)[number];

/** Terminal events are written only by finalize (§8.2); the UI must treat them as the last frame for a job. */
export const REPORT_TERMINAL_EVENTS: readonly ReportEventType[] = [
  "job.succeeded",
  "job.partial",
  "job.failed",
  "job.cancelled",
];

/** The outbox delivery type is NOT broadcastable and must never appear in the SSE stream. */
export const REPORT_OUTBOX_FINALIZE_TYPE = "report_job_finalize";

export const isReportTerminalEvent = (t: string): boolean =>
  (REPORT_TERMINAL_EVENTS as readonly string[]).includes(t);

/* ------------------------------------------------------------------ */
/* §8.7 error code set 44101–44123 (report_codes.go) — 23, contiguous        */
/* 44117–44123 are the assurance codes from doc/301 §7.3, appended not      */
/* renumbered: a shift would silently re-label an existing UI message.      */
/* ------------------------------------------------------------------ */

export interface ReportErrorCodeMeta {
  /** human-readable name, matches the Go constant suffix */
  name: string;
  httpStatus: number;
}

export const REPORT_ERROR_CODES: Record<number, ReportErrorCodeMeta> = {
  44101: { name: "TemplateNotFound", httpStatus: 404 },
  44102: { name: "CapabilityBlocked", httpStatus: 400 },
  44103: { name: "EvidenceSnapshotRejected", httpStatus: 500 },
  44104: { name: "InvalidTransition", httpStatus: 409 },
  44105: { name: "OutlineInvalid", httpStatus: 400 },
  44106: { name: "ExportRequiresPublished", httpStatus: 409 },
  44107: { name: "SectionNotFound", httpStatus: 404 },
  44108: { name: "RevisionNotFound", httpStatus: 404 },
  44109: { name: "AccessDenied", httpStatus: 404 },
  44110: { name: "ActiveJobConflict", httpStatus: 409 },
  44111: { name: "BudgetExhausted", httpStatus: 409 },
  44112: { name: "LedgerInconsistent", httpStatus: 500 },
  44113: { name: "ArtifactVersionConflict", httpStatus: 500 },
  44114: { name: "IdempotencyKeyRequired", httpStatus: 400 },
  44115: { name: "EventCursorExpired", httpStatus: 409 },
  44116: { name: "EvidenceExpired", httpStatus: 409 },
  44117: { name: "RevisionNotAssurable", httpStatus: 400 },
  44118: { name: "AssuranceRunConflict", httpStatus: 409 },
  44119: { name: "AssuranceBudgetExhausted", httpStatus: 409 },
  44120: { name: "FindingNotFound", httpStatus: 404 },
  44121: { name: "AssuranceAckRequired", httpStatus: 409 },
  44122: { name: "AssuranceNotReady", httpStatus: 409 },
  44123: { name: "AssuranceQuotaExhausted", httpStatus: 429 },
};

/**
 * §7.3 note (doc/301): 44123 is the only 429 in the family and the only code that
 * carries a `data.quota` block (`period` / `limit` / `used` / `pending` /
 * `min_call_tokens`). The UI must render the arithmetic, not "quota exceeded":
 * "you have 320 tokens left, one call needs 900" tells the operator what to do.
 */
export interface ReportAssuranceQuota {
  period: string;
  limit: number;
  used: number;
  pending: number;
  min_call_tokens: number;
}

/**
 * §8.7 note: object-level "missing" and "not yours" are BOTH 404/44109 by design —
 * a 403 would leak existence. The UI must render 44109 as "not found / not yours"
 * without distinguishing, and must never treat it as forbidden.
 */

/* ------------------------------------------------------------------ */
/* SSR frame + cursor (§8.3)                                          */
/* ------------------------------------------------------------------ */

/**
 * SSE frame contract (§8.3): the client parses only `data:` lines; each data JSON
 * carries `seq` (monotonic), `type` (from REPORT_EVENT_TYPES) and `payload`. The
 * `id:` field and the in-body `seq` are the SAME number — the client resumes with
 * `after=<seq>` and MUST send it back as the `Last-Event-ID` header.
 */
export const SSE_CURSOR_QUERY_PARAM = "after" as const;

/** Body of 44115 when the cursor fell behind the retention window (§8.3). */
export interface ReportCursorReset {
  /** smallest `seq` that still exists in storage */
  min_seq: number;
  /** the seq of the snapshot the client must re-read */
  event_seq: number;
}

/**
 * Idempotency-Key (header, §7.7): REQUIRED for mutations that mint an aggregate
 * (create / regenerate / retry). Same key ⇒ same aggregate (`created=false`)
 * returned as a success; missing/malformed ⇒ 44114.
 */
export const IDEMPOTENCY_HEADER = "Idempotency-Key" as const;

export const HTTP_ACCEPTED = 202 as const;
