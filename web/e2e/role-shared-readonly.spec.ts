import { expect, type Page, test } from "@playwright/test";

// doc/315 finding 1, the console half. The API decides per caller whether a role
// is writable (`editable`) and the list also carries `owned`; the screen has to
// read that decision instead of rendering the buttons unconditionally. Before
// this, a tenant admin saw "configure permissions" / edit / delete on the shared
// built-ins, and every one of those was a 403 the dialog could not explain.
//
// Mocked API on purpose: the assertion is about what the console *renders* for a
// given payload, and it must hold with no backend (this suite runs in CI). The
// server half of the same contract is proven by probes/t29_role_tenant_isolation.py
// S8 against the deployed instance.

interface Envelope {
  code: number;
  message: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  data: any;
}

// A built-in: shared, readable, never writable by anyone.
const BUILTIN = {
  id: "tenant_admin",
  name: "tenant_admin",
  scope: "tenant",
  description: "built-in workspace administrator",
  builtin: true,
  status: "active",
  owned: false,
  editable: false,
};

// Platform-authored but not built-in: the row the shared note is about.
const SHARED = {
  id: "role_shared_audit",
  name: "shared-audit",
  scope: "tenant",
  description: "maintained by the platform",
  builtin: false,
  status: "active",
  owned: false,
  editable: false,
};

// Authored by this workspace: editable and deletable.
const OWNED = {
  id: "role_a_owned",
  name: "t29-role-a",
  scope: "tenant",
  description: "authored by this workspace",
  builtin: false,
  status: "active",
  owned: true,
  editable: true,
};

// Parked by the migration: only the platform receives it, and only the platform
// can put it back into service.
const PARKED = {
  id: "role_legacy_orphan",
  name: "legacy-orphan",
  scope: "tenant",
  description: "the migration could not prove an owner",
  builtin: false,
  status: "quarantined",
  owned: false,
  editable: true,
};

async function mockBackend(
  page: Page,
  opts: { roles: unknown[]; platform: boolean },
): Promise<void> {
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
    }
    if (path.endsWith("/auth/me")) {
      // Unauthenticated means 401, not "here is an identity": without this the app
      // never renders the login form and the test would start inside the shell.
      if (!route.request().headers().cookie?.includes("rgx_access_token=test-token")) {
        await route.fulfill({
          status: 401,
          contentType: "application/json",
          body: JSON.stringify({ code: 401, message: "unauthorized" }),
        });
        return;
      }
      body = {
        code: 0,
        message: "",
        data: {
          id: opts.platform ? "u-platform" : "u-a-admin",
          username: opts.platform ? "admin" : "tenant_admin_a",
          tenant_id: opts.platform ? "" : "tenant-a",
          role: opts.platform ? "platform_admin" : "tenant_admin",
          platform: opts.platform,
          // A tenant admin that *does* hold role management: this is the
          // combination whose write buttons must still be withheld on a shared row.
          permissions: [
            { resource: "*", action: "*", effect: "allow" },
            { resource: "roles", action: "manage", effect: "allow" },
            { resource: "role", action: "manage", effect: "allow" },
            { resource: "role", action: "read", effect: "allow" },
            { resource: "tenants", action: "governance.manage", effect: "allow" },
          ],
        },
      };
    } else if (path.endsWith("/branding/public") || path.endsWith("/branding")) {
      body = { code: 0, message: "", data: { name: "RAGFlow-X", logo: "" } };
    } else if (path.endsWith("/system/setup/status")) {
      body = { code: 0, message: "", data: { initialized: true } };
    } else if (path.endsWith("/system/health")) {
      body = { code: 0, message: "", data: { status: "ok", version: "test" } };
    } else if (path.endsWith("/roles/permission-catalog")) {
      body = {
        code: 0,
        message: "",
        data: [
          { resource: "role", actions: ["read", "manage"] },
          { resource: "chat", actions: ["execute"] },
        ],
      };
    } else if (/\/roles\/[^/]+\/permissions$/.test(path)) {
      body = { code: 0, message: "", data: [{ action: "read", resource: "role", effect: "allow" }] };
    } else if (path.endsWith("/roles")) {
      // `roles` is a "listable" resource: the data adapter expects a bare array,
      // not the {items,total} envelope the paginated ones use.
      body = { code: 0, message: "", data: opts.roles };
    } else if (path.endsWith("/usage/summary")) {
      body = { code: 0, message: "", data: { total_tokens: 0, requests: 0, cost: 0 } };
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  });
}

async function signIn(page: Page, username: string): Promise<void> {
  await page.goto("/");
  await page.getByRole("textbox", { name: "用户名" }).fill(username);
  await page.getByRole("textbox", { name: "密码" }).fill("secret123");
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.locator('main[aria-label="主内容区域"]')).toBeVisible();
  await page.goto("/#/roles");
}

test("shared roles are read-only, an owned role is editable (doc/315 1)", async ({ page }) => {
  await mockBackend(page, { roles: [BUILTIN, SHARED, OWNED], platform: false });
  await signIn(page, "tenant_admin_a");

  const builtinRow = page.getByRole("row", { name: /tenant_admin/ });
  const sharedRow = page.getByRole("row", { name: /shared-audit/ });
  const ownedRow = page.getByRole("row", { name: /t29-role-a/ });
  await expect(builtinRow).toBeVisible();

  // Reading is offered, writing is not — on both kinds of shared row.
  await expect(builtinRow.getByRole("button", { name: "查看权限" })).toBeVisible();
  await expect(sharedRow.getByRole("button", { name: "查看权限" })).toBeVisible();
  for (const row of [builtinRow, sharedRow]) {
    await expect(row.getByRole("button", { name: "编辑角色" })).toHaveCount(0);
    await expect(row.getByRole("button", { name: "删除角色" })).toHaveCount(0);
  }

  // The owned row keeps its write affordances: the flags narrow one row, they do
  // not disable the screen.
  await expect(ownedRow.getByRole("button", { name: "编辑角色" })).toBeVisible();
  await expect(ownedRow.getByRole("button", { name: "删除角色" })).toBeVisible();

  // Opening a shared matrix shows the grants and says why it cannot be saved.
  await sharedRow.getByRole("button", { name: "查看权限" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await expect(
    dialog.getByText("这是平台维护的共享角色，可以查看权限，但只有平台管理员能修改。"),
  ).toBeVisible();
  await expect(dialog.getByRole("button", { name: "保存权限" })).toHaveCount(0);
  // Readable, not empty: the matrix carries the role's actual rule, and the
  // controls are disabled rather than absent.
  const cell = dialog.getByLabel("角色 查看");
  await expect(cell).toHaveValue("allow");
  await expect(cell).toBeDisabled();

  // A built-in already says that, one branch earlier, and keeps saying it: the
  // read-only note above is for the platform-shared rows that are not built in.
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
});

test("a parked role reaches the platform admin with its status (doc/315 2)", async ({ page }) => {
  // Only the platform receives quarantined rows (the server filters them out for
  // every tenant), so this is the review path: the row must be visible, labelled,
  // and editable — including the status field that puts it back into service.
  await mockBackend(page, { roles: [PARKED], platform: true });
  await signIn(page, "admin");

  const row = page.getByRole("row", { name: /legacy-orphan/ });
  await expect(row).toBeVisible();
  await expect(row.getByText("待平台确认")).toBeVisible();
  await expect(row.getByRole("button", { name: "编辑角色" })).toBeVisible();

  await row.getByRole("button", { name: "编辑角色" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("状态")).toBeVisible();
  const status = dialog.getByRole("combobox").filter({ hasText: "待平台确认" });
  await expect(status).toHaveValue("quarantined");
  await expect(dialog.getByRole("option", { name: "启用" })).toBeAttached();
});
