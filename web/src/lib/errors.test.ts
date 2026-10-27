// ScenarioID: SC-FE-DATA-001
import { describe, expect, test } from "vitest";
import {
  getErrorMessage,
  getLoginErrorMessage,
  getSetupErrorMessage,
} from "./errors";

describe("getErrorMessage", () => {
  test("prefers the exact business-code mapping", () => {
    expect(getErrorMessage(40082, 400, "builtin role")).toBe("内建角色不可修改或删除");
    expect(getErrorMessage(429, 429, "rate limited")).toBe("请求过于频繁，请稍后重试");
    expect(getErrorMessage(40101, 401, "invalid username or password")).toBe("用户名或密码错误");
  });

  // doc/118 F-16: newly added answer-delivery / knowledge-ops / release codes
  // must resolve to the Chinese mapping instead of raw backend English.
  test("maps answer delivery, knowledge ops and release conflict codes", () => {
    expect(getErrorMessage(40120, 400, "tenant, session, assistant and request are required")).toBe("回答快照或导出请求参数不完整");
    expect(getErrorMessage(40121, 400, "invalid answer status")).toBe("回答状态无效");
    expect(getErrorMessage(40122, 400, "invalid completion reason")).toBe("完成原因无效");
    expect(getErrorMessage(40123, 400, "invalid answer lifecycle")).toBe("回答生命周期状态无效");
    expect(getErrorMessage(40130, 400, "answer snapshot and supported format are required")).toBe("导出请求无效或缺少可导出的回答快照");
    expect(getErrorMessage(40131, 400, "unsupported export format")).toBe("导出格式不支持");
    expect(getErrorMessage(40132, 400, "pdf renderer requires RGX_EXPORT_PDF_FONT")).toBe("PDF 渲染器未配置字体，请联系管理员");
    expect(getErrorMessage(40134, 400, "unsupported answer schema version")).toBe("回答快照 schema 版本不受支持");
    expect(getErrorMessage(40140, 400, "dataset impact target is required")).toBe("知识影响面报告参数无效");
    expect(getErrorMessage(40141, 400, "duplicate source, candidate and at least one reason are required")).toBe("重复候选参数无效，需填写来源、候选及至少一个理由");
    expect(getErrorMessage(40150, 400, "OIDC login is not configured")).toBe("OIDC 登录未配置，请联系管理员");
    expect(getErrorMessage(40151, 400, "invalid OIDC callback state")).toBe("OIDC 回调状态校验失败，请重新发起登录");
    expect(getErrorMessage(40142, 400, "relation must be duplicate, supersedes, conflicts or related")).toBe("重复治理决策参数无效，需填写关系类型与备注");
    expect(getErrorMessage(40143, 400, "document retirement requires dataset_id evidence")).toBe("替代关系处置失败，需在证据中提供数据集信息");
    expect(getErrorMessage(40920, 409, "duplicate WeCom callback")).toBe("企业微信回调重复，请勿重复提交");
    expect(getErrorMessage(40930, 409, "stale release worker")).toBe("发布操作状态冲突，请刷新后重试");
    expect(getErrorMessage(40933, 409, "only failed export jobs can be retried")).toBe("仅失败的导出任务可以重试");
  });

  test("preserves detailed backend messages before HTTP fallback", () => {
    expect(getErrorMessage(12345, 403, "denied")).toBe("denied");
    expect(getErrorMessage(99999, 404, "missing resource")).toBe("missing resource");
  });

  test("uses the HTTP status message when backend detail is unavailable", () => {
    expect(getErrorMessage(99999, 404, "")).toBe("请求的资源不存在");
  });

  test("uses a friendly generic message for HTTP 404 business responses", () => {
    expect(getErrorMessage(404, 404, "dataset not found")).toBe(
      "请求的数据集不存在或当前范围不可访问",
    );
  });

  test("does not globally reinterpret the overloaded setup password code", () => {
    expect(getErrorMessage(40002, 400, "invalid status")).toBe("invalid status");
  });

  test("falls back to the original backend message otherwise", () => {
    expect(getErrorMessage(0, 0, "network down")).toBe("网络连接失败，请检查网络");
    expect(getErrorMessage(70000, 200, "custom english")).toBe("custom english");
  });
});

describe("getSetupErrorMessage", () => {
  test("prefers setup-specific validation mappings", () => {
    expect(getSetupErrorMessage(40001, "username")).toBe("用户名至少需要 3 个字符");
    expect(getSetupErrorMessage(40002, "password")).toBe("密码至少需要 8 个字符");
  });

  test("preserves detailed setup and configuration backend errors", () => {
    expect(getSetupErrorMessage(40004, "migrate database: migration 71 failed")).toBe(
      "migrate database: migration 71 failed",
    );
    expect(getSetupErrorMessage(40005, "runtime database privilege check failed")).toBe(
      "runtime database privilege check failed",
    );
  });
});

describe("getLoginErrorMessage", () => {
  test("uses the login-specific mapping when a code matches", () => {
    expect(getLoginErrorMessage(40101, 401, "invalid credentials")).toBe("用户名或密码错误");
    expect(getLoginErrorMessage(429, 429, "rate limited")).toBe("登录尝试过于频繁，请等待限流窗口结束后再试");
  });

  test("falls back to the HTTP status message when code is unknown", () => {
    expect(getLoginErrorMessage(12345, 400, "invalid request")).toBe("登录请求无效，请检查用户名和密码");
  });

  test("falls back to the original backend message for unrecognized login errors", () => {
    expect(getLoginErrorMessage(12345, 403, "login denied")).toBe("login denied");
  });
});
