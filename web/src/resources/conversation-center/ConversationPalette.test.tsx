import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ConversationCenterInput } from "./ConversationCenterInput";
import { ConversationPalette } from "./ConversationPalette";
import type { ConversationTargetRef } from "./types";

const baseInputProps = {
  input: "@",
  inputRef: { current: null },
  attachments: [],
  paletteOpen: true,
  paletteId: "palette",
  activeOptionIndex: 0,
  activeOptionId: "palette-option-0",
  commandOptions: [],
  targetOptions: [
    { kind: "chat", id: "chat-1", name: "Support Chat" },
  ] as ConversationTargetRef[],
  disabled: false,
  busy: false,
  onChange: vi.fn(),
  onKeyDown: vi.fn(),
  onCompositionStart: vi.fn(),
  onCompositionEnd: vi.fn(),
  onRemoveAttachment: vi.fn(),
  onRunCommand: vi.fn(),
  onChooseTarget: vi.fn(),
  onCancel: vi.fn(),
  onSubmit: vi.fn(),
};

describe("conversation palette combobox contract", () => {
  it("keeps focus on the textarea and points activedescendant at an existing option", () => {
    render(<ConversationCenterInput {...baseInputProps} />);
    const textarea = screen.getByRole("combobox");
    const option = screen.getByRole("option", {
      name: "Support Chat conversationCenter.kind_chat",
    });

    expect(textarea).toHaveAttribute("aria-expanded", "true");
    expect(textarea).toHaveAttribute("aria-controls", "palette");
    expect(textarea).toHaveAttribute("aria-activedescendant", "palette-option-0");
    expect(option).toHaveAttribute("id", "palette-option-0");
    expect(option).toHaveAttribute("tabindex", "-1");
    expect(option).not.toHaveAttribute("aria-describedby");
  });

  it("clears popup references when the palette is closed", () => {
    render(
      <ConversationCenterInput
        {...baseInputProps}
        paletteOpen={false}
        activeOptionId={undefined}
      />,
    );
    const textarea = screen.getByRole("combobox");

    expect(textarea).toHaveAttribute("aria-expanded", "false");
    expect(textarea).not.toHaveAttribute("aria-controls");
    expect(textarea).not.toHaveAttribute("aria-activedescendant");
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });
});
