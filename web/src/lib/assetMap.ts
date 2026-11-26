/**
 * The asset-governance board renders one card per dataset and reads list fields
 * directly (`assistant_ids.length`, `chunk_evidence.slice(0, 5)`). A Go nil
 * slice marshals to JSON `null`, which blanked the whole page with
 * "TypeError: Cannot read properties of null (reading 'length')" (E159).
 *
 * The server now sends `[]` for those fields, but the console may also talk to
 * an older server, and a single null must not take down a page that is otherwise
 * useful. Normalizing once at the fetch boundary keeps the render code honest
 * without sprinkling `?.` over every read.
 */
export const ASSET_MAP_LIST_FIELDS = [
  "assistant_ids",
  "assistant_release_ids",
  "trace_runs",
  "chunk_evidence",
  "knowledge_tasks",
] as const;

/** Fields the asset-map search box lowercases on every keystroke. */
export const ASSET_MAP_TEXT_FIELDS = ["name", "business_domain"] as const;

/** Anything that is not a JSON array is treated as "no entries". */
export function normalizeAssetMapItem<T extends object>(item: T): T {
  const normalized: Record<string, unknown> = { ...(item as Record<string, unknown>) };
  for (const field of ASSET_MAP_LIST_FIELDS) {
    if (!Array.isArray(normalized[field])) {
      normalized[field] = [];
    }
  }
  // The search box calls `.toLowerCase()` on both: a null would crash the same
  // way the list fields did.
  for (const field of ASSET_MAP_TEXT_FIELDS) {
    if (typeof normalized[field] !== "string") {
      normalized[field] = "";
    }
  }
  return normalized as T;
}

export function normalizeAssetMapItems<T extends object>(items: T[] | null | undefined): T[] {
  return (items ?? []).filter((item): item is T => item !== null && typeof item === "object").map(normalizeAssetMapItem);
}
