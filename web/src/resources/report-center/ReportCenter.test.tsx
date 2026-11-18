import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
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
  useCanAccess: () => ({ canAccess: mocks.canManage }),
  useTranslate: () => (key: string, options?: Record<string, string>) => {
    if (!options) return key;
    return Object.entries(options).reduce(
      (text, [name, value]) => text.split(`%{${name}}`).join(value),
      key,
    );
  },
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

    await user.click(screen.getByRole("button", { name: "reportCenter.open" }));
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
    // Three reads: the page, the trend and the retention card — each exactly once.
    await waitFor(() => expect(mocks.apiGet).toHaveBeenCalledTimes(3));
    expect(mocks.apiGet).toHaveBeenCalledTimes(3);
  });

  it("shows an unjudged pass rate as 暂无判定 instead of 0%", async () => {
    mockReads();
    render(<ReportCenter />);

    await screen.findByText("动火作业风险分析");
    // The server stores `null` when the denominator is empty; the browser must
    // not turn that into "0%" — that would be the same lie the server stopped
    // telling (doc/288 §14.1).
    expect(screen.getByText("暂无判定")).toBeTruthy();
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
    await user.click(screen.getByRole("button", { name: "reportCenter.delete" }));
    await user.click(await screen.findByRole("button", { name: "reportCenter.delete" }));

    await waitFor(() => expect(mocks.apiDelete).toHaveBeenCalledWith("/reports/rep-1"));
    expect(mocks.notify).toHaveBeenCalledWith("reportCenter.deleted");

    // The recovery window is only real if it is pressable.
    await user.click(screen.getByRole("button", { name: "reportCenter.undo_action" }));
    await waitFor(() => expect(mocks.apiPost).toHaveBeenCalledWith("/reports/rep-1/restore"));
    expect(mocks.notify).toHaveBeenCalledWith("reportCenter.restored");
    await waitFor(() => expect(screen.queryByRole("button", { name: "reportCenter.undo_action" })).toBeNull());
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
    await user.click(screen.getByRole("button", { name: "reportCenter.delete" }));
    await user.click(await screen.findByRole("button", { name: "reportCenter.delete" }));

    await waitFor(() => expect(mocks.notify).toHaveBeenCalledWith("reportCenter.delete_fail", { type: "error" }));
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
    await user.type(screen.getByPlaceholderText("reportCenter.search_placeholder"), "compliance");
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
    await user.click(screen.getByRole("button", { name: "reportCenter.next_page" }));

    // The request carries the cursor — page 2 must be a server read, not a slice
    // of page 1 (the list is the only place a report can be found).
    expect(await screen.findByText("第二十一份报告")).toBeTruthy();
    expect(
      mocks.apiGet.mock.calls.some((call) => (call[1] as { params?: { page?: number } })?.params?.page === 2),
    ).toBe(true);
    // The double echoes keys, so this only proves the indicator is rendered with the
    // page cursor; the behavioural proof is the page=2 request above.
    expect(screen.getByText("reportCenter.page_indicator")).toBeTruthy();
    expect(
      screen.getByRole("button", { name: "reportCenter.next_page" }).hasAttribute("disabled"),
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
    await user.click(screen.getByRole("button", { name: "reportCenter.next_page" }));
    await screen.findByText("第二十一份报告");

    await user.click(screen.getByRole("button", { name: "reportCenter.delete" }));
    await user.click(screen.getByRole("button", { name: "reportCenter.delete" }));

    // An emptied page must not stay on screen as "0 of N": the page steps back.
    expect(await screen.findByText("动火作业风险分析")).toBeTruthy();
    expect(screen.queryByText("第二十一份报告")).toBeNull();
  });

  it("offers the workspace trend only to a caller who may manage reports", async () => {
    // A plain reader gets `manage:report` denied, so offering the toggle would be a
    // link to a 403. The card is read-only for them as well.
    mockReads();
    const { unmount } = render(<ReportCenter />);
    await screen.findByText("动火作业风险分析");
    expect(screen.queryByTestId("trend-view-tenant")).toBeNull();
    expect(screen.queryByRole("button", { name: "reportCenter.retention_edit" })).toBeNull();
    unmount();

    mocks.canManage = true;
    render(<ReportCenter />);
    await screen.findByText("动火作业风险分析");
    expect(screen.getByTestId("trend-view-own")).toBeTruthy();
    expect(screen.getByTestId("trend-view-tenant")).toBeTruthy();
    expect(screen.getByRole("button", { name: "reportCenter.retention_edit" })).toBeTruthy();
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

  it("renders the trend even when the report list is empty, and flags truncation", async () => {
    mockReads([], trend({ truncated: true, totals: { revisions: 0, published: 0, quality_gate_pass_rate: 0 } }));
    render(<ReportCenter />);

    await waitFor(() => expect(screen.getByText("reportCenter.trend_truncated")).toBeTruthy());
    expect(screen.getByText("reportCenter.empty")).toBeTruthy();
  });
});
