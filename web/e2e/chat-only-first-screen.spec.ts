// chat-only 首屏：doc/304 §7 第 55 条（E155 / E176）
//
// 第 55 条存在的理由是 E159：三块分别有单测（navigationRoles / Dashboard /
// ConversationCenter），线上仍然 403 —— 单测证明的是"每个零件按我以为的方式拼"，
// 不是"它们真的拼起来了"。这个 spec 在**真实浏览器**里跑一遍拼接：
//
//   ① 一个只有 chat 授权的自定义角色登录 → 打开 `/` → 落到对话中心，侧栏只有它
//      真能进的入口，而且**发得出第一条消息**（不是渲染了个空壳）；
//   ② 应用根**一次都没有**渲染仪表板——判据是仪表板自己的请求发了没有，
//      而 ③ 是同一套 mock 的对照组：同样的页面、同样的角色字段，只把授权换掉，
//      请求必须出现。没有 ③ 的 ② 是空的：一个永远不成立的断言语义上也永远绿；
//   ④ 移动端：输入框字号 ≥16px（<16px 时 iOS 聚焦会放大整页，键盘一弹就走位）。
//
// 探针边界（这条 spec **不**声称的）：`home-loading` 那一帧没有在这里断言。
// 当前实现里 `checkAuth` 的 `/auth/me` 已经把身份写进模块级缓存，挂载时
// `useGetIdentity`/`usePermissions` 基本在首次提交前就解析完成，浏览器里那个
// "pending" 窗口只有一个 microtask 宽——抓不到，抓到也不算数。未决态本身的渲染
// 由 `Dashboard.test.tsx` 用可冻结的 pending 覆盖。E176 在浏览器里可观察的后果
// 就是②：仪表板真的被画出来过，连它那几条汇总请求一起。

import { expect, test, type Page } from "@playwright/test";

interface Envelope {
  code: number;
  message: string;
  data: unknown;
}

const json = (data: unknown) =>
  JSON.stringify({ code: 0, message: "", data } satisfies Envelope);

interface Grant {
  resource: string;
  action: string;
  effect: "allow" | "deny";
}

// 一个自定义、非内置的角色：只被授予"说话"这件事。`role` 故意不是任何内置 id，
// 因为 E155 的那条分支只在自定义角色上生效（内置角色按 id 直接映射视图）。
const CHAT_ONLY_PERMISSIONS: Grant[] = [
  { resource: "chat", action: "read", effect: "allow" },
  { resource: "chat", action: "execute", effect: "allow" },
  { resource: "chat", action: "session:create", effect: "allow" },
  { resource: "user", action: "read", effect: "allow" },
];

// 对照组的授权：拿掉 `chat|execute`，所以不再是 chat 视图；给 `dashboard|read`，
// 所以它**应该**看到仪表板。角色字段与 chat-only 完全相同，差别只在这张表。
const DASHBOARD_PERMISSIONS: Grant[] = [
  { resource: "dashboard", action: "read", effect: "allow" },
  { resource: "usage", action: "read", effect: "allow" },
  { resource: "user", action: "read", effect: "allow" },
];

interface MockOptions {
  permissions?: Grant[];
}

async function mockBackend(page: Page, options: MockOptions = {}): Promise<void> {
  const permissions = options.permissions ?? CHAT_ONLY_PERMISSIONS;
  // 登录前 `/auth/me` 必须是 401：一个"总是已登录"的 mock 会让 `page.goto("/")`
  // 直接进应用，登录表单那一步就被跳过了——测试仍然绿，但它测的不是用户旅程。
  let authenticated = false;
  await page.route("**/api/v1/**", async (route) => {
    const method = route.request().method();
    const path = new URL(route.request().url()).pathname;

    if (method === "POST" && path === "/api/v1/auth/login") {
      authenticated = true;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        headers: { "Set-Cookie": "rgx_access_token=test-token; Path=/" },
        body: json({ token: "test-token", refresh_token: "test-refresh" }),
      });
      return;
    }
    if (path === "/api/v1/auth/me") {
      if (!authenticated) {
        await route.fulfill({
          status: 401,
          contentType: "application/json",
          body: JSON.stringify({ code: 401, message: "unauthenticated" }),
        });
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: json({
          id: "user-chat",
          username: "chat-only",
          tenant_id: "tenant-1",
          role: "member",
          platform: false,
          permissions,
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
          items: [{ id: "chat-1", kind: "chat", name: "合同助手", status: "active", updated_at: new Date().toISOString() }],
          total: 1,
        }),
      });
      return;
    }
    if (path === "/api/v1/chats" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [{ id: "chat-1", name: "合同助手" }], total: 1 }) });
      return;
    }
    if (path === "/api/v1/chats/chat-1" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ id: "chat-1", name: "合同助手" }) });
      return;
    }
    if (path === "/api/v1/chats/chat-1/config" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ id: "chat-1", name: "合同助手", kb_names: ["合同"], llm_id: "deepseek-v4-flash" }) });
      return;
    }
    if (path === "/api/v1/chats/chat-1/sessions" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [{ id: "session-1", name: "既有会话" }], total: 1 }) });
      return;
    }
    if (path === "/api/v1/chats/chat-1/sessions/session-1/messages" && method === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: json({ items: [], next_cursor: undefined }) });
      return;
    }
    if (path === "/api/v1/chat/completions" && method === "POST") {
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        body: [
          'data: {"code":0,"data":{"answer":"收到"}}\n\n',
          'data: {"code":0,"data":{"answer":"，正在处理。","session_id":"session-1"}}\n\n',
          "data: [DONE]\n\n",
        ].join(""),
      });
      return;
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: json(null) });
  });
}

async function login(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("textbox", { name: "用户名" }).fill("chat-only");
  await page.getByRole("textbox", { name: "密码" }).fill("chat-only-pass");
  await page.getByRole("button", { name: "登录" }).click();
}

/**
 * 仪表板自己会发的请求。判据取这几条而不是某个 CSS 类，是因为它们是渲染
 * `DashboardContent` 的**后果**：只要它被提交一次，`useEffect` 就会发。
 */
const DASHBOARD_REQUEST = /^\/api\/v1\/(dashboard|usage|system\/health)\b/;

function watchDashboardRequests(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (request) => {
    const pathname = new URL(request.url()).pathname;
    if (DASHBOARD_REQUEST.test(pathname)) seen.push(pathname);
  });
  return seen;
}

test("① a chat-only user lands on the conversation centre and can ask a question", async ({ page, isMobile }) => {
  test.setTimeout(60_000);
  await mockBackend(page);
  await login(page);

  // 第一块：应用根把这个人送到对话中心，而不是一个它没有任何授权的仪表板。
  await expect(page).toHaveURL(/#\/conversation-center/, { timeout: 15_000 });

  // 第二块：侧栏是他真能进的东西。`数据集` 是 chat-only 角色没有的授权，它不能出现
  // ——侧栏多一个不能进的入口，点进去就是 403，而那正是 E159 的形状。
  //
  // 手机上的侧栏是抽屉，关着的时候入口虽然存在但不可见，先把它拉开，否则这条断言
  // 测的是"抽屉关着"，不是"这个人能看到什么"。
  if (isMobile) {
    await page.getByRole("button", { name: "Toggle Sidebar" }).first().click();
  }
  await expect(page.getByRole("link", { name: "对话中心" }).first()).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole("link", { name: "数据集" })).toHaveCount(0);
  if (isMobile) {
    await page.keyboard.press("Escape");
  }

  // 第三块：真的说一句话。首屏渲染出来不等于能发消息——发送要 `chat|session:create`，
  // 而 E176 的第二个 bug 正是在"权限还在查"的窗口里把它判成了"没有权限"。
  //
  // 目标应用在主内容区里点：侧栏也可能有同名的"最近使用"，两处都合法，但只有
  // 主内容区那一个会把会话打开。
  const targets = page.getByRole("button", { name: /合同助手/ });
  await expect(targets.last()).toBeVisible({ timeout: 15_000 });
  await targets.last().click();

  const composer = page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标");
  await expect(composer).toBeVisible({ timeout: 15_000 });
  // 逐字符输入，不用 `fill`：这个 textarea 是受控的，并且在切上下文时会清空草稿，
  // `fill` 一次性写入的那一帧如果正撞上清空，值就没了——而"输入框有值"是发送的前提。
  await composer.click();
  await composer.pressSequentially("这份合同的风险点在哪");
  await expect(page.getByRole("button", { name: "发送" })).toBeEnabled({ timeout: 10_000 });
  await page.getByRole("button", { name: "发送" }).click();

  await expect(page.getByText(/收到/).first()).toBeVisible({ timeout: 15_000 });
  // 一条明确的失败提示比沉默好：这三条文案里出现任何一条都说明这一趟没成功。
  for (const bad of ["没有权限", "无权", "正在确认当前账号的权限"]) {
    await expect(page.getByText(bad)).toHaveCount(0);
  }
});

test("② the app root never paints the dashboard for a chat-only user", async ({ page }) => {
  test.setTimeout(60_000);
  const dashboardRequests = watchDashboardRequests(page);
  await mockBackend(page);
  await login(page);

  await expect(page.getByText("合同助手").first()).toBeVisible({ timeout: 15_000 });

  // 断言的对象是"这些请求一次都没有发出"，它对"闪一下"和"一直停在那儿"同样敏感
  // ——比断言某个加载态的长相稳，也比断言某一帧的 DOM 更接近故障本身。
  //
  // 顺序上先看请求、再看 URL：故障真的发生时，先失败的那条要能指名道姓。
  expect(dashboardRequests).toEqual([]);
  await expect(page).toHaveURL(/#\/conversation-center/, { timeout: 15_000 });
});

test("③ control: the same page with dashboard grants does paint it", async ({ page }) => {
  test.setTimeout(60_000);
  // 对照组：如果 ② 的判据根本不可能触发（比如请求名写错了、或者这个 mock 下仪表板
  // 永远不渲染），③ 会跟着一起绿，而它必须是绿的——所以 ③ 绿、② 空，② 才说得上话。
  const dashboardRequests = watchDashboardRequests(page);
  await mockBackend(page, { permissions: DASHBOARD_PERMISSIONS });
  await login(page);

  await expect.poll(() => dashboardRequests.length, { timeout: 15_000 }).toBeGreaterThan(0);
  await expect(page).toHaveURL(/#\/$/, { timeout: 15_000 });
});

test("④ the composer is 16px on a phone, so iOS does not zoom the layout", async ({ page, isMobile }) => {
  test.skip(!isMobile, "a phone-only reading: the 16px floor exists for iOS Safari");
  await mockBackend(page);
  await login(page);
  await expect(page).toHaveURL(/#\/conversation-center/, { timeout: 15_000 });

  const composer = page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标");
  await expect(composer).toBeVisible({ timeout: 15_000 });
  const fontSize = await composer.evaluate((element) =>
    Number.parseFloat(getComputedStyle(element).fontSize),
  );
  expect(fontSize).toBeGreaterThanOrEqual(16);
});

// ⑤ 手机尺寸下的三条布局读数（doc/314 第四节末 / doc/305 §2.73）。
//
// 314 对移动端的四条要求里，"dvh 高度"、"visualViewport 监听"、"输入区 sticky"
// 和"44px 触控"都不是组件单测能证明的东西：jsdom 没有布局，`mobileShell.test.ts`
// 读的是 HTML/CSS 字符串。真正的读数只能来自一个有布局引擎的浏览器，所以放在
// 这里。键盘本身**模拟不了**（headless Chromium 不弹软键盘），所以这一条断言的是
// 键盘弹起时那条链路的两端：visualViewport 的读数被写成了 CSS 变量（`--rgx-viewport-height`），
// 以及主内容区真的按这个变量定高。中间"软键盘把 visualViewport 变小"那一步
// 是浏览器的行为，不是我们的代码。
test.describe("⑤ a 390x844 phone", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("has no sideways scroll and keeps the composer inside the visual viewport", async ({
    page,
    isMobile,
  }) => {
    test.skip(!isMobile, "a phone-sized reading: the viewport unit only matters there");
    test.setTimeout(60_000);
    await mockBackend(page);
    await login(page);
    await expect(page).toHaveURL(/#\/conversation-center/, { timeout: 15_000 });

    const targets = page.getByRole("button", { name: /合同助手/ });
    await expect(targets.last()).toBeVisible({ timeout: 15_000 });
    await targets.last().click();

    const composer = page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标");
    await expect(composer).toBeVisible({ timeout: 15_000 });
    await composer.click();
    await composer.pressSequentially("移动端布局读数");

    // ① 没有横向滚动条：一个 390 宽的屏上出现 400 宽的内容，用户只能左右拖。
    const overflow = await page.evaluate(() => ({
      scrollWidth: document.documentElement.scrollWidth,
      innerWidth: window.innerWidth,
    }));
    expect(overflow.scrollWidth).toBeLessThanOrEqual(overflow.innerWidth);

    // ② 输入框在可视区里：不用 `100vh` 的理由就是它在 iOS 上比看得见的区域高，
    //    超出的那一段正好是输入框所在的位置。
    const box = await composer.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.y).toBeGreaterThanOrEqual(0);
    expect(box!.y + box!.height).toBeLessThanOrEqual(844);

    // ③ visualViewport 的读数写进了 CSS 变量，主内容区按它定高。
    const shell = await page.evaluate(() => {
      const root = document.documentElement;
      const main = document.querySelector("main");
      return {
        varHeight: root.style.getPropertyValue("--rgx-viewport-height").trim(),
        mainHeight: main ? main.getBoundingClientRect().height : -1,
        innerHeight: window.innerHeight,
      };
    });
    expect(shell.varHeight).toMatch(/^\d+(\.\d+)?px$/);
    // ±1px 是浏览器把小数高度取整的余量，不是给"差很多"留的口子。
    expect(Math.abs(Number.parseFloat(shell.varHeight) - shell.innerHeight)).toBeLessThanOrEqual(1);
    expect(shell.mainHeight).toBeLessThanOrEqual(shell.innerHeight + 1);

    // ④ 拇指够得着的两个按钮：发送与侧栏开关都不小于 44px（314 的触控下限）。
    for (const button of [
      page.getByRole("button", { name: "发送" }),
      page.getByRole("button", { name: "Toggle Sidebar" }).first(),
    ]) {
      const size = await button.evaluate((element) => {
        const rect = element.getBoundingClientRect();
        return { width: rect.width, height: rect.height };
      });
      expect(size.width).toBeGreaterThanOrEqual(44);
      expect(size.height).toBeGreaterThanOrEqual(44);
    }
  });
});
