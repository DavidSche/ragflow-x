import { expect, type Page, test } from "@playwright/test";

interface Envelope {
  code: number;
  message: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  data: any;
}

const holdBody = {
  approval_id: "a-202",
  request_no: "APR-E2E-202",
  object_type: "api-key",
  action: "create",
  status: "pending_approval",
  current_step: 1,
  expire_at: "2026-09-04T00:00:00Z",
  approval_path: "/approvals/a-202/show",
};

const providerHoldBody = {
  approval_id: "a-provider-202",
  request_no: "APR-E2E-PROVIDER-202",
  object_type: "model-provider",
  action: "create",
  status: "pending_approval",
  current_step: 1,
  expire_at: "2026-09-04T00:00:00Z",
  approval_path: "/approvals/a-provider-202/show",
};

async function login(page: Page): Promise<void> {
  let authenticated = false;
  await page.route("**/api/v1/**", async (route) => {
    const method = route.request().method();
    const path = new URL(route.request().url()).pathname;
    let body: Envelope = { code: 0, message: "", data: null };
    let status = 200;

    if (method === "POST" && path.endsWith("/auth/login")) {
      authenticated = true;
      body = { code: 0, message: "", data: { token: "token", refresh_token: "refresh" } };
    } else if (path.endsWith("/auth/me")) {
      if (!authenticated) {
        await route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ code: 401, message: "unauthenticated" }) });
        return;
      }
      body = {
        code: 0,
        message: "",
        data: {
          id: "approver-1",
          username: "admin",
          tenant_id: "tenant-1",
          role: "platform_admin",
          platform: true,
          permissions: [{ resource: "*", action: "*", effect: "allow" }],
          approval_enabled: true,
        },
      };
    } else if (path.endsWith("/keys") && method === "POST") {
      status = 202;
      body = { code: 0, message: "approval submitted", data: holdBody };
    } else if (path === "/api/v1/model-providers" && method === "POST") {
      status = 202;
      body = { code: 0, message: "approval submitted", data: providerHoldBody };
    } else if (path === "/api/v1/approvals/a-provider-202" && method === "GET") {
      body = {
        code: 0,
        message: "",
        data: {
          approval: {
            id: providerHoldBody.approval_id,
            request_no: providerHoldBody.request_no,
            object_type: providerHoldBody.object_type,
            object_id: "new:openai",
            action: providerHoldBody.action,
            title: "Create model provider",
            status: providerHoldBody.status,
            current_step: providerHoldBody.current_step,
            requester_id: "approver-1",
            created_at: "2026-09-01T00:00:00Z",
            submitted_at: "2026-09-01T00:00:00Z",
            expires_at: providerHoldBody.expire_at,
            payload_json: "{}",
            snapshot_json: "{}",
            result_json: "{}",
            steps: [],
          },
          steps: [],
        },
      };
    } else if (path === "/api/v1/approvals/a-202" && method === "GET") {
      body = {
        code: 0,
        message: "",
        data: {
          approval: {
            id: holdBody.approval_id,
            request_no: holdBody.request_no,
            object_type: holdBody.object_type,
            object_id: "approved-gateway",
            action: holdBody.action,
            title: "Create API key",
            status: holdBody.status,
            current_step: holdBody.current_step,
            requester_id: "approver-1",
            created_at: "2026-09-01T00:00:00Z",
            submitted_at: "2026-09-01T00:00:00Z",
            expires_at: holdBody.expire_at,
            payload_json: "{}",
            snapshot_json: "{}",
            result_json: "{}",
            steps: [],
          },
          steps: [],
        },
      };
    }

    await route.fulfill({
      status,
      contentType: "application/json",
      headers: status === 202 ? { "X-Approval-Request-Id": holdBody.request_no } : {},
      body: JSON.stringify(body),
    });
  });

  await page.goto("/");
  await page.getByRole("textbox", { name: "用户名" }).fill("admin");
  await page.getByRole("textbox", { name: "密码" }).fill("admin123");
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.locator('main[aria-label="主内容区域"]')).toBeVisible();
  if ((page.viewportSize()?.width ?? 0) >= 1024) {
    await expect(page.getByRole("link", { name: "审批中心" })).toBeVisible();
  }
}

test("business 202 responses show approval guidance", async ({ page }) => {
  await login(page);
  await page.goto("/#/api-keys/create");
  await page.getByLabel("密钥名称").fill("approved-gateway");
  await page.getByRole("button", { name: "生成密钥" }).click();

  await expect(page.getByText("已进入审批流程")).toBeVisible();
  await expect(page.getByText("APR-E2E-202")).toBeVisible();
  await page.getByRole("button", { name: "查看审批单" }).click();
  await expect(page).toHaveURL(/\/approvals\/a-202\/show/);
});

test("model provider create page handles an approval hold", async ({ page }) => {
  await login(page);
  await page.goto("/#/model-providers/create");
  await page.getByLabel("供应商名称").fill("openai");
  await page.getByRole("button", { name: "接入供应商" }).click();

  await expect(page.getByText("已进入审批流程")).toBeVisible();
  await expect(page.getByText("APR-E2E-PROVIDER-202")).toBeVisible();
  await page.getByRole("button", { name: "查看审批单" }).click();
  await expect(page).toHaveURL(/\/approvals\/a-provider-202\/show/);
});

test("approval center can submit a manual request", async ({ page }) => {
  const submissions: Array<Record<string, unknown>> = [];
  let authenticated = false;
  await page.route("**/api/v1/**", async (route) => {
    const method = route.request().method();
    const path = new URL(route.request().url()).pathname;
    let body: Envelope = { code: 0, message: "", data: null };

    if (method === "POST" && path.endsWith("/auth/login")) {
      authenticated = true;
      body = { code: 0, message: "", data: { token: "token", refresh_token: "refresh" } };
    } else if (path.endsWith("/auth/me")) {
      if (!authenticated) {
        await route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ code: 401, message: "unauthenticated" }) });
        return;
      }
      body = {
        code: 0,
        message: "",
        data: {
          id: "requester-1",
          username: "requester",
          tenant_id: "tenant-1",
          role: "operator",
          platform: false,
          permissions: [{ resource: "*", action: "*", effect: "allow" }],
          approval_enabled: true,
        },
      };
    } else if (path === "/api/v1/approvals" && method === "POST") {
      submissions.push(route.request().postDataJSON());
      body = {
        code: 0,
        message: "",
        data: { id: "a-manual", request_no: "APR-E2E-MANUAL", status: "pending_approval", current_step: 1 },
      };
    } else if (path === "/api/v1/datasets" && method === "GET") {
      body = {
        code: 0,
        message: "",
        data: [{ id: "dataset-1", name: "Enterprise dataset" }],
      };
    }

    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  });

  await page.goto("/");
  await page.getByRole("textbox", { name: "用户名" }).fill("requester");
  await page.getByRole("textbox", { name: "密码" }).fill("secret123");
  await page.getByRole("button", { name: "登录" }).click();
  await page.goto("/#/approvals/create");

  await page.getByLabel("审批对象").selectOption("dataset-1");
  await page.getByLabel("申请原因").fill("E2E recovery request");
  await page.getByRole("button", { name: "提交审批" }).click();

  await expect(page).toHaveURL(/\/approvals\/a-manual\/show/);
  await expect(page.getByText("审批单已创建")).toBeVisible();
  expect(submissions).toHaveLength(1);
  expect(submissions[0]).toMatchObject({ object_type: "dataset", action: "delete", object_id: "dataset-1" });
  expect(submissions[0].idempotency_key).toMatch(/requester-1:/);
});

test("approval detail exposes business recovery guidance", async ({ page }) => {
  let authenticated = false;
  await page.route("**/api/v1/**", async (route) => {
    const method = route.request().method();
    const path = new URL(route.request().url()).pathname;
    let body: Envelope = { code: 0, message: "", data: null };

    if (method === "POST" && path.endsWith("/auth/login")) {
      authenticated = true;
      body = { code: 0, message: "", data: { token: "token", refresh_token: "refresh" } };
    } else if (path.endsWith("/auth/me")) {
      if (!authenticated) {
        await route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ code: 401, message: "unauthenticated" }) });
        return;
      }
      body = {
        code: 0,
        message: "",
        data: {
          id: "requester-1",
          username: "requester",
          tenant_id: "tenant-1",
          role: "operator",
          platform: false,
          permissions: [{ resource: "*", action: "*", effect: "allow" }],
          approval_enabled: true,
        },
      };
    } else if (path === "/api/v1/approvals/a-failed" && method === "GET") {
      body = {
        code: 0,
        message: "",
        data: {
          approval: {
            id: "a-failed",
            request_no: "APR-E2E-FAILED",
            object_type: "dataset",
            object_id: "dataset-1",
            action: "delete",
            title: "Delete dataset",
            status: "execution_failed",
            current_step: 1,
            requester_id: "requester-1",
            last_error: "RAGFlow unavailable",
          },
          steps: [],
        },
      };
    }

    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
  });

  await page.goto("/");
  await page.getByRole("textbox", { name: "用户名" }).fill("requester");
  await page.getByRole("textbox", { name: "密码" }).fill("secret123");
  await page.getByRole("button", { name: "登录" }).click();
  await page.goto("/#/approvals/a-failed/show");

  await expect(page.getByText("业务处理引导")).toBeVisible();
  await expect(page.getByText("执行失败时，请先查看失败原因；处理完成后可重试或重新发起审批。")).toBeVisible();
  await expect(page.getByRole("button", { name: "重新发起审批" })).toBeVisible();
  await expect(page.getByRole("button", { name: "返回业务对象" })).toBeVisible();
});
