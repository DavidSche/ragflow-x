import type { CapabilityRepairView, DatasetCapabilityView } from "./types";

/**
 * The repair click-to-request mapping, kept out of the component so it can be
 * tested without React (doc/285 §2 / §10).
 *
 * Two defects live behind this module, both of the same family:
 *
 *  1. the wizard used to send `{chunk_method}` only, so any dataset that already
 *     had parsed documents was answered `40915 (set rewrite_existing=true)` —
 *     the button could never succeed in the very case the check called
 *     repairable;
 *  2. the button posted the parse-profile channel *whatever* the repair was, so
 *     a `metadata_not_ready` dataset (whose repair is `enable_pushdown_and_
 *     sync_meta`) got its chunk method rewritten instead — a destructive write
 *     the operator never asked for.
 *
 * Every repair therefore declares its channel first: a repair the wizard cannot
 * drive gets no button at all, instead of a button that quietly does something
 * else.
 */

/** Which write channel can execute a repair — `manual`/`blocked` have none. */
export type RepairChannel = "parse_profile" | "pushdown" | "manual" | "blocked";

export function repairChannel(repair: { action?: string } | undefined): RepairChannel {
  switch (repair?.action) {
    case "apply_parse_profile":
      return "parse_profile";
    case "enable_pushdown_and_sync_meta":
      return "pushdown";
    case "remove_requirement":
      // Severity `blocked`: the engine cannot do this at all, so there is
      // nothing the wizard may write. Only a different template/dataset helps.
      return "blocked";
    default:
      // `reconfigure_dataset` and anything added later: the server names the
      // gap but not a value the wizard can write, so the operator finishes it in
      // the dataset settings.
      return "manual";
  }
}

/** True when this repair has a write channel the wizard can drive itself. */
export function repairExecutable(repair: { action?: string } | undefined): boolean {
  const channel = repairChannel(repair);
  return channel === "parse_profile" || channel === "pushdown";
}

/**
 * What to do with the capability conclusion right after a repair write.
 *
 * `parse_profile` rewrites `chunk_method` synchronously, so a re-check reports
 * the new method (at worst `pending_sync` while the re-parse runs).
 *
 * `pushdown` only *starts* an asynchronous engine-side `meta_fields` backfill,
 * so a re-check fired in the same second reads the old documents and answers
 * `repairable` with the very same repair — the wizard then looks like the fix
 * did nothing and the button stays lit (observed end-to-end on 2026-11-16: the
 * metadata landed 30–90 s later). The conclusion belongs to the moment it was
 * taken, so the wizard keeps it and asks for an explicit re-check instead of
 * contradicting its own success message.
 */
export type PostRepairStep = "recheck" | "wait_for_backfill";

export function postRepairStep(channel: RepairChannel): PostRepairStep {
  return channel === "pushdown" ? "wait_for_backfill" : "recheck";
}

export interface RepairConfirmation {
  /** Explicit acknowledgement of the shared-dataset impact (40917). */
  acknowledgeSharedImpact: boolean;
  /** Explicit acknowledgement that existing chunks will be replaced (40915). */
  reparseAcknowledged: boolean;
}

export interface RepairRequest {
  chunk_method: string;
  rewrite_existing?: boolean;
  acknowledge_shared_impact?: boolean;
}

export interface PushdownRequest {
  pushdown_enabled: boolean;
  acknowledge_shared_impact?: boolean;
}

/** Which acknowledgement (or missing target) still blocks the button. */
export type RepairBlocker = "shared" | "reparse" | "target";

/**
 * Target chunk method for `apply_parse_profile`. The server names it on the
 * repair; the probe evidence is the only other admissible source. When neither
 * says anything the answer is the empty string — the wizard must not invent a
 * method, and the caller disables the button instead.
 */
export function repairTargetMethod(
  repair: { chunk_method?: string } | undefined,
  row: DatasetCapabilityView,
): string {
  const named = repair?.chunk_method?.trim();
  if (named) return named;
  return suggestedChunkMethod(row);
}

/** Documents that would be re-parsed, i.e. the destructive part of the write. */
export function repairReparseCount(
  repair: { requires_reparse?: boolean } | undefined,
  row: DatasetCapabilityView,
): number {
  const parsed = row.parsed_count ?? 0;
  if (parsed > 0) return parsed;
  return repair?.requires_reparse ? 1 : 0;
}

/** Whether this repair replaces existing chunks (only the parse-profile one does). */
export function repairNeedsReparse(
  repair: { action?: string; requires_reparse?: boolean } | undefined,
  row: DatasetCapabilityView,
): boolean {
  return repairChannel(repair) === "parse_profile" && repairReparseCount(repair, row) > 0;
}

export function repairBlockers(
  row: DatasetCapabilityView,
  repair: { action?: string; chunk_method?: string; requires_reparse?: boolean } | undefined,
  confirmation: RepairConfirmation,
): RepairBlocker[] {
  const channel = repairChannel(repair);
  if (!repairExecutable(repair)) return [];
  const blockers: RepairBlocker[] = [];
  if ((row.shared_usage?.count ?? 0) > 0 && !confirmation.acknowledgeSharedImpact) {
    blockers.push("shared");
  }
  if (channel === "parse_profile") {
    if (repairTargetMethod(repair, row) === "") blockers.push("target");
    if (repairNeedsReparse(repair, row) && !confirmation.reparseAcknowledged) {
      blockers.push("reparse");
    }
  }
  return blockers;
}

/**
 * Builds the POST /datasets/:id/parse-profile body. `rewrite_existing` and
 * `acknowledge_shared_impact` are only sent when they are genuinely true — the
 * server treats their presence as the operator's explicit consent, so a
 * `false` default would be a lie dressed as a parameter.
 */
export function buildRepairRequest(
  row: DatasetCapabilityView,
  repair: { action?: string; chunk_method?: string; requires_reparse?: boolean } | undefined,
  confirmation: RepairConfirmation,
): RepairRequest {
  const body: RepairRequest = { chunk_method: repairTargetMethod(repair, row) };
  if (repairNeedsReparse(repair, row) && confirmation.reparseAcknowledged) {
    body.rewrite_existing = true;
  }
  if ((row.shared_usage?.count ?? 0) > 0 && confirmation.acknowledgeSharedImpact) {
    body.acknowledge_shared_impact = true;
  }
  return body;
}

/**
 * Builds the PUT /datasets/:id body for `enable_pushdown_and_sync_meta`
 * (`pushdown_enabled` is the local retrieval-pushdown policy, doc/276 §7.4).
 * Keeping this in its own builder is the point: the two repairs write different
 * resources and must never share a request shape.
 */
export function buildPushdownRequest(
  row: DatasetCapabilityView,
  confirmation: RepairConfirmation,
): PushdownRequest {
  const body: PushdownRequest = { pushdown_enabled: true };
  if ((row.shared_usage?.count ?? 0) > 0 && confirmation.acknowledgeSharedImpact) {
    body.acknowledge_shared_impact = true;
  }
  return body;
}

/** Repairs the wizard can actually execute, in a stable display order. */
export function executableRepairs(repairs: CapabilityRepairView[]): CapabilityRepairView[] {
  return repairs.filter(repairExecutable);
}

/**
 * Prefer the method the probe already reported for `chunk_method`; return the
 * empty string when the evidence carries none. `naive` used to be invented here,
 * which is how a `book` template ended up rewriting datasets to the wrong parser
 * (doc/285 §3).
 */
export function suggestedChunkMethod(row: DatasetCapabilityView): string {
  const evidence = row.capabilities.find((item) => item.capability === "chunk_method");
  const reported = (evidence?.note ?? "").match(/chunk_method=([a-z_]+)/)?.[1];
  if (reported) return reported;
  const achieved = row.requirements.find((item) => item.capability === "chunk_method")?.achieved;
  if (achieved && achieved !== "none" && achieved !== "unknown") return achieved;
  return "";
}
