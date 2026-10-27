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

vi.mock("ra-core", () => ({
  useCanAccess: () => ({ data: true }),
  useTranslate: () => (key: string) => key,
}));

const detail = {
  id: "detail-1", request_id: "req-1234567890", tenant_id: "tenant-1",
  user_id: "user-1", key_id: "key-1", model: "qwen-max", scenario: "chat",
  chat_id: "chat-1", session_id: "session-1", dataset_ids: "",
  tokens_in: 20, tokens_out: 10, estimated_cost: 0.006, created_at: "2026-09-16T00:00:00Z",
};

describe("UsageDetailPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.get.mockResolvedValue({ status: 200, data: { code: 0, data: { items: [detail], total: 1 } } });
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

    expect(await screen.findByText("qwen-max")).toBeInTheDocument();
    expect(api.get).toHaveBeenCalledWith("/usage/details?page=1&page_size=20&scope=all");

    fireEvent.change(screen.getByPlaceholderText("usage.filter_chat_id"), { target: { value: "chat-9" } });
    await waitFor(() => expect(api.get).toHaveBeenLastCalledWith(
      "/usage/details?page=1&page_size=20&scope=all&chat_id=chat-9",
    ));
  });
});
