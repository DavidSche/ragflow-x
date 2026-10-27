import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SelfMemoryPolicyPanel } from "./SelfMemoryPolicyPanel";

const mocks = vi.hoisted(() => ({
  useCanAccess: vi.fn(),
  useNotify: vi.fn(),
  notify: vi.fn(),
  apiGet: vi.fn(),
}));

vi.mock("ra-core", () => ({
  useCanAccess: mocks.useCanAccess,
  useNotify: mocks.useNotify,
  useTranslate: () => (key: string) => key,
}));

vi.mock("@/lib/api", () => ({
  api: {
    get: mocks.apiGet,
    post: vi.fn(),
  },
  ApiError: class extends Error {
    displayMessage = "request failed";
  },
  ApiEnvelope: class {},
  unwrap: (promise: Promise<{ data: { code: number; data: unknown } }>) =>
    promise.then((response) => response.data.data),
}));

describe("SelfMemoryPolicyPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.useCanAccess.mockReturnValue({ canAccess: true });
    mocks.useNotify.mockReturnValue(mocks.notify);
    mocks.apiGet.mockImplementation((url: string) => {
      if (url === "/self/memory-policies") {
        return Promise.resolve({
          data: {
            code: 0,
            data: {
              items: [{
                id: "policy-1",
                assistant_id: "assistant-1",
                state: "active",
                policy_generation: 1,
                consent_version: 1,
                updated_at: "2026-01-01T00:00:00Z",
              }],
              total: 1,
            },
          },
        });
      }
      return Promise.resolve({
        data: {
          code: 0,
          data: {
            run_id: "run-1",
            gate_status: "PASS",
            pass: true,
            metrics: { pair_count: 20, hit_lift_pp: 85 },
            evidence_url: "/api/v1/evidence-bundles?evaluation_run_id=run-1",
          },
        },
      });
    });
  });

  it("renders the policy quality gate and evidence link", async () => {
    render(<SelfMemoryPolicyPanel />);

    expect(await screen.findByText("PASS")).toBeInTheDocument();
    expect(screen.getByText((_, element) => element?.textContent === "memoryPolicy.pairs: 20")).toBeInTheDocument();
    expect(screen.getByText((_, element) => element?.textContent === "memoryPolicy.hit_lift: 85pp")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "memoryPolicy.evidence" })).toHaveAttribute(
      "href",
      "/api/v1/evidence-bundles?evaluation_run_id=run-1",
    );
  });
});
