import { expect, type Page, test } from "@playwright/test";

interface Envelope { code: number; message: string; data: unknown; }

const pageEnvelope = (items: unknown[]) => ({ items, total: items.length });

async function login(page: Page): Promise<void> {
  let authenticated = false;
  const json = (data: unknown, status = 200) => ({
    status,
    contentType: "application/json",
    body: JSON.stringify({ code: 0, message: "", data } satisfies Envelope),
  });

  await page.route("**/api/v1/**", async (route) => {
    const method = route.request().method();
    const path = new URL(route.request().url()).pathname;

    if (path.endsWith("/branding/public") || path.endsWith("/branding")) {
      await route.fulfill(json({ name: "RAGFlow-X", logo: "" }));
      return;
    }
    if (path.endsWith("/system/setup/status")) {
      await route.fulfill(json({ initialized: true }));
      return;
    }
    if (path.endsWith("/system/health")) {
      await route.fulfill(json({ status: "ok", version: "test" }));
      return;
    }
    if (method === "POST" && path.endsWith("/auth/login")) {
      authenticated = true;
      await route.fulfill({
        status: 200, contentType: "application/json",
        headers: { "Set-Cookie": "rgx_access_token=test-token; Path=/" },
        body: JSON.stringify({ code: 0, message: "", data: { token: "test-token", refresh_token: "test-refresh" } }),
      });
      return;
    }
    if (path.endsWith("/auth/me")) {
      if (!authenticated) {
        await route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ code: 401, message: "unauthenticated" }) });
        return;
      }
      await route.fulfill(json({
        id: "user-1", username: "admin", tenant_id: "tenant-1", role: "platform_admin",
        platform: true, permissions: [{ resource: "*", action: "*", effect: "allow" }],
      }));
      return;
    }
  if (path === "/api/v1/scenario-template-assets" && method === "GET") {
      await route.fulfill(json(pageEnvelope([])));
      return;
    }
    if (path === "/api/v1/prompt-policies" && method === "GET") {
      await route.fulfill(json(pageEnvelope([])));
      return;
    }
    if (path === "/api/v1/knowledge-lifecycle" && method === "GET") {
      await route.fulfill(json([{
        id: "dataset-1", name: "Contract Knowledge", owner_id: "", owner_team_id: "",
        source_type: "ragflow", business_domain: "legal", sensitivity: "internal",
        review_status: "approved", quality_score: 93, lifecycle_status: "current",
      }]));
      return;
    }
    if (path === "/api/v1/knowledge-lifecycle/health") {
      await route.fulfill(json({
        dataset_count: 1, lifecycle: { current: 1, due: 0, expired: 0, missing_owner: 0, unreviewed: 0 },
        parse: { task_count: 1, done: 1, running: 0, queued: 0, failed: 0, stopped: 0, ready_rate: 1 },
        average_quality_score: 93, low_quality_count: 0, missing_classification: 0,
        citation_missing_count: 0, citation_window_days: 30,
      }));
      return;
    }
    if (path === "/api/v1/knowledge-lifecycle/asset-map") {
      await route.fulfill(json([{
        id: "dataset-1", name: "Contract Knowledge", owner_id: "", owner_team_id: "",
        source_type: "ragflow", business_domain: "legal", sensitivity: "internal",
        review_status: "approved", quality_score: 93, lifecycle_status: "current",
        assistant_ids: ["assistant-1"], assistant_release_ids: ["release-1"],
        trace_runs: [{ trace_id: "trace-1", request_id: "request-1", assistant_id: "assistant-1", app_type: "chat", channel: "web", status: "COMPLETED" }],
        knowledge_tasks: [{ id: "task-1", title: "Refresh missing policy", category: "knowledge", status: "open", priority: "high", owner_id: "user-1", source_chunk_id: "chunk-1", source_document_version: "v3", regression_status: "PENDING", created_at: "2026-09-20T08:05:00Z" }],
        chunk_evidence: [{ answer_snapshot_id: "snapshot-1", request_id: "request-1", citation_id: "citation-1", chunk_id: "chunk-1", document_id: "document-1", document_version: "v3", citation_locator: "policy.md#L12", citation_hash: "hash-policy", created_at: "2026-09-01T00:00:00Z" }],
      }]));
      return;
    }
    if (path.startsWith("/api/v1/knowledge-ops?") || path === "/api/v1/knowledge-ops") {
      await route.fulfill(json({
        total_turns: 12, active_users: 3, active_sessions: 4, completed: 9, no_answer: 2, failed: 1,
        with_citations: 8, tokens_in: 100, tokens_out: 200, positive: 8, negative: 3,
        attribution_summary: { knowledge: 2, retrieval: 1 },
        citation_rate: 0.89, no_answer_rate: 0.22, failure_rate: 0.11,
        satisfaction_rate: 0.73, avg_latency_ms: 620, avg_resolution_hours: 18,
      }));
      return;
    }
    if (path.startsWith("/api/v1/knowledge-ops/top-queries")) {
      await route.fulfill(json([{ question: "contract update evidence gap", requests: 5, last_asked_at: "2026-09-20T08:00:00Z", no_answer_count: 1, failed_count: 0, citation_missing_count: 1, avg_latency_ms: 640 }]));
      return;
    }
    if (path.startsWith("/api/v1/knowledge-ops/events")) {
      await route.fulfill(json(pageEnvelope([{ id: "event-1", request_id: "request-1", app_type: "chat", app_id: "chat-1", question: "contract update evidence gap", status: "no_answer", citations_count: 0, duration_ms: 600, tokens_in: 20, tokens_out: 40, created_at: "2026-09-20T08:00:00Z", review_status: "open", feedback_rating: "negative", feedback_attribution: "knowledge" }])));
      return;
    }
    if (path === "/api/v1/knowledge-tasks/summary") {
      await route.fulfill(json({ open: 1, in_progress: 0, blocked: 0, pending_approval: 0, resolved: 0, canceled: 0, overdue: 0 }));
      return;
    }
    if (path === "/api/v1/knowledge-tasks") {
      await route.fulfill(json(pageEnvelope([{ id: "task-1", source_event_id: "event-1", source_request_id: "request-1", source_attribution: "knowledge", title: "Refresh missing policy", description: "Add the 2026 contract update", category: "knowledge", owner_id: "user-1", priority: "high", status: "open", requires_approval: false, regression_status: "PENDING", created_at: "2026-09-20T08:05:00Z" }])));
      return;
    }
    if (path.startsWith("/api/v1/eval-sets")) {
      await route.fulfill(json(pageEnvelope([{ id: "eval-set-1", name: "policy-regression", app_type: "chat", item_count: 3, status: "ACTIVE" }])));
      return;
    }
    if (path.startsWith("/api/v1/release-candidates")) {
      await route.fulfill(json(pageEnvelope([{ candidate_id: "candidate-1", candidate_version: 1, target_type: "chat", target_id: "chat-1", target_version: "v3", base_version: "v2", change_summary: "contract policy update", candidate_hash: "hash-candidate", status: "READY", created_at: "2026-09-20T09:00:00Z" }])));
      return;
    }
    if (path.startsWith("/api/v1/execution-snapshots")) {
      await route.fulfill(json(pageEnvelope([{ id: "snapshot-rel-1", release_candidate_id: "candidate-1", candidate_version: 1, snapshot_schema_version: "v1", snapshot_hash: "hash-execution", execution_config: "{}", created_at: "2026-09-20T09:05:00Z" }])));
      return;
    }
    if (path.startsWith("/api/v1/evaluation-runs")) {
      await route.fulfill(json(pageEnvelope([{ id: "eval-run-1", release_candidate_id: "candidate-1", candidate_version: 1, status: "SUCCEEDED", eval_set_id: "eval-set-1", eval_set_version: 1, eval_set_hash: "hash-eval", pass: true, created_at: "2026-09-20T09:10:00Z" }])));
      return;
    }
    if (path.startsWith("/api/v1/evidence-bundles")) {
      await route.fulfill(json(pageEnvelope([{ id: "evidence-1", release_candidate_id: "candidate-1", candidate_version: 1, evaluation_run_id: "eval-run-1", snapshot_id: "snapshot-rel-1", snapshot_hash: "hash-snapshot", created_at: "2026-09-20T09:15:00Z" }])));
      return;
    }
    if (path.startsWith("/api/v1/release-gates")) {
      await route.fulfill(json(pageEnvelope([{ id: "gate-1", release_candidate_id: "candidate-1", candidate_version: 1, evidence_bundle_id: "evidence-1", environment: "pilot", decision: "PASS", reason: "all gates satisfied", active_gate: true, created_at: "2026-09-20T09:20:00Z" }])));
      return;
    }
    if (path.startsWith("/api/v1/releases")) {
      await route.fulfill(json(pageEnvelope([{ id: "release-1", release_candidate_id: "candidate-1", candidate_version: 1, snapshot_id: "snapshot-rel-1", gate_decision_id: "gate-1", environment: "pilot", status: "RELEASED", rollback_baseline: "v2", created_at: "2026-09-20T09:25:00Z" }])));
      return;
    }
    await route.fulfill(json([]));
  });

  await page.goto("/");
  await page.getByRole("textbox", { name: "\u7528\u6237\u540d" }).fill("admin");
  await page.getByRole("textbox", { name: "\u5bc6\u7801" }).fill("admin123");
  await page.getByRole("button", { name: "\u767b\u5f55" }).click();
  await expect(page.locator('main[aria-label="\u4e3b\u5185\u5bb9\u533a\u57df"]')).toBeVisible();
}

test("S23 knowledge operations journey remains connected", async ({ page }) => {
  await login(page);

  await page.goto("/#/asset-governance");
  await page.waitForLoadState("networkidle");
  await page.getByRole("tab", { name: "\u77e5\u8bc6\u8d44\u4ea7\u5730\u56fe" }).click();
  await expect(page.getByText("Contract Knowledge")).toBeVisible();
  await expect(page.getByText("chunk-1")).toBeVisible();
  await expect(page.getByText("hash-policy")).toBeVisible();

  await page.getByRole("tab", { name: "\u8bc4\u6d4b\u96c6" }).click();
  await expect(page.getByText("policy-regression")).toBeVisible();

  await page.goto("/#/knowledge-ops");
  await expect(page.getByText("contract update evidence gap").first()).toBeVisible();
  await expect(page.getByText("Refresh missing policy")).toBeVisible();
  await expect(page.getByText("\u77e5\u8bc6").first()).toBeVisible();

  await page.goto("/#/release-governance");
  await expect(page.getByRole("combobox", { name: "\u9009\u62e9\u5019\u9009\u7248\u672c" })).toContainText("candidate-1 v1");
  await page.getByRole("tab", { name: "\u8bc4\u4f30\u8fd0\u884c" }).click();
  await expect(page.getByText("eval-run-1")).toBeVisible();
  await page.getByRole("tab", { name: "\u8bc1\u636e / \u95e8\u7981" }).click();
  await expect(page.getByText("evidence-1")).toBeVisible();
  await expect(page.getByText("gate-1")).toBeVisible();
  await page.getByRole("tab", { name: "\u53d1\u5e03" }).click();
  await expect(page.getByText("release-1")).toBeVisible();
});
