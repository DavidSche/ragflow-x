/**
 * T10 SectionDrawer tests — status badges, rollup disclosure, and evidence
 * disclosure severity (supported vs conflicting/outdated/unsupported).
 */
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { SectionDrawer, type SectionEvidenceEntry } from "./SectionDrawer";
import type { ReportSectionSnapshot } from "./report-run";

vi.mock("ra-core", async (importOriginal) => ({
  ...(await importOriginal<typeof import("ra-core")>()),
  useTranslate: () =>
    (key: string, options?: { _?: string }) =>
      options && typeof options._ === "string" ? options._ : key,
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
    expect(screen.getByText("conversationCenter.report_sections_empty")).toBeInTheDocument();
  });

  it("renders section rows with their status badge", () => {
    render(<SectionDrawer sections={[succeeded, optional]} />);
    expect(screen.getByText("执行摘要")).toBeInTheDocument();
    expect(screen.getByText("附录")).toBeInTheDocument();
    expect(screen.getAllByText(/succeeded/i).length).toBeGreaterThan(0);
  });

  it("expands a section and shows evidence, quality, citations and attempts", async () => {
    const user = userEvent.setup();
    const junction: SectionEvidenceEntry[] = [
      { section_key: "exec_summary", status: "conflicting", reason: "两处数据口径不一致" },
      { section_key: "exec_summary", status: "supported", claims: ["库存周转率下降"] },
    ];
    render(
      <SectionDrawer
        sections={[succeeded]}
        evidenceBySection={{ exec_summary: junction }}
      />,
    );
    let toggle = screen.getByTestId("section-toggle-exec_summary");
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    await user.click(toggle);
    toggle = screen.getByTestId("section-toggle-exec_summary");
    expect(toggle.getAttribute("aria-expanded")).toBe("true");

    expect(screen.getByText("conversationCenter.report_section_evidence")).toBeInTheDocument();
    expect(screen.getByText("conversationCenter.report_section_quality")).toBeInTheDocument();
    expect(screen.getByText("5")).toBeInTheDocument(); // citations
    expect(screen.getByText("2")).toBeInTheDocument(); // attempt
    // Both disclosures appear, deduplicated across the section and the badge.
    expect(screen.getByText("conflicting")).toBeInTheDocument();
    expect(screen.getAllByText("supported").length).toBeGreaterThan(0);
    expect(screen.getByText("两处数据口径不一致")).toBeInTheDocument();
  });

  it("shows the not-required label for sections without evidence mandates", async () => {
    const user = userEvent.setup();
    render(<SectionDrawer sections={[optional]} />);
    await user.click(screen.getByTestId("section-toggle-appendix"));
    expect(screen.getByText("conversationCenter.report_section_not_required")).toBeInTheDocument();
  });

  it("warns when a required section has collected no evidence", async () => {
    const user = userEvent.setup();
    render(<SectionDrawer sections={[succeeded]} />);
    await user.click(screen.getByTestId("section-toggle-exec_summary"));
    expect(screen.getByText("conversationCenter.report_section_no_evidence")).toBeInTheDocument();
  });
});
