import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { DocumentsPanel } from "./DocumentsPanel";

const mocks = vi.hoisted(() => ({
  useCanAccess: vi.fn(),
  apiGet: vi.fn(),
  apiPost: vi.fn(),
  apiPut: vi.fn(),
  apiPatch: vi.fn(),
}));

vi.mock("ra-core", () => ({
  useCanAccess: mocks.useCanAccess,
  useGetIdentity: () => ({ data: { tenant_id: "t1" } }),
  useNotify: () => vi.fn(),
  useRecordContext: () => ({ id: "dataset-1", tenant_id: "t1" }),
  useTranslate: () => (key: string) => key,
}));

vi.mock("../../lib/api", () => ({
  api: {
    get: mocks.apiGet,
    post: mocks.apiPost,
    put: mocks.apiPut,
    patch: mocks.apiPatch,
    delete: vi.fn(),
  },
}));

vi.mock("../../components/documents/original-document-viewer", () => ({
  OriginalDocumentViewer: () => <div data-testid="preview-stub" />,
}));

const doc = {
  id: "doc-1",
  name: "policy.md",
  status: "3",
  enabled: true,
  size: 12,
};

const chunk = {
  id: "chunk-1",
  content: "original chunk body",
  keywords: ["alpha", "beta"],
  questions: ["what is alpha?"],
};

function apiGetDispatch(url: string) {
  if (url.includes("/documents/doc-1/chunks?page=")) {
    return Promise.resolve({
      data: { data: { items: [chunk], total: 1, page: 1 } },
    });
  }
  if (url.includes("/documents/doc-1/preview")) {
    return Promise.resolve({
      data: new Blob(["policy body"], { type: "text/plain" }),
      headers: { "content-type": "text/plain" },
    });
  }
  if (url.includes("/documents")) {
    return Promise.resolve({ data: { data: [doc] } });
  }
  return Promise.resolve({ data: { data: [] } });
}

async function openChunkDialog() {
  const user = userEvent.setup();
  render(<DocumentsPanel />);
  await screen.findByText("policy.md");
  await user.click(
    screen.getByRole("button", { name: "datasets.doc_chunks_btn" }),
  );
  await screen.findByText("original chunk body");
  return user;
}

describe("DocumentsPanel chunk editing", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.useCanAccess.mockImplementation(({ action }: { action: string }) => ({
      canAccess: action !== "append",
    }));
    mocks.apiGet.mockImplementation(apiGetDispatch);
    mocks.apiPost.mockResolvedValue({
      data: { data: { chunk: { id: "chunk-2", content: "x" } } },
    });
    mocks.apiPut.mockResolvedValue({ data: { data: { updated: true } } });
  });

  it("adds a chunk through the create dialog", async () => {
    const user = await openChunkDialog();
    await user.click(screen.getByRole("button", { name: "datasets.chunk_add" }));

    await screen.findByRole("heading", { name: "datasets.chunk_add" });
    await user.type(
      screen.getByLabelText("datasets.chunk_edit_content"),
      "manually added chunk",
    );
    await user.type(
      screen.getByLabelText("datasets.chunk_edit_keywords"),
      "manual, ingest",
    );
    await user.type(
      screen.getByLabelText("datasets.chunk_edit_questions"),
      "who added this?",
    );
    await user.click(screen.getByRole("button", { name: "datasets.chunk_save" }));

    await waitFor(() => expect(mocks.apiPost).toHaveBeenCalledTimes(1));
    const [url, body] = mocks.apiPost.mock.calls[0];
    expect(url).toBe("/datasets/dataset-1/documents/doc-1/chunks");
    expect(body).toEqual({
      content: "manually added chunk",
      important_keywords: ["manual", "ingest"],
      questions: ["who added this?"],
    });
  });

  it("edits an existing chunk and submits only changed fields", async () => {
    const user = await openChunkDialog();
    await user.click(
      screen.getByRole("button", { name: "datasets.chunk_edit" }),
    );

    await screen.findByDisplayValue("original chunk body");
    const keywordsField = screen.getByLabelText(
      "datasets.chunk_edit_keywords",
    ) as HTMLInputElement;
    expect(keywordsField.value).toBe("alpha, beta");
    const questionsField = screen.getByLabelText(
      "datasets.chunk_edit_questions",
    ) as HTMLInputElement;
    expect(questionsField.value).toBe("what is alpha?");

    const contentField = screen.getByLabelText(
      "datasets.chunk_edit_content",
    ) as HTMLTextAreaElement;
    await user.clear(contentField);
    await user.type(contentField, "rewritten body");
    await user.click(screen.getByRole("button", { name: "datasets.chunk_save" }));

    await waitFor(() => expect(mocks.apiPut).toHaveBeenCalledTimes(1));
    const [url, body] = mocks.apiPut.mock.calls[0];
    expect(url).toBe("/datasets/dataset-1/documents/doc-1/chunks/chunk-1");
    // PUT is an idempotent full-field replacement: unchanged visible fields
    // round-trip their prefilled values verbatim.
    expect(body).toEqual({
      content: "rewritten body",
      important_keywords: ["alpha", "beta"],
      questions: ["what is alpha?"],
    });
  });

  it("hides chunk authoring actions without execute permission", async () => {
    mocks.useCanAccess.mockImplementation(() => ({ canAccess: false }));
    await openChunkDialog();
    expect(
      screen.queryByRole("button", { name: "datasets.chunk_add" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "datasets.chunk_edit" }),
    ).not.toBeInTheDocument();
  });
});
