import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { tZh } from "../../test/translate";
import { ReportCenter } from "./ReportCenter";

const mocks = vi.hoisted(() => ({
  apiGet: vi.fn(),
  apiDelete: vi.fn(),
  apiPost: vi.fn(),
  notify: vi.fn(),
  navigate: vi.fn(),
  canManage: false,
}));

vi.mock("ra-core", () => ({
  useNotify: () => mocks.notify,
  useNavigate: () => mocks.navigate,
  // The RBAC mirror the page asks before offering governance actions. The server
  // still decides; this only keeps the toggle off a 403 path.
  // R19 governance has its own resource. The page asks three questions, so the
  // double answers by resource: an unreachable template read keeps these tests on
  // the report surfaces they were written for.
  useCanAccess: ({ resource, action }: { resource?: string; action?: string }) => ({
    canAccess:
      resource === "report-template"
        ? action === "manage"
          ? mocks.canManage
          : false
        : mocks.canManage,
  }),
  useTranslate: () => tZh,
}));

vi.mock("../../lib/api", () => ({
  api: {
    get: mocks.apiGet,
    delete: mocks.apiDelete,
    post: mocks.apiPost,
  },
  ApiError: class extends Error {
    readonly status = 400;
    readonly code = 44109;
    readonly displayMessage = "报告不存在（404/44109）";
  },
  unwrap: async (request: Promise<{ data: { code: number; data: unknown } }>) => {
    const response = await request;
    if (response.data.code !== 0) throw new Error("request failed");
    return response.data.data;
  },
}));

const report = {
  id: "rep-1",
  title: "动火作业风险分析",
  question: "动火作业有哪些风险？",
  template_key: "analysis",
  status: "active",
  origin_chat_id: "chat-9",
  origin_session_id: "session-3",
  created_at: "2026-11-18T09:00:00Z",
  expires_at: "2026-12-18T09:00:00Z",
};

function trend(overrides: Record<string, unknown> = {}) {
  return {
    view: "own",
    bucket: "day",
    from: "2026-11-04T00:00:00Z",
    to: "2026-11-18T00:00:00Z",
    truncated: false,
    buckets: [
      { date: "2026-11-17", revisions: 2, published: 1 },
      { date: "2026-11-18", revisions: 1, published: 1 },
    ],
    totals: {
      revisions: 3,
      published: 2,
      quality_gate_pass_rate: null,
      citation_coverage: 0.5,
    },
    ...overrides,
  };
}

const templates = [
  {
    template_id: "tpl-1",
    template_key: "analysis",
    version: 1,
    tenant_id: "t-1",
    status: "active",
    retired: false,
    payload: { name: { zh: "制度符合性分析报告", en: "Compliance analysis" } },
  },
];

const retention = {
  tenant_id: "t-1",
  report_days: 90,
  report_evidence_days: 90,
  report_artifact_days: 30,
  report_event_days: 7,
  report_cleanup_hold_days: 7,
  system_report_days: 90,
  system_report_evidence_days: 90,
  system_report_artifact_days: 30,
  system_report_event_days: 7,
  system_report_cleanup_hold_days: 7,
  overridden: [] as string[],
};

/** Routes the three reads the page makes; anything else fails loudly. */
function mockReads(reports: unknown[] = [report], trendBody: unknown = trend()) {
  mocks.apiGet.mockImplementation((url: string) => {
    if (url === "/reports") {
      return Promise.resolve({
        data: { code: 0, data: { items: reports, total: reports.length, page: 1, page_size: 20 } },
      });
    }
    if (url === "/report-metrics/trend") {
      return Promise.resolve({ data: { code: 0, data: trendBody } });
    }
    if (url === "/report-retention") {
      return Promise.resolve({ data: { code: 0, data: retention } });
    }
    if (url === "/report-templates") {
      return Promise.resolve({ data: { code: 0, data: templates } });
    }
    return Promise.reject(new Error(`unexpected GET ${url}`));
  });
}

describe("ReportCenter", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.canManage = false;
  });

  it("lists reports from the read API and opens the originating conversation", async () => {
    mockReads();
    const user = userEvent.setup();
    render(<ReportCenter />);

    expect(await screen.findByText("动火作业风险分析")).toBeTruthy();

    await user.click(screen.getByRole("button", { name: "打开" }));
    expect(mocks.navigate).toHaveBeenCalledWith(
      "/conversation-center?kind=chat&targetId=chat-9&contextId=session-3",
    );
  });

  it("loads exactly once per mount even though useTranslate is not stable here", async () => {
    // The translate double above returns a fresh closure on every render. If the
    // page's loader depended on it, the mount effect would re-run forever — the
    // drawer had exactly that defect, and a page-level list would hammer the API.
    render(<ReportCenter />);
    await screen.findByText("动火作业风险分析");
    // Four reads: the page, the trend, the retention card and the template
    // catalogue — each exactly once. A loader that depended on `t` would loop.
    await waitFor(() => expect(mocks.apiGet).toHaveBeenCalledTimes(4));
    expect(mocks.apiGet).toHaveBeenCalledTimes(4);
    for (const url of ["/reports", "/report-metrics/trend", "/report-retention", "/report-templates"]) {
      expect(mocks.apiGet.mock.calls.filter(([called]) => called === url)).toHaveLength(1);
    }
  });

  it("shows an unjudged pass rate as 暂无判定 instead of 0%", async () => {
    mockReads();
    render(<ReportCenter />);

    await screen.findByText("动火作业风险分析");
    // The server stores `null` when the denominator is empty; the browser must
    // not turn that into "0%" — that would be the same lie the server stopped
    // telling (doc/288 §14.1).
    // Two cards say 暂无判定 here and both mean it: the quality pass rate (no
    // revision was judged) and the locator-assisted rate (no section cited anything,
    // so 0/0 has no denominator). A third would mean another metric lost its
    // population, which is why the count is pinned rather than "at least one".
    expect(screen.getAllByText("暂无判定")).toHaveLength(2);
    expect(screen.queryByText("0%")).toBeNull();
    expect(screen.getByText("50%")).toBeTruthy();
  });

  it("deletes, offers an inline undo, and restores through POST /reports/:id/restore", async () => {
    mockReads();
    mocks.apiDelete.mockResolvedValue({ data: { code: 0, data: { deleted: true, recoverable: true } } });
    mocks.apiPost.mockResolvedValue({ data: { code: 0, data: { report: { id: report.id } } } });
    const user = userEvent.setup();
    render(<ReportCenter />);

    await screen.findByText("动火作业风险分析");
    await user.click(screen.getByRole("button", { name: "删除" }));
    await user.click(await screen.findByRole("button", { name: "删除" }));

    await waitFor(() => expect(mocks.apiDelete).toHaveBeenCalledWith("/reports/rep-1"));
    expect(mocks.notify).toHaveBeenCalledWith("已删除，可在提示条里撤销。");

    // The recovery window is only real if it is pressable.
    await user.click(screen.getByRole("button", { name: "撤销删除" }));
    await waitFor(() => expect(mocks.apiPost).toHaveBeenCalledWith("/reports/rep-1/restore"));
    expect(mocks.notify).toHaveBeenCalledWith("已恢复。");
    await waitFor(() => expect(screen.queryByRole("button", { name: "撤销删除" })).toBeNull());
  });

  it("reads the report in place, resolving the live revision from the server", async () => {
    // A row that has a published base: `current_revision_id` is the pointer the
    // server keeps, so the reader asks for whatever number that points at.
    mockReads([{ ...report, current_revision_id: "rev-2" }]);
    mocks.apiGet.mockImplementation(((url: string) => {
      if (url === "/reports") {
        return Promise.resolve({
          data: {
            code: 0,
            data: { items: [{ ...report, current_revision_id: "rev-2" }], total: 1, page: 1, page_size: 20 },
          },
        });
      }
      if (url === "/report-metrics/trend") return Promise.resolve({ data: { code: 0, data: trend() } });
      if (url === "/report-retention") return Promise.resolve({ data: { code: 0, data: retention } });
      if (url === "/reports/rep-1") {
        return Promise.resolve({
          data: { code: 0, data: { report, revision: { id: "rev-2", revision: 2 } } },
        });
      }
      if (url === "/reports/rep-1/revisions/2/sections") {
        return Promise.resolve({
          data: {
            code: 0,
            data: {
              sections: [
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
                  citation_count: 1,
                  content_md: "本报告覆盖动火作业的审批与监护。",
                  gap_reason: "",
                },
              ],
            },
          },
        });
      }
      return Promise.reject(new Error(`unexpected GET ${url}`));
    }) as never);
    const user = userEvent.setup();
    render(<ReportCenter />);

    await screen.findByText("动火作业风险分析");
    await user.click(screen.getByTestId("report-read-rep-1"));

    expect(await screen.findByTestId("report-reader")).toBeTruthy();
    expect(await screen.findByText(/本报告覆盖动火作业的审批与监护/)).toBeTruthy();
    expect(mocks.apiGet).toHaveBeenCalledWith("/reports/rep-1/revisions/2/sections");
    // Reading is not a navigation: the user stays in the centre.
    expect(mocks.navigate).not.toHaveBeenCalled();
  });

  it("does not offer a reading view for a report that has no delivered revision", async () => {
    mockReads();
    render(<ReportCenter />);

    await screen.findByText("动火作业风险分析");
    // No `current_revision_id` ⇒ nothing to read. The button is disabled rather
    // than opening a reader that would answer "no readable revision".
    expect(screen.getByTestId("report-read-rep-1")).toHaveProperty("disabled", true);
  });

  it("surfaces the server's refusal message and keeps the row when deletion fails", async () => {
    mockReads();
    mocks.apiDelete.mockRejectedValue(new Error("boom"));
    const user = userEvent.setup();
    render(<ReportCenter />);

    await screen.findByText("动火作业风险分析");
    await user.click(screen.getByRole("button", { name: "删除" }));
    await user.click(await screen.findByRole("button", { name: "删除" }));

    await waitFor(() => expect(mocks.notify).toHaveBeenCalledWith("删除失败。", { type: "error" }));
    expect(screen.getByText("动火作业风险分析")).toBeTruthy();
  });

  it("filters client-side by title, question and template key", async () => {
    mockReads([
      report,
      { ...report, id: "rep-2", title: "受限空间作业", question: "受限空间如何进入？", template_key: "compliance" },
    ]);
    const user = userEvent.setup();
    render(<ReportCenter />);

    await screen.findByText("动火作业风险分析");
    await user.type(screen.getByPlaceholderText("搜索标题 / 问题 / 模板"), "compliance");
    expect(screen.queryByText("动火作业风险分析")).toBeNull();
    expect(screen.getByText("受限空间作业")).toBeTruthy();
  });

  it("walks to the next page instead of hiding reports past the first twenty", async () => {
    mocks.apiGet.mockImplementation((url: string, config?: { params?: Record<string, unknown> }) => {
      if (url === "/reports") {
        const pageNo = Number(config?.params?.page ?? 1);
        const items =
          pageNo === 1 ? [report] : [{ ...report, id: "rep-21", title: "第二十一份报告" }];
        return Promise.resolve({
          data: { code: 0, data: { items, total: 21, page: pageNo, page_size: 20 } },
        });
      }
      if (url === "/report-metrics/trend") {
        return Promise.resolve({ data: { code: 0, data: trend() } });
      }
      return Promise.reject(new Error(`unexpected GET ${url}`));
    });
    const user = userEvent.setup();
    render(<ReportCenter />);

    expect(await screen.findByText("动火作业风险分析")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "下一页" }));

    // The request carries the cursor — page 2 must be a server read, not a slice
    // of page 1 (the list is the only place a report can be found).
    expect(await screen.findByText("第二十一份报告")).toBeTruthy();
    expect(
      mocks.apiGet.mock.calls.some((call) => (call[1] as { params?: { page?: number } })?.params?.page === 2),
    ).toBe(true);
    // The double echoes keys, so this only proves the indicator is rendered with the
    // page cursor; the behavioural proof is the page=2 request above.
    expect(screen.getByText(/第 \d+ \/ \d+ 页/)).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "下一页" }).hasAttribute("disabled"),
    ).toBe(true);
  });

  it("steps back a page when the last row on it is deleted", async () => {
    let deleted = false;
    mocks.apiGet.mockImplementation((url: string, config?: { params?: Record<string, unknown> }) => {
      if (url === "/reports") {
        const pageNo = Number(config?.params?.page ?? 1);
        const items = pageNo === 2 && !deleted ? [{ ...report, id: "rep-21", title: "第二十一份报告" }] : pageNo === 1 ? [report] : [];
        return Promise.resolve({
          data: { code: 0, data: { items, total: deleted ? 20 : 21, page: pageNo, page_size: 20 } },
        });
      }
      if (url === "/report-metrics/trend") {
        return Promise.resolve({ data: { code: 0, data: trend() } });
      }
      return Promise.reject(new Error(`unexpected GET ${url}`));
    });
    mocks.apiDelete.mockImplementation(() => {
      deleted = true;
      return Promise.resolve({ data: { code: 0, data: {} } });
    });
    const user = userEvent.setup();
    render(<ReportCenter />);

    await screen.findByText("动火作业风险分析");
    await user.click(screen.getByRole("button", { name: "下一页" }));
    await screen.findByText("第二十一份报告");

    await user.click(screen.getByRole("button", { name: "删除" }));
    await user.click(screen.getByRole("button", { name: "删除" }));

    // An emptied page must not stay on screen as "0 of N": the page steps back.
    expect(await screen.findByText("动火作业风险分析")).toBeTruthy();
    expect(screen.queryByText("第二十一份报告")).toBeNull();
  });

  it("asks the server for one lifecycle view at a time and returns the cursor to page 1", async () => {
    // The list is a server query, not a client filter: pressing 「已归档」 must re-read
    // with `?status=archived`, because filtering the loaded page would only ever
    // archive-search the twenty rows that happened to be in memory.
    const calls: Array<{ page: number; status: string }> = [];
    const archivedRows: Record<string, unknown[]> = {
      active: [report],
      archived: [{ ...report, id: "rep-7", title: "去年的年度合规报告", status: "archived" }],
      all: [report],
    };
    mocks.apiGet.mockImplementation((url: string, config?: { params?: Record<string, unknown> }) => {
      if (url === "/reports") {
        const pageNo = Number(config?.params?.page ?? 1);
        const status = String(config?.params?.status ?? "");
        calls.push({ page: pageNo, status });
        const items = status === "archived" ? archivedRows.archived : status === "all" ? archivedRows.all : archivedRows.active;
        return Promise.resolve({
          data: { code: 0, data: { items, total: items.length, page: pageNo, page_size: 20 } },
        });
      }
      if (url === "/report-metrics/trend") {
        return Promise.resolve({ data: { code: 0, data: trend() } });
      }
      return Promise.reject(new Error(`unexpected GET ${url}`));
    });
    const user = userEvent.setup();
    render(<ReportCenter />);

    await screen.findByText("动火作业风险分析");
    // The default view is `active`, the same default the server applies.
    expect(calls[0]).toEqual({ page: 1, status: "active" });
    expect(screen.getByTestId("report-view-active").getAttribute("aria-pressed")).toBe("true");

    await user.click(screen.getByTestId("report-view-archived"));
    expect(await screen.findByText("去年的年度合规报告")).toBeTruthy();
    expect(calls.at(-1)).toEqual({ page: 1, status: "archived" });
    expect(screen.queryByText("动火作业风险分析")).toBeNull();

    await user.click(screen.getByTestId("report-view-all"));
    await screen.findByText("动火作业风险分析");
    expect(calls.at(-1)).toEqual({ page: 1, status: "all" });
  });

  it("explains an empty archived view instead of printing that there is no data", async () => {
    mocks.apiGet.mockImplementation((url: string, config?: { params?: Record<string, unknown> }) => {
      if (url === "/reports") {
        const status = String(config?.params?.status ?? "");
        const items = status === "active" ? [report] : [];
        return Promise.resolve({
          data: { code: 0, data: { items, total: items.length, page: 1, page_size: 20 } },
        });
      }
      if (url === "/report-metrics/trend") {
        return Promise.resolve({ data: { code: 0, data: trend() } });
      }
      return Promise.reject(new Error(`unexpected GET ${url}`));
    });
    const user = userEvent.setup();
    render(<ReportCenter />);

    await screen.findByText("动火作业风险分析");
    await user.click(screen.getByTestId("report-view-archived"));
    // A workspace that never archived anything sees the feature described, not a
    // bare "no data" that makes the new view look broken.
    expect(await screen.findByTestId("report-empty-archived")).toBeTruthy();
    expect(screen.getByText("还没有归档的报告")).toBeTruthy();
  });

  it("badges an archived row in the view that mixes both lifecycles", async () => {
    const archivedRow = { ...report, id: "rep-7", title: "去年的年度合规报告", status: "archived" };
    mocks.apiGet.mockImplementation((url: string, config?: { params?: Record<string, unknown> }) => {
      if (url === "/reports") {
        const status = String(config?.params?.status ?? "");
        const items = status === "archived" ? [archivedRow] : status === "all" ? [report, archivedRow] : [report];
        return Promise.resolve({
          data: { code: 0, data: { items, total: items.length, page: 1, page_size: 20 } },
        });
      }
      if (url === "/report-metrics/trend") {
        return Promise.resolve({ data: { code: 0, data: trend() } });
      }
      return Promise.reject(new Error(`unexpected GET ${url}`));
    });
    const user = userEvent.setup();
    render(<ReportCenter />);

    await screen.findByText("动火作业风险分析");
    // No badge in the active view: there is nothing to disambiguate there.
    expect(screen.queryByTestId("report-archived-badge-rep-7")).toBeNull();

    await user.click(screen.getByTestId("report-view-all"));
    // …and in 「全部」 the archived row says so, otherwise it looks like any other
    // report that simply refuses to be rerun.
    expect(await screen.findByTestId("report-archived-badge-rep-7")).toBeTruthy();
    expect(screen.queryByTestId("report-archived-badge-rep-1")).toBeNull();
  });

  it("offers the workspace trend only to a caller who may manage reports", async () => {
    // A plain reader gets `manage:report` denied, so offering the toggle would be a
    // link to a 403. The card is read-only for them as well.
    mockReads();
    const { unmount } = render(<ReportCenter />);
    await screen.findByText("动火作业风险分析");
    expect(screen.queryByTestId("trend-view-tenant")).toBeNull();
    expect(screen.queryByRole("button", { name: "调整保留期" })).toBeNull();
    unmount();

    mocks.canManage = true;
    render(<ReportCenter />);
    await screen.findByText("动火作业风险分析");
    expect(screen.getByTestId("trend-view-own")).toBeTruthy();
    expect(screen.getByTestId("trend-view-tenant")).toBeTruthy();
    expect(screen.getByRole("button", { name: "调整保留期" })).toBeTruthy();
  });

  it("re-reads the trend for the workspace instead of re-labelling the own numbers", async () => {
    mocks.canManage = true;
    mockReads();
    const user = userEvent.setup();
    render(<ReportCenter />);
    await screen.findByText("动火作业风险分析");

    await user.click(screen.getByTestId("trend-view-tenant"));

    // `view` is the ladder parameter; `scope` belongs to the platform's tenant-scope
    // guard and would be rejected as an unregistered value.
    await waitFor(() =>
      expect(mocks.apiGet).toHaveBeenCalledWith("/report-metrics/trend", {
        params: { days: 14, bucket: "day", view: "tenant" },
      }),
    );
    expect(mocks.apiGet).not.toHaveBeenCalledWith("/report-metrics/trend", {
      params: { days: 14, bucket: "day", view: "scope" },
    });
  });

  it("states the trend window and the scope it is reading, never a raw placeholder", async () => {
    // Live 2026-11-18: the heading printed `近 %{days} 天质量趋势` because the card
    // called `t()` without the interpolation params, and the subtitle said
    // `view=own` even after the reader switched to the workspace.
    mocks.canManage = true;
    mockReads();
    const user = userEvent.setup();
    const { container } = render(<ReportCenter />);
    await screen.findByText("动火作业风险分析");

    expect(screen.getByText("近 14 天质量趋势")).toBeTruthy();
    expect(screen.getByText("仅统计你自己的报告。")).toBeTruthy();
    expect(container.textContent).not.toContain("%{");

    await user.click(screen.getByTestId("trend-view-tenant"));
    expect(await screen.findByText("统计整个工作区已交付的报告。")).toBeTruthy();
    expect(screen.queryByText("仅统计你自己的报告。")).toBeNull();
  });

  it("names the template in the list column instead of printing its key", async () => {
    // Live 2026-11-19: the 模板 column printed `report-compliance` — a machine id in a
    // Chinese table. The name comes from the catalogue; the key stays as the title.
    mockReads();
    const { container } = render(<ReportCenter />);
    await screen.findByText("动火作业风险分析");

    expect(await screen.findByText("制度符合性分析报告")).toBeTruthy();
    const cells = Array.from(container.querySelectorAll("td")).map((td) => (td.textContent ?? "").trim());
    expect(cells).not.toContain("analysis");
    const cell = screen.getByText("制度符合性分析报告").closest("td");
    expect(cell?.getAttribute("title")).toBe("analysis");
  });

  it("falls back to the template key when the catalogue cannot be read", async () => {
    // The name is a nicety; a failed catalogue read must not blank the column or
    // take the list down with it.
    mockReads();
    const reads = mocks.apiGet.getMockImplementation();
    mocks.apiGet.mockImplementation((url: string, config?: { params?: Record<string, unknown> }) => {
      if (url === "/report-templates") return Promise.reject(new Error("catalogue down"));
      return reads!(url, config);
    });
    render(<ReportCenter />);

    expect(await screen.findByText("动火作业风险分析")).toBeTruthy();
    expect(await screen.findByText("analysis")).toBeTruthy();
  });

  it("prints the degraded-research count with the number of planned sections", async () => {
    // doc/301 §5.8 item 2. A count without its denominator cannot answer §5.8 item 3's
    // question ("is degradation common enough to fund planning v2"), so the card prints
    // both — and prints nothing at all when no section carried a plan, because
    // "0 degraded out of nothing planned" would read as a clean bill of health.
    mockReads([], trend({
      totals: {
        revisions: 3,
        published: 2,
        quality_gate_pass_rate: null,
        citation_coverage: 0.5,
        research_degraded_sections: 20,
        research_planned_sections: 40,
      },
    }));
    render(<ReportCenter />);

    expect(await screen.findByText("深研降级章节")).toBeTruthy();
    expect(screen.getByText("20")).toBeTruthy();
    expect(screen.getByText("占已规划 40 个章节")).toBeTruthy();
  });

  it("omits the degraded-research card when no section carried a research plan", async () => {
    mockReads();
    render(<ReportCenter />);

    await screen.findByText("动火作业风险分析");
    expect(screen.queryByText("深研降级章节")).toBeNull();
  });

  it("renders the trend even when the report list is empty, and flags truncation", async () => {
    mockReads([], trend({ truncated: true, totals: { revisions: 0, published: 0, quality_gate_pass_rate: 0 } }));
    render(<ReportCenter />);

    await waitFor(() => expect(screen.getByText("数据量超过统计上限，趋势只覆盖最近的部分。")).toBeTruthy());
    expect(screen.getByText("还没有报告。在对话中心提一个需要成文的问题，就会生成报告。")).toBeTruthy();
  });
});
