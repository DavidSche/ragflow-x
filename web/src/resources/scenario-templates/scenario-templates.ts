import { api } from "../../lib/api";

/**
 * Scenario template catalog.
 *
 * The catalog itself lives in the backend (`GET /scenario-template-catalog`)
 * and is seeded per tenant from `internal/service/scenario_template_seed.go`.
 * This module only carries the wire types, the localization projection and the
 * instantiation call: there is deliberately no template copy, preset text or
 * parameter table here. Prompt and parameters are resolved by the backend
 * (`prompt` / `parameters` on the catalog row) so the wizard renders exactly
 * what the server writes into the assistant (doc/228 T7.0 single source of
 * truth; corrected after review: the wizard used to send its own copy back as
 * an override, which silently discarded the template payload).
 */

export type ScenarioAppType = "chat" | "search" | "agent";

/** Builtin template keys, kept only for tests/e2e deep links. */
export const SCENARIO_TEMPLATE_KEYS = [
  "enterprise-qa",
  "policy",
  "customer-service",
  "product-manual",
  "sales-support",
  "contract-compliance",
  "operations",
  "training",
  "finance",
] as const;

export type ScenarioTemplateKey = (typeof SCENARIO_TEMPLATE_KEYS)[number];

export interface ScenarioTemplateTranslation {
  name?: string;
  description?: string;
  datasetSuggestions?: string[];
  sampleQuestions?: string[];
  evaluationQuestions?: string[];
  permissionAdvice?: string;
  publishChecklist?: string[];
  expectedOutcome?: string;
}

/** Raw catalog row as returned by the API. */
/** Server-resolved prompt defaults applied at instantiation. */
export interface ScenarioTemplatePrompt {
  system: string;
  prologue: string;
  empty_response: string;
}

/** Server-resolved retrieval/generation defaults applied at instantiation. */
export interface ScenarioTemplateParameters {
  top_n: number;
  similarity_threshold: number;
  vector_similarity_weight: number;
  temperature: number;
  top_p: number;
  max_tokens: number;
}

export interface ScenarioTemplateCatalogItem {
  id: string;
  key: string;
  name: string;
  description: string;
  app_types: ScenarioAppType[];
  latest_version: number;
  source: "builtin" | "custom" | string;
  status: string;
  payload_schema: string;
  prompt_preset_id: string;
  parameter_profile_id: string;
  prompt?: ScenarioTemplatePrompt;
  parameters?: ScenarioTemplateParameters;
  dataset_suggestions: string[];
  sample_questions: string[];
  evaluation_questions: string[];
  permission_advice: string;
  publish_checklist: string[];
  expected_outcome: string;
  translations?: Record<string, ScenarioTemplateTranslation>;
}

/** View model consumed by the wizard and empty state. */
export interface ScenarioTemplateView {
  id: string;
  key: string;
  label: string;
  description: string;
  appTypes: ScenarioAppType[];
  latestVersion: number;
  source: string;
  promptPresetId: string;
  profileId: string;
  prompt: ScenarioTemplatePrompt;
  parameters: ScenarioTemplateParameters;
  suggestedDatasetNames: string[];
  sampleQuestions: string[];
  evaluationQuestions: string[];
  permissionAdvice: string;
  publishChecklist: string[];
  expectedOutcome: string;
  /** True when the requested locale fell back to the canonical copy. */
  translationMissing: boolean;
}

const EMPTY_PROMPT: ScenarioTemplatePrompt = { system: "", prologue: "", empty_response: "" };
const EMPTY_PARAMETERS: ScenarioTemplateParameters = {
  top_n: 0,
  similarity_threshold: 0,
  vector_similarity_weight: 0,
  temperature: 0,
  top_p: 0,
  max_tokens: 0,
};

function localizedList(localized: string[] | undefined, canonical: string[]): string[] {
  return localized && localized.length > 0 ? localized : canonical;
}

/** Projects one catalog row into the requested locale. */
export function localizeScenarioTemplate(
  item: ScenarioTemplateCatalogItem,
  language = "zh",
): ScenarioTemplateView {
  const translation = item.translations?.[language];
  const missing = language !== "zh" && !translation?.name;
  return {
    id: item.id,
    key: item.key,
    label: missing ? item.name : (translation?.name ?? item.name),
    description: missing ? item.description : (translation?.description ?? item.description),
    appTypes: item.app_types ?? [],
    latestVersion: item.latest_version ?? 0,
    source: item.source,
    promptPresetId: item.prompt_preset_id ?? "",
    profileId: item.parameter_profile_id ?? "",
    prompt: item.prompt ?? EMPTY_PROMPT,
    parameters: item.parameters ?? EMPTY_PARAMETERS,
    suggestedDatasetNames: localizedList(
      missing ? undefined : translation?.datasetSuggestions,
      item.dataset_suggestions ?? [],
    ),
    sampleQuestions: localizedList(
      missing ? undefined : translation?.sampleQuestions,
      item.sample_questions ?? [],
    ),
    evaluationQuestions: localizedList(
      missing ? undefined : translation?.evaluationQuestions,
      item.evaluation_questions ?? [],
    ),
    permissionAdvice: missing
      ? item.permission_advice
      : (translation?.permissionAdvice ?? item.permission_advice),
    publishChecklist: localizedList(
      missing ? undefined : translation?.publishChecklist,
      item.publish_checklist ?? [],
    ),
    expectedOutcome: missing
      ? item.expected_outcome
      : (translation?.expectedOutcome ?? item.expected_outcome),
    translationMissing: missing,
  };
}

/** Loads the tenant's published catalog from the API. */
export async function fetchScenarioTemplateCatalog(language = "zh"): Promise<ScenarioTemplateView[]> {
  const res = await api.get<{ code: number; data: ScenarioTemplateCatalogItem[] }>(
    "/scenario-template-catalog",
  );
  const rows = res.data?.data ?? [];
  return rows.map((row) => localizeScenarioTemplate(row, language));
}

/** Request body for `POST /scenario-template-assets/:id/instantiate-chat`. */
export interface InstantiateChatPayload {
  name?: string;
  dataset_ids?: string[];
  create_missing_datasets?: boolean;
  authoring?: {
    llm_id?: string;
    language?: string;
    top_n?: number;
    similarity_threshold?: number;
    vector_similarity_weight?: number;
    prompt_config?: Record<string, unknown>;
    llm_setting?: Record<string, unknown>;
  };
}

export interface InstantiatedChat {
  id: string;
  name: string;
  dataset_ids: string;
  scenario_template_id: string;
  scenario_template_version: number;
}

export async function instantiateScenarioChat(
  templateId: string,
  payload: InstantiateChatPayload,
): Promise<InstantiatedChat> {
  const res = await api.post<{
    code: number;
    data: { chat: InstantiatedChat; template_version: number; created_dataset_ids: string[] };
  }>(`/scenario-template-assets/${templateId}/instantiate-chat`, payload);
  return res.data.data.chat;
}
