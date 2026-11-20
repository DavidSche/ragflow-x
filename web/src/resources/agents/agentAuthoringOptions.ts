import { useEffect, useState } from "react";
import { api, ApiError } from "../../lib/api";

/**
 * Canvas inspector picker data (doc: agent property panel).
 *
 * The agent DSL is stored in the RAGFlow engine verbatim, so every node
 * parameter that references a system resource stores an ENGINE id
 * (`llm_id` = `name@[instance@]provider`, `kb_ids` = engine dataset ids,
 * `memory_ids` = engine memory ids). An operator must never have to type those
 * by hand: this module fetches the pickable lists from
 * `GET /api/v1/agents/authoring-options` (read/agent, governance scope) and the
 * property panel renders selects instead of text inputs.
 *
 * The endpoint degrades per section, so a section can be empty with a reason
 * (`models_error` / `datasets_error` / `memories_error`) — the panel then falls
 * back to manual entry for that field only.
 */

/** One model in the tenant catalog (`GET /models` shape). */
export interface AgentModelOption {
  model_id: string;
  name: string;
  model_type?: string[];
  provider_name?: string;
  instance_name?: string;
}

/** One tenant default model (`GET /models/default` shape). */
export interface AgentDefaultModel {
  model_id: string;
  name?: string;
  model_name?: string;
  model_provider?: string;
  model_instance?: string;
  model_type?: string;
}

/** Model catalog + tenant defaults, as returned by the engine. */
export interface AgentModelCatalog {
  chat: AgentModelOption[];
  rerank: AgentModelOption[];
  embedding?: AgentModelOption[];
  defaults?: AgentDefaultModel[];
}

/** One bindable knowledge base (linked alias first, raw engine library last). */
export interface AgentDatasetOption {
  link_id?: string;
  name: string;
  ragflow_dataset_id: string;
  document_count?: number;
  sensitivity?: string;
  source: "linked" | "engine";
}

/** One pickable memory (`retrieval_from=memory`). */
export interface AgentMemoryOption {
  id: string;
  name: string;
}

/** One compilation template a Compiler node may name (`kind` is the engine kind). */
export interface AgentCompilationTemplateOption {
  id: string;
  name: string;
  description?: string;
}

/** One governed compilation template group (alternative to a single template). */
export interface AgentCompilationTemplateGroupOption {
  id: string;
  name: string;
  description?: string;
}

/** Full picker payload; each section carries its own failure reason. */
export interface AgentAuthoringOptions {
  models: AgentModelCatalog | null;
  models_error?: string;
  datasets: AgentDatasetOption[];
  datasets_error?: string;
  memories: AgentMemoryOption[];
  memories_error?: string;
  /** image2text/ocr models — the only ids a Parser `vlm.llm_id` may carry. */
  vision_models: AgentModelOption[];
  vision_models_error?: string;
  compilation_templates: AgentCompilationTemplateOption[];
  compilation_templates_error?: string;
  compilation_template_groups: AgentCompilationTemplateGroupOption[];
  compilation_template_groups_error?: string;
}

/** Empty payload used before the first successful load. */
export const EMPTY_AGENT_AUTHORING_OPTIONS: AgentAuthoringOptions = {
  models: null,
  datasets: [],
  memories: [],
  vision_models: [],
  compilation_templates: [],
  compilation_template_groups: [],
};

/**
 * Build the engine `llm_id` / `rerank_id` composite for a catalog model.
 *
 * The engine's parser (`splitCompositeLLMID`, right-anchored) reads the last
 * `@` segment as the provider and the second-to-last as the instance, so the
 * instance is only appended when it is not the provider default — that keeps
 * the common value readable (`deepseek-v3@SiliconFlow`) while still
 * disambiguating multi-instance providers.
 */
export function modelRef(model: {
  name?: string;
  provider_name?: string;
  instance_name?: string;
}): string {
  const name = (model.name ?? "").trim();
  const provider = (model.provider_name ?? "").trim();
  const instance = (model.instance_name ?? "").trim();
  if (!name) return "";
  if (!provider) return name;
  if (!instance || instance === "default") return `${name}@${provider}`;
  return `${name}@${instance}@${provider}`;
}

/**
 * Readable model name out of a stored `llm_id` / `rerank_id`.
 *
 * Mirrors the engine's right-anchored rule: the last segment is the provider and,
 * with three or more segments, the second-to-last is the instance — everything
 * before that is the model name (which may itself contain `@`). Node badges show
 * this instead of the full composite, which a 200px card can only truncate.
 */
export function modelNameOf(ref: string): string {
  const parts = (ref ?? "").split("@");
  if (parts.length <= 1) return ref ?? "";
  if (parts.length === 2) return parts[0];
  return parts.slice(0, parts.length - 2).join("@");
}

/** Same composite for a tenant default, whose identity keys differ upstream. */
export function defaultModelRef(model: AgentDefaultModel): string {
  return modelRef({
    name: model.model_name ?? model.name,
    provider_name: model.model_provider,
    instance_name: model.model_instance,
  });
}

/** Human label for a catalog model: `name · provider/instance`. */
export function modelLabel(model: AgentModelOption): string {
  const name = model.name ?? model.model_id;
  const scope = [model.provider_name, model.instance_name].filter(Boolean).join("/");
  return scope ? `${name} · ${scope}` : name;
}

/** The tenant default of one model type, if the tenant pinned one. */
export function defaultModelOf(
  models: AgentModelCatalog | null,
  kind: "chat" | "rerank" | "image2text",
): AgentDefaultModel | undefined {
  return (models?.defaults ?? []).find((d) => d.model_type === kind);
}

/** Normalise an unknown thrown value into the shared ApiError type. */
function toApiError(err: unknown): ApiError {
  return err instanceof ApiError ? err : new ApiError(0, 0, String(err));
}

/** Accepts only the documented envelope; anything else is a transport error. */
function unwrap(res: { data?: { code?: number; data?: unknown } }): AgentAuthoringOptions {
  const envelope = res?.data;
  if (!envelope || typeof envelope.code !== "number" || envelope.code !== 0) {
    throw new ApiError(200, envelope?.code ?? 0, "agents_authoring_options_bad_envelope");
  }
  const data = (envelope.data ?? {}) as Partial<AgentAuthoringOptions>;
  return {
    models: data.models ?? null,
    models_error: data.models_error,
    datasets: Array.isArray(data.datasets) ? data.datasets : [],
    datasets_error: data.datasets_error,
    memories: Array.isArray(data.memories) ? data.memories : [],
    memories_error: data.memories_error,
    vision_models: Array.isArray(data.vision_models) ? data.vision_models : [],
    vision_models_error: data.vision_models_error,
    compilation_templates: Array.isArray(data.compilation_templates) ? data.compilation_templates : [],
    compilation_templates_error: data.compilation_templates_error,
    compilation_template_groups: Array.isArray(data.compilation_template_groups)
      ? data.compilation_template_groups
      : [],
    compilation_template_groups_error: data.compilation_template_groups_error,
  };
}

/*
 * Module-level cache: the inspector mounts once per canvas, but every node
 * selection re-renders the same panel — one request per canvas is enough, and
 * re-selecting a node must not refetch. `resetAgentAuthoringOptions` exists for
 * tests and for a manual refresh.
 */
let cachedOptions: AgentAuthoringOptions | null = null;
let inflight: Promise<AgentAuthoringOptions> | null = null;

/** Clear the module cache (tests, and after the tenant/model config changes). */
export function resetAgentAuthoringOptions(): void {
  cachedOptions = null;
  inflight = null;
}

/** Load (and cache) the picker payload; concurrent callers share one request. */
export function loadAgentAuthoringOptions(): Promise<AgentAuthoringOptions> {
  if (cachedOptions) return Promise.resolve(cachedOptions);
  if (!inflight) {
    inflight = api
      .get<{ code: number; data: unknown }>("/agents/authoring-options")
      .then((res) => {
        const options = unwrap(res);
        cachedOptions = options;
        return options;
      })
      .finally(() => {
        inflight = null;
      });
  }
  return inflight;
}

/** State returned by {@link useAgentAuthoringOptions}. */
export interface AgentAuthoringOptionsState {
  options: AgentAuthoringOptions;
  /** True until the first answer (success or failure) arrives. */
  loading: boolean;
  /** Non-null only when the whole request failed (network / 403 / 5xx). */
  error: ApiError | null;
}

/**
 * Subscribe to the picker payload. A whole-request failure keeps every section
 * empty so the panel degrades to manual id entry rather than blocking edits; a
 * per-section failure surfaces through `<section>_error`.
 */
export function useAgentAuthoringOptions(): AgentAuthoringOptionsState {
  const [state, setState] = useState<AgentAuthoringOptionsState>(() =>
    cachedOptions
      ? { options: cachedOptions, loading: false, error: null }
      : { options: EMPTY_AGENT_AUTHORING_OPTIONS, loading: true, error: null },
  );

  useEffect(() => {
    let cancelled = false;
    if (cachedOptions) {
      setState({ options: cachedOptions, loading: false, error: null });
      return;
    }
    loadAgentAuthoringOptions()
      .then((options) => {
        if (cancelled) return;
        setState({ options, loading: false, error: null });
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        setState({ options: EMPTY_AGENT_AUTHORING_OPTIONS, loading: false, error: toApiError(err) });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return state;
}
