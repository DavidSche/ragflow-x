import { describe, expect, it } from "vitest";
import { tenantScopedDatasetOptions } from "./datasetScope";
import type { DatasetLike } from "@/resources/chats/chat-types";

const rows: DatasetLike[] = [
  { id: "ours-a", name: "报销制度", tenant_id: "tenant-a" },
  { id: "theirs", name: "采购合同", tenant_id: "tenant-b", ragflow_dataset_id: "e9e52e30092f4a74bb1713a610c5e4e2" },
  { id: "ours-b", name: "差旅规范", tenant_id: "tenant-a" },
];

// 4.151 drill 2026-11-17: the picker listed another workspace's dataset, the
// wizard submitted it, and instantiate answered 400/41120
// "dataset not found in workspace: e9e5…" at the last step.
describe("tenantScopedDatasetOptions", () => {
  it("keeps only the caller's workspace rows", () => {
    const { values, hidden } = tenantScopedDatasetOptions(rows, "tenant-a");
    expect(values.map((r) => r.id)).toEqual(["ours-a", "ours-b"]);
    expect(hidden).toBe(1);
  });

  it("hides every row of a foreign workspace, engine ids included", () => {
    const { values } = tenantScopedDatasetOptions(rows, "tenant-a");
    expect(values.some((r) => r.ragflow_dataset_id === "e9e52e30092f4a74bb1713a610c5e4e2")).toBe(false);
  });

  it("does not blank the picker before the identity resolves", () => {
    expect(tenantScopedDatasetOptions(rows, undefined).values).toHaveLength(3);
    expect(tenantScopedDatasetOptions(rows, "  ").values).toHaveLength(3);
    expect(tenantScopedDatasetOptions(rows, "").hidden).toBe(0);
  });

  it("keeps rows without a tenant marker rather than guessing them foreign", () => {
    const legacy: DatasetLike[] = [{ id: "legacy", name: "老数据集" }];
    const { values, hidden } = tenantScopedDatasetOptions(legacy, "tenant-a");
    expect(values.map((r) => r.id)).toEqual(["legacy"]);
    expect(hidden).toBe(0);
  });

  it("reports zero hidden when everything belongs to the caller", () => {
    expect(tenantScopedDatasetOptions(rows.slice(0, 1), "tenant-a").hidden).toBe(0);
  });
});
