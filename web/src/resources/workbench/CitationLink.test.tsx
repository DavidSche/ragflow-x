import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { CitationLink } from "./CitationLink";
import type { Citation } from "./workbench-types";

// ra-core is only used for useTranslate; stub it. The Radix popover is replaced
// with a thin pass-through so the test stays focused on CitationLink's own logic
// (no portal / animation machinery in jsdom).
vi.mock("ra-core", () => ({ useTranslate: () => (key: string) => key }));
vi.mock("@/components/ui/popover", () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  Popover: ({ children }: any) => <>{children}</>,
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  PopoverTrigger: ({ children }: any) => <>{children}</>,
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  PopoverContent: ({ children, ..._props }: any) => <div>{children}</div>,
}));

const citation: Citation = { name: "a.pdf", content: "body" };

describe("CitationLink", () => {
  it("renders an ID pill and is disabled without a citation", () => {
    render(<CitationLink index={3} onOpen={vi.fn()} />);
    const pill = screen.getByRole("button", {
      name: "workbench.citation_view_ref",
    }) as HTMLButtonElement;
    expect(pill.textContent).toBe("ID:3");
    expect(pill.disabled).toBe(true);
  });

  it("opens the citation when clicked", () => {
    const onOpen = vi.fn();
    render(<CitationLink index={1} citation={citation} onOpen={onOpen} />);
    fireEvent.click(screen.getByRole("button", { name: "workbench.citation_view_ref" }));
    expect(onOpen).toHaveBeenCalledWith(citation);
  });

  it("does not forward the click to onOpen when disabled", () => {
    const onOpen = vi.fn();
    render(<CitationLink index={2} onOpen={onOpen} />);
    fireEvent.click(screen.getByRole("button", { name: "workbench.citation_view_ref" }));
    expect(onOpen).not.toHaveBeenCalled();
  });
});
