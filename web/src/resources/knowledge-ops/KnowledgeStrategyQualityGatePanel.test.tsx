import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { KnowledgeStrategyQualityGatePanel } from "./KnowledgeStrategyQualityGatePanel";

const apiGet = vi.fn();
const translate = (key: string) => key;
const notify = vi.fn();

vi.mock("ra-core", () => ({
  useTranslate: () => translate,
  useNotify: () => notify,
  useCanAccess: ({ action }: { action: string }) => ({ canAccess: action === "read" }),
}));

vi.mock("@/lib/api", () => ({
  ApiError: class ApiError extends Error {},
  api: { get: (...args: unknown[]) => apiGet(...args) },
  unwrap: async (response: { data: unknown }) => (response as { data: unknown }).data,
}));

const envelope = <T,>(data: T) => ({ data: { code: 0, data } });

describe("<KnowledgeStrategyQualityGatePanel />", () => {
  beforeEach(() => {
    apiGet.mockReset();
    apiGet.mockImplementation(async (url: string) => {
      if (url === "/knowledge-strategies?page=1&page_size=50") {
        return envelope({
          items: [{ id: "strategy-1", dataset_id: "dataset-1", strategy_type: "page_index", ragflow_capability: "knowledge_compilation", active: true }],
          total: 1,
        });
      }
      if (url === "/knowledge-strategies/strategy-1/quality-gate") {
        return envelope({
          strategy_id: "strategy-1",
          run_id: "run-1",
          run_status: "COMPLETED",
          gate_status: "PASS",
          pass: true,
          capability: "page_index",
          execution_mode: "paired_ab",
          eval_set_id: "set-1",
          eval_set_version: 1,
          metrics: { pair_count: 30, hit_lift_pp: 100 },
          evidence_url: "/api/v1/evidence-bundles?evaluation_scope=compilation_strategy&x_strategy_id=strategy-1&evaluation_run_id=run-1",
        });
      }
      throw new Error(`unexpected url: ${url}`);
    });
  });

  it("shows strategy gate metrics and permission-gated evidence link", async () => {
    render(<KnowledgeStrategyQualityGatePanel />);

    await waitFor(() => expect(apiGet).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.getByText("PASS")).toBeInTheDocument());
    expect(screen.getByText("strategy-1")).toBeInTheDocument();
    expect(screen.getByText(/strategy_quality_pairs/)).toBeInTheDocument();
    expect(screen.getByText(/strategy_quality_pairs/).textContent).toContain("30");
    expect(screen.getByText(/strategy_quality_hit_lift/)).toBeInTheDocument();
    expect(screen.getByText(/strategy_quality_hit_lift/).textContent).toContain("100pp");
    expect(screen.getByRole("link", { name: /strategy_quality_evidence/ })).toHaveAttribute(
      "href",
      expect.stringContaining("/api/v1/evidence-bundles?"),
    );
  });
});
