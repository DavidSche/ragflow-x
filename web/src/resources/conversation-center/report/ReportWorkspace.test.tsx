/**
 * ReportWorkspace flow tests (doc/288 T8/T9/T11/T12):
 *   1. access gating  — no_access when the RS permission check denies
 *   2. template → dataset → create → outline-confirm → running → published
 *   3. cancel  — a non-terminal job shows the cancel action, which issues the
 *      POST and keeps the view on the current run
 *   4. terminal mapping — a failed snapshot lands on the failed banner
 *
 * The job-follow engine (SSE parsing, cursors, 44115 reset) is covered by
 * report-run.test.ts; here the snapshot endpoint is stubbed so the workspace
 * exercises its own phase machine without a network.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { ReportWorkspace } from "./ReportWorkspace";
import type { ReportAdapter } from "../adapters";
import type {
  CreateReportResult,
  ReportJobSnapshot,
  ReportTemplateView,
} from "./report-run";

const state = vi.hoisted(() => ({
  canAccess: { canAccess: true } as { canAccess: boolean },
  datasets: [{ id: "d1", name: "KM Docs" }] as Array<{ id: string; name: string }>,
}));

vi.mock("ra-core", () => ({
  useTranslate: () =>
    (key: string, options?: { _?: string }) =>
      options && typeof options._ === "string" ? options._ : key,
  useNotify: () => vi.fn(),
  useCanAccess: () => state.canAccess,
  useGetList: () => ({ data: state.datasets, total: state.datasets.length }),
}));
vi.mock("@/components/ui/badge", () => ({
  Badge: ({ children }: { children?: ReactNode }) => <span>{children}</span>,
}));
vi.mock("@/components/ui/button", () => ({
  Button: (props: Record<string, unknown>) => <button {...props} />,
}));
vi.mock("@/components/ui/input", () => ({
  Input: (props: Record<string, unknown>) => <input {...props} />,
}));
vi.mock("@/components/ui/tooltip", () => ({
  Tooltip: ({ children }: { children?: ReactNode }) => <>{children}</>,
  TooltipContent: () => null,
  TooltipTrigger: ({ children }: { children?: ReactNode }) => <>{children}</>,
}));

const template: ReportTemplateView = {
  template_id: "tpl-1",
  template_key: "regulatory_report",
  version: 1,
  tenant_id: "t1",
  status: "active",
  retired: false,
  payload: {
    name: { zh: "监管合规报告", en: "Regulatory Report" },
    output_formats: ["docx"],
    sections: [
      {
        key: "overview",
        title: "概览",
        prompt: "概述合规状态",
        requires_evidence: true,
        max_words: 500,
      },
    ],
    limits: {
      max_sections: 10,
      slice_max_seconds: 60,
      total_max_seconds: 120,
      max_slices: 20,
      max_tokens_per_section: 8000,
    },
    retrieval: { top_k: 5, threshold_policy: "auto", min_evidence: 1 },
    evidence_policy: {
      conflict_handling: "flag",
      outdated_evidence: "flag",
      unsupported_claims: "flag",
    },
    outline_confirmation: "required",
  },
};

const createResult: CreateReportResult = {
  created: true,
  report: {
    id: "rep1",
    title: "合规报告",
    question: "合规报告",
    template_key: "regulatory_report",
    status: "running",
    access_policy: "owner_only",
    current_revision_id: "rev1",
    created_at: "2026-11-17T10:00:00.000Z",
    updated_at: "2026-11-17T10:00:00.000Z",
  },
  revision: {
    id: "rev1",
    report_id: "rep1",
    revision: 1,
    based_on_revision: 0,
    outline_json: "[]",
    outline_confirmation: "required",
    datasets_json: "[]",
    template_key: "regulatory_report",
    template_version: 1,
    engine_version: "v1",
    metrics_json: "{}",
    gap_json: "{}",
    status: "awaiting_outline",
    completeness: "pending",
    published_at: null,
    superseded_at: null,
    created_at: "2026-11-17T10:00:00.000Z",
  },
  job: { id: "job1", status: "queued", job_kind: "report" },
};

function snapshot(status: string): ReportJobSnapshot {
  return {
    job_id: "job1",
    report_id: "rep1",
    job_kind: "report",
    status,
    stage: status === "succeeded" ? "compose" : "writing",
    attempt_seq: 1,
    slice_seq: 2,
    progress_done: status === "succeeded" ? 5 : 2,
    progress_total: 5,
    event_seq: status === "succeeded" ? 8 : 3,
    terminal: status === "succeeded" || status === "failed" || status === "cancelled",
    cancel_requested_at: null,
    error_code: status === "failed" ? "44114" : "",
    updated_at: "2026-11-17T10:05:00.000Z",
    tokens: {
      envelope: 100,
      actual: 90,
      charged: 90,
      released: 0,
      overage: 0,
      pending_reconcile: 0,
    },
    revision: {
      id: "rev1",
      revision: 1,
      status: "draft",
      completeness: "pending",
      outline_confirmation: "required",
      template_key: "regulatory_report",
      template_version: 1,
      execution_snapshot_hash: "h",
      metrics: {},
      published_at: null,
      superseded_at: null,
    },
    sections: [],
    gaps: status === "failed" ? [{ section_key: "overview", ord: 0, reason: "failed" }] : [],
  };
}

function makeFlowAdapter(terminalAfterConfirm: "succeeded" | "failed" | null) {
  let status = "awaiting_outline";
  const adapter = {
    kind: "report" as const,
    listTemplates: vi.fn(async () => [template]),
    createReport: vi.fn(async () => createResult),
    cancelReport: vi.fn(async () => undefined),
    loadSnapshot: vi.fn(async () => snapshot(status)),
    // A11: the published surface re-reads the delivery history from the read.
    loadDetail: vi.fn(async () => ({ ...createResult, artifacts: [] })),
    artifactDownloadUrl: vi.fn(
      (reportId: string, revisionNo: number, artifactId: string) =>
        `/api/v1/reports/${reportId}/revisions/${revisionNo}/artifacts/${artifactId}/download`,
    ),
    createExport: vi.fn(async () => ({
      export_job: { id: "ej1" },
      canonical_hash: "sha256:abc",
      downloadable: true,
      export_artifact: {
        id: "art-1",
        export_job_id: "ej1",
        subject_type: "report_revision",
        report_revision_id: "rev1",
        artifact_version: 1,
        artifact_status: "ready",
        format: "docx",
        renderer_version: "export.renderer.v2",
        filename: "report.docx",
        mime_type: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
        byte_size: 2048,
        sha256: "sha256:abc",
        created_at: "2026-11-17T10:00:00.000Z",
        expires_at: "2026-12-17T10:00:00.000Z",
      },
    })),
    // The outline confirm is what releases the job to run; only after it is
    // invoked do snapshots turn terminal — deterministic for the phase machine.
    confirmOutline: vi.fn(async () => {
      if (terminalAfterConfirm) status = terminalAfterConfirm;
    }),
  } as unknown as ReportAdapter;
  return { adapter, setStatus: (next: string) => {
    status = next;
  } };
}

async function createThroughConfigure(user: ReturnType<typeof userEvent.setup>, adapter: ReportAdapter) {
  render(<ReportWorkspace adapter={adapter} identity={{ tenant_id: "t1", id: "u1" }} onClose={vi.fn()} />);
  await screen.findByTestId("template-list");
  await user.click(screen.getByTestId("template-regulatory_report"));
  await user.click(screen.getByTestId("dataset-d1"));
  await user.type(screen.getByTestId("report-title-input"), "合规报告");
  await user.click(screen.getByTestId("report-create"));
  await screen.findByTestId("outline-editor");
}

describe("ReportWorkspace", () => {
  beforeEach(() => {
    state.canAccess = { canAccess: true };
    localStorage.clear();
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("no network")));
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("shows the access-denied screen when the RS permission is not granted", async () => {
    state.canAccess = { canAccess: false };
    const { adapter } = makeFlowAdapter(null);
    render(<ReportWorkspace adapter={adapter} identity={null} onClose={vi.fn()} />);
    expect(await screen.findByText("conversationCenter.report_no_access")).toBeInTheDocument();
  });

  it("walks template → dataset → create → outline-confirm → published with the artifact panel", async () => {
    const user = userEvent.setup();
    const { adapter } = makeFlowAdapter("succeeded");
    await createThroughConfigure(user, adapter);

    // Required mode: the confirm button POSTs the reviewed outline (header + each required section).
    await user.click(
      screen.getByRole("button", { name: "conversationCenter.report_outline_confirm" }),
    );
    expect(adapter.confirmOutline).toHaveBeenCalledWith("rep1", 1, [
      { section_key: "overview", title: "概览", requires_evidence: true },
    ]);

    // Terminal job status lands on the published surface with the exports.
    expect(await screen.findByTestId("artifact-panel")).toBeInTheDocument();
    expect(screen.getByTestId("export-docx")).toBeInTheDocument();
    expect(screen.queryByTestId("report-cancel")).not.toBeInTheDocument();
  });

  it("keeps a running job live with a cancel action and progress", async () => {
    const user = userEvent.setup();
    const { adapter } = makeFlowAdapter(null);
    await createThroughConfigure(user, adapter);

    await user.click(
      screen.getByRole("button", { name: "conversationCenter.report_outline_confirm" }),
    );
    const cancel = await screen.findByTestId("report-cancel");
    expect(screen.getByTestId("progress-step")).toBeInTheDocument();
    await user.click(cancel);
    expect(adapter.cancelReport).toHaveBeenCalledWith("rep1");
  });

  it("maps a failed job snapshot to the failed banner", async () => {
    const user = userEvent.setup();
    const { adapter } = makeFlowAdapter("failed");
    await createThroughConfigure(user, adapter);
    await user.click(
      screen.getByRole("button", { name: "conversationCenter.report_outline_confirm" }),
    );
    expect(await screen.findByText("conversationCenter.report_failed_banner")).toBeInTheDocument();
    expect(screen.queryByTestId("artifact-panel")).not.toBeInTheDocument();
  });
});
