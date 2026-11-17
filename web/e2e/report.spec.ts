import { expect, test, type Page } from "@playwright/test";

// Report workspace E2E (doc/288 T16): the report kind renders as a dedicated
// full-screen workspace reached via the `/report` slash command. The backend
// is fully mocked in-page; the same envelope contract the app unwraps:
//   CreateReportResult -> ReportJobSnapshot (mutable) -> outline confirm
//   release -> terminal snapshot -> published surface with artifact export.
interface Envelope {
  code: number;
  message: string;
  data: unknown;
}

function json(data: unknown): string {
  const body: Envelope = { code: 0, message: "", data };
  return JSON.stringify(body);
}

const ISO = "2026-11-17T10:00:00.000Z";

interface ReportFlowState {
  outlined: boolean;
}

function templateView() {
  return {
    template_id: "tpl-1",
    template_key: "regulatory_report",
    version: 1,
    tenant_id: "tenant-1",
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
        {
          key: "gaps",
          title: "合规缺口",
          prompt: "列出未达标项",
          requires_evidence: true,
          max_words: 800,
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
}

function createResult() {
  return {
    created: true,
    report: {
      id: "rep-rt-1",
      title: "合规报告",
      question: "合规报告",
      template_key: "regulatory_report",
      status: "running",
      access_policy: "owner_only",
      current_revision_id: "rev-rt-1",
      created_at: ISO,
      updated_at: ISO,
    },
    revision: {
      id: "rev-rt-1",
      report_id: "rep-rt-1",
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
      created_at: ISO,
    },
    job: { id: "jobrt-1", status: "queued", job_kind: "report" },
  };
}

function snapshot(state: ReportFlowState) {
  const terminal = state.outlined;
  return {
    job_id: "jobrt-1",
    report_id: "rep-rt-1",
    job_kind: "report",
    status: terminal ? "succeeded" : "running",
    stage: "writing",
    attempt_seq: 1,
    slice_seq: 0,
    progress_done: terminal ? 5 : 2,
    progress_total: 5,
    event_seq: terminal ? 9 : 1,
    terminal,
    cancel_requested_at: null,
    error_code: "",
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
      id: "rev-rt-1",
      revision: 1,
      status: "draft",
      completeness: "pending",
      outline_confirmation: "required",
      template_key: "regulatory_report",
      template_version: 1,
      execution_snapshot_hash: "abc",
      metrics: {},
      published_at: null,
      superseded_at: null,
    },
    sections: [],
    gaps: [],
  };
}

async function mockReportBackend(page: Page, state: ReportFlowState): Promise<void> {
  await page.route("**/api/v1/**", async (route) => {
    const method = route.request().method();
    const path = new URL(route.request().url()).pathname;

    if (method === "POST" && path === "/api/v1/auth/login") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        headers: { "Set-Cookie": "rgx_access_token=test-token; Path=/" },
        body: json({ token: "test-token", refresh_token: "test-refresh" }),
      });
      return;
    }
    if (path === "/api/v1/auth/me") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: json({
          id: "user-1",
          username: "operator",
          tenant_id: "tenant-1",
          role: "member",
          platform: true,
          permissions: [{ resource: "*", action: "*", effect: "allow" }],
        }),
      });
      return;
    }
    if (path === "/api/v1/branding/public" || path === "/api/v1/branding") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ name: "RAGFlow-X", logo: "" }) });
      return;
    }
    if (path === "/api/v1/system/setup/status") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ initialized: true }) });
      return;
    }
    if (path === "/api/v1/conversation/assistants" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [], total: 0 }) });
      return;
    }
    if (path === "/api/v1/chats" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [], total: 0 }) });
      return;
    }

    // Report surface.
    if (path === "/api/v1/report-templates" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json([templateView()]) });
      return;
    }
    if (path.startsWith("/api/v1/datasets") && method === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: json([{ id: "d-docs", name: "知识库文档" }]),
      });
      return;
    }
    if (path === "/api/v1/reports" && method === "POST") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json(createResult()) });
      return;
    }
    if (path === "/api/v1/report-jobs/jobrt-1" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json(snapshot(state)) });
      return;
    }
    if (path === "/api/v1/reports/rep-rt-1/cancel" && method === "POST") {
      await route.fulfill({ status: 202, contentType: "application/json", body: json(null) });
      return;
    }
    if (path === "/api/v1/reports/rep-rt-1/revisions/1/outline/confirm" && method === "POST") {
      state.outlined = true;
      await route.fulfill({ status: 200, contentType: "application/json", body: json(null) });
      return;
    }
    // The poller re-subscribes while the job runs; keep the stream quiet.
    if (path === "/api/v1/report-jobs/jobrt-1/events" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "text/event-stream", body: "" });
      return;
    }

    await route.fulfill({ status: 200, contentType: "application/json", body: json(null) });
  });
}

test("report workspace runs template → outline → publish with artifact export", async ({ page }) => {
  const state: ReportFlowState = { outlined: false };
  await mockReportBackend(page, state);
  await page.goto("/#/conversation-center");

  // Reach the workspace through the `/report` slash command.
  await page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标").fill("/report");
  await page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标").press("Enter");
  await expect(page.getByTestId("template-catalog")).toBeVisible();

  // Template → configure → datasets → create.
  await page.getByTestId("template-regulatory_report").click();
  await page.getByTestId("dataset-d-docs").click();
  await page.getByTestId("report-title-input").fill("合规报告");
  await page.getByTestId("report-create").click();

  // Required outline confirmation surfaces the editor.
  await expect(page.getByTestId("outline-editor")).toBeVisible();
  await page.getByRole("button", { name: "确认提纲并生成" }).click();

  // The released job turns terminal; the published surface mounts the exports.
  await expect(page.getByTestId("artifact-panel")).toBeVisible();
  await expect(page.getByTestId("export-docx")).toBeVisible();
  await expect(page.getByTestId("report-cancel")).toHaveCount(0);
});
