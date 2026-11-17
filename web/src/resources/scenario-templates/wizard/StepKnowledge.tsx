import { useMemo, useState } from "react";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { Database, Loader2, RefreshCw, Settings2, ShieldX, Wrench } from "lucide-react";
import { CapabilityBadge, requirementBadgeState } from "./CapabilityBadge";
import { DatasetPicker } from "./DatasetPicker";
import {
  capabilityName,
  capabilityReason,
  capabilityRole,
  capabilityVerdict,
  translateOr,
} from "./capabilityLabels";
import {
  buildPushdownRequest,
  buildRepairRequest,
  repairBlockers,
  repairChannel,
  repairReparseCount,
  repairTargetMethod,
} from "./repairPlan";
import type { PushdownRequest, RepairRequest } from "./repairPlan";
import type { CapabilityRepairView, DatasetCapabilityView } from "./types";
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
  // Two different consents: the shared-dataset impact (40917) and the re-parse
  // of already-parsed documents (40915). Collapsing them would let one checkbox
  // authorise two different destructive writes.
  const [acknowledged, setAcknowledged] = useState<Record<string, boolean>>({});
  const [reparseAcknowledged, setReparseAcknowledged] = useState<Record<string, boolean>>({});

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
        <DatasetPicker wizard={wizard} suggestedNames={template.suggestedDatasetNames} />
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
                  label={capabilityVerdict(t, wizard.capabilityCheck.verdict)}
                />
              </div>
              <p className="mt-1 text-xs text-muted-foreground">
                {t("scenarioTemplates.wizard.probeStatus", { status: wizard.capabilityCheck.probe_status })}
              </p>
              <p className="mt-1 text-xs text-muted-foreground">{t("scenarioTemplates.wizard.retrievalOnlyHint")}</p>
              {wizard.checkStale ? (
                <p className="mt-2 rounded border border-amber-500/40 bg-amber-500/5 p-2 text-xs text-amber-700" role="status">
                  {t("scenarioTemplates.wizard.checkStaleBackfill")}
                </p>
              ) : null}
            </div>
            {wizard.capabilityCheck.datasets.map((row) => (
              <DatasetVerdictCard
                key={`${row.role}-${row.dataset_id}`}
                row={row}
                checked={checkedIds.has(row.dataset_id)}
                acknowledged={acknowledged[row.dataset_id] ?? false}
                reparseAcknowledged={reparseAcknowledged[row.dataset_id] ?? false}
                onAcknowledge={(value) => setAcknowledged((prev) => ({ ...prev, [row.dataset_id]: value }))}
                onReparseAcknowledge={(value) =>
                  setReparseAcknowledged((prev) => ({ ...prev, [row.dataset_id]: value }))
                }
                onRepair={(body) => void wizard.repairDataset(row.dataset_id, body)}
                onRepairPushdown={(body) => void wizard.repairDatasetPushdown(row.dataset_id, body)}
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
  reparseAcknowledged: boolean;
  onAcknowledge: (value: boolean) => void;
  onReparseAcknowledge: (value: boolean) => void;
  onRepair: (body: RepairRequest) => void;
  onRepairPushdown: (body: PushdownRequest) => void;
}

function DatasetVerdictCard({
  row,
  checked,
  acknowledged,
  reparseAcknowledged,
  onAcknowledge,
  onReparseAcknowledge,
  onRepair,
  onRepairPushdown,
}: DatasetVerdictCardProps) {
  const t = useTranslate();
  const shared = row.shared_usage?.count ?? 0;
  const confirmation = { acknowledgeSharedImpact: acknowledged, reparseAcknowledged };
  return (
    <article className="rounded border p-4 text-sm">
      <header className="flex flex-wrap items-center gap-2">
        <h3 className="font-medium">{row.dataset_name ?? row.dataset_id}</h3>
        <CapabilityBadge state={row.verdict} label={capabilityRole(t, row.role)} />
        {!checked ? (
          <span className="text-[11px] text-muted-foreground">{t("scenarioTemplates.wizard.unchecked")}</span>
        ) : null}
      </header>
      <ul className="mt-2 space-y-1">
        {row.requirements.map((requirement) => (
          <li key={requirement.capability} className="flex flex-wrap items-center gap-2 text-xs">
            <CapabilityBadge
              state={requirementBadgeState(requirement)}
              label={capabilityName(t, requirement.capability)}
              detail={`${requirement.min_level} → ${requirement.achieved}`}
            />
            <span className="text-muted-foreground">
              {requirement.min_level} → {requirement.achieved}
              {requirement.reason ? ` · ${capabilityReason(t, requirement.reason)}` : ""}
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
      {shared > 0 ? (
        <div className="mt-3 rounded border border-amber-500/40 bg-amber-500/5 p-2 text-xs">
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
      {row.repairs.length > 0 ? (
        <div className="mt-3 space-y-2">
          {row.repairs.map((repair) => (
            <RepairRow
              key={repair.action}
              row={row}
              repair={repair}
              confirmation={confirmation}
              reparseAcknowledged={reparseAcknowledged}
              onReparseAcknowledge={onReparseAcknowledge}
              onRepair={onRepair}
              onRepairPushdown={onRepairPushdown}
            />
          ))}
        </div>
      ) : null}
    </article>
  );
}

interface RepairRowProps {
  row: DatasetCapabilityView;
  repair: CapabilityRepairView;
  confirmation: { acknowledgeSharedImpact: boolean; reparseAcknowledged: boolean };
  reparseAcknowledged: boolean;
  onReparseAcknowledge: (value: boolean) => void;
  onRepair: (body: RepairRequest) => void;
  onRepairPushdown: (body: PushdownRequest) => void;
}

/**
 * One repair, one channel. The action id decides what the button writes, so a
 * dataset whose only gap is metadata (`enable_pushdown_and_sync_meta`) can never
 * reach the parse-profile writer, and a repair the wizard cannot drive
 * (`reconfigure_dataset`, `remove_requirement`) gets no button at all —
 * doc/285 §10.
 */
function RepairRow({
  row,
  repair,
  confirmation,
  reparseAcknowledged,
  onReparseAcknowledge,
  onRepair,
  onRepairPushdown,
}: RepairRowProps) {
  const t = useTranslate();
  const channel = repairChannel(repair);
  const label = translateOr(t, `scenarioTemplates.wizard.repairAction.${repair.action}`, repair.action);
  const target = channel === "parse_profile" ? repairTargetMethod(repair, row) : "";
  const reparseCount = channel === "parse_profile" ? repairReparseCount(repair, row) : 0;
  const blockers = repairBlockers(row, repair, confirmation);
  const heading = (
    <p className="flex items-center gap-1 text-xs font-medium">
      {channel === "blocked" ? (
        <ShieldX className="size-3" aria-hidden="true" />
      ) : channel === "manual" ? (
        <Settings2 className="size-3" aria-hidden="true" />
      ) : (
        <Wrench className="size-3" aria-hidden="true" />
      )}
      {t("scenarioTemplates.wizard.repairActionLabel")} {label}
    </p>
  );
  const reason = repair.reason ? (
    <p className="mt-1 font-mono text-[11px] text-muted-foreground">
      {t("scenarioTemplates.wizard.repairReasonCaption")} {repair.reason}
    </p>
  ) : null;

  if (channel === "blocked" || channel === "manual") {
    return (
      <div className="rounded bg-muted/40 p-2">
        {heading}
        <p className="mt-1 text-xs text-muted-foreground">
          {channel === "blocked"
            ? t("scenarioTemplates.wizard.repairBlockedHint")
            : t("scenarioTemplates.wizard.repairManualHint")}
        </p>
        {reason}
      </div>
    );
  }

  const applying =
    channel === "parse_profile"
      ? () => onRepair(buildRepairRequest(row, repair, confirmation))
      : () => onRepairPushdown(buildPushdownRequest(row, confirmation));
  let hint = t("scenarioTemplates.wizard.repairPushdownHint");
  if (channel === "parse_profile") {
    if (target === "") hint = t("scenarioTemplates.wizard.repairTargetUnknown");
    else if (reparseCount > 0) hint = t("scenarioTemplates.wizard.repairParseHint", { method: target, count: reparseCount });
    else hint = t("scenarioTemplates.wizard.repairParseHintEmpty", { method: target });
  }

  return (
    <div className="rounded bg-muted/40 p-2">
      {heading}
      <p className="mt-1 text-xs text-muted-foreground">{hint}</p>
      {reason}
      {channel === "parse_profile" && reparseCount > 0 ? (
        <div className="mt-2 text-xs">
          <p className="font-medium text-amber-700">
            {t("scenarioTemplates.wizard.repairReparseWarning", { count: reparseCount })}
          </p>
          <label className="mt-1 flex items-center gap-2">
            <input
              type="checkbox"
              checked={reparseAcknowledged}
              onChange={(event) => onReparseAcknowledge(event.target.checked)}
            />
            {t("scenarioTemplates.wizard.repairReparseAcknowledge", { count: reparseCount })}
          </label>
        </div>
      ) : null}
      <Button className="mt-2" size="sm" variant="outline" disabled={blockers.length > 0} onClick={applying}>
        {channel === "parse_profile"
          ? t("scenarioTemplates.wizard.repairApply")
          : t("scenarioTemplates.wizard.repairPushdownApply")}
        {target ? <span className="ml-1 font-mono text-[10px] text-muted-foreground">{target}</span> : null}
      </Button>
    </div>
  );
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
