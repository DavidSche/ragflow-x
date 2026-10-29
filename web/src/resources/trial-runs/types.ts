import { api } from "@/lib/api";

/**
 * Trial run wire types and client (doc/228 T7.3′). A trial run is a diagnostic
 * of one assistant against its template eval suite: retrieval only, never gate
 * eligible, and `hit_at_k` is null when no case defines expected citations.
 */

export type TrialRunStatus = "QUEUED" | "RUNNING" | "COMPLETED" | "FAILED" | "CANCELLED";

export interface TrialRunSummary {
  cases: number;
  returned_cases: number;
  failed_cases: number;
  hit_at_k: number | null;
  hit_denominator: number;
  undefined_cases: number;
  top_score_avg: number;
  latency_p95_ms: number;
  top_n: number;
  mode: string;
  gate_eligible: boolean;
}

export interface TrialRunConfigSnapshot {
  assistant_id: string;
  assistant_name: string;
  assistant_kind: string;
  dataset_ids: string[];
  language: string;
  llm_id: string;
  rerank_id: string;
  top_n: number;
  top_k: number;
  similarity_threshold: number;
  vector_similarity_weight: number;
  template_id: string;
  template_key: string;
  template_name: string;
  template_version: number;
  prompt_preset_id: string;
  parameter_profile_id: string;
  mode: string;
  policy_id: string;
}

export interface TrialCaseResult {
  case_id: string;
  question: string;
  returned: boolean;
  hit_at_k: number | null;
  hit_undefined_reason?: string;
  top_score: number;
  top_n_scores: number[];
  latency_ms: number;
  error_code?: string;
}

export interface TrialRun {
  id: string;
  scenario_template_id: string;
  template_version: number;
  assistant_id: string;
  assistant_kind: string;
  eval_set_id: string;
  eval_set_version: number;
  eval_set_hash: string;
  policy_id: string;
  policy_hash: string;
  mode: string;
  status: TrialRunStatus;
  failure_reason?: string;
  idempotency_key: string;
  config_snapshot: TrialRunConfigSnapshot;
  summary: TrialRunSummary;
  gate_eligible: boolean;
  cases?: TrialCaseResult[];
  created_at: string;
  started_at?: string;
  completed_at?: string;
}

export async function listTrialRuns(params: {
  scenario_template_id?: string;
  assistant_id?: string;
}): Promise<TrialRun[]> {
  const res = await api.get<{ code: number; data: { items: TrialRun[] } }>("/trial-runs", {
    params,
  });
  return res.data?.data?.items ?? [];
}

export async function getTrialRun(id: string): Promise<TrialRun> {
  const res = await api.get<{ code: number; data: TrialRun }>(`/trial-runs/${id}`);
  return res.data.data;
}

export async function cancelTrialRun(id: string): Promise<TrialRun> {
  const res = await api.post<{ code: number; data: TrialRun }>(`/trial-runs/${id}/cancel`, {});
  return res.data.data;
}

export interface OnboardingMilestone {
  key: "dataset_ready" | "assistant_ready" | "trial_completed" | "first_cited_answer";
  done: boolean;
  evidence: Record<string, unknown>;
}

export interface OnboardingReadiness {
  assistant_id: string;
  scenario_template_id?: string;
  completed: number;
  total: number;
  next?: string;
  milestones: OnboardingMilestone[];
}

export async function getOnboardingReadiness(assistantId?: string): Promise<OnboardingReadiness> {
  const res = await api.get<{ code: number; data: OnboardingReadiness }>("/onboarding/readiness", {
    params: assistantId ? { assistant_id: assistantId } : undefined,
  });
  return res.data.data;
}

/** Formats a hit rate without ever inventing 0% for an undefined metric. */
export function formatHitRate(summary: TrialRunSummary, undefinedLabel: string): string {
  if (summary.hit_at_k === null || summary.hit_denominator === 0) {
    return undefinedLabel;
  }
  return `${Math.round(summary.hit_at_k * 1000) / 10}% (${Math.round(summary.hit_at_k * summary.hit_denominator)}/${summary.hit_denominator})`;
}
