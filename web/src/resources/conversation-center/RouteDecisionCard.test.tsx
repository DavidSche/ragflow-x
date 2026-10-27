import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { ConversationRouteDecision } from "./types";
import { RouteDecisionCard } from "./RouteDecisionCard";

const candidate = {
  id: "chat-1",
  kind: "chat" as const,
  target_id: "chat-1",
  name: "Contract Assistant",
  normalized_score: 0.94,
  normalized_margin: 0.3,
  confidence: 0.94,
  confidence_status: "calibrated" as const,
  confidence_margin: 0.3,
  candidate_count: 2,
  has_competitor: true,
  routing_readiness: 1,
  routing_readiness_type: "METADATA_COMPLETENESS",
  agent_flow_readiness: 1,
  pre_execution_risk: "low" as const,
  auto_select_enabled: true,
  catalog_freshness_sec: 0,
  reasons: ["contract"],
};

const decision: ConversationRouteDecision = {
  route_id: "route-1",
  score_status: "calibrated",
  candidate_count: 1,
  requested_mode: "suggest",
  effective_mode: "auto_low_risk",
  selected: candidate,
  candidates: [candidate],
  expires_at: new Date().toISOString(),
  router_version: "v1",
  policy_mode: "auto_low_risk",
  policy_version: "policy-v1",
  rerank_status: "not_applicable",
  route_budget_ms: 100,
  budget_exceeded: false,
  latency_ms: 1,
};

describe("route decision card", () => {
  it("describes the auto countdown without treating match score as a percentage", () => {
    render(
      <RouteDecisionCard
        decision={decision}
        status="suggest"
        autoEligible
        error={null}
        onChoose={() => undefined}
        onDismiss={() => undefined}
        translate={(key) => key}
      />,
    );

    expect(screen.getByText("conversationCenter.route_auto_countdown")).toBeInTheDocument();
    const candidateButton = screen.getByText("Contract Assistant").closest("button");
    expect(candidateButton?.textContent).toContain("conversationCenter.kind_chat · 94 · low");
    expect(candidateButton?.textContent).not.toContain("%");
  });

  it("does not show the auto countdown when execution is not eligible", () => {
    render(
      <RouteDecisionCard
        decision={decision}
        status="suggest"
        error={null}
        onChoose={() => undefined}
        onDismiss={() => undefined}
        translate={(key) => key}
      />,
    );

    expect(screen.queryByText("conversationCenter.route_auto_countdown")).not.toBeInTheDocument();
  });
});
