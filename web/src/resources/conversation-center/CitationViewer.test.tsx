import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ConversationCitationViewer } from "./CitationViewer";
import type { Citation } from "../workbench/workbench-types";
import { api } from "../../lib/api";

const citation: Citation = {
  id: "chunk-1",
  name: "contract.pdf",
  content: "payment clause",
  datasetId: "dataset-1",
  docId: "doc-1",
  chunkId: "chunk-1",
};

vi.mock("../../lib/api", () => ({
  api: { get: vi.fn().mockResolvedValue({ data: { code: 0, data: { content: "payment clause" } } }) },
}));

beforeEach(() => {
  vi.mocked(api.get).mockClear().mockResolvedValue({ data: { code: 0, data: { content: "payment clause" } } });
});

describe("ConversationCitationViewer", () => {
  it("shows returned citation content without a document deep link when chat read is unavailable", async () => {
    render(<ConversationCitationViewer citation={citation} onCitationChange={() => undefined} documentEnabled={false} />);
    await waitFor(() => expect(screen.getByText("payment clause")).toBeInTheDocument());
    expect(screen.getByRole("button", { name: "workbench.citation_view_doc" })).toBeDisabled();
  });

  it("enables the document deep link only for chat-read users", () => {
    render(<ConversationCitationViewer citation={citation} onCitationChange={() => undefined} documentEnabled />);
    expect(screen.getByRole("button", { name: "workbench.citation_view_doc" })).toBeEnabled();
  });
});
