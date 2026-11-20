import { describe, expect, it } from "vitest";
import {
  EXPORT_ARTIFACT_STATUSES,
  EXPORT_FORMATS,
  REPORT_EXPORT_FORMATS,
  isReportTerminalEvent,
  REPORT_ERROR_CODES,
  REPORT_EVENT_TYPES,
  REPORT_JOB_KINDS,
  REPORT_REWRITE_REASONS,
  REPORT_SECTION_STATUSES,
  REPORT_TERMINAL_EVENTS,
  type ReportErrorCodeMeta,
} from "./report-contract";

// Mirrors the Go registry guards: no gaps, no extras, no renumbering.

describe("report contract — error codes", () => {
  it("is exactly the closed set 44101..44123", () => {
    const codes = Object.keys(REPORT_ERROR_CODES)
      .map(Number)
      .sort((a, b) => a - b);
    expect(codes).toHaveLength(23);
    expect(codes[0]).toBe(44101);
    expect(codes[22]).toBe(44123);
    const expected = Array.from({ length: 23 }, (_, i) => 44101 + i);
    expect(codes).toEqual(expected);
  });

  it("keeps 44123 as the only 429 (a rate condition, not a state conflict)", () => {
    const ratelimited = Object.entries(REPORT_ERROR_CODES)
      .filter(([, meta]) => meta.httpStatus === 429)
      .map(([code]) => Number(code));
    expect(ratelimited).toEqual([44123]);
  });

  it("keeps every object-level code at 404, not 403", () => {
    for (const code of [44101, 44107, 44108, 44109]) {
      expect(REPORT_ERROR_CODES[code].httpStatus).toBe(404);
    }
  });

  it("keeps 500 reserved for a human to look: 44103/44112 are bookkeeping-only", () => {
    const sortByCode = (a: [number, ReportErrorCodeMeta], b: [number, ReportErrorCodeMeta]) =>
      a[0] - b[0];
    const fives = Object.entries(REPORT_ERROR_CODES)
      .map(([code, meta]) => [Number(code), meta] as [number, ReportErrorCodeMeta])
      .filter(([, meta]) => meta.httpStatus === 500)
      .sort(sortByCode)
      .map(([code]) => code);
    // 44103/44112 land only in rgx_report_job.error_code and never surface as an
    // HTTP response; 44113 is the one 500 the client can actually receive.
    expect(fives).toEqual([44103, 44112, 44113]);
  });
});

describe("report contract — broadcast events", () => {
  it("is the §8.8 closed set of 15 values, no duplicates", () => {
    expect(REPORT_EVENT_TYPES).toHaveLength(15);
    expect(new Set(REPORT_EVENT_TYPES).size).toBe(15);
  });

  it("marks exactly the four terminal events, and finalizing is not one", () => {
    expect(REPORT_TERMINAL_EVENTS).toHaveLength(4);
    for (const t of REPORT_TERMINAL_EVENTS) {
      expect(isReportTerminalEvent(t)).toBe(true);
    }
    // `job.finalizing` is emphatically NOT terminal: the revision is not
    // published yet and the runtime job may still be running.
    expect(isReportTerminalEvent("job.finalizing")).toBe(false);
  });

  it("never lets the outbox type masquerade as a broadcast event", () => {
    expect(
      REPORT_EVENT_TYPES.includes("report_job_finalize" as never),
    ).toBe(false);
  });
});

describe("report contract — status closed sets", () => {
  it("keeps section superseded and job kind section_retry present", () => {
    expect(REPORT_SECTION_STATUSES).toContain("superseded");
    expect(REPORT_JOB_KINDS).toContain("section_retry");
    expect(REPORT_REWRITE_REASONS).toEqual([
      "user_edit",
      "retry",
      "quality_fix",
      "regenerate",
    ]);
  });

  it("freezes the export format + artifact status sets to the §7.7/§12 values", () => {
    expect(EXPORT_FORMATS).toEqual([
      "markdown",
      "html",
      "json",
      "docx",
      "pdf",
      "xlsx",
    ]);
    // The report delivery subset: five formats, `json` excluded on purpose.
    expect(REPORT_EXPORT_FORMATS).toEqual([
      "markdown",
      "docx",
      "pdf",
      "html",
      "xlsx",
    ]);
    expect(new Set(REPORT_EXPORT_FORMATS).size).toBe(REPORT_EXPORT_FORMATS.length);
    for (const format of REPORT_EXPORT_FORMATS) {
      expect(EXPORT_FORMATS).toContain(format);
    }
    expect(EXPORT_ARTIFACT_STATUSES).toEqual([
      "pending",
      "ready",
      "failed",
      "aborted",
      "expired",
      "deleted",
    ]);
  });
});
