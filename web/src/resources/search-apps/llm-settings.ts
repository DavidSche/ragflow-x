/* ── Search App LLM generation parameters ──────────────────────────────
 * The engine-side `llm_setting` is an opaque map that is pushed down as-is
 * (temperature / top_p / max_tokens are the knobs RAGFlow v1 search-apps
 * honour). Absent keys fall back to engine defaults, so build() only injects a
 * key the user explicitly set; parse() surfaces the E-proven DEFAULT_LLM
 * signature for knobs the engine left unset (Default-first, doc/263 §7.2).
 * Keep this module pure & free of UI imports so it stays unit-testable.
 * -------------------------------------------------------------------- */

export interface SearchAppLLMFormValues {
  /** Sampling temperature (0–2); always present so the slider has a value. */
  temperature: number;
  /** Nucleus sampling threshold (0–1); undefined = engine default. */
  topP?: number;
  /** Per-response token cap; undefined = engine default. */
  maxTokens?: number;
}

/** Valid upper bound the UI should accept for max_tokens. */
export const MAX_TOKENS_LIMIT = 100_000;

/**
 * Default-first knobs (E-proven deterministic signature, see doc/263 §7.2):
 * temperature 0 + top_p 0.1 + max_tokens 800. Any search app whose engine
 * config omits a knob surfaces this signature instead of the engine's looser
 * default, so an out-of-the-box app is deterministic without any tuning.
 */
export const DEFAULT_LLM = {
  temperature: 0,
  topP: 0.1,
  maxTokens: 800,
} as const;

/** Read the live engine `llm_setting` into editor form values. */
export function parseSearchAppLLM(
  llm: Record<string, unknown> | undefined | null,
): SearchAppLLMFormValues {
  const temperature = Number(
    llm && llm.temperature !== undefined ? llm.temperature : DEFAULT_LLM.temperature,
  );
  const topP = readNumberOrDefault(llm, "top_p", DEFAULT_LLM.topP);
  const maxTokens = readNumberOrDefault(llm, "max_tokens", DEFAULT_LLM.maxTokens);
  return { temperature, topP, maxTokens };
}

/** Build the engine `llm_setting` map; only includes optional knobs the user set. */
export function buildSearchAppLLM(values: SearchAppLLMFormValues, llmId: string): Record<string, unknown> {
  const setting: Record<string, unknown> = { llm_id: llmId, temperature: values.temperature };
  if (values.topP !== undefined && Number.isFinite(values.topP)) setting.top_p = values.topP;
  if (values.maxTokens !== undefined && Number.isFinite(values.maxTokens)) setting.max_tokens = values.maxTokens;
  return setting;
}

/** Live-config shape buildInitLLMPayload reads (subset of GET /search-apps/:id/config). */
export interface SearchAppInitConfig {
  search_config?: {
    chat_id?: string;
    llm_setting?: Record<string, unknown> | null;
  };
}

/**
 * Build the PUT payload that pre-tunes a brand-new search app to the E-proven
 * deterministic signature (DEFAULT_LLM) — the “new app is deterministic out
 * of the box” step (doc/264 1.2, Default-first). Returns null when no chat
 * model is resolvable yet (engine still defaulting), in which case the caller
 * skips the write and the detail page remains the fallback edit surface.
 */
export function buildInitLLMPayload(
  config: SearchAppInitConfig | undefined | null,
  name: string,
): { name: string; search_config: { llm_setting: Record<string, unknown> } } | null {
  const c = config?.search_config;
  const llm = c?.llm_setting && typeof c.llm_setting === "object" ? c.llm_setting : {};
  const chatId =
    (c?.chat_id ?? "").trim() || ((llm.llm_id as string | undefined) ?? "").trim();
  if (!chatId) return null;
  return { name, search_config: { llm_setting: buildSearchAppLLM(DEFAULT_LLM, chatId) } };
}

/**
 * Validate the raw max_tokens string from a number input.
 * Returns the parsed integer, or undefined when blank, or NaN when invalid.
 * A valid value is an integer in [1, MAX_TOKENS_LIMIT].
 */
export function maxTokensFromInput(raw: string): number | undefined {
  const trimmed = raw.trim();
  if (trimmed === "") return undefined;
  const n = Number(trimmed);
  if (!Number.isFinite(n) || !Number.isInteger(n) || n < 1 || n > MAX_TOKENS_LIMIT) {
    return Number.NaN;
  }
  return n;
}

function readNumberOrDefault(
  llm: Record<string, unknown> | undefined | null,
  key: string,
  defaultValue: number,
): number {
  if (!llm || llm[key] === undefined || llm[key] === null) return defaultValue;
  const n = Number(llm[key]);
  return Number.isFinite(n) ? n : defaultValue;
}

/* ── Answer-discipline presets (copy-paste templates) ───────────────────
 * The RAGFlow engine composes a search-app answer from ITS OWN system
 * prompt (`prompt_config.system`; `{knowledge}` receives the retrieved
 * chunks). ragflow-x has no prompt slot on SearchConfig (doc/268 §3.2), so
 * the "answer only from {knowledge}" discipline must live engine-side.
 * These presets give an out-of-the-box app the same contract DEFAULT_LLM
 * already enforces for sampling determinism. Archived identically as
 * doc/268 §3.2 and localenv/templates/search-app-answer-discipline/ — keep
 * all three in sync. Pure data, no UI imports, unit-testable.
 * ---------------------------------------------------------------------- */

/** One copy-paste system-prompt preset for the RAGFlow search-app. */
export interface SearchAppSystemPromptPreset {
  /** Stable id (strict-zh / light-en). */
  key: string;
  /** Human label shown next to the copy button. */
  label: string;
  /** Prompt language. */
  language: "zh" | "en";
  /** The system prompt body; MUST contain the {knowledge} placeholder. */
  system: string;
  /** Suggested citation-confirmation sentence. */
  quote: string;
  /** Suggested empty-response sentence (engine `empty_response`). */
  emptyResponse: string;
}

/**
 * Answer-discipline presets. The strict one targets the i-0002 class
 * (out-of-scope extrapolation) and i-0005 (threshold/calibre drift) from the
 * tri-matrix audit (doc/268 §1.1): every factual claim must trace to
 * {knowledge}, thresholds are quoted verbatim, and unsupported points are
 * declared instead of fabricated.
 */
export const SEARCH_APP_SYSTEM_PROMPT_PRESETS: readonly SearchAppSystemPromptPreset[] = [
  {
    key: "strict-zh",
    label: "答案纪律 · 严格（推荐）",
    language: "zh",
    system:
      "你是一名严谨的安全生产领域检索问答助手。你的唯一事实来源是下面提供的知识库文本，请基于它详细回答问题。\n\n" +
      "【检索命中纪律（硬性，优先级最高）】\n" +
      "1. 只依据 {knowledge} 中出现的内容作答：凡知识库未出现的条件、限额、口径、案例、数值，一律不得补充。\n" +
      "2. 知识库已给出判定标准/限额时，逐字照抄其口径，不得换算、不得自行设定上限或默认值、不得用常识/教材/题库外内容补全。\n" +
      "3. 答案中每一个事实性断言的依据都必须能在 {knowledge} 中找到对应原文；无依据的断言不得写出。\n" +
      "4. 当知识库内容不足以完整回答时：先答出有依据的部分，再明确说明「以下内容未在知识库中找到依据」并列明缺失项；禁止编造细节凑成答案。\n" +
      "5. 知识库检索结果为空时，只输出：The answer you are looking for is not found in the knowledge base!\n\n" +
      "【作答格式】\n" +
      "- 命中充分：给出确定性结论（含具体数值/条件，如「超一类企业按…判定，其中…」），可分段详答。\n" +
      "- 涉及多类：逐类罗列，每类结论后标注其依据出自知识库哪一部分。\n" +
      "- 回答结束，结合给定的引用内容规范引用。\n\n" +
      "知识库文本如下：\n{knowledge}\n以上为知识库文本，请按上述纪律作答。",
    quote: "请只引用 {knowledge} 中与结论直接相关的原文片段；每个结论至少一条引用，禁止无源引用。",
    emptyResponse: "Sorry! No relevant content was found in the knowledge base!",
  },
  {
    key: "light-en",
    label: "Answer discipline · light (keeps upstream style)",
    language: "en",
    system:
      "You are an intelligent assistant. Please summarize the content of the dataset to answer the question.\n" +
      "Please list the data in the dataset and answer in detail. When all dataset content is irrelevant to the question,\n" +
      "your answer must include the sentence \"The answer you are looking for is not found in the knowledge base!\"\n" +
      "Answers need to consider chat history.\n\n" +
      "Strict discipline (higher priority than the above):\n" +
      "- Answer ONLY from {knowledge}. Never add conditions, thresholds, cases, or values not present in it.\n" +
      "- Quote thresholds/rules verbatim from {knowledge}; do not convert, estimate, or substitute standards.\n" +
      "- State explicitly when a requested point has no support in {knowledge}; never fabricate.\n\n" +
      "Here is the knowledge base:\n{knowledge}\nThe above is the knowledge base.",
    quote:
      "Cite only the exact {knowledge} passages that back each conclusion; every conclusion needs at least one citation, and unsupported citations are forbidden.",
    emptyResponse: "Sorry! No relevant content was found in the knowledge base!",
  },
];

/** Shortcut to the recommended strict preset for docs/tooltips. */
export const DEFAULT_ANSWER_DISCIPLINE_PRESET = SEARCH_APP_SYSTEM_PROMPT_PRESETS[0];
