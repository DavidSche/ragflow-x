import { describe, expect, it } from "vitest";
import { createBlockers } from "./gates";
import type { CapabilityCheckView, DatasetCapabilityView } from "./types";

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

function check(datasets: DatasetCapabilityView[]): CapabilityCheckView {
  return {
    template_id: "t-1",
    template_version: 1,
    app_type: "chat",
    verdict: "repairable",
    probe_status: "success",
    capability_set_version: "1",
    datasets,
  };
}

describe("createBlockers", () => {
  it("does not block before a check has run", () => {
    expect(createBlockers(null)).toEqual([]);
  });

  it("does not block a repairable verdict", () => {
    expect(createBlockers(check([row({ verdict: "repairable" })]))).toEqual([]);
  });

  it("does not block a probe-limited verdict — unverified is not unsupported", () => {
    expect(createBlockers(check([row({ verdict: "probe_limited", probe_status: "access_denied" })]))).toEqual([]);
  });

  it("blocks on a hard capability gap and names the dataset and capability", () => {
    const blocked = row({
      dataset_id: "ds-9",
      dataset_name: "合同库",
      verdict: "blocked",
      requirements: [
        { capability: "vector", scope: "dataset", min_level: "operational", achieved: "operational", satisfied: true },
        {
          capability: "graph",
          scope: "dataset",
          min_level: "configured",
          achieved: "none",
          satisfied: false,
          reason: "capability_not_supported",
        },
      ],
    });
    expect(createBlockers(check([row(), blocked]))).toEqual([
      { kind: "capability_blocked", datasetId: "ds-9", datasetName: "合同库", capability: "graph", reason: "capability_not_supported" },
    ]);
  });

  it("falls back to the first unsatisfied requirement when no reason is given", () => {
    const blocked = row({
      verdict: "blocked",
      requirements: [
        { capability: "citation_locator", scope: "dataset", min_level: "configured", achieved: "unknown", satisfied: false },
      ],
    });
    expect(createBlockers(check([blocked]))[0].capability).toBe("citation_locator");
  });
});
