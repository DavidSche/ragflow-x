import type { CapabilityCheckView } from "./types";

/**
 * Gates the wizard enforces before it creates anything (doc/276 §6.5 / §12).
 *
 * doc/276's verdict table says `blocked` **blocks instantiation** — the engine
 * cannot provide a capability the template requires, and no configuration change
 * reaches it. The server does not refuse it today, so the wizard refuses on the
 * server's behalf and says why; `instantiate` itself remains permissive (an open
 * item recorded in doc/285 §10, not something this module pretends to own).
 */

export interface CreateBlocker {
  kind: "capability_blocked";
  datasetId: string;
  datasetName: string;
  /** The first requirement the server called blocked, for the message. */
  capability: string;
  reason?: string;
}

/**
 * Blockers that must stop creation. Asks the *check response*, never a locally
 * recomputed verdict: two sides computing the same thing is how the
 * `requires_reparse` and `metadata_filter` mismatches happened (doc/285 §4).
 */
export function createBlockers(check: CapabilityCheckView | null): CreateBlocker[] {
  if (!check) return [];
  const blockers: CreateBlocker[] = [];
  for (const row of check.datasets) {
    if (row.verdict !== "blocked") continue;
    const blockedRequirement =
      row.requirements.find((item) => item.reason === "capability_not_supported") ??
      row.requirements.find((item) => !item.satisfied);
    blockers.push({
      kind: "capability_blocked",
      datasetId: row.dataset_id,
      datasetName: row.dataset_name ?? row.dataset_id,
      capability: blockedRequirement?.capability ?? row.role,
      reason: blockedRequirement?.reason,
    });
  }
  return blockers;
}
