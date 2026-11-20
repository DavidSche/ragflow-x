import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { tZh } from "../../test/translate";
import { OperatorParamForm } from "./OperatorParamForm";
import { OPERATOR_FORMS, defaultParams, operatorFormFor } from "./operatorForms";
import { resetAgentAuthoringOptions } from "./agentAuthoringOptions";
import type { AgentAuthoringOptions } from "./agentAuthoringOptions";

const mocks = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { get: mocks.get } };
});

vi.mock("ra-core", () => ({ useTranslate: () => tZh }));

const LLM_FIELDS = operatorFormFor("LLM")?.fields ?? [];

const CATALOG: AgentAuthoringOptions = {
  models: {
    chat: [
      { model_id: "m1", name: "deepseek-v4-flash-0731", provider_name: "GPUStack", instance_name: "gpustack-local" },
      { model_id: "m2", name: "glm-5.3-flash", provider_name: "GPUStack" },
    ],
    // The live engine answered `GET /models?type=rerank` with an empty list, so
    // the rerank picker must stay usable without one.
    rerank: [],
    defaults: [
      {
        model_id: "m1",
        model_name: "deepseek-v4-flash-0731",
        model_provider: "GPUStack",
        model_instance: "gpustack-local",
        model_type: "chat",
      },
    ],
  },
  datasets: [
    { name: "安全规程库", ragflow_dataset_id: "ds-1", document_count: 12, source: "linked" },
    { name: "孤儿库", ragflow_dataset_id: "ds-2", source: "engine" },
  ],
  memories: [{ id: "mem-1", name: "对话记忆" }],
  // 151 answered `GET /models?type=image2text` and `type=ocr` with an empty
  // list, so the vision picker must stay usable without one.
  vision_models: [],
  compilation_templates: [{ id: "tree", name: "Tree" }],
  compilation_template_groups: [],
};

function stubOptions(payload: AgentAuthoringOptions | Record<string, unknown> = CATALOG) {
  mocks.get.mockResolvedValue({ data: { code: 0, data: payload } });
}

beforeEach(() => {
  mocks.get.mockReset();
  resetAgentAuthoringOptions();
});

describe("OperatorParamForm model picker", () => {
  it("offers the tenant catalog instead of a free-text llm_id", async () => {
    stubOptions();
    const onChange = vi.fn();
    render(<OperatorParamForm fields={LLM_FIELDS} values={{}} onChange={onChange} />);

    await waitFor(() => expect(screen.getByText("deepseek-v4-flash-0731 · GPUStack/gpustack-local")).toBeTruthy());
    const select = screen.getByTestId("model-select-llm_id") as HTMLSelectElement;
    fireEvent.change(select, { target: { value: "glm-5.3-flash@GPUStack" } });
    // The stored value is the engine composite, never the display label.
    expect(onChange).toHaveBeenCalledWith("llm_id", "glm-5.3-flash@GPUStack");
  });

  it("pre-fills the tenant default model on a fresh node", async () => {
    stubOptions();
    const onChange = vi.fn();
    render(<OperatorParamForm fields={LLM_FIELDS} values={{}} onChange={onChange} />);
    await waitFor(() =>
      expect(onChange).toHaveBeenCalledWith("llm_id", "deepseek-v4-flash-0731@gpustack-local@GPUStack"),
    );
  });

  it("keeps a configured id that is absent from the catalog", async () => {
    stubOptions();
    render(<OperatorParamForm fields={LLM_FIELDS} values={{ llm_id: "1770b9a2a83011f1a1127f02938890be" }} onChange={vi.fn()} />);
    await waitFor(() => {
      const select = screen.getByTestId("model-select-llm_id") as HTMLSelectElement;
      const values = Array.from(select.options).map((o) => o.value);
      // The dangling value stays selectable (and selected) instead of being
      // silently swapped for the tenant default.
      expect(values).toContain("1770b9a2a83011f1a1127f02938890be");
      expect(select.value).toBe("1770b9a2a83011f1a1127f02938890be");
      expect(select.options[0].textContent).toContain("已配置，但不在当前列表中");
    });
  });

  // An unavailable catalog must not lock the panel: the field degrades to the
  // old manual input with the reason spelled out.
  it("falls back to a text input when the catalog request fails", async () => {
    mocks.get.mockRejectedValue(new Error("boom"));
    const onChange = vi.fn();
    render(<OperatorParamForm fields={LLM_FIELDS} values={{}} onChange={onChange} />);
    await waitFor(() => expect(screen.getByTestId("model-input-llm_id")).toBeTruthy());
    fireEvent.change(screen.getByTestId("model-input-llm_id"), { target: { value: "hand-typed@P" } });
    expect(onChange).toHaveBeenCalledWith("llm_id", "hand-typed@P");
  });
});

describe("OperatorParamForm knowledge picker", () => {
  const kbOnly = OPERATOR_FORMS.Knowledge.fields.filter(
    (f) => f.name === "retrieval_from" || f.name === "kb_ids" || f.name === "memory_ids",
  );

  it("renders knowledge bases as checkboxes and stores engine dataset ids", async () => {
    stubOptions();
    const onChange = vi.fn();
    render(<OperatorParamForm fields={kbOnly} values={{ retrieval_from: "dataset" }} onChange={onChange} />);

    await waitFor(() => expect(screen.getByText("安全规程库")).toBeTruthy());
    expect(screen.getByText(/引擎直连库，未建立别名/)).toBeTruthy();
    const boxes = screen.getAllByRole("checkbox") as HTMLInputElement[];
    fireEvent.click(boxes[0]);
    expect(onChange).toHaveBeenCalledWith("kb_ids", ["ds-1"]);
    fireEvent.click(boxes[1]);
    expect(onChange).toHaveBeenCalledWith("kb_ids", ["ds-2"]);
  });

  // retrieval_from switches the whole group: knowledge bases and memories are
  // mutually exclusive in the engine's retrievalArgs.
  it("shows only the picker matching the retrieval source", async () => {
    stubOptions();
    const { rerender } = render(
      <OperatorParamForm fields={kbOnly} values={{ retrieval_from: "dataset" }} onChange={vi.fn()} />,
    );
    await waitFor(() => expect(screen.getByTestId("resource-multi-kb_ids")).toBeTruthy());
    expect(screen.queryByTestId("resource-multi-memory_ids")).toBeNull();

    rerender(<OperatorParamForm fields={kbOnly} values={{ retrieval_from: "memory" }} onChange={vi.fn()} />);
    expect(screen.getByTestId("resource-multi-memory_ids")).toBeTruthy();
    expect(screen.queryByTestId("resource-multi-kb_ids")).toBeNull();
  });

  // Legacy node DSLs omit retrieval_from; the engine treats that as `dataset`,
  // so the knowledge picker must stay visible instead of silently vanishing.
  it("defaults a missing retrieval source to the knowledge base", async () => {
    stubOptions();
    render(<OperatorParamForm fields={kbOnly} values={{}} onChange={vi.fn()} />);
    await waitFor(() => expect(screen.getByTestId("resource-multi-kb_ids")).toBeTruthy());
  });

  // A live tenant can hold dozens of libraries; the picker needs a filter and a
  // no-match hint instead of a 50-row scroll box.
  it("filters a long dataset list by name", async () => {
    const many = Array.from({ length: 12 }, (_, i) => ({
      name: `drill-kb-${i}`,
      ragflow_dataset_id: `ds-${i}`,
      source: "engine" as const,
    }));
    stubOptions({ ...CATALOG, datasets: many });
    render(<OperatorParamForm fields={kbOnly} values={{ retrieval_from: "dataset" }} onChange={vi.fn()} />);

    await waitFor(() => expect(screen.getByTestId("resource-filter-kb_ids")).toBeTruthy());
    fireEvent.change(screen.getByTestId("resource-filter-kb_ids"), { target: { value: "drill-kb-1" } });
    expect(screen.getByText("drill-kb-1")).toBeTruthy();
    expect(screen.queryByText("drill-kb-2")).toBeNull();

    fireEvent.change(screen.getByTestId("resource-filter-kb_ids"), { target: { value: "nothing" } });
    expect(screen.getByText("没有匹配的选项。")).toBeTruthy();
  });

  // A deleted/unlinked library must stay visible so the operator can drop it.
  it("flags a stored id that no longer exists in the picker", async () => {
    stubOptions();
    render(<OperatorParamForm fields={kbOnly} values={{ retrieval_from: "dataset", kb_ids: ["ds-gone"] }} onChange={vi.fn()} />);
    await waitFor(() => expect(screen.getByText(/已失效引用：ds-gone/)).toBeTruthy());
  });
});

describe("OperatorParamForm defaults", () => {
  // Empty multi-value defaults must not be written into a new node: they would
  // replace the engine's own default binding with an empty one.
  it("does not seed empty id arrays", () => {
    const seeded = defaultParams("Knowledge");
    expect("kb_ids" in seeded).toBe(false);
    expect("memory_ids" in seeded).toBe(false);
    expect(seeded.retrieval_from).toBe("dataset");
  });
});
