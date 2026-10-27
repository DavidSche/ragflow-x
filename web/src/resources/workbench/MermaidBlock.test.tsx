import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MermaidBlock } from "./MermaidBlock";

const mermaid = vi.hoisted(() => ({
  initialize: vi.fn(),
  render: vi.fn(),
}));

vi.mock("mermaid", () => ({ default: mermaid }));

describe("<MermaidBlock />", () => {
  beforeEach(() => {
    mermaid.initialize.mockClear();
    mermaid.render.mockReset();
  });

  it("renders model-generated Mermaid in strict mode", async () => {
    mermaid.render.mockResolvedValue({ svg: '<svg data-testid="mermaid-svg" />' });
    render(<MermaidBlock code="graph TD; A --> B" />);

    await waitFor(() => expect(screen.getByTestId("mermaid-svg")).toBeInTheDocument());
    expect(mermaid.initialize).toHaveBeenCalledWith(
      expect.objectContaining({ startOnLoad: false, securityLevel: "strict" }),
    );
    expect(mermaid.render).toHaveBeenCalledWith(expect.any(String), "graph TD; A --> B");
  });

  it("falls back to source text with a visible error", async () => {
    mermaid.render.mockRejectedValue(new Error("diagram syntax error"));
    render(<MermaidBlock code="graph TD; A -> B" />);

    expect(await screen.findByText(/Mermaid 渲染失败/)).toBeInTheDocument();
    expect(screen.getByText("graph TD; A -> B")).toBeInTheDocument();
  });
});
