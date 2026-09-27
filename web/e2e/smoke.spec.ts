import { expect, type Page, test } from "@playwright/test";

// Self-contained smoke: every /api/v1/** request is mocked so the test needs
// no live ragflow-x backend. Covers the A4 acceptance path "登录 → 首页可达".

interface Envelope {
  code: number;
  message: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  data: any;
}

async function mockBackend(page: Page): Promise<void> {
  await page.route("**/api/v1/**", async (route) => {
    const method = route.request().method();
    const path = new URL(route.request().url()).pathname;
    let body: Envelope = { code: 0, message: "", data: null };
    if (method === "POST" && path.endsWith("/auth/login")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        headers: { "Set-Cookie": "rgx_access_token=test-token; Path=/" },
        body: JSON.stringify({ code: 0, message: "", data: { token: "test-token", refresh_token: "test-refresh" } }),
      });
      return;
    } else if (path.endsWith("/auth/me")) {
      if (!route.request().headers().cookie?.includes("rgx_access_token=test-token")) {
        await route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ code: 401, message: "unauthorized" }) });
        return;
      }
      body = {
        code: 0,
        message: "",
        data: {
          id: "u1",
          username: "admin",
          tenant_id: "t1",
          role: "admin",
          platform: true,
          permissions: [{ resource: "*", action: "*", effect: "allow" }],
        },
      };
    } else if (path.endsWith("/branding/public") || path.endsWith("/branding")) {
      body = { code: 0, message: "", data: { name: "RAGFlow-X", logo: "" } };
    } else if (path.endsWith("/system/setup/status")) {
      body = { code: 0, message: "", data: { initialized: true } };
    } else if (path.endsWith("/system/health")) {
      body = { code: 0, message: "", data: { status: "ok", version: "test" } };
    } else if (path.endsWith("/usage/summary")) {
      body = { code: 0, message: "", data: { total_tokens: 0, requests: 0, cost: 0 } };
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  });
}

test("logs in and renders the authenticated shell", async ({ page }) => {
  await mockBackend(page);

  await page.goto("/");
  await expect(page.getByRole("button", { name: "登录" })).toBeVisible();

  await page.getByRole("textbox", { name: "用户名" }).fill("admin");
  await page.getByRole("textbox", { name: "密码" }).fill("admin123");
  await page.getByRole("button", { name: "登录" }).click();

  // Authenticated shell: main content area + sidebar brand + workbench entry.
  await expect(page.locator('main[aria-label="主内容区域"]')).toBeVisible();
  if ((page.viewportSize()?.width ?? 0) >= 1024) {
    await expect(page.getByRole("link", { name: "问答工作台" })).toBeVisible();
  }
  if ((page.viewportSize()?.width ?? 0) >= 1024) {
    await expect(page.getByRole("link", { name: "RAGFlow-X" })).toBeVisible();
  }
});
