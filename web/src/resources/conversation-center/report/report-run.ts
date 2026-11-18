/**
 * Report run runtime (doc/288 T8 backbone + T12 "刷新仍在").
 *
 * Pure helpers and types for the report workspace: snapshot shapes from the Go
 * API, the SSE frame parser, the job event poller, and context builders. No React
 * in here — every function is unit-testable against a mocked fetch/api.
 *
 * The closed sets come from `report-contract.ts` (T0); this module must never
 * re-declare a status string.
 */
import type { ConversationContext } from "../types";
import { isReportTerminalEvent } from "./report-contract";

// ---------------------------------------------------------------------------
// API shapes (mirrors Go `internal/service/report_stream.go` + `report_read.go`)
// ---------------------------------------------------------------------------

export interface ReportRecord {
  id: string;
  title: string;
  question: string;
  template_key: string;
  status: string;
  access_policy: string;
  current_revision_id: string;
  created_at: string;
  updated_at: string;
}

export interface ReportRevisionRecord {
  id: string;
  report_id: string;
  revision: number;
  based_on_revision: number;
  outline_json: string;
  outline_confirmation: string;
  datasets_json: string;
  template_key: string;
  template_version: number;
  engine_version: string;
  metrics_json: string;
  gap_json: string;
  status: string;
  completeness: string;
  published_at: string | null;
  superseded_at: string | null;
  created_at: string;
}

/**
 * One delivery of one revision, as the read returns it (`GET /reports/:id`).
 * `downloadable` is decided by the server (`reportArtifactDownloadable`) — the UI
 * must render this flag rather than inspect `artifact_status`/`byte_size` itself,
 * because the 30-day clock is a server fact (A11/A47, doc/288 §7.7).
 */
export interface ReportArtifactEntry {
  artifact: ExportArtifactRecord;
  downloadable: boolean;
}

export interface ReportDetailRecord {
  report: ReportRecord;
  revision: ReportRevisionRecord;
  /** Delivery history, newest first. Present so a refresh re-finds earlier files. */
  artifacts?: ReportArtifactEntry[];
  /**
   * Whether this report's citations can still be re-checked (doc/288 §7.6 / A43).
   * False once any cited evidence snapshot has been purged or passed its clock: the
   * body and the recorded judgement stay readable, but the evidence behind them is
   * gone, and a client must say so instead of opening an empty citation.
   *
   * Optional because the pre-A43 payload did not carry it; the reopen surface that
   * renders the notice arrives with P1, so this field is the contract half only.
   */
  evidence_verifiable?: boolean;
  evidence_expired_at?: string | null;
}

/**
 * Outline-time evidence preview (doc/288 §9.6). `insufficient` is nullable on
 * purpose: `null` means the preview could not be computed, so the UI must say
 * 未评估 instead of 充足 or 不足.
 */
export interface ReportEvidencePreview {
  hit_chunks: number;
  top_sources: string[];
  insufficient: boolean | null;
  note?: string;
}

export interface ReportSectionSnapshot {
  id: string;
  ord: number;
  section_key: string;
  title: string;
  section_version: number;
  status: string;
  evidence_status: string;
  quality_status: string;
  requires_evidence: boolean;
  /** Absent on rows written before v175: render 未评估, never 充足. */
  evidence_preview?: ReportEvidencePreview | null;
  citation_count: number;
  claim_count: number;
  attempt: number;
  tokens_in: number;
  tokens_out: number;
  started_at: string | null;
  finished_at: string | null;
}

export interface ReportRevisionSnapshot {
  id: string;
  revision: number;
  status: string;
  completeness: string;
  outline_confirmation: string;
  template_key: string;
  template_version: number;
  execution_snapshot_hash: string;
  metrics: Record<string, unknown>;
  published_at: string | null;
  superseded_at: string | null;
}

export interface ReportTokenLedger {
  envelope: number;
  actual: number;
  charged: number;
  released: number;
  overage: number;
  pending_reconcile: number;
}

export interface ReportGapEntry {
  section_key: string;
  ord: number;
  title?: string;
  reason: "failed" | "skipped" | "budget_exhausted";
  error_code?: string;
  impact?: string;
}

export interface ReportJobSnapshot {
  job_id: string;
  report_id: string;
  job_kind: string;
  status: string;
  stage: string;
  attempt_seq: number;
  slice_seq: number;
  progress_done: number;
  progress_total: number;
  event_seq: number;
  terminal: boolean;
  cancel_requested_at: string | null;
  error_code: string;
  updated_at: string;
  tokens: ReportTokenLedger;
  revision: ReportRevisionSnapshot;
  sections: ReportSectionSnapshot[];
  gaps: ReportGapEntry[];
}

export interface ReportJobEventFrame {
  seq: number;
  type: string;
  job_id: string;
  revision_id: string;
  at: string;
  payload: Record<string, unknown>;
}

/** POST /reports (202) aggregate echo, §7.7 idempotent replay included. */
export interface CreateReportResult {
  report: ReportRecord;
  revision: ReportRevisionRecord;
  job: { id: string; status: string; job_kind: string };
  created: boolean;
}

export interface ReportTemplateSection {
  key: string;
  title: string;
  prompt?: string;
  requires_evidence: boolean;
  max_words?: number;
  depends_on?: string[];
}

/** One row of POST /reports/:id/revisions/:revision/artifacts (T11/T12). */
export interface ExportArtifactRecord {
  id: string;
  export_job_id: string;
  subject_type: string;
  report_revision_id: string;
  artifact_version: number;
  artifact_status: string;
  format: string;
  renderer_version: string;
  filename: string;
  mime_type: string;
  byte_size: number;
  sha256: string;
  created_at: string;
  expires_at: string;
}

export interface ReportTemplateView {
  template_id: string;
  template_key: string;
  version: number;
  tenant_id: string;
  status: string;
  retired: boolean;
  payload: {
    name: { zh?: string; en?: string };
    output_formats: string[];
    sections: ReportTemplateSection[];
    limits: { max_sections: number; slice_max_seconds: number; total_max_seconds: number; max_slices: number; max_tokens_per_section: number };
    retrieval: { top_k: number; threshold_policy: string; min_evidence: number };
    evidence_policy: { conflict_handling: string; outdated_evidence: string; unsupported_claims: string };
    outline_confirmation: string;
  };
}

// ---------------------------------------------------------------------------
// Closed-set helpers
// ---------------------------------------------------------------------------

/** A job status is over when it is in the terminal slice of the contract set. */
export function isReportJobTerminal(status: string | undefined): boolean {
  if (!status) return false;
  return status === "succeeded" || status === "partial" || status === "failed" || status === "cancelled";
}

// ---------------------------------------------------------------------------
// SSE parsing (§8.3)
// ---------------------------------------------------------------------------

/**
 * Parses one non-empty `data:` line of the report event stream into a frame, or
 * null for a non-frame line. `: keepalive` comments and `id:`/`event:` metadata
 * lines are not data frames; the body of a `data:` frame is the §8.8 object.
 */
export function parseReportSseFrame(rawLine: string): ReportJobEventFrame | null {
  const line = rawLine.trim();
  if (!line.startsWith("data:")) return null;
  const payload = line.slice(5).trim();
  if (!payload) return null;
  try {
    const parsed = JSON.parse(payload) as Record<string, unknown>;
    if (typeof parsed.seq !== "number" || typeof parsed.type !== "string") return null;
    return {
      seq: parsed.seq,
      type: parsed.type,
      job_id: typeof parsed.job_id === "string" ? parsed.job_id : "",
      revision_id: typeof parsed.revision_id === "string" ? parsed.revision_id : "",
      at: typeof parsed.at === "string" ? parsed.at : new Date().toISOString(),
      payload: parsed.payload && typeof parsed.payload === "object" ? parsed.payload as Record<string, unknown> : {},
    };
  } catch {
    return null;
  }
}

/** Splits an SSE byte stream into frames, mirroring `readSse` in the chat adapter. */
export async function* readReportSse(
  body: ReadableStream<Uint8Array>,
): AsyncGenerator<ReportJobEventFrame> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";
    for (const rawLine of lines) {
      const frame = parseReportSseFrame(rawLine);
      if (frame) yield frame;
    }
  }
}

// ---------------------------------------------------------------------------
// Context builders
// ---------------------------------------------------------------------------

export function reportContextFromRecords(
  detail: ReportDetailRecord | CreateReportResult,
  options: { jobId?: string } = {},
): ConversationContext & { kind: "report" } {
  const report = detail.report;
  const revisionStatus = "revision" in detail && detail.revision ? detail.revision.status : undefined;
  const jobStatus = "job" in detail && detail.job ? detail.job.status : undefined;
  const jobId = options.jobId ?? ("job" in detail && detail.job ? detail.job.id : undefined);
  return {
    id: report.id,
    targetId: report.template_key,
    kind: "report",
    type: "run",
    title: report.title,
    question: report.question,
    reportStatus: jobStatus ?? revisionStatus,
    ...(jobId ? { jobId } : {}),
    updatedAt: Date.parse(report.updated_at) || undefined,
  };
}

// ---------------------------------------------------------------------------
// The event poller — the one place that talks to the stream endpoint (§8.3)
// ---------------------------------------------------------------------------

export interface ReportEventPollerOptions {
  /** Where to start: `after=0` is a full replay and never expires. */
  after?: number;
  signal?: AbortSignal;
  /** Called when the server answers 44115 (cursor expired); returns the resume seq. */
  onReset?: (reset: { min_seq: number; event_seq: number }) => number | Promise<number>;
  /** Reference used by tests to swap in a fake transport. */
  transport?: (url: string, init?: RequestInit) => Promise<Response>;
}

export interface ReportEventPoller {
  abort(): void;
  frames(): AsyncGenerator<ReportJobEventFrame>;
  readonly aborted: boolean;
  readonly lastCursor: number;
}

/**
 * Opens the job event stream and yields frames until a terminal event is seen,
 * reconnecting with the last cursor when the server closes a non-terminal
 * stream. `fetch` gets the cookies of the current session, matching the chat
 * adapter's plain-fetch approach (the axios client cannot stream).
 */
export function openReportEventPoller(
  jobId: string,
  options: ReportEventPollerOptions = {},
): ReportEventPoller {
  const controller = new AbortController();
  const forwardAbort = () => controller.abort();
  options.signal?.addEventListener("abort", forwardAbort, { once: true });
  const state = { after: options.after ?? 0, aborted: false, lastCursor: options.after ?? 0 };
  const transport = options.transport ?? ((url: string, init?: RequestInit) => fetch(url, init));

  async function* frames(): AsyncGenerator<ReportJobEventFrame> {
    for (;;) {
      if (state.aborted) return;
      const url = `/api/v1/report-jobs/${encodeURIComponent(jobId)}/events${state.after > 0 ? `?after=${state.after}` : ""}`;
      // The cursor travels twice on purpose: `?after=` is what the server reads
      // first, and `Last-Event-ID` is the header §8.3 names so the request is
      // self-describing in logs and in any proxy that understands resumption. A
      // browser does NOT resend it on a `fetch`-driven reconnect (EventSource would),
      // which is why this client sets it by hand.
      const init: RequestInit = {
        signal: controller.signal,
        ...(state.after > 0 ? { headers: { "Last-Event-ID": String(state.after) } } : {}),
      };
      let response: Response;
      try {
        response = await transport(url, init);
      } catch (error) {
        if (state.aborted) return;
        throw error;
      }
      if (response.status === 409) {
        const body = await response.json().catch(() => null) as { code?: number; data?: { reset_required?: boolean; min_seq?: number; event_seq?: number } } | null;
        if (body?.code === 44115 && options.onReset) {
          const data = body.data ?? {};
          state.after = (await options.onReset({ min_seq: data.min_seq ?? 0, event_seq: data.event_seq ?? 0 })) ?? 0;
          state.lastCursor = state.after;
          continue;
        }
        throw new Error(`report event cursor expired (${response.status})`);
      }
      if (!response.ok || !response.body) {
        throw new Error(`report event stream failed (${response.status})`);
      }
      let terminalSeen = false;
      for await (const frame of readReportSse(response.body)) {
        if (state.aborted) return;
        state.after = frame.seq;
        state.lastCursor = frame.seq;
        yield frame;
        if (isReportTerminalEvent(frame.type)) {
          terminalSeen = true;
          break;
        }
      }
      if (terminalSeen || state.aborted) return;
      // Non-terminal close: reconnect (the server closes idle streams on ctx
      // cancellation while the backend keeps working). Bound reconnect churn.
      await new Promise((resolve) => setTimeout(resolve, 750));
    }
  }

  return {
    abort: () => {
      state.aborted = true;
      controller.abort();
    },
    frames,
    get aborted() {
      return state.aborted;
    },
    get lastCursor() {
      return state.lastCursor;
    },
  };
}

/**
 * Best-effort in-browser mapping from a report id to the job id that created it.
 * The server owns the truth; this only lets a refreshed page resume progress
 * polling before it has re-obtained a job id through the create response. A
 * terminal report never needs it, and the report detail surface works without it.
 */
const REPORT_JOB_MAP_PREFIX = "rgx:report-job-map:";

export function rememberReportJob(reportId: string, jobId: string): void {
  try {
    localStorage.setItem(`${REPORT_JOB_MAP_PREFIX}${reportId}`, jobId);
  } catch {
    // storage unavailable (privacy mode) — the mapping is an optimisation
  }
}

export function rememberedReportJob(reportId: string): string | undefined {
  try {
    return localStorage.getItem(`${REPORT_JOB_MAP_PREFIX}${reportId}`) ?? undefined;
  } catch {
    return undefined;
  }
}
