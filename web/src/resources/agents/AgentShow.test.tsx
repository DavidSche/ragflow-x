import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentShow } from "./AgentShow";

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  notify: vi.fn(),
}));

vi.mock("react-router-dom", () => ({
  useParams: () => ({ id: "agent-1" }),
}));

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
  useNotify: () => mocks.notify,
}));

vi.mock("../../lib/api", () => ({
  api: { get: mocks.get, put: vi.fn(), post: mocks.post },
  ApiError: class extends Error {
    status = 500;
    code = 50000;
    displayMessage = "error";
  },
}));

// The canvas (react-flow) and the chat surface are irrelevant here: this test
// only pins which tabs a canvas kind is allowed to offer.
vi.mock("./AgentCanvas", () => ({ AgentCanvas: () => <div data-testid="canvas" /> }));
vi.mock("./AgentChat", () => ({ AgentChat: () => <div data-testid="chat" /> }));
vi.mock("./VersionPreview", () => ({ VersionPreview: () => null }));
vi.mock("./DiffView", () => ({ DiffView: () => null }));

const detail = (category: string) => ({
  data: { code: 0, data: { id: "agent-1", title: "canvas", release: false, canvas_category: category, dsl: {} } },
});

function stubApi(category: string) {
  mocks.get.mockImplementation((path: string) => {
    if (path.endsWith("/detail")) return Promise.resolve(detail(category));
    if (path.endsWith("/versions")) return Promise.resolve({ data: { code: 0, data: [] } });
    if (path.endsWith("/sessions")) return Promise.resolve({ data: { code: 0, data: { items: [], total: 0 } } });
    return Promise.resolve({ data: { code: 0, data: {} } });
  });
}

describe("AgentShow canvas kinds", () => {
  beforeEach(() => {
    mocks.get.mockReset();
    mocks.post.mockReset();
    mocks.notify.mockReset();
  });

  // A data-pipeline canvas parses documents; sending it chat messages made the
  // engine answer with a node-level error inside the answer bubble (defect ㊳).
  it("does not offer conversation tabs for a data pipeline", async () => {
    stubApi("dataflow_canvas");
    render(<AgentShow />);

    await waitFor(() => expect(screen.getByTestId("canvas")).toBeTruthy());
    expect(screen.getByText("agents.pipeline_notice_title")).toBeTruthy();
    expect(screen.queryByText("agents.tab_sessions")).toBeNull();
    expect(screen.queryByText("agents.tab_run")).toBeNull();
    expect(screen.queryByTestId("chat")).toBeNull();
  });

  it("keeps conversation tabs for a conversational canvas", async () => {
    stubApi("agent_canvas");
    render(<AgentShow />);

    await waitFor(() => expect(screen.getByTestId("canvas")).toBeTruthy());
    expect(screen.getByText("agents.tab_sessions")).toBeTruthy();
    expect(screen.getByText("agents.tab_run")).toBeTruthy();
    expect(screen.queryByText("agents.pipeline_notice_title")).toBeNull();
  });
});

describe("AgentShow publish gate", () => {
  beforeEach(() => {
    mocks.get.mockReset();
    mocks.post.mockReset();
    mocks.notify.mockReset();
  });

  // The stored canvas is what users would get, so the gate reads it from the
  // agent endpoint — not from the editor buffer, which may hold unsaved edits.
  it("refuses to publish a canvas the engine cannot run", async () => {
    mocks.get.mockImplementation((path: string) => {
      if (path.endsWith("/detail")) return Promise.resolve(detail("agent_canvas"));
      if (path.endsWith("/versions")) return Promise.resolve({ data: { code: 0, data: [] } });
      if (path.endsWith("/sessions")) return Promise.resolve({ data: { code: 0, data: { items: [], total: 0 } } });
      if (path.endsWith("/validation")) {
        return Promise.resolve({
          data: {
            code: 0,
            data: {
              ok: false,
              errors: [
                {
                  code: "retrieval_dataset_missing",
                  severity: "error",
                  node_id: "retr",
                  node_label: "Knowledge",
                  message: "knowledge node has no knowledge base selected",
                },
              ],
              warnings: [],
              auto_fixed: [],
            },
          },
        });
      }
      return Promise.resolve({ data: { code: 0, data: {} } });
    });
    render(<AgentShow />);

    await waitFor(() => expect(screen.getByTestId("canvas")).toBeTruthy());
    fireEvent.click(screen.getByText("agents.publish"));

    await waitFor(() =>
      expect(mocks.notify).toHaveBeenCalledWith("agents.validation_blocks_publish", {
        type: "warning",
      }),
    );
    expect(mocks.post).not.toHaveBeenCalled();
    await waitFor(() =>
      expect(screen.getByTestId("agent-validation-error").textContent).toContain("Knowledge"),
    );
  });

  it("publishes when the stored canvas validates", async () => {
    mocks.get.mockImplementation((path: string) => {
      if (path.endsWith("/detail")) return Promise.resolve(detail("agent_canvas"));
      if (path.endsWith("/versions")) return Promise.resolve({ data: { code: 0, data: [] } });
      if (path.endsWith("/sessions")) return Promise.resolve({ data: { code: 0, data: { items: [], total: 0 } } });
      if (path.endsWith("/validation")) {
        return Promise.resolve({ data: { code: 0, data: { ok: true, errors: [], warnings: [] } } });
      }
      return Promise.resolve({ data: { code: 0, data: {} } });
    });
    mocks.post.mockResolvedValue({ data: { code: 0, data: {} } });
    render(<AgentShow />);

    await waitFor(() => expect(screen.getByTestId("canvas")).toBeTruthy());
    fireEvent.click(screen.getByText("agents.publish"));

    await waitFor(() => expect(mocks.post).toHaveBeenCalledWith("/agents/agent-1/publish", { release: true }));
    expect(mocks.notify).toHaveBeenCalledWith("agents.published", { type: "success" });
  });
});
