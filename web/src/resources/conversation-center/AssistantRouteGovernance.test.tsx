import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AssistantRouteGovernanceButton } from "./AssistantRouteGovernance";

const api = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  notify: vi.fn(),
}));

vi.mock("../../lib/api", () => ({
  ApiError: class ApiError extends Error {
    displayMessage: string;
    constructor(public status: number, public code: number, message: string) {
      super(message);
      this.displayMessage = message;
    }
  },
  api,
}));

vi.mock("ra-core", () => ({
  useCanAccess: () => ({ canAccess: true }),
  useNotify: () => api.notify,
  useTranslate: () => (key: string) => key,
}));

describe("AssistantRouteGovernanceButton", () => {
  beforeEach(() => {
    api.get.mockReset();
    api.put.mockReset();
    api.notify.mockReset();
  });

  it("loads catalog governance and saves the explicit auto-route opt-in", async () => {
    api.get.mockResolvedValueOnce({
      status: 200,
      data: { code: 0, data: { items: [{ id: "chat-1", auto_select_enabled: false, assistant_risk_level: "low", workflow_risk_upper_bound: "low", keywords: ["policy"], examples: ["What is required?"] }] } },
    });
    api.get.mockResolvedValueOnce({ status: 200, data: { code: 0, data: { auto_route_mode: "recommend_only" } } });
    api.get.mockResolvedValueOnce({
      status: 200,
      data: { code: 0, data: { items: [{
        id: "run-1", name: "Latest", source: "manual", gate_state: "passed", allow_auto_low_risk: false,
        created_at: "2026-09-20T00:00:00Z", metrics_json: JSON.stringify({
          case_count: 4, evaluated_count: 4, candidate_recall: 0.5, top1_accuracy: 0.75, top3_recall: 1,
          no_match_rate: 0.25, wrong_route_rate: 0.25, wrong_execution_rate: 0.25,
          auto_execute_count: 1, auto_execute_wrong_rate: 1, abstain_rate: 0.25,
          route_p95_ms: 700, route_cost: 0.003, route_timing_sample_count: 4, gate_state: "failed",
          gate_failures: ["auto_execute_wrong_count_not_zero"],
          candidate_distribution: [{ candidate_key: "chat:assistant-a", rank: 1, count: 2 }],
        }),
      }] } },
    });
    api.put.mockResolvedValueOnce({ status: 200, data: { code: 0 } });
    render(<AssistantRouteGovernanceButton kind="chat" targetId="chat-1" name="Policy" />);
    fireEvent.click(screen.getByRole("button", { name: "conversationCenter.route_governance" }));
    expect(await screen.findByText("conversationCenter.route_governance_title")).toBeInTheDocument();
    expect(await screen.findByText("conversationCenter.route_evaluation_title")).toBeInTheDocument();
    expect(screen.getByText("conversationCenter.route_evaluation_candidate_recall")).toBeInTheDocument();
    expect(screen.getByText("50%")).toBeInTheDocument();
    expect(screen.getByText("700 ms")).toBeInTheDocument();
    expect(screen.getByText("auto_execute_wrong_count_not_zero")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "ra.action.save" }));
    await waitFor(() => expect(api.put).toHaveBeenNthCalledWith(2, "/conversation/routing/policy", {
      auto_route_mode: "recommend_only",
    }));
    expect(api.put).toHaveBeenNthCalledWith(1, "/conversation/assistants/chat/chat-1", expect.objectContaining({
      auto_select_enabled: false,
      assistant_risk_level: "low",
    }));
  });
});
