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
import { tZh } from "../../../test/translate";
import { ReportWorkspace, workspaceName } from "./ReportWorkspace";
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
  // Resolve through the shipped dictionary so a missing key fails the test.
  useTranslate: () => tZh,
  useNotify: () => vi.fn(),
  useCanAccess: () => state.canAccess,

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
    // Real `rgx_report_job.stage` values (model/report.go). The fixture used to
    // invent `compose`/`writing`, which hid the fact that the strip asked the
    // dictionary for keys that do not exist.
    stage: status === "succeeded" ? "assembling" : "section_writing",
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
    // The bindable datasets are asked for explicitly (workspace-scoped), not read
    // off the governance catalogue.
    listReportDatasets: vi.fn(async () => [{ id: "d1", name: "安全生产制度" }]),
    createReport: vi.fn(async () => createResult),
    cancelReport: vi.fn(async () => undefined),
    loadSnapshot: vi.fn(async () => snapshot(status)),
    // A11: the published surface re-reads the delivery history from the read.
    loadDetail: vi.fn(async () => ({ ...createResult, artifacts: [] })),
    // A14: the chips are derived server-side; the workspace only renders them.
    loadFollowUps: vi.fn(async () => ({
      report_id: "rep1",
      revision: 1,
      completeness: "partial",
      items: [
        { text: "这份报告还缺哪些内容？补什么材料能补齐？", kind: "report_gap", section_key: "", section_ord: 0 },
        { text: "第 1 节《概览》为什么没有写成？补什么材料能补齐？", kind: "section_gap", section_key: "overview", section_ord: 1 },
        { text: "基于这份报告，下一步优先做哪三件事？", kind: "next_steps", section_key: "", section_ord: 0 },
      ],
    })),
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

async function createThroughConfigure(
  user: ReturnType<typeof userEvent.setup>,
  adapter: ReportAdapter,
  origin: { chat?: string; session?: string } = {},
  options: { toggleDepth?: boolean; onFollowUp?: (request: import("./ReportWorkspace").ReportFollowUpRequest) => void } = {},
) {
  render(
    <ReportWorkspace
      adapter={adapter}
      identity={{ tenant_id: "t1", tenant_name: "最佳实践工作区", id: "u1" }}
      onClose={vi.fn()}
      originChatId={origin.chat}
      originSessionId={origin.session}
      onFollowUp={options.onFollowUp}
    />,
  );
  await screen.findByTestId("template-list");
  await user.click(screen.getByTestId("template-regulatory_report"));
  await user.click(screen.getByTestId("dataset-d1"));
  await user.type(screen.getByTestId("report-title-input"), "合规报告");
  if (options.toggleDepth) await user.click(screen.getByTestId("research-depth-toggle"));
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
    expect(await screen.findByText("你没有生成报告的权限。")).toBeInTheDocument();
  });

  it("walks template → dataset → create → outline-confirm → published with the artifact panel", async () => {
    const user = userEvent.setup();
    const { adapter } = makeFlowAdapter("succeeded");
    await createThroughConfigure(user, adapter);

    // Required mode: the confirm button POSTs the reviewed outline (header + each required section).
    await user.click(
      screen.getByRole("button", { name: "确认提纲并生成" }),
    );
    expect(adapter.confirmOutline).toHaveBeenCalledWith("rep1", 1, [
      { section_key: "overview", title: "概览", requires_evidence: true },
    ]);

    // Terminal job status lands on the published surface with the exports.
    expect(await screen.findByTestId("artifact-panel")).toBeInTheDocument();
    expect(screen.getByTestId("export-docx")).toBeInTheDocument();
    expect(screen.queryByTestId("report-cancel")).not.toBeInTheDocument();
  });

  it("offers the derived follow-up chips and sends the three keys on click", async () => {
    // A14: the chip is a *reference* to the report, not a copy of it — the click has
    // to carry id/revision/section and nothing else, because the server reads the
    // body from its own rows.
    const user = userEvent.setup();
    const { adapter } = makeFlowAdapter("succeeded");
    const onFollowUp = vi.fn();
    await createThroughConfigure(user, adapter, {}, { onFollowUp });
    await user.click(screen.getByRole("button", { name: "确认提纲并生成" }));

    const chips = await screen.findByTestId("report-follow-ups");
    expect(chips).toBeInTheDocument();
    expect(adapter.loadFollowUps).toHaveBeenCalledWith("rep1", expect.objectContaining({ revision: 1 }));

    await user.click(screen.getByTestId("report-followup-section_gap"));
    expect(onFollowUp).toHaveBeenCalledWith({
      text: "第 1 节《概览》为什么没有写成？补什么材料能补齐？",
      reportId: "rep1",
      revision: 1,
      sectionKey: "overview",
    });
  });

  it("renders no chip when there is no conversation to answer them", async () => {
    const user = userEvent.setup();
    const { adapter } = makeFlowAdapter("succeeded");
    await createThroughConfigure(user, adapter);
    await user.click(screen.getByRole("button", { name: "确认提纲并生成" }));
    await screen.findByTestId("artifact-panel");
    // Without a handler a chip would be a button that dead-ends.
    expect(screen.queryByTestId("report-follow-ups")).not.toBeInTheDocument();
    expect(adapter.loadFollowUps).not.toHaveBeenCalled();
  });

  it("sends standard depth unless the reviewer turns deep research on", async () => {
    // R18 (doc/295 §2.2): the depth is frozen at creation, so the create body is the
    // only place it can be chosen — a run created with the default must say so
    // explicitly rather than leave the server to guess.
    const user = userEvent.setup();
    const { adapter } = makeFlowAdapter(null);
    await createThroughConfigure(user, adapter);

    const body = (adapter.createReport as unknown as { mock: { calls: [Record<string, unknown>][] } })
      .mock.calls[0][0];
    expect(body.research_depth).toBe("standard");
  });

  it("offers deep research as an opt-in on the configure step", async () => {
    const user = userEvent.setup();
    const { adapter } = makeFlowAdapter(null);
    await createThroughConfigure(user, adapter, {}, { toggleDepth: true });

    const body = (adapter.createReport as unknown as { mock: { calls: [Record<string, unknown>][] } })
      .mock.calls[0][0];
    expect(body.research_depth).toBe("deep");
  });

  it("labels the depth toggle with the state it will send", async () => {
    const user = userEvent.setup();
    const { adapter } = makeFlowAdapter(null);
    render(
      <ReportWorkspace
        adapter={adapter}
        identity={{ tenant_id: "t1", tenant_name: "最佳实践工作区", id: "u1" }}
        onClose={vi.fn()}
      />,
    );
    await screen.findByTestId("template-list");
    await user.click(screen.getByTestId("template-regulatory_report"));
    await user.click(screen.getByTestId("dataset-d1"));

    // Default is the cheaper run, and the button says which one is active.
    const toggle = screen.getByTestId("research-depth-toggle");
    expect(toggle.textContent).toBe("标准检索");
    expect(toggle.getAttribute("aria-pressed")).toBe("false");
    await user.click(toggle);
    expect(screen.getByTestId("research-depth-toggle").textContent).toBe("深研检索");
    expect(screen.getByTestId("research-depth-toggle").getAttribute("aria-pressed")).toBe("true");
    // The hint is the honest half: it widens retrieval, it does not change the rules.
    expect(screen.getByText(/引用与核实规则不变/)).toBeTruthy();
  });

  it("records the conversation a run was started from, so the centre can jump back", async () => {
    // Live 2026-11-18: every report carried an empty `origin_chat_id`, so the report
    // centre's 「打开」 was permanently disabled with
    // "该报告没有关联对话记录，无法从这里打开".
    const user = userEvent.setup();
    const { adapter } = makeFlowAdapter(null);
    await createThroughConfigure(user, adapter, { chat: "chat-1", session: "session-9" });

    expect(adapter.createReport).toHaveBeenCalledWith(
      expect.objectContaining({ origin_chat_id: "chat-1", origin_session_id: "session-9" }),
    );
  });

  it("sends no origin when the workspace was opened outside a conversation", async () => {
    // An empty string is not an origin: storing one would make the centre build a
    // link to `targetId=`, which the centre then has to treat as "no conversation".
    const user = userEvent.setup();
    const { adapter } = makeFlowAdapter(null);
    await createThroughConfigure(user, adapter);

    const body = (adapter.createReport as unknown as { mock: { calls: [Record<string, unknown>][] } })
      .mock.calls[0][0];
    expect(body).not.toHaveProperty("origin_chat_id");
    expect(body).not.toHaveProperty("origin_session_id");
  });

  it("keeps a running job live with a cancel action and progress", async () => {
    const user = userEvent.setup();
    const { adapter } = makeFlowAdapter(null);
    await createThroughConfigure(user, adapter);

    await user.click(
      screen.getByRole("button", { name: "确认提纲并生成" }),
    );
    const cancel = await screen.findByTestId("report-cancel");
    // The progress strip names its stage in Chinese: it used to build
    // `conversationCenter.report_stage_<stage>`, a key family that does not exist.
    expect(screen.getByTestId("progress-step")).toHaveTextContent("撰写章节");
    expect(screen.queryByText(/section_(writing|retrieval)/)).toBeNull();
    await user.click(cancel);
    expect(adapter.cancelReport).toHaveBeenCalledWith("rep1");
  });

  it("writes the run badge and the footer in the reader's language", async () => {
    const user = userEvent.setup();
    const { adapter } = makeFlowAdapter(null);
    await createThroughConfigure(user, adapter);

    // The badge used to build `conversationCenter.report_job_<status>` — a key
    // family with zero entries — so it printed `awaiting_outline` verbatim.
    // The Badge primitive is stubbed here, so the assertion reads the text.
    expect(screen.getByText("待确认提纲")).toBeInTheDocument();
    // The footer used to print `tenant_id`: 32 hex chars where a name belongs.
    expect(screen.getByText(/工作区 最佳实践工作区 · 报告生成已纳入审计。/)).toBeInTheDocument();
  });

  it("falls back to the id when /me carries no workspace name", () => {
    // Older deployments answer `/me` without `tenant_name`; the line degrades to
    // the id instead of printing an empty label or "undefined".
    expect(workspaceName({ tenant_id: "t1" })).toBe("t1");
    expect(workspaceName({ tenant_name: "  ", tenant_id: "t1" })).toBe("t1");
    expect(workspaceName(null)).toBe("");
  });

  it("never rolls the badge back when an older read lands after the job finished", async () => {
    // Live 2026-11-18: the published surface showed the exports under a badge that
    // read 汇总成文. A snapshot read issued while the job was still finalizing came
    // back after the terminal frame and overwrote it, so the badge walked backwards
    // on a finished report. Two reads racing is normal; the state must not regress.
    const user = userEvent.setup();
    const { adapter, setStatus } = makeFlowAdapter("succeeded");
    await createThroughConfigure(user, adapter);
    await user.click(screen.getByRole("button", { name: "确认提纲并生成" }));
    expect(await screen.findByTestId("artifact-panel")).toBeInTheDocument();
    // The Badge primitive is stubbed above and drops its props, so the badge is
    // read by text.
    expect(screen.getByText("已生成")).toBeInTheDocument();

    // Two ways a finished report can be handed a non-terminal reading:
    //  1. the read was issued earlier, so it carries an older cursor;
    //  2. the read raced the final frame, so it carries the same cursor.
    const reads: Array<Record<string, unknown>> = [
      { ...snapshot("succeeded"), status: "finalizing", event_seq: 4, terminal: false },
      { ...snapshot("succeeded"), status: "finalizing", event_seq: 8, terminal: false },
    ];
    let next = 0;
    (adapter.loadSnapshot as unknown as { mockImplementation: (fn: () => Promise<unknown>) => void })
      .mockImplementation(async () => reads[next]);
    for (next = 0; next < reads.length; next++) {
      await user.click(screen.getByLabelText("refresh"));
      expect(screen.getByText("已生成")).toBeInTheDocument();
      expect(screen.queryByText("汇总成文")).toBeNull();
    }
    setStatus("succeeded");
  });

  it("maps a failed job snapshot to the failed banner", async () => {
    const user = userEvent.setup();
    const { adapter } = makeFlowAdapter("failed");
    await createThroughConfigure(user, adapter);
    await user.click(
      screen.getByRole("button", { name: "确认提纲并生成" }),
    );
    expect(await screen.findByText("报告任务失败，未产出已发布版本。")).toBeInTheDocument();
    expect(screen.queryByTestId("artifact-panel")).not.toBeInTheDocument();
  });
});
