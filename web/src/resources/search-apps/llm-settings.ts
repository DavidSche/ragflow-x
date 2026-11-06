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
