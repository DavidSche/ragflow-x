import { useState } from "react";
import { useCanAccess, useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Loader2, Route } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { NumericRangeField } from "@/components/NumericRangeField";
import { clampNumber } from "@/lib/numeric";

interface AssistantRouteGovernanceProps {
  kind: "chat" | "agent";
  targetId: string;
  name: string;
}

interface AssistantGovernanceForm {
  tenantRouteMode: string;
  autoSelect: boolean;
  assistantRisk: string;
  workflowRisk: string;
  agentFlowReadiness: string;
  keywords: string;
  examples: string;
}

interface RouteEvaluationMetrics {
  case_count: number;
  evaluated_count: number;
  candidate_recall: number;
  top1_accuracy: number;
  top3_recall: number;
  no_match_rate: number;
  wrong_route_rate: number;
  wrong_execution_rate: number;
  auto_execute_count: number;
  auto_execute_wrong_rate: number;
  abstain_rate: number;
  route_p95_ms: number;
  route_cost: number;
  route_timing_sample_count: number;
  gate_state: string;
  gate_failures?: string[];
  candidate_distribution?: Array<{ candidate_key: string; rank: number; count: number }>;
}

interface RouteEvaluationRun {
  id: string;
  name: string;
  source: string;
  gate_state: string;
  allow_auto_low_risk: boolean;
  created_at: string;
  metrics_json: string;
}

const parseRouteMetrics = (run: RouteEvaluationRun | null): RouteEvaluationMetrics | null => {
  if (!run) return null;
  try {
    return JSON.parse(run.metrics_json) as RouteEvaluationMetrics;
  } catch {
    return null;
  }
};

const formatRouteMetric = (value: number, percent = true) =>
  percent ? `${Math.round((value || 0) * 1000) / 10}%` : `${value || 0}`;

const RouteEvaluationSummary = ({ run }: { run: RouteEvaluationRun | null }) => {
  const t = useTranslate();
  const metrics = parseRouteMetrics(run);
  if (!metrics) return <p className="text-sm text-muted-foreground">{t("conversationCenter.route_evaluation_empty")}</p>;
  const rows = [
    { key: "route_evaluation_candidate_recall", value: formatRouteMetric(metrics.candidate_recall) },
    { key: "route_evaluation_top1", value: formatRouteMetric(metrics.top1_accuracy) },
    { key: "route_evaluation_top3", value: formatRouteMetric(metrics.top3_recall) },
    { key: "route_evaluation_no_match", value: formatRouteMetric(metrics.no_match_rate) },
    { key: "route_evaluation_wrong_route", value: formatRouteMetric(metrics.wrong_route_rate) },
    { key: "route_evaluation_wrong_execution", value: formatRouteMetric(metrics.wrong_execution_rate) },
    { key: "route_evaluation_auto_execute_wrong", value: formatRouteMetric(metrics.auto_execute_wrong_rate) },
    { key: "route_evaluation_abstain", value: formatRouteMetric(metrics.abstain_rate) },
    { key: "route_evaluation_p95", value: `${metrics.route_p95_ms || 0} ms` },
    { key: "route_evaluation_cost", value: formatRouteMetric(metrics.route_cost, false) },
  ];
  const distribution = (metrics.candidate_distribution ?? [])
    .slice()
    .sort((left, right) => right.count - left.count)
    .slice(0, 4);

  return (
    <section className="rounded border p-3" aria-label={t("conversationCenter.route_evaluation_title")}>
      <div className="mb-2 flex items-center justify-between gap-2">
        <p className="text-sm font-medium">{t("conversationCenter.route_evaluation_title")}</p>
        <span className="text-xs text-muted-foreground">{metrics.gate_state}</span>
      </div>
      <dl className="grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
        {rows.map((row) => (
          <div key={row.key} className="flex justify-between gap-2">
            <dt className="text-muted-foreground">{t(`conversationCenter.${row.key}`)}</dt>
            <dd className="font-mono">{row.value}</dd>
          </div>
        ))}
      </dl>
      <div className="mt-2 text-xs">
        <p className="text-muted-foreground">{t("conversationCenter.route_evaluation_candidate_distribution")}</p>
        {distribution.length === 0 ? (
          <p className="font-mono">{t("conversationCenter.route_evaluation_distribution_empty")}</p>
        ) : (
          <ul className="mt-1 space-y-0.5">
            {distribution.map((item) => (
              <li key={`${item.candidate_key}-${item.rank}`} className="font-mono">
                {item.candidate_key} · #{item.rank} · {item.count}
              </li>
            ))}
          </ul>
        )}
      </div>
      {(metrics.gate_failures ?? []).length > 0 ? (
        <p className="mt-2 text-xs text-destructive">{(metrics.gate_failures ?? []).join(", ")}</p>
      ) : null}
    </section>
  );
};

const splitList = (value: string) => value.split(",").map((item) => item.trim()).filter(Boolean);

export function AssistantRouteGovernanceButton({ kind, targetId, name }: AssistantRouteGovernanceProps) {
  const t = useTranslate();
  const notify = useNotify();
  const { canAccess } = useCanAccess({ resource: "assistant", action: "manage" });
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [evaluation, setEvaluation] = useState<RouteEvaluationRun | null>(null);
  const [form, setForm] = useState<AssistantGovernanceForm>({
    tenantRouteMode: "recommend_only", autoSelect: false, assistantRisk: "low",
    workflowRisk: "low", agentFlowReadiness: "0", keywords: "", examples: "",
  });

  if (!canAccess) return null;

  const load = async () => {
    setLoading(true);
    try {
      const [response, policyResponse, evaluationResponse] = await Promise.all([
        api.get<{ code: number; data?: { items?: Array<Record<string, unknown>> } }>(
          `/conversation/assistants?kind=${kind}&query=${encodeURIComponent(name)}`,
        ),
        api.get<{ code: number; data?: { auto_route_mode?: string } }>("/conversation/routing/policy"),
        api.get<{ code: number; data?: { items?: RouteEvaluationRun[] } }>("/conversation/routing/evaluations?page=1&page_size=1")
          .catch(() => null),
      ]);
      const item = response.data.data?.items?.find((candidate) => candidate.id === targetId);
      if (!item) throw new ApiError(response.status, response.data.code, t("conversationCenter.route_governance_not_found"));
      setEvaluation(evaluationResponse?.data.data?.items?.[0] ?? null);
      setForm({
        tenantRouteMode: String(policyResponse.data.data?.auto_route_mode ?? "recommend_only"),
        autoSelect: Boolean(item.auto_select_enabled),
        assistantRisk: String(item.assistant_risk_level ?? "low"),
        workflowRisk: String(item.workflow_risk_upper_bound ?? "low"),
        agentFlowReadiness: String(item.agent_flow_readiness ?? (kind === "agent" ? "0" : "1")),
        keywords: Array.isArray(item.keywords) ? item.keywords.join(", ") : "",
        examples: Array.isArray(item.examples) ? item.examples.join(", ") : "",
      });
      setOpen(true);
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("conversationCenter.route_governance_load_failed"), { type: "error" });
    } finally {
      setLoading(false);
    }
  };

  const save = async () => {
    setSaving(true);
    try {
        await api.put(`/conversation/assistants/${kind}/${encodeURIComponent(targetId)}`, {
        keywords: splitList(form.keywords),
        examples: splitList(form.examples),
        assistant_risk_level: form.assistantRisk,
        workflow_risk_upper_bound: form.workflowRisk,
        agent_flow_readiness: clampNumber(Number(form.agentFlowReadiness), 0, 1, kind === "agent" ? 0 : 1),
        auto_select_enabled: form.autoSelect,
        });
        await api.put("/conversation/routing/policy", { auto_route_mode: form.tenantRouteMode });
      notify(t("conversationCenter.route_governance_saved"), { type: "success" });
      setOpen(false);
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("conversationCenter.route_governance_save_failed"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <Button type="button" variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
        {loading ? <Loader2 className="size-4 animate-spin" aria-hidden /> : <Route className="size-4" aria-hidden />}
        {t("conversationCenter.route_governance")}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t("conversationCenter.route_governance_title")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <RouteEvaluationSummary run={evaluation} />
            <div className="flex items-center justify-between rounded border p-3">
              <div>
                <Label htmlFor="route-auto-select">{t("conversationCenter.route_auto_select")}</Label>
                <p className="text-xs text-muted-foreground">{t("conversationCenter.route_auto_select_hint")}</p>
              </div>
              <Switch id="route-auto-select" checked={form.autoSelect} onCheckedChange={(checked) => setForm((current) => ({ ...current, autoSelect: checked }))} />
            </div>
            <div className="grid gap-3 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="route-tenant-mode">{t("conversationCenter.route_tenant_mode")}</Label>
                <select id="route-tenant-mode" className="h-9 w-full rounded border bg-background px-2 text-sm" value={form.tenantRouteMode} onChange={(event) => setForm((current) => ({ ...current, tenantRouteMode: event.target.value }))}>
                  <option value="disabled">{t("conversationCenter.route_mode_disabled")}</option>
                  <option value="recommend_only">{t("conversationCenter.route_mode_recommend_only")}</option>
                  <option value="auto_low_risk">{t("conversationCenter.route_mode_auto_low_risk")}</option>
                </select>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="route-assistant-risk">{t("conversationCenter.route_assistant_risk")}</Label>
                <select id="route-assistant-risk" className="h-9 w-full rounded border bg-background px-2 text-sm" value={form.assistantRisk} onChange={(event) => setForm((current) => ({ ...current, assistantRisk: event.target.value }))}>
                  <option value="low">low</option>
                  <option value="medium">medium</option>
                  <option value="high">high</option>
                </select>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="route-workflow-risk">{t("conversationCenter.route_workflow_risk")}</Label>
                <select id="route-workflow-risk" className="h-9 w-full rounded border bg-background px-2 text-sm" value={form.workflowRisk} onChange={(event) => setForm((current) => ({ ...current, workflowRisk: event.target.value }))}>
                  <option value="low">low</option>
                  <option value="medium">medium</option>
                  <option value="high">high</option>
                </select>
              </div>
              {kind === "agent" ? (
                <div>
                  <NumericRangeField
                    id="route-agent-flow-readiness"
                    label={t("conversationCenter.route_agent_flow_readiness")}
                    value={form.agentFlowReadiness}
                    min={0}
                    max={1}
                    step={0.1}
                    onChange={(next) => setForm((current) => ({ ...current, agentFlowReadiness: String(next ?? 0) }))}
                  />
                  <p className="text-xs text-muted-foreground">{t("conversationCenter.route_agent_flow_readiness_hint")}</p>
                </div>
              ) : null}
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="route-keywords">{t("conversationCenter.route_keywords")}</Label>
              <Input id="route-keywords" value={form.keywords} onChange={(event) => setForm((current) => ({ ...current, keywords: event.target.value }))} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="route-examples">{t("conversationCenter.route_examples")}</Label>
              <Input id="route-examples" value={form.examples} onChange={(event) => setForm((current) => ({ ...current, examples: event.target.value }))} />
            </div>
            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={() => setOpen(false)}>{t("ra.action.cancel")}</Button>
              <Button type="button" onClick={() => void save()} disabled={saving}>
                {saving ? <Loader2 className="size-4 animate-spin" aria-hidden /> : t("ra.action.save")}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}
