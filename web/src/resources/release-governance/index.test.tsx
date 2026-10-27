import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ReleaseGovernanceBoard } from "./index";

const mocks = vi.hoisted(() => ({
  api: { get: vi.fn(), post: vi.fn() },
  notify: vi.fn(),
}));

vi.mock("../../lib/api", () => ({
  api: mocks.api,
  unwrap: (promise: Promise<{ status: number; data: { code: number; data: unknown } }>) =>
    promise.then((response) => {
      if (response.data.code !== 0) throw new Error(response.data.code.toString());
      return response.data.data;
    }),
}));

vi.mock("ra-core", async (importOriginal) => ({
  ...(await importOriginal<typeof import("ra-core")>()),
  useNotify: () => mocks.notify,
  useTranslate: () => (key: string) => key,
}));

const page = (items: unknown[]) => ({ status: 200, data: { code: 0, data: { items, total: items.length } } });

const releaseGateStates = () => Object.fromEntries([
  "compilation_contract", "memory_contract", "identity", "migration", "permission",
  "improvement", "privacy", "cost", "conversation", "experience", "operations",
].map((name) => [name, "PASS"]));

describe("ReleaseGovernanceBoard", () => {
  beforeEach(() => {
    mocks.api.get.mockReset();
    mocks.api.post.mockReset();
    mocks.notify.mockReset();
  });

  it("loads and renders the governance chain", async () => {
    const candidate = { candidate_id: "cand", candidate_version: 1, target_type: "assistant", target_id: "chat-1", target_version: "v1", base_version: "", change_summary: "Update policy", candidate_hash: "hash", status: "DRAFT" };
    const snapshot = { id: "snap", release_candidate_id: "cand", candidate_version: 1, snapshot_schema_version: "v1", snapshot_hash: "hash", execution_config: "{}" };
    const run = { id: "run", release_candidate_id: "cand", candidate_version: 1, status: "COMPLETED", eval_set_id: "set", eval_set_version: 1, eval_set_hash: "hash", pass: true };
    const evidence = { id: "evidence", release_candidate_id: "cand", candidate_version: 1, evaluation_run_id: "run", snapshot_id: "snap", snapshot_hash: "hash", model_route_pin_id: "pin", model_route_pin_version: 1, enterprise_connection_id: "connection", enterprise_connection_version: 12, enterprise_connection_config_hash: "conn-hash", enterprise_binding_id: "binding", enterprise_binding_version: 4, enterprise_model_ref: "gpt-4o-mini", credential_version: "v7" };
    const gate = { id: "gate", release_candidate_id: "cand", candidate_version: 1, evidence_bundle_id: "evidence", environment: "pilot", decision: "PASS", reason: "", active_gate: true, sub_gate_states: JSON.stringify(releaseGateStates()) };
    const release = { id: "release", release_candidate_id: "cand", candidate_version: 1, snapshot_id: "snap", gate_decision_id: "gate", environment: "pilot", status: "RELEASED", rollback_baseline: "", model_route_pin_id: "pin", model_route_pin_version: 1, enterprise_connection_id: "connection", enterprise_connection_version: 12, enterprise_connection_config_hash: "conn-hash", enterprise_binding_id: "binding", enterprise_binding_version: 4, enterprise_model_ref: "gpt-4o-mini", credential_version: "v7" };
    const issue = { id: "issue", source: "evaluation", source_id: "case", title: "Bad answer", owner: "owner", status: "OPEN", resolution: "", eval_case_id: "case", evaluation_run_id: "run" };

    mocks.api.get.mockImplementation((url: string) => {
      if (url.startsWith("/release-candidates")) return Promise.resolve(page([candidate]));
      if (url.startsWith("/execution-snapshots")) return Promise.resolve(page([snapshot]));
      if (url.startsWith("/evaluation-runs")) return Promise.resolve(page([run]));
      if (url.startsWith("/evidence-bundles")) return Promise.resolve(page([evidence]));
      if (url.startsWith("/release-gates/evidence")) return Promise.resolve({ status: 200, data: { code: 0, data: { candidate_id: "cand", candidate_version: 1, overall_state: "WAIT", advisory_only: true, items: [] } } });
      if (url.startsWith("/release-gates")) return Promise.resolve(page([gate]));
      if (url.startsWith("/releases")) return Promise.resolve(page([release]));
      if (url.startsWith("/quality-issues")) return Promise.resolve(page([issue]));
      return Promise.reject(new Error(`unexpected GET ${url}`));
    });

    render(<ReleaseGovernanceBoard />);
    expect(screen.getByTestId("release-governance-loading")).toBeInTheDocument();
    expect(await screen.findByText("Update policy")).toBeInTheDocument();
    await waitFor(() => expect(mocks.api.get).toHaveBeenCalledWith(expect.stringContaining("/evaluation-runs?")));
    expect(mocks.api.get).toHaveBeenCalledWith(expect.stringContaining("/release-gates?"));
  });

  it("renders the advisory release gate evidence report without creating a gate", async () => {
    const candidate = { candidate_id: "cand", candidate_version: 1, target_type: "assistant", target_id: "assistant", target_version: "v1", base_version: "", change_summary: "", candidate_hash: "hash", status: "EVALUATED", created_at: "" };
    const report = {
      candidate_id: "cand", candidate_version: 1, overall_state: "WAIT", advisory_only: true,
      items: [{ gate: "compilation_contract", state: "NOT_DERIVED", reason: "release dataset bindings are unavailable" }],
    };

    mocks.api.get.mockImplementation((url: string) => {
      if (url.startsWith("/release-candidates")) return Promise.resolve(page([candidate]));
      if (url.startsWith("/execution-snapshots")) return Promise.resolve(page([]));
      if (url.startsWith("/evaluation-runs")) return Promise.resolve(page([]));
      if (url.startsWith("/evidence-bundles")) return Promise.resolve(page([]));
      if (url.startsWith("/release-gates/evidence")) return Promise.resolve({ status: 200, data: { code: 0, data: report } });
      if (url.startsWith("/release-gates")) return Promise.resolve(page([]));
      if (url.startsWith("/releases")) return Promise.resolve(page([]));
      if (url.startsWith("/quality-issues")) return Promise.resolve(page([]));
      return Promise.reject(new Error(`unexpected GET ${url}`));
    });

    render(<ReleaseGovernanceBoard />);
    await userEvent.setup().click(await screen.findByRole("tab", { name: "release_governance.tab_evidence" }));
    expect(await screen.findByTestId("release-gate-evidence-report")).toBeInTheDocument();
    expect(screen.getByText("release_governance.evidence_report_advisory")).toBeInTheDocument();
    expect(screen.getByText("compilation_contract")).toBeInTheDocument();
    expect(screen.getByText("NOT_DERIVED")).toBeInTheDocument();
    expect(mocks.api.get).toHaveBeenCalledWith(expect.stringContaining("/release-gates/evidence?candidate_id=cand&candidate_version=1"));
    expect(mocks.api.post).not.toHaveBeenCalled();
  });

  it("renders every named release governance gate", async () => {
    const candidate = { candidate_id: "cand", candidate_version: 1, target_type: "assistant", target_id: "assistant", target_version: "v1", base_version: "", change_summary: "", candidate_hash: "hash", status: "READY", created_at: "" };
    const gate = { id: "gate", release_candidate_id: "cand", candidate_version: 1, evidence_bundle_id: "evidence", environment: "pilot", decision: "PASS", reason: "", active_gate: true, sub_gate_states: JSON.stringify(releaseGateStates()) };
    mocks.api.get.mockImplementation((url: string) => {
      if (url.startsWith("/release-candidates")) return Promise.resolve(page([candidate]));
      if (url.startsWith("/execution-snapshots")) return Promise.resolve(page([]));
      if (url.startsWith("/evaluation-runs")) return Promise.resolve(page([]));
      if (url.startsWith("/evidence-bundles")) return Promise.resolve(page([]));
      if (url.startsWith("/release-gates/evidence")) return Promise.resolve({ status: 200, data: { code: 0, data: { candidate_id: "cand", candidate_version: 1, overall_state: "WAIT", advisory_only: true, items: [] } } });
      if (url.startsWith("/release-gates")) return Promise.resolve(page([gate]));
      return Promise.resolve(page([]));
    });
    render(<ReleaseGovernanceBoard />);
    await userEvent.setup().click(await screen.findByRole("tab", { name: "release_governance.tab_evidence" }));
    await screen.findByText("gate");
    for (const name of ["compilation_contract", "memory_contract", "improvement", "cost", "operations"]) {
      expect(screen.getByText(name)).toBeInTheDocument();
    }
    expect(screen.getAllByText("PASS")).toHaveLength(12);
  });

  it("fails closed when release gate subgate states are absent", async () => {
    const candidate = { candidate_id: "cand", candidate_version: 1, target_type: "assistant", target_id: "assistant", target_version: "v1", base_version: "", change_summary: "", candidate_hash: "hash", status: "READY", created_at: "" };
    const gate = { id: "gate", release_candidate_id: "cand", candidate_version: 1, evidence_bundle_id: "evidence", environment: "pilot", decision: "PASS", reason: "", active_gate: true, sub_gate_states: "" };
    mocks.api.get.mockImplementation((url: string) => {
      if (url.startsWith("/release-candidates")) return Promise.resolve(page([candidate]));
      if (url.startsWith("/execution-snapshots")) return Promise.resolve(page([]));
      if (url.startsWith("/evaluation-runs")) return Promise.resolve(page([]));
      if (url.startsWith("/evidence-bundles")) return Promise.resolve(page([]));
      if (url.startsWith("/release-gates/evidence")) return Promise.resolve({ status: 200, data: { code: 0, data: { candidate_id: "cand", candidate_version: 1, overall_state: "WAIT", advisory_only: true, items: [] } } });
      if (url.startsWith("/release-gates")) return Promise.resolve(page([gate]));
      return Promise.resolve(page([]));
    });
    render(<ReleaseGovernanceBoard />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("tab", { name: "release_governance.tab_evidence" }));
    expect((await screen.findAllByText("MISSING")).length).toBe(11);
    expect(screen.queryAllByText("PASS")).toHaveLength(1);
  });

  it("renders invalid release gate subgate states as invalid", async () => {
    const candidate = { candidate_id: "cand", candidate_version: 1, target_type: "assistant", target_id: "assistant", target_version: "v1", base_version: "", change_summary: "", candidate_hash: "hash", status: "READY", created_at: "" };
    const gate = { id: "gate", release_candidate_id: "cand", candidate_version: 1, evidence_bundle_id: "evidence", environment: "pilot", decision: "PASS", reason: "", active_gate: true, sub_gate_states: "not-json" };
    mocks.api.get.mockImplementation((url: string) => {
      if (url.startsWith("/release-candidates")) return Promise.resolve(page([candidate]));
      if (url.startsWith("/execution-snapshots")) return Promise.resolve(page([]));
      if (url.startsWith("/evaluation-runs")) return Promise.resolve(page([]));
      if (url.startsWith("/evidence-bundles")) return Promise.resolve(page([]));
      if (url.startsWith("/release-gates/evidence")) return Promise.resolve({ status: 200, data: { code: 0, data: { candidate_id: "cand", candidate_version: 1, overall_state: "WAIT", advisory_only: true, items: [] } } });
      if (url.startsWith("/release-gates")) return Promise.resolve(page([gate]));
      return Promise.resolve(page([]));
    });
    render(<ReleaseGovernanceBoard />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("tab", { name: "release_governance.tab_evidence" }));
    expect((await screen.findAllByText("INVALID")).length).toBe(11);
    expect(screen.getByText("compilation_contract")).toBeInTheDocument();
  });

  it("renders enterprise pin evidence for evidence bundles and releases", async () => {
    const evidence = {
      id: "evidence", release_candidate_id: "cand", candidate_version: 1, evaluation_run_id: "run",
      snapshot_id: "snap", snapshot_hash: "hash", model_route_pin_id: "pin", model_route_pin_version: 1,
      enterprise_connection_id: "connection", enterprise_connection_version: 12,
      enterprise_connection_config_hash: "conn-hash", enterprise_binding_id: "binding",
      enterprise_binding_version: 4, enterprise_model_ref: "gpt-4o-mini", credential_version: "v7",
    };
    const release = { ...evidence, id: "release", snapshot_id: "snap", gate_decision_id: "gate", environment: "pilot", status: "RELEASED", rollback_baseline: "" };
    mocks.api.get.mockImplementation((url: string) => {
      if (url.startsWith("/release-candidates")) return Promise.resolve(page([]));
      if (url.startsWith("/evaluation-runs")) return Promise.resolve(page([]));
      if (url.startsWith("/release-gates/evidence")) return Promise.resolve({ status: 200, data: { code: 0, data: { candidate_id: "cand", candidate_version: 1, overall_state: "WAIT", advisory_only: true, items: [] } } });
      if (url.startsWith("/release-gates")) return Promise.resolve(page([]));
      if (url.startsWith("/evidence-bundles")) return Promise.resolve(page([evidence]));
      if (url.startsWith("/releases")) return Promise.resolve(page([release]));
      if (url.startsWith("/quality-issues")) return Promise.resolve(page([]));
      return Promise.resolve(page([]));
    });

    render(<ReleaseGovernanceBoard />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("tab", { name: "release_governance.tab_evidence" }));
    expect(await screen.findByText("conn-hash")).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "release_governance.tab_releases" }));
    expect(await screen.findByText("conn-hash")).toBeInTheDocument();
  });

  it("marks a draft candidate ready and reloads", async () => {
    let status = "DRAFT";
    const candidate = () => ({ candidate_id: "cand", candidate_version: 1, target_type: "assistant", target_id: "chat-1", target_version: "v1", base_version: "", change_summary: "Update policy", candidate_hash: "hash", status });
    mocks.api.get.mockImplementation((url: string) => {
      if (url.startsWith("/release-candidates")) return Promise.resolve(page([candidate()]));
      return Promise.resolve(page([]));
    });
    mocks.api.post.mockResolvedValue({ status: 200, data: { code: 0, data: {} } });

    render(<ReleaseGovernanceBoard />);
    const button = await screen.findByRole("button", { name: "release_governance.mark_ready" });
    await waitFor(() => expect(button).toBeEnabled());
    fireEvent.click(button);
    await waitFor(() => expect(mocks.api.post).toHaveBeenCalledWith("/release-candidates/cand/ready", {}));
    await waitFor(() => expect(mocks.notify).toHaveBeenCalledWith("release_governance.candidate_ready", { type: "success" }));
  });

  it("submits strict candidate-scoped release evidence contracts", async () => {
    const candidate = { candidate_id: "cand", candidate_version: 1, target_type: "assistant", target_id: "assistant", target_version: "v1", base_version: "", change_summary: "", candidate_hash: "hash", status: "EVALUATED", created_at: "" };
    const run = { id: "run", release_candidate_id: "cand", candidate_version: 1, status: "COMPLETED", eval_set_id: "set", eval_set_version: 1, eval_set_hash: "hash", pass: true };
    mocks.api.get.mockImplementation((url: string) => {
      if (url.startsWith("/release-candidates")) return Promise.resolve(page([candidate]));
      if (url.startsWith("/evaluation-runs")) return Promise.resolve(page([run]));
      if (url.startsWith("/release-gates/evidence")) return Promise.resolve({ status: 200, data: { code: 0, data: { candidate_id: "cand", candidate_version: 1, overall_state: "WAIT", advisory_only: true, items: [] } } });
      return Promise.resolve(page([]));
    });
    mocks.api.post.mockResolvedValue({ status: 200, data: { code: 0, data: {} } });

    render(<ReleaseGovernanceBoard />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("tab", { name: "release_governance.tab_runs" }));
    await user.click(await screen.findByRole("button", { name: "release_governance.select" }));
    await user.click(await screen.findByRole("tab", { name: "release_governance.tab_evidence" }));
    const createEvidence = await screen.findByRole("button", { name: "release_governance.create_evidence" });
    await waitFor(() => expect(createEvidence).toBeEnabled());
    await user.click(createEvidence);
    await screen.findByRole("dialog");
    const permission = JSON.parse((await screen.findByDisplayValue(/"permission_gate"/) as HTMLTextAreaElement).value);
    const configuration = JSON.parse((await screen.findByDisplayValue(/"experience_gate"/) as HTMLTextAreaElement).value);
    expect(permission.permission_gate).toMatchObject({ candidate_id: "cand", candidate_version: 1, passed: true, resource_matrix: true });
    expect(configuration.experience_gate).toMatchObject({ candidate_id: "cand", candidate_version: 1, passed: true, i18n_zh: true });
    await user.click(screen.getByRole("button", { name: "ra.action.save" }));
    await waitFor(() => expect(mocks.api.post).toHaveBeenCalledWith("/evidence-bundles", expect.objectContaining({
      permission_evidence: expect.objectContaining({ permission_gate: expect.objectContaining({ candidate_id: "cand" }) }),
      configuration_evidence: expect.objectContaining({ experience_gate: expect.objectContaining({ candidate_version: 1 }) }),
    })));
  });

  it("filters scoped evaluations and blocks scoped release gates", async () => {
    const run = {
      id: "strategy-run", status: "COMPLETED", eval_set_id: "strategy", eval_set_version: 1,
      eval_set_hash: "hash", evaluation_scope: "compilation_strategy", x_strategy_id: "strategy-1",
    };
    const evidence = {
      id: "strategy-evidence", evaluation_run_id: "strategy-run", snapshot_id: "", snapshot_hash: "",
      evaluation_scope: "compilation_strategy", x_strategy_id: "strategy-1",
    };
    mocks.api.get.mockImplementation((url: string) => {
      if (url.startsWith("/release-candidates")) return Promise.resolve(page([]));
      if (url.startsWith("/evaluation-runs")) return Promise.resolve(page([run]));
      if (url.startsWith("/evidence-bundles")) return Promise.resolve(page([evidence]));
      return Promise.resolve(page([]));
    });

    render(<ReleaseGovernanceBoard />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("tab", { name: "release_governance.tab_runs" }));
    expect(await screen.findByText("strategy:strategy-1")).toBeInTheDocument();
    await user.click(screen.getByRole("tab", { name: "release_governance.tab_evidence" }));
    expect(await screen.findByText("strategy:strategy-1")).toBeInTheDocument();
    await user.click(screen.getByText("strategy-evidence"));
    expect(screen.getByRole("button", { name: "release_governance.create_gate" })).toBeDisabled();
  });

  it("executes a paired scoped evaluation run", async () => {
    const run = {
      id: "strategy-run", status: "RUNNING", eval_set_id: "strategy", eval_set_version: 1,
      eval_set_hash: "hash", evaluation_scope: "compilation_strategy", x_strategy_id: "strategy-1",
      capability: "page_index", execution_mode: "paired_ab", gate_status: "WAIT", gate_reason: "running",
    };
    mocks.api.get.mockImplementation((url: string) => {
      if (url.startsWith("/release-candidates")) return Promise.resolve(page([]));
      if (url.startsWith("/evaluation-runs")) return Promise.resolve(page([run]));
      return Promise.resolve(page([]));
    });
    mocks.api.post.mockResolvedValue({ status: 200, data: { code: 0, data: {} } });

    render(<ReleaseGovernanceBoard />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("tab", { name: "release_governance.tab_runs" }));
    await user.click(await screen.findByRole("button", { name: "release_governance.select" }));
    await user.click(screen.getByRole("button", { name: "release_governance.execute_run" }));
    await waitFor(() => expect(mocks.api.post).toHaveBeenCalledWith("/evaluation-runs/strategy-run/execute", {}));
    await waitFor(() => expect(mocks.notify).toHaveBeenCalledWith("release_governance.run_executed", { type: "success" }));
  });
});
