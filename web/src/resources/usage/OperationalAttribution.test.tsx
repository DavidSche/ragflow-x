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

// A stable translator: returning a fresh function every render makes the
// panels' `load` callback (it depends on `t`) change identity and re-fetch in a
// loop — a test artefact that production's memoized t does not have.
const translate = (key: string) => key;

vi.mock("ra-core", () => ({
  useCanAccess: () => ({ data: true }),
  useTranslate: () => translate,
}));

const row = {
  tenant_id: "tenant-1", project_id: "project-1234567890", assistant_id: "assistant-1",
  assistant_release_id: "release-1", scenario: "chat", requests: 2, failed: 1,
  no_answer: 1, tokens_in: 60, tokens_out: 40, estimated_cost: 0.016,
  avg_latency_ms: 120, quota_consumed_tokens: 80,
};

const filterOptions = {
  models: [],
  scenarios: [{ value: "chat", label: "chat" }],
  users: [],
  keys: [],
  chats: [],
  tenants: [{ value: "tenant-a", label: "Workspace A" }],
  projects: [{ value: "project-b", label: "Project B" }],
  assistants: [{ value: "assistant-b", label: "Policy helper" }],
  releases: [{ value: "release-b", label: "Policy helper v3" }],
};

describe("OperationalAttributionPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    api.get.mockImplementation((url: string) => {
      if (url === "/usage/filter-options") {
        return Promise.resolve({ status: 200, data: { code: 0, data: filterOptions } });
      }
      return Promise.resolve({ status: 200, data: { code: 0, data: [row] } });
    });
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

    // Attribution dimensions are dropdowns, so the operator picks a named
    // project/assistant/release instead of pasting a 32-char ID.
    expect(screen.getByLabelText("usage.filter_project_id").tagName).toBe("SELECT");
    expect(screen.getByRole("option", { name: "Project B" })).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("usage.filter_project_id"), { target: { value: "project-b" } });
    await waitFor(() => expect(api.get).toHaveBeenLastCalledWith("/usage/attribution?project_id=project-b"));
  });
});
