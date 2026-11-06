import { describe, expect, it } from "vitest";
import {
  MAX_TOKENS_LIMIT,
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
