import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

interface Envelope {
  code: number;
  message: string;
  data: unknown;
}

type MePermission = {
  resource: string;
  action: string;
  effect: "allow" | "deny";
};

function json(data: unknown, status = 200): {
  status: number;
  contentType: string;
  body: string;
} {
  return {
    status,
    contentType: "application/json",
    body: JSON.stringify({ code: status === 200 ? 0 : status, message: "", data } satisfies Envelope),
  };
}

async function mockBackend(
  page: Page,
  options: {
    requestId: string;
    permissions: MePermission[];
    decisionStatus: number;
    expectContextSource?: boolean;
  },
): Promise<void> {
  let activeRequestId = options.requestId;
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
      await route.fulfill(json({
        id: "user-1",
        username: "operator",
        tenant_id: "tenant-1",
        role: "member",
        platform: false,
        permissions: options.permissions,
      }));
      return;
    }
    if (path === "/api/v1/branding/public" || path === "/api/v1/branding") {
      await route.fulfill(json({ name: "RAGFlow-X", logo: "" }));
      return;
    }
    if (path === "/api/v1/system/setup/status") {
      await route.fulfill(json({ initialized: true }));
      return;
    }
    if (path === "/api/v1/conversation/assistants") {
      await route.fulfill(json({
        items: [{ id: "chat-1", kind: "chat", name: "Contract Assistant", status: "active" }],
        total: 1,
      }));
      return;
    }
    if (path === "/api/v1/chats/chat-1/config") {
      await route.fulfill(json({ id: "chat-1", name: "Contract Assistant", kb_names: ["Contracts"] }));
      return;
    }
    if (path === "/api/v1/chats/chat-1/sessions") {
      if (method === "POST") {
        await route.fulfill(json({ id: "session-1", name: "Governed session" }));
        return;
      }
      await route.fulfill(json({ items: [{ id: "session-1", name: "Governed session" }], total: 1 }));
      return;
    }
    if (path === "/api/v1/chats/chat-1/sessions/session-1/messages") {
      await route.fulfill(json({ items: [] }));
      return;
    }
    if (path === "/api/v1/chat/completions" && method === "POST") {
      activeRequestId = route.request().headers()["x-request-id"] ?? activeRequestId;
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: [
          `data: {"code":0,"data":{"answer":"governed answer","message_id":"assistant-${options.requestId}"}}\n\n`,
          "data: [DONE]\n\n",
        ].join(""),
      });
      return;
    }
    if (path.startsWith("/api/v1/conversation-context/decisions/by-request/")) {
      const decision = {
        id: "decision-1",
        request_id: activeRequestId,
        decision: "memory_allowed",
        detail: JSON.stringify({
          reason: "",
          context_sources: ["memory"],
          target_kind: "chat",
          policy_scope: "user",
          policy_generation: 2,
          consent_version: 1,
          release_manifest_status: "not_managed",
          memory_retrieved: true,
          memory_result_count: 1,
          raw_payload: "MUST NOT RENDER",
        }),
      };
      if (options.decisionStatus === 200) {
        await route.fulfill(json(decision));
      } else {
        await route.fulfill({
          status: options.decisionStatus,
          contentType: "application/json",
          body: JSON.stringify({ code: options.decisionStatus, message: "decision not found" }),
        });
      }
      return;
    }
    await route.fulfill(json(null));
  });
  await page.goto("/#/conversation-center?kind=chat");
  await expect(page.getByText("Contract Assistant").first()).toBeVisible();
  await page.getByRole("button", { name: "Governed session" }).click();
  await page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标").fill("enterprise question");
  await page.getByRole("button", { name: "发送" }).click();
  await expect(page.getByText("governed answer")).toBeVisible();
  if (options.expectContextSource === false) {
    await expect(page.getByRole("button", { name: "上下文来源" })).toHaveCount(0);
  } else {
    await expect(page.getByRole("button", { name: "上下文来源" })).toBeVisible();
  }
}

const readPermissions: MePermission[] = [
  { resource: "*", action: "*", effect: "allow" },
];
const noContextReadPermissions: MePermission[] = [
  { resource: "*", action: "*", effect: "allow" },
  { resource: "context-decision", action: "read", effect: "deny" },
];

test("context source dialog exposes safe governance metadata and passes axe", async ({ page }) => {
  await mockBackend(page, { requestId: "run-owner", permissions: readPermissions, decisionStatus: 200 });
  await page.getByRole("button", { name: "上下文来源" }).click();

  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("Memory 已授权")).toBeVisible();
  await expect(dialog.getByText("memory", { exact: true })).toBeVisible();
  await expect(dialog.getByText("对话", { exact: true })).toBeVisible();
  await expect(dialog.getByText("user", { exact: true })).toBeVisible();
  await expect(dialog.getByText("1", { exact: true }).first()).toBeVisible();
  await expect(dialog.getByText("仅显示治理元数据，不显示 Memory 密文、密钥、原始载荷或敏感审计明细。")).toBeVisible();
  await expect(dialog).not.toContainText("MUST NOT RENDER");

  const scan = await new AxeBuilder({ page }).include('[role="dialog"]').analyze();
  const actionable = scan.violations.filter((violation) =>
    ["critical", "serious"].includes(violation.impact ?? ""),
  );
  expect(actionable).toEqual([]);
});

test("cross-tenant decision failure renders a safe empty state", async ({ page }) => {
  await mockBackend(page, { requestId: "run-cross-tenant", permissions: readPermissions, decisionStatus: 404 });
  await page.getByRole("button", { name: "上下文来源" }).click();

  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("本次运行没有来源决策")).toBeVisible();
  await expect(dialog).not.toContainText("MUST NOT RENDER");
});

test("readers without context-decision permission do not receive the action", async ({ page }) => {
  await mockBackend(page, {
    requestId: "run-denied",
    permissions: noContextReadPermissions,
    decisionStatus: 200,
    expectContextSource: false,
  });
});
