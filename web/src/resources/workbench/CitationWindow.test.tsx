import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";

const { apiGet } = vi.hoisted(() => ({
  apiGet: vi.fn(),
}));

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
}));

vi.mock("../../lib/api", () => ({
  api: { get: apiGet },
}));

vi.mock("@/components/documents/original-document-viewer", () => ({
  OriginalDocumentViewer: ({ filename }: { filename?: string }) => (
    <div data-testid="citation-spreadsheet-preview">{filename}</div>
  ),
}));

import { CitationWindow } from "./CitationWindow";
import type { Citation } from "./workbench-types";

describe("<CitationWindow />", () => {
  it("previews an Excel citation inside the citation dialog", async () => {
    apiGet.mockResolvedValue({
      data: new Blob([]),
      headers: { "content-type": "application/octet-stream" },
    });
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:xlsx");
    const citation: Citation = {
      id: "chunk-1",
      name: "兖矿能源安全风险点防控情况统计表.xlsx",
      content: "",
      datasetId: "dataset-1",
      docId: "doc-1",
      chunkId: "chunk-1",
    };

    render(
      <CitationWindow
        open
        onOpenChange={vi.fn()}
        citation={citation}
        content=""
        loading={false}
        onOpenDocument={vi.fn()}
      />,
    );

    await waitFor(() => {
      expect(apiGet).toHaveBeenCalledWith(
        "/chat/document/preview?dataset=dataset-1&doc=doc-1",
        expect.objectContaining({ responseType: "blob" }),
      );
      expect(screen.getByTestId("citation-spreadsheet-preview")).toHaveTextContent(
        "兖矿能源安全风险点防控情况统计表.xlsx",
      );
    });
  });
});
