/**
 * R17 / R18 disclosures (doc/295 §2.1, §2.2, A54–A57).
 *
 * Two failure modes are being pinned here, both found the hard way in earlier
 * slices of this feature:
 *
 *  - **A locator that reads like a citation.** The block sits right above the
 *    citation count, so if the label ever degrades into a bare list of page titles a
 *    reader will count it as evidence. The label is asserted verbatim.
 *  - **A degradation that reads like a failure.** Three of R18's planning states say
 *    "the planner did not run". The section still succeeded from its own retrieval,
 *    so the disclosure must not use the destructive/error surface.
 */
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ReportSectionDisclosure } from "./ReportSectionDisclosure";
import { tZh } from "../../test/translate";

vi.mock("ra-core", () => ({
  useTranslate: () => tZh,
}));

describe("ReportSectionDisclosure", () => {
  it("labels locators as navigation and never as evidence", () => {
    render(
      <ReportSectionDisclosure
        section={{
          ord: 2,
          location_hints: ["页面索引：差旅报销制度", "知识页面：差旅标准"],
        }}
      />,
    );

    // The exact wording, with the parenthesis: 「定位线索（非证据）」.
    expect(screen.getByText("定位线索（非证据）：")).toBeTruthy();
    expect(screen.getByText("页面索引：差旅报销制度")).toBeTruthy();
    expect(screen.getByText("知识页面：差旅标准")).toBeTruthy();
    // No research disclosure was asked for, so none appears.
    expect(screen.queryByTestId("report-section-research-2")).toBeNull();
  });

  it("renders nothing at all for a section with neither disclosure", () => {
    const { container } = render(<ReportSectionDisclosure section={{ ord: 1 }} />);
    expect(container.textContent).toBe("");
  });

  it("shows deep research with its merge counts", () => {
    render(
      <ReportSectionDisclosure
        section={{
          ord: 3,
          research_plan: { depth: "deep", planning: "planned", merged: 5, duplicates: 2 },
        }}
      />,
    );

    expect(screen.getByText("深研披露：")).toBeTruthy();
    expect(screen.getByText("已按计划扩展检索")).toBeTruthy();
    expect(screen.getByText("（新增 5 条片段，重复 2 条）")).toBeTruthy();
    // No locators in this section.
    expect(screen.queryByTestId("report-section-hints-3")).toBeNull();
  });

  it.each([
    ["fallback_planner_error", "深研未启用：查证规划调用失败"],
    ["fallback_budget", "深研未启用：预算不足"],
    ["fallback_unparseable", "深研未启用：规划结果无法解析"],
  ])("states %s as a skipped upgrade, not a failure", (planning, expected) => {
    render(
      <ReportSectionDisclosure
        section={{ ord: 4, research_plan: { depth: "deep", planning, merged: 0, duplicates: 0 } }}
      />,
    );

    const note = screen.getByTestId("report-section-research-4");
    expect(note.textContent).toContain(expected);
    // A degradation is not an error: no alert role, no raw enum on screen.
    expect(screen.queryByRole("alert")).toBeNull();
    expect(note.textContent).not.toContain(planning);
  });

  it("stays quiet for a standard-depth section that planned nothing special", () => {
    const { container } = render(
      <ReportSectionDisclosure
        section={{ ord: 5, research_plan: { depth: "standard", planning: "standard", merged: 0, duplicates: 0 } }}
      />,
    );
    expect(container.textContent).toBe("");
  });
});
