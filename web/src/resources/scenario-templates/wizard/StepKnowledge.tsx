import { useMemo, useState } from "react";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { Database, Loader2, RefreshCw, Wrench } from "lucide-react";
import type { DatasetLike } from "@/resources/chats/chat-types";
import { CapabilityBadge } from "./CapabilityBadge";
import type { DatasetCapabilityView } from "./types";
import type { ScenarioWizardApi } from "./useScenarioWizard";

interface StepKnowledgeProps {
  wizard: ScenarioWizardApi;
}

/**
 * Step 2 — bind datasets, then read the capability verdict. Two rules from
 * doc/276 §12 are enforced here rather than in prose:
 *  - a probe-limited dataset is labelled "unverified", never "passed";
 *  - repairing a shared dataset requires an explicit acknowledgement, and the
 *    affected applications are listed before the checkbox is offered.
 */
export function StepKnowledge({ wizard }: StepKnowledgeProps) {
  const t = useTranslate();
  const template = wizard.selection.template;
  const [acknowledged, setAcknowledged] = useState<Record<string, boolean>>({});

  const checkedIds = useMemo(
    () => new Set((wizard.capabilityCheck?.datasets ?? []).map((item) => item.dataset_id)),
    [wizard.capabilityCheck],
  );

  if (!template) return null;

  return (
    <section className="grid gap-4 lg:grid-cols-[1fr_1.1fr]">
      <div className="rounded border p-4">
        <h2 className="flex items-center gap-2 text-sm font-medium">
          <Database className="size-4" aria-hidden="true" />
          {t("scenarioTemplates.wizard.bindDatasets")}
        </h2>
        <p className="mt-1 text-xs text-muted-foreground">{t("scenarioTemplates.wizard.bindDatasetsHint")}</p>
        <div className="mt-3 max-h-72 space-y-1 overflow-y-auto rounded border p-2">
          {wizard.datasetValues.map((item: DatasetLike) => {
            const suggested = template.suggestedDatasetNames.some((keyword) => item.name.includes(keyword));
            const verdict = wizard.capabilityCheck?.datasets.find((row) => row.dataset_id === item.id);
            return (
              <label key={item.id} className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={wizard.selection.datasetIds.includes(item.id)}
                  onChange={() => wizard.toggleDataset(item.id)}
                />
                <span>{item.name}</span>
                {suggested ? (
                  <span className="rounded bg-primary/10 px-1 text-[10px] text-primary">
                    {t("scenarioTemplates.wizard.suggested")}
                  </span>
                ) : null}
                {verdict ? <CapabilityBadge state={verdict.verdict} label={t("scenarioTemplates.wizard.checked")} /> : null}
              </label>
            );
          })}
          {wizard.datasetValues.length === 0 ? (
            <p className="text-sm text-muted-foreground">{t("scenarioTemplates.wizard.noDatasets")}</p>
          ) : null}
        </div>
        <div className="mt-3 flex items-center gap-2">
          <Button variant="outline" disabled={wizard.isChecking} onClick={() => void wizard.runCapabilityCheck()}>
            {wizard.isChecking ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
            {t("scenarioTemplates.wizard.runCheck")}
          </Button>
          <span className="text-xs text-muted-foreground">
            {t("scenarioTemplates.wizard.checkScope", { version: wizard.capabilityCheck?.capability_set_version ?? "-" })}
          </span>
        </div>
        {wizard.capabilityError ? (
          <p className="mt-2 text-xs text-destructive" role="alert">
            {wizard.capabilityError}
          </p>
        ) : null}
        <SkeletonPanel wizard={wizard} />
      </div>

      <div className="space-y-3">
        {wizard.capabilityCheck ? (
          <>
            <div className="rounded border p-4 text-sm">
              <div className="flex items-center justify-between gap-2">
                <h2 className="font-medium">{t("scenarioTemplates.wizard.checkTitle")}</h2>
                <CapabilityBadge
                  state={wizard.capabilityCheck.verdict}
                  label={t(`scenarioTemplates.capability.verdict.${wizard.capabilityCheck.verdict}` as const, {
                    defaultValue: wizard.capabilityCheck.verdict,
                  })}
                />
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                {t("scenarioTemplates.wizard.probeStatus", { status: wizard.capabilityCheck.probe_status })}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">{t("scenarioTemplates.wizard.retrievalOnlyHint")}</p>
            </div>
            {wizard.capabilityCheck.datasets.map((row) => (
              <DatasetVerdictCard
                key={`${row.role}-${row.dataset_id}`}
                row={row}
                checked={checkedIds.has(row.dataset_id)}
                acknowledged={acknowledged[row.dataset_id] ?? false}
                onAcknowledge={(value) => setAcknowledged((prev) => ({ ...prev, [row.dataset_id]: value }))}
                onRepair={(chunkMethod) =>
                  void wizard.repairDataset(row.dataset_id, chunkMethod, acknowledged[row.dataset_id] ?? false)
                }
              />
            ))}
          </>
        ) : (
          <div className="rounded border p-4 text-sm text-muted-foreground">
            {t("scenarioTemplates.wizard.checkPending")}
          </div>
        )}
      </div>
    </section>
  );
}

interface DatasetVerdictCardProps {
  row: DatasetCapabilityView;
  checked: boolean;
  acknowledged: boolean;
  onAcknowledge: (value: boolean) => void;
  onRepair: (chunkMethod: string) => void;
}

function DatasetVerdictCard({ row, checked, acknowledged, onAcknowledge, onRepair }: DatasetVerdictCardProps) {
  const t = useTranslate();
  const shared = row.shared_usage?.count ?? 0;
  const repair = row.repairs.find((item) => item.action === "apply_parse_profile") ?? row.repairs[0];
  const chunkMethod = suggestedChunkMethod(row);
  return (
    <article className="rounded border p-4 text-sm">
      <header className="flex flex-wrap items-center gap-2">
        <h3 className="font-medium">{row.dataset_name ?? row.dataset_id}</h3>
        <CapabilityBadge state={row.verdict} label={t("scenarioTemplates.capability.role." + row.role, { defaultValue: row.role })} />
        {!checked ? (
          <span className="text-[11px] text-muted-foreground">{t("scenarioTemplates.wizard.unchecked")}</span>
        ) : null}
      </header>
      <ul className="mt-2 space-y-1">
        {row.requirements.map((requirement) => (
          <li key={requirement.capability} className="flex flex-wrap items-center gap-2 text-xs">
            <CapabilityBadge
              state={requirement.satisfied ? requirement.achieved : "blocked"}
              label={t(`scenarioTemplates.capability.name.${requirement.capability}`, {
                defaultValue: requirement.capability,
              })}
              detail={`${requirement.min_level} → ${requirement.achieved}`}
            />
            <span className="text-muted-foreground">
              {requirement.min_level} → {requirement.achieved}
              {requirement.reason ? ` · ${requirement.reason}` : ""}
            </span>
          </li>
        ))}
      </ul>
      <details className="mt-2 text-xs">
        <summary className="cursor-pointer text-muted-foreground">
          {t("scenarioTemplates.wizard.probeEvidence", { count: row.capabilities.length })}
        </summary>
        <ul className="mt-1 space-y-0.5">
          {row.capabilities.map((evidence) => (
            <li key={evidence.capability} className="text-muted-foreground">
              <span className="font-mono">{evidence.capability}</span>: {evidence.status} · {evidence.source}
              {evidence.note ? ` · ${evidence.note}` : ""}
            </li>
          ))}
        </ul>
      </details>
      {repair ? (
        <div className="mt-3 rounded bg-muted/40 p-2">
          <p className="flex items-center gap-1 text-xs font-medium">
            <Wrench className="size-3" aria-hidden="true" />
            {t("scenarioTemplates.wizard.repairAction", { action: repair.action })}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">{repair.reason}</p>
          {shared > 0 ? (
            <div className="mt-2 text-xs">
              <p className="font-medium text-amber-700">
                {t("scenarioTemplates.wizard.sharedWarning", { count: shared })}
              </p>
              <ul className="mt-1 list-disc pl-4 text-muted-foreground">
                {(row.shared_usage.chat_ids ?? []).map((id) => (
                  <li key={`chat-${id}`}>{t("scenarioTemplates.wizard.sharedChat", { id })}</li>
                ))}
                {(row.shared_usage.search_ids ?? []).map((id) => (
                  <li key={`search-${id}`}>{t("scenarioTemplates.wizard.sharedSearch", { id })}</li>
                ))}
              </ul>
              <label className="mt-1 flex items-center gap-2">
                <input type="checkbox" checked={acknowledged} onChange={(event) => onAcknowledge(event.target.checked)} />
                {t("scenarioTemplates.wizard.sharedAcknowledge")}
              </label>
            </div>
          ) : null}
          <Button
            className="mt-2"
            size="sm"
            variant="outline"
            disabled={shared > 0 && !acknowledged}
            onClick={() => onRepair(chunkMethod)}
          >
            {t("scenarioTemplates.wizard.repairApply")}
          </Button>
        </div>
      ) : null}
    </article>
  );
}

/**
 * The repair writes a chunk method. Prefer the method the probe already reported
 * for `chunk_method`; fall back to `naive` only when the evidence carries none —
 * the wizard never invents a method the backend did not describe.
 */
export function suggestedChunkMethod(row: DatasetCapabilityView): string {
  const evidence = row.capabilities.find((item) => item.capability === "chunk_method");
  const reported = (evidence?.note ?? "").match(/chunk_method=([a-z_]+)/)?.[1];
  if (reported) return reported;
  const achieved = row.requirements.find((item) => item.capability === "chunk_method")?.achieved;
  return achieved && achieved !== "none" ? achieved : "naive";
}

function SkeletonPanel({ wizard }: { wizard: ScenarioWizardApi }) {
  const t = useTranslate();
  const degraded = wizard.skeletonState === "degraded" || wizard.skeletonState === "error";
  return (
    <div className="mt-4 rounded border p-3 text-xs">
      <div className="flex items-center justify-between gap-2">
        <h3 className="font-medium">{t("scenarioTemplates.wizard.skeletonTitle")}</h3>
        <Button
          size="sm"
          variant="ghost"
          disabled={wizard.skeletonState === "loading"}
          onClick={() => void wizard.loadSkeletons()}
        >
          {wizard.skeletonState === "loading" ? <Loader2 className="size-3 animate-spin" /> : null}
          {t("scenarioTemplates.wizard.skeletonLoad")}
        </Button>
      </div>
      <p className="mt-1 text-muted-foreground">{t("scenarioTemplates.wizard.skeletonHint")}</p>
      {degraded ? (
        <p className="mt-1 text-amber-700" role="status">
          {t("scenarioTemplates.wizard.skeletonDegraded")}
        </p>
      ) : null}
      {wizard.skeletons.length > 0 ? (
        <ul className="mt-2 max-h-40 space-y-1 overflow-y-auto">
          {wizard.skeletons.map((item) => (
            <li key={item.id} className="flex items-center justify-between gap-2">
              <span className={cn("truncate", item.has_dsl ? "" : "text-muted-foreground")}>
                {item.title || item.upstream_id}
                <span className="ml-1 font-mono text-[10px] text-muted-foreground">{item.id}</span>
              </span>
              <Button size="sm" variant="outline" onClick={() => void wizard.importSkeleton(item.id)}>
                {t("scenarioTemplates.wizard.skeletonImport")}
              </Button>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
