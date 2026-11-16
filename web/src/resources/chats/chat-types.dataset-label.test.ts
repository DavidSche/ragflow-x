import { describe, expect, it } from "vitest";
import { datasetIdLabel } from "./chat-types";

const datasets = [
  { id: "479882ccd2924f8da7ac917aded7b29e", name: "安全生产法规与制度", ragflow_dataset_id: "65057e70b59f47f995b634bd8b5617ab" },
  { id: "ds-2", name: "报销制度" },
];

describe("datasetIdLabel", () => {
  it("resolves an engine id to the workspace dataset name", () => {
    expect(datasetIdLabel("65057e70b59f47f995b634bd8b5617ab", datasets)).toBe("安全生产法规与制度");
  });

  it("resolves workspace ids and comma-separated lists", () => {
    expect(datasetIdLabel("65057e70b59f47f995b634bd8b5617ab,ds-2", datasets)).toBe(
      "安全生产法规与制度, 报销制度",
    );
  });

  it("keeps unknown ids visible instead of dropping them", () => {
    expect(datasetIdLabel("engine-x", datasets)).toBe("engine-x");
  });

  it("falls back when nothing is bound", () => {
    expect(datasetIdLabel("", datasets)).toBe("-");
    expect(datasetIdLabel(undefined, datasets)).toBe("-");
    expect(datasetIdLabel("   ", undefined)).toBe("-");
  });
});
