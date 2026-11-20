import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/api";
import {
  defaultModelOf,
  defaultModelRef,
  EMPTY_AGENT_AUTHORING_OPTIONS,
  loadAgentAuthoringOptions,
  modelLabel,
  modelNameOf,
  modelRef,
  resetAgentAuthoringOptions,
  useAgentAuthoringOptions,
} from "./agentAuthoringOptions";

const mocks = vi.hoisted(() => ({ get: vi.fn() }));

vi.mock("../../lib/api", async () => {
  const actual = await vi.importActual<typeof import("../../lib/api")>("../../lib/api");
  return { ...actual, api: { get: mocks.get } };
});

const payload = (data: Record<string, unknown>) => ({ data: { code: 0, data } });

beforeEach(() => {
  mocks.get.mockReset();
  resetAgentAuthoringOptions();
});

afterEach(() => {
  resetAgentAuthoringOptions();
});

describe("engine model references", () => {
  // The engine parses `llm_id` right-anchored: the last @-segment is the
  // provider, the second-to-last the instance. A model name itself may contain
  // an @ (GGUF quantisation: text-embedding-nomic-embed-text-v1.5@q8_0), so the
  // builder must not split on the left.
  it("keeps a default instance out of the composite", () => {
    expect(modelRef({ name: "deepseek-v3", provider_name: "SiliconFlow", instance_name: "default" })).toBe(
      "deepseek-v3@SiliconFlow",
    );
  });

  it("appends a non-default instance and tolerates @ inside the model name", () => {
    expect(
      modelRef({
        name: "text-embedding-nomic-embed-text-v1.5@q8_0",
        provider_name: "GPUStack",
        instance_name: "gpustack-local",
      }),
    ).toBe("text-embedding-nomic-embed-text-v1.5@q8_0@gpustack-local@GPUStack");
  });

  it("returns the bare name when the catalog carries no provider", () => {
    expect(modelRef({ name: "qwen2.5" })).toBe("qwen2.5");
    expect(modelRef({ provider_name: "GPUStack" })).toBe("");
  });

  // `/models/default` answers the `ModelItem` shape (model_name/provider/instance),
  // not the `/models` `name`/`provider_name` shape.
  it("builds the same composite from a tenant default model", () => {
    const ref = defaultModelRef({
      model_id: "1770b9a2",
      model_name: "deepseek-v4-flash-0731",
      model_provider: "GPUStack",
      model_instance: "gpustack-local",
    });
    expect(ref).toBe("deepseek-v4-flash-0731@gpustack-local@GPUStack");
  });

  it("labels a model with its provider scope", () => {
    expect(modelLabel({ model_id: "a", name: "glm-5.3", provider_name: "GPUStack", instance_name: "local" })).toBe(
      "glm-5.3 · GPUStack/local",
    );
    expect(modelLabel({ model_id: "a", name: "glm-5.3" })).toBe("glm-5.3");
  });

  // The node badge shows the name only: the composite does not fit a 200px card.
  it("strips provider/instance back out for display", () => {
    expect(modelNameOf("glm-5.3-flash@GPUStack")).toBe("glm-5.3-flash");
    expect(modelNameOf("deepseek-v4-flash-0731@gpustack-local@GPUStack")).toBe("deepseek-v4-flash-0731");
    expect(modelNameOf("text-embedding-nomic-embed-text-v1.5@q8_0@local@GPUStack")).toBe(
      "text-embedding-nomic-embed-text-v1.5@q8_0",
    );
    expect(modelNameOf("bare-model")).toBe("bare-model");
    expect(modelNameOf("")).toBe("");
  });

  it("finds the tenant default of one model type only", () => {
    const models = {
      chat: [],
      rerank: [],
      defaults: [
        { model_id: "1", model_name: "chat-a", model_type: "chat" },
        { model_id: "2", model_name: "emb", model_type: "embedding" },
      ],
    };
    expect(defaultModelOf(models, "chat")?.model_name).toBe("chat-a");
    expect(defaultModelOf(models, "rerank")).toBeUndefined();
    expect(defaultModelOf(null, "chat")).toBeUndefined();
  });
});

describe("loadAgentAuthoringOptions", () => {
  it("normalises a full payload and caches it across callers", async () => {
    mocks.get.mockResolvedValue(
      payload({
        models: { chat: [{ model_id: "m1", name: "chat-a", provider_name: "P" }], rerank: [], defaults: [] },
        datasets: [{ name: "库A", ragflow_dataset_id: "ds-1", source: "linked" }],
        memories: [{ id: "mem-1", name: "记忆A" }],
      }),
    );

    const [first, second] = await Promise.all([loadAgentAuthoringOptions(), loadAgentAuthoringOptions()]);
    expect(mocks.get).toHaveBeenCalledTimes(1);
    expect(mocks.get).toHaveBeenCalledWith("/agents/authoring-options");
    expect(first.datasets[0].ragflow_dataset_id).toBe("ds-1");
    expect(first.memories[0].id).toBe("mem-1");
    expect(second).toBe(first);
  });

  // The data-pipeline panel needs three more sections: the vision catalog for
  // `vlm.llm_id`, and the two compilation catalogs for a Compiler node.
  it("normalises the pipeline catalogs and their failure reasons", async () => {
    mocks.get.mockResolvedValueOnce(
      payload({
        vision_models: [{ model_id: "vm1", name: "qwen-vl-max", provider_name: "GPUStack", instance_name: "gpustack-local" }],
        compilation_templates: [{ id: "tree", name: "Tree", description: "tree shaped" }],
        compilation_template_groups: [{ id: "grp-1", name: "通用分组" }],
      }),
    );
    const ok = await loadAgentAuthoringOptions();
    expect(ok.vision_models).toHaveLength(1);
    expect(ok.compilation_templates[0]).toMatchObject({ id: "tree", name: "Tree" });
    expect(ok.compilation_template_groups[0].id).toBe("grp-1");

    // 151 answers `type=image2text` with an empty list: absent keys must become
    // empty arrays (not `undefined`) so the pickers can render their empty state.
    resetAgentAuthoringOptions();
    mocks.get.mockResolvedValueOnce(payload({}));
    const bare = await loadAgentAuthoringOptions();
    expect(bare.vision_models).toEqual([]);
    expect(bare.compilation_templates).toEqual([]);
    expect(bare.compilation_template_groups).toEqual([]);
    expect(bare.vision_models_error).toBeUndefined();
  });

  it("keeps per-section failure reasons instead of failing the whole payload", async () => {
    mocks.get.mockResolvedValue(
      payload({
        models: null,
        models_error: "引擎不可用",
        datasets: [{ name: "库A", ragflow_dataset_id: "ds-1", source: "engine" }],
        memories: [],
        memories_error: "记忆表读取失败",
        vision_models: [],
        vision_models_error: "list ragflow vision models failed",
        compilation_templates: [],
        compilation_templates_error: "list compilation templates failed",
      }),
    );
    const options = await loadAgentAuthoringOptions();
    expect(options.models).toBeNull();
    expect(options.models_error).toBe("引擎不可用");
    expect(options.datasets).toHaveLength(1);
    expect(options.memories_error).toBe("记忆表读取失败");
    expect(options.vision_models_error).toBe("list ragflow vision models failed");
    expect(options.compilation_templates_error).toBe("list compilation templates failed");
  });

  it("rejects a non-zero envelope so the panel falls back to manual entry", async () => {
    mocks.get.mockResolvedValue({ data: { code: 50001, data: null } });
    await expect(loadAgentAuthoringOptions()).rejects.toBeInstanceOf(ApiError);
  });

  it("does not cache a failed request", async () => {
    mocks.get.mockRejectedValueOnce(new ApiError(403, 40301, "forbidden"));
    await expect(loadAgentAuthoringOptions()).rejects.toBeInstanceOf(ApiError);
    mocks.get.mockResolvedValueOnce(payload({ datasets: [], memories: [] }));
    await expect(loadAgentAuthoringOptions()).resolves.toBeTruthy();
    expect(mocks.get).toHaveBeenCalledTimes(2);
  });
});

describe("useAgentAuthoringOptions", () => {
  it("starts loading, then exposes the payload", async () => {
    mocks.get.mockResolvedValue(payload({ datasets: [], memories: [], models: { chat: [], rerank: [] } }));
    const { result } = renderHook(() => useAgentAuthoringOptions());
    expect(result.current.loading).toBe(true);
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBeNull();
    expect(result.current.options.models?.chat).toEqual([]);
  });

  // A 403 (no read/agent) or a network error must not block authoring: fields
  // keep their manual-input fallback, so the hook reports empty lists + error.
  it("degrades to empty lists and reports the error", async () => {
    mocks.get.mockRejectedValue(new ApiError(403, 40301, "forbidden"));
    const { result } = renderHook(() => useAgentAuthoringOptions());
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(result.current.error).toBeInstanceOf(ApiError);
    expect(result.current.options).toEqual(EMPTY_AGENT_AUTHORING_OPTIONS);
  });
});
