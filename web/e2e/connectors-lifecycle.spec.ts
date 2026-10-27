import { expect, type Page, test } from "@playwright/test";

interface Envelope {
  code: number;
  message: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  data: any;
}

type ConnectorRow = {
  id: string;
  name: string;
  provider: string;
  adapter_kind: string;
  target_dataset_id: string;
  schedule_kind: string;
  schedule_value: number;
  schedule_tz_offset_min: number;
  enabled: boolean;
  status: string;
  last_run_status: string;
  has_credential: boolean;
  credential_version: number;
  created_by: string;
};

const hold = {
  approval_id: "a-connector-202",
  request_no: "APR-E2E-CONNECTOR-202",
  object_type: "connector",
  action: "connector.create",
  status: "pending_approval",
  current_step: 1,
  expire_at: "2026-11-01T00:00:00Z",
  approval_path: "/approvals/a-connector-202/show",
};

const run = {
  id: "run-1",
  connector_id: "c-new",
  trigger: "manual",
  triggered_by: "admin",
  status: "succeeded",
  started_at: "2026-10-25T08:00:00Z",
  finished_at: "2026-10-25T08:00:01Z",
  added: 2,
  changed: 1,
  deleted: 0,
  skipped: 1,
  failed: 0,
  duration_ms: 1200,
  trace_id: "trace-connector-e2e",
  created_at: "2026-10-25T08:00:00Z",
};

type ApiOptions = {
  permissions?: { resource: string; action: string; effect: string }[];
  holdCreate?: boolean;
  rows?: ConnectorRow[];
};

type ApiState = {
  created: Record<string, unknown> | null;
  deleted: boolean;
  rows: ConnectorRow[];
};

function makeRow(): ConnectorRow {
  return {
    id: "c-new",
    name: "minio-e2e",
    provider: "s3",
    adapter_kind: "native",
    target_dataset_id: "ds-1",
    schedule_kind: "daily",
    schedule_value: 135,
    schedule_tz_offset_min: 480,
    enabled: true,
    status: "idle",
    last_run_status: "succeeded",
    has_credential: true,
    credential_version: 1,
    created_by: "admin",
  };
}

async function mockConnectorApi(page: Page, options: ApiOptions = {}): Promise<ApiState> {
  const state: ApiState = {
    created: null,
    deleted: false,
    rows: options.rows ?? [],
  };
  let authenticated = false;

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const method = request.method();
    const path = new URL(request.url()).pathname;
    let body: Envelope = { code: 0, message: "", data: null };
    let status = 200;

    if (method === "POST" && path.endsWith("/auth/login")) {
      authenticated = true;
      body = { code: 0, message: "", data: { token: "token", refresh_token: "refresh" } };
    } else if (path.endsWith("/auth/me")) {
      if (!authenticated) {
        await route.fulfill({
          status: 401,
          contentType: "application/json",
          body: JSON.stringify({ code: 401, message: "unauthenticated" }),
        });
        return;
      }
      body = {
        code: 0,
        message: "",
        data: {
          id: "admin",
          username: "admin",
          tenant_id: "tenant-1",
          role: "platform_admin",
          platform: true,
          permissions: options.permissions ?? [{ resource: "*", action: "*", effect: "allow" }],
          approval_enabled: true,
        },
      };
    } else if (path === "/api/v1/connectors/providers/meta" && method === "GET") {
      body = {
        code: 0,
        message: "",
        data: [
          {
            type: "local_folder",
            label: "本地目录 (LocalFolder)",
            tier: "A",
            closure: "full",
            target_version: "v0.4.0 GA",
            spec_fields: [{ key: "root", label: "根目录", required: true }],
            cred_fields: [],
          },
          {
            type: "s3",
            label: "S3 / MinIO (S3 兼容)",
            tier: "A",
            closure: "full",
            target_version: "v0.4.0 GA",
            spec_fields: [
              { key: "endpoint", label: "Endpoint", required: true },
              { key: "region", label: "Region", default: "us-east-1" },
              { key: "bucket", label: "Bucket", required: true },
              { key: "prefix", label: "Prefix (可选)" },
            ],
            cred_fields: [
              { key: "access_key_id", label: "Access Key ID", required: true },
              { key: "access_secret", label: "Access Secret", required: true, secret: true },
            ],
          },
        ],
      };
    } else if (path === "/api/v1/datasets" && method === "GET") {
      body = { code: 0, message: "", data: [{ id: "ds-1", name: "团队知识库" }] };
    } else if (path === "/api/v1/connectors/test" && method === "POST") {
      body = { code: 0, message: "", data: { reachable: true, auth_ok: true } };
    } else if (path === "/api/v1/connectors" && method === "GET") {
      body = {
        code: 0,
        message: "",
        data: { items: state.rows, total: state.rows.length, page: 1, page_size: 20 },
      };
    } else if (path === "/api/v1/connectors" && method === "POST") {
      state.created = request.postDataJSON() as Record<string, unknown>;
      if (options.holdCreate) {
        status = 202;
        body = { code: 0, message: "approval submitted", data: hold };
      } else {
        state.rows = [makeRow()];
        body = { code: 0, message: "", data: makeRow() };
      }
    } else if (path === "/api/v1/connectors/c-new" && method === "GET") {
      body = { code: 0, message: "", data: state.rows[0] };
    } else if (path === "/api/v1/connectors/c-new/runs" && method === "GET") {
      body = {
        code: 0,
        message: "",
        data: { items: [run], total: 1, page: 1, page_size: 10 },
      };
    } else if (path === "/api/v1/connectors/c-new/runs/run-1" && method === "GET") {
      body = {
        code: 0,
        message: "",
        data: {
          ...run,
          detail: {
            skipped: [{ key: "docs/large-26mb.bin", reason: "large", size: 27262976 }],
            skipped_unchanged: 2,
          },
        },
      };
    } else if (path === "/api/v1/connectors/c-new/preview" && method === "POST") {
      body = { code: 0, message: "", data: { added: 1, changed: 1, deleted: 0, skipped: 0 } };
    } else if (path === "/api/v1/connectors/c-new/sync" && method === "POST") {
      body = { code: 0, message: "", data: { run_id: "run-2" } };
    } else if (path === "/api/v1/connectors/c-new/disable" && method === "POST") {
      state.rows = [{ ...state.rows[0], enabled: false, status: "disabled" }];
      body = { code: 0, message: "", data: state.rows[0] };
    } else if (path === "/api/v1/connectors/c-new" && method === "DELETE") {
      state.deleted = true;
      state.rows = [];
      body = { code: 0, message: "", data: { deleted: true } };
    } else if (path === "/api/v1/connectors/c-viewer" && method === "GET") {
      body = {
        code: 0,
        message: "",
        data: { ...makeRow(), id: "c-viewer", name: "viewer-connector" },
      };
    }

    await route.fulfill({
      status,
      contentType: "application/json",
      headers: status === 202 ? { "X-Approval-Request-Id": hold.request_no } : {},
      body: JSON.stringify(body),
    });
  });

  return state;
}

async function login(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("textbox", { name: "用户名" }).fill("admin");
  await page.getByRole("textbox", { name: "密码" }).fill("admin123");
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.locator('main[aria-label="主内容区域"]')).toBeVisible();
}

async function fillWizard(page: Page, name: string, sensitive = false): Promise<void> {
  await page.getByRole("button", { name: "新建连接器" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: /S3 \/ MinIO/ })).toBeVisible();
  await dialog.getByRole("button", { name: /S3 \/ MinIO/ }).click();
  await dialog.getByRole("button", { name: "下一步" }).click();

  await expect(dialog.getByLabel(/Endpoint/)).toBeVisible();
  await dialog.getByLabel(/Endpoint/).fill("https://minio.e2e.local");
  await dialog.getByLabel(/Bucket/).fill("rgx-connector-e2e");
  await dialog.getByLabel(/Prefix/).fill("docs");
  await dialog.getByLabel(/Access Key ID/).fill("rgxadmin");
  await dialog.getByLabel(/Access Secret/).fill("RgxMinio2026!");
  await dialog.getByRole("button", { name: "测试连接" }).click();
  await expect(dialog.getByText("连接可用")).toBeVisible();
  await dialog.getByRole("button", { name: "下一步" }).click();

  await dialog.getByLabel(/连接器名称/).fill(name);
  await dialog.getByRole("combobox", { name: /目标数据集/ }).selectOption("ds-1");
  await dialog.getByRole("combobox", { name: /同步计划/ }).selectOption("daily");
  await dialog.getByLabel(/时间（本地）/).fill("10:15");
  if (sensitive) {
    await dialog.getByRole("checkbox", { name: "标记为敏感数据源" }).check();
  }
  await dialog.getByRole("button", { name: "下一步" }).click();
  await expect(dialog.getByText("团队知识库")).toBeVisible();
}

test("connector lifecycle: wizard, runs, preview, sync, disable, delete", async ({ page }) => {
  const state = await mockConnectorApi(page);
  await login(page);
  await page.goto("/#/connectors");
  await expect(page.getByRole("button", { name: "新建连接器" })).toBeVisible();

  await fillWizard(page, "minio-e2e");
  await page.getByRole("dialog").getByRole("button", { name: "新建连接器", exact: true }).click();
  await expect(page.getByText("连接器已创建")).toBeVisible();

  expect(state.created).not.toBeNull();
  const payload = state.created as Record<string, unknown>;
  expect(payload).toMatchObject({
    name: "minio-e2e",
    provider: "s3",
    target_dataset_id: "ds-1",
    schedule: { kind: "daily", hhmm: "10:15" },
    schedule_tz_offset_min: 480,
    sensitive: false,
  });
  expect(payload.spec).toMatchObject({
    endpoint: "https://minio.e2e.local",
    bucket: "rgx-connector-e2e",
    prefix: "docs",
  });
  expect(payload.credential).toMatchObject({ access_key_id: "rgxadmin", access_secret: "RgxMinio2026!" });

  const detailLink = page.getByRole("link", { name: "minio-e2e" });
  await expect(detailLink).toHaveAttribute("href", "#/connectors/c-new/show");
  await detailLink.click();
  await expect(page).toHaveURL(/\/connectors\/c-new\/show$/);
  await expect(page.getByRole("heading", { name: "minio-e2e", exact: true })).toBeVisible();

  // Run history loads counters and the parsed skip detail from the scoped
  // run-detail endpoint (docs/large-26mb.bin > 25 MiB => skipped/large).
  await expect(page.getByText(/2 \/ 1 \/ 0/)).toBeVisible();
  await page.getByRole("button", { name: "显示详情" }).click();
  await expect(page.getByText(/docs\/large-26mb\.bin/)).toBeVisible();
  await expect(page.getByText(/large \(26 MiB\)/)).toBeVisible();

  await page.getByRole("button", { name: "预览变更" }).click();
  await expect(page.getByText("预览完成")).toBeVisible();
  await page.getByRole("button", { name: "立即同步" }).click();
  await expect(page.getByText("已提交同步任务")).toBeVisible();

  await page.getByRole("button", { name: "禁用" }).click();
  await expect(page.getByText("连接器已禁用")).toBeVisible();
  await expect(page.getByRole("button", { name: "启用" })).toBeVisible();

  await page.getByRole("button", { name: "删除" }).click();
  const confirm = page.getByRole("dialog", { name: "删除连接器" });
  await expect(confirm.getByRole("heading", { name: "删除连接器" })).toBeVisible();
  await expect(confirm.getByText(/确认删除连接器「minio-e2e」/)).toBeVisible();
  await confirm.getByRole("button", { name: "删除" }).click();
  await expect(page.getByText("连接器已删除")).toBeVisible();
  await expect(page).toHaveURL(/\/connectors$/);
  await expect(page.getByText("暂无连接器")).toBeVisible();
  expect(state.deleted).toBe(true);
});

test("sensitive connector create honors the 202 approval hold", async ({ page }) => {
  const state = await mockConnectorApi(page, { holdCreate: true });
  await login(page);
  await page.goto("/#/connectors");
  await expect(page.getByRole("button", { name: "新建连接器" })).toBeVisible();

  await fillWizard(page, "sensitive-minio", true);
  await page.getByRole("dialog").getByRole("button", { name: "新建连接器", exact: true }).click();

  const holdDialog = page.getByRole("dialog", { name: "已进入审批流程" });
  await expect(holdDialog).toBeVisible();
  await expect(holdDialog.getByText("已进入审批流程")).toBeVisible();
  await expect(holdDialog.getByText("APR-E2E-CONNECTOR-202")).toBeVisible();
  await expect(holdDialog.getByRole("button", { name: "查看审批单" })).toBeVisible();
  expect((state.created as Record<string, unknown>).sensitive).toBe(true);
  expect(state.rows).toEqual([]);
  await holdDialog.getByRole("button", { name: "取消" }).click();
});

test("read-only roles see the connector list but no manage actions", async ({ page }) => {
  const row = { ...makeRow(), id: "c-viewer", name: "viewer-connector" };
  await mockConnectorApi(page, {
    rows: [row],
    permissions: [{ resource: "connector", action: "read", effect: "allow" }],
  });
  await login(page);
  await page.goto("/#/connectors");

  await expect(page.getByRole("link", { name: "viewer-connector" })).toBeVisible();
  await expect(page.getByRole("button", { name: "新建连接器" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "预览变更" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "立即同步" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "禁用" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "删除" })).toHaveCount(0);

  const detailLink = page.getByRole("link", { name: "viewer-connector" });
  await expect(detailLink).toHaveAttribute("href", "#/connectors/c-viewer/show");
  await detailLink.click();
  await expect(page).toHaveURL(/\/connectors\/c-viewer\/show$/);
  await expect(page.getByRole("heading", { name: "viewer-connector", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "预览变更" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "立即同步" })).toHaveCount(0);
});
