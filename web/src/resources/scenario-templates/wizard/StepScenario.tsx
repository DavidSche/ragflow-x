import { useTranslate } from "ra-core";
import { cn } from "@/lib/utils";
import { Loader2 } from "lucide-react";
import type { ScenarioTemplateView } from "../scenario-templates";
import type { ScenarioWizardApi } from "./useScenarioWizard";

interface StepScenarioProps {
  wizard: ScenarioWizardApi;
}

/** Step 1 — pick a scenario. The row count and copy come from the server. */
export function StepScenario({ wizard }: StepScenarioProps) {
  const t = useTranslate();
  if (wizard.catalogState === "loading") {
    return (
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" aria-hidden="true" />
        {t("scenarioTemplates.wizard.catalogLoading")}
      </p>
    );
  }
  if (wizard.catalogState === "error") {
    return (
      <div className="rounded border border-destructive/40 p-4 text-sm">
        <p>{t("scenarioTemplates.wizard.catalogFailed")}</p>
        <button type="button" className="mt-2 text-primary underline" onClick={wizard.reloadCatalog}>
          {t("scenarioTemplates.wizard.retry")}
        </button>
      </div>
    );
  }
  if (wizard.templates.length === 0) {
    return (
      <div className="rounded border p-4 text-sm">
        <p className="font-medium">{t("scenarioTemplates.wizard.catalogEmptyTitle")}</p>
        <p className="mt-1 text-xs text-muted-foreground">{t("scenarioTemplates.wizard.catalogEmpty")}</p>
      </div>
    );
  }
  const selectedId = wizard.selection.template?.id ?? "";
  return (
    <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-3" aria-label={t("scenarioTemplates.wizard.stepScenario")}>
      {wizard.templates.map((item: ScenarioTemplateView) => {
        const gateBlocked = item.evalGate.required && !item.evalGate.passed;
        return (
          <button
            key={item.id}
            type="button"
            onClick={() => wizard.selectTemplate(item)}
            aria-pressed={selectedId === item.id}
            className={cn(
              "rounded border p-4 text-left transition-colors",
              selectedId === item.id ? "border-primary bg-primary/5" : "bg-background hover:bg-muted/40",
            )}
          >
            <h2 className="text-sm font-medium">{item.label}</h2>
            <p className="mt-1 text-xs text-muted-foreground">{item.description}</p>
            <div className="mt-3 flex flex-wrap gap-1">
              {item.appTypes.map((type) => (
                <span key={type} className="rounded bg-muted px-1.5 py-0.5 text-[11px]">
                  {t(`scenarioTemplates.appType.${type}.label`)}
                </span>
              ))}
              {item.source === "builtin" ? (
                <span className="rounded bg-primary/10 px-1.5 py-0.5 text-[11px] text-primary">
                  {t("scenarioTemplates.wizard.baselineBadge", { version: item.baselineVersion || item.latestVersion })}
                </span>
              ) : null}
              {gateBlocked ? (
                <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-[11px] text-amber-700">
                  {t("scenarioTemplates.wizard.evalGateBlocked")}
                </span>
              ) : null}
            </div>
            {item.sampleQuestions.length > 0 ? (
              <p className="mt-3 text-[11px] text-muted-foreground">
                {t("scenarioTemplates.wizard.sampleHint", { question: item.sampleQuestions[0] })}
              </p>
            ) : null}
          </button>
        );
      })}
    </section>
  );
}
