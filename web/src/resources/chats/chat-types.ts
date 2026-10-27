/* ── Chat module shared types & helpers ───────────────────────────── */

export interface ChatLike {
  id: string;
  name: string;
  status: string;
  dataset_ids?: string;
  owner_id?: string;
  message_count?: number;
  created_at?: string;
}

export interface DatasetLike {
  id: string;
  name: string;
  ragflow_dataset_id?: string;
}

export interface ChatConfigLike {
  id: string;
  name: string;
  dataset_ids?: string[];
  language?: string;
  llm_id?: string;
  rerank_id?: string;
  top_n?: number;
  top_k?: number;
  similarity_threshold?: number;
  vector_similarity_weight?: number;
  llm_setting?: Record<string, unknown>;
  prompt_config?: { system?: string; prologue?: string; empty_response?: string };
}

export interface AddedModelLike {
  model_id: string;
  name: string;
  provider_name?: string;
}

export interface DefaultModelLike {
  model_id: string;
  name: string;
  model_type: string;
}

export interface ChatConfigValues {
  system: string;
  prologue: string;
  emptyResponse: string;
  language: string;
  topN: string;
  similarity: string;
  vectorWeight: string;
  llmId: string;
  rerankId: string;
  llmSetting: string;
}

/* ── Constants ───────────────────────────────────────────────────── */

export const statusChoices = [
  { id: "active", name: "启用" },
  { id: "disabled", name: "停用" },
];

export const EMPTY_CONFIG: ChatConfigValues = {
  system: "",
  prologue: "",
  emptyResponse: "",
  language: "Chinese",
  topN: "",
  similarity: "",
  vectorWeight: "",
  llmId: "",
  rerankId: "",
  llmSetting: "",
};

export const RECOMMENDED_PROMPT = {
  system: "你是一个智能助手。请基于知识库内容回答用户的问题，优先引用知识库信息；如果知识库内容与问题无关，请明确说明未找到相关内容。\n以下是知识库：\n{knowledge}\n以上是知识库。",
  prologue: "你好！我是企业知识助手，请问有什么可以帮您？",
  emptyResponse: "抱歉，当前数据范围内没有找到相关内容，请换个问法试试。",
};

/* ── Helpers ─────────────────────────────────────────────────────── */

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function toCfg(d: any): ChatConfigValues {
  return {
    system: d?.prompt_config?.system ?? "",
    prologue: d?.prompt_config?.prologue ?? "",
    emptyResponse: d?.prompt_config?.empty_response ?? "",
    language: d?.language ?? "Chinese",
    topN: d?.top_n ? String(d.top_n) : "",
    similarity: d?.similarity_threshold !== undefined ? String(d.similarity_threshold) : "",
    vectorWeight: d?.vector_similarity_weight !== undefined ? String(d.vector_similarity_weight) : "",
    llmId: d?.llm_id ?? "",
    rerankId: d?.rerank_id ?? "",
    llmSetting: d?.llm_setting ? JSON.stringify(d.llm_setting, null, 2) : "",
  };
}

export function rangeVal(raw: string, fallback: number): number {
  const n = Number(raw);
  return Number.isNaN(n) ? fallback : Math.max(0, Math.min(100000, n));
}

export function datasetOptions(datasets: DatasetLike[] | undefined) {
  return { values: datasets ?? [], mapId: (d: DatasetLike) => d.ragflow_dataset_id || d.id };
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function parseLLMSetting(raw: string): any {
  const t = raw.trim();
  if (!t) return undefined;
  try {
    return JSON.parse(t);
  } catch {
    throw new Error("模型高级参数不是合法 JSON");
  }
}
