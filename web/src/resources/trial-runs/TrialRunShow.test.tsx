import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { TrialRunShow } from "./TrialRunShow";

const access = vi.hoisted(() => ({ map: {} as Record<string, boolean | undefined> }));
const client = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string, vars?: Record<string, unknown>) =>
    vars ? `${key}:${JSON.stringify(vars)}` : key,
  useCreatePath: () => ({ resource, type }: { resource: string; type: string }) => `/${resource}/${type}`,
  useNavigate: () => () => undefined,
  useParams: () => ({ id: "run-1" }),
  useCanAccess: ({ resource, action }: { resource: string; action: string }) => ({
    canAccess: access.map[`${resource}:${action}`],
    isPending: false,
  }),
}));

vi.mock("./types", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./types")>();
  return { ...actual, getTrialRun: client.get };
});

const runningRun = {
  id: "run-1",
  scenario_template_id: "tpl-1",
  template_version: 1,
  assistant_id: "chat-1",
  assistant_kind: "chat",
  eval_set_id: "es-1",
  eval_set_version: 1,
  eval_set_hash: "hash",
  policy_id: "pol-1",
  policy_hash: "polhash",
  mode: "retrieval",
  status: "RUNNING" as const,
  idempotency_key: "wizard-chat-1-1",
  config_snapshot: {
    assistant_id: "chat-1",
    assistant_name: "Support bot",
    assistant_kind: "chat",
    dataset_ids: ["ds-1"],
    language: "zh",
    llm_id: "llm-1",
    rerank_id: "",
    top_n: 5,
    top_k: 20,
    similarity_threshold: 0.2,
    vector_similarity_weight: 0.3,
    template_id: "tpl-1",
    template_key: "support",
    template_name: "Support",
    template_version: 1,
    prompt_preset_id: "preset-1",
    parameter_profile_id: "profile-1",
    mode: "retrieval",
    policy_id: "pol-1",
  },
  summary: {
    cases: 3,
    returned_cases: 2,
    failed_cases: 0,
    hit_at_k: 0.5,
    hit_denominator: 2,
    undefined_cases: 0,
    top_score_avg: 0.8,
    latency_p95_ms: 120,
    top_n: 5,
    mode: "retrieval",
    gate_eligible: false,
  },
  gate_eligible: false,
  cases: [],
  created_at: "2026-10-27T00:00:00Z",
};

function renderShow() {
  return render(
    <MemoryRouter initialEntries={["/trial-runs/run-1/show"]}>
      <Routes>
        <Route path="/trial-runs/:id/show" element={<TrialRunShow />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("TrialRunShow cancel affordance", () => {
  beforeEach(() => {
    access.map = {};
    client.get.mockReset();
    client.get.mockResolvedValue(runningRun);
  });

  it("offers cancel to a caller holding trial-run:execute", async () => {
    access.map["trial-run:execute"] = true;
    renderShow();
    expect(await screen.findByText("scenarioTemplates.trial.cancel")).toBeTruthy();
  });

  it("hides cancel for read-only callers instead of offering a guaranteed 403", async () => {
    access.map["trial-run:read"] = true;
    access.map["trial-run:execute"] = false;
    renderShow();
    await screen.findByText("scenarioTemplates.trial.refresh");
    expect(screen.queryByText("scenarioTemplates.trial.cancel")).toBeNull();
  });
});
