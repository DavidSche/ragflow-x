import { PARAMETER_PROFILES, parameterProfileById, type ParameterProfileId } from "./prompt-presets";

export interface SearchParameterConfig {
  llm_temperature: number;
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
      config.similarity_threshold === profile.similarityThreshold &&
      config.vector_similarity_weight === profile.vectorWeight &&
      config.top_k === profile.topN,
  )?.id;
}
