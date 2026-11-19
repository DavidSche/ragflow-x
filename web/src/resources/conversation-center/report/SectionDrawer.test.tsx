/**
 * T10 SectionDrawer tests — status badges, rollup disclosure, and evidence
 * disclosure severity (supported vs conflicting/outdated/unsupported).
 */
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { SectionDrawer } from "./SectionDrawer";
import { tZh } from "../../../test/translate";
import type { ReportSectionSnapshot } from "./report-run";

// Labels resolve against the shipped dictionary (the drawer and the reader share
// one vocabulary, so the test must read the same words a user sees).
vi.mock("ra-core", async (importOriginal) => ({
  ...(await importOriginal<typeof import("ra-core")>()),
  useTranslate: () => tZh,
}));
vi.mock("@/components/ui/badge", () => ({
  Badge: ({ children }: { children?: ReactNode }) => <span>{children}</span>,
}));
vi.mock("@/components/ui/tooltip", () => ({
  Tooltip: ({ children }: { children?: ReactNode }) => <>{children}</>,
  TooltipContent: () => null,
  TooltipTrigger: ({ children }: { children?: ReactNode }) => <>{children}</>,
}));

const succeeded: ReportSectionSnapshot = {
  id: "sec-1",
  section_key: "exec_summary",
  section_version: 1,
  title: "执行摘要",
  ord: 0,
  status: "succeeded",
  attempt: 2,
  evidence_status: "supported",
  quality_status: "good",
  requires_evidence: true,
  citation_count: 5,
  claim_count: 3,
  tokens_in: 100,
  tokens_out: 80,
  started_at: "2026-11-17T10:00:00.000Z",
  finished_at: "2026-11-17T10:05:00.000Z",
};

const optional: ReportSectionSnapshot = {
  ...succeeded,
  id: "sec-2",
  section_key: "appendix",
  title: "附录",
  ord: 1,
  requires_evidence: false,
};

describe("SectionDrawer", () => {
  it("renders an empty state when there are no sections", () => {
    render(<SectionDrawer sections={[]} />);
    expect(screen.getByText("暂未捕获到章节。")).toBeInTheDocument();
  });

  it("renders section rows with their status badge", () => {
    render(<SectionDrawer sections={[succeeded, optional]} />);
    expect(screen.getByText("执行摘要")).toBeInTheDocument();
    expect(screen.getByText("附录")).toBeInTheDocument();
    // The badge speaks the reader's language: `succeeded` used to be printed raw.
    expect(screen.getAllByText("已生成").length).toBeGreaterThan(0);
  });

  it("expands a section and shows evidence, quality, citations and attempts", async () => {
    const user = userEvent.setup();
    render(<SectionDrawer sections={[succeeded]} />);
    let toggle = screen.getByTestId("section-toggle-exec_summary");
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    await user.click(toggle);
    toggle = screen.getByTestId("section-toggle-exec_summary");
    expect(toggle.getAttribute("aria-expanded")).toBe("true");

    expect(screen.getByText("证据")).toBeInTheDocument();
    expect(screen.getByText("质量")).toBeInTheDocument();
    expect(screen.getByText("5")).toBeInTheDocument(); // citations
    expect(screen.getByText("2")).toBeInTheDocument(); // attempt
    // The judgement is translated, not the raw enum: a reader sees「证据充分」.
    expect(screen.getAllByText("证据充分").length).toBeGreaterThan(0);
    // A section that cites five sources must not also claim it collected none:
    // the "no evidence" line used to key off a per-section evidence list the job
    // snapshot never carries, so a live section with 8 citations said
    // "本节未采集到证据。" (2026-11-18).
    expect(screen.queryByText("本节未采集到证据。")).toBeNull();
  });

  it("shows an evidence-free required section as unsupported, not as absent", async () => {
    const user = userEvent.setup();
    // A3b: the section exists and says what it lacks. Dropping the row would turn
    // an honest gap into a silent one.
    const unsupported: ReportSectionSnapshot = {
      ...succeeded,
      evidence_status: "unsupported",
      citation_count: 0,
    };
    render(<SectionDrawer sections={[unsupported]} />);
    expect(screen.getByText("执行摘要")).toBeInTheDocument();
    await user.click(screen.getByTestId("section-toggle-exec_summary"));
    expect(screen.getByText("证据不足")).toBeInTheDocument();
  });

  it("shows the not-required label for sections without evidence mandates", async () => {
    const user = userEvent.setup();
    render(<SectionDrawer sections={[optional]} />);
    await user.click(screen.getByTestId("section-toggle-appendix"));
    expect(screen.getByText("证据可选")).toBeInTheDocument();
  });

  it("shows how many statements a section made, without inventing a zero", async () => {
    const user = userEvent.setup();
    const { unmount } = render(<SectionDrawer sections={[succeeded]} />);
    await user.click(screen.getByTestId("section-toggle-exec_summary"));
    // R11's count is the section's own disclosure: three sentences were asserted.
    expect(screen.getByTestId("section-claims-exec_summary").textContent).toBe("3");
    expect(screen.getByText("结论句")).toBeInTheDocument();
    unmount();

    // A row written before the claim table existed carries `claim_count = 0`, which
    // means "not measured" — printing it would read as "this section asserts
    // nothing", the opposite of a disclosure.
    render(<SectionDrawer sections={[{ ...succeeded, claim_count: 0 }]} />);
    expect(screen.queryByTestId("section-claims-exec_summary")).toBeNull();
  });

  it("colours the evidence verdict by what it asks the reader to do", async () => {
    const user = userEvent.setup();
    // §9.3: conflicting and outdated mean "do not quote this as-is" and are the two
    // the drawer must make visible; partial/insufficient are amber, sufficient is
    // green, and an optional-evidence section carries no severity at all.
    const cases: Array<[string, string]> = [
      ["supported", "text-emerald-600 dark:text-emerald-400"],
      ["partially_supported", "text-amber-600 dark:text-amber-400"],
      ["unsupported", "text-amber-600 dark:text-amber-400"],
      ["conflicting", "text-destructive"],
      ["outdated", "text-destructive"],
    ];
    for (const [status, expected] of cases) {
      const { unmount } = render(
        <SectionDrawer sections={[{ ...succeeded, evidence_status: status }]} />,
      );
      await user.click(screen.getByTestId("section-toggle-exec_summary"));
      const cell = screen.getByTestId("section-evidence-exec_summary");
      expect(cell.className).toBe(expected);
      // The label is always words, never the wire value.
      expect(cell.textContent).not.toContain("_");
      unmount();
    }
  });

  it("shows R17 locators as navigation, never as evidence", async () => {
    const user = userEvent.setup();
    render(
      <SectionDrawer
        sections={[
          {
            ...succeeded,
            location_hints: ["页面索引：动火作业管理制度", "文档目录：作业许可"],
          },
        ]}
      />,
    );
    await user.click(screen.getByTestId("section-toggle-exec_summary"));

    // Same wording as the reader: one label for one idea.
    const hints = screen.getByTestId("report-section-hints-0");
    expect(hints.textContent).toContain("定位线索（非证据）：");
    expect(hints.textContent).toContain("页面索引：动火作业管理制度");
    // The section's own citation count is untouched by a locator: a compiled page
    // index is a way to find the page, not a source that supports a sentence.
    expect(screen.getByText("5")).toBeInTheDocument();
  });

  it("warns when a required section has collected no evidence", async () => {
    const user = userEvent.setup();
    const empty: ReportSectionSnapshot = {
      ...succeeded,
      evidence_status: "unsupported",
      citation_count: 0,
    };
    render(<SectionDrawer sections={[empty]} />);
    await user.click(screen.getByTestId("section-toggle-exec_summary"));
    expect(screen.getByText("本节未采集到证据。")).toBeInTheDocument();
    expect(screen.getByText("证据不足")).toBeInTheDocument();
  });
});
