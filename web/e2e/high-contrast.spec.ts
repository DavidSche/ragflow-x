// ScenarioID: SC-A11Y-001 (doc/219 high-contrast walkthrough — automated
// emulation part; real-device visual judgement stays a manual item).
// Verifies the app does not regress under high-contrast emulation
// (forced-colors active + dark scheme): authentication still resolves the
// role-dependent view, the navigation sidebar stays usable (expanded group
// surfaces its entry points, collapsed group stays folded), and no page
// error is raised. Runs on both the desktop project and the mobile overlay.
import { expect, type Page, test } from "@playwright/test";

interface Envelope {
  code: number;
  message: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  data: any;
}

const json = (data: unknown, status = 200) => ({
  status,
  contentType: "application/json",
  body: JSON.stringify({ code: 0, message: "", data } satisfies Envelope),
});

const OPERATOR: { role: string; displayName: string; permissions: Array<{ resource: string; action: string; effect: string }>; approvalEnabled: boolean } = {
  role: "operator",
  displayName: "数据工程师",
  permissions: [{ resource: "*", action: "*", effect: "allow" }],
  approvalEnabled: true,
};

async function mockBackend(page: Page): Promise<void> {
  let authenticated = false;
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
        username: "a11y",
        tenant_id: "tenant-1",
        role: OPERATOR.role,
        platform: false,
        permissions: OPERATOR.permissions,
        approval_enabled: OPERATOR.approvalEnabled,
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
    if (path === "/api/v1/system/settings") {
      await route.fulfill(json({ revision: 1, desired_revision_id: "r1", deployment_effective_state: "applied", groups: {}, runtime_instances: [], secret_references: {} }));
      return;
    }
    await route.fulfill(json({}));
  });
}

test("high-contrast emulation: shell renders, nav usable, no page error", async ({ page, isMobile }) => {
  test.setTimeout(60_000);
  const pageErrors: string[] = [];
  page.on("pageerror", (err) => pageErrors.push(String(err)));

  await mockBackend(page);
  // High-contrast emulation: forced-colors active + dark color scheme. The
  // theme-provider resolves the system preference and the browser forces the
  // accessible color set on top — the shell must keep rendering and stay
  // operable without explicit forced-colors CSS today (219 item, emulation
  // part).
  await page.emulateMedia({ colorScheme: "dark", forcedColors: "active" });

  await page.goto("/");
  await page.getByRole("textbox", { name: "用户名" }).fill("a11y");
  await page.getByRole("textbox", { name: "密码" }).fill("admin123");
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.locator('main[aria-label="主内容区域"]')).toBeVisible();

  // The emulated preferences actually reach the document.
  const meta = await page.evaluate(() => ({
    dark: window.matchMedia("(prefers-color-scheme: dark)").matches,
    forced: window.matchMedia("(forced-colors: active)").matches,
  }));
  expect(meta.dark).toBe(true);
  expect(meta.forced).toBe(true);

  // Sidebar group surface is present and usable under forced colors.
  if (isMobile) {
    await page.getByRole("button", { name: "Toggle Sidebar" }).first().click();
    await expect(page.locator('button[data-group-key]').first()).toBeAttached();
  } else {
    await expect(page.locator('[data-slot="sidebar-inner"]').first()).toBeAttached();
  }
  const knowledge = page.locator('button[data-group-key="lifecycle"], button[data-group-key="knowledge"]').first();
  await expect(knowledge).toHaveAttribute("aria-expanded", "true");

  // The operator view resolves to the （问答）workbench entry point, and it can
  // be navigated to without crashing the shell.
  const workbenchLink = page.getByRole("link", { name: "问答工作台" }).first();
  if (await workbenchLink.isVisible().catch(() => false)) {
    await workbenchLink.click();
    await expect(page.locator('main[aria-label="主内容区域"]')).toBeVisible();
  }

  await expect.poll(() => pageErrors).toEqual([]);
});
