import type { ChatConfigValues } from "../chats/chat-types";
import {
  PARAMETER_PROFILES,
  PROMPT_PRESETS,
  parameterProfileById,
  type ParameterProfileId,
  type PromptPresetId,
} from "./prompt-presets";

export function applyPromptPreset(value: ChatConfigValues, presetId: PromptPresetId): ChatConfigValues {
  const preset = PROMPT_PRESETS.find((item) => item.id === presetId);
  if (!preset) return value;
  const profile = parameterProfileById(preset.profileId);
  return {
    ...value,
    ...applyParameterProfileToChat(value, profile.id),
    system: preset.system,
    prologue: preset.prologue,
    emptyResponse: preset.emptyResponse,
  };
}

export function applyParameterProfileToChat(
  value: ChatConfigValues,
  profileId: Parameters<typeof parameterProfileById>[0],
): ChatConfigValues {
  const profile = parameterProfileById(profileId);
  return {
    ...value,
    topN: String(profile.topN),
    similarity: String(profile.similarityThreshold),
    vectorWeight: String(profile.vectorWeight),
    llmSetting: JSON.stringify(
      {
        temperature: profile.temperature,
        top_p: profile.topP,
        max_tokens: profile.maxTokens,
      },
      null,
      2,
    ),
  };
}

export function matchPromptPresetId(value: ChatConfigValues): PromptPresetId | undefined {
  return PROMPT_PRESETS.find(
    (preset) =>
      preset.system === value.system &&
      preset.prologue === value.prologue &&
      preset.emptyResponse === value.emptyResponse,
  )?.id;
}

export function matchParameterProfileId(value: ChatConfigValues): ParameterProfileId | undefined {
  return PARAMETER_PROFILES.find(
    (profile) =>
      value.topN === String(profile.topN) &&
      value.similarity === String(profile.similarityThreshold) &&
      value.vectorWeight === String(profile.vectorWeight) &&
      value.llmSetting ===
        JSON.stringify(
          { temperature: profile.temperature, top_p: profile.topP, max_tokens: profile.maxTokens },
          null,
          2,
        ),
  )?.id;
}
