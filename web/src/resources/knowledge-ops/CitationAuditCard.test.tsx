import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { translateFor, tZh } from "../../test/translate";
import { CitationAuditCard, formatAuditRate, isCitationAuditRunsPayload } from "./CitationAuditCard";

// The dictionary is resolved for real (`tZh`), never mocked as `(key) => key`: these
// assertions exist to prove a reader sees 「暂无判定」/「预算用尽」 and not a key name.
vi.mock("ra-core", () => ({
  useTranslate: () => translateFor("zh"),
}));

const apiGet = vi.fn();
const apiPost = vi.fn();

vi.mock("../../lib/api", () => ({
  api: {
    get: (...args: unknown[]) => apiGet(...args),
    post: (...args: unknown[]) => apiPost(...args),
  },
}));

const envelope = <T,>(data: T) => Promise.resolve({ data: { code: 0, data } });

function run(overrides: Record<string, unknown> = {}) {
  return {
    id: "run-1",
    period_start: "2026-02-23",
    window_from: "2026-02-23T00:00:00Z",
    window_to: "2026-03-02T00:00:00Z",
    method_version: "qa2.v1",
    sample_target: 50,
    sampled: 12,
    judged: 10,
    passed: 8,
    failed: 2,
    skipped: 2,
    findings: 14,
    tokens_in: 1200,
    tokens_out: 90,
    budget_tokens: 40000,
    semantic: true,
    status: "completed",
    started_at: "2026-03-02T00:05:00Z",
    finished_at: "2026-03-02T00:09:00Z",
    ...overrides,
  };
}

function finding(overrides: Record<string, unknown> = {}) {
  return {
    id: "f-1",
    request_id: "req-1",
    citation_id: "c1",
    verdict: "unsupported",
    method: "deterministic",
    score: 0.04,
    reason: "excerpt_absent_from_answer",
    excerpt_hash_ok: true,
    ...overrides,
  };
}

describe("<CitationAuditCard />", () => {
  beforeEach(() => {
    apiGet.mockReset();
    apiPost.mockReset();
  });

  it("prints every rate next to its denominator", async () => {
    apiGet.mockImplementation((path: string) =>
      path.includes("/findings")
        ? envelope({ findings: [finding()], total: 1 })
        : envelope({ runs: [run()] }),
    );
    render(<CitationAuditCard />);
    await waitFor(() => expect(screen.getByTestId("audit-pass-run-1")).toBeTruthy());
    const cell = screen.getByTestId("audit-pass-run-1");
    expect(cell.textContent).toContain("80.0%");
    expect(cell.textContent).toContain("8/10");
    // The three dispositions stay separate: `skipped` must not be folded into `passed`.
    expect(screen.getByTestId("citation-audit-sample").textContent).toContain("12/50");
    expect(screen.getByTestId("citation-audit-status").textContent).toBe(
      tZh("knowledgeOps.audit_status_completed"),
    );
    expect(screen.getByTestId("citation-audit-semantic").textContent).toContain("40000");
    expect(screen.getByTestId("citation-audit-findings").textContent).toContain(
      tZh("knowledgeOps.audit_verdict_unsupported"),
    );
    expect(screen.getByTestId("citation-audit-findings").textContent).toContain(
      tZh("knowledgeOps.audit_reason_excerpt_absent_from_answer"),
    );
  });

  it("renders 暂无判定 rather than 0% when nothing was judged", async () => {
    apiGet.mockImplementation((path: string) =>
      path.includes("/findings")
        ? envelope({ findings: [], total: 0 })
        : envelope({
            runs: [
              run({
                sampled: 0,
                judged: 0,
                passed: 0,
                failed: 0,
                skipped: 0,
                findings: 0,
                status: "budget_exhausted",
                budget_tokens: 0,
                semantic: false,
              }),
            ],
          }),
    );
    render(<CitationAuditCard />);
    await waitFor(() => expect(screen.getByTestId("audit-pass-run-1")).toBeTruthy());
    const cell = screen.getByTestId("audit-pass-run-1");
    expect(cell.textContent).toContain(tZh("knowledgeOps.audit_ratio_unavailable"));
    expect(cell.textContent).not.toContain("0.0%");
    // `budget_exhausted` is its own status word, not "completed".
    expect(screen.getByTestId("citation-audit-status").textContent).toBe(
      tZh("knowledgeOps.audit_status_budget_exhausted"),
    );
    expect(screen.getByTestId("citation-audit-semantic").textContent).toBe(
      tZh("knowledgeOps.audit_semantic_off"),
    );
    expect(screen.getByTestId("citation-audit-unavailable")).toBeTruthy();
  });

  it("flags a finding whose excerpt hash no longer matches", async () => {
    apiGet.mockImplementation((path: string) =>
      path.includes("/findings")
        ? envelope({ findings: [finding({ reason: "excerpt_hash_mismatch", excerpt_hash_ok: false })], total: 1 })
        : envelope({ runs: [run({ failed: 1, passed: 0, judged: 1 })] }),
    );
    render(<CitationAuditCard />);
    await waitFor(() => expect(screen.getByTestId("citation-audit-findings")).toBeTruthy());
    expect(screen.getByTestId("citation-audit-findings").textContent).toContain(
      tZh("knowledgeOps.audit_hash_mismatch"),
    );
  });

  it("survives a malformed runs payload without blanking", async () => {
    apiGet.mockImplementation(() => envelope({ unexpected: true }));
    render(<CitationAuditCard />);
    await waitFor(() =>
      expect(screen.getByText(tZh("knowledgeOps.audit_load_failed"))).toBeTruthy(),
    );
    expect(isCitationAuditRunsPayload({ unexpected: true })).toBe(false);
  });

  it("reports a refused trigger instead of pretending the sample ran", async () => {
    apiGet.mockImplementation(() => envelope({ runs: [] }));
    apiPost.mockRejectedValue(new Error("400"));
    render(<CitationAuditCard />);
    await waitFor(() => expect(screen.getByTestId("citation-audit-trigger")).toBeTruthy());
    await act(async () => {
      fireEvent.click(screen.getByTestId("citation-audit-trigger"));
    });
    await waitFor(() =>
      expect(screen.getByTestId("citation-audit-trigger-error").textContent).toBe(
        tZh("knowledgeOps.audit_trigger_failed"),
      ),
    );
  });
});

describe("formatAuditRate", () => {
  it("never divides by zero", () => {
    expect(formatAuditRate(0, 0, tZh)).toBe(tZh("knowledgeOps.audit_ratio_unavailable"));
    expect(formatAuditRate(0, 5, tZh)).toBe("0.0%");
    expect(formatAuditRate(3, 4, tZh)).toBe("75.0%");
  });
});
