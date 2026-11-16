import { describe, expect, it } from "vitest";
import { projectChoices } from "./projectOptions";

describe("projectChoices", () => {
  it("renders the project name and keeps the id for the API", () => {
    expect(projectChoices([{ id: "p1", name: "安全生产知识库项目" }])).toEqual([
      { id: "p1", name: "安全生产知识库项目" },
    ]);
  });

  it("trims padded names", () => {
    expect(projectChoices([{ id: "p1", name: "  项目 A  " }])[0].name).toBe("项目 A");
  });

  it("falls back to the id when the project has no name", () => {
    expect(projectChoices([{ id: "p2" }, { id: "p3", name: "   " }])).toEqual([
      { id: "p2", name: "p2" },
      { id: "p3", name: "p3" },
    ]);
  });

  it("tolerates a missing list", () => {
    expect(projectChoices(undefined)).toEqual([]);
    expect(projectChoices(null)).toEqual([]);
  });
});
