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

const notifyMock = vi.hoisted(() => vi.fn());

vi.mock("ra-core", async (importOriginal) => ({
  ...(await importOriginal<typeof import("ra-core")>()),
  useTranslate: () =>
    (key: string, options?: { _?: string }) =>
      options && typeof options._ === "string" ? options._ : key,
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
    expect(screen.getByText("conversationCenter.report_artifacts_empty")).toBeInTheDocument();
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
        "conversationCenter.report_export_pdf_unavailable",
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
    expect(screen.getByText("conversationCenter.report_artifact_pending")).toBeInTheDocument();
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
    expect(row.textContent).toContain("failed");
    expect(screen.queryByTestId("artifact-download-art-failed")).not.toBeInTheDocument();
  });
});
