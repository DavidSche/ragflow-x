import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { NumericRangeField } from "./NumericRangeField";

describe("NumericRangeField", () => {
  it("renders the range and clamps invalid typed values on blur", async () => {
    const onChange = vi.fn();
    render(
      <NumericRangeField
        label="Tokens"
        value="512"
        min={1}
        max={2048}
        unit="tokens"
        onChange={onChange}
      />,
    );

    const slider = screen.getByRole("slider", { name: "Tokens tokens" });
    expect(slider).toHaveAttribute("min", "1");
    expect(slider).toHaveAttribute("max", "2048");
    expect(screen.getByText("1 ~ 2048")).toBeInTheDocument();

    const input = screen.getByLabelText("Tokens");
    await userEvent.clear(input);
    await userEvent.type(input, "9999");
    await userEvent.tab();

    expect(onChange).toHaveBeenLastCalledWith(2048);
    expect(input).toHaveValue(2048);
  });

  it("commits empty values only in allowEmpty mode", async () => {
    const onChange = vi.fn();
    render(
      <NumericRangeField
        label="Quota"
        value=""
        min={0}
        max={100}
        allowEmpty
        onChange={onChange}
      />,
    );

    const input = screen.getByRole("spinbutton", { name: "Quota" });
    await userEvent.clear(input);
    await userEvent.tab();

    expect(onChange).toHaveBeenLastCalledWith(null);
    expect(input).toHaveValue(null);
  });
});
