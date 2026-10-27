import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";

const { renderAsyncMock } = vi.hoisted(() => ({
  renderAsyncMock: vi.fn(async () => undefined),
}));

vi.mock("docx-preview", () => ({
  renderAsync: renderAsyncMock,
}));

import { officePreviewKind } from "./document-preview";
import { OriginalDocumentViewer } from "./original-document-viewer";

vi.mock("read-excel-file/browser", () => ({
  default: vi.fn(async () => [
    { sheet: "风险台账", data: [["风险点", "已整改"]] },
  ]),
}));

describe("officePreviewKind", () => {
  it("detects Word, Excel and PowerPoint files", () => {
    expect(officePreviewKind("", "report.docx")).toBe("docx");
    expect(
      officePreviewKind(
        "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
        "",
      ),
    ).toBe("xlsx");
    expect(officePreviewKind("", "deck.pptx")).toBe("pptx");
  });

  it("does not claim legacy Office binary formats", () => {
    expect(officePreviewKind("", "legacy.doc")).toBeNull();
    expect(officePreviewKind("", "legacy.ppt")).toBeNull();
  });
});

describe("OriginalDocumentViewer", () => {
  it("renders Markdown source as formatted content", async () => {
    render(
      <OriginalDocumentViewer
        blobUrl="blob:markdown"
        contentType="text/markdown"
        text={"## 强化问题隐患溯源\n\n- 压实主要负责人职责"}
      />,
    );
    expect(await screen.findByTestId("markdown-preview")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "强化问题隐患溯源" })).toBeInTheDocument();
    expect(screen.getByText("压实主要负责人职责")).toBeInTheDocument();
  });

  it("renders HTML in a script-disabled frame", () => {
    render(
      <OriginalDocumentViewer
        blobUrl="blob:html"
        contentType="text/html"
        text="<h1>安全政策</h1>"
      />,
    );
    const frame = screen.getByTitle("document preview") as HTMLIFrameElement;
    expect(frame.getAttribute("sandbox")).toBe("");
    expect(frame.getAttribute("srcdoc")).toContain("安全政策");
  });

  it("renders CSV as a table", () => {
    render(
      <OriginalDocumentViewer
        blobUrl="blob:csv"
        contentType="text/csv"
        text={"风险点,状态\n高处作业,已整改"}
      />,
    );
    expect(screen.getByTestId("csv-preview")).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "风险点" })).toBeInTheDocument();
    expect(screen.getByText("已整改")).toBeInTheDocument();
  });

  it("renders JSON as formatted text", () => {
    render(
      <OriginalDocumentViewer
        blobUrl="blob:json"
        contentType="application/json"
        text='{"risk":"high"}'
      />,
    );
    expect(JSON.parse(screen.getByTestId("json-preview").textContent ?? "{}")).toEqual({ risk: "high" });
  });

  it("renders media formats with native controls", () => {
    const { rerender } = render(
      <OriginalDocumentViewer blobUrl="blob:audio" contentType="audio/mpeg" />,
    );
    expect(screen.getByTestId("document-audio")).toBeInTheDocument();
    rerender(
      <OriginalDocumentViewer blobUrl="blob:video" contentType="video/mp4" />,
    );
    expect(screen.getByTestId("document-video")).toBeInTheDocument();
  });

  it("renders a Word document through docx-preview", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({ arrayBuffer: async () => new ArrayBuffer(8) }) as Response),
    );
    render(
      <OriginalDocumentViewer
        blobUrl="blob:test"
        contentType=""
        filename="安全报告.docx"
      />,
    );
    await waitFor(() => expect(renderAsyncMock).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId("office-preview")).toBeInTheDocument();
  });

  it("renders an Excel workbook as a table", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => ({ arrayBuffer: async () => new ArrayBuffer(8) }) as Response),
    );
    render(
      <OriginalDocumentViewer
        blobUrl="blob:test"
        contentType=""
        filename="台账.xlsx"
      />,
    );
    expect(await screen.findByText("风险台账")).toBeInTheDocument();
    expect(screen.getByText("风险点")).toBeInTheDocument();
    expect(screen.getByText("已整改")).toBeInTheDocument();
  });
});
