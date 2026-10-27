import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RetrievalTestPanel } from "./RetrievalTestPanel";

const mocks = vi.hoisted(() => ({
  apiPost: vi.fn(),
  notify: vi.fn(),
}));

vi.mock("ra-core", () => ({
  useGetIdentity: () => ({ data: { tenant_id: "t1" } }),
  useNotify: () => mocks.notify,
  useRecordContext: () => ({ id: "dataset-1", tenant_id: "t1" }),
  useTranslate: () => (key: string) => key,
}));

vi.mock("../../lib/api", () => ({
  api: {
    post: mocks.apiPost,
  },
  ApiError: class ApiError extends Error {
    displayMessage = "boom";
  },
  unwrap: (promise: Promise<{ data: { data: unknown } }>) =>
    promise.then((response) => response.data.data),
}));

const notify = mocks.notify;

const result = {
  question: "what is the policy?",
  total: 2,
  duration_ms: 42,
  similarity_threshold: 0.2,
  vector_similarity_weight: 0.3,
  chunks: [
    { id: "chunk-1", content: "first hit body", similarity: 0.9, docnm_kwd: "policy.md" },
    { id: "chunk-2", content: "second hit body", similarity: 0.72, docnm_kwd: "faq.md" },
  ],
};

describe("RetrievalTestPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("submits the tuning parameters and renders retrieved chunks", async () => {
    mocks.apiPost.mockResolvedValue({ data: { data: result } });
    render(<RetrievalTestPanel />);

    const question = screen.getByLabelText("datasets.retrieval_test_question");
    await userEvent.type(question, "what is the policy?");
    await userEvent.click(screen.getByRole("button", { name: "datasets.retrieval_test_run" }));

    await waitFor(() => {
      expect(screen.getByText("first hit body")).toBeInTheDocument();
    });
    expect(screen.getByText("second hit body")).toBeInTheDocument();
    expect(mocks.apiPost).toHaveBeenCalledWith("/datasets/dataset-1/retrieval-test", {
      question: "what is the policy?",
      page_size: 5,
      similarity_threshold: 0.2,
      vector_similarity_weight: 0.3,
      keyword: false,
      rerank_id: undefined,
    });
    expect(screen.getByText("datasets.retrieval_test_hits")).toBeInTheDocument();
  });

  it("blocks the run without a question", async () => {
    render(<RetrievalTestPanel />);
    await userEvent.click(screen.getByRole("button", { name: "datasets.retrieval_test_run" }));
    await waitFor(() => {
      expect(notify).toHaveBeenCalledWith("datasets.retrieval_test_question_required", { type: "warning" });
    });
    expect(mocks.apiPost).not.toHaveBeenCalled();
  });

  it("rejects out-of-range tuning values before calling the API", async () => {
    render(<RetrievalTestPanel />);
    await userEvent.type(screen.getByLabelText("datasets.retrieval_test_question"), "q");
    await userEvent.clear(screen.getByLabelText("datasets.retrieval_test_top_n"));
    await userEvent.type(screen.getByLabelText("datasets.retrieval_test_top_n"), "99");
    await userEvent.click(screen.getByRole("button", { name: "datasets.retrieval_test_run" }));
    await waitFor(() => {
      expect(notify).toHaveBeenCalledWith("datasets.retrieval_test_page_size_invalid", { type: "warning" });
    });
    expect(mocks.apiPost).not.toHaveBeenCalled();
  });

  it("surfaces API errors through the notifier", async () => {
    mocks.apiPost.mockRejectedValue(new Error("network down"));
    render(<RetrievalTestPanel />);
    await userEvent.type(screen.getByLabelText("datasets.retrieval_test_question"), "q");
    await userEvent.click(screen.getByRole("button", { name: "datasets.retrieval_test_run" }));
    await waitFor(() => {
      expect(notify).toHaveBeenCalledWith("datasets.retrieval_test_failed", { type: "error" });
    });
  });
});
