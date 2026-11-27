/**
 * Error code → Chinese display message mapping.
 *
 * The backend returns English error messages in the response envelope.
 * This map provides user-facing Chinese translations keyed by business
 * error code.
 *
 * A code belongs here only if it means exactly one thing. Codes the backend
 * uses as generic buckets are deliberately absent, so the specific backend
 * detail wins by construction instead of being overwritten by a canned
 * sentence:
 *
 *   40000   handler-level "invalid request" bucket (field-specific details)
 *   40060   gateway "invalid body" bucket
 *   404     one detail per entity (chat, dataset, agent, …)
 *
 * `40085` used to be in that group (18 unrelated messages on one code, see
 * doc/294) until the backend split it: 40085 is Role-only again and the
 * request-precondition family moved to 41100-41199 (go `httperr/codes.go`).
 *
 * Keep this file in sync with doc/specification/12_前后端接口交互规范.md §2.2.
 */

const ERROR_MESSAGES: Record<number, string> = {
  // ── 400xx — 参数/验证错误 ──────────────────────────
  // 40000 / 40060 are generic buckets and stay out of this table on purpose.
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
  40099: "画布存在无法运行的问题，请按提示修改后再发布",

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
  40032: "模型类型不在默认模型可选范围内",
  40037: "该模型不在当前租户的模型清单中，不能设为默认",
  40038: "该模型的类型与目标默认类型不一致",
  40035: "当前 RAGFlow 版本不支持该模型的默认类型",
  40036: "缺少 expected_model_id，请先读取当前默认模型后再提交",

  // ── 411xx — 请求前置校验（原 40085 的多语义拆分，go httperr/codes.go）──
  41101: "应用类型必须是 chat、search 或 agent",
  41102: "实例化需要选择项目",
  41103: "只有已发布的场景模板可以实例化",
  41104: "该场景模板不支持所选的实例化形态",
  41105: "该模板没有智能体画布，请选择对话或检索形态",
  41106: "模板内容声明的形态与所选形态不一致",
  41110: "更新场景模板必须提交内容",
  41111: "模板建议的数据集未找到",
  41112: "骨架 id 必须形如 <source>:<upstream_id>",
  41113: "不支持该骨架来源",
  41114: "模板需要的能力在本工作区没有默认模型",
  41120: "数据集不在当前工作区",
  41121: "引擎尚未注册嵌入模型，请先配置默认嵌入模型",
  41122: "没有可更新的字段",
  41130: "相邻审批步骤不能由同一人审批",
  41140: "引擎拒绝了这个请求",
  // A duplicate Search App name is fixable by the operator (rename it), so the
  // sentence points at the name field. RAGFlow reports it as HTTP 200 + code 102
  // on `PUT /searches/<id>` only — `POST` accepts the duplicate
  // (doc/304 §4 E169).
  41141: "已存在同名搜索应用（RAGFlow 不允许重名），请换一个名称后重试",
  41150: "该 API Key 已吊销或已轮换，不能再轮换",
  41160: "父角色选择会造成角色继承成环，请换一个父角色",
  // Role preconditions that used to borrow 40087–40090, which already meant
  // "智能体名称不能为空" / "智能体 DSL 不能为空" / task and document messages.
  41161: "父角色与角色的作用域不一致，工作区角色和平台角色不能互相继承",
  41162: "父角色属于其他工作区，不能选作父角色",
  41163: "角色下还有子角色，改作用域前请先把子角色挂到其他父级",
  41164: "提交的权限项不在权限目录内，请刷新后重新选择",

  // ── 409xx — 资源冲突 ───────────────────────────────
  40920: "企业微信回调重复，请勿重复提交",

  // ── 403xx — 授权错误 ───────────────────────────────
  403: "权限不足，无法执行此操作",
  40301: "所属工作区已停用，请联系管理员",

  // ── 404xx — 资源不存在 ─────────────────────────────
  40400: "工作区不存在",
  // 404 stays out of this table: the detail names the entity (chat, dataset,
  // agent, …) and is always more accurate than one generic sentence.

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
  40912: "默认模型已被其他人修改，请刷新后重试",
  40913: "另一个默认模型设置操作正在进行，请稍后重试",

  // ── 429xx — 限流 ───────────────────────────────────
  429: "请求过于频繁，请稍后重试",
  42971: "请选择通知通道",
  42972: "通知服务未启用",
  42973: "已成功的通知无需重试",
  42974: "通知正在投递，请稍后重试",

  // ── 500xx/502xx — 服务端/上游错误 ─────────────────
  500: "服务器内部错误，请联系管理员",
  50256: "设置默认模型失败，请查看 RAGFlow 返回的原因后重试",
  50257: "无法确认默认模型是否已设置，请刷新查看当前状态后再决定",
  50258: "写锁连接不可用，请稍后重试",
  50259: "写锁执行失败，请稍后重试",
  50293: "读取默认模型失败，暂无法确认当前状态",
};

/**
 * Literal English details the backend emits for flows a user can actually hit.
 * Only exact or anchored matches live here: a rough match would be the same
 * class of bug as an overloaded code map. Anything unmatched keeps the raw
 * detail, which is still more accurate than a wrong Chinese sentence.
 */
const EXACT_DETAIL_MESSAGES: Record<string, string> = {
  "role cannot be its own parent": "角色不能是自身的父角色",
  "parent role not found": "父角色不存在",
  "the same user cannot approve adjacent steps": "相邻审批步骤不能由同一人审批",
  "dataset not found": "数据集不存在或当前范围不可访问",
  "agent not found": "智能体不存在",
  "chat not found": "对话助手不存在",
  "project not found": "项目不存在",
  "tenant not found": "工作区不存在",
  "memory not found": "记忆不存在",
  "search app not found": "检索应用不存在",
  "model provider not found": "模型提供商不存在",
  "document not found": "文档不存在",
  "chunk not found": "切片不存在",
  "role not found": "角色不存在",
  "user not found": "用户不存在",
  "team not found": "团队不存在",
  "approval not found": "审批不存在",
  "evaluation set not found": "评测集不存在",
  "model not found": "模型不存在",
  "api key not found": "API Key 不存在",
  "task not found": "任务不存在",
  "invalid request body": "请求体无效",
  "invalid request": "请求无效",
  "empty file": "文件为空",
  "file is required": "请选择文件",
  "file too large (max 20MB)": "文件过大（上限 20MB）",
  "logo file too large (max 2MB)": "Logo 文件过大（上限 2MB）",
  "unsupported image type": "不支持的图片格式",
  "app_type must be chat, search or agent": "应用类型必须是 chat、search 或 agent",
  "only published scenario templates can be instantiated": "只有已发布的场景模板可以实例化",
  "scenario template does not support chat assistants": "该场景模板不支持对话助手",
  "project_id is required for instantiation": "实例化需要选择项目",
  "payload is required when updating a scenario template": "更新场景模板必须提交内容",
  "skeleton id must be <source>:<upstream_id>": "骨架 id 必须形如 <source>:<upstream_id>",
  "unsupported skeleton source": "不支持该骨架来源",
  "nothing to update: send name, compilation_token_budget or pushdown_enabled":
    "没有可更新的字段：请提交名称、编译预算或推送下推开关",
};

/** Form names the backend puts in app-type sentences, in the operator's words. */
const APP_FORM_LABELS: Record<string, string> = {
  chat: "对话",
  search: "检索",
  agent: "智能体",
};

const DETAIL_PATTERNS: Array<{ pattern: RegExp; render: (match: RegExpMatchArray) => string }> = [
  {
    pattern: /^dataset not found in workspace:\s*(.+)$/,
    render: (m) => `数据集不在当前工作区（${m[1]}），请切换到该工作区或重新创建数据集`,
  },
  {
    pattern: /^no embedding model is registered in the engine/i,
    render: () => "引擎尚未注册嵌入模型，请先在模型管理中配置嵌入模型再创建数据集",
  },
  {
    pattern: /^dataset suggestions not found:\s*(.+)$/,
    render: (m) => `模板建议的数据集未找到：${m[1]}`,
  },
  {
    pattern: /^scenario template does not support (\S+) applications?$/,
    render: (m) => `该场景模板不支持 ${APP_FORM_LABELS[m[1]] ?? m[1]} 形态`,
  },
  {
    pattern: /^scenario pack requires model capabilities with no tenant default:\s*(.+)$/,
    render: (m) => `模板需要这些能力，但本工作区没有对应的默认模型：${m[1]}`,
  },
  {
    pattern: /^this template cannot be instantiated as an agent/i,
    render: () => "该模板没有智能体画布，请选择对话或检索形态",
  },
  // Handler-level validation (code 40000): the detail carries the field name and
  // is the only place the operator can learn which parameter is missing.
  {
    pattern: /^([a-z][a-z0-9_]*) (?:is|are) required$/,
    render: (m) => `缺少必填参数：${m[1]}`,
  },
  {
    pattern: /^(?:invalid|unsupported) .*payload$/,
    render: () => "请求体参数无效或缺少必填字段",
  },
];

/** Translate a backend detail message when we know it verbatim; else "". */
export function translateErrorDetail(detail: string): string {
  const text = detail.trim();
  if (!text) return "";
  const exact = EXACT_DETAIL_MESSAGES[text];
  if (exact) return exact;
  for (const rule of DETAIL_PATTERNS) {
    const match = text.match(rule.pattern);
    if (match) return rule.render(match);
  }
  return "";
}

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
 * 1. Verbatim/anchored translation of the backend detail (most specific; also
 *    the only correct path for the generic 40000/40060/404 buckets, which are
 *    deliberately absent from ERROR_MESSAGES)
 * 2. Business code match — every code in the table means exactly one thing
 * 3. Raw backend detail
 * 4. HTTP status fallback when the backend did not provide a usable message
 */
export function getErrorMessage(
  code: number,
  status: number,
  fallbackMessage: string,
): string {
  if (code === 0 && status === 0) {
    return HTTP_STATUS_MESSAGES[0] ?? fallbackMessage;
  }
  const detail = (fallbackMessage ?? "").trim();
  const translated = translateErrorDetail(detail);
  if (translated) return translated;
  if (ERROR_MESSAGES[code]) return ERROR_MESSAGES[code];
  return detail || HTTP_STATUS_MESSAGES[status] || fallbackMessage;
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

