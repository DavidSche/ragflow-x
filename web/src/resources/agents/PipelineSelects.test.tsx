// The data-pipeline property panel used to hand-type engine ids: a Parser's
// `vlm.llm_id`, an Extractor's `llm_id` and a Compiler's template id were free
// text (or absent), which is how a canvas ends up naming a model that was never
// configured. These specs pin the pickers and their unavailable-catalog path.
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { tZh } from "../../test/translate";
import { PipelineOperatorForm } from "./PipelineForms";
import { PipelineCatalogSelect, PipelineModelSelect } from "./PipelineSelects";
import { resetAgentAuthoringOptions } from "./agentAuthoringOptions";
import { DATAFLOW_PARSER_FORM, cloneSeedForm, transformParserParams } from "./dslUtils";

const mocks = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { get: mocks.get } };
});

vi.mock("ra-core", () => ({ useTranslate: () => tZh }));

const VISION_MODEL = {
  model_id: "vm1",
  name: "qwen-vl-max",
  provider_name: "GPUStack",
  instance_name: "gpustack-local",
};

const TEMPLATE = { id: "tree", name: "Tree（树形）", description: "tree shaped" };
const GROUP = { id: "grp-1", name: "通用分组" };

function stub(payload: Record<string, unknown>) {
  mocks.get.mockResolvedValue({ data: { code: 0, data: payload } });
}

const FULL_CATALOG = {
  models: { chat: [], rerank: [], defaults: [] },
  datasets: [],
  memories: [],
  vision_models: [VISION_MODEL],
  compilation_templates: [TEMPLATE],
  compilation_template_groups: [GROUP],
};

beforeEach(() => {
  mocks.get.mockReset();
  resetAgentAuthoringOptions();
});

describe("PipelineModelSelect", () => {
  it("offers the vision catalog and stores the engine composite id", async () => {
    stub(FULL_CATALOG);
    const onChange = vi.fn();
    render(<PipelineModelSelect name="vlm.llm_id" kind="vision" allowEmpty value="" onChange={onChange} />);

    await waitFor(() => expect(screen.getByTestId("pipeline-model-select-vlm.llm_id")).toBeTruthy());
    fireEvent.change(screen.getByTestId("pipeline-model-select-vlm.llm_id"), {
      target: { value: "qwen-vl-max@gpustack-local@GPUStack" },
    });
    expect(onChange).toHaveBeenCalledWith("qwen-vl-max@gpustack-local@GPUStack");
  });

  // 151 answered `GET /models?type=image2text` with an empty list: the panel must
  // say so and still accept an id instead of rendering an empty dropdown.
  it("falls back to manual entry when the workspace has no vision model", async () => {
    stub({ ...FULL_CATALOG, vision_models: [] });
    const onChange = vi.fn();
    render(<PipelineModelSelect name="vlm.llm_id" kind="vision" value="" onChange={onChange} />);

    await waitFor(() => expect(screen.getByTestId("pipeline-model-input-vlm.llm_id")).toBeTruthy());
    expect(screen.getByText(/没有 image2text \/ ocr 模型/)).toBeTruthy();
    fireEvent.change(screen.getByTestId("pipeline-model-input-vlm.llm_id"), {
      target: { value: "custom-vlm@GPUStack" },
    });
    expect(onChange).toHaveBeenCalledWith("custom-vlm@GPUStack");
  });

  it("explains a failed section instead of showing an empty list", async () => {
    stub({ ...FULL_CATALOG, vision_models: [], vision_models_error: "list ragflow vision models failed" });
    render(<PipelineModelSelect name="vlm.llm_id" kind="vision" value="" onChange={vi.fn()} />);
    await waitFor(() => expect(screen.getByText(/options 加载失败|加载失败/)).toBeTruthy());
  });
});

describe("PipelineCatalogSelect", () => {
  it("lists engine compilation templates by name and stores the id", async () => {
    stub(FULL_CATALOG);
    const onChange = vi.fn();
    render(
      <PipelineCatalogSelect
        testId="compiler-template-select"
        section="compilation_templates"
        value=""
        placeholder="选择编译模板"
        emptyHint="没有模板"
        onChange={onChange}
      />,
    );
    await waitFor(() => expect(screen.getByTestId("compiler-template-select")).toBeTruthy());
    fireEvent.change(screen.getByTestId("compiler-template-select"), { target: { value: "tree" } });
    expect(onChange).toHaveBeenCalledWith("tree");
  });

  // A stored id the catalog no longer carries must stay selected: the validator
  // reports the dangling reference, the form must not quietly rewrite it.
  it("keeps a stored id that is not in the catalog", async () => {
    stub(FULL_CATALOG);
    render(
      <PipelineCatalogSelect
        testId="compiler-template-select"
        section="compilation_templates"
        value="removed-template"
        placeholder="选择编译模板"
        emptyHint="没有模板"
        onChange={vi.fn()}
      />,
    );
    await waitFor(() => expect(screen.getByText(/removed-template/)).toBeTruthy());
  });
});

describe("pipeline forms", () => {
  it("writes the compiler template id the engine requires", async () => {
    stub(FULL_CATALOG);
    const onChange = vi.fn();
    render(<PipelineOperatorForm label="Compiler" values={{}} onChange={onChange} />);
    await waitFor(() => expect(screen.getByTestId("compiler-template-select")).toBeTruthy());
    fireEvent.change(screen.getByTestId("compiler-template-select"), { target: { value: "tree" } });
    expect(onChange).toHaveBeenCalledWith("compilation_template_id", "tree");
  });

  // `enabled: true` with `top_n: 0` is rejected by the engine, so switching a
  // group on has to hand over a usable count in the same write.
  it("seeds top_n when an extraction group is switched on", async () => {
    stub(FULL_CATALOG);
    const onChange = vi.fn();
    render(
      <PipelineOperatorForm
        label="Extractor"
        values={{ keywords: { enabled: false, top_n: 0 } }}
        onChange={onChange}
      />,
    );
    await waitFor(() => expect(screen.getByLabelText("抽取关键词")).toBeTruthy());
    fireEvent.click(screen.getByLabelText("抽取关键词"));
    expect(onChange).toHaveBeenCalledWith("keywords", { enabled: true, top_n: 3 });
  });

  it("toggles image OCR through the switch the engine reads", () => {
    const onChange = vi.fn();
    render(<PipelineOperatorForm label="Parser" values={cloneSeedForm(DATAFLOW_PARSER_FORM)} onChange={onChange} />);
    fireEvent.click(screen.getByLabelText("本地 OCR"));
    const [name, setups] = onChange.mock.calls.at(-1) ?? [];
    expect(name).toBe("setups");
    const image = (setups as Record<string, any>[]).find((row) => row.fileFormat === "image");
    expect(image?.ocr_enabled).toBe(false);
  });
});

describe("parser seeds", () => {
  // The engine reads `ocr_enabled` (a legacy setup encodes the same choice in
  // `parse_method`), plus the Parser-level vision switch.
  it("carries the native OCR switch into the engine shape", () => {
    const engineShape = transformParserParams(cloneSeedForm(DATAFLOW_PARSER_FORM)) as Record<string, any>;
    expect(engineShape.setups.image.ocr_enabled).toBe(true);
    expect(engineShape.enable_vision_enhancement).toBe(false);
    expect(engineShape.vlm).toEqual({ llm_id: "" });
  });
});
