import type { ScenarioAppType, ScenarioTemplateView } from "../scenario-templates";

/** Wizard steps: scenario → knowledge (capability check) → application → verify. */
export const WIZARD_STEPS = ["scenario", "knowledge", "application", "verify"] as const;

export type WizardStepKey = (typeof WIZARD_STEPS)[number];

/** Four-state capability verdict (doc/276 §6.5). Never derived from colour alone. */
export type CapabilityState = "satisfied" | "probe_limited" | "repairable" | "blocked";

export interface CapabilityEvidenceView {
  capability: string;
  status: string;
  source: string;
  note?: string;
}

export interface CapabilityRequirementView {
  capability: string;
  scope: string;
  min_level: string;
  achieved: string;
  satisfied: boolean;
  reason?: string;
}

export interface CapabilityRepairView {
  action: string;
  reason: string;
  severity: string;
  /** Machine-readable exec path, when the repair has one (doc/282 §6). */
  endpoint?: string;
  /** Target parse profile for `apply_parse_profile` (doc/285 §4). */
  chunk_method?: string;
  /** The write re-parses existing documents and needs explicit consent (40915). */
  requires_reparse?: boolean;
}

export interface SharedUsageView {
  count: number;
  chat_ids?: string[];
  search_ids?: string[];
  release_ids?: string[];
}

export interface DatasetCapabilityView {
  role: string;
  dataset_id: string;
  /** Documents in the dataset, and how many already have chunks. */
  document_count?: number;
  parsed_count?: number;
  verdict: CapabilityState | string;
  aggregate?: string;
  probe_status: string;
  requirements: CapabilityRequirementView[];
  capabilities: CapabilityEvidenceView[];
  repairs: CapabilityRepairView[];
  shared_usage: SharedUsageView;
  /** Display name filled in by the wizard from the dataset list. */
  dataset_name?: string;
}

export interface CapabilityCheckView {
  template_id: string;
  template_version: number;
  app_type: string;
  verdict: CapabilityState | string;
  aggregate?: string;
  probe_status: string;
  capability_set_version: string;
  datasets: DatasetCapabilityView[];
}

/** `eval_gate` as returned on the catalog row (doc/276 §9). */
export interface ScenarioEvalGate {
  required: boolean;
  passed: boolean;
  runId?: string;
  reason?: string;
}

export interface WizardSelection {
  template: ScenarioTemplateView | null;
  appType: ScenarioAppType;
  name: string;
  projectId: string;
  modelId: string;
  datasetIds: string[];
}
