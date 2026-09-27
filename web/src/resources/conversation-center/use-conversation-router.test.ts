import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useConversationRouter } from "./use-conversation-router";
import type { ConversationRouteCandidate } from "./types";

const api = vi.hoisted(() => ({
  post: vi.fn(),
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

const candidate: ConversationRouteCandidate = {
  kind: "agent",
  id: "agent-1",
  target_id: "agent-1",
  name: "Approval Workflow",
  normalized_score: 0.8,
  normalized_margin: 0.2,
  confidence: null,
  confidence_status: "not_calibrated",
  candidate_count: 2,
  has_competitor: true,
  routing_readiness: 0.9,
  routing_readiness_type: "METADATA_COMPLETENESS",
  agent_flow_readiness: 0.8,
  pre_execution_risk: "low",
  auto_select_enabled: false,
  catalog_freshness_sec: 0,
  reasons: ["workflow"],
};

const decision = {
  route_id: "route-1",
  score_status: "provisional",
  candidate_count: 1,
  requested_mode: "suggest",
  effective_mode: "suggest",
  selected: null,
  candidates: [candidate],
  expires_at: new Date(Date.now() + 60_000).toISOString(),
  router_version: "v1-m2",
  route_budget_ms: 1500,
  budget_exceeded: false,
  latency_ms: 12,
};

describe("useConversationRouter", () => {
  beforeEach(() => {
    api.post.mockReset();
  });

  it("routes deterministically, reserves selection, and bootstraps a session", async () => {
    api.post.mockResolvedValueOnce({ status: 200, data: { code: 0, data: decision } });
    api.post.mockResolvedValueOnce({ status: 200, data: { code: 0, data: { route_selection_id: "rs-1", state: "NEW" } } });
    api.post.mockResolvedValueOnce({ status: 200, data: { code: 0, data: { bootstrap_state: "SUCCEEDED", session_id: "session-1" } } });
    const { result } = renderHook(() => useConversationRouter({ enabled: true, notify: vi.fn(), translate: (key) => key }));
    await act(async () => {
      await result.current.route("approval problem");
    });
    await waitFor(() => expect(result.current.decision).toEqual(decision));
    await act(async () => {
      await result.current.select(candidate, decision.route_id);
    });
    expect(api.post).toHaveBeenNthCalledWith(1, "/conversation/route", { query: "approval problem", requested_mode: "suggest" }, expect.anything());
    expect(api.post).toHaveBeenNthCalledWith(2, "/conversation/route/route-1/select", { candidate_id: "agent-1", candidate_kind: "agent" }, expect.objectContaining({ headers: expect.anything() }));
    expect(api.post).toHaveBeenNthCalledWith(3, "/conversation/route-selections/rs-1/bootstrap", {}, expect.objectContaining({ headers: expect.anything() }));
  });

  it("returns to a failed state when bootstrap fails", async () => {
    api.post.mockResolvedValueOnce({ status: 200, data: { code: 0, data: decision } });
    api.post.mockResolvedValueOnce({ status: 200, data: { code: 0, data: { route_selection_id: "rs-1", state: "NEW" } } });
    api.post.mockResolvedValueOnce({ status: 200, data: { code: 409, message: "selection consumed" } });
    const { result } = renderHook(() => useConversationRouter({ enabled: true, notify: vi.fn(), translate: (key) => key }));
    await act(async () => {
      await result.current.route("approval problem");
    });
    let operation = null;
    await act(async () => {
      operation = await result.current.select(candidate, decision.route_id);
    });
    expect(operation).toBeNull();
    await waitFor(() => expect(result.current.error).toBe("selection consumed"));
  });

  it("moves an empty decision into clarify instead of an error", async () => {
    api.post.mockResolvedValueOnce({
      status: 200,
      data: { code: 0, data: { ...decision, candidate_count: 0, candidates: [] } },
    });
    const { result } = renderHook(() => useConversationRouter({ enabled: true, notify: vi.fn(), translate: (key) => key }));
    await act(async () => {
      await result.current.route("vague question");
    });
    expect(result.current.status).toBe("clarify");
    expect(result.current.decision?.candidates).toHaveLength(0);
    expect(result.current.error).toBeNull();
  });

  it("auto-selects at most once per route decision even when select fails", async () => {
    const autoDecision = { ...decision, selected: candidate };
    api.post.mockResolvedValueOnce({ status: 200, data: { code: 0, data: autoDecision } });
    api.post.mockRejectedValue(new Error("server error"));
    const onAutoSelected = vi.fn();
    const { result } = renderHook(() =>
      useConversationRouter({ enabled: true, notify: vi.fn(), translate: (key) => key, onAutoSelected }),
    );
    await act(async () => {
      await result.current.route("approval problem");
    });
    expect(result.current.decision).toEqual(autoDecision);
    await waitFor(() => expect(result.current.status).toBe("suggest"));
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 50));
    });
    const selectCalls = api.post.mock.calls.filter(
      ([url]) => typeof url === "string" && url.includes("/select"),
    );
    expect(selectCalls).toHaveLength(1);
    expect(onAutoSelected).not.toHaveBeenCalled();
  });
});
