// Report-center view model.
//
// The centre does not invent facts: every number it shows comes from the read
// API, and a ratio the server refused to judge stays `null` here (rendered as
// "暂无判定"). Turning `null` into `0` in the browser would recreate exactly the
// lie the server side was fixed to stop telling (see doc/288 §14.1).

export interface ReportRow {
  id: string;
  title: string;
  question?: string;
  template_key: string;
  /**
   * Lifecycle flag: `active` or `archived` (doc/297 §2). Never a delivery state, and
   * never "deleted" — a deleted report has a `deleted_at` and does not appear here.
   */
  status: string;
  /** Number of the current revision (0/absent when the pointer dangles). */
  revision?: number;
  /** Current revision's delivery state: published / superseded / running / failed / cancelled… */
  revision_status?: string;
  /** `complete` | `partial` for the current revision. */
  completeness?: string;
  published_at?: string | null;
  current_revision_id?: string;
  origin_chat_id?: string;
  origin_session_id?: string;
  created_at: string;
  expires_at: string;
  deleted_at?: string | null;
}

/** One row of `GET /api/v1/report-metrics/trend`. */
export interface TrendResult {
  view: "own" | "tenant" | string;
  bucket: "day" | "week" | string;
  from: string;
  to: string;
  days?: number;
  truncated: boolean;
  buckets: Array<Record<string, unknown>>;
  totals: {
    revisions?: number;
    published?: number;
    quality_judged?: number;
    quality_passed?: number;
    quality_gate_pass_rate?: number | null;
    // doc/301 T10: pooled numerator/denominator of the locator-assisted rate. They
    // travel with the rate so the workspace can print "3/12 引用章节" beside "25%"
    // instead of a percentage whose population is invisible.
    locator_assisted_sections?: number;
    with_citation_sections?: number;
    locator_assisted_section_rate?: number | null;
  /**
   * T23 / §5.8 item 2: pooled counts of sections whose frozen plan is one of the
   * three `fallback_*` states, and of sections that carried a plan at all. Counts,
   * never a ratio — the threshold in §5.8 item 3 is "thin/degraded share", and a
   * share without its denominator cannot be read on a quiet week.
   */
  research_degraded_sections?: number;
  research_planned_sections?: number;
    [key: string]: unknown;
  };
}

// Enum → words.
//
// Three rules, learned the hard way in the 2026-11-18 live pass:
//
//  1. the words live in the dictionary, not in a TypeScript constant: a constant
//     map bypasses `zh.ts`/`en.ts`, so the English build showed Chinese labels and
//     nobody noticed until someone read the screen in English;
//  2. an unknown value is printed raw (`{ _: value }`): inventing a label for a
//     status the backend does not produce is worse than an ugly word;
//  3. one enum has one vocabulary across every surface. The workspace drawer and
//     the reader used to word `evidence_status` differently ("已支持" vs
//     "证据充分"), which reads like two different judgements.
export const REVISION_STATUSES = [
  "draft",
  "queued",
  "awaiting_outline",
  "generating",
  "published",
  "superseded",
  "cancelled",
  "failed",
] as const;
/**
 * `rgx_report.status` — the lifecycle flag (model/report.go `ReportLifecycleStatuses`).
 *
 * `archived` is not a step towards deletion: it takes a finished report out of the
 * daily list and refuses anything that would produce new content, while the delivered
 * revisions stay readable and downloadable (doc/297 §2). This constant used to say
 * `deleted`, a value the server never wrote, so the list's lifecycle column had two
 * members and neither of them was the one archiving introduced.
 */
export const REPORT_LIFECYCLE_STATUSES = ["active", "archived"] as const;

/**
 * The list's `?status=` views. `all` is a *view*, not a stored value: the server
 * answers 44105 to a status column it never writes, and `all` resolves to "no
 * predicate" (doc/297 §2.3). Keeping the two lists apart is what stops `all` from
 * leaking into the lifecycle vocabulary.
 */
export const REPORT_LIST_VIEWS = ["active", "archived", "all"] as const;
export type ReportListView = (typeof REPORT_LIST_VIEWS)[number];
export const COMPLETENESS_VALUES = ["full", "partial"] as const;
export const SECTION_STATUSES = [
  "pending",
  "writing",
  "succeeded",
  "failed",
  "skipped",
  "superseded",
] as const;
export const QUALITY_STATUSES = ["not_checked", "passed", "needs_review", "failed"] as const;
export const EVIDENCE_STATUSES = [
  "not_required",
  "supported",
  "partially_supported",
  "unsupported",
  "conflicting",
  "outdated",
] as const;
export const ARTIFACT_STATUSES = ["ready", "expired", "failed"] as const;

/**
 * `rgx_report_section.gap_reason` — why a section ended without text
 * (model/report.go `ReportGap*`). The reader printed the raw value, so a live run
 * said `这一节未交付: failed` (2026-11-18).
 */
export const GAP_REASONS = ["failed", "skipped", "budget_exhausted"] as const;

/**
 * `rgx_report_revision.gap_json[].impact` — what the hole costs the reader
 * (model/report.go `ReportGapImpact*`). It is a second closed set, not a free-text
 * note: every gap the finalizer writes today says `section_missing`, because the
 * only thing it can honestly promise is that the section is not there.
 */
export const GAP_IMPACTS = ["section_missing"] as const;

/**
 * `rgx_report_job.status` — the run's own lifecycle, which is what the workspace
 * header badge states. It is NOT the revision status: a job `running` can belong to
 * a revision `generating`, and the header used to print the raw word `running`.
 */
export const JOB_STATUSES = [
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

/**
 * R18's research depth, frozen on the revision at creation (doc/295 §2.2).
 * `deep` means the section's own retrieval was widened by a planner; it changes how
 * much is looked at, never what a sentence is allowed to claim.
 */
export const RESEARCH_DEPTHS = ["standard", "deep"] as const;

/**
 * R18's planning state — the closed set the server writes into
 * `research_plan_json.planning`. The three `fallback_*` members are *degradations*,
 * not failures: the section was written from its own retrieval and the disclosure
 * says which upgrade did not happen. Printing one as an error would tell the reader
 * a section failed when it did not.
 */
export const RESEARCH_PLANNING_STATES = [
  "standard",
  "planned",
  "fallback_planner_error",
  "fallback_budget",
  "fallback_unparseable",
] as const;

/** R20 cadence, and what a scheduled run rebuilds. */
export const SCHEDULE_CADENCES = ["daily", "weekly"] as const;
export const SCHEDULE_UPDATE_MODES = ["full", "reuse_static"] as const;

/**
 * R20's `last_status`. `created` is the only success; the four `skipped_*` members
 * are closed-set reasons a due occurrence produced nothing, and each implies a
 * different next step ("wait", "publish a version", "fix the template", "re-enable
 * it") — which is why they are not collapsed into "failed".
 */
export const SCHEDULE_RUN_STATUSES = [
  "created",
  "skipped_busy",
  "skipped_no_base",
  "skipped_template_missing",
  "skipped_disabled",
  "failed",
] as const;

/**
 * Sharing policies (doc/296 §2.1, `rgx_report.access_policy`). The four are the
 * server's closed set, in the order the panel offers them: narrowest first, so
 * "who can read this" is answered by picking from what the report already means.
 */
export const REPORT_ACCESS_POLICIES = [
  "owner_only",
  "tenant_members",
  "selected_users",
  "selected_groups",
] as const;
export const REPORT_GRANT_KINDS = ["user", "team"] as const;

/**
 * The two tenant switches of the report surface (doc/296 §2.2/§2.3).
 *
 * `enforce`/`self` are the documented defaults and the fail-closed direction: a
 * workspace that never opened the switch keeps the old behaviour.
 */
export const REPORT_BUDGET_POLICIES = ["enforce", "observe"] as const;
export const REPORT_AUTO_CONFIRM_POLICIES = ["self", "approval"] as const;

/** The per-request outline modes; `auto` still needs the template's permission. */
export const REPORT_OUTLINE_CONFIRMATIONS = ["required", "auto"] as const;

/**
 * A budget extension either landed or is waiting for a human (doc/296 §2.2).
 *
 * The distinction is the whole point of the endpoint: "waiting for approval" is not
 * a failure and must not be rendered as one, but it also must not look like "the
 * limit was raised" — nothing has changed yet.
 */
export const REPORT_BUDGET_EXTENSION_STATUSES = ["applied", "pending_approval"] as const;

/** R19: a template row is either the platform baseline or a workspace's own fork. */
export const TEMPLATE_SOURCES = ["platform", "workspace"] as const;
export const TEMPLATE_STATUSES = ["active", "retired"] as const;

/** `rgx_report_job.stage` — the progress strip's five stops (model/report.go). */
export const REPORT_STAGES = [
  "outlining",
  "section_retrieval",
  "section_writing",
  "quality_check",
  "assembling",
] as const;

type Translator = (key: string, options?: Record<string, unknown>) => string;

function enumLabel(t: Translator, group: string, value: string): string {
  if (!value) return "";
  return t(`reportCenter.label_${group}.${value}`, { _: value });
}

/** Delivery state of the *current revision* (drawer / history banner / reader). */
export function reportRevisionStatusLabel(t: Translator, status: string): string {
  return enumLabel(t, "revision", status);
}

/** Report-level lifecycle flag: `active` is 在列表中, never "in progress". */
export function reportStatusLabel(t: Translator, status: string): string {
  return enumLabel(t, "lifecycle", status);
}

/**
 * The label of one list view (`active` / `archived` / `all`). It is a *filter*, not a
 * row state, so it has its own words: reusing the row label would make the "全部"
 * button read as a status the report could have.
 */
export function reportListViewLabel(t: Translator, view: string): string {
  switch (view) {
    case "active":
      return t("reportCenter.view_active");
    case "archived":
      return t("reportCenter.view_archived");
    case "all":
      return t("reportCenter.view_all");
  }
  return view;
}

/**
 * Human label for one list row: the current revision's delivery state, with the
 * partial-delivery disclosure kept visible (`已交付（部分）`) rather than folded
 * into `已交付` — a partial report is published, but it is not the same thing.
 * A dangling pointer (the purge can leave one for a janitor tick) falls back to the
 * lifecycle flag.
 */
export function reportDeliveryLabel(
  t: Translator,
  row: { status: string; revision_status?: string; completeness?: string },
): string {
  const status = row.revision_status ?? "";
  if (!status) return reportStatusLabel(t, row.status);
  const label = reportRevisionStatusLabel(t, status);
  if (status === "published" && row.completeness === "partial") {
    return t("reportCenter.label_delivered_partial", { label });
  }
  return label;
}

/** `full` / `partial`. Partial is a disclosure, printed next to the state. */
export function reportCompletenessLabel(t: Translator, completeness: string | null | undefined): string {
  if (!completeness) return "";
  return enumLabel(t, "completeness", completeness);
}

/** Per-section badges: did it run / did it pass the gate / does it have evidence. */
export function reportSectionStatusLabel(t: Translator, status: string): string {
  return enumLabel(t, "section", status);
}

export function reportQualityStatusLabel(t: Translator, status: string): string {
  return enumLabel(t, "quality", status);
}

export function reportEvidenceStatusLabel(t: Translator, status: string): string {
  return enumLabel(t, "evidence", status);
}

/**
 * R11 claim vocabulary (doc/288 §9.2): `fact` / `inference` / `recommendation` /
 * `limitation`. The raw enum never reaches the page — an unknown value falls back to
 * itself so a maintainer can grep it rather than read a plausible guess.
 */
export function reportClaimTypeLabel(t: Translator, claimType: string): string {
  return enumLabel(t, "claim_type", claimType);
}

/**
 * R11 support verdicts. Deliberately a *different* word from the section-level
 * `evidence` labels: `supported` here means the sentence's numbers and dates all
 * appear in the chunks it cites (§9.2) — not that the sentence is true, and not that
 * the section had enough evidence.
 */
export function reportClaimSupportLabel(t: Translator, support: string): string {
  return enumLabel(t, "claim_support", support);
}

/**
 * One claim row as `GET /reports/:id/revisions/:n/sections` hands it over.
 * `markers` are the citation numbers the sentence used, recomputed server-side.
 */
export interface ReportClaimView {
  ord: number;
  claim_text: string;
  claim_type: string;
  support_status: string;
  markers: number[];
}

/** One R16 cross-section disagreement, frozen in the revision's metrics. */
export interface ReportConsistencyIssue {
  kind: string;
  label: string;
  ords: number[];
  values: string[];
}

/**
 * Parses R16's disclosure out of `metrics_json`. `null`/absent means the check was
 * not run (pre-P1 revisions) — render nothing rather than "0 issues found", which
 * would claim a check that never happened. An issue with no values carries no
 * information and is dropped instead of printed as an empty bracket.
 */
export function parseConsistencyIssues(raw: string | undefined): {
  keys_checked: number | null;
  issues: ReportConsistencyIssue[];
} {
  const empty = { keys_checked: null as number | null, issues: [] as ReportConsistencyIssue[] };
  if (!raw || !raw.trim()) return empty;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return empty;
  }
  if (!parsed || typeof parsed !== "object") return empty;
  const source = parsed as Record<string, unknown>;
  const checked = source.consistency_keys_checked;
  const keysChecked = typeof checked === "number" ? checked : null;
  const rawIssues = Array.isArray(source.consistency_issues) ? source.consistency_issues : [];
  const issues: ReportConsistencyIssue[] = [];
  for (const entry of rawIssues) {
    if (!entry || typeof entry !== "object") continue;
    const record = entry as Record<string, unknown>;
    const values = Array.isArray(record.values) ? record.values.map((v) => String(v)) : [];
    if (values.length < 2) continue;
    const ords = Array.isArray(record.ords)
      ? record.ords.map((v) => Number(v)).filter((v) => Number.isFinite(v) && v > 0)
      : [];
    issues.push({
      kind: String(record.kind ?? ""),
      label: String(record.label ?? ""),
      ords,
      values,
    });
  }
  return { keys_checked: keysChecked, issues };
}

/** R18: `standard` / `deep`. */
export function reportResearchDepthLabel(t: Translator, depth: string): string {
  return enumLabel(t, "research_depth", depth);
}

/** R18's planning state, including the three degradations. */
export function reportResearchPlanningLabel(t: Translator, planning: string): string {
  return enumLabel(t, "research_planning", planning);
}

/** R20 cadence: `daily` / `weekly`. */
export function reportScheduleCadenceLabel(t: Translator, cadence: string): string {
  return enumLabel(t, "schedule_cadence", cadence);
}

/** R20 `update_mode`: rebuild everything, or keep the sections that need no evidence. */
export function reportScheduleUpdateModeLabel(t: Translator, mode: string): string {
  return enumLabel(t, "schedule_update_mode", mode);
}

/** R20 `last_status`. An unknown value prints raw so a maintainer can grep it. */
export function reportScheduleRunStatusLabel(t: Translator, status: string): string {
  return enumLabel(t, "schedule_status", status);
}

/** R19 `source`: why a template can (or cannot) be edited in this workspace. */
export function reportTemplateSourceLabel(t: Translator, source: string): string {
  return enumLabel(t, "template_source", source);
}

export function reportTemplateStatusLabel(t: Translator, status: string): string {
  return enumLabel(t, "template_status", status);
}

/** Weekday names, indexed like the server's `weekday` (0 = Sunday). */
export function reportWeekdayLabel(t: Translator, weekday: number): string {
  if (!Number.isInteger(weekday) || weekday < 0 || weekday > 6) return "";
  return t(`reportCenter.weekday.${weekday}`, { _: String(weekday) });
}

/** `09:05`, in the schedule's own wall clock. */
export function formatScheduleClock(hour: number, minute: number): string {
  const h = Number.isFinite(hour) ? Math.min(23, Math.max(0, Math.trunc(hour))) : 0;
  const m = Number.isFinite(minute) ? Math.min(59, Math.max(0, Math.trunc(minute))) : 0;
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}`;
}

/**
 * The UTC offset a schedule runs in, as `UTC+08:00`.
 *
 * The server stores `tz_offset_min` rather than an IANA zone on purpose (doc/295
 * §2.4): an offset has no DST rules to disagree about, and a plan that silently
 * shifts by an hour twice a year is worse than one that is honestly fixed.
 */
export function formatTZOffset(tzOffsetMin: number): string {
  const minutes = Number.isFinite(tzOffsetMin) ? Math.trunc(tzOffsetMin) : 0;
  const sign = minutes < 0 ? "-" : "+";
  const absolute = Math.abs(minutes);
  const hours = String(Math.floor(absolute / 60)).padStart(2, "0");
  const rest = String(absolute % 60).padStart(2, "0");
  return `UTC${sign}${hours}:${rest}`;
}

/** `ready` / `expired` / `failed` — the download state of one delivered file. */
export function reportArtifactStatusLabel(t: Translator, status: string): string {
  return enumLabel(t, "artifact", status);
}

export function reportGapReasonLabel(t: Translator, reason: string): string {
  return enumLabel(t, "gap", reason);
}

export function reportGapImpactLabel(t: Translator, impact: string): string {
  return enumLabel(t, "gap_impact", impact);
}

export function reportJobStatusLabel(t: Translator, status: string): string {
  return enumLabel(t, "job", status);
}

export function reportStageLabel(t: Translator, stage: string): string {
  return enumLabel(t, "stage", stage);
}

/**
 * Renders a ratio the server may have declined to compute.
 * `null`/`undefined` ⇒ 暂无判定; never "0%".
 */
export function formatRatio(t: Translator, value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) {
    return t("reportCenter.ratio_unavailable");
  }
  return `${Math.round(value * 100)}%`;
}

/** Buckets that actually carry data, in ascending time order. */
export function trendPoints(result: TrendResult | null): Array<{ key: string; revisions: number; published: number }> {
  if (!result || !Array.isArray(result.buckets)) return [];
  return result.buckets
    .map((bucket) => ({
      // The server keys a bucket by its start date (`date`), not by a label.
      key: String(bucket.date ?? bucket.bucket ?? ""),
      revisions: Number(bucket.revisions ?? 0),
      published: Number(bucket.published ?? 0),
    }))
    .filter((point) => point.key !== "")
    .sort((a, b) => a.key.localeCompare(b.key));
}

/** Where "open" goes: the conversation that authored the report. */
export function reportOpenPath(report: ReportRow): string | null {
  if (!report.origin_chat_id) return null;
  const params = new URLSearchParams({ kind: "chat", targetId: report.origin_chat_id });
  if (report.origin_session_id) params.set("contextId", report.origin_session_id);
  return `/conversation-center?${params.toString()}`;
}

/**
 * R17's locators, as the read API sends them: already-rendered display lines such as
 * 「页面索引：差旅报销制度」. They are navigation, never evidence, and the server
 * deliberately sends no score, excerpt or chunk id with them (doc/295 A55) — so
 * there is nothing here a reader could mistake for a citation, and nothing that
 * could be joined back onto an evidence row.
 */
export function reportSectionLocationHints(section: { location_hints?: unknown }): string[] {
  const raw = section.location_hints;
  if (!Array.isArray(raw)) return [];
  return raw
    .map((entry) => (typeof entry === "string" ? entry.trim() : ""))
    .filter((entry) => entry !== "");
}

/** R18's `research_plan` on a section read; `null` means "no plan was attempted". */
export interface ReportResearchPlan {
  depth: string;
  planning: string;
  merged: number;
  duplicates: number;
}

/**
 * Reads a section's research disclosure. A missing or malformed value reads as "no
 * plan": the disclosure exists to explain the body, so a bad one must not hide it.
 */
export function reportSectionResearchPlan(section: { research_plan?: unknown }): ReportResearchPlan | null {
  const raw = section.research_plan;
  if (!raw || typeof raw !== "object") return null;
  const record = raw as Record<string, unknown>;
  const depth = String(record.depth ?? "");
  const planning = String(record.planning ?? "");
  if (!depth && !planning) return null;
  return {
    depth,
    planning,
    merged: Number(record.merged ?? 0) || 0,
    duplicates: Number(record.duplicates ?? 0) || 0,
  };
}

/**
 * Whether a plan is worth printing. A `standard` section is the default and its
 * disclosure says nothing a reader needs; `planned` and every `fallback_*` are
 * disclosures, because they state that extra retrieval did or did not happen.
 */
export function shouldShowResearchPlan(plan: ReportResearchPlan | null): boolean {
  if (!plan) return false;
  if (plan.depth === "deep") return true;
  return plan.planning !== "" && plan.planning !== "standard";
}

/** R20: `GET/PUT /reports/:id/schedule` — at most one plan per report. */
export interface ReportScheduleView {
  report_id: string;
  cadence: string;
  hour: number;
  minute: number;
  weekday: number;
  tz_offset_min: number;
  update_mode: string;
  enabled: boolean;
  next_run_at: string;
  last_run_at: string | null;
  last_revision: number;
  last_status: string;
  last_error: string;
  created_at: string;
  updated_at: string;
}

/** The writable half of a schedule; an omitted field keeps its stored value. */
export interface ReportSchedulePatch {
  cadence?: string;
  hour?: number;
  minute?: number;
  weekday?: number;
  tz_offset_min?: number;
  update_mode?: string;
  enabled?: boolean;
}

/**
 * One line a reader can act on: `每天 09:00（UTC+08:00）` / `每周三 09:00（UTC+08:00）`.
 *
 * The clock is the plan's own wall clock, not the browser's: a schedule runs where it
 * was authored, and rendering it in the reader's local time would show a different
 * hour to every colleague looking at the same plan.
 */
export function reportScheduleWhen(t: Translator, view: ReportScheduleView): string {
  const clock = formatScheduleClock(view.hour, view.minute);
  const offset = formatTZOffset(view.tz_offset_min);
  if (view.cadence === "weekly") {
    return t("reportCenter.schedule_weekly_at", {
      weekday: reportWeekdayLabel(t, view.weekday),
      clock,
      offset,
    });
  }
  return t("reportCenter.schedule_daily_at", { clock, offset });
}

/** R19: a template row as `GET /report-templates` sends it (the fields the panel reads). */
export interface ReportTemplateRow {
  template_id: string;
  template_key: string;
  version: number;
  tenant_id: string;
  payload: { name?: { zh?: string; en?: string }; [key: string]: unknown };
  status: string;
  retired: boolean;
  source: string;
  editable: boolean;
  source_template_id?: string;
  source_template_version?: number;
  /**
   * Versions of this key, counted by the service. The list is one row per key, so
   * the row count is always 1 and cannot answer "how many versions exist".
   * Absent on publish/retire answers and on an older server.
   */
  versions?: number;
}

/**
 * The name a template should be called on screen, in the reading language, with the
 * key as the last resort: a template with no name is still addressable, and showing
 * its key is honest where an empty cell is not.
 */
export function reportTemplateName(row: ReportTemplateRow, language: string): string {
  const name = row.payload?.name ?? {};
  const preferred = language.startsWith("zh") ? name.zh || name.en : name.en || name.zh;
  return (preferred || row.template_key || "").trim();
}

/**
 * Why the panel offers no edit button; `null` means "editable here".
 *
 * The reason is a sentence, not a disabled button: a platform baseline looks exactly
 * like a workspace template in a list, and "cannot edit" with no explanation reads as
 * a permission bug.
 */
export function reportTemplateEditBlocker(t: Translator, row: ReportTemplateRow): string | null {
  if (row.editable) return null;
  if (row.source === "platform") return t("reportCenter.template_platform_readonly");
  if (row.retired) return t("reportCenter.template_retired_readonly");
  return t("reportCenter.template_readonly");
}

/**
 * One named grantee of a shared report (doc/296 §2.1).
 *
 * `missing` is set when the id no longer resolves — a deleted user or team. The
 * grant row stays (revoking it silently would change the policy behind the owner's
 * back), so the panel says "已失效" instead of printing the id.
 */
export interface ReportAccessGrantee {
  kind: string;
  id: string;
  name: string;
  missing: boolean;
}

/** The sharing state carried by `GET /reports/:id` (`ReportAccessView`). */
export interface ReportAccessView {
  policy: string;
  /** Not a permission: the write route re-checks. It gates the editor UI. */
  is_owner: boolean;
  can_manage: boolean;
  grantees: ReportAccessGrantee[];
}

/**
 * The share editor's payload. Both lists travel together so "switch from user A to
 * team B" is one write: two writes would leave a window where neither applies.
 */
export interface SetReportAccessRequest {
  policy: string;
  user_ids?: string[];
  team_ids?: string[];
}

/**
 * The live run's spend, as `GET /reports/:id` carries it (`ReportDetailBudget`).
 *
 * `max_extension_tokens` is the server's bound, not a browser rule: "at most the
 * current envelope again" is a governance decision and a second copy of it in
 * TypeScript would be a second thing to keep true.
 */
export interface ReportDetailBudget {
  budget_tokens: number;
  spent_tokens: number;
  available_tokens: number;
  max_extension_tokens: number;
  budget_policy: string;
  observing: boolean;
}

/**
 * A budget extension either landed (`applied`) or waits for a human
 * (`pending_approval`); `added_tokens` is the *increment*, and `budget_tokens` is
 * the envelope after it.
 */
export interface ReportBudgetExtensionView {
  status: string;
  job_id?: string;
  added_tokens: number;
  budget_tokens: number;
  approval_id?: string;
}

export interface ReportBudgetExtensionRequest {
  tokens: number;
  reason?: string;
}

/** Whether the policy is one of the two that name grantees. */
export function reportAccessPolicyTakesUsers(policy: string): boolean {
  return policy === "selected_users";
}

export function reportAccessPolicyTakesTeams(policy: string): boolean {
  return policy === "selected_groups";
}

/** `owner_only` / `tenant_members` / … in words, never the raw value. */
export function reportAccessPolicyLabel(t: Translator, policy: string): string {
  return enumLabel(t, "access_policy", policy);
}

export function reportAccessGrantKindLabel(t: Translator, kind: string): string {
  return enumLabel(t, "grant_kind", kind);
}

/**
 * What to print for one grantee: the resolved name, or an explicit "已失效".
 *
 * Printing the bare id would leak a machine identifier into the UI and would look
 * like a bug; printing an empty string would hide that the share still exists.
 */
export function reportAccessGranteeLabel(t: Translator, grantee: ReportAccessGrantee): string {
  const name = (grantee.name ?? "").trim();
  if (grantee.missing || name === "") return t("reportCenter.access_grantee_missing");
  return name;
}

export function reportBudgetPolicyLabel(t: Translator, policy: string): string {
  return enumLabel(t, "budget_policy", policy);
}

export function reportAutoConfirmPolicyLabel(t: Translator, policy: string): string {
  return enumLabel(t, "auto_confirm_policy", policy);
}

export function reportOutlineConfirmationLabel(t: Translator, mode: string): string {
  return enumLabel(t, "outline_confirmation", mode);
}

export function reportBudgetExtensionStatusLabel(t: Translator, status: string): string {
  return enumLabel(t, "budget_extension", status);
}

/** True when the request is still waiting for an approver — not a failure. */
export function reportBudgetExtensionPending(view: ReportBudgetExtensionView | null): boolean {
  return view?.status === "pending_approval";
}

/**
 * The largest increment one request may ask for: the current envelope itself.
 *
 * The server enforces it (`reportBudgetExtensionMaxFactor = 1`, so a template's
 * envelope can at most double). Repeating the rule here lets the form say "最多一次加
 * 当前额度的一倍" instead of sending a request that will be refused.
 */
export function reportBudgetExtensionMaxFor(budget: number): number {
  return budget > 0 ? budget : 0;
}

/** One delivered file of the current revision (`ReportArtifactView`). */
export interface ArtifactView {
  id: string;
  format: string;
  artifact_version: number;
  artifact_status: string;
  byte_size: number;
  filename?: string;
  sha256?: string;
  created_at: string;
  expires_at: string;
}

export interface ReportRevisionRow {
  id: string;
  revision: number;
  status: string;
  completeness: string;
  metrics_json?: string;
  gap_json?: string;
  published_at?: string | null;
  created_at: string;
  error_code?: string;
}

/** `GET /api/v1/reports/:id` — the read aggregate the detail drawer shows. */
/**
 * One row of the version history (`GET /reports/:id/revisions`, doc/288 §7.10).
 *
 * `is_current` is the report's own pointer, not "the newest row": a regenerate that
 * is still running is newer than the published base, and marking the attempt as
 * current is how a reader ends up believing their published report is a draft.
 */
export interface ReportRevisionSummaryRecord {
  id: string;
  revision: number;
  status: string;
  completeness: string;
  template_key: string;
  template_version: number;
  published_at: string | null;
  superseded_at: string | null;
  created_at: string;
  is_current: boolean;
}

export interface ReportDetailRecord {
  report: ReportRow;
  revision: ReportRevisionRow;
  evidence_verifiable: boolean;
  evidence_expired_at?: string | null;
  artifacts: Array<{ artifact: ArtifactView; downloadable: boolean }>;
  /**
   * Sharing state (doc/296 §2.1). It rides on the detail read rather than a separate
   * `GET /reports/:id/access` because a reader who cannot see the policy cannot tell
   * whether a colleague may open the report — and an `undefined` here means an older
   * server, which the panel renders as "不可用" instead of guessing `owner_only`.
   */
  access?: ReportAccessView;
  /**
   * The spend of the live run (doc/296 §2.2). Absent means there is nothing to
   * extend — no job, or one that already finalized — which the card states rather
   * than rendering a form the server would answer 44104.
   */
  budget?: ReportDetailBudget;
}

/** One disclosed gap (`gap_json` entry). */
export interface ReportGap {
  section_key: string;
  ord: number;
  title?: string;
  reason: string;
  impact?: string;
  error_code?: string;
}

/**
 * Parses `metrics_json` defensively: a hand-edited or half-written blob must
 * degrade to "no metrics", not crash the drawer. Values stay as the server wrote
 * them, `null` included — a ratio that was never judged must not become 0 here.
 */
export function parseMetrics(raw: string | undefined): Record<string, number | null> {
  if (!raw || !raw.trim()) return {};
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object") return {};
    const out: Record<string, number | null> = {};
    for (const [key, value] of Object.entries(parsed as Record<string, unknown>)) {
      if (value === null) out[key] = null;
      else if (typeof value === "number") out[key] = value;
    }
    return out;
  } catch {
    return {};
  }
}

/** Parses `gap_json`; an unreadable blob means "no disclosed gaps", not a crash. */
export function parseGaps(raw: string | undefined): ReportGap[] {
  if (!raw || !raw.trim()) return [];
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed
      .filter((entry): entry is Record<string, unknown> => !!entry && typeof entry === "object")
      .map((entry) => ({
        section_key: String(entry.section_key ?? ""),
        ord: Number(entry.ord ?? 0),
        title: entry.title === undefined ? undefined : String(entry.title),
        reason: String(entry.reason ?? ""),
        impact: entry.impact === undefined ? undefined : String(entry.impact),
        error_code: entry.error_code === undefined ? undefined : String(entry.error_code),
      }))
      .sort((a, b) => a.ord - b.ord);
  } catch {
    return [];
  }
}

/** Download URL for a delivered file; auth rides the session cookie. */
export function artifactDownloadPath(reportId: string, revisionNo: number, artifactId: string): string {
  return `/api/v1/reports/${encodeURIComponent(reportId)}/revisions/${revisionNo}/artifacts/${encodeURIComponent(artifactId)}/download`;
}

/** Human-readable size; `undefined` when the server reported none. */
export function formatBytes(size: number | undefined): string {
  if (!size || size <= 0) return "—";
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

/** `GET/PUT /api/v1/report-retention` (T20b) — effective clocks plus the inherited ones. */
export interface RetentionView {
  tenant_id: string;
  report_days: number;
  report_evidence_days: number;
  report_artifact_days: number;
  report_event_days: number;
  report_cleanup_hold_days: number;
  system_report_days: number;
  system_report_evidence_days: number;
  system_report_artifact_days: number;
  system_report_event_days: number;
  system_report_cleanup_hold_days: number;
  /**
   * The two governance switches (doc/296 §2.2/§2.3). They live on this payload
   * because they are the same kind of decision as the clocks: one workspace-wide
   * answer an operator writes once.
   */
  budget_policy: string;
  auto_confirm_policy: string;
  system_budget_policy: string;
  system_auto_confirm_policy: string;
  overridden: string[];
  updated_by?: string;
  updated_at?: string;
}

/**
 * The five clocks, with their JSON names, the inherited counterpart the view
 * carries, and the bounds the server enforces (`validateReportTTLDays`).
 *
 * The bounds live here as well so the UI refuses a value before the round trip;
 * the server stays the authority (it answers 44105 for anything invalid, and a
 * client that only checked would still have to render that).
 */
export const RETENTION_FIELDS = [
  { key: "report_days", inherited: "system_report_days", min: 1, max: 3650 },
  { key: "report_evidence_days", inherited: "system_report_evidence_days", min: 1, max: 3650 },
  { key: "report_artifact_days", inherited: "system_report_artifact_days", min: 1, max: 3650 },
  { key: "report_event_days", inherited: "system_report_event_days", min: 1, max: 3650 },
  // A zero-day recovery window is a legal governance choice ("no undo"), so this
  // one field allows 0 — matching the server, where `allowZero` is set for it
  // alone.
  { key: "report_cleanup_hold_days", inherited: "system_report_cleanup_hold_days", min: 0, max: 365 },
] as const;

export type RetentionKey = (typeof RETENTION_FIELDS)[number]["key"];

/** Only the pinned fields — an omitted clock keeps inheriting the system value. */
export type RetentionPatch = Partial<Record<RetentionKey, number>>;

/**
 * Builds the PUT body from the edited values.
 *
 * A clock the caller left blank is *omitted*, which the server reads as "inherit".
 * Sending the inherited number instead would pin it, and a later system change
 * would silently stop applying to this workspace — the exact thing "继承" means
 * it should keep doing.
 */
export function retentionPatch(edited: Partial<Record<RetentionKey, string>>): RetentionPatch {
  const patch: RetentionPatch = {};
  for (const field of RETENTION_FIELDS) {
    const raw = edited[field.key];
    if (raw === undefined || raw.trim() === "") continue;
    const value = Number(raw);
    if (!Number.isInteger(value) || value < field.min || value > field.max) {
      throw new RangeError(`${field.key} out of range`);
    }
    patch[field.key] = value;
  }
  return patch;
}
