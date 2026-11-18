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
  status: string;
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
    [key: string]: unknown;
  };
}

// Only `active` is written today: a report is never archived in place, it is
// soft-deleted and later purged. Unknown values are shown raw rather than
// dressed up in a label the backend does not produce.
const REPORT_STATUS_LABEL: Record<string, string> = { active: "进行中" };

/** Human label for the report-level status (not the revision status). */
export function reportStatusLabel(status: string): string {
  return REPORT_STATUS_LABEL[status] ?? status;
}

/**
 * Renders a ratio the server may have declined to compute.
 * `null`/`undefined` ⇒ "暂无判定"; never "0%".
 */
export function formatRatio(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return "暂无判定";
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
