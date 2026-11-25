// ScenarioID: SC-GATEWAY-002
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { buildUsageDetailParams, UsageDetailPanel } from "./UsageDetailTable";

const api = vi.hoisted(() => ({
  get: vi.fn(),
}));

vi.mock("../../lib/api", () => ({
  ApiError: class ApiError extends Error {
    displayMessage: string;
    constructor(message: string) {
      super(message);
      this.displayMessage = message;
    }
  },
  api,
  unwrap: async <T,>(request: Promise<{ data: { data: T } }>) => (await request).data.data,
}));

// A stable translator: returning a fresh function every render makes the
// panels' `load` callback (it depends on `t`) change identity and re-fetch in a
// loop — a test artefact that production's memoized t does not have.
const translate = (key: string) => key;

vi.mock("ra-core", () => ({
  useCanAccess: () => ({ data: true }),
  useTranslate: () => translate,
}));

const detail = {
  id: "detail-1", request_id: "req-1234567890", tenant_id: "tenant-1",
  user_id: "user-1", key_id: "key-1", model: "qwen-max", scenario: "chat",
  chat_id: "chat-1", session_id: "session-1", dataset_ids: "",
  tokens_in: 20, tokens_out: 10, estimated_cost: 0.006, created_at: "2026-09-16T00:00:00Z",
};

const filterOptions = {
  models: [{ value: "qwen-max", label: "qwen-max" }],
  scenarios: [{ value: "chat", label: "chat" }],
  users: [{ value: "user-1", label: "alice" }],
  keys: [{ value: "key-1", label: "Gateway key (rgx_ab12)" }],
  chats: [{ value: "chat-9", label: "Policy assistant" }],
  tenants: [{ value: "tenant-a", label: "Workspace A" }],
  projects: [],
  assistants: [],
  releases: [],
};

describe("UsageDetailPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.get.mockImplementation((url: string) => {
      if (url.startsWith("/usage/filter-options")) {
        return Promise.resolve({ status: 200, data: { code: 0, data: filterOptions } });
      }
      return Promise.resolve({ status: 200, data: { code: 0, data: { items: [detail], total: 1 } } });
    });
  });

  it("builds governed attribution filters", () => {
    const params = buildUsageDetailParams({
      tenant_id: " tenant-a ", user_id: "user-1", key_id: "", chat_id: "chat-1",
      model: "qwen-max", scenario: "chat",
    }, true);

    expect(params.get("scope")).toBe("all");
    expect(params.get("tenant_id")).toBe("tenant-a");
    expect(params.get("chat_id")).toBe("chat-1");
    expect(params.get("key_id")).toBeNull();
  });

  it("loads and filters request-level cost attribution", async () => {
    render(<UsageDetailPanel />);

    // "qwen-max" now appears twice: the model dropdown option and the row.
    expect(await screen.findAllByText("qwen-max")).not.toHaveLength(0);
    expect(api.get).toHaveBeenCalledWith("/usage/details?page=1&page_size=20&scope=all");

    // Filters are dropdowns fed by /usage/filter-options, not free-text boxes.
    const chatFilter = screen.getByLabelText("usage.filter_chat_id");
    // The chat dimension is a searchable combobox, not a plain select.
    expect(chatFilter.tagName).toBe("BUTTON");
    fireEvent.click(chatFilter);
    fireEvent.change(screen.getByLabelText("usage.filter_search_placeholder"), {
      target: { value: "policy" },
    });
    await waitFor(() =>
      expect(api.get).toHaveBeenCalledWith("/usage/filter-options?search=policy"),
    );
    fireEvent.click(await screen.findByText("Policy assistant"));

    await waitFor(() => expect(api.get).toHaveBeenLastCalledWith(
      "/usage/details?page=1&page_size=20&scope=all&chat_id=chat-9",
    ));
  });
});
