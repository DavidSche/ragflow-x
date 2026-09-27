import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { InlineCitations } from "./InlineCitations";
import type { Citation } from "./workbench-types";

// InlineCitations only needs ra-core's useTranslate hook; replace it with an
// identity translate so aria-labels become easy to assert on.
vi.mock("ra-core", () => ({ useTranslate: () => (key: string) => key }));

function citation(name: string, opts: Partial<Citation> = {}): Citation {
  return { name, content: "chunk-body", datasetId: "ds1", docId: "doc1", ...opts };
}

describe("InlineCitations", () => {
  it("groups citations by source document and shows deduplicated pills", () => {
    const citations = [
      citation("a.pdf"),
      citation("a.pdf", { content: "second chunk of a" }),
      citation("b.pdf"),
    ];
    render(
      <InlineCitations
        content="see [ID:0], [ID:1] and [ID:2]"
        citations={citations}
        onOpen={vi.fn()}
        onOpenDocument={vi.fn()}
      />,
    );

    const summary = screen.getByRole("button", { name: "workbench.citation_source_count" });
    expect(summary).toHaveTextContent("workbench.citation_source_count");
    fireEvent.click(summary);

    const aPill = screen.getByRole("button", { name: "workbench.citation_source：a.pdf" });
    const bPill = screen.getByRole("button", { name: "workbench.citation_source：b.pdf" });
    expect(aPill).toBeInTheDocument();
    expect(bPill).toBeInTheDocument();

    // a.pdf is referenced twice ([ID:0], [ID:1] both map to it) -> shows x2.
    expect(aPill.parentElement?.textContent).toContain("×2");
    expect(bPill.parentElement?.textContent).not.toContain("×");
  });

  it("calls onOpen with the grouped sample citation on pill click", () => {
    const onOpen = vi.fn();
    const sample = citation("a.pdf");
    render(
      <InlineCitations
        content="[ID:0]"
        citations={[sample]}
        onOpen={onOpen}
        onOpenDocument={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "workbench.citation_source_count" }));
    fireEvent.click(screen.getByRole("button", { name: "workbench.citation_source：a.pdf" }));
    expect(onOpen).toHaveBeenCalledWith(sample);
  });

  it("calls onOpenDocument only when dataset and doc ids are present", () => {
    const onOpenDocument = vi.fn();
    const { rerender } = render(
      <InlineCitations
        content="[ID:0]"
        citations={[citation("a.pdf")]}
        onOpen={vi.fn()}
        onOpenDocument={onOpenDocument}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "workbench.citation_source_count" }));
    fireEvent.click(screen.getByRole("button", { name: "workbench.citation_view_doc：a.pdf" }));
    expect(onOpenDocument).toHaveBeenCalledTimes(1);

    // Without datasetId/docId no view-document button is rendered.
    rerender(
      <InlineCitations
        content="[ID:0]"
        citations={[{ name: "bare.pdf", content: "x" }]}
        onOpen={vi.fn()}
        onOpenDocument={onOpenDocument}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "workbench.citation_source_count" }));
    expect(screen.queryByRole("button", { name: "workbench.citation_view_doc：bare.pdf" })).toBeNull();
  });

  it("renders an empty container when there is nothing to cite", () => {
    const { container } = render(
      <InlineCitations content="" citations={[]} onOpen={vi.fn()} onOpenDocument={vi.fn()} />,
    );
    expect(container.firstChild).toBeEmptyDOMElement();
  });
});
