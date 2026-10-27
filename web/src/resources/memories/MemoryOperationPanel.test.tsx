import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryOperationPanel } from "./MemoryOperationPanel";

const mocks = vi.hoisted(() => ({
  useCanAccess: vi.fn(),
  useGetIdentity: vi.fn(),
  useNotify: vi.fn(),
  notify: vi.fn(),
  apiGet: vi.fn(),
  apiPost: vi.fn(),
}));

vi.mock("ra-core", () => ({
  useCanAccess: mocks.useCanAccess,
  useGetIdentity: mocks.useGetIdentity,
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
  unwrap: async (request: Promise<{ data: { code: number; data: unknown } }>) => {
    const response = await request;
    if (response.data.code !== 0) throw new Error("request failed");
    return response.data.data;
  },
}));

describe("MemoryOperationPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.notify.mockClear();
    mocks.useNotify.mockReturnValue(mocks.notify);
    mocks.useCanAccess.mockImplementation(({ resource }: { resource: string }) => ({
      canAccess: resource === "memory-operation" || resource === "memory-forget",
    }));
    mocks.useGetIdentity.mockReturnValue({ data: { id: "user-1" } });
    mocks.apiGet.mockResolvedValue({
      data: {
        code: 0,
        data: {
          items: [{
            id: "operation-1",
            operation_type: "write",
            state: "accepted",
            policy_id: "policy-1",
            memory_id: "memory-1",
            assistant_id: "assistant-1",
            project_id: "",
            tombstone_active: false,
            user_id: "user-1",
            policy_generation: 1,
            consent_version: 1,
            request_id: "request-1",
            payload_hash: "hmac-sha256:v1:fixture",
            created_at: "2026-01-01T00:00:00Z",
          }],
          total: 1,
        },
      },
    });
  });

  it("renders the owner cancellation action and posts tombstone once", async () => {
    const user = userEvent.setup();
    vi.spyOn(window, "confirm").mockReturnValue(true);
    mocks.apiPost.mockResolvedValue({
      data: {
        code: 0,
        data: {
          id: "operation-1",
          operation_type: "write",
          state: "skipped",
          policy_id: "policy-1",
          memory_id: "memory-1",
          assistant_id: "assistant-1",
          project_id: "",
          tombstone_active: true,
          policy_generation: 1,
          consent_version: 1,
          request_id: "request-1",
          created_at: "2026-01-01T00:00:00Z",
        },
      },
    });
    render(<MemoryOperationPanel memoryId="memory-1" />);

    await screen.findByText("memoryOperation.state_accepted");
    await screen.findByText(/hmac-sha256:v1:fixture/);
    await user.click(screen.getByRole("button", { name: "memoryOperation.tombstone_action operation-1" }));
    await waitFor(() => expect(mocks.apiPost).toHaveBeenCalledWith("/memory-operations/operation-1/tombstone"));
    expect(mocks.notify).toHaveBeenCalledWith("memoryOperation.tombstone_requested", { type: "success" });
  });

  it("hides cancellation action without memory-forget execute", async () => {
    mocks.useCanAccess.mockImplementation(({ resource }: { resource: string }) => ({
      canAccess: resource === "memory-operation",
    }));
    render(<MemoryOperationPanel memoryId="memory-1" />);

    await screen.findByText("memoryOperation.state_accepted");
    expect(screen.queryByRole("button", { name: "memoryOperation.tombstone_action operation-1" })).not.toBeInTheDocument();
    expect(mocks.apiPost).not.toHaveBeenCalled();
  });

  it("hides cancellation action for a governance viewer who is not the operation owner", async () => {
    mocks.useGetIdentity.mockReturnValue({ data: { id: "tenant-admin" } });
    render(<MemoryOperationPanel memoryId="memory-1" />);

    await screen.findByText("memoryOperation.state_accepted");
    expect(screen.queryByRole("button", { name: "memoryOperation.tombstone_action operation-1" })).not.toBeInTheDocument();
    expect(mocks.apiPost).not.toHaveBeenCalled();
  });
});
