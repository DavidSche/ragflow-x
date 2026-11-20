import { describe, expect, it, vi } from "vitest";
import {
  AUTO_FIX_LABELS,
  type AgentDslFixKind,
  fetchAgentValidation,
  fixSummary,
  issueSummary,
  normalizeReport,
  reportLines,
  validateAgentDsl,
} from "./agentValidation";

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
}));

vi.mock("../../lib/api", () => ({
  api: { get: mocks.get, post: mocks.post },
  ApiError: class extends Error {},
}));

const envelope = (data: unknown) => ({ data: { code: 0, data } });

describe("normalizeReport", () => {
  // The editor renders three lists; an older backend (or a stripped payload)
  // must read as "nothing to report" instead of handing the renderer undefined.
  it("fills every list for a payload that omits them", () => {
    const report = normalizeReport({ ok: true });
    expect(report.errors).toEqual([]);
    expect(report.warnings).toEqual([]);
    expect(report.auto_fixed).toEqual([]);
    expect(report.unchecked).toEqual([]);
  });

  it("keeps the report ok unless the backend says otherwise", () => {
    expect(normalizeReport(undefined).ok).toBe(true);
    expect(normalizeReport({ ok: false }).ok).toBe(false);
  });
});

describe("issue and fix summaries", () => {
  // The author thinks in canvas labels ("Generate"), not component ids.
  it("names the node by its label", () => {
    expect(
      issueSummary({
        code: "llm_model_missing",
        severity: "error",
        node_id: "llm_1",
        node_label: "Generate",
        message: "model node has no model selected",
      }),
    ).toBe("Generate：model node has no model selected");
  });

  it("falls back to the node id, then to the bare message", () => {
    expect(
      issueSummary({ code: "x", severity: "error", node_id: "llm_1", message: "broken" }),
    ).toBe("llm_1：broken");
    expect(issueSummary({ code: "x", severity: "error", message: "broken" })).toBe("broken");
  });

  it("labels every correction kind with a sentence, never a raw kind", () => {
    const kinds: AgentDslFixKind[] = [
      "component_name",
      "node_id",
      "node_link",
      "prompt_param",
      "message_content",
    ];
    for (const kind of kinds) {
      expect(AUTO_FIX_LABELS[kind]).toBeTruthy();
      expect(fixSummary({ kind, node_id: "n1" })).not.toContain(kind);
    }
  });

  it("shows what an auto-correction changed", () => {
    expect(
      fixSummary({
        kind: "prompt_param",
        node_id: "llm_1",
        node_label: "Generate",
        from: "prompt",
        to: "user_prompt",
      }),
    ).toBe("Generate：提示词参数已改为引擎字段名（prompt → user_prompt）");
  });

  it("lists errors before warnings", () => {
    const lines = reportLines({
      ok: false,
      errors: [{ code: "a", severity: "error", node_label: "A", message: "blocking" }],
      warnings: [{ code: "b", severity: "warning", node_label: "B", message: "suspect" }],
    });
    expect(lines).toEqual(["A：blocking", "B：suspect"]);
  });
});

describe("validation calls", () => {
  it("posts the canvas under check, not a saved one", async () => {
    mocks.post.mockResolvedValue(envelope({ ok: false, errors: [{ code: "x" }] }));
    const report = await validateAgentDsl({ components: {} }, "agent_canvas");
    expect(mocks.post).toHaveBeenCalledWith("/agents/validate", {
      dsl: { components: {} },
      canvas_category: "agent_canvas",
    });
    expect(report.ok).toBe(false);
  });

  it("reads the stored canvas from the agent endpoint", async () => {
    mocks.get.mockResolvedValue(envelope({ ok: true, node_count: 4 }));
    const report = await fetchAgentValidation("agent-1");
    expect(mocks.get).toHaveBeenCalledWith("/agents/agent-1/validation");
    expect(report.node_count).toBe(4);
  });

  // A non-zero envelope is a real failure, not an empty report: silently
  // reporting "ok" would let a broken canvas through the publish gate.
  it("rejects a non-zero envelope", async () => {
    mocks.post.mockResolvedValue({ data: { code: 502, data: null } });
    await expect(validateAgentDsl({}, "agent_canvas")).rejects.toThrow(/agent validation failed/);
  });
});
