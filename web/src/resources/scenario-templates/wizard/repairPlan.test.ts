import { describe, expect, it } from "vitest";
import {
  buildPushdownRequest,
  buildRepairRequest,
  executableRepairs,
  repairBlockers,
  postRepairStep,
  repairChannel,
  repairReparseCount,
  repairTargetMethod,
  suggestedChunkMethod,
} from "./repairPlan";
import type { CapabilityRepairView, DatasetCapabilityView } from "./types";

function row(overrides: Partial<DatasetCapabilityView> = {}): DatasetCapabilityView {
  return {
    role: "primary",
    dataset_id: "ds-1",
    verdict: "repairable",
    probe_status: "success",
    requirements: [],
    capabilities: [],
    repairs: [],
    shared_usage: { count: 0 },
    ...overrides,
  };
}

const parseRepair: CapabilityRepairView = {
  action: "apply_parse_profile",
  reason: "",
  severity: "repairable",
  chunk_method: "book",
};
/** The server named no target: the button must lock instead of guessing. */
const parseRepairNoTarget: CapabilityRepairView = {
  action: "apply_parse_profile",
  reason: "",
  severity: "repairable",
};

describe("repairTargetMethod", () => {
  it("takes the method the server named on the repair", () => {
    expect(repairTargetMethod({ ...parseRepair, chunk_method: "book" }, row())).toBe("book");
  });

  it("falls back to the probe evidence when the server names nothing", () => {
    const withEvidence = row({
      capabilities: [{ capability: "chunk_method", status: "ready", source: "dataset.config", note: "chunk_method=paper" }],
    });
    expect(repairTargetMethod(parseRepairNoTarget, withEvidence)).toBe("paper");
  });
});

describe("repairReparseCount", () => {
  it("reports the parsed documents the write would replace", () => {
    expect(repairReparseCount({ requires_reparse: true }, row({ parsed_count: 3, document_count: 5 }))).toBe(3);
  });

  it("is zero for a dataset with nothing parsed", () => {
    expect(repairReparseCount(parseRepair, row({ document_count: 0 }))).toBe(0);
  });

  it("trusts the server flag when counts are absent (older response)", () => {
    expect(repairReparseCount({ requires_reparse: true }, row())).toBe(1);
  });
});

describe("repairBlockers", () => {
  it("requires the re-parse acknowledgement on a parsed dataset", () => {
    const parsed = row({ parsed_count: 2 });
    expect(repairBlockers(parsed, parseRepair, { acknowledgeSharedImpact: false, reparseAcknowledged: false })).toEqual([
      "reparse",
    ]);
  });

  it("requires the shared acknowledgement when applications use the dataset", () => {
    const shared = row({ shared_usage: { count: 2, chat_ids: ["c1"] } });
    expect(repairBlockers(shared, parseRepair, { acknowledgeSharedImpact: false, reparseAcknowledged: false })).toEqual([
      "shared",
    ]);
  });

  it("unlocks only when both acknowledgements are given", () => {
    const both = row({ parsed_count: 1, shared_usage: { count: 1 } });
    expect(repairBlockers(both, parseRepair, { acknowledgeSharedImpact: true, reparseAcknowledged: true })).toEqual([]);
  });

  it("does not ask for a re-parse on an empty dataset", () => {
    expect(repairBlockers(row(), parseRepair, { acknowledgeSharedImpact: true, reparseAcknowledged: false })).toEqual(
      [],
    );
  });
});

describe("buildRepairRequest", () => {
  it("sends rewrite_existing once the operator confirms the re-parse", () => {
    const parsed = row({ parsed_count: 3 });
    expect(
      buildRepairRequest(parsed, { ...parseRepair, chunk_method: "book" }, {
        acknowledgeSharedImpact: false,
        reparseAcknowledged: true,
      }),
    ).toEqual({ chunk_method: "book", rewrite_existing: true });
  });

  it("omits rewrite_existing on an empty dataset", () => {
    expect(
      buildRepairRequest(row(), { ...parseRepair, chunk_method: "book" }, {
        acknowledgeSharedImpact: false,
        reparseAcknowledged: false,
      }),
    ).toEqual({ chunk_method: "book" });
  });

  it("sends acknowledge_shared_impact only when the operator checked it", () => {
    const shared = row({ shared_usage: { count: 1 } });
    expect(
      buildRepairRequest(shared, { ...parseRepair, chunk_method: "book" }, {
        acknowledgeSharedImpact: true,
        reparseAcknowledged: false,
      }),
    ).toEqual({ chunk_method: "book", acknowledge_shared_impact: true });
  });
});

describe("suggestedChunkMethod", () => {
  it("reads the method the probe reported, and never invents one", () => {
    // The old fallback returned `naive`, which is not what a book template
    // wants: it rewrote datasets to the wrong parser (doc/285 §3).
    const withEvidence = row({
      capabilities: [{ capability: "chunk_method", status: "ready", source: "dataset.config", note: "chunk_method=book" }],
    });
    expect(suggestedChunkMethod(withEvidence)).toBe("book");
    expect(suggestedChunkMethod(row())).toBe("");
  });
});

describe("repairChannel", () => {
  it("routes each known action to the write channel that can execute it", () => {
    expect(repairChannel({ action: "apply_parse_profile" })).toBe("parse_profile");
    expect(repairChannel({ action: "enable_pushdown_and_sync_meta" })).toBe("pushdown");
    expect(repairChannel({ action: "reconfigure_dataset" })).toBe("manual");
    expect(repairChannel({ action: "remove_requirement" })).toBe("blocked");
  });

  it("treats an unknown action as manual, never as a write it cannot perform", () => {
    expect(repairChannel({ action: "some_future_action" })).toBe("manual");
    expect(repairChannel(undefined)).toBe("manual");
  });

  it("keeps a metadata-only dataset away from the parse-profile writer", () => {
    // Regression for the `repairs[0]` fallback: the only repair here is the
    // pushdown one, and the old code posted a chunk-method rewrite for it.
    const metadataOnly = row({
      repairs: [
        { action: "enable_pushdown_and_sync_meta", reason: "no document carries meta_fields", severity: "repairable" },
      ],
    });
    const repairs = executableRepairs(metadataOnly.repairs);
    expect(repairs.map((item) => repairChannel(item))).toEqual(["pushdown"]);
    expect(repairs.some((item) => repairChannel(item) === "parse_profile")).toBe(false);
  });

  it("gives a blocked requirement no executable repair", () => {
    const blocked = row({
      verdict: "blocked",
      repairs: [{ action: "remove_requirement", reason: "capability_not_supported", severity: "blocked" }],
    });
    expect(executableRepairs(blocked.repairs)).toEqual([]);
    expect(repairBlockers(blocked, blocked.repairs[0], { acknowledgeSharedImpact: false, reparseAcknowledged: false })).toEqual([]);
  });
});

describe("pushdown repair", () => {
  const pushdown = { action: "enable_pushdown_and_sync_meta", reason: "", severity: "repairable" };

  it("builds the dataset-update body, not a parse-profile body", () => {
    expect(buildPushdownRequest(row(), { acknowledgeSharedImpact: false, reparseAcknowledged: false })).toEqual({
      pushdown_enabled: true,
    });
  });

  it("carries the shared acknowledgement only when the operator gave it", () => {
    const shared = row({ shared_usage: { count: 1 } });
    expect(buildPushdownRequest(shared, { acknowledgeSharedImpact: true, reparseAcknowledged: false })).toEqual({
      pushdown_enabled: true,
      acknowledge_shared_impact: true,
    });
  });

  it("asks for the shared acknowledgement but never for a re-parse", () => {
    const shared = row({ parsed_count: 2, shared_usage: { count: 1 } });
    expect(repairBlockers(shared, pushdown, { acknowledgeSharedImpact: false, reparseAcknowledged: false })).toEqual([
      "shared",
    ]);
    expect(repairBlockers(shared, pushdown, { acknowledgeSharedImpact: true, reparseAcknowledged: false })).toEqual([]);
  });
});

describe("repairTargetMethod without a server-named target", () => {
  it("returns the empty string instead of inventing naive", () => {
    expect(repairTargetMethod(parseRepairNoTarget, row())).toBe("");
    expect(suggestedChunkMethod(row())).toBe("");
  });

  it("blocks the parse-profile button when no target is known", () => {
    expect(repairBlockers(row(), parseRepairNoTarget, { acknowledgeSharedImpact: true, reparseAcknowledged: true })).toEqual([
      "target",
    ]);
  });
});

describe("postRepairStep", () => {
  it("only asks for an immediate re-check when the write lands synchronously", () => {
    expect(postRepairStep("parse_profile")).toBe("recheck");
  });

  it("waits for the asynchronous metadata backfill after a pushdown repair", () => {
    // A re-check fired right after `PUT /datasets/:id {pushdown_enabled:true}`
    // reads the pre-backfill documents, answers `repairable` again with the same
    // repair, and makes the fix look broken (E2E 2026-11-16).
    expect(postRepairStep("pushdown")).toBe("wait_for_backfill");
  });
});
