import { useEffect, useState } from "react";
import { useTranslate } from "ra-core";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";
import { MessageSquare, Network, Search } from "lucide-react";
import { api } from "@/lib/api";
import type { ScenarioAppType } from "../scenario-templates";
import type { ScenarioWizardApi } from "./useScenarioWizard";

interface ModelOption {
  model_id: string;
  name: string;
  model_type: string;
}

const APP_TYPES: { value: ScenarioAppType; icon: typeof MessageSquare }[] = [
  { value: "chat", icon: MessageSquare },
  { value: "search", icon: Search },
  { value: "agent", icon: Network },
];

interface StepApplicationProps {
  wizard: ScenarioWizardApi;
}

/**
 * Step 3 — form, prompt and parameters. Copy and parameters are display-only:
 * the backend merges the template payload, so the preview cannot drift from the
 * created assistant (doc/228 T7.0). The only user-authored inputs are name,
 * project and the optional model override.
 */
export function StepApplication({ wizard }: StepApplicationProps) {
  const t = useTranslate();
  const template = wizard.selection.template;
  const [models, setModels] = useState<ModelOption[]>([]);

  useEffect(() => {
    if (wizard.selection.appType === "agent") return;
    let mounted = true;
    api
      .get<{ code: number; data: { chat?: ModelOption[] } }>("/chats/model-options")
      .then((res) => {
        if (mounted) setModels(res.data?.data?.chat ?? []);
      })
      .catch(() => {
        if (mounted) setModels([]);
      });
    return () => {
      mounted = false;
    };
  }, [wizard.selection.appType]);

  if (!template) return null;

  const available = APP_TYPES.filter((option) =>
    option.value === "agent" ? wizard.canManageAgent : option.value === "chat" ? wizard.canManageChat : true,
  );

  return (
    <section className="space-y-4">
      <div className="grid gap-3 md:grid-cols-3">
        {APP_TYPES.map((option) => {
          const declared = template.appTypes.includes(option.value);
          const renderable = template.renderableAppTypes.includes(option.value);
          const enabled = renderable && available.some((item) => item.value === option.value);
          const Icon = option.icon;
          const active = wizard.selection.appType === option.value;
          return (
            <button
              key={option.value}
              type="button"
              disabled={!enabled}
              onClick={() => wizard.setAppType(option.value)}
              aria-pressed={active}
              className={cn(
                "rounded border p-3 text-left transition-colors disabled:opacity-50",
                active ? "border-primary bg-primary/5" : "bg-background hover:bg-muted/40",
              )}
            >
              <span className="flex items-center gap-2 text-sm font-medium">
                <Icon className="size-4" aria-hidden="true" />
                {t(`scenarioTemplates.appType.${option.value}.label`)}
              </span>
              <p className="mt-1 text-xs text-muted-foreground">
                {t(`scenarioTemplates.appType.${option.value}.description`)}
              </p>
              {declared && !renderable ? (
                <p className="mt-1 text-xs text-amber-600">
                  {t("scenarioTemplates.wizard.appTypeUnavailable")}
                </p>
              ) : null}
            </button>
          );
        })}
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label htmlFor="scenario-name">{t("scenarioTemplates.wizard.name")}</Label>
          <Input
            id="scenario-name"
            value={wizard.selection.name}
            onChange={(event) => wizard.setName(event.target.value)}
            placeholder={t("scenarioTemplates.wizard.namePlaceholder")}
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="scenario-project">{t("scenarioTemplates.wizard.project")}</Label>
          <Input
            id="scenario-project"
            value={wizard.selection.projectId}
            onChange={(event) => wizard.setProjectId(event.target.value)}
            placeholder={t("scenarioTemplates.wizard.projectPlaceholder")}
          />
        </div>
      </div>

      {wizard.selection.appType !== "agent" ? (
        <div className="space-y-1.5">
          <Label htmlFor="scenario-model">{t("scenarioTemplates.wizard.model")}</Label>
          <select
            id="scenario-model"
            className="w-full rounded border bg-background px-2 py-1.5 text-sm"
            value={wizard.selection.modelId}
            onChange={(event) => wizard.setModelId(event.target.value)}
          >
            <option value="">{t("scenarioTemplates.wizard.modelDefault")}</option>
            {models.map((option) => (
              <option key={option.model_id} value={option.model_id}>
                {option.name}
              </option>
            ))}
          </select>
          <p className="text-xs text-muted-foreground">{t("scenarioTemplates.wizard.modelHint")}</p>
        </div>
      ) : null}

      <div className="rounded border p-4 text-sm">
        <h2 className="font-medium">{t("scenarioTemplates.wizard.configTitle")}</h2>
        <p className="mt-1 text-xs text-muted-foreground">{t("scenarioTemplates.wizard.configHint")}</p>
        <dl className="mt-3 grid gap-2 sm:grid-cols-2">
          <div>
            <dt className="text-xs text-muted-foreground">{t("scenarioTemplates.wizard.configTopN")}</dt>
            <dd>{template.parameters.top_n}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">{t("scenarioTemplates.wizard.configSimilarity")}</dt>
            <dd>{template.parameters.similarity_threshold}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">{t("scenarioTemplates.wizard.configTemperature")}</dt>
            <dd>{template.parameters.temperature}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">{t("scenarioTemplates.wizard.configMaxTokens")}</dt>
            <dd>{template.parameters.max_tokens}</dd>
          </div>
        </dl>
        {template.prompt.system ? (
          <div className="mt-3">
            <p className="text-xs text-muted-foreground">{t("scenarioTemplates.wizard.configPrompt")}</p>
            <p className="mt-1 whitespace-pre-line rounded bg-muted/40 p-2 text-xs">{template.prompt.system}</p>
          </div>
        ) : null}
      </div>
    </section>
  );
}
