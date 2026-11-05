import { useEffect, useState } from "react";
import { api, ApiError } from "../../lib/api";

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
