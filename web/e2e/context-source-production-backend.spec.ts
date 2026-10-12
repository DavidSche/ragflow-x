import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Browser, type Page } from "@playwright/test";

const ownerUsername = process.env.RGX_E2E_OWNER_USERNAME;
const ownerPassword = process.env.RGX_E2E_OWNER_PASSWORD;
const memberUsername = process.env.RGX_E2E_MEMBER_USERNAME;
const memberPassword = process.env.RGX_E2E_MEMBER_PASSWORD;
const otherUsername = process.env.RGX_E2E_OTHER_USERNAME;
const otherPassword = process.env.RGX_E2E_OTHER_PASSWORD;
const chatID = process.env.RGX_E2E_CHAT_ID;
const sessionID = process.env.RGX_E2E_SESSION_ID;
const query = process.env.RGX_E2E_QUERY;
const answer = process.env.RGX_E2E_ANSWER;
const memoryID = process.env.RGX_E2E_MEMORY_ID;
const policyID = process.env.RGX_E2E_POLICY_ID;

const liveConfigured =
  Boolean(ownerUsername && ownerPassword && memberUsername && memberPassword &&
    otherUsername && otherPassword && chatID && sessionID && query && answer &&
    memoryID && policyID);

async function login(page: Page, username: string, password: string): Promise<void> {
  await page.goto("/#/login");
  await page.getByLabel("用户名").fill(username);
  await page.getByRole("textbox", { name: "密码" }).fill(password);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByLabel("用户名")).toHaveCount(0);
}

async function assertNoSensitiveContent(text: string): Promise<void> {
  for (const sensitive of [query, answer, memoryID, policyID]) {
    expect(text, "browser content leaked a governed fixture value").not.toContain(sensitive ?? "");
  }
  expect(text).not.toContain("raw_payload");
}

test("production backend owner sees governed metadata and non-owners fail closed", async ({
  browser,
}) => {
  test.skip(!liveConfigured, "production browser governance live fixture is required");

  const owner = await browser.newContext();
  const page = await owner.newPage();
  let completionRequestID = "";
  page.on("request", (request) => {
    if (request.method() === "POST" && new URL(request.url()).pathname === "/api/v1/chat/completions") {
      completionRequestID = request.headers()["x-request-id"] ?? "";
    }
  });

  await login(page, ownerUsername!, ownerPassword!);
  await page.goto(
    `/#/conversation-center?kind=chat&targetId=${encodeURIComponent(chatID!)}&contextId=${encodeURIComponent(sessionID!)}`,
  );
  const input = page.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标");
  await expect(input).toBeVisible();

  const completionResponse = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      new URL(response.url()).pathname === "/api/v1/chat/completions",
  );
  await input.fill(query!);
  await page.getByRole("button", { name: "发送" }).click();
  const decisionResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname.startsWith(
        "/api/v1/conversation-context/decisions/by-request/",
      ),
  );
  const contextButtonClicked = page.evaluate(
    () =>
      new Promise<boolean>((resolve) => {
        const observer = new MutationObserver(() => tryClick());
        const tryClick = () => {
          const button = document.querySelector<HTMLButtonElement>(
            'button[aria-label="上下文来源"]',
          );
          if (!button) return;
          observer.disconnect();
          button.click();
          resolve(true);
        };
        tryClick();
        observer.observe(document.body, {
          attributes: true,
          childList: true,
          subtree: true,
        });
        setTimeout(() => {
          observer.disconnect();
          resolve(false);
        }, 15_000);
      }),
  );
  const [clicked, completion] = await Promise.all([
    contextButtonClicked,
    completionResponse,
  ]);
  expect(clicked).toBe(true);
  expect(completion.status()).toBe(200);
  expect(completionRequestID).not.toBe("");
  const decisionURL = new URL((await decisionResponse).url());
  const decisionPath = decisionURL.pathname;
  expect(decisionPath).toBe(
    `/api/v1/conversation-context/decisions/by-request/${completionRequestID}`,
  );
  const decision = await decisionResponse;
  expect(decision.status()).toBe(200);
  const decisionBody = (await decision.json()) as {
    data?: {
      request_id?: string;
      decision?: string;
      detail?: string;
    };
  };
  expect(decisionBody.data?.request_id).toBe(completionRequestID);
  expect(decisionBody.data?.decision).toBe("memory_allowed");
  const detail = JSON.parse(decisionBody.data?.detail ?? "{}") as {
    memory_retrieved?: boolean;
    memory_result_count?: number;
  };

  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("Memory 已授权")).toBeVisible();
  await expect(
    dialog.getByText("仅显示治理元数据，不显示 Memory 密文、密钥、原始载荷或敏感审计明细。"),
  ).toBeVisible();
  await assertNoSensitiveContent(await dialog.innerText());

  const scan = await new AxeBuilder({ page }).include('[role="dialog"]').analyze();
  const actionable = scan.violations.filter((violation) =>
    ["critical", "serious"].includes(violation.impact ?? ""),
  );
  expect(actionable).toEqual([]);

  const finalDecision = await page.request.get(
    `/api/v1/conversation-context/decisions/by-request/${completionRequestID}`,
  );
  expect(finalDecision.status()).toBe(200);
  const finalDecisionBody = (await finalDecision.json()) as {
    data?: {
      decision?: string;
      detail?: string;
    };
  };
  expect(finalDecisionBody.data?.decision).toBe("memory_allowed");
  const finalDetail = JSON.parse(finalDecisionBody.data?.detail ?? "{}") as {
    memory_retrieved?: boolean;
    memory_result_count?: number;
  };
  expect(finalDetail.memory_retrieved).toBe(true);
  expect(finalDetail.memory_result_count ?? 0).toBeGreaterThan(0);
  await owner.close();

  for (const [name, username, password, expectRestoreFailure] of [
    ["same-tenant non-owner", memberUsername!, memberPassword!, false],
    ["cross-tenant user", otherUsername!, otherPassword!, true],
  ] as const) {
    const restricted = await browser.newContext();
    const restrictedPage = await restricted.newPage();
    await login(restrictedPage, username, password);
    await restrictedPage.goto(
      `/#/conversation-center?kind=chat&targetId=${encodeURIComponent(chatID!)}&contextId=${encodeURIComponent(sessionID!)}`,
    );
    if (expectRestoreFailure) {
      await expect(restrictedPage.getByText("目标或会话不存在、已删除或无权访问")).toBeVisible();
    } else {
      await expect(
        restrictedPage.getByPlaceholder("输入问题；/ 切换类型，@ 选择目标"),
      ).toBeVisible();
    }
    await expect(restrictedPage.getByRole("button", { name: "上下文来源" })).toHaveCount(0);
    const restrictedDecision = await restrictedPage.request.get(
      `/api/v1/conversation-context/decisions/by-request/${completionRequestID}`,
    );
    expect(restrictedDecision.status()).toBe(404);
    await assertNoSensitiveContent(await restrictedDecision.text());
    await assertNoSensitiveContent(await restrictedPage.locator("body").innerText());
    await restricted.close();
  }
});
