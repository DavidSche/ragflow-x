import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { RagflowCapabilityPanel } from "./RagflowCapabilityPanel";

const api = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  notify: vi.fn(),
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
  useNotify: () => api.notify,
  useTranslate: () => (key: string) => key,
}));

const report = {
  provider: "ragflow",
  provider_version: "adapter.v1",
  pinned_version: "0.27.1",
  detected_version: "0.27.1",
  runtime_health: "HEALTHY",
  items: [
    { name: "chat", api_profile: "chat.v1", lifecycle: "GA", status: "VERIFIED", runtime_health: "HEALTHY" },
    { name: "stream", api_profile: "stream.v1", lifecycle: "GA", status: "UNVERIFIED", runtime_health: "UNKNOWN" },
    { name: "memory", api_profile: "memory.v1", lifecycle: "GA", status: "DEPRECATED", runtime_health: "UNAVAILABLE" },
  ],
};

const blockedReport = {
  ...report,
  detected_version: "0.28.0",
  upgrade_decision: "BLOCKED_CONTRACT_DRILL_REQUIRED",
  blocking_capabilities: ["chat"],
  trial_capabilities: ["chat"],
  items: [
    { name: "chat", api_profile: "chat.v1", lifecycle: "GA", status: "COMPATIBLE", runtime_health: "HEALTHY" },
  ],
};

describe("RagflowCapabilityPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.get.mockResolvedValue({ status: 200, data: { code: 0, data: report } });
    api.post.mockResolvedValue({ status: 200, data: { code: 0, data: report } });
  });

  it("renders capability health with intuitive status colors", async () => {
    const { container } = render(<RagflowCapabilityPanel />);

    expect(await screen.findByText("capability_verified")).toHaveClass("text-emerald-700");
    expect(screen.getByText(/system.capability_provider_adapter/)).toBeInTheDocument();
    expect(screen.getByText(/adapter\.v1/)).toBeInTheDocument();
    expect(screen.getAllByText("capability_runtime_healthy")[0]).toHaveClass("text-emerald-700");
    expect(screen.getByText("capability_unverified")).toHaveClass("text-amber-700");
    expect(screen.getByText("capability_runtime_unknown")).toHaveClass("text-amber-700");
    expect(screen.getByText("capability_deprecated")).toHaveClass("text-red-700");
    expect(screen.getByText("capability_runtime_unavailable")).toHaveClass("text-red-700");
    expect(container.querySelector("table")).toBeInTheDocument();
  });

  it("re-verifies the capability contract", async () => {
    render(<RagflowCapabilityPanel />);
    fireEvent.click(await screen.findByRole("button", { name: "system.capabilities_verify" }));

    await waitFor(() => expect(api.post).toHaveBeenCalledWith("/system/capabilities/verify", {}));
    expect(api.notify).toHaveBeenCalledWith("system.capabilities_verified", { type: "success" });
  });

  it("blocks an unproven 0.28 compatibility candidate at the upgrade gate", async () => {
    api.get.mockResolvedValueOnce({ status: 200, data: { code: 0, data: blockedReport } });
    render(<RagflowCapabilityPanel />);

    expect(await screen.findByText("capability_upgrade_blocked_contract_drill")).toBeInTheDocument();
    expect(screen.getByText("capability_compatible")).toBeInTheDocument();
    expect(screen.getByText(/system.capability_upgrade_blocking/)).toBeInTheDocument();
  });
});
