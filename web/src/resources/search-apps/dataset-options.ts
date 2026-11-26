import { useEffect, useState } from "react";
import { api, ApiError } from "../../lib/api";
import type { DatasetLike } from "../chats/chat-types";

/**
 * 搜索应用「可绑定数据集」选项（P1 向导数据面契约，doc/develop/252 §2）。
 *
 * `GET /api/v1/search-apps/dataset-options`（读权限 read/search-app，治理 scope）返回
 * 当前 scope 内可绑定数据集：
 * - `source=linked`：`rgx_dataset_link` 行（带 link_id/name/document_count/sensitivity）
 * - `source=engine`：同一引擎账号下尚未链接的库（引擎兜底）
 *
 * 形状兼容 chat-types 的 `DatasetLike`（id=引擎 kb id、name、ragflow_dataset_id），
 * 绑定提交用 `ragflow_dataset_id` 即可（后端三轨引用解析的引擎 id passthrough）。
 */
export interface SearchAppDatasetOption {
  link_id?: string;
  name: string;
  ragflow_dataset_id: string;
  document_count?: number;
  sensitivity?: string;
  source: "linked" | "engine";
}

/**
 * 把引擎侧的 `kb_ids` 翻译成**工作区 dataset link id**，才能问
 * `GET /datasets/:id/documents`。
 *
 * RAGFlow 的 search app 配置里存的是引擎 dataset id；我们的 `/datasets/:id/...`
 * 按 `rgx_dataset_link.id`（工作区别名）解析（后端 `GetDatasetLink(tenantID, id)`）。
 * 直接拿引擎 id 去问会 404 `dataset not found` —— 2026-11-26 的 4.151 演练里，
 * 每次打开搜索应用详情都会刷三条 404，文档下拉永远是空的（doc/304 §4 E169 次因）。
 *
 * 解析不到 link 的引擎 id 会被丢弃：没有工作区身份就列不出文档，
 * 发一个注定 404 的请求只会把控制台弄脏。顺序保持、结果去重。
 */
export function workspaceDatasetIdsForDocuments(
  kbIds: string[],
  datasets: DatasetLike[] | undefined,
): string[] {
  const rows = datasets ?? [];
  const byEngine = new Map<string, string>();
  const byLink = new Set<string>();
  for (const d of rows) {
    if (d.id) byLink.add(d.id);
    if (d.ragflow_dataset_id && d.id) byEngine.set(d.ragflow_dataset_id, d.id);
  }
  const out: string[] = [];
  for (const raw of kbIds ?? []) {
    const id = byLink.has(raw) ? raw : byEngine.get(raw) ?? "";
    if (!id || out.includes(id)) continue;
    out.push(id);
  }
  return out;
}

export interface SearchAppDatasetOptionsState {
  /**
   * 已成功加载则非 null（空数组 = scope 内确实无可绑数据集）；
   * null = 加载中或加载失败（调用方回退旧 /datasets 逻辑以保持向后兼容）。
   */
  options: SearchAppDatasetOption[] | null;
  /** 加载失败原因（403 无权限 / 网络等），用于区分「失败」与「加载中」。 */
  error: ApiError | null;
}

/**
 * 拉取搜索应用数据集绑定选项。
 *
 * 成功：options 非 null（含空数组）；失败：options=null 且 error 携带原因。
 * 组件用 `options ?? fallback` 即可做到「成功走治理数据面、失败退回旧路径」。
 */
export function useSearchAppDatasetOptions(): SearchAppDatasetOptionsState {
  const [options, setOptions] = useState<SearchAppDatasetOption[] | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  useEffect(() => {
    let cancelled = false;
    setOptions(null);
    setError(null);
    api
      .get<{ code: number; data: SearchAppDatasetOption[] }>("/search-apps/dataset-options")
      .then((res) => {
        if (cancelled) return;
        const data = res.data?.data;
        setOptions(Array.isArray(data) ? data : []);
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setError(err instanceof ApiError ? err : new ApiError(0, 0, String(err)));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return { options, error };
}
