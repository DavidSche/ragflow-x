import AxeBuilder from "@axe-core/playwright";
import { expect, type Page, test } from "@playwright/test";

interface Envelope {
  code: number;
  message: string;
  data: unknown;
}

const asset = {
  id: "dataset-1", name: "Contract Knowledge", owner_id: "", owner_team_id: "",
  source_type: "ragflow", business_domain: "legal", sensitivity: "internal",
  expires_at: "2026-12-31T00:00:00Z", review_status: "approved", quality_score: 92,
  lifecycle_status: "current", assistant_ids: ["assistant-1"], assistant_release_ids: ["release-1"],
  trace_runs: [], chunk_evidence: [{
    answer_snapshot_id: "snapshot-1", request_id: "request-1", citation_id: "citation-1",
    chunk_id: "chunk-1", document_id: "document-1", document_version: "v1",
    citation_locator: "contract.md#L1", citation_hash: "hash", created_at: "2026-09-01T00:00:00Z",
  }],
  knowledge_tasks: [],
};

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
      await route.fulfill(json({ items: [], total: 0 }));
      return;
    }
    if (path === "/api/v1/prompt-policies" && method === "GET") {
      await route.fulfill(json({ items: [], total: 0 }));
      return;
    }
    if (path === "/api/v1/knowledge-lifecycle" && method === "GET") {
      await route.fulfill(json([asset]));
      return;
    }
    if (path === "/api/v1/knowledge-lifecycle/health") {
      await route.fulfill(json({
        dataset_count: 1, lifecycle: { current: 1, due: 0, expired: 0, missing_owner: 0, unreviewed: 0 },
        parse: { task_count: 1, done: 1, running: 0, queued: 0, failed: 0, stopped: 0, ready_rate: 1 },
        average_quality_score: 92, low_quality_count: 0, missing_classification: 0,
        citation_missing_count: 0, citation_window_days: 30,
      }));
      return;
    }
    if (path === "/api/v1/knowledge-lifecycle/asset-map") {
      await route.fulfill(json([asset]));
      return;
    }
    if (path === "/api/v1/eval-sets" && method === "GET") {
      await route.fulfill(json({ items: [], total: 0 }));
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

async function openAssetMap(page: Page): Promise<void> {
  await page.goto("/#/asset-governance");
  await page.getByRole("tab", { name: "\u77e5\u8bc6\u8d44\u4ea7\u5730\u56fe" }).click();
  await expect(page.getByRole("textbox", { name: "\u6309\u540d\u79f0\u6216\u4e1a\u52a1\u57df\u7b5b\u9009" })).toBeVisible();
  await expect(page.getByText("Contract Knowledge")).toBeVisible();
  await expect(page.getByText("chunk-1")).toBeVisible();
}

test("asset map passes accessibility and keyboard focus checks", async ({ page }) => {
  test.skip((page.viewportSize()?.width ?? 0) < 640, "focus order is validated on the desktop matrix");
  await login(page);
  await openAssetMap(page);

  const scan = await new AxeBuilder({ page }).analyze();
  const actionable = scan.violations.filter((violation) => ["critical", "serious"].includes(violation.impact ?? ""));
  expect(actionable).toEqual([]);

  const search = page.getByRole("textbox", { name: "\u6309\u540d\u79f0\u6216\u4e1a\u52a1\u57df\u7b5b\u9009" });
  await page.getByRole("tab", { name: "\u77e5\u8bc6\u8d44\u4ea7\u5730\u56fe" }).focus();
  for (let attempt = 0; attempt < 10; attempt += 1) {
    await page.keyboard.press("Tab");
    if (await search.evaluate((element) => element === document.activeElement)) break;
  }
  await expect(search).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(page.getByRole("combobox", { name: "\u6309\u751f\u547d\u5468\u671f\u7b5b\u9009" })).toBeFocused();
});

test("asset map remains operable at a mobile viewport", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await login(page);
  await openAssetMap(page);

  const scan = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag21aa", "wcag22aa"]).analyze();
  const actionable = scan.violations.filter((violation) => ["critical", "serious"].includes(violation.impact ?? ""));
  expect(actionable).toEqual([]);
  await expect(page.getByRole("textbox", { name: "\u6309\u540d\u79f0\u6216\u4e1a\u52a1\u57df\u7b5b\u9009" })).toBeInViewport();
  await expect(page.getByText("Contract Knowledge")).toBeInViewport();
});
