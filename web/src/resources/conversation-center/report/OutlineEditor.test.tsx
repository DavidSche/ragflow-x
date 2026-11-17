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
});
