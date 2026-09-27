import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { PromptPresetPicker } from "./PromptPresetPicker";

describe("<PromptPresetPicker />", () => {
  it("renders prompt and parameter presets", () => {
    render(
      <PromptPresetPicker
        activePresetId="policy"
        activeProfileId="safe"
        onSelectPreset={() => undefined}
        onSelectProfile={() => undefined}
      />,
    );
    expect(screen.getByText("制度与员工助手")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Safe/ })).toHaveAttribute("aria-pressed", "true");
  });

  it("emits selected preset and profile", async () => {
    const user = userEvent.setup();
    const onSelectPreset = vi.fn();
    const onSelectProfile = vi.fn();
    render(
      <PromptPresetPicker
        onSelectPreset={onSelectPreset}
        onSelectProfile={onSelectProfile}
      />,
    );
    await user.click(screen.getByText("合同与合规"));
    await user.click(screen.getByRole("button", { name: /Speed/ }));
    expect(onSelectPreset).toHaveBeenCalledWith("contract-compliance");
    expect(onSelectProfile).toHaveBeenCalledWith("speed");
  });

  it("can hide scenario prompts for retrieval-only configuration", () => {
    render(
      <PromptPresetPicker
        showPrompts={false}
        onSelectPreset={() => undefined}
        onSelectProfile={() => undefined}
      />,
    );
    expect(screen.queryByText("提示词模板")).not.toBeInTheDocument();
    expect(screen.getByText("参数档位")).toBeInTheDocument();
  });
});
