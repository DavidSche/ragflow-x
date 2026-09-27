// ScenarioID: SC-SYNC-001
// ScenarioID: SC-AUDIT-002
import { expect, type Page, test } from "@playwright/test";

interface Envelope {
  code: number;
  message: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  data: any;
}

const syncSetting = {
  enabled: true,
  scheduled_reconcile_enabled: false,
  resource_types: ["dataset"],
  interval_seconds: 900,
  batch_size: 100,
  max_resources: 5000,
  deletion_confirmations: 3,
  default_target_tenant_id: "tenant-1",
  default_owner_id: "",
};

  async function mockBackend(page: Page): Promise<void> {
  let authenticated = false;
  const json = (data: unknown, status = 200) => ({
    status,
    contentType: "application/json",
    body: JSON.stringify({ code: 0, message: "", data } satisfies Envelope),
  });

  await page.route("**/api/v1/**", async (route) => {
    const method = route.request().method();
    const path = new URL(route.request().url()).pathname;

    if (method === "POST" && path.endsWith("/auth/login")) {
      authenticated = true;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
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
        id: "user-1",
        username: "admin",
        tenant_id: "tenant-1",
        role: "platform_admin",
        platform: true,
        permissions: [{ resource: "*", action: "*", effect: "allow" }],
      }));
      return;
    }
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
    if (path.endsWith("/system/config/public-key")) {
      await route.fulfill(json({ public_key: "test-key" }));
      return;
    }
    if (path === "/api/v1/system/settings") {
      await route.fulfill(json({
        revision: 1,
        desired_revision_id: "revision-1",
        deployment_effective_state: "applied",
        groups: {},
        runtime_instances: [],
        secret_references: {},
      }));
      return;
    }
    if (path === "/api/v1/system/settings/revisions") {
      await route.fulfill(json({ items: [], total: 0 }));
      return;
    }
    if (path.endsWith("/system/config")) {
      await route.fulfill(json({
        configured: true,
        database: { driver: "postgres", host: "localhost", port: 5432, user: "postgres", name: "ragflow-x", sslmode: "disable", has_password: true },
        ragflow: { provider: "http", base_url: "http://ragflow.test", timeout: 30, max_conns: 20, has_api_key: true },
        redis: { enabled: false, addr: "", username: "", db: 0, pool_size: 20, has_password: false },
      }));
      return;
    }
    if (path === "/api/v1/system/ragflow/sync/settings") {
      await route.fulfill(json(syncSetting));
      return;
    }
    if (path === "/api/v1/system/ragflow/sync/mappings") {
      await route.fulfill(json([]));
      return;
    }
    if (path === "/api/v1/system/ragflow/sync/runs") {
      await route.fulfill(json({ items: [{
        id: "sync-run-1",
        trigger_type: "manual_import",
        status: "planned",
        scan_consistency: "page_scan_approximate",
        deletion_safe: false,
        plan_summary_json: "{}",
        result_summary_json: "{}",
        created_at: "2026-09-01T00:00:00Z",
      }], total: 1 }));
      return;
    }
    if (path === "/api/v1/system/ragflow/sync/runs/sync-run-1/items") {
      await route.fulfill(json({ items: [{
        id: "sync-item-1",
        resource_type: "dataset",
        external_tenant_id: "__GLOBAL__",
        external_id: "rag-1",
        local_id: "dataset-1",
        tenant_id: "tenant-1",
        owner_id: "",
        action: "create",
        status: "pending",
        conflict_type: "",
        sync_state: "pending",
        upstream_last_synced_hash: "",
        upstream_current_hash: "upstream-current",
        local_last_synced_hash: "",
        local_current_hash: "local-current",
        error: "",
      }], total: 1 }));
      return;
    }
    if (path === "/api/v1/system/ragflow/sync/resources") {
      await route.fulfill(json({ items: [{
        id: "binding-1",
        resource_type: "dataset",
        external_tenant_id: "__GLOBAL__",
        external_id: "rag-1",
        local_id: "dataset-1",
        tenant_id: "tenant-1",
        binding_lifecycle: "active",
        governance_state: "normal",
        conflict_type: "",
      }], total: 1 }));
      return;
    }
    if (path === "/api/v1/alerts/deliveries") {
      await route.fulfill(json({ items: [{
        id: "alert-delivery-1",
        alert_event_id: "alert-event-1",
        channel: "webhook",
        status: "pending",
        attempts: 1,
        last_error: "",
        last_attempt_at: "2026-09-01T00:00:00Z",
        next_retry_at: "2026-09-01T00:05:00Z",
        lease_owner: "worker-1",
        lease_expires_at: "2026-09-01T00:05:00Z",
      }], total: 1 }));
      return;
    }
    if (path === "/api/v1/alerts") {
      await route.fulfill(json({ items: [], total: 0 }));
      return;
    }

    await route.fulfill(json(null));
  });
}

async function login(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("textbox", { name: "用户名" }).fill("admin");
  await page.getByRole("textbox", { name: "密码" }).fill("admin123");
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.locator('main[aria-label="主内容区域"]')).toBeVisible();
}

test("navigates RAGFlow resource sync and alert delivery views", async ({ page }) => {
  await mockBackend(page);
  await login(page);

  await page.goto("/#/alert-deliveries");
  await expect(page.getByRole("table").filter({ hasText: "alert-event-1" })).toBeVisible();
  await expect(page.getByText("alert-event-1")).toBeVisible();
  if ((page.viewportSize()?.width ?? 0) >= 1280) {
    await expect(page.getByText("worker-1")).toBeVisible();
  }

  await page.goto("/#/system");
  await page.getByRole("tab", { name: "RAGFlow 资源同步" }).click();
  await expect(page.getByRole("heading", { name: "RAGFlow 资源同步" })).toBeVisible();
  await page.getByRole("tab", { name: "运行与明细" }).click();
  await expect(page.getByText("计划明细")).toBeVisible();
  await expect(page.getByText("共 1 项 · 待处理 1 · 成功 0 · 失败 0")).toBeVisible();
  await expect(page.getByRole("row", { name: /dataset · create/ })).toBeVisible();
});
