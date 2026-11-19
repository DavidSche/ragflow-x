import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { tZh } from "../../test/translate";
// The mocked class, not the real one: what matters here is that the drawer prints
// `displayMessage` and stays where it was.
import { ApiError } from "../../lib/api";
import { ReportDetailDrawer } from "./ReportDetailDrawer";

const mocks = vi.hoisted(() => ({
  apiGet: vi.fn(),
  apiPost: vi.fn(),
  notify: vi.fn(),
  canDelete: false,
}));

vi.mock("ra-core", () => ({
  // Resolve against the shipped dictionary: the assertion stays Chinese and a
  // missing key fails instead of matching the key text.
  useTranslate: () => tZh,
  // The drawer offers the R20 schedule card under `execute:report`. These tests
  // read the panel's read-only state; the card's own behaviour lives in
  // ReportScheduleCard.test.tsx.
  // The drawer asks two questions: `execute:report` (the R20 plan card, which these
  // tests read in its read-only state) and `delete:own` (archiving). Answered by
  // action so a test can grant one without granting the other.
  useCanAccess: ({ action }: { action?: string }) => ({
    canAccess: action === "delete:own" ? mocks.canDelete : false,
  }),
  useNotify: () => mocks.notify,
}));

vi.mock("../../lib/api", () => ({
  api: { get: mocks.apiGet, post: mocks.apiPost },
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
  id: "rep-1",
  title: "动火作业风险分析",
  template_key: "analysis",
  status: "active",
  created_at: "2026-11-18T09:00:00Z",
  expires_at: "2026-12-18T09:00:00Z",
};

/** The server's `ReportDetail` shape, gaps/metrics included as JSON strings. */
const detail = {
  report: row,
  revision: {
    id: "rev-2",
    revision: 2,
    status: "published",
    completeness: "partial",
    // A pass rate the gate never computed stays `null`; citation coverage is real.
    metrics_json: JSON.stringify({
      quality_gate_pass_rate: null,
      citation_coverage: 0.5,
      structure_completeness: 1,
      quality_judged: 0,
      // R11 / R16 (doc/288 §9.2 / §9.7): a rate with its denominator, and one
      // cross-section disagreement frozen next to the numbers it was derived from.
      claim_support_rate: 0.75,
      claim_scored: 4,
      claim_not_required: 1,
      consistency_keys_checked: 4,
      consistency_issues: [
        { kind: "number", label: "住宿标准", ords: [2, 4], values: ["600元", "800元"] },
      ],
    }),
    gap_json: JSON.stringify([
      {
        section_key: "gaps",
        ord: 4,
        title: "遗留问题",
        // 闭集，不是散文。写成"该节没有内容"曾让抽屉把原始枚举
        // `failed — section_missing` 直接印在中文界面上却测试全绿
        // （2026-11-19 实测；和 0-based ord 是同一个教训）。
        reason: "skipped",
        impact: "section_missing",
      },
    ]),
    created_at: "2026-11-18T09:00:00Z",
  },
  evidence_verifiable: false,
  evidence_expired_at: "2026-12-01T00:00:00Z",
  // Sharing and the live run's spend (doc/296 §2.1/§2.2). Both travel inside the
  // detail read: no second request, and the drawer cannot show a policy the list
  // endpoint would disagree with.
  access: {
    policy: "selected_users",
    is_owner: true,
    can_manage: true,
    grantees: [{ kind: "user", id: "u-2", name: "张三", missing: false }],
  },
  budget: {
    budget_tokens: 5000,
    spent_tokens: 1200,
    available_tokens: 3800,
    max_extension_tokens: 5000,
    budget_policy: "enforce",
    observing: false,
  },
  artifacts: [
    {
      artifact: {
        id: "art-md",
        format: "markdown",
        artifact_version: 1,
        artifact_status: "ready",
        byte_size: 54424,
        created_at: "2026-11-18T09:10:00Z",
        expires_at: "2026-12-18T09:10:00Z",
      },
      downloadable: true,
    },
    {
      artifact: {
        id: "art-pdf",
        format: "pdf",
        artifact_version: 1,
        artifact_status: "expired",
        byte_size: 177055,
        created_at: "2026-11-18T09:10:00Z",
        expires_at: "2026-11-19T09:10:00Z",
      },
      downloadable: false,
    },
  ],
};

const history = {
  revisions: [
    {
      id: "rev-3",
      revision: 3,
      status: "generating",
      completeness: "",
      template_key: "analysis",
      template_version: 1,
      published_at: null,
      superseded_at: null,
      created_at: "2026-11-19T09:00:00Z",
      is_current: false,
    },
    {
      id: "rev-2",
      revision: 2,
      status: "published",
      completeness: "partial",
      template_key: "analysis",
      template_version: 1,
      published_at: "2026-11-18T09:30:00Z",
      superseded_at: null,
      created_at: "2026-11-18T09:00:00Z",
      is_current: true,
    },
    {
      id: "rev-1",
      revision: 1,
      status: "superseded",
      completeness: "full",
      template_key: "analysis",
      template_version: 1,
      published_at: "2026-11-17T09:30:00Z",
      superseded_at: "2026-11-18T09:30:00Z",
      created_at: "2026-11-17T09:00:00Z",
      is_current: false,
    },
  ],
};

/** Routes the detail read, the history read and "revision N" to their payloads. */
function routeGet(url: string): { data: { code: number; data: unknown } } {
  if (url.endsWith("/revisions")) return { data: { code: 0, data: history } };
  if (/\/revisions\/\d+$/.test(url)) {
    const revisionNo = Number(url.split("/").pop());
    const status = revisionNo === 1 ? "superseded" : "published";
    return {
      data: {
        code: 0,
        data: {
          ...detail,
          revision: { ...detail.revision, revision: revisionNo, status },
          artifacts: revisionNo === 1 ? detail.artifacts.slice(0, 1) : detail.artifacts,
        },
      },
    };
  }
  return { data: { code: 0, data: detail } };
}

describe("ReportDetailDrawer", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.canDelete = false;
    mocks.apiGet.mockImplementation(async (url: string) => routeGet(url));
    mocks.apiPost.mockResolvedValue({ data: { code: 0, data: {} } });
  });

  it("reads the current revision, stored metrics and disclosed gaps", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);

    await waitFor(() => expect(mocks.apiGet).toHaveBeenCalledWith("/reports/rep-1"));
    // Revision 2 is the published base, not merely "the newest row".
    expect(await screen.findByText("v2 · 已交付 · 部分")).toBeTruthy();
    // The dialog's close button is the only way a screen reader announces "leave
    // this panel"; it shipped as a hard-coded English `Close` in a Chinese UI.
    // The ra-core double echoes keys, so the assertion is on the key it asks for.
    expect(screen.getByRole("button", { name: "关闭" })).toBeTruthy();
    // Unjudged ratio: a sentence, never 0%.
    expect(screen.getByText("暂无判定")).toBeTruthy();
    expect(screen.getByText("50%")).toBeTruthy();
    expect(screen.getByText("100%")).toBeTruthy();
    expect(screen.getByText(/遗留问题/)).toBeTruthy();
    // Two closed sets, two labels: the reason says why, the impact says what is
    // missing. Neither is allowed to appear as its wire value.
    expect(screen.getByText(/原因：本轮执行未进行到这一节/)).toBeTruthy();
    expect(screen.getByText(/影响：整节缺失/)).toBeTruthy();
    expect(screen.queryByText(/skipped/)).toBeNull();
    expect(screen.queryByText(/section_missing/)).toBeNull();
    // R11: the claim rate is printed from the frozen metrics, with its denominator
    // still on the wire for anyone reading the JSON.
    expect(screen.getByTestId("report-detail-claim-support").textContent).toBe("75%");
    // R16: the disagreement names the fact, both chapters and both values — a line
    // saying only "跨章节数字冲突" would leave the reader to hunt for it.
    const consistency = screen.getByTestId("report-detail-consistency");
    expect(consistency.textContent).toContain("一致性核对 4 项");
    expect(consistency.textContent).toContain("住宿标准");
    expect(consistency.textContent).toContain("第 2 节");
    expect(consistency.textContent).toContain("第 4 节");
    expect(consistency.textContent).toContain("600元");
    expect(consistency.textContent).toContain("800元");
    expect(screen.queryByText(/consistency_issues/)).toBeNull();
  });

  it("carries the sharing policy and the live spend from the detail read", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);

    // One request serves the whole panel: a separate GET for sharing would let the
    // drawer show a policy the server had already replaced.
    expect((await screen.findByTestId("report-share-current")).textContent).toBe("指定成员");
    expect(screen.getByTestId("report-share-grantees").textContent).toContain("张三");
    // The envelope and its bound are the server's numbers, quoted as tokens.
    expect(screen.getByText("当前额度 5000 tokens，单次最多追加 5000 tokens。")).toBeTruthy();
    expect(mocks.apiGet).toHaveBeenCalledWith("/reports/rep-1");
  });

  it("says a report has no live run rather than omitting the card", async () => {
    mocks.apiGet.mockImplementation(async (url: string) => {
      if (url.endsWith("/revisions")) return { data: { code: 0, data: history } };
      return { data: { code: 0, data: { ...detail, budget: undefined } } };
    });
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);

    // A finalized run cannot be extended (the service answers 44104), so the card
    // states that instead of offering the request.
    expect(await screen.findByText("当前没有在跑的运行，无需追加预算。")).toBeTruthy();
    expect(screen.queryByTestId("report-budget-extension-open")).toBeNull();
  });

  it("downloads only what the server marked downloadable", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);

    const link = await screen.findByRole("link", { name: "下载" });
    expect(link.getAttribute("href")).toBe("/api/v1/reports/rep-1/revisions/2/artifacts/art-md/download");
    // PAID: 一个 expired 产物不能被渲染成可点按钮——那是服务端会拒绝的链接。
    expect(screen.getAllByRole("link")).toHaveLength(1);
    expect(screen.getByText("已过期或已撤回，不可下载")).toBeTruthy();
  });

  it("warns that citations can no longer be re-checked", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);
    await screen.findByText("v2 · 已交付 · 部分");
    // The banner carries the expiry timestamp after the sentence, so match on the
    // sentence rather than the whole node.
    expect(screen.getByText(/引用证据已过期：正文与判定仍可读，但引用无法再复核。/)).toBeTruthy();
  });

  it("exports a format from the centre and re-reads the delivered list", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);
    await screen.findByText("v2 · 已交付 · 部分");

    const calls = mocks.apiGet.mock.calls.length;
    fireEvent.click(screen.getByTestId("report-detail-export-pdf"));

    // The drawer now also renders the share panel and the budget card, so the export
    // round trip competes with more work in the same tick; the assertions are
    // unchanged, only the window is wide enough for a loaded CI box.
    await waitFor(
      () =>
        expect(mocks.apiPost).toHaveBeenCalledWith("/reports/rep-1/revisions/2/artifacts", {
          format: "pdf",
        }),
      { timeout: 5000 },
    );
    // The list is re-read from the server; the drawer does not splice locally.
    // One re-read = the detail plus its history (two GETs), and never a loop:
    // a translate function with an unstable identity would keep firing here.
    await waitFor(() => expect(mocks.apiGet.mock.calls.length).toBe(calls + 2), {
      timeout: 5000,
    });
    expect(mocks.apiGet.mock.calls.length).toBe(calls + 2);
  });

  it("offers exactly the five report delivery formats the server accepts", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);
    await screen.findByText("v2 · 已交付 · 部分");

    // One button per format in the contract's subset, with a reader-facing label.
    // The set is asserted literally because adding a format is a server change: a
    // button that posts a format the server refuses is the R19 lesson repeated.
    expect(screen.getByTestId("report-detail-export-markdown")).toBeTruthy();
    expect(screen.getByTestId("report-detail-export-docx")).toBeTruthy();
    expect(screen.getByTestId("report-detail-export-pdf")).toBeTruthy();
    expect(screen.getByTestId("report-detail-export-html")).toBeTruthy();
    expect(screen.getByTestId("report-detail-export-xlsx")).toBeTruthy();
    // `json` is not a report delivery: the frozen payload already is the JSON.
    expect(screen.queryByTestId("report-detail-export-json")).toBeNull();
    expect(screen.getByTestId("report-detail-export-html").textContent).toBe("网页");
    expect(screen.getByTestId("report-detail-export-xlsx").textContent).toBe("表格");
  });

  it("posts the new formats with their canonical names", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);
    await screen.findByText("v2 · 已交付 · 部分");

    fireEvent.click(screen.getByTestId("report-detail-export-xlsx"));
    await waitFor(
      () =>
        expect(mocks.apiPost).toHaveBeenCalledWith("/reports/rep-1/revisions/2/artifacts", {
          format: "xlsx",
        }),
      { timeout: 5000 },
    );
  });

  it("refuses to offer export on a revision the gate would reject", async () => {
    mocks.apiGet.mockImplementation(async (url: string) => {
      if (url.endsWith("/revisions")) return { data: { code: 0, data: { revisions: [] } } };
      return {
        data: {
          code: 0,
          data: { ...detail, revision: { ...detail.revision, status: "superseded" } },
        },
      };
    });
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);

    await screen.findByText("v2 · 已被替代 · 部分");
    // The export gate is published-only (§7.10): no button, one explanation.
    expect(screen.queryByTestId("report-detail-export-pdf")).toBeNull();
    expect(screen.getByText("只有已发布版本可导出。")).toBeTruthy();
  });

  it("degrades to 'no metrics/gaps' when the stored blobs are unreadable", async () => {
    mocks.apiGet.mockImplementation(async (url: string) => {
      if (url.endsWith("/revisions")) return { data: { code: 0, data: { revisions: [] } } };
      return {
        data: {
          code: 0,
          data: {
          ...detail,
          revision: { ...detail.revision, metrics_json: "{not json", gap_json: "{not json" },
            artifacts: [],
          },
        },
      };
    });
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);

    await screen.findByText("v2 · 已交付 · 部分");
    // No crash, no fabricated 0%: an unreadable blob means "nothing to show".
    // Four, not three: the claim support rate is a fourth ratio that was never
    // computed, and it must say so as well.
    expect(screen.getAllByText("暂无判定")).toHaveLength(4);
    // An unreadable blob also means "the consistency check never ran" — no section,
    // no "0 项" reassurance.
    expect(screen.queryByTestId("report-detail-consistency")).toBeNull();
    expect(screen.getByText("还没有导出过文件。")).toBeTruthy();
  });

  it("shows the server's refusal instead of an empty shell", async () => {
    mocks.apiGet.mockRejectedValue(new Error("boom"));
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);

    expect(await screen.findByRole("alert")).toHaveProperty("textContent");
    expect(screen.queryByText("v2 · 已交付 · 部分")).toBeNull();
  });

  it("does not request anything when closed", () => {
    render(<ReportDetailDrawer report={null} onOpenChange={vi.fn()} />);
    expect(mocks.apiGet).not.toHaveBeenCalled();
  });

  // ---- version history (doc/288 §7.10 / A41) -------------------------------

  it("lists the revision history and marks the report's own current revision", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);
    await waitFor(() => expect(mocks.apiGet).toHaveBeenCalledWith("/reports/rep-1/revisions"));

    // v3 is newest but is a running attempt: the badge belongs to v2, the published
    // base the report actually points at (A32 / the R13 defect class).
    const current = await screen.findByTestId("report-detail-history-2");
    expect(current.textContent).toContain("当前");
    const running = screen.getByTestId("report-detail-history-3");
    expect(running.textContent).not.toContain("当前");
    // Every non-current row is openable; the current one is where you already are.
    expect(screen.getByTestId("report-detail-open-revision-3")).toBeTruthy();
    expect(screen.queryByTestId("report-detail-open-revision-2")).toBeNull();
  });

  it("opens a historical revision read-only, with its own delivered files", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);
    await screen.findByTestId("report-detail-history-1");

    fireEvent.click(screen.getByTestId("report-detail-open-revision-1"));

    await waitFor(() => expect(mocks.apiGet).toHaveBeenCalledWith("/reports/rep-1/revisions/1"));
    expect(await screen.findByText("v1 · 已被替代 · 部分")).toBeTruthy();
    // The banner names what is being looked at, so a reader cannot mistake it for
    // the live revision, and the way back is one click.
    const banner = screen.getByTestId("report-detail-history-banner");
    expect(banner.textContent).toContain("正在查看历史版本 v1（只读，可下载当时交付的文件）");
    // No export: the gate is published-only, and a superseded revision would get 409.
    expect(screen.queryByTestId("report-detail-export-pdf")).toBeNull();
    // Its historical artifact is still downloadable (A41), not silently dropped.
    expect(screen.getByRole("link", { name: "下载" })).toBeTruthy();
  });

  it("returns to the current revision from the history banner", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);
    await screen.findByTestId("report-detail-history-1");
    fireEvent.click(screen.getByTestId("report-detail-open-revision-1"));
    await screen.findByText("v1 · 已被替代 · 部分");

    fireEvent.click(screen.getByTestId("report-detail-back-to-current"));

    expect(await screen.findByText("v2 · 已交付 · 部分")).toBeTruthy();
    expect(screen.queryByTestId("report-detail-history-banner")).toBeNull();
  });

  it("archives behind a confirmation and shows the lifecycle the server returned", async () => {
    mocks.canDelete = true;
    // The answer is the detail view, so the drawer prints the server's lifecycle
    // rather than flipping a local flag: a client-side flip would disagree with the
    // list the moment the request was refused.
    const archived = {
      ...detail,
      report: { ...detail.report, status: "archived" },
    };
    mocks.apiPost.mockResolvedValueOnce({ data: { code: 0, data: archived } });
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);

    await screen.findByText("v2 · 已交付 · 部分");
    expect(screen.getByTestId("report-detail-lifecycle-status").textContent).toBe("在用");
    // The hint states what archiving does *not* do, so the confirmation is informed.
    expect(screen.getByTestId("report-detail-lifecycle").textContent).toContain(
      "已交付的版本仍然可以阅读、下载和导出",
    );

    fireEvent.click(screen.getByTestId("report-detail-archive"));
    // Nothing is sent until the confirmation is accepted.
    expect(mocks.apiPost).not.toHaveBeenCalled();
    expect(await screen.findByText("归档这份报告？")).toBeTruthy();
    expect(screen.getByText(/《动火作业风险分析》会从日常列表移到「已归档」/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "归档" }));

    await waitFor(() => expect(mocks.apiPost).toHaveBeenCalledWith("/reports/rep-1/archive"));
    expect((await screen.findByTestId("report-detail-lifecycle-status")).textContent).toBe("已归档");
    // Archived is a delivery question, not a reading question: the delivered file is
    // still there, and the way back is offered in the same place.
    expect(screen.getByRole("link", { name: "下载" })).toBeTruthy();
    expect(screen.getByTestId("report-detail-unarchive")).toBeTruthy();
    expect(screen.queryByTestId("report-detail-archive")).toBeNull();
    expect(mocks.notify).toHaveBeenCalledWith("已归档");
  });

  it("unarchives in one click, because the safe direction needs no confirmation", async () => {
    mocks.canDelete = true;
    mocks.apiGet.mockImplementation(async (url: string) =>
      url.endsWith("/revisions")
        ? { data: { code: 0, data: history } }
        : { data: { code: 0, data: { ...detail, report: { ...detail.report, status: "archived" } } } },
    );
    mocks.apiPost.mockResolvedValueOnce({ data: { code: 0, data: detail } });
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);

    expect((await screen.findByTestId("report-detail-lifecycle-status")).textContent).toBe("已归档");
    expect(screen.getByTestId("report-detail-lifecycle").textContent).toContain(
      "不再产生新内容",
    );

    fireEvent.click(screen.getByTestId("report-detail-unarchive"));

    await waitFor(() => expect(mocks.apiPost).toHaveBeenCalledWith("/reports/rep-1/unarchive"));
    expect((await screen.findByTestId("report-detail-lifecycle-status")).textContent).toBe("在用");
    expect(mocks.notify).toHaveBeenCalledWith("已取消归档");
  });

  it("keeps the lifecycle the report had when the server refuses the archive", async () => {
    mocks.canDelete = true;
    mocks.apiPost.mockRejectedValueOnce(new ApiError(409, 44104, "report is archived"));
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);

    await screen.findByText("v2 · 已交付 · 部分");
    fireEvent.click(screen.getByTestId("report-detail-archive"));
    fireEvent.click(await screen.findByRole("button", { name: "归档" }));

    await waitFor(() =>
      expect(mocks.notify).toHaveBeenCalledWith("报告不存在（404/44109）", { type: "error" }),
    );
    // 44104/44109 leave the report where it was, so the drawer must not have moved it.
    expect(screen.getByTestId("report-detail-lifecycle-status").textContent).toBe("在用");
  });

  it("offers no lifecycle action to a caller without delete:own", async () => {
    // The server answers 44109 to everyone but the owner; a button here would be a
    // link to that refusal.
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);
    await screen.findByText("v2 · 已交付 · 部分");
    expect(screen.getByTestId("report-detail-lifecycle")).toBeTruthy();
    expect(screen.queryByTestId("report-detail-archive")).toBeNull();
    expect(screen.queryByTestId("report-detail-unarchive")).toBeNull();
  });
});
