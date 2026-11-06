/* ── Search App LLM generation parameters ──────────────────────────────
 * The engine-side `llm_setting` is an opaque map that is pushed down as-is
 * (temperature / top_p / max_tokens are the knobs RAGFlow v1 search-apps
 * honour). Absent keys fall back to engine defaults, so the editor keeps
 * top_p / max_tokens optional and only includes a key once the user set it.
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

/** Read the live engine `llm_setting` into editor form values. */
export function parseSearchAppLLM(
  llm: Record<string, unknown> | undefined | null,
): SearchAppLLMFormValues {
  const temperature = Number(
    llm && llm.temperature !== undefined ? llm.temperature : 0.1,
  );
  const topP = readOptionalNumber(llm, "top_p");
  const maxTokens = readOptionalNumber(llm, "max_tokens");
  return { temperature, topP, maxTokens };
}

/** Build the engine `llm_setting` map; only includes optional knobs the user set. */
export function buildSearchAppLLM(values: SearchAppLLMFormValues, llmId: string): Record<string, unknown> {
  const setting: Record<string, unknown> = { llm_id: llmId, temperature: values.temperature };
  if (values.topP !== undefined && Number.isFinite(values.topP)) setting.top_p = values.topP;
  if (values.maxTokens !== undefined && Number.isFinite(values.maxTokens)) setting.max_tokens = values.maxTokens;
  return setting;
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

function readOptionalNumber(
  llm: Record<string, unknown> | undefined | null,
  key: string,
): number | undefined {
  if (!llm || llm[key] === undefined || llm[key] === null) return undefined;
  const n = Number(llm[key]);
  return Number.isFinite(n) ? n : undefined;
}
