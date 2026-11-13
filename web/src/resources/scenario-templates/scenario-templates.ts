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
  /**
   * `app_types` minus the forms this template cannot build: a v2 template
   * declares `agent` for documentation but carries no canvas, and a v3 pack only
   * renders the form it declares. The wizard gates its buttons on this list so a
   * guaranteed 40085 never looks like a choice (doc/276 §8.3).
   */
  renderable_app_types?: ScenarioAppType[];
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
  /** Provenance of the published version (doc/276 §7.1). */
  lineage_state?: string;
  baseline_key?: string;
  baseline_version?: number;
  latest_baseline_version?: number;
  overrides_baseline?: boolean;
  baseline_update_available?: boolean;
  /** Evaluation gate state of the published version (doc/276 §9). */
  eval_gate?: ScenarioEvalGateView;
}

export interface ScenarioEvalGateView {
  required: boolean;
  passed: boolean;
  runId?: string;
  reason?: string;
}

/** View model consumed by the wizard and empty state. */
export interface ScenarioTemplateView {
  id: string;
  key: string;
  label: string;
  description: string;
  appTypes: ScenarioAppType[];
  /** Forms the instantiation endpoint can actually build. */
  renderableAppTypes: ScenarioAppType[];
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
  lineageState: string;
  baselineKey: string;
  baselineVersion: number;
  latestBaselineVersion: number;
  baselineUpdateAvailable: boolean;
  evalGate: ScenarioEvalGateView;
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
    renderableAppTypes: item.renderable_app_types ?? item.app_types ?? [],
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
    lineageState: item.lineage_state ?? "",
    baselineKey: item.baseline_key ?? "",
    baselineVersion: item.baseline_version ?? 0,
    latestBaselineVersion: item.latest_baseline_version ?? item.baseline_version ?? 0,
    baselineUpdateAvailable: item.baseline_update_available ?? false,
    evalGate: item.eval_gate ?? { required: false, passed: true },
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

/**
 * One unified instantiation call for all three forms (doc/276 §7.5). The
 * idempotency key travels in the header only; the body never carries one.
 */
export async function instantiateScenarioTemplate(
  templateId: string,
  body: {
    app_type: ScenarioAppType;
    name: string;
    project_id: string;
    dataset_ids?: string[];
    create_missing_datasets?: boolean;
    authoring?: Record<string, unknown>;
    search_config?: Record<string, unknown>;
    agent_dsl?: Record<string, unknown>;
    canvas_category?: string;
  },
  idempotencyKey: string,
): Promise<UnifiedInstantiateResult> {
  const res = await api.post<{ code: number; data: UnifiedInstantiateResult }>(
    `/scenario-template-assets/${templateId}/instantiate`,
    body,
    { headers: { "Idempotency-Key": idempotencyKey } },
  );
  return res.data.data;
}

export interface UnifiedInstantiateResult {
  app_type: ScenarioAppType;
  template_id: string;
  template_version: number;
  instance_id: string;
  instantiation_state: string;
  replayed: boolean;
  chat?: { id: string; name?: string };
  search_app?: { id: string; name?: string };
  agent?: { id: string; title?: string };
  created_dataset_ids?: string[];
}

/** `POST /scenario-template-assets/:id/capability-check` (doc/276 §7.3). */
export async function checkScenarioTemplateCapabilities(
  templateId: string,
  body: { app_type: ScenarioAppType; datasets?: { role: string; dataset_id: string }[] },
): Promise<CapabilityCheckResponse> {
  const res = await api.post<{ code: number; data: CapabilityCheckResponse }>(
    `/scenario-template-assets/${templateId}/capability-check`,
    body,
  );
  return res.data.data;
}

export interface CapabilityCheckResponse {
  template_id: string;
  template_version: number;
  app_type: string;
  verdict: string;
  aggregate?: string;
  probe_status: string;
  capability_set_version: string;
  datasets: {
    role: string;
    dataset_id: string;
    verdict: string;
    aggregate?: string;
    probe_status: string;
    requirements: {
      capability: string;
      scope: string;
      min_level: string;
      achieved: string;
      satisfied: boolean;
      reason?: string;
    }[];
    capabilities: { capability: string; status: string; source: string; note?: string }[];
    repairs: { action: string; reason: string; severity: string }[];
    shared_usage: {
      count: number;
      chat_ids?: string[];
      search_ids?: string[];
      release_ids?: string[];
    };
  }[];
}

/** `GET /datasets/:id/parse-profile` — current chunk method + write-impact preview. */
export async function fetchDatasetParseProfile(datasetId: string): Promise<ParseProfileResponse> {
  const res = await api.get<{ code: number; data: ParseProfileResponse }>(
    `/datasets/${datasetId}/parse-profile`,
  );
  return res.data.data;
}

export interface ParseProfileResponse {
  dataset_id: string;
  chunk_method: string;
  parser_config?: Record<string, unknown>;
  embedding_model?: string;
  pushdown_enabled: boolean;
  document_count: number;
  parsed_count: number;
  reparse_triggered?: boolean;
}

/**
 * `POST /datasets/:id/parse-profile` — the only write channel for the
 * `apply_parse_profile` repair. `acknowledge_shared_impact` must be the user's
 * explicit choice; the wizard never sets it on their behalf.
 */
export async function applyDatasetParseProfile(
  datasetId: string,
  body: {
    chunk_method: string;
    parser_config?: Record<string, unknown>;
    rewrite_existing?: boolean;
    acknowledge_shared_impact?: boolean;
  },
): Promise<ParseProfileResponse> {
  const res = await api.post<{ code: number; data: ParseProfileResponse }>(
    `/datasets/${datasetId}/parse-profile`,
    body,
  );
  return res.data.data;
}

/** `GET /scenario-template-skeletons` — read-only upstream mirror (doc/276 §8.4). */
export async function fetchScenarioSkeletons(): Promise<ScenarioSkeletonCatalog> {
  const res = await api.get<{ code: number; data: ScenarioSkeletonCatalog }>(
    "/scenario-template-skeletons",
  );
  return res.data.data;
}

export interface ScenarioSkeleton {
  id: string;
  source: string;
  upstream_id: string;
  title: string;
  description: string;
  kind?: string;
  canvas_category?: string;
  app_types?: string[];
  has_dsl: boolean;
  /** Upstream-specific extras (`canvas_type`, `filename`, …). */
  metadata?: Record<string, unknown>;
}

export interface ScenarioSkeletonCatalog {
  items: ScenarioSkeleton[];
  /**
   * Per-source mirror status. The field names mirror
   * `service.ScenarioSkeletonSourceStatus`; `available=false` with an
   * `error_detail` is a degraded source, not an empty one — the distinction the
   * panel needs to explain why a catalogue looks empty.
   */
  sources: { source: string; available: boolean; count: number; error_detail?: string }[];
  degraded: boolean;
  upstream_ok: boolean;
}

/** `POST /scenario-template-skeletons/:id/import` — always lands as a draft. */
export async function importScenarioSkeleton(skeletonId: string): Promise<{ id: string; key: string; status: string }> {
  const res = await api.post<{ code: number; data: { id: string; key: string; status: string } }>(
    `/scenario-template-skeletons/${encodeURIComponent(skeletonId)}/import`,
    {},
  );
  return res.data.data;
}

/** `GET /scenario-template-assets/:id/health` — five-metric board (doc/276 §8.6). */
export async function fetchScenarioTemplateHealth(templateId: string): Promise<ScenarioTemplateHealth> {
  const res = await api.get<{ code: number; data: ScenarioTemplateHealth }>(
    `/scenario-template-assets/${templateId}/health`,
  );
  return res.data.data;
}

export interface ScenarioTemplateHealth {
  template_id: string;
  template_name: string;
  window_days: number;
  instances: number;
  truncated: boolean;
  computed_at: string;
  metrics: {
    key: string;
    label: string;
    value: number;
    numerator: number;
    denominator: number;
    available: boolean;
    reason?: string;
    definition: string;
  }[];
}

/**
 * Skeleton mirror with a soft failure mode. `50303` means "every upstream
 * catalogue is unreachable", which the wizard shows as a degraded panel instead
 * of blocking the nine builtin baselines (doc/276 §8.4).
 */
export async function fetchScenarioSkeletonCatalogSafe(): Promise<ScenarioSkeletonCatalog> {
  try {
    return await fetchScenarioSkeletons();
  } catch (error) {
    if (error instanceof Error && "code" in error && (error as { code?: number }).code === 50303) {
      return { items: [], sources: [], degraded: true, upstream_ok: false };
    }
    throw error;
  }
}
