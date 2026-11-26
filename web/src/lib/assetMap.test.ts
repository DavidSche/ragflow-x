import { describe, expect, it } from "vitest";
import { ASSET_MAP_LIST_FIELDS, normalizeAssetMapItem, normalizeAssetMapItems } from "./assetMap";

describe("normalizeAssetMapItem", () => {
  it("turns null list fields into empty arrays instead of leaving null", () => {
    const row: Record<string, unknown> = { id: "ds-1", name: "manual", assistant_ids: null, assistant_release_ids: null, trace_runs: null, chunk_evidence: null, knowledge_tasks: null };
    const normalized = normalizeAssetMapItem(row);
    for (const field of ASSET_MAP_LIST_FIELDS) {
      expect(Array.isArray(normalized[field])).toBe(true);
      expect(normalized[field]).toHaveLength(0);
    }
    // The crash was `null.length`, so prove the reported access now works.
    expect(() => (normalized.assistant_ids as unknown[]).length).not.toThrow();
    expect(normalized.id).toBe("ds-1");
  });

  it("fills in fields the server omitted and keeps the ones it sent", () => {
    const traces = [{ trace_id: "t-1" }];
    const row: Record<string, unknown> = { id: "ds-2", trace_runs: traces, chunk_evidence: [] };
    const normalized = normalizeAssetMapItem(row);
    expect(normalized.trace_runs).toBe(traces);
    expect(normalized.assistant_ids).toEqual([]);
    expect(normalized.knowledge_tasks).toEqual([]);
  });

  it("leaves non-list fields untouched", () => {
    const row = { id: "ds-3", business_domain: "维修", quality_score: 87, assistant_ids: [] };
    const normalized = normalizeAssetMapItem(row);
    expect(normalized.business_domain).toBe("维修");
    expect(normalized.quality_score).toBe(87);
  });
});

describe("normalizeAssetMapItems", () => {
  it("accepts a missing payload as an empty board", () => {
    expect(normalizeAssetMapItems(null)).toEqual([]);
    expect(normalizeAssetMapItems(undefined)).toEqual([]);
  });

  it("normalizes every row of a page", () => {
    const rows = normalizeAssetMapItems([{ id: "a", assistant_ids: null }, { id: "b", assistant_ids: ["asst-1"] }] as Array<Record<string, unknown>>);
    expect(rows.map((row) => row.assistant_ids)).toEqual([[], ["asst-1"]]);
  });
});

describe("text fields used by the search box", () => {
  it("coerces a null name and business domain so .toLowerCase() cannot throw", () => {
    const row: Record<string, unknown> = { id: "ds-4", name: null, business_domain: null };
    const normalized = normalizeAssetMapItem(row);
    expect(normalized.name).toBe("");
    expect(normalized.business_domain).toBe("");
    expect(() => (normalized.name as string).toLowerCase()).not.toThrow();
  });
});
