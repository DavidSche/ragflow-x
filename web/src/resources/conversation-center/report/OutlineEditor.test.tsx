/**
 * T9 OutlineEditor tests — template section rendering, required-section lock,
 * edited tracking, and the confirmation callback contract (trimmed titles).
 */
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { OutlineEditor, type OutlineEditorProps } from "./OutlineEditor";
import type { ReportTemplateSection } from "./report-run";

vi.mock("ra-core", async (importOriginal) => ({
  ...(await importOriginal<typeof import("ra-core")>()),
  useTranslate: () => (key: string) => key,
}));

vi.mock("@/components/ui/badge", () => ({
  Badge: ({ children }: { children?: ReactNode }) => <span>{children}</span>,
}));
vi.mock("@/components/ui/button", () => ({
  Button: (props: Record<string, unknown>) => <button {...props} />,
}));
vi.mock("@/components/ui/input", () => ({
  Input: ({
    value,
    onChange,
    disabled,
    "aria-label": label,
  }: {
    value?: string;
    onChange?: (event: { target: { value: string } }) => void;
    disabled?: boolean;
    "aria-label"?: string;
  }) => (
    <input
      aria-label={label}
      value={value}
      disabled={disabled}
      onChange={onChange}
    />
  ),
}));

const sections: ReportTemplateSection[] = [
  {
    key: "exec_summary",
    title: "执行摘要",
    prompt: "总结",
    requires_evidence: true,
    max_words: 500,
  },
  {
    key: "appendix",
    title: "附录",
    prompt: "数据表",
    requires_evidence: false,
    max_words: 300,
  },
];

function setup(overrides: Partial<OutlineEditorProps> = {}) {
  const onConfirm = vi.fn(async () => undefined);
  const props: OutlineEditorProps = {
    reportId: "rep1",
    revision: 1,
    mode: "required",
    sections,
    onConfirm,
    ...overrides,
  };
  render(<OutlineEditor {...props} />);
  return { onConfirm };
}

describe("OutlineEditor", () => {
  it("renders every template section as an editable draft", () => {
    setup();
    expect(screen.getByDisplayValue("执行摘要")).toBeInTheDocument();
    expect(screen.getByDisplayValue("附录")).toBeInTheDocument();
    expect(
      screen.getByText("conversationCenter.report_outline_section_count"),
    ).toBeInTheDocument();
  });

  it("locks required sections by default (P0-A: every section is required)", () => {
    setup();
    const removeButtons = screen
      .getAllByRole("button")
      .filter((button) => button.getAttribute("aria-label") !== null);
    expect(removeButtons.length).toBeGreaterThan(0);
    for (const button of removeButtons) {
      expect(button).toBeDisabled();
    }
    expect(screen.getByDisplayValue("执行摘要")).toBeEnabled();
  });

  it("allows removing a section when requiredKeys is empty", async () => {
    const user = userEvent.setup();
    setup({ requiredKeys: [] });
    const removeButton = screen
      .getAllByRole("button")
      .find((button) => button.getAttribute("aria-label") === "conversationCenter.report_outline_remove");
    expect(removeButton).toBeTruthy();
    await user.click(removeButton!);
    expect(screen.queryByDisplayValue("执行摘要")).not.toBeInTheDocument();
    expect(screen.getByDisplayValue("附录")).toBeInTheDocument();
  });

  it("tracks edited titles and confirms with trimmed values", async () => {
    const user = userEvent.setup();
    const { onConfirm } = setup();
    const titleInput = screen.getByDisplayValue("执行摘要");
    await user.clear(titleInput);
    await user.type(titleInput, "  三季度质量回顾  ");
    // Edited badge reflects a changed title.
    expect(screen.getByText(/edited/)).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "conversationCenter.report_outline_confirm" }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    expect(onConfirm).toHaveBeenCalledWith([
      { section_key: "exec_summary", title: "三季度质量回顾", requires_evidence: true },
      { section_key: "appendix", title: "附录", requires_evidence: false },
    ]);
  });

  it("blocks confirmation while a title is empty", async () => {
    const user = userEvent.setup();
    const { onConfirm } = setup();
    await user.clear(screen.getByDisplayValue("执行摘要"));
    await user.click(screen.getByRole("button", { name: "conversationCenter.report_outline_confirm" }));
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("renders the auto-mode confirm label when not confirmed", () => {
    setup({ mode: "auto" });
    expect(screen.getByText("conversationCenter.report_outline_auto")).toBeInTheDocument();
  });

  it("renders the confirmed badge once the outline is locked in", () => {
    setup({ confirmed: true });
    expect(screen.getByText("conversationCenter.report_outline_confirmed")).toBeInTheDocument();
  });

  it("disables editing while busy", () => {
    setup({ busy: true });
    expect(screen.getByDisplayValue("执行摘要")).toBeDisabled();
  });

  // ---- outline evidence preview (doc/288 §9.6, v175) ------------------------
  //
  // The confirmation is the last cheap moment to notice that a section has no
  // evidence. Four states must stay distinguishable, because they answer
  // different questions: hits (we looked and found something), thin (we looked
  // and the template minimum was not met), unjudged (we did not look), and
  // nothing frozen at all (a pre-v175 row).

  it("shows the frozen hit count and main sources per section", () => {
    setup({
      previews: {
        exec_summary: {
          hit_chunks: 3,
          top_sources: ["制度汇编.md", "报销手册.md"],
          insufficient: false,
        },
        appendix: { hit_chunks: 0, top_sources: [], insufficient: false },
      },
    });
    const hits = screen.getAllByTestId("outline-evidence-hits");
    expect(hits).toHaveLength(2);
    expect(hits[0]).toHaveTextContent(
      "conversationCenter.report_outline_evidence_preview_hits",
    );
    expect(hits[0]).toHaveTextContent(
      "conversationCenter.report_outline_evidence_preview_sources",
    );
  });

  it("warns about thin evidence without claiming the section is empty", () => {
    setup({
      previews: {
        exec_summary: { hit_chunks: 1, top_sources: ["制度汇编.md"], insufficient: true },
      },
    });
    const thin = screen.getByTestId("outline-evidence-thin");
    expect(thin).toHaveTextContent(
      "conversationCenter.report_outline_evidence_preview_thin",
    );
    // It is a warning, not a verdict: the reviewer can still confirm.
    expect(
      screen.getByRole("button", { name: "conversationCenter.report_outline_confirm" }),
    ).toBeEnabled();
  });

  it("renders an unavailable preview as unjudged, never as healthy", () => {
    setup({
      previews: { exec_summary: { hit_chunks: 0, top_sources: [], insufficient: null, note: "retrieval_unavailable" } },
    });
    // Two sections, one with no frozen preview at all — both must read 未评估,
    // and neither may borrow the "hits" line.
    expect(screen.getAllByTestId("outline-evidence-unjudged")).toHaveLength(2);
    expect(screen.queryAllByTestId("outline-evidence-hits")).toHaveLength(0);
    expect(screen.queryAllByTestId("outline-evidence-thin")).toHaveLength(0);
  });

  it("keeps the preview a preview: the notice says it is not a sufficiency verdict", () => {
    setup({ previews: {} });
    expect(screen.getByTestId("outline-evidence-notice")).toHaveTextContent(
      "conversationCenter.report_outline_evidence_preview_notice",
    );
  });
});
