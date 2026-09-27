import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { KnowledgeHealthCards } from "./KnowledgeHealthCards";

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
}));

describe("KnowledgeHealthCards", () => {
  it("renders explicit numerators, denominators and risk counters", () => {
    render(<KnowledgeHealthCards summary={{
      dataset_count: 10,
      lifecycle: { current: 8, due: 1, expired: 2, missing_owner: 3, unreviewed: 4 },
      parse: { task_count: 20, done: 19, running: 0, queued: 0, failed: 1, stopped: 0, ready_rate: 0.95 },
      average_quality_score: 87,
      low_quality_count: 2,
      missing_classification: 1,
      citation_missing_count: 5,
      citation_window_days: 30,
    }} />);

    expect(screen.getByText("assetGovernance.knowledge_health_parse_ready_rate")).toBeInTheDocument();
    expect(screen.getByText("95%")).toBeInTheDocument();
    expect(screen.getByText("19 / 20")).toBeInTheDocument();
    expect(screen.getByText("8 / 10")).toBeInTheDocument();
    expect(screen.getByText("assetGovernance.knowledge_health_risk_expired: 2")).toBeInTheDocument();
    expect(screen.getByText("assetGovernance.knowledge_health_risk_parse_failed: 1")).toBeInTheDocument();
  });
});
