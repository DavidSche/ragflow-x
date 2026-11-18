/**
 * Full reading view (doc/288 §7.10 / §9.5, doc/44 「好用」 gap #4).
 *
 * The point of these tests is the *honesty* of the surface, not its styling: a
 * revision number must come from the server's pointer, an empty section must state
 * its reason instead of rendering a blank heading, and a body that cannot be read
 * must say so rather than look like a report with nothing in it.
 */
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ReportReader } from "./ReportReader";

const mocks = vi.hoisted(() => ({ apiGet: vi.fn() }));

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
}));

vi.mock("../../lib/api", () => ({
  api: { get: mocks.apiGet, post: vi.fn() },
  ApiError: class extends Error {
    readonly displayMessage = "报告不存在（404/44109）";
  },
  unwrap: async (request: Promise<{ data: { code: number; data: unknown } }>) => {
    const response = await request;
    if (response.data.code !== 0) throw new Error("request failed");
    return response.data.data;
  },
}));

const row = {
  id: "rep-9",
  title: "动火作业风险分析",
  template_key: "analysis",
  status: "active",
  current_revision_id: "rev-7",
  created_at: "2026-11-18T09:00:00Z",
  expires_at: "2026-12-18T09:00:00Z",
};

const sections = [
  {
    id: "sec-1",
    ord: 1,
    section_key: "scope",
    title: "范围与依据",
    section_version: 1,
    status: "succeeded",
    quality_status: "passed",
    evidence_status: "supported",
    requires_evidence: true,
    citation_count: 2,
    content_md: "本报告覆盖动火作业的审批与监护。",
    gap_reason: "",
  },
  {
    id: "sec-2",
    ord: 2,
    section_key: "risks",
    title: "主要风险",
    section_version: 1,
    status: "failed",
    quality_status: "failed",
    evidence_status: "unsupported",
    requires_evidence: true,
    citation_count: 0,
    content_md: "",
    gap_reason: "模型连续两次未返回可用内容",
  },
  {
    id: "sec-3",
    ord: 3,
    section_key: "appendix",
    title: "附录",
    section_version: 1,
    status: "skipped",
    quality_status: "not_checked",
    evidence_status: "not_required",
    requires_evidence: false,
    citation_count: 0,
    content_md: "",
    gap_reason: "",
  },
];

function mockServer(revisionNo: number) {
  mocks.apiGet.mockImplementation(async (url: string) => {
    if (url.endsWith("/sections")) {
      return { data: { code: 0, data: { sections } } };
    }
    return {
      data: { code: 0, data: { report: row, revision: { id: "rev-7", revision: revisionNo } } },
    };
  });
}

describe("ReportReader", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("resolves the live revision from the server pointer, not the newest one", async () => {
    // A running regenerate has written v3; the report still points at v2.
    mockServer(2);
    render(<ReportReader report={row} revision={0} onOpenChange={vi.fn()} />);

    await waitFor(() =>
      expect(mocks.apiGet).toHaveBeenCalledWith("/reports/rep-9/revisions/2/sections"),
    );
    expect(mocks.apiGet).not.toHaveBeenCalledWith("/reports/rep-9/revisions/3/sections");
    expect(await screen.findByText("动火作业风险分析 · v2")).toBeTruthy();
  });

  it("reads a named revision directly, without asking for the current one", async () => {
    mockServer(2);
    render(<ReportReader report={row} revision={1} onOpenChange={vi.fn()} />);

    await waitFor(() =>
      expect(mocks.apiGet).toHaveBeenCalledWith("/reports/rep-9/revisions/1/sections"),
    );
    // No detail read: the number was given, so the pointer is not consulted.
    expect(mocks.apiGet).toHaveBeenCalledTimes(1);
  });

  it("renders the body, keeps status visible, and states why a section is empty", async () => {
    mockServer(2);
    render(<ReportReader report={row} revision={0} onOpenChange={vi.fn()} />);

    expect(await screen.findByText(/本报告覆盖动火作业的审批与监护/)).toBeTruthy();
    // The status strip is the section's own words, never a re-derived verdict.
    expect(screen.getByText("succeeded · passed · supported")).toBeTruthy();
    // A failed section explains itself; an empty heading would read as a bug.
    const gap = screen.getByTestId("report-reader-gap-2");
    expect(gap.textContent).toContain("reportCenter.reader_section_gap");
    expect(gap.textContent).toContain("模型连续两次未返回可用内容");
    // A skipped section with no recorded reason still says it produced nothing.
    expect(screen.getByTestId("report-reader-gap-3").textContent).toContain(
      "reportCenter.reader_section_empty",
    );
    // Citations are the section's stored count, not a recount of the body text.
    expect(screen.getByText("reportCenter.reader_citations")).toBeTruthy();
  });

  it("builds the table of contents from the sections in display order", async () => {
    mockServer(2);
    render(<ReportReader report={row} revision={0} onOpenChange={vi.fn()} />);
    await screen.findByTestId("report-reader-toc");

    const items = screen.getAllByTestId(/report-reader-toc-\d/);
    expect(items.map((item) => item.textContent)).toEqual([
      "1. 范围与依据",
      "2. 主要风险",
      "3. 附录",
    ]);
    // Clicking is a jump, not a navigation: no extra request.
    const calls = mocks.apiGet.mock.calls.length;
    fireEvent.click(screen.getByTestId("report-reader-toc-2"));
    expect(mocks.apiGet.mock.calls.length).toBe(calls);
  });

  it("says the body could not be read instead of showing an empty document", async () => {
    mocks.apiGet.mockRejectedValue(new Error("boom"));
    render(<ReportReader report={row} revision={2} onOpenChange={vi.fn()} />);

    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByTestId("report-reader-body")).toBeNull();
  });

  it("explains a report that has no readable revision yet", async () => {
    mocks.apiGet.mockResolvedValue({
      data: { code: 0, data: { report: row, revision: { id: "", revision: 0 } } },
    });
    render(<ReportReader report={row} revision={0} onOpenChange={vi.fn()} />);

    expect(await screen.findByText("reportCenter.reader_empty")).toBeTruthy();
    // It never guesses a number and asks for revision 0.
    expect(mocks.apiGet).toHaveBeenCalledTimes(1);
  });

  it("does not request anything when closed", () => {
    render(<ReportReader report={null} onOpenChange={vi.fn()} />);
    expect(mocks.apiGet).not.toHaveBeenCalled();
  });
});
