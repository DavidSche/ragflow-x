// P1 向导数据面接线单测：useSearchAppDatasetOptions 的三态语义
// （成功/空数组/失败），以及 DatasetLike 兼容形状（绑定用引擎 id）。
import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../../lib/api", () => ({
  api: { get: getMock },
  ApiError: class ApiError extends Error {
    status = 403;
    code = 403;
  },
}));

const { useSearchAppDatasetOptions, workspaceDatasetIdsForDocuments } = await import(
  "./dataset-options"
);

const linked = {
  link_id: "link-a1",
  name: "安全生产台账",
  ragflow_dataset_id: "rf-a1",
  document_count: 12,
  sensitivity: "sensitive",
  source: "linked" as const,
};
const engine = {
  name: "新入库未链接库",
  ragflow_dataset_id: "rf-x",
  document_count: 0,
  source: "engine" as const,
};

describe("useSearchAppDatasetOptions", () => {
  beforeEach(() => {
    getMock.mockReset();
  });

  it("加载成功后返回 dataset-options（含 linked 优先行与 engine 兜底行）", async () => {
    getMock.mockResolvedValue({
      data: { code: 0, data: [linked, engine] },
    });

    const { result } = renderHook(() => useSearchAppDatasetOptions(), {
      wrapper: ({ children }: { children: ReactNode }) => children,
    });

    await waitFor(() => {
      expect(result.current.options).toEqual([linked, engine]);
    });
    expect(result.current.error).toBeNull();
    // DatasetLike 兼容：id/ragflow_dataset_id 均为引擎 kb id（绑定提交用）。
    expect(result.current.options?.[0].ragflow_dataset_id).toBe("rf-a1");
    expect(getMock).toHaveBeenCalledWith("/search-apps/dataset-options");
  });

  it("scope 内无可绑数据集时返回空数组（不走回退语义）", async () => {
    getMock.mockResolvedValue({ data: { code: 0, data: [] } });

    const { result } = renderHook(() => useSearchAppDatasetOptions(), {
      wrapper: ({ children }: { children: ReactNode }) => children,
    });

    await waitFor(() => {
      expect(result.current.options).toEqual([]);
    });
    expect(result.current.error).toBeNull();
  });

  it("加载失败返回 options=null 并携带原因（调用方可回退旧路径）", async () => {
    getMock.mockRejectedValue(new Error("403"));

    const { result } = renderHook(() => useSearchAppDatasetOptions(), {
      wrapper: ({ children }: { children: ReactNode }) => children,
    });

    await waitFor(() => {
      expect(result.current.options).toBeNull();
      expect(result.current.error).toBeInstanceOf(Error);
    });
  });
});

// 引擎 id → 工作区 link id 的翻译：`GET /datasets/:id/documents` 只认 link id，
// 直接拿 RAGFlow 配置里的 kb_ids 去问会 404 dataset not found
// （doc/304 §4 E169：4.151 演练里每次打开搜索应用详情刷三条 404）。
describe("workspaceDatasetIdsForDocuments", () => {
  const rows = [
    { id: "link-a1", name: "安全生产台账", ragflow_dataset_id: "rf-a1" },
    { id: "link-b2", name: "客服 FAQ", ragflow_dataset_id: "rf-b2" },
  ];

  it("引擎 id 翻译成 link id", () => {
    expect(workspaceDatasetIdsForDocuments(["rf-a1", "rf-b2"], rows)).toEqual([
      "link-a1",
      "link-b2",
    ]);
  });

  it("已经是 link id 的输入原样保留（幂等）", () => {
    expect(workspaceDatasetIdsForDocuments(["link-a1"], rows)).toEqual(["link-a1"]);
  });

  it("没有 link 的引擎 id 被丢弃：发出去只会是 404", () => {
    expect(workspaceDatasetIdsForDocuments(["rf-missing", "rf-a1"], rows)).toEqual([
      "link-a1",
    ]);
  });

  it("数据集列表还没加载时返回空数组，不发请求", () => {
    expect(workspaceDatasetIdsForDocuments(["rf-a1"], undefined)).toEqual([]);
  });

  it("顺序保持并去重（同一 link 被两个 id 指到）", () => {
    expect(
      workspaceDatasetIdsForDocuments(["rf-b2", "link-b2", "rf-a1"], rows),
    ).toEqual(["link-b2", "link-a1"]);
  });
});
