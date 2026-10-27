import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { KnowledgeGraphPanel } from "./KnowledgeGraphPanel";

const mocks = vi.hoisted(() => ({
  apiGet: vi.fn(),
  notify: vi.fn(),
}));

const translate = (key: string) => key;

vi.mock("ra-core", () => ({
  useGetIdentity: () => ({ data: { tenant_id: "t1" } }),
  useNotify: () => mocks.notify,
  useRecordContext: () => ({ id: "dataset-1", tenant_id: "t1" }),
  useTranslate: () => translate,
}));

vi.mock("../../lib/api", () => ({
  api: {
    get: mocks.apiGet,
  },
  ApiError: class ApiError extends Error {
    displayMessage = "boom";
  },
  unwrap: (promise: Promise<{ data: { data: unknown } }>) =>
    promise.then((response) => response.data.data),
}));

const graph = {
  nodes: [
    { id: "n1", labels: ["Alpha"], x: 0.2, y: 0.3 },
    { id: "n2", labels: ["Beta"], x: 0.8, y: 0.7 },
  ],
  edges: [{ source: "n1", target: "n2", weight: 2, description: "related to" }],
};

describe("KnowledgeGraphPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("loads and renders nodes and edges as read-only SVG", async () => {
    mocks.apiGet.mockResolvedValue({ data: { data: graph } });
    render(<KnowledgeGraphPanel />);

    await waitFor(() => {
      expect(mocks.apiGet).toHaveBeenCalledWith("/datasets/dataset-1/graph");
    });
    await waitFor(() => {
      expect(screen.getByTestId("knowledge-graph-svg")).toBeInTheDocument();
    });
    expect(screen.getAllByTestId("graph-node")).toHaveLength(2);
    expect(screen.getByText("datasets.graph_nodes")).toBeInTheDocument();
    expect(screen.getByText("datasets.graph_edges")).toBeInTheDocument();
  });

  it("shows the empty state when the engine has no graph", async () => {
    mocks.apiGet.mockResolvedValue({ data: { data: { nodes: [], edges: [] } } });
    render(<KnowledgeGraphPanel />);
    await waitFor(() => {
      expect(screen.getByText("datasets.graph_empty")).toBeInTheDocument();
    });
    expect(screen.queryByTestId("knowledge-graph-svg")).not.toBeInTheDocument();
  });

  it("surfaces load failures through the notifier", async () => {
    mocks.apiGet.mockRejectedValue(new Error("network down"));
    render(<KnowledgeGraphPanel />);
    await waitFor(() => {
      expect(mocks.notify).toHaveBeenCalledWith("datasets.graph_load_failed", { type: "error" });
    });
  });

  it("refreshes on demand", async () => {
    mocks.apiGet.mockResolvedValue({ data: { data: graph } });
    render(<KnowledgeGraphPanel />);
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "datasets.graph_refresh" })).toBeInTheDocument();
    });
    await userEvent.click(screen.getByRole("button", { name: "datasets.graph_refresh" }));
    await waitFor(() => {
      expect(mocks.apiGet).toHaveBeenCalledTimes(2);
    });
  });
});
