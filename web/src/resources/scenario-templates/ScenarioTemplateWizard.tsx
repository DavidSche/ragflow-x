import { useMemo, useState } from "react";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { ArrowLeft, ArrowRight, LayoutTemplate, Loader2 } from "lucide-react";
import { StepApplication } from "./wizard/StepApplication";
import { StepKnowledge } from "./wizard/StepKnowledge";
import { StepScenario } from "./wizard/StepScenario";
import { StepVerify } from "./wizard/StepVerify";
import { useScenarioWizard } from "./wizard/useScenarioWizard";

const STEP_KEYS = ["scenario", "knowledge", "application", "verify"] as const;

/**
 * Four-step scenario wizard (doc/276 §12). Orchestration, network calls and
 * validation live in `useScenarioWizard`; each step is its own component so no
 * file crosses the 400-line ceiling in specification/26.
 *
 * Instantiation goes through the single `POST …/instantiate` endpoint — the
 * wizard no longer posts to `/search-apps` or `/agents` directly, and the local
 * agent DSL builder is gone (doc/276 §7.5 / C3).
 */
export const ScenarioTemplateWizard = () => {
  const t = useTranslate();
  const wizard = useScenarioWizard();
  const [step, setStep] = useState(0);

  const stepLabels = useMemo(
    () => STEP_KEYS.map((key) => t(`scenarioTemplates.wizard.stepLabel.${key}`)),
    [t],
  );

  const template = wizard.selection.template;
  const canAdvance = Boolean(template && wizard.selection.name.trim());
  const canCreate = Boolean(
    canAdvance && wizard.selection.projectId.trim() && wizard.selection.datasetIds.length > 0 && !wizard.saving,
  );

  return (
    <div className="mx-auto w-full max-w-6xl space-y-5 p-4">
      <header className="flex items-center gap-3">
        <LayoutTemplate className="size-6 text-primary" aria-hidden="true" />
        <div>
          <h1 className="text-xl font-semibold">{t("scenarioTemplates.wizard.title")}</h1>
          <p className="text-sm text-muted-foreground">{t("scenarioTemplates.wizard.subtitle")}</p>
        </div>
      </header>

      <ol className="flex flex-wrap items-center gap-2 text-sm" aria-label={t("scenarioTemplates.wizard.stepsAria")}>
        {stepLabels.map((label, index) => (
          <li
            key={label}
            aria-current={index === step ? "step" : undefined}
            className={cn(
              "rounded-full border px-3 py-1",
              index === step ? "border-primary bg-primary/5 text-primary" : "text-muted-foreground",
            )}
          >
            {index + 1}. {label}
          </li>
        ))}
      </ol>

      {step === 0 ? <StepScenario wizard={wizard} /> : null}
      {step === 1 ? <StepKnowledge wizard={wizard} /> : null}
      {step === 2 ? <StepApplication wizard={wizard} /> : null}
      {step === 3 ? <StepVerify wizard={wizard} /> : null}

      <footer className="flex flex-col gap-3 rounded border p-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="text-sm text-muted-foreground">
          {template
            ? t("scenarioTemplates.wizard.selected", {
                template: template.label,
                app: t(`scenarioTemplates.appType.${wizard.selection.appType}.label`),
                datasets: wizard.selectedDatasetNames.length,
              })
            : t("scenarioTemplates.wizard.pickFirst")}
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            disabled={step === 0 || wizard.saving}
            onClick={() => setStep((prev) => Math.max(0, prev - 1))}
          >
            <ArrowLeft className="size-4" /> {t("scenarioTemplates.wizard.previous")}
          </Button>
          {step < STEP_KEYS.length - 1 ? (
            <Button disabled={!canAdvance} onClick={() => setStep((prev) => Math.min(STEP_KEYS.length - 1, prev + 1))}>
              {t("scenarioTemplates.wizard.next")} <ArrowRight className="size-4" />
            </Button>
          ) : (
            <Button disabled={!canCreate} onClick={() => void wizard.createApplication()}>
              {wizard.saving ? <Loader2 className="size-4 animate-spin" /> : null}
              {wizard.saving ? t("scenarioTemplates.wizard.creating") : t("scenarioTemplates.wizard.create")}
            </Button>
          )}
        </div>
      </footer>
    </div>
  );
};
