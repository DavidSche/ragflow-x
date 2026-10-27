// ScenarioID: SC-NAV-001 … SC-NAV-005 (doc/223 §8 T8 — five-role navigation
// walkthrough). Renders the app sidebar under each built-in role view and
// asserts the role-specific default-collapse contract
// (NAVIGATION_VIEW_DEFAULT_EXPANDED) plus group usability (an expanded group
// surfaces its resource entry points).
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

interface Identity {
  role: string;
  displayName: string;
  permissions: Array<{ resource: string; action: string; effect?: string }>;
  approvalEnabled?: boolean;
}

const ROLES: Array<{ case: string; identity: Identity; expect: Record<string, boolean> }> = [
  {
    case: "platform admin",
    identity: { role: "platform_admin", displayName: "平台管理员", permissions: [{ resource: "*", action: "*", effect: "allow" }] },
    expect: { workspace: true, governance: true, audit: true, "assistant-apps": false, lifecycle: false, gateway: false },
  },
  {
    case: "business admin",
    identity: { role: "tenant_admin", displayName: "业务管理员", permissions: [{ resource: "*", action: "*", effect: "allow" }] },
    expect: { workspace: true, governance: true, knowledge: false, audit: false },
  },
  {
    case: "data engineer",
    identity: { role: "operator", displayName: "数据工程师", permissions: [{ resource: "*", action: "*", effect: "allow" }] },
    expect: { knowledge: true, lifecycle: true, "assistant-apps": false, interaction: false, "access-usage": false, audit: false, system: false },
  },
  {
    case: "business user",
    identity: { role: "business_user", displayName: "业务用户", permissions: [{ resource: "*", action: "*", effect: "allow" }] },
    expect: { "assistant-apps": true, interaction: true, knowledge: false },
  },
  {
    case: "auditor",
    identity: { role: "auditor", displayName: "审计员", permissions: [{ resource: "*", action: "*", effect: "allow" }] },
    expect: { audit: true, governance: true, workspace: false, knowledge: false },
  },
];

async function mockBackend(page: Page, identity: Identity): Promise<void> {
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
        username: "walkthrough",
        tenant_id: "tenant-1",
        role: identity.role,
        platform: identity.role === "platform_admin",
        permissions: identity.permissions,
        approval_enabled: identity.approvalEnabled ?? true,
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
    if (path.endsWith("/audit")) {
      await route.fulfill(json({ items: [], total: 0 }));
      return;
    }
    await route.fulfill(json({}));
  });
}

async function login(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("textbox", { name: "用户名" }).fill("walkthrough");
  await page.getByRole("textbox", { name: "密码" }).fill("admin123");
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.locator('main[aria-label="主内容区域"]')).toBeVisible();
}

// Phase 1 (both projects): attribute-level collapse contract — direct DOM,
// independent of the sidebar width state.
async function expectGroupAttribute(page: Page, groupKey: string, expanded: boolean): Promise<void> {
  const btn = page.locator(`button[data-group-key="${groupKey}"]`);
  await expect(btn).toHaveAttribute("aria-expanded", String(expanded));
}

for (const { case: label, identity, expect: expectMap } of ROLES) {
  test(`sidebar default-collapse walkthrough: ${label}`, async ({ page, isMobile }) => {
    test.setTimeout(60_000);
    await mockBackend(page, identity);
    await login(page);

    // Mobile renders the sidebar in a closed overlay; open it so the group
    // toggles are in the DOM, then assert the attribute-level contract.
    if (isMobile) {
      await page.getByRole("button", { name: "Toggle Sidebar" }).first().click();
      await expect(page.locator('button[data-group-key]').first()).toBeAttached();
    } else {
      await expect(page.locator('[data-slot="sidebar-inner"]').first()).toBeAttached();
    }

    // Default-collapse contract resolved from NAVIGATION_VIEW_DEFAULT_EXPANDED.
    for (const [groupKey, expanded] of Object.entries(expectMap)) {
      await expectGroupAttribute(page, groupKey, expanded);
    }

    // Desktop usability: the floating sidebar is expanded by default, so the
    // expanded groups are visible with entry points, collapsed groups stay
    // folded, and a manual fold/expand is respected (usable navigation under
    // each role).
    if (!isMobile) {
      for (const [groupKey, expanded] of Object.entries(expectMap)) {
        const content = page.locator(`[data-sidebar-group-content="${groupKey}"]`);
        if (expanded) {
          await expect(content).toBeVisible();
        } else {
          await expect(content).toHaveCount(0);
        }
      }

      const collapsed = Object.entries(expectMap).find(([, expanded]) => !expanded);
      if (collapsed) {
        const [collapseKey] = collapsed;
        const btn = page.locator(`button[data-group-key="${collapseKey}"]`);
        await btn.click();
        await expect(page.locator(`[data-sidebar-group-content="${collapseKey}"]`)).toBeVisible();
      }
    }
  });
}
