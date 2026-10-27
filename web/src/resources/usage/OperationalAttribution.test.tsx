import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { buildOperationalAttributionParams, OperationalAttributionPanel } from "./OperationalAttribution";

const api = vi.hoisted(() => ({
  get: vi.fn(),
}));

vi.mock("../../lib/api", () => ({
  ApiError: class ApiError extends Error {},
  api,
  unwrap: async <T,>(request: Promise<{ data: { data: T } }>) => (await request).data.data,
}));

vi.mock("ra-core", () => ({
  useCanAccess: () => ({ data: true }),
  useTranslate: () => (key: string) => key,
}));

const row = {
  tenant_id: "tenant-1", project_id: "project-1234567890", assistant_id: "assistant-1",
  assistant_release_id: "release-1", scenario: "chat", requests: 2, failed: 1,
  no_answer: 1, tokens_in: 60, tokens_out: 40, estimated_cost: 0.016,
  avg_latency_ms: 120, quota_consumed_tokens: 80,
};

describe("OperationalAttributionPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.get.mockResolvedValue({ status: 200, data: { code: 0, data: [row] } });
  });

  it("builds governed attribution filters", () => {
    const params = buildOperationalAttributionParams({
      project_id: " project-a ", assistant_id: "assistant-a",
      assistant_release_id: "", scenario: "", tenant_id: " tenant-a ",
      date_from: "", date_to: "",
    }, true);

    expect(params.get("project_id")).toBe("project-a");
    expect(params.get("assistant_id")).toBe("assistant-a");
    expect(params.get("tenant_id")).toBe("tenant-a");
  });

  it("loads and filters operational attribution", async () => {
    render(<OperationalAttributionPanel />);

    expect(await screen.findByText("$0.0160")).toBeInTheDocument();
    expect(api.get).toHaveBeenCalledWith("/usage/attribution?");

    fireEvent.change(screen.getByLabelText("usage.filter_project_id"), { target: { value: "project-b" } });
    await waitFor(() => expect(api.get).toHaveBeenLastCalledWith("/usage/attribution?project_id=project-b"));
  });
});
