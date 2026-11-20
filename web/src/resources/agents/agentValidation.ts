import { api } from "../../lib/api";

/**
 * Save-time canvas validation, mirroring `internal/service/agent_validate.go`.
 *
 * The backend already repairs the mistakes that have a single correct answer
 * (canvas labels → engine component names, illegal node ids, `prompt` →
 * `user_prompt`, an empty Answer forwarding its upstream). What is left are
 * decisions only the author can make — a knowledge node with no knowledge base,
 * a model node with no model — and those used to surface as a broken chat hours
 * after publishing. This module turns them into a list the editor can show
 * before saving.
 */

/** Every correction the engine boundary can apply on the way out. */
export type AgentDslFixKind =
  | "component_name"
  | "node_id"
  | "node_link"
  | "prompt_param"
  | "message_content";

export interface AgentDslFix {
  kind: AgentDslFixKind;
  node_id?: string;
  node_label?: string;
  from?: string;
  to?: string;
  message?: string;
}

export interface AgentDslIssue {
  code: string;
  severity: "error" | "warning";
  node_id?: string;
  node_label?: string;
  field?: string;
  params?: Record<string, string>;
  message: string;
}

export interface AgentValidationReport {
  ok: boolean;
  canvas_category?: string;
  node_count?: number;
  auto_fixed?: AgentDslFix[];
  errors?: AgentDslIssue[];
  warnings?: AgentDslIssue[];
  /** Sections the backend could not check (components/models/datasets). */
  unchecked?: string[];
}

const EMPTY: AgentValidationReport = { ok: true, auto_fixed: [], errors: [], warnings: [] };

/** validateAgentDsl checks the DSL the editor currently holds, without saving it. */
export async function validateAgentDsl(
  dsl: Record<string, any>,
  canvasCategory: string,
): Promise<AgentValidationReport> {
  const res = await api.post<{ code: number; data: unknown }>("/agents/validate", {
    dsl,
    canvas_category: canvasCategory,
  });
  return normalizeReport(unwrap(res));
}

/** fetchAgentValidation checks the DSL the engine already stores for the agent. */
export async function fetchAgentValidation(agentId: string): Promise<AgentValidationReport> {
  const res = await api.get<{ code: number; data: unknown }>(`/agents/${agentId}/validation`);
  return normalizeReport(unwrap(res));
}

function unwrap(res: { data?: { code?: number; data?: unknown } }): unknown {
  const envelope = res?.data;
  if (!envelope || typeof envelope.code !== "number" || envelope.code !== 0) {
    throw new Error(`agent validation failed (code=${envelope?.code ?? "unknown"})`);
  }
  return envelope.data;
}

/**
 * normalizeReport keeps one bad field from crashing the editor: an older backend
 * (or a stripped response) must read as "nothing to report", never as `undefined`
 * sections that the renderer would have to guard one by one.
 */
export function normalizeReport(raw: unknown): AgentValidationReport {
  const data = (raw ?? {}) as Partial<AgentValidationReport>;
  return {
    ok: data.ok !== false,
    canvas_category: data.canvas_category,
    node_count: typeof data.node_count === "number" ? data.node_count : undefined,
    auto_fixed: Array.isArray(data.auto_fixed) ? data.auto_fixed : [],
    errors: Array.isArray(data.errors) ? data.errors : [],
    warnings: Array.isArray(data.warnings) ? data.warnings : [],
    unchecked: Array.isArray(data.unchecked) ? data.unchecked : [],
  };
}

/** AUTO_FIX_LABELS maps a correction kind to the sentence shown next to it. */
export const AUTO_FIX_LABELS: Record<string, string> = {
  component_name: "节点类型已改为引擎名称",
  node_id: "节点 ID 含非法字符，已重命名并同步引用",
  node_link: "节点连线只写在 obj 里，已同步到引擎读取的位置",
  prompt_param: "提示词参数已改为引擎字段名",
  message_content: "回答节点为空，已改为转发上游内容",
};

/** fixSummary renders one auto-correction as "<node>: <what changed>". */
export function fixSummary(fix: AgentDslFix): string {
  const node = fix.node_label || fix.node_id || "";
  const what = AUTO_FIX_LABELS[fix.kind] ?? fix.kind;
  const detail =
    fix.from && fix.to ? `${what}（${fix.from} → ${fix.to}）` : what;
  return node ? `${node}：${detail}` : detail;
}

/** issueSummary renders one blocking/warning finding with its node. */
export function issueSummary(issue: AgentDslIssue): string {
  const node = issue.node_label || issue.node_id || "";
  return node ? `${node}：${issue.message}` : issue.message;
}

/** reportLines renders the report for a toast or an alert body. */
export function reportLines(report: AgentValidationReport): string[] {
  return [...(report.errors ?? []), ...(report.warnings ?? [])].map(issueSummary);
}

export { EMPTY as EMPTY_VALIDATION_REPORT };
