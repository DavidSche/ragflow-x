import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ReportDetailDrawer } from "./ReportDetailDrawer";

const mocks = vi.hoisted(() => ({
  apiGet: vi.fn(),
  apiPost: vi.fn(),
}));

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
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
    }),
    gap_json: JSON.stringify([
      {
        section_key: "gaps",
        ord: 4,
        title: "遗留问题",
        reason: "skipped",
        impact: "该节没有内容",
      },
    ]),
    created_at: "2026-11-18T09:00:00Z",
  },
  evidence_verifiable: false,
  evidence_expired_at: "2026-12-01T00:00:00Z",
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
    mocks.apiGet.mockImplementation(async (url: string) => routeGet(url));
    mocks.apiPost.mockResolvedValue({ data: { code: 0, data: {} } });
  });

  it("reads the current revision, stored metrics and disclosed gaps", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);

    await waitFor(() => expect(mocks.apiGet).toHaveBeenCalledWith("/reports/rep-1"));
    // Revision 2 is the published base, not merely "the newest row".
    expect(await screen.findByText("v2 · published · partial")).toBeTruthy();
    // Unjudged ratio: a sentence, never 0%.
    expect(screen.getByText("暂无判定")).toBeTruthy();
    expect(screen.getByText("50%")).toBeTruthy();
    expect(screen.getByText("100%")).toBeTruthy();
    expect(screen.getByText(/遗留问题/)).toBeTruthy();
    expect(screen.getByText(/skipped/)).toBeTruthy();
  });

  it("downloads only what the server marked downloadable", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);

    const link = await screen.findByRole("link", { name: "reportCenter.detail_download" });
    expect(link.getAttribute("href")).toBe("/api/v1/reports/rep-1/revisions/2/artifacts/art-md/download");
    // PAID: 一个 expired 产物不能被渲染成可点按钮——那是服务端会拒绝的链接。
    expect(screen.getAllByRole("link")).toHaveLength(1);
    expect(screen.getByText("reportCenter.detail_not_downloadable")).toBeTruthy();
  });

  it("warns that citations can no longer be re-checked", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);
    await screen.findByText("v2 · published · partial");
    expect(screen.getByText(/reportCenter.detail_evidence_expired/)).toBeTruthy();
  });

  it("exports a format from the centre and re-reads the delivered list", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);
    await screen.findByText("v2 · published · partial");

    const calls = mocks.apiGet.mock.calls.length;
    fireEvent.click(screen.getByTestId("report-detail-export-pdf"));

    await waitFor(() =>
      expect(mocks.apiPost).toHaveBeenCalledWith("/reports/rep-1/revisions/2/artifacts", { format: "pdf" }),
    );
    // The list is re-read from the server; the drawer does not splice locally.
    // One re-read = the detail plus its history (two GETs), and never a loop:
    // a translate function with an unstable identity would keep firing here.
    await waitFor(() => expect(mocks.apiGet.mock.calls.length).toBe(calls + 2));
    expect(mocks.apiGet.mock.calls.length).toBe(calls + 2);
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

    await screen.findByText("v2 · superseded · partial");
    // The export gate is published-only (§7.10): no button, one explanation.
    expect(screen.queryByTestId("report-detail-export-pdf")).toBeNull();
    expect(screen.getByText("reportCenter.detail_export_only_published")).toBeTruthy();
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

    await screen.findByText("v2 · published · partial");
    // No crash, no fabricated 0%: an unreadable blob means "nothing to show".
    expect(screen.getAllByText("暂无判定")).toHaveLength(3);
    expect(screen.getByText("reportCenter.detail_no_artifacts")).toBeTruthy();
  });

  it("shows the server's refusal instead of an empty shell", async () => {
    mocks.apiGet.mockRejectedValue(new Error("boom"));
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);

    expect(await screen.findByRole("alert")).toHaveProperty("textContent");
    expect(screen.queryByText("v2 · published · partial")).toBeNull();
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
    expect(current.textContent).toContain("reportCenter.detail_history_current");
    const running = screen.getByTestId("report-detail-history-3");
    expect(running.textContent).not.toContain("reportCenter.detail_history_current");
    // Every non-current row is openable; the current one is where you already are.
    expect(screen.getByTestId("report-detail-open-revision-3")).toBeTruthy();
    expect(screen.queryByTestId("report-detail-open-revision-2")).toBeNull();
  });

  it("opens a historical revision read-only, with its own delivered files", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);
    await screen.findByTestId("report-detail-history-1");

    fireEvent.click(screen.getByTestId("report-detail-open-revision-1"));

    await waitFor(() => expect(mocks.apiGet).toHaveBeenCalledWith("/reports/rep-1/revisions/1"));
    expect(await screen.findByText("v1 · superseded · partial")).toBeTruthy();
    // The banner names what is being looked at, so a reader cannot mistake it for
    // the live revision, and the way back is one click.
    const banner = screen.getByTestId("report-detail-history-banner");
    expect(banner.textContent).toContain("reportCenter.detail_viewing_history");
    // No export: the gate is published-only, and a superseded revision would get 409.
    expect(screen.queryByTestId("report-detail-export-pdf")).toBeNull();
    // Its historical artifact is still downloadable (A41), not silently dropped.
    expect(screen.getByRole("link", { name: "reportCenter.detail_download" })).toBeTruthy();
  });

  it("returns to the current revision from the history banner", async () => {
    render(<ReportDetailDrawer report={row} onOpenChange={vi.fn()} />);
    await screen.findByTestId("report-detail-history-1");
    fireEvent.click(screen.getByTestId("report-detail-open-revision-1"));
    await screen.findByText("v1 · superseded · partial");

    fireEvent.click(screen.getByTestId("report-detail-back-to-current"));

    expect(await screen.findByText("v2 · published · partial")).toBeTruthy();
    expect(screen.queryByTestId("report-detail-history-banner")).toBeNull();
  });
});
