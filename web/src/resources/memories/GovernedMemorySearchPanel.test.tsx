import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { GovernedMemorySearchPanel } from "./GovernedMemorySearchPanel";

const mocks = vi.hoisted(() => ({
  useCanAccess: vi.fn(),
  useNotify: vi.fn(),
  notify: vi.fn(),
  apiGet: vi.fn(),
  apiPost: vi.fn(),
}));

vi.mock("ra-core", () => ({
  useCanAccess: mocks.useCanAccess,
  useNotify: mocks.useNotify,
  useTranslate: () => (key: string) => key,
}));

vi.mock("@/lib/api", () => ({
  api: {
    get: mocks.apiGet,
    post: mocks.apiPost,
  },
  ApiError: class extends Error {
    displayMessage = "request failed";
  },
}));

describe("GovernedMemorySearchPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.useNotify.mockReturnValue(mocks.notify);
    mocks.apiGet.mockImplementation((url: string) => {
      if (url.includes("/sessions")) {
        return Promise.resolve({ data: { code: 0, data: { items: [{ id: "session-1", name: "Session 1" }] } } });
      }
      return Promise.resolve({ data: { code: 0, data: { items: [{ id: "assistant-1", title: "Assistant", status: "active" }] } } });
    });
  });

  it("hides governed retrieval without memory execute", async () => {
    mocks.useCanAccess.mockReturnValue({ canAccess: false });
    render(<GovernedMemorySearchPanel memoryId="memory-1" />);

    expect(screen.queryByRole("button", { name: "memories.governed_run" })).not.toBeInTheDocument();
    expect(mocks.apiGet).not.toHaveBeenCalled();
    expect(mocks.apiPost).not.toHaveBeenCalled();
  });

  it("posts immutable request fields and renders decision", async () => {
    const user = userEvent.setup();
    mocks.useCanAccess.mockReturnValue({ canAccess: true });
    mocks.apiPost.mockResolvedValue({
      data: {
        code: 0,
        data: {
          decision: { id: "decision-1", decision: "memory_allowed", memory_policy_id: "policy-1", request_id: "request-1" },
          messages: [{ message_id: 1, user_input: "preferred deployment", agent_response: "on-premise" }],
        },
      },
    });
    render(<GovernedMemorySearchPanel memoryId="memory-1" />);

    await screen.findByRole("option", { name: "Assistant" });
    await user.selectOptions(screen.getByRole("combobox", { name: "memories.governed_agent" }), "assistant-1");
    await user.selectOptions(screen.getByRole("combobox", { name: "memories.governed_session" }), "session-1");
    await user.type(screen.getByRole("textbox"), "deployment");
    await user.click(screen.getByRole("button", { name: "memories.governed_run" }));

    await waitFor(() => expect(mocks.apiPost).toHaveBeenCalledWith(
      "/memories/memory-1/governed-search",
      expect.objectContaining({ assistant_id: "assistant-1", session_id: "session-1", query: "deployment" }),
    ));
    expect(await screen.findByText(/policy-1/)).toBeInTheDocument();
    expect(screen.getByText(/preferred deployment/)).toBeInTheDocument();
    expect(mocks.notify).toHaveBeenCalledWith("memories.governed_done", { type: "success" });
  });
});
