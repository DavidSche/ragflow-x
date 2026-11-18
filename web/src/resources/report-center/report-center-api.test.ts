import { describe, expect, it } from "vitest";
import {
  formatRatio,
  reportOpenPath,
  reportStatusLabel,
  trendPoints,
  type TrendResult,
} from "./report-center-api";

/**
 * The fixtures below are the *server* shape (`ReportMetricsTrendView` in
 * `internal/service/report_metrics.go`), not a convenient one: a bucket is keyed
 * by `date`, and an unjudged ratio is `null`. Reading `bucket.bucket` (a name the
 * backend never sends) produced an empty chart that still looked plausible — the
 * kind of bug only a fixture taken from the API can catch.
 */
const serverTrend: TrendResult = {
  view: "own",
  bucket: "day",
  from: "2026-11-04T00:00:00Z",
  to: "2026-11-18T00:00:00Z",
  days: 14,
  truncated: false,
  buckets: [
    { date: "2026-11-18", revisions: 1, published: 1 },
    { date: "2026-11-16", revisions: 3, published: 2 },
  ],
  totals: {
    revisions: 4,
    published: 3,
    quality_judged: 2,
    quality_passed: 1,
    quality_gate_pass_rate: 0.5,
    citation_coverage: null,
  },
};

describe("report center view model", () => {
  it("reads the server's `date` bucket key and sorts ascending", () => {
    expect(trendPoints(serverTrend).map((point) => point.key)).toEqual([
      "2026-11-16",
      "2026-11-18",
    ]);
  });

  it("renders an unjudged ratio as a sentence, never as 0%", () => {
    expect(formatRatio(null)).toBe("暂无判定");
    expect(formatRatio(undefined)).toBe("暂无判定");
    expect(formatRatio(0)).toBe("0%");
    expect(formatRatio(0.5)).toBe("50%");
    expect(formatRatio(1)).toBe("100%");
  });

  it("rounds a computed ratio instead of printing float noise", () => {
    expect(formatRatio(0.3333)).toBe("33%");
  });

  it("opens the report's originating conversation and keeps the session", () => {
    expect(
      reportOpenPath({
        id: "r1",
        title: "t",
        template_key: "analysis",
        status: "active",
        origin_chat_id: "chat 1",
        origin_session_id: "s&1",
        created_at: "",
        expires_at: "",
      }),
    ).toBe("/conversation-center?kind=chat&targetId=chat+1&contextId=s%261");
  });

  it("does not invent an entry point for a report with no origin chat", () => {
    expect(
      reportOpenPath({
        id: "r1",
        title: "t",
        template_key: "analysis",
        status: "active",
        created_at: "",
        expires_at: "",
      }),
    ).toBeNull();
  });

  it("labels only statuses the backend actually writes", () => {
    expect(reportStatusLabel("active")).toBe("进行中");
    // Unknown values are shown raw: a label for a state nobody writes is fiction.
    expect(reportStatusLabel("archived")).toBe("archived");
  });
});
