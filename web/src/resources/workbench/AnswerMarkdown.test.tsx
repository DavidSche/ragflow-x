// ScenarioID: SC-WORKBENCH-001
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AnswerMarkdown } from "./AnswerMarkdown";
import type { Citation } from "./workbench-types";

vi.mock("./CitationLink", () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  CitationLink: ({ index, citation, onOpen }: any) => (
    <button type="button" onClick={() => onOpen(citation)}>{`ID:${index}`}</button>
  ),
}));

const citation: Citation = { name: "manual.pdf", content: "操作步骤" };

describe("<AnswerMarkdown />", () => {
  it("renders headings, tables, inline code and citation links", () => {
    const onOpen = vi.fn();
    const markdown = [
      "# 制度答案",
      "",
      "| 项目 | 值 |",
      "|---|---|",
      "| 年假 | 5 天 |",
      "",
      "引用 [ID:0]",
    ].join("\n");
    render(<AnswerMarkdown content={markdown} citations={[citation]} onOpenCitation={onOpen} />);

    expect(screen.getByRole("heading", { level: 1, name: "制度答案" })).toBeInTheDocument();
    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getByText("年假")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "ID:0" }));
    expect(onOpen).toHaveBeenCalledWith(citation);
  });

  it("does not allow unsafe markdown URL schemes", () => {
    render(<AnswerMarkdown content='[危险链接](javascript:alert(1))' onOpenCitation={vi.fn()} />);
    const link = screen.getByRole("link", { name: "危险链接" });
    expect(link).toHaveAttribute("href", "#");
  });

  it("renders normal markdown links and code blocks", () => {
    render(
      <AnswerMarkdown
        content={"[手册](https://example.com/manual)\n\n```js\nconst answer = 42;\n```"}
        onOpenCitation={vi.fn()}
      />,
    );
    expect(screen.getByRole("link", { name: "手册" })).toHaveAttribute("href", "https://example.com/manual");
    expect(screen.getByText(/const answer = 42;/)).toBeInTheDocument();
  });

  // The agent canvas' Retrieval -> LLM path cites engine chunk ids, not indices.
  it("resolves engine chunk-id markers to the matching citation", () => {
    const chunked: Citation = { name: "manual.pdf", content: "操作步骤", chunkId: "bd7636ac5bbdadce" };
    const onOpen = vi.fn();
    render(
      <AnswerMarkdown content={"依据 [ID:bd7636ac5bbdadce]"} citations={[chunked]} onOpenCitation={onOpen} />,
    );
    // The mocked CitationLink prints ID:<index>, so a resolved id token shows ID:0.
    fireEvent.click(screen.getByRole("button", { name: "ID:0" }));
    expect(onOpen).toHaveBeenCalledWith(chunked);
    // and the raw marker is gone
    expect(screen.queryByText(/bd7636ac5bbdadce/)).toBeNull();
  });

  it("keeps an unresolvable chunk-id marker as a muted chip instead of mangling it", () => {
    render(<AnswerMarkdown content={"依据 [ID:bd7636ac5bbdadce]"} onOpenCitation={vi.fn()} />);
    // Previously this rendered "ID:7636ID:5": digits scraped out of the hex id.
    expect(screen.queryByText(/ID:7636/)).toBeNull();
    expect(screen.getByText("ID:bd7636ac")).toBeInTheDocument();
  });
});
