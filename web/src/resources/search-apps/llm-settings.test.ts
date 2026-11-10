import { describe, expect, it } from "vitest";
import {
  DEFAULT_ANSWER_DISCIPLINE_PRESET,
  MAX_TOKENS_LIMIT,
  SEARCH_APP_SYSTEM_PROMPT_PRESETS,
  buildInitLLMPayload,
  buildSearchAppLLM,
  maxTokensFromInput,
  parseSearchAppLLM,
} from "./llm-settings";

describe("parseSearchAppLLM", () => {
  it("falls back to the deterministic default signature when the map is absent", () => {
    expect(parseSearchAppLLM(undefined)).toEqual({ temperature: 0, topP: 0.1, maxTokens: 800 });
    expect(parseSearchAppLLM(null)).toEqual({ temperature: 0, topP: 0.1, maxTokens: 800 });
  });

  it("reads all three knobs from a live engine config", () => {
    const values = parseSearchAppLLM({ llm_id: "m", temperature: 0, top_p: 0.1, max_tokens: 800 });
    expect(values).toEqual({ temperature: 0, topP: 0.1, maxTokens: 800 });
  });

  it("fills deterministic defaults when the engine config omits optional knobs", () => {
    const values = parseSearchAppLLM({ llm_id: "m", temperature: 0.5 });
    expect(values.topP).toBe(0.1);
    expect(values.maxTokens).toBe(800);
  });

  it("falls back to defaults for non-finite optional values instead of corrupting state", () => {
    const values = parseSearchAppLLM({ temperature: 0.5, top_p: "abc", max_tokens: null });
    expect(values).toEqual({ temperature: 0.5, topP: 0.1, maxTokens: 800 });
  });
});

describe("buildSearchAppLLM", () => {
  it("always sends llm_id and temperature", () => {
    expect(buildSearchAppLLM({ temperature: 0 }, "m1")).toEqual({ llm_id: "m1", temperature: 0 });
  });

  it("only includes optional knobs the user actually set (no data loss on round-trip)", () => {
    const setting = buildSearchAppLLM({ temperature: 0.3, topP: 0.9, maxTokens: 2000 }, "m1");
    expect(setting).toEqual({ llm_id: "m1", temperature: 0.3, top_p: 0.9, max_tokens: 2000 });
  });

  it("omits blank/NaN optional knobs so the engine keeps its defaults", () => {
    const setting = buildSearchAppLLM({ temperature: 0.3, topP: undefined, maxTokens: Number.NaN }, "m1");
    expect(setting).toEqual({ llm_id: "m1", temperature: 0.3 });
  });
});

describe("buildInitLLMPayload", () => {
  it("builds the E-proven deterministic signature payload from the live chat model", () => {
    const payload = buildInitLLMPayload(
      { search_config: { chat_id: "chat-nlp-1", llm_setting: { llm_id: "chat-nlp-1", temperature: 0.7 } } },
      "法规问答",
    );
    expect(payload).toEqual({
      name: "法规问答",
      search_config: {
        llm_setting: { llm_id: "chat-nlp-1", temperature: 0, top_p: 0.1, max_tokens: 800 },
      },
    });
  });

  it("falls back to llm_setting.llm_id when chat_id is not set", () => {
    const payload = buildInitLLMPayload(
      { search_config: { llm_setting: { llm_id: "chat-nlp-2" } } },
      "x",
    );
    expect(payload?.search_config.llm_setting.llm_id).toBe("chat-nlp-2");
    expect(payload?.search_config.llm_setting.temperature).toBe(0);
  });

  it("returns null when no chat model is resolvable yet (skip the write)", () => {
    expect(buildInitLLMPayload(null, "x")).toBeNull();
    expect(buildInitLLMPayload(undefined, "x")).toBeNull();
    expect(buildInitLLMPayload({ search_config: {} }, "x")).toBeNull();
  });
});

describe("maxTokensFromInput", () => {
  it("returns undefined for a blank input (use engine default)", () => {
    expect(maxTokensFromInput("")).toBeUndefined();
    expect(maxTokensFromInput("   ")).toBeUndefined();
  });

  it("parses a valid integer within range", () => {
    expect(maxTokensFromInput("800")).toBe(800);
    expect(maxTokensFromInput(String(MAX_TOKENS_LIMIT))).toBe(MAX_TOKENS_LIMIT);
  });

  it("rejects out-of-range, non-integer, and non-numeric input", () => {
    expect(maxTokensFromInput("0")).toBeNaN();
    expect(maxTokensFromInput("100001")).toBeNaN();
    expect(maxTokensFromInput("12.5")).toBeNaN();
    expect(maxTokensFromInput("abc")).toBeNaN();
    expect(maxTokensFromInput("Infinity")).toBeNaN();
  });
});

describe("answer-discipline presets", () => {
  it("exposes the strict preset as the recommended default", () => {
    expect(DEFAULT_ANSWER_DISCIPLINE_PRESET.key).toBe("strict-zh");
  });

  it("every preset system prompt carries the {knowledge} placeholder and stays engine-safe", () => {
    for (const preset of SEARCH_APP_SYSTEM_PROMPT_PRESETS) {
      expect(preset.system).toContain("{knowledge}");
      // The engine injects retrieved chunks via {knowledge}; {question} travels
      // through the user message, so the system prompt must not depend on it.
      expect(preset.system).not.toContain("{question}");
      expect(preset.quote).toContain("{knowledge}");
      expect(preset.emptyResponse.length).toBeGreaterThan(0);
    }
  });

  it("strict preset hard-bans fabrication and extrapolation in its wording", () => {
    const zh = SEARCH_APP_SYSTEM_PROMPT_PRESETS[0].system;
    expect(zh).toContain("不得补充");
    expect(zh).toContain("不得换算");
    expect(zh).toContain("不得写出");
    expect(zh).toContain("禁止编造细节凑成答案");
  });

  it("preset ids are unique and stable in order", () => {
    const keys = SEARCH_APP_SYSTEM_PROMPT_PRESETS.map((p) => p.key);
    expect(new Set(keys).size).toBe(keys.length);
    expect(keys).toEqual(["strict-zh", "light-en"]);
  });
});
