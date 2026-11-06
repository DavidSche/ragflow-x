import { PARAMETER_PROFILES, parameterProfileById, type ParameterProfileId } from "./prompt-presets";

export interface SearchParameterConfig {
  llm_temperature: number;
  /** Optional engine knobs; present once the search app editor sets them. */
  llm_top_p?: number;
  llm_max_tokens?: number;
  similarity_threshold: number;
  vector_similarity_weight: number;
  top_k: number;
}

export function applyParameterProfileToSearchConfig<T extends SearchParameterConfig>(
  config: T,
  profileId: ParameterProfileId,
): T {
  const profile = parameterProfileById(profileId);
  return {
    ...config,
    llm_temperature: profile.temperature,
    // Full LLM-knob down-push: a profile sets temperature AND top_p AND
    // max_tokens so SearchAppShow.save() sends the complete trio to the
    // engine (missing keys are dropped, so absent knobs must be written here).
    llm_top_p: profile.topP,
    llm_max_tokens: profile.maxTokens,
    similarity_threshold: profile.similarityThreshold,
    vector_similarity_weight: profile.vectorWeight,
    top_k: profile.topN,
  };
}

export function matchSearchParameterProfileId(
  config: SearchParameterConfig,
): ParameterProfileId | undefined {
  return PARAMETER_PROFILES.find(
    (profile) =>
      config.llm_temperature === profile.temperature &&
      config.llm_top_p === profile.topP &&
      config.llm_max_tokens === profile.maxTokens &&
      config.similarity_threshold === profile.similarityThreshold &&
      config.vector_similarity_weight === profile.vectorWeight &&
      config.top_k === profile.topN,
  )?.id;
}
