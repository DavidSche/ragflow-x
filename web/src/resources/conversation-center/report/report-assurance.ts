/**
 * Assurance surface types (doc/301 §5–§7, T08/T09/T12).
 *
 * Mirrors the Go projection in `internal/service/report_assurance_review.go`. Three
 * rules shape everything here, and each one is a place the UI could otherwise lie:
 *
 * 1. `release_eligibility` and `requested_policy` are two fields. A `queued` run has
 *    a policy and NO tier — `pending` is not "we forgot", it is "nothing has been
 *    measured yet" (§5.5).
 * 2. A rate is `number | null`. `null` means "no denominator", which is different
 *    from 0 and must render differently: "0% coverage" is a verdict, "—" is the
 *    absence of one (§4.3).
 * 3. `quote_status` and `support_verdict` are separate. `verified` means the quoted
 *    text exists in the snapshot; it never means the claim is supported (§5.1).
 *
 * The closed sets are re-declared here only as *types*; the values must match the Go
 * constants (`internal/model/report_assurance.go`) and `report-assurance.test.ts`
 * pins the lists so a value added on one side fails on the other.
 */

/** `rgx_report_assurance_run.status` (report_assurance.go). */
export const ASSURANCE_RUN_STATUSES = [
  "queued",
  "running",
  "succeeded",
  "degraded",
  "failed",
  "cancelled",
] as const;
export type AssuranceRunStatus = (typeof ASSURANCE_RUN_STATUSES)[number];

/** `ReportAssuranceTerminal`: the four statuses after which no verdict will change. */
export const ASSURANCE_TERMINAL_STATUSES: readonly AssuranceRunStatus[] = [
  "succeeded",
  "degraded",
  "failed",
  "cancelled",
];
export const isAssuranceTerminal = (status: string): boolean =>
  (ASSURANCE_TERMINAL_STATUSES as readonly string[]).includes(status);

/** Run modes; `citation_audit` is the offline sample and writes no report-domain rows. */
export const ASSURANCE_MODES = ["semantic", "citation_audit", "full"] as const;
export type AssuranceMode = (typeof ASSURANCE_MODES)[number];

/** `requested_policy` — what the run was allowed to aim at, frozen at creation. */
export const ASSURANCE_POLICIES = ["off", "advisory", "required"] as const;
export type AssurancePolicy = (typeof ASSURANCE_POLICIES)[number];

/** `release_eligibility` — what the run actually established. */
export const ASSURANCE_ELIGIBILITIES = [
  "pending",
  "tier_0",
  "tier_1",
  "tier_2",
  "blocked",
] as const;
export type AssuranceEligibility = (typeof ASSURANCE_ELIGIBILITIES)[number];

/** Claim verdicts (§5.2). `abstained` is deliberately NOT in the scored set. */
export const ASSURANCE_VERDICTS = [
  "supported",
  "partially_supported",
  "unsupported",
  "conflicting",
  "outdated",
  "insufficient_evidence",
  "abstained",
  "not_evaluated",
  "not_required",
] as const;
export type AssuranceVerdict = (typeof ASSURANCE_VERDICTS)[number];

/** Verdicts that count as "a fact judgement was produced" (Go `ReportVerdictScored`). */
export const ASSURANCE_SCORED_VERDICTS: readonly AssuranceVerdict[] = [
  "supported",
  "partially_supported",
  "unsupported",
  "conflicting",
  "outdated",
  "insufficient_evidence",
];

/** Quote traceability (§5.1.1). Existence only — never a support verdict. */
export const QUOTE_STATUSES = ["verified", "not_found", "ambiguous", "not_provided"] as const;
export type QuoteStatus = (typeof QUOTE_STATUSES)[number];

/** Finding statuses (§7.3) and the transitions the server accepts. */
export const FINDING_STATUSES = ["open", "acknowledged", "dismissed", "resolved"] as const;
export type FindingStatus = (typeof FINDING_STATUSES)[number];

/**
 * The closed transition table (§7.3), duplicated here so the buttons are disabled
 * for an illegal move instead of the request failing at 44104. `dismissed` and
 * `resolved` are terminal: a false positive that turns out to be real is a *new*
 * finding from a new run, not a reopened one.
 */
export const FINDING_TRANSITIONS: Record<FindingStatus, readonly FindingStatus[]> = {
  open: ["acknowledged", "dismissed"],
  acknowledged: ["resolved", "dismissed"],
  dismissed: [],
  resolved: [],
};

export const canTransitionFinding = (from: string, to: string): boolean =>
  (FINDING_TRANSITIONS[from as FindingStatus] ?? []).includes(to as FindingStatus);

/** Finding categories (§5.3.2) — `contradiction` and `source_conflict` need different fixes. */
export const FINDING_CATEGORIES = [
  "missing_evidence",
  "unsupported",
  "contradiction",
  "source_conflict",
  "overreach",
  "omission",
  "numeric_mismatch",
  "structure",
  "outdated",
  "traceability",
  "quote_unverifiable",
  "disputed",
] as const;
export type FindingCategory = (typeof FINDING_CATEGORIES)[number];

export const FINDING_SEVERITIES = ["high", "medium", "low"] as const;
export type FindingSeverity = (typeof FINDING_SEVERITIES)[number];

/** Independence scale (§5.4), strongest first. */
export const INDEPENDENCE_LEVELS = [
  "independent_provider",
  "different_model_family",
  "same_model_family",
  "same_model",
  "unknown",
] as const;
export type IndependenceLevel = (typeof INDEPENDENCE_LEVELS)[number];

/* ------------------------------------------------------------------ */
/* API shapes                                                         */
/* ------------------------------------------------------------------ */

/** One row of `GET .../assurance-runs` (Go `ReportAssuranceRunView`). */
export interface AssuranceRun {
  id: string;
  report_id: string;
  revision_id: string;
  revision: number;
  status: AssuranceRunStatus;
  mode: AssuranceMode;
  requested_policy: AssurancePolicy;
  /** `pending` until the run ends. There is no tier on a queued run. */
  release_eligibility: AssuranceEligibility;
  method_version: string;
  independence: IndependenceLevel;
  review_model?: string;
  generation_model?: string;

  total_claims: number;
  evaluated: number;
  not_required: number;
  not_evaluated: number;
  abstained: number;
  structure_pass: number;
  structure_fail: number;

  budget_tokens: number;
  min_call_tokens: number;
  used_tokens: number;
  budget_exhausted: boolean;
  quota_period?: string;
  quota_settled_tokens: number;

  policy?: AssurancePolicyResolution;
  metrics?: Record<string, unknown>;
  failure_code?: string;
  failure_reason?: string;

  triggered_by?: string;
  created_at: string;
  started_at?: string | null;
  finished_at?: string | null;
  /** true when the response was answered from the Idempotency-Key, not a new run. */
  replayed: boolean;
}

/** The frozen policy of a run, per field (§7.6 rule 1: the resolver says where it won). */
export interface AssurancePolicyResolution {
  gate: AssurancePolicy;
  scan_top_k: number;
  budget_tokens: number;
  policy_source: Record<string, string>;
  assurance_model?: string;
  assurance_review_model?: string;
}

/** One claim row inside the review projection. */
export interface ReviewClaim {
  id: string;
  ord: number;
  claim_text: string;
  claim_type?: string;
  support_verdict: AssuranceVerdict;
  quote_status: QuoteStatus | "";
  structure_verdict?: string;
  relevance?: string;
  rationale?: string;
  quote_text?: string;
  quote_span_end?: number;
  matched_snapshot_id?: string;
  abstain_reason?: string;
  not_evaluated_reason?: string;
  insufficient_reason?: string;
  confidence?: number | null;
  cache_hit: boolean;
  finding_ids?: string[];
}

export interface ReviewSection {
  ord: number;
  section_key: string;
  title: string;
  requires_evidence: boolean;
  evidence_status?: string;
  /**
   * The *writing-time* retrieval pool (doc/301 §5.8 item 1). All three are optional
   * and never defaulted: `undefined` means "this revision did not record it", which
   * is not the same statement as `0` (= the pool was empty). A reviewer deciding
   * whether a weak claim is the writer's fault or the corpus's needs that difference,
   * and a `?? 0` here would erase it.
   */
  pool_hit_chunks?: number;
  pool_insufficient?: boolean;
  pool_candidate_count?: number;
  /** Frozen plan state: `standard` / `planned` / `fallback_*` (§5.8 item 2). */
  research_planning?: string;
  research_depth?: string;
  claims: ReviewClaim[];
  next_claim_cursor?: string;
}

export interface ReviewFinding {
  id: string;
  section_ord: number;
  claim_id?: string;
  source: string;
  category: FindingCategory;
  severity: FindingSeverity;
  status: FindingStatus;
  title?: string;
  detail?: string;
  suggested_action?: string;
  resolved_revision_id?: string;
  reviewed_by?: string;
  reviewed_at?: string | null;
  review_note?: string;
  created_at: string;
}

export interface ReviewCounters {
  total_claims: number;
  denominator: number;
  evaluated: number;
  supported: number;
  partially_supported: number;
  unsupported: number;
  conflicting: number;
  insufficient_evidence: number;
  abstained: number;
  not_evaluated: number;
  not_required: number;
  high_findings: number;
  open_findings: number;
  not_required_share: number | null;
  denominator_small: boolean;
  coverage: number | null;
}

/** `GET .../review` (Go `ReportReviewView`). */
export interface ReportReview {
  report_id: string;
  revision_id: string;
  revision: number;
  revision_status: string;
  run?: AssuranceRun;
  release_eligibility: AssuranceEligibility;
  release_eligibility_basis: string[];
  requested_policy: AssurancePolicy;
  acknowledgeable: boolean;
  acknowledged: boolean;
  counters: ReviewCounters;
  sections: ReviewSection[];
  findings: ReviewFinding[];
  next_section_cursor?: string;
  next_finding_cursor?: string;
  truncated?: string[];
  /**
   * What the review compared against (doc/301 §5.3.1). `cited_only` means no
   * cross-document contrast happened at all, and the surface must say so: "no
   * conflict found" would otherwise read as "checked against other documents".
   */
  scan_scope: string;
  /** How many uncited candidates this revision froze (0 when none were frozen). */
  candidate_count: number;
  /**
   * Owner-only content switch, stated by the server rather than inferred from
   * `candidates.length === 0` — "not your content" and "retention purged them" are
   * different facts and must not render as the same empty list.
   */
  candidates_visible: boolean;
  candidates?: ReviewCandidate[];
  candidates_truncated?: boolean;
}

/**
 * One uncited candidate that took part in the contrast.
 *
 * Deliberately no `chunk_id`, no `document_id` and no score: a candidate is not
 * evidence, and a projection that carried an identifier would be sorted next to a
 * citation by some renderer (§5.3.1). `document_label` is resolved from this
 * revision's own citation rows, so it can be absent for a document cited nowhere.
 */
export interface ReviewCandidate {
  section_ord: number;
  pool_rank: number;
  document_label?: string;
  content?: string;
  truncated?: boolean;
}

/* ------------------------------------------------------------------ */
/* Formatting helpers (pure — the panel must not invent numbers)       */
/* ------------------------------------------------------------------ */

/** Percent with one decimal, or an em dash for a null rate. `null ≠ 0`. */
/**
 * A rate, or the dictionary's word for "there is no denominator".
 *
 * The translator is required rather than optional on purpose: an optional one would be
 * omitted at exactly the call sites people add later, and those are the ones that would
 * print a bare dash. (doc/301 §5/§8 — a missing field must read `not_available`, not a
 * silent blank, and never 0%.)
 */
export const formatRate = (rate: number | null | undefined, t: Translate): string =>
  rate === null || rate === undefined || Number.isNaN(rate)
    ? t(assuranceKey("value_not_recorded"))
    : `${(rate * 100).toFixed(1)}%`;

/** The react-admin translator (`useTranslate()`), narrowed to what we call. */
export type Translate = (key: string, options?: Record<string, unknown>) => string;

/** One prefix, so a message key cannot be spelled two ways. */
const assuranceKey = (suffix: string): string => `conversationCenter.assurance.${suffix}`;

/**
 * Looks a message up, falling back to the raw server value.
 *
 * A closed set that grows on the server must render *something* recognisable rather
 * than a blank badge: showing `source_conflict` is ugly but true, while showing
 * "未知" hides which value arrived.
 */
const labelOr = (t: Translate, key: string, fallback: string): string =>
  t(key, { _: fallback });

/**
 * The one-line reason a revision is not release-eligible.
 *
 * `pending` is worded as "not measured yet" rather than "not eligible": a queued run
 * has not failed anything, and telling a reader it did is the exact confusion
 * `release_eligibility=pending` exists to prevent (§5.5).
 */
export const describeEligibility = (
  t: Translate,
  eligibility: AssuranceEligibility,
  basis: readonly string[],
): string => {
  switch (eligibility) {
    case "pending":
    case "tier_0":
    case "tier_1":
    case "tier_2":
      return t(assuranceKey(`eligibility_${eligibility}`));
    case "blocked":
      // The basis words themselves are server-computed reason codes; they are shown
      // verbatim under the tier, and joining them here keeps the sentence readable
      // when the list is collapsed.
      return basis.length > 0
        ? t(assuranceKey("eligibility_blocked_with_reasons"), {
            reasons: basis.join(t(assuranceKey("basis_separator"))),
          })
        : t(assuranceKey("eligibility_blocked"));
    default:
      return t(assuranceKey("eligibility_unknown"));
  }
};

/**
 * `not_required_share` above the ceiling means the coverage figure rests on a small
 * denominator (§6.3 invariant 9). The UI must say so next to the number, not below it.
 */
export const DENOMINATOR_SMALL_CEILING = 0.4;

export const coverageCaveat = (t: Translate, counters: ReviewCounters): string | null =>
  counters.denominator_small
    ? t(assuranceKey("coverage_caveat"), { share: formatRate(counters.not_required_share, t) })
    : null;

/**
 * 44123 carries the arithmetic, so the UI can explain it (§7.3). Returns null for any
 * other code: inventing a quota message for a different failure is worse than silence.
 */
export const describeQuotaShortfall = (
  t: Translate,
  errorCode: number,
  quota?: { period: string; limit: number; used: number; pending: number; min_call_tokens: number },
): string | null => {
  if (errorCode !== 44123 || !quota) return null;
  return t(assuranceKey("quota_shortfall"), {
    period: quota.period,
    remaining: Math.max(0, quota.limit - quota.used - quota.pending),
    min: quota.min_call_tokens,
    limit: quota.limit,
    used: quota.used,
    pending: quota.pending,
  });
};

/**
 * Quote status wording. `ambiguous` is shown as "无法唯一定位" and NOT as a highlight:
 * guessing which of two identical sentences the report meant is how a review UI
 * points a reader at the wrong paragraph (§5.1.1).
 */
export const describeQuoteStatus = (t: Translate, status: QuoteStatus | ""): string => {
  switch (status) {
    case "verified":
    case "not_found":
    case "ambiguous":
    case "not_provided":
      return t(assuranceKey(`quote_${status}`));
    default:
      return t(assuranceKey("quote_unchecked"));
  }
};

/** The nine verdicts, keyed by value so a new one lands as the raw enum. */
export const verdictLabel = (t: Translate, verdict: string): string =>
  labelOr(t, `conversationCenter.verdict.${verdict}`, verdict);

export const findingCategoryLabel = (t: Translate, category: string): string =>
  labelOr(t, `conversationCenter.finding_category.${category}`, category);

export const findingStatusLabel = (t: Translate, status: string): string =>
  labelOr(t, `conversationCenter.finding_status.${status}`, status);

export const severityLabel = (t: Translate, severity: string): string =>
  labelOr(t, `conversationCenter.severity.${severity}`, severity);

/**
 * Whether the workbench may show a highlight for a claim. Only `verified` with a
 * span qualifies; a `partially_supported` verdict is not a reason to highlight.
 */
export const canHighlightQuote = (claim: ReviewClaim): boolean =>
  claim.quote_status === "verified" && (claim.quote_span_end ?? 0) > 0 && Boolean(claim.matched_snapshot_id);
