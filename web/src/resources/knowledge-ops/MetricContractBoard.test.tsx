import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { MetricContractBoard } from "./MetricContractBoard";

const apiGet = vi.fn();

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
}));

vi.mock("@/components/ui/skeleton", () => ({
  Skeleton: () => <div data-testid="skeleton" />,
}));

vi.mock("../../lib/api", () => ({
  ApiError: class ApiError extends Error {},
  api: { get: (...args: unknown[]) => apiGet(...args) },
}));

function item(overrides: Record<string, unknown>) {
  return {
    key: "top1_accuracy",
    label: "Top1 Accuracy",
    group: "routing",
    value: 0.95,
    target: 0.9,
    comparator: "gte",
    format: "percent",
    sample_size: 40,
    evaluation_version: "",
    judge_type: "rule",
    gate_state: "passed",
    updated_at: "2026-09-27T00:00:00Z",
    ...overrides,
  };
}

describe("<MetricContractBoard />", () => {
  beforeEach(() => {
    apiGet.mockReset();
  });

  it("renders contract rows grouped with gate badges", async () => {
    apiGet.mockResolvedValueOnce({ data: { code: 0, data: {
      items: [
        item({ key: "top1_accuracy", group: "routing", value: 0.95, target: 0.9, gate_state: "passed" }),
        item({ key: "no_answer_rate", group: "answer", value: 0.02, target: 0.05, gate_state: "passed" }),
        item({ key: "avg_latency_ms", group: "operations", format: "ms", value: 4200, target: 3000, gate_state: "failed" }),
      ],
      route_gate_state: "passed",
      route_run_name: "contract-run",
      window_days: 30,
    } } });
    render(<MetricContractBoard />);

    await screen.findByText("knowledgeOps.metrics_contract_key_top1_accuracy");
    expect(screen.getByText("95.0%")).toBeInTheDocument();
    expect(screen.getByText("≥90%")).toBeInTheDocument();
    expect(screen.getByText("4200 ms")).toBeInTheDocument();
    expect(screen.getAllByText("failed").length).toBeGreaterThan(0);
    expect(screen.getByText(/contract-run/)).toBeInTheDocument();
    const breachedRow = screen.getByText("knowledgeOps.metrics_contract_key_avg_latency_ms").closest("tr");
    expect(breachedRow?.className).toContain("bg-red-50");
  });

  it("shows the empty placeholder when no metrics exist", async () => {
    apiGet.mockResolvedValueOnce({ data: { code: 0, data: { items: [], route_gate_state: "", window_days: 30 } } });
    render(<MetricContractBoard />);
    await screen.findByText("knowledgeOps.metrics_contract_empty");
  });

  it("marks human calibration metrics with a badge", async () => {
    apiGet.mockResolvedValueOnce({ data: { code: 0, data: {
      items: [item({ key: "no_answer_rate", group: "answer", judge_type: "human_calibration_required" })],
      route_gate_state: "",
      window_days: 30,
    } } });
    render(<MetricContractBoard />);
    await screen.findByText("knowledgeOps.metrics_contract_human_calibration");
  });
});
