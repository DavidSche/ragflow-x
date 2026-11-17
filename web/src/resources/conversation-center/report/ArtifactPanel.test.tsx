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

vi.mock("ra-core", async (importOriginal) => ({
  ...(await importOriginal<typeof import("ra-core")>()),
  useTranslate: () =>
    (key: string, options?: { _?: string }) =>
      options && typeof options._ === "string" ? options._ : key,
  useNotify: () => vi.fn(),
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
};

function makeAdapter(): ReportAdapter {
  return {
    kind: "report" as const,
    createExport: vi.fn(async (reportId: string, revisionNo: number, format: "md" | "docx") => ({
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

  it("renders pre-existing exports provided through initialExports", () => {
    render(
      <ArtifactPanel adapter={makeAdapter()} reportId="rep1" revision={1} initialExports={[readyExport]} />,
    );
    expect(screen.getByText(/Q3_质量报告_v1\.docx/)).toBeInTheDocument();
    expect(screen.getByTestId("artifact-download-art-1")).toHaveAttribute("href", expect.stringContaining("art-1"));
  });

  it("discloses a failed artifact without a download link", async () => {
    const user = userEvent.setup();
    const adapter = makeAdapter();
    adapter.createExport = vi.fn(async () => ({
      ...readyExport,
      export_artifact: {
        ...readyExport.export_artifact,
        id: "art-failed",
        artifact_status: "failed" as const,
        byte_size: 0,
      },
    }));
    render(<ArtifactPanel adapter={adapter} reportId="rep1" revision={1} />);
    await user.click(screen.getByTestId("export-md"));
    const row = await screen.findByTestId("artifact-art-failed");
    expect(row).toBeInTheDocument();
    expect(row.textContent).toContain("failed");
    expect(screen.queryByTestId("artifact-download-art-failed")).not.toBeInTheDocument();
  });
});
