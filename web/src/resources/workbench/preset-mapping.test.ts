import { describe, expect, it } from "vitest";
import { EMPTY_CONFIG } from "../chats/chat-types";
import { PARAMETER_PROFILES, PROMPT_PRESETS } from "./prompt-presets";
import {
  applyParameterProfileToChat,
  applyPromptPreset,
  matchParameterProfileId,
  matchPromptPresetId,
} from "./preset-mapping";
import {
  applyParameterProfileToSearchConfig,
  matchSearchParameterProfileId,
} from "./search-preset-mapping";

describe("prompt preset mapping", () => {
  it("applies a scenario prompt and its default profile", () => {
    const next = applyPromptPreset(EMPTY_CONFIG, "contract-compliance");
    const preset = PROMPT_PRESETS.find((item) => item.id === "contract-compliance");
    expect(preset).toBeDefined();
    expect(next.system).toContain("合同与合规辅助助手");
    expect(next.topN).toBe("8");
    expect(JSON.parse(next.llmSetting)).toEqual({
      temperature: 0.05,
      top_p: 0.8,
      max_tokens: 2048,
    });
    expect(matchPromptPresetId(next)).toBe("contract-compliance");
    expect(matchParameterProfileId(next)).toBe("citation-strict");
  });

  it("applies each parameter profile without changing prompts or models", () => {
    const source = { ...EMPTY_CONFIG, llmId: "glm-5.3-flash" };
    PARAMETER_PROFILES.forEach((profile) => {
      const next = applyParameterProfileToChat(source, profile.id);
      expect(next.llmId).toBe("glm-5.3-flash");
      expect(next.topN).toBe(String(profile.topN));
      expect(JSON.parse(next.llmSetting)).toEqual({
        temperature: profile.temperature,
        top_p: profile.topP,
        max_tokens: profile.maxTokens,
      });
      expect(matchParameterProfileId(next)).toBe(profile.id);
    });
  });

  it("maps profiles to search app fields while preserving other settings", () => {
    const source = {
      kb_ids: ["kb-1"],
      summary: true,
      llm_temperature: 0.7,
      similarity_threshold: 0.2,
      vector_similarity_weight: 0.3,
      top_k: 1024,
    };
    const next = applyParameterProfileToSearchConfig(source, "safe");
    expect(next.kb_ids).toEqual(["kb-1"]);
    expect(next.summary).toBe(true);
    expect(next.llm_temperature).toBe(0.1);
    expect(next.similarity_threshold).toBe(0.32);
    expect(next.vector_similarity_weight).toBe(0.35);
    expect(next.top_k).toBe(6);
    expect(matchSearchParameterProfileId(next)).toBe("safe");
  });
});
