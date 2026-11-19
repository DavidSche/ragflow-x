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
import { tZh } from "../../test/translate";
import { ReportReader } from "./ReportReader";

const mocks = vi.hoisted(() => ({ apiGet: vi.fn() }));

vi.mock("ra-core", () => ({
  useTranslate: () => tZh,
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
    // R11 (doc/288 §9.2): the sentences the section asserts, with the rule-check
    // verdict and the citation numbers each one used.
    claims: [
      {
        ord: 1,
        claim_text: "本报告覆盖动火作业的审批与监护[1]。",
        claim_type: "fact",
        support_status: "supported",
        markers: [1],
      },
      {
        ord: 2,
        claim_text: "监护人到场后方可动火。",
        claim_type: "fact",
        support_status: "unsupported",
        markers: [],
      },
      {
        ord: 3,
        claim_text: "建议每季度演练一次。",
        claim_type: "recommendation",
        support_status: "not_required",
        markers: [],
      },
    ],
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
    // The server only ever writes this closed set (model.ReportGap*); a prose
    // fixture hid the fact that the reader printed the raw value.
    gap_reason: "failed",
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

    // The body, not the claim list: the same sentence now appears twice on the page
    // (once as delivered prose, once as a claim), so the assertion names the surface.
    const body = await screen.findByTestId("report-reader-body-1");
    expect(body.textContent).toContain("本报告覆盖动火作业的审批与监护");
    // The status strip is the section's own words, never a re-derived verdict.
    // The badge answers three separate questions in the UI's language; a raw
    // `succeeded · passed · supported` is the server's vocabulary, not the reader's.
    expect(screen.getByText("已生成 · 质量通过 · 证据充分")).toBeTruthy();
    // A failed section explains itself; an empty heading would read as a bug.
    const gap = screen.getByTestId("report-reader-gap-2");
    expect(gap.textContent).toContain("未交付");
    // `failed` is the server's word, not the reader's: printing it verbatim said
    // `这一节未交付: failed` on a live run (2026-11-18).
    expect(gap.textContent).toContain("模型重试后仍未产出正文");
    expect(gap.textContent).not.toContain("failed");
    // A skipped section with no recorded reason still says it produced nothing.
    expect(screen.getByTestId("report-reader-gap-3").textContent).toContain(
      "这一节没有产出正文。",
    );
    // Citations are the section's stored count, not a recount of the body text.
    // The dictionary formats `%{count}`, and react-admin interpolates it; a
    // `{{count}}` placeholder would print its own braces (fixed 2026-11-18).
    expect(screen.getByText("共 2 条引用")).toBeTruthy();
  });

  it("shows R17 locators as navigation and R18 research next to the body", async () => {
    mocks.apiGet.mockImplementation(async (url: string) => {
      if (url.endsWith("/sections")) {
        return {
          data: {
            code: 0,
            data: {
              sections: [
                {
                  ...sections[0],
                  location_hints: ["页面索引：动火作业管理制度"],
                  research_plan: { depth: "deep", planning: "planned", merged: 4, duplicates: 1 },
                },
              ],
            },
          },
        };
      }
      return { data: { code: 0, data: { report: row, revision: { id: "rev-7", revision: 2 } } } };
    });
    render(<ReportReader report={row} revision={0} onOpenChange={vi.fn()} />);

    const body = await screen.findByTestId("report-reader-body-1");
    // The locator is drawn outside the body: a line of it inside the prose would be
    // indistinguishable from a sentence the model wrote.
    expect(body.textContent).not.toContain("页面索引");
    expect(screen.getByTestId("report-section-hints-1").textContent).toContain(
      "定位线索（非证据）：",
    );
    expect(screen.getByTestId("report-section-hints-1").textContent).toContain(
      "页面索引：动火作业管理制度",
    );
    // R18: the disclosure names the state, not the wire enum.
    const research = screen.getByTestId("report-section-research-1");
    expect(research.textContent).toContain("已按计划扩展检索");
    expect(research.textContent).toContain("新增 4 条片段");
    expect(research.textContent).not.toContain("planned");
    // The citation count is still the section's own stored number: a locator is not
    // a citation, so it must not change what the reader is told.
    expect(screen.getByText("共 2 条引用")).toBeTruthy();
  });

  it("lists every claim with its verdict, and never certifies truth", async () => {
    mockServer(2);
    render(<ReportReader report={row} revision={0} onOpenChange={vi.fn()} />);

    const panel = await screen.findByTestId("report-reader-claims");
    // The fixed preamble is the reason the list is allowed to exist at all: a green
    // 「可核实」 without it reads as "this sentence is true" (§9.2).
    expect(panel.textContent).toContain("规则检查结果");
    expect(panel.textContent).toContain("不代表句子本身为真");
    // All three claims, including the one that cites nothing: hiding it would hide
    // exactly what the appendix is for.
    expect(panel.querySelectorAll("li")).toHaveLength(3);
    expect(screen.getByTestId("report-reader-claim-support-1").textContent).toBe("可核实");
    // An amendment-worthy verdict is printed as a verdict, not as the wire enum.
    expect(screen.getByTestId("report-reader-claim-support-2").textContent).toBe(
      "未获证据支持",
    );
    expect(panel.textContent).toContain("未引用证据");
    expect(panel.textContent).not.toContain("unsupported");
    expect(panel.textContent).not.toContain("partially_supported");
    // An opinion is not an unverified fact, so it gets no support badge — but it is
    // still listed, with its type, so the list adds up to the section.
    expect(screen.queryByTestId("report-reader-claim-support-3")).toBeNull();
    expect(panel.textContent).toContain("建议");
    expect(panel.textContent).toContain("每季度演练一次");
  });

  it("marks a body carried over from an earlier version (R14)", async () => {
    mocks.apiGet.mockImplementation(async (url: string) => {
      if (url.endsWith("/sections")) {
        return {
          data: {
            code: 0,
            data: { sections: [{ ...sections[0], rewrite_reason: "reuse" }, sections[1]] },
          },
        };
      }
      return {
        data: { code: 0, data: { report: row, revision: { id: "rev-7", revision: 3 } } },
      };
    });
    render(<ReportReader report={row} revision={3} onOpenChange={vi.fn()} />);

    expect(await screen.findByTestId("report-reader-reused-1")).toBeTruthy();
    // Only the reused section is flagged; a section written for this run is not.
    expect(screen.queryByTestId("report-reader-reused-2")).toBeNull();
  });

  it("renders a section body as Markdown instead of printing its syntax", async () => {
    // A live report body is Markdown with GFM tables: `**加粗**`, `| a | b |` and
    // `|---|---|`. Pre-wrapping the string printed every one of those characters
    // in the reading view (2026-11-19), so this pins the render, not the styling.
    mocks.apiGet.mockImplementation(async (url: string) => {
      if (url.endsWith("/sections")) {
        return {
          data: {
            code: 0,
            data: {
              sections: [
                {
                  ...sections[0],
                  content_md:
                    "本次结论**已复核**。" +
                    String.fromCharCode(10) +
                    String.fromCharCode(10) +
                    "| 制度 | 条款 |" +
                    String.fromCharCode(10) +
                    "| --- | --- |" +
                    String.fromCharCode(10) +
                    "| 通知 | 第 3 条 |" +
                    String.fromCharCode(10) +
                    String.fromCharCode(10) +
                    "- 第一项" +
                    String.fromCharCode(10) +
                    "- 第二项",
                },
              ],
            },
          },
        };
      }
      return {
        data: { code: 0, data: { report: row, revision: { id: "rev-7", revision: 2 } } },
      };
    });
    render(<ReportReader report={row} revision={2} onOpenChange={vi.fn()} />);

    const body = await screen.findByTestId("report-reader-body-1");
    expect(body.querySelector("strong")?.textContent).toBe("已复核");
    expect(body.querySelectorAll("table tbody tr")).toHaveLength(1);
    expect(body.querySelectorAll("li")).toHaveLength(2);
    // The syntax itself must not survive into the text the reader sees.
    expect(body.textContent).not.toContain("**");
    expect(body.textContent).not.toContain("---");
    expect(body.textContent).not.toContain("|");
    expect(body.textContent).toContain("本次结论已复核。");
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

    expect(await screen.findByText("这个版本没有可显示的章节")).toBeTruthy();
    // It never guesses a number and asks for revision 0.
    expect(mocks.apiGet).toHaveBeenCalledTimes(1);
  });

  it("does not request anything when closed", () => {
    render(<ReportReader report={null} onOpenChange={vi.fn()} />);
    expect(mocks.apiGet).not.toHaveBeenCalled();
  });
});
