import type { DatasetLike } from "@/resources/chats/chat-types";

/**
 * Restrict the wizard's dataset picker to the caller's own workspace.
 *
 * The 4.151 drill (2026-11-17) failed every instantiation with
 * `400/41120 dataset not found in workspace: e9e5…`: `GET /datasets` is
 * scope-aware and an operator who may *read* other workspaces sees their
 * datasets in the list, but `CreateChat`/instantiate resolve every
 * `dataset_ids[]` entry with `GetDatasetLink(callerTenant, id)`. Cross-workspace
 * rows are therefore un-selectable by construction, and offering them only
 * produces a dead click at the last step.
 *
 * Ownership is not silently widened on the server: a chat in workspace A
 * retrieving a dataset of workspace B would escape the tenant boundary the
 * capability check just evaluated. Hide the row and say how many were hidden.
 */
export function tenantScopedDatasetOptions(
  rows: DatasetLike[],
  tenantId: string | undefined,
): { values: DatasetLike[]; hidden: number } {
  const scoped = (tenantId ?? "").trim();
  // Before /auth/me resolves we do not know the tenant; keep every row rather
  // than blank the picker (the list re-renders on identity arrival).
  if (!scoped) return { values: rows, hidden: 0 };
  const values = rows.filter((row) => !row.tenant_id || row.tenant_id === scoped);
  return { values, hidden: rows.length - values.length };
}
