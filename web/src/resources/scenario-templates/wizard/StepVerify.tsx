import { useTranslate } from "ra-core";
import { ClipboardCheck, ShieldAlert } from "lucide-react";
import { CapabilityBadge } from "./CapabilityBadge";
import type { ScenarioWizardApi } from "./useScenarioWizard";

interface StepVerifyProps {
  wizard: ScenarioWizardApi;
}

/**
 * Step 4 — questions, checklist and the gates that can refuse publication. The
 * eval gate is read from the catalog row (`eval_gate`) instead of being inferred
 * from the template status: a gated template that has not passed evaluation must
 * say so before the user invests in a trial run (doc/276 §9).
 */
export function StepVerify({ wizard }: StepVerifyProps) {
  const t = useTranslate();
  const template = wizard.selection.template;
  if (!template) return null;
  const gate = template.evalGate;
  const gateBlocked = gate.required && !gate.passed;

  return (
    <section className="grid gap-4 lg:grid-cols-[1.2fr_1fr]">
      <div className="rounded border p-4">
        <h2 className="flex items-center gap-2 text-sm font-medium">
          <ClipboardCheck className="size-4" aria-hidden="true" />
          {t("scenarioTemplates.wizard.trialQuestions")}
        </h2>
        <ul className="mt-2 list-disc space-y-1 pl-5 text-sm text-muted-foreground">
          {template.evaluationQuestions.map((question) => (
            <li key={question}>{question}</li>
          ))}
        </ul>
        <p className="mt-2 text-xs text-muted-foreground">{t("scenarioTemplates.wizard.trialHint")}</p>
        {template.evaluationQuestions.length === 0 ? (
          <p className="mt-2 text-xs text-muted-foreground">{t("scenarioTemplates.wizard.noQuestions")}</p>
        ) : null}
      </div>

      <div className="space-y-3">
        <div className="rounded border p-4 text-sm">
          <h2 className="font-medium">{t("scenarioTemplates.wizard.publishChecklist")}</h2>
          <ul className="mt-2 list-disc space-y-1 pl-5 text-muted-foreground">
            {template.publishChecklist.map((item) => (
              <li key={item}>{item}</li>
            ))}
          </ul>
          <p className="mt-3 text-xs text-muted-foreground">
            {t("scenarioTemplates.wizard.permissionAdvice", { advice: template.permissionAdvice })}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">
            {t("scenarioTemplates.wizard.expectedOutcome", { outcome: template.expectedOutcome })}
          </p>
        </div>

        <div className="rounded border p-4 text-sm">
          <h2 className="font-medium">{t("scenarioTemplates.wizard.gateTitle")}</h2>
          <div className="mt-2 flex flex-wrap items-center gap-2">
            <CapabilityBadge
              state={gateBlocked ? "blocked" : gate.required ? "satisfied" : "probe_limited"}
              label={
                gate.required
                  ? gateBlocked
                    ? t("scenarioTemplates.wizard.evalGateBlocked")
                    : t("scenarioTemplates.wizard.evalGatePassed")
                  : t("scenarioTemplates.wizard.evalGateNotRequired")
              }
            />
            {gate.runId ? (
              <span className="font-mono text-[11px] text-muted-foreground">
                {t("scenarioTemplates.wizard.evalRun", { id: gate.runId })}
              </span>
            ) : null}
          </div>
          {gate.reason ? <p className="mt-2 text-xs text-muted-foreground">{gate.reason}</p> : null}
          {wizard.capabilityCheck ? (
            <p className="mt-2 text-xs text-muted-foreground">
              {t("scenarioTemplates.wizard.checkSummary", {
                verdict: t(`scenarioTemplates.capability.verdict.${wizard.capabilityCheck.verdict}`, {
                  defaultValue: wizard.capabilityCheck.verdict,
                }),
                status: wizard.capabilityCheck.probe_status,
              })}
            </p>
          ) : null}
        </div>

        {gateBlocked ? (
          <div className="flex items-start gap-2 rounded border border-amber-500/40 bg-amber-500/5 p-3 text-xs">
            <ShieldAlert className="mt-0.5 size-4 shrink-0 text-amber-600" aria-hidden="true" />
            <p>{t("scenarioTemplates.wizard.evalGateBlockedHint")}</p>
          </div>
        ) : null}
      </div>
    </section>
  );
}
