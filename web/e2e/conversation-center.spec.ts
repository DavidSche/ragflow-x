import { expect, test, type Page } from "@playwright/test";

interface Envelope {
  code: number;
  message: string;
  data: unknown;
}

function json(data: unknown): string {
  const body: Envelope = { code: 0, message: "", data };
  return JSON.stringify(body);
}

async function mockBackend(page: Page): Promise<void> {
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
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: json({
          id: "user-1",
          username: "operator",
          tenant_id: "tenant-1",
          role: "member",
          platform: true,
          permissions: [{ resource: "*", action: "*", effect: "allow" }],
        }),
      });
      return;
    }
    if (path === "/api/v1/branding/public" || path === "/api/v1/branding") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ name: "RAGFlow-X", logo: "" }) });
      return;
    }
    if (path === "/api/v1/system/setup/status") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ initialized: true }) });
      return;
    }

    if (path === "/api/v1/conversation/assistants" && method === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: json({
          items: [
            { id: "chat-1", kind: "chat", name: "Contract Assistant", status: "active", updated_at: new Date().toISOString() },
            { id: "agent-1", kind: "agent", name: "Operations Agent", status: "active", updated_at: new Date().toISOString() },
          ],
          total: 2,
        }),
      });
      return;
    }

    if (path === "/api/v1/chats" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [{ id: "chat-1", name: "Contract Assistant" }], total: 1 }) });
      return;
    }
    if (path === "/api/v1/chats/chat-1" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ id: "chat-1", name: "Contract Assistant" }) });
      return;
    }
    if (path === "/api/v1/chats/chat-1/config" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ id: "chat-1", name: "Contract Assistant", kb_names: ["Contracts"], llm_id: "deepseek-v4-flash" }) });
      return;
    }
    if (path === "/api/v1/chats/chat-1/sessions" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [{ id: "session-1", name: "Existing session" }], total: 1 }) });
      return;
    }
    if (path === "/api/v1/chats/chat-1/sessions/session-1/messages" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [{ id: "m-restored", role: "assistant", content: "restored answer" }], next_cursor: undefined }) });
      return;
    }
    if (path === "/api/v1/chats/chat-1/sessions/session-new/messages" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [{ id: "m-restored", role: "assistant", content: "restored answer" }], next_cursor: undefined }) });
      return;
    }
    if (path === "/api/v1/chat/completions" && method === "POST") {
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: [
          'data: {"code":0,"data":{"answer":"Hel"}}\n\n',
          'data: {"code":0,"data":{"answer":"lo","session_id":"session-new"}}\n\n',
          "data: [DONE]\n\n",
        ].join(""),
      });
      return;
    }

    if (path === "/api/v1/search-apps" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [{ id: "search-1", name: "Legal Search" }], total: 1 }) });
      return;
    }
    if (path === "/api/v1/search-apps/search-1" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ id: "search-1", name: "Legal Search" }) });
      return;
    }
    if (path === "/api/v1/search-apps/search-1/completion/stream" && method === "POST") {
      await route.fulfill({ status: 200, contentType: "text/event-stream", body: 'data: {"code":0,"data":{"answer":"search answer"}}\n\ndata: [DONE]\n\n' });
      return;
    }

    if (path === "/api/v1/agents" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [{ id: "agent-1", title: "Operations Agent", canvas_category: "agent_canvas" }, { id: "dataflow-agent", title: "Background Pipeline", canvas_category: "dataflow_canvas" }], total: 2 }) });
      return;
    }
    if (path === "/api/v1/agents/agent-1/detail" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ id: "agent-1", title: "Operations Agent", canvas_category: "agent_canvas" }) });
      return;
    }
    if (path === "/api/v1/agents/agent-1/sessions" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [{ id: "agent-session-1", name: "Agent session" }], total: 1 }) });
      return;
    }
    if (path === "/api/v1/agents/agent-1/sessions/agent-session-1/messages" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [] }) });
      return;
    }
    if (path === "/api/v1/agents/agent-1/chat/completions/stream" && method === "POST") {
      await route.fulfill({ status: 200, contentType: "text/event-stream", body: 'data: {"choices":[{"delta":{"content":"agent answer"}}]}\n\ndata: [DONE]\n\n' });
      return;
    }

    await route.fulfill({ status: 200, contentType: "application/json", body: json(null) });
  });
}

test("unified center completes chat, search and agent conversations", async ({ page }) => {
  await mockBackend(page);
  await page.goto("/#/conversation-center?kind=chat");
  await expect(page.getByText("Contract Assistant").first()).toBeVisible();
  await page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标").fill("review contract");
  await page.getByRole("button", { name: "发送" }).click();
  await expect(page.getByText("Hello")).toBeVisible();
  await expect(page.getByText("已完成")).toBeVisible();
  expect(new URLSearchParams(new URL(page.url()).hash.split("?")[1]).get("contextId")).toBe("session-new");

  await page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标").fill("/");
  await expect(page.getByText("/chat")).toBeVisible();
  await page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标").fill("@");
  await expect(page.getByText("Contract Assistant").first()).toBeVisible();

  await page.reload();
  await expect(page.getByText("restored answer")).toBeVisible();

  await page.getByRole("button", { name: "企业搜索" }).click();
  await expect(page.getByText("Legal Search").first()).toBeVisible();
  await page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标").fill("find contracts");
  await page.getByRole("button", { name: "发送" }).click();
  await expect(page.getByText("search answer")).toBeVisible();
  expect(new URLSearchParams(new URL(page.url()).hash.split("?")[1]).has("contextId")).toBe(false);

  await page.getByRole("button", { name: "统一助手" }).click();
  await page.getByRole("button", { name: /Operations Agent/ }).last().click();
  await expect(page.getByText("Operations Agent").first()).toBeVisible();
  await expect(page.getByText("Background Pipeline")).toHaveCount(0);
  await page.getByText("Agent session").click();
  await page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标").fill("run agent");
  await page.getByRole("button", { name: "发送" }).click();
  await expect(page.getByText("agent answer")).toBeVisible();
  expect(new URLSearchParams(new URL(page.url()).hash.split("?")[1]).get("contextId")).toBe("agent-session-1");

  await page.getByRole("button", { name: /Contract Assistant/ }).first().click();
  await expect(page).toHaveURL(/kind=chat&targetId=chat-1/);
});

test("removes an unavailable recent target without requesting its sessions", async ({ page }) => {
  let sessionsRequested = false;
  await mockBackend(page);
  await page.route("**/api/v1/agents/stale-agent/sessions", async (route) => {
    sessionsRequested = true;
    await route.fulfill({ status: 404, contentType: "application/json", body: json({ code: 404, message: "agent not found", data: null }) });
  });
  await page.route("**/api/v1/agents/stale-agent", async (route) => {
    await route.fulfill({ status: 404, contentType: "application/json", body: json({ code: 404, message: "agent not found", data: null }) });
  });
  await page.route("**/api/v1/agents/stale-agent/detail", async (route) => {
    await route.fulfill({ status: 404, contentType: "application/json", body: json({ code: 404, message: "agent not found", data: null }) });
  });
  await page.addInitScript(() => {
    localStorage.setItem("conversation-center:recent:tenant-1:user-1", JSON.stringify([{
      target: { id: "stale-agent", kind: "agent", name: "Stale Agent" },
      lastUsedAt: Date.now(),
      source: "local",
    }]));
  });

  await page.goto("/#/conversation-center?kind=chat");
  await expect(page.getByText("Stale Agent")).toBeVisible();
  await page.getByText("Stale Agent").click();
  await expect(page.getByText("Stale Agent")).toBeHidden();
  expect(sessionsRequested).toBe(false);
  const inputFocused = await page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标").evaluate((element) => element === document.activeElement);
  expect(inputFocused).toBe(true);
});

test("routes a question, binds the selected assistant, and bootstraps its session", async ({ page }) => {
  let selectionRequested = false;
  let bootstrapRequested = false;
  await mockBackend(page);
  await page.route("**/api/v1/conversation/assistants**", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [], total: 0 }) });
      return;
    }
    await route.fallback();
  });
  await page.route("**/api/v1/chats**", async (route) => {
    if (route.request().method() === "GET" && new URL(route.request().url()).pathname.endsWith("/chats")) {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [], total: 0 }) });
      return;
    }
    await route.fallback();
  });
  await page.route("**/api/v1/conversation/route", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: json({
        route_id: "route-1",
        score_status: "calibrated",
        candidate_count: 1,
        requested_mode: "suggest",
        effective_mode: "suggest",
        selected: null,
        candidates: [{
          kind: "chat", id: "chat-1", target_id: "chat-1", name: "Contract Assistant",
          normalized_score: 0.95, normalized_margin: 0.35, confidence: 0.94,
          confidence_status: "calibrated", candidate_count: 1, has_competitor: false,
          routing_readiness: 1, routing_readiness_type: "METADATA_COMPLETENESS",
          agent_flow_readiness: 1, pre_execution_risk: "low", auto_select_enabled: false,
          catalog_freshness_sec: 0, reasons: ["contract"],
        }],
        expires_at: new Date(Date.now() + 60_000).toISOString(),
        router_version: "route-policy-v2", policy_mode: "recommend_only",
        policy_version: "route-policy-v2:test", rerank_status: "not_applicable", latency_ms: 5,
      }),
    });
  });
  await page.route("**/api/v1/conversation/route/route-1/select", async (route) => {
    selectionRequested = true;
    await route.fulfill({ status: 200, contentType: "application/json", body: json({ route_selection_id: "rs-1", kind: "chat", target_id: "chat-1", state: "NEW", requires_session: true }) });
  });
  await page.route("**/api/v1/conversation/route-selections/rs-1/bootstrap", async (route) => {
    bootstrapRequested = true;
    await route.fulfill({ status: 200, contentType: "application/json", body: json({ route_selection_id: "rs-1", bootstrap_operation_id: "boot-1", bootstrap_type: "CREATE_SESSION", kind: "chat", target_id: "chat-1", session_id: "session-new", selection_state: "RESERVED", bootstrap_state: "SUCCEEDED" }) });
  });

  await page.goto("/#/conversation-center?kind=chat");
  await page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标").fill("review the enterprise contract");
  await page.getByRole("button", { name: "发送" }).click();
  await page.getByRole("button", { name: /Contract Assistant.*95%.*low/ }).click();
  await expect.poll(() => selectionRequested).toBe(true);
  await expect.poll(() => bootstrapRequested).toBe(true);
  await expect(page).toHaveURL(/kind=chat&targetId=chat-1&contextId=session-new/);
  await expect(page.getByText("restored answer")).toBeVisible();
  expect(new URLSearchParams(new URL(page.url()).hash.split("?")[1]).get("targetId")).toBe("chat-1");
  expect(new URLSearchParams(new URL(page.url()).hash.split("?")[1]).get("contextId")).toBe("session-new");
  const inputFocused = await page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标").evaluate((element) => element === document.activeElement);
  expect(inputFocused).toBe(true);
});

test("keeps the conversation center and new session action within a 1080p viewport", async ({ page }) => {
  await mockBackend(page);
  await page.setViewportSize({ width: 1920, height: 1080 });
  await page.goto("/#/conversation-center?kind=chat");

  await expect(page.getByText("Contract Assistant").first()).toBeVisible();
  const newSession = page.getByRole("button", { name: "新会话" });
  await expect(newSession).toBeVisible();
  await expect(newSession).toBeInViewport();

  const pageScroll = await page.evaluate(() => ({
    scrollHeight: document.documentElement.scrollHeight,
    clientHeight: document.documentElement.clientHeight,
  }));
  expect(pageScroll.scrollHeight).toBeLessThanOrEqual(pageScroll.clientHeight);
});
