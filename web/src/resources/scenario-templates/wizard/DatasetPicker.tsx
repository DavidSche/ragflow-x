import { useTranslate } from "ra-core";
import type { DatasetLike } from "@/resources/chats/chat-types";
import { CapabilityBadge } from "./CapabilityBadge";
import type { ScenarioWizardApi } from "./useScenarioWizard";

/**
 * The step-3 dataset list, split out to keep StepKnowledge under the 400-line
 * ceiling (doc/276 §12). Two facts live here:
 *  - `datasetValues` is already tenant-scoped by the hook, so every row is
 *    bindable; cross-workspace rows never reach this list.
 *  - the hidden count is disclosed instead of silently dropped: an operator who
 *    can *read* another workspace's datasets must learn why they cannot bind
 *    one (4.151 drill 2026-11-17, 400/41120 at the last step).
 */
export function DatasetPicker({
  wizard,
  suggestedNames,
}: {
  wizard: Pick<
    ScenarioWizardApi,
    "datasetValues" | "selection" | "toggleDataset" | "capabilityCheck" | "hiddenDatasetCount"
  >;
  suggestedNames: string[];
}) {
  const t = useTranslate();
  return (
    <>
      <div className="mt-3 max-h-72 space-y-1 overflow-y-auto rounded border p-2">
        {wizard.datasetValues.map((item: DatasetLike) => {
          const suggested = suggestedNames.some((keyword) => item.name.includes(keyword));
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
      {wizard.hiddenDatasetCount > 0 ? (
        <p className="mt-2 text-xs text-muted-foreground">
          {t("scenarioTemplates.wizard.hiddenDatasets", { count: String(wizard.hiddenDatasetCount) })}
        </p>
      ) : null}
    </>
  );
}
