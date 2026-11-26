/**
 * T12 ArtifactPanel tests — synchronous export flow, artifact row rendering,
 * download link construction, and failed-artifact disclosure.
 */
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { ArtifactPanel, formatArtifactSize } from "./ArtifactPanel";
import type { ReportAdapter, ReportExportSuccess } from "../adapters";
import { REPORT_EXPORT_FORMATS } from "./report-contract";
import { ApiError } from "../../../lib/api";
import { tZh } from "../../../test/translate";

const notifyMock = vi.hoisted(() => vi.fn());

// Resolve through the shipped dictionary rather than echoing the key: a key-echo mock
// is banned here because it cannot tell "the copy says what the design says" from
// "the copy is the key", and every dialog assertion below is about the sentence.
vi.mock("ra-core", async (importOriginal) => ({
  ...(await importOriginal<typeof import("ra-core")>()),
  useTranslate: () => (key: string, options?: Record<string, unknown>) =>
    tZh(key, options),
  useNotify: () => notifyMock,
}));
vi.mock("@/components/ui/badge", () => ({
  Badge: ({ children }: { children?: ReactNode }) => <span>{children}</span>,
}));
vi.mock("@/components/ui/button", () => ({
  Button: (props: Record<string, unknown>) => <button {...props} />,
}));

const readyExport: ReportExportSuccess = {
  export_job: { id: "ej1", status: "ready" },
  export_artifact: {
    id: "art-1",
    export_job_id: "ej1",
    subject_type: "report_revision",
    report_revision_id: "rev1",
    artifact_version: 1,
    artifact_status: "ready",
    format: "docx",
    renderer_version: "v1",
    filename: "Q3_质量报告_v1.docx",
    mime_type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    byte_size: 2048,
    sha256: "sha256:abc",
    created_at: "2026-11-17T10:00:00.000Z",
    expires_at: "2026-11-24T10:00:00.000Z",
  },
  canonical_hash: "sha256:canon-abc",
  downloadable: true,
};

function makeAdapter(): ReportAdapter {
  return {
    kind: "report" as const,
    createExport: vi.fn(async (reportId: string, revisionNo: number, format: string) => ({
      ...readyExport,
      export_artifact: { ...readyExport.export_artifact, id: `art-${format}-${reportId}` },
    })),
    artifactDownloadUrl: vi.fn(
      (reportId: string, revisionNo: number, artifactId: string) =>
        `/api/v1/reports/${reportId}/revisions/${revisionNo}/artifacts/${artifactId}/download`,
    ),
  } as unknown as ReportAdapter;
}

describe("formatArtifactSize", () => {
  it("formats bytes into human units", () => {
    expect(formatArtifactSize(0)).toBe("—");
    expect(formatArtifactSize(512)).toBe("512 B");
    expect(formatArtifactSize(2048)).toBe("2.0 KB");
    expect(formatArtifactSize(2.5 * 1024 * 1024)).toBe("2.5 MB");
  });
});

describe("ArtifactPanel", () => {
  it("shows the empty state before any export", () => {
    render(<ArtifactPanel adapter={makeAdapter()} reportId="rep1" revision={1} />);
    expect(screen.getByText(tZh("conversationCenter.report_artifacts_empty"))).toBeInTheDocument();
  });

  it("exports and renders a downloadable artifact row", async () => {
    const user = userEvent.setup();
    const adapter = makeAdapter();
    render(<ArtifactPanel adapter={adapter} reportId="rep1" revision={1} />);
    await user.click(screen.getByTestId("export-docx"));
    expect(adapter.createExport).toHaveBeenCalledWith("rep1", 1, "docx");
    const artifact = await screen.findByText(/Q3_质量报告_v1\.docx/);
    expect(artifact).toBeInTheDocument();
    expect(screen.getByTestId("artifact-download-art-docx-rep1")).toHaveAttribute(
      "href",
      "/api/v1/reports/rep1/revisions/1/artifacts/art-docx-rep1/download",
    );
  });

  it("offers the five-report-format set the drawer offers, not the old three", () => {
    // 2026-11-19 线上复核发现的缺口：同一个交付，工作区面板只给 3 个按钮，
    // 详情抽屉给 5 个。两个入口必须映射同一个闭集。
    render(<ArtifactPanel adapter={makeAdapter()} reportId="rep1" revision={1} />);
    for (const format of REPORT_EXPORT_FORMATS) {
      expect(screen.getByTestId(`export-${format}`)).toBeInTheDocument();
    }
    expect(screen.queryByTestId("export-json")).not.toBeInTheDocument();
    expect(screen.queryByTestId("export-html")).toBeInTheDocument();
    expect(screen.queryByTestId("export-xlsx")).toBeInTheDocument();
  });

  it("exports HTML and XLSX with their wire names", async () => {
    const user = userEvent.setup();
    const adapter = makeAdapter();
    render(<ArtifactPanel adapter={adapter} reportId="rep1" revision={1} />);
    await user.click(screen.getByTestId("export-html"));
    expect(adapter.createExport).toHaveBeenCalledWith("rep1", 1, "html");
    await user.click(screen.getByTestId("export-xlsx"));
    expect(adapter.createExport).toHaveBeenCalledWith("rep1", 1, "xlsx");
  });

  it("offers a PDF export alongside Markdown and Word (T14)", async () => {
    const user = userEvent.setup();
    const adapter = makeAdapter();
    render(<ArtifactPanel adapter={adapter} reportId="rep1" revision={1} />);
    await user.click(screen.getByTestId("export-pdf"));
    expect(adapter.createExport).toHaveBeenCalledWith("rep1", 1, "pdf");
  });

  it("explains a failed PDF as a missing font, not a transient error", async () => {
    const user = userEvent.setup();
    const adapter = makeAdapter();
    adapter.createExport = vi.fn(async () => ({
      ...readyExport,
      downloadable: false,
      export_artifact: {
        ...readyExport.export_artifact,
        id: "art-pdf-failed",
        format: "pdf",
        artifact_status: "failed" as const,
        byte_size: 0,
      },
    }));
    render(<ArtifactPanel adapter={adapter} reportId="rep1" revision={1} />);
    notifyMock.mockClear();
    await user.click(screen.getByTestId("export-pdf"));
    // A missing CJK font is an operator fix, not a retry: the message names it.
    await vi.waitFor(() =>
      expect(notifyMock).toHaveBeenCalledWith(
        tZh("conversationCenter.report_export_pdf_unavailable"),
        { type: "error" },
      ),
    );
  });

  it("renders the server delivery history (A11: survives a refresh)", () => {
    render(
      <ArtifactPanel
        adapter={makeAdapter()}
        reportId="rep1"
        revision={1}
        history={[{ artifact: readyExport.export_artifact, downloadable: true }]}
      />,
    );
    expect(screen.getByText(/Q3_质量报告_v1\.docx/)).toBeInTheDocument();
    expect(screen.getByTestId("artifact-download-art-1")).toHaveAttribute("href", expect.stringContaining("art-1"));
  });

  it("renders a historic artifact the server marked undownloadable", () => {
    // The 30-day clock is the server's fact (A47). The panel must not re-derive
    // "ready + size > 0" and hand out a link the download endpoint would refuse.
    render(
      <ArtifactPanel
        adapter={makeAdapter()}
        reportId="rep1"
        revision={1}
        history={[{ artifact: readyExport.export_artifact, downloadable: false }]}
      />,
    );
    expect(screen.getByTestId("artifact-art-1")).toBeInTheDocument();
    expect(screen.queryByTestId("artifact-download-art-1")).not.toBeInTheDocument();
    expect(screen.getByText(tZh("conversationCenter.report_artifact_pending"))).toBeInTheDocument();
  });

  it("merges a fresh export over the history without duplicating the row", async () => {
    const user = userEvent.setup();
    const adapter = makeAdapter();
    const onExported = vi.fn();
    render(
      <ArtifactPanel
        adapter={adapter}
        reportId="rep1"
        revision={1}
        history={[{ artifact: readyExport.export_artifact, downloadable: true }]}
        onExported={onExported}
      />,
    );
    await user.click(screen.getByTestId("export-docx"));
    // The export asked the workspace to re-read the history (one source of truth).
    expect(onExported).toHaveBeenCalled();
    const fresh = await screen.findByTestId("artifact-art-docx-rep1");
    expect(fresh).toBeInTheDocument();
    // The pre-existing row is still there exactly once.
    expect(screen.getAllByTestId("artifact-art-1")).toHaveLength(1);
  });

  it("discloses a failed artifact without a download link", async () => {
    const user = userEvent.setup();
    const adapter = makeAdapter();
    adapter.createExport = vi.fn(async () => ({
      ...readyExport,
      // A failed render is not a file: the server's own flag says so, and the panel
      // renders the flag rather than re-deriving it from `artifact_status`.
      downloadable: false,
      export_artifact: {
        ...readyExport.export_artifact,
        id: "art-failed",
        artifact_status: "failed" as const,
        byte_size: 0,
      },
    }));
    render(<ArtifactPanel adapter={adapter} reportId="rep1" revision={1} />);
    await user.click(screen.getByTestId("export-markdown"));
    const row = await screen.findByTestId("artifact-art-failed");
    expect(row).toBeInTheDocument();
    expect(row.textContent).toContain(tZh("reportCenter.label_artifact.failed"));
    expect(screen.queryByTestId("artifact-download-art-failed")).not.toBeInTheDocument();
  });
});

/**
 * 导出前的知悉确认 (doc/301 §7.2 / T09).
 *
 * The two assurance refusals are different products, and the panel must not merge
 * them: 44121 asks a question (acknowledge the open conflicts and go on), 44122
 * states a fact (the review has not finished, so there is nothing to acknowledge).
 * A panel that treated them alike would either invent an ack nobody gave or offer a
 * button that cannot work.
 */
describe("ArtifactPanel assurance gate", () => {
  const review = {
    report_id: "rep1",
    revision_id: "rev1",
    revision: 1,
    revision_status: "published",
    release_eligibility: "blocked" as const,
    release_eligibility_basis: ["unresolved_conflicts"],
    requested_policy: "advisory" as const,
    acknowledgeable: true,
    acknowledged: false,
    counters: {
      total_claims: 4,
      denominator: 4,
      evaluated: 4,
      supported: 2,
      partially_supported: 0,
      unsupported: 0,
      conflicting: 2,
      insufficient_evidence: 0,
      abstained: 0,
      not_evaluated: 0,
      not_required: 0,
      high_findings: 1,
      open_findings: 2,
      not_required_share: 0,
      denominator_small: false,
      coverage: 1,
    },
    sections: [],
    findings: [],
    scan_scope: "cited_only",
    candidate_count: 0,
    candidates_visible: false,
  };

  function adapterRefusing(code: number) {
    const adapter = makeAdapter();
    let acked = false;
    adapter.createExport = vi.fn(async (
      reportId: string,
      revisionNo: number,
      format: string,
      options?: { acknowledgeAssurance?: boolean },
    ) => {
      if (!acked && !options?.acknowledgeAssurance) {
        acked = options?.acknowledgeAssurance === true;
        throw new ApiError(409, code, "refused");
      }
      return { ...readyExport, export_artifact: { ...readyExport.export_artifact, id: `art-${format}-acked` } };
    }) as unknown as ReportAdapter["createExport"];
    return adapter;
  }

  it("turns 44121 into a dialog stating the verdict and the open conflicts", async () => {
    const user = userEvent.setup();
    const adapter = adapterRefusing(44121);
    const loadReview = vi.fn(async () => review);
    render(
      <ArtifactPanel adapter={adapter} reportId="rep1" revision={1} loadReview={loadReview} />,
    );
    notifyMock.mockClear();
    await user.click(screen.getByTestId("export-docx"));

    const dialog = await screen.findByTestId("ack-dialog");
    expect(dialog).toBeInTheDocument();
    expect(loadReview).toHaveBeenCalledWith("rep1", 1);
    expect(screen.getByTestId("ack-counts")).toHaveTextContent("未解决冲突 2 项");
    expect(screen.getByTestId("ack-counts")).toHaveTextContent("高风险 1 项");
    expect(screen.getByTestId("ack-eligibility")).toHaveTextContent("复核结论");
    // The refusal itself is not an error to notify: it is a question being asked.
    expect(notifyMock).not.toHaveBeenCalled();
  });

  /**
   * doc/301 D6/J6 + T18 (2026-11-26): v0.7.0 is advisory + ack. An uncalibrated
   * review (tier_0) must reach the same dialog, and the dialog must state the
   * eligibility rather than claim there are conflicts to look at — otherwise the
   * reviewer confirms "2 conflicts" that do not exist.
   */
  it("asks for the ack when the review is tier_0, not only when conflicts exist", async () => {
    const user = userEvent.setup();
    const adapter = adapterRefusing(44121);
    const uncalibrated = {
      ...review,
      release_eligibility: "tier_0" as const,
      release_eligibility_basis: ["semantic_layer_unavailable"],
      counters: { ...review.counters, open_findings: 0, high_findings: 0, conflicting: 0 },
    };
    render(
      <ArtifactPanel adapter={adapter} reportId="rep1" revision={1} loadReview={async () => uncalibrated} />,
    );
    await user.click(screen.getByTestId("export-docx"));

    const dialog = await screen.findByTestId("ack-dialog");
    expect(dialog).toBeInTheDocument();
    expect(screen.getByTestId("ack-counts")).toHaveTextContent("未解决冲突 0 项");
    expect(screen.getByTestId("ack-counts")).toHaveTextContent("即使冲突为 0");
    expect(screen.getByTestId("ack-eligibility")).toHaveTextContent("复核结论");
    expect(notifyMock).not.toHaveBeenCalled();
  });

  it("re-exports with acknowledge_assurance only after the user confirms", async () => {
    const user = userEvent.setup();
    const adapter = adapterRefusing(44121);
    render(
      <ArtifactPanel adapter={adapter} reportId="rep1" revision={1} loadReview={async () => review} />,
    );
    await user.click(screen.getByTestId("export-docx"));
    await screen.findByTestId("ack-dialog");

    await user.click(screen.getByTestId("ack-confirm"));
    await screen.findByTestId("artifact-download-art-docx-acked");
    expect(adapter.createExport).toHaveBeenLastCalledWith("rep1", 1, "docx", {
      acknowledgeAssurance: true,
    });
    expect(screen.queryByTestId("ack-dialog")).not.toBeInTheDocument();
  });

  it("cancelling writes nothing: no second request, no ack", async () => {
    const user = userEvent.setup();
    const adapter = adapterRefusing(44121);
    render(
      <ArtifactPanel adapter={adapter} reportId="rep1" revision={1} loadReview={async () => review} />,
    );
    await user.click(screen.getByTestId("export-docx"));
    await screen.findByTestId("ack-dialog");
    const calls = (adapter.createExport as unknown as { mock: { calls: unknown[] } }).mock.calls.length;

    await user.click(screen.getByTestId("ack-cancel"));
    expect(screen.queryByTestId("ack-dialog")).not.toBeInTheDocument();
    expect((adapter.createExport as unknown as { mock: { calls: unknown[] } }).mock.calls.length).toBe(calls);
  });

  it("says the review has not finished for 44122 and offers no dialog", async () => {
    const user = userEvent.setup();
    const adapter = adapterRefusing(44122);
    const loadReview = vi.fn(async () => review);
    render(
      <ArtifactPanel adapter={adapter} reportId="rep1" revision={1} loadReview={loadReview} />,
    );
    notifyMock.mockClear();
    await user.click(screen.getByTestId("export-docx"));

    expect(await vi.waitFor(() => expect(notifyMock).toHaveBeenCalled())).toBeTruthy();
    expect(notifyMock).toHaveBeenCalledWith(
      tZh("conversationCenter.report_export_assurance_not_ready"),
      { type: "error" },
    );
    expect(screen.queryByTestId("ack-dialog")).not.toBeInTheDocument();
    // Nothing to acknowledge yet, so the review is not even read.
    expect(loadReview).not.toHaveBeenCalled();
  });

  it("degrades to a plain error when no review reader is wired", async () => {
    const user = userEvent.setup();
    const adapter = adapterRefusing(44121);
    render(<ArtifactPanel adapter={adapter} reportId="rep1" revision={1} />);
    notifyMock.mockClear();
    await user.click(screen.getByTestId("export-docx"));
    await vi.waitFor(() => expect(notifyMock).toHaveBeenCalled());
    expect(screen.queryByTestId("ack-dialog")).not.toBeInTheDocument();
  });
});
