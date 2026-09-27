/**
 * Error code → Chinese display message mapping.
 *
 * The backend returns English error messages in the response envelope.
 * This map provides user-facing Chinese translations keyed by business
 * error code.  When a code is found here the Chinese message is preferred;
 * otherwise the original backend message is used as-is.
 *
 * Keep this file in sync with doc/12_前后端接口交互规范.md §2.2.
 */

const ERROR_MESSAGES: Record<number, string> = {
  // ── 400xx — 参数/验证错误 ──────────────────────────
  40000: "请求参数无效",
  40001: "用户名至少需要 3 个字符",
  40003: "服务地址和 API Key 为必填项",
  40010: "数据集名称不能为空",
  40020: "用户名和密码为必填项",
  40021: "该角色不可分配",
  40022: "用户名已存在",
  40024: "密码至少需要 8 个字符",
  40026: "邮箱已存在",
  40025: "无效的状态值",
  40030: "提供商名称和类型为必填项",
  40031: "provider_id、model_alias 和 target_model 为必填项",
  40040: "实例名称为必填项",
  40041: "模型名称为必填项",
  40050: "团队名称不能为空",
  40060: "无效的请求体",
  40061: "模型名称为必填项",
  40070: "项目名称不能为空",
  40080: "角色名称不能为空",
  40081: "无效的角色范围",
  40082: "内建角色不可修改或删除",
  40083: "角色已分配给用户，不可删除",
  40084: "角色下有子角色，不可删除",
  40085: "角色不能是自身的父角色",
  40086: "父角色不存在",
  40087: "智能体名称不能为空",
  40088: "智能体 DSL 不能为空",
  40089: "仅允许删除已完成、失败或停止的任务",
  40095: "记忆名称不能为空",
  40096: "用户输入和智能体回复为必填项",
  40097: "记忆类型不能为空，且必须包含原始记忆",
  40098: "请选择嵌入模型",

  // ── 401xx — 认证错误 ───────────────────────────────
  401: "认证信息异常，请重新登录",
  40101: "用户名或密码错误",

  // ── 401xx — Answer Delivery 契约、知识影响面与 OIDC（doc/118 F-16）──
  40120: "回答快照或导出请求参数不完整",
  40121: "回答状态无效",
  40122: "完成原因无效",
  40123: "回答生命周期状态无效",
  40130: "导出请求无效或缺少可导出的回答快照",
  40131: "导出格式不支持",
  40132: "PDF 渲染器未配置字体，请联系管理员",
  40133: "导出任务失败或已过期，请重试",
  40134: "回答快照 schema 版本不受支持",
  40140: "知识影响面报告参数无效",
  40141: "重复候选参数无效，需填写来源、候选及至少一个理由",
  40142: "重复治理决策参数无效，需填写关系类型与备注",
  40143: "替代关系处置失败，需在证据中提供数据集信息",
  40150: "OIDC 登录未配置，请联系管理员",
  40151: "OIDC 回调状态校验失败，请重新发起登录",

  // ── 409xx — 资源冲突 ───────────────────────────────
  40920: "企业微信回调重复，请勿重复提交",

  // ── 403xx — 授权错误 ───────────────────────────────
  403: "权限不足，无法执行此操作",
  40301: "所属工作区已停用，请联系管理员",

  // ── 404xx — 资源不存在 ─────────────────────────────
  40400: "工作区不存在",
  404: "请求的数据集不存在或当前范围不可访问",

  // ── 409xx — 资源冲突 ───────────────────────────────
  40901: "系统已经初始化，不可重复操作",
  40909: "审批状态已变更，请刷新后重试",
  40911: "名称已存在，请更换后重试",
  40903: "平台管理员不可删除",
  40904: "平台管理员信息不可修改",
  40905: "不可移除或禁用最后一个工作区管理员",
  40906: "不可修改自己的角色",
  40907: "不可修改自己的状态",
  40908: "不可删除自己",
  40930: "发布操作状态冲突，请刷新后重试",
  40933: "仅失败的导出任务可以重试",

  // ── 429xx — 限流 ───────────────────────────────────
  429: "请求过于频繁，请稍后重试",
  42971: "请选择通知通道",
  42972: "通知服务未启用",
  42973: "已成功的通知无需重试",
  42974: "通知正在投递，请稍后重试",

  // ── 500xx — 服务端错误 ─────────────────────────────
  500: "服务器内部错误，请联系管理员",
};

/** Fallback messages keyed by HTTP status when no code-specific mapping exists. */
const HTTP_STATUS_MESSAGES: Record<number, string> = {
  0: "网络连接失败，请检查网络",
  400: "请求参数无效",
  401: "登录已过期，请重新登录",
  403: "权限不足，无法执行此操作",
  404: "请求的资源不存在",
  409: "操作冲突，请刷新后重试",
  429: "请求过于频繁，请稍后重试",
  500: "服务器内部错误，请联系管理员",
  502: "网关错误，请稍后重试",
  503: "服务暂不可用，请稍后重试",
};

/**
 * Get the user-facing Chinese message for an error code.
 *
 * Lookup order:
 * 1. Exact business code match (e.g. 40082 → "内建角色不可修改或删除")
 * 2. Original backend message, because detailed operational errors must not be
 *    hidden by a generic status message
 * 3. HTTP status fallback when the backend did not provide a usable message
 */
export function getErrorMessage(
  code: number,
  status: number,
  fallbackMessage: string,
): string {
  if (code === 0 && status === 0) {
    return HTTP_STATUS_MESSAGES[0] ?? fallbackMessage;
  }
  if (ERROR_MESSAGES[code]) return ERROR_MESSAGES[code];
  return fallbackMessage || HTTP_STATUS_MESSAGES[status] || fallbackMessage;
}

const LOGIN_ERROR_MESSAGES: Record<number, string> = {
  40101: "用户名或密码错误",
  40301: "所属工作区已停用，请联系管理员",
  429: "登录尝试过于频繁，请等待限流窗口结束后再试",
};

const LOGIN_STATUS_MESSAGES: Record<number, string> = {
  400: "登录请求无效，请检查用户名和密码",
  401: "认证信息异常，请重新登录",
  429: "登录尝试过于频繁，请稍后重试",
};

const SETUP_ERROR_MESSAGES: Record<number, string> = {
  40001: "用户名至少需要 3 个字符",
  40002: "密码至少需要 8 个字符",
  40003: "引擎地址和 API Key 为必填项",
};

export function getSetupErrorMessage(
  code: number,
  fallbackMessage: string,
): string {
  return SETUP_ERROR_MESSAGES[code] ?? fallbackMessage;
}

export function getLoginErrorMessage(
  code: number,
  status: number,
  fallbackMessage: string,
): string {
  return LOGIN_ERROR_MESSAGES[code] ?? LOGIN_STATUS_MESSAGES[status] ?? fallbackMessage;
}

