import { useEffect, useMemo, useState } from "react";
import {
  useCanAccess,
  useCreatePath,
  useGetList,
  useLocale,
  useNavigate,
  useNotify,
  useTranslate,
} from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";
import { api, ApiError } from "../../lib/api";
import {
  MessageSquare,
  Search,
  Network,
  ArrowLeft,
  ArrowRight,
  ClipboardCheck,
  Database,
  Loader2,
  LayoutTemplate,
} from "lucide-react";
import { datasetOptions } from "../chats/chat-types";
import type { DatasetLike } from "../chats/chat-types";
import {
  fetchScenarioTemplateCatalog,
  instantiateScenarioChat,
  type ScenarioAppType,
  type ScenarioTemplateView,
} from "./scenario-templates";
import { createScenarioAgentDsl } from "./scenario-agent-dsl";
import { AGENT_CANVAS_CATEGORY } from "../agents/dslUtils";

interface ModelOption {
  model_id: string;
  name: string;
  model_type: string;
}

interface ModelOptionsResponse {
  chat?: ModelOption[];
  defaults?: ModelOption[];
}

const APP_TYPES: { value: ScenarioAppType; icon: typeof MessageSquare }[] = [
  { value: "chat", icon: MessageSquare },
  { value: "search", icon: Search },
  { value: "agent", icon: Network },
];

/**
 * Three-step scenario wizard. Template copy, sample questions, evaluation
 * questions, dataset suggestions, the resolved system prompt and the retrieval
 * parameters all come from the backend catalog; the wizard posts no prompt or
 * parameter override, so the server applies the template payload and the
 * preview cannot drift from the created assistant (doc/228 T7.0).
 */
export const ScenarioTemplateWizard = () => {
  const t = useTranslate();
  const notify = useNotify();
  const navigate = useNavigate();
  const createPath = useCreatePath();
  const [locale] = useLocale();
  const [step, setStep] = useState(0);
  const [templates, setTemplates] = useState<ScenarioTemplateView[]>([]);
  const [catalogState, setCatalogState] = useState<"loading" | "ready" | "error">("loading");
  const [reloadKey, setReloadKey] = useState(0);
  const [templateId, setTemplateId] = useState<string>("");
  const [appType, setAppType] = useState<ScenarioAppType>("chat");
  const [name, setName] = useState("");
  const [modelId, setModelId] = useState("");
  const [selectedDatasets, setSelectedDatasets] = useState<string[]>([]);
  const [modelOptions, setModelOptions] = useState<ModelOptionsResponse | null>(null);
  const [saving, setSaving] = useState(false);

  const { data: datasets } = useGetList("datasets", { pagination: { page: 1, perPage: 200 } });
  const { values: datasetValues, mapId } = datasetOptions(datasets as DatasetLike[] | undefined);

  const { canAccess: canManageChat } = useCanAccess({ resource: "chat", action: "manage" });
  const { canAccess: canManageAgent } = useCanAccess({ resource: "agent", action: "manage" });

  useEffect(() => {
    let mounted = true;
    setCatalogState("loading");
    fetchScenarioTemplateCatalog(locale)
      .then((rows) => {
        if (!mounted) return;
        setTemplates(rows);
        setCatalogState("ready");
      })
      .catch(() => {
        if (!mounted) return;
        setTemplates([]);
        setCatalogState("error");
      });
    return () => {
      mounted = false;
    };
  }, [locale, reloadKey]);

  const availableAppTypes = useMemo(
    () =>
      APP_TYPES.filter(({ value }) =>
        value === "agent" ? canManageAgent !== false : value === "chat" ? canManageChat !== false : true,
      ),
    [canManageAgent, canManageChat],
  );

  const template = templates.find((item) => item.id === templateId);

  useEffect(() => {
    if (!template || appType === "agent") return;
    const endpoint = appType === "chat" ? "/chats/model-options" : "/search-apps/model-options";
    let mounted = true;
    api
      .get<{ code: number; data: ModelOptionsResponse }>(endpoint)
      .then((res) => {
        if (!mounted) return;
        setModelOptions(res.data?.data ?? {});
        const defaultModel = (res.data?.data?.defaults ?? []).find((item) => item.model_type === "chat");
        setModelId(defaultModel?.model_id ?? "");
      })
      .catch(() => {
        if (mounted) {
          setModelOptions({});
          setModelId("");
        }
      });
    return () => {
      mounted = false;
    };
  }, [appType, template]);

  const selectTemplate = (item: ScenarioTemplateView) => {
    setTemplateId(item.id);
    setAppType(item.appTypes[0] ?? "chat");
    setName(item.label);
    setModelId("");
  };

  const toggleDataset = (datasetId: string) => {
    setSelectedDatasets((prev) =>
      prev.includes(datasetId) ? prev.filter((id) => id !== datasetId) : [...prev, datasetId],
    );
  };

  const startTrialRun = async (templateID: string, assistantID: string) => {
    try {
      const res = await api.post<{ code: number; data: { id: string } }>("/trial-runs", {
        scenario_template_id: templateID,
        assistant_id: assistantID,
        idempotency_key: `wizard-${assistantID}-${Date.now()}`,
      });
      const runID = res.data?.data?.id;
      if (runID) {
        navigate(createPath({ resource: "trial-runs", type: "show", id: runID }));
        return;
      }
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("scenarioTemplates.trial.startFailed"), {
        type: "warning",
      });
    }
    navigate(createPath({ resource: "chats", type: "show", id: assistantID }));
  };

  const createApp = async () => {
    if (!template || !name.trim()) return;
    setSaving(true);
    try {
      const datasetIds = selectedDatasets.map((id) => {
        const dataset = datasetValues.find((item: DatasetLike) => item.id === id);
        return dataset ? mapId(dataset) : id;
      });
      if (appType === "chat") {
        // No prompt/parameter override: the server merges the template payload,
        // so the catalog preview and the created assistant stay identical.
        const chat = await instantiateScenarioChat(template.id, {
          name: name.trim(),
          dataset_ids: datasetIds,
          create_missing_datasets: false,
          authoring: { llm_id: modelId || undefined },
        });
        notify(t("scenarioTemplates.wizard.created"), { type: "success" });
        await startTrialRun(template.id, chat.id);
      } else if (appType === "search") {
        const parameters = template.parameters;
        const res = await api.post<{ code: number; data: { id: string } }>("/search-apps", {
          name: name.trim(),
          description: template.description,
          dataset_ids: datasetIds,
          search_config: {
            kb_ids: datasetIds,
            similarity_threshold: parameters.similarity_threshold,
            vector_similarity_weight: parameters.vector_similarity_weight,
            top_k: parameters.top_n,
            summary: true,
            chat_id: modelId,
            llm_setting: {
              llm_id: modelId,
              temperature: parameters.temperature,
              top_p: parameters.top_p,
              max_tokens: parameters.max_tokens,
            },
          },
        });
        notify(t("scenarioTemplates.wizard.created"), { type: "success" });
        navigate(createPath({ resource: "search-apps", type: "show", id: res.data.data.id }));
      } else {
        const res = await api.post<{ code: number; data: { id: string } }>("/agents", {
          title: name.trim(),
          release: false,
          canvas_category: AGENT_CANVAS_CATEGORY,
          dsl: createScenarioAgentDsl({
            description: template.description,
            prompt: template.prompt,
            parameters: template.parameters,
            datasetIds,
            modelId,
          }),
        });
        notify(t("scenarioTemplates.wizard.created"), { type: "success" });
        navigate(createPath({ resource: "agents", type: "show", id: res.data.data.id }));
      }
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("scenarioTemplates.wizard.createFailed"), {
        type: "error",
      });
    } finally {
      setSaving(false);
    }
  };

  const selectedDatasetNames = datasetValues
    .filter((item: DatasetLike) => selectedDatasets.includes(item.id))
    .map((item: DatasetLike) => item.name);

  const stepLabels = [
    t("scenarioTemplates.wizard.stepScenario"),
    t("scenarioTemplates.wizard.stepApp"),
    t("scenarioTemplates.wizard.stepData"),
  ];

  return (
    <div className="mx-auto w-full max-w-6xl space-y-5 p-4">
      <header className="flex items-center gap-3">
        <LayoutTemplate className="size-6 text-primary" aria-hidden="true" />
        <div>
          <h1 className="text-xl font-semibold">{t("scenarioTemplates.wizard.title")}</h1>
          <p className="text-sm text-muted-foreground">{t("scenarioTemplates.wizard.subtitle")}</p>
        </div>
      </header>

      <ol className="flex flex-wrap items-center gap-2 text-sm">
        {stepLabels.map((label, index) => (
          <li
            key={label}
            className={cn(
              "rounded-full border px-3 py-1",
              index === step ? "border-primary bg-primary/5 text-primary" : "text-muted-foreground",
            )}
          >
            {index + 1}. {label}
          </li>
        ))}
      </ol>

      {catalogState === "loading" ? (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" aria-hidden="true" />
          {t("scenarioTemplates.wizard.catalogLoading")}
        </p>
      ) : null}
      {catalogState === "error" ? (
        <div className="rounded border border-destructive/40 p-4 text-sm">
          <p>{t("scenarioTemplates.wizard.catalogFailed")}</p>
          <Button className="mt-2" variant="outline" onClick={() => setReloadKey((value) => value + 1)}>
            {t("scenarioTemplates.wizard.retry")}
          </Button>
        </div>
      ) : null}

      {step === 0 && catalogState === "ready" && templates.length === 0 ? (
        <div className="rounded border p-4 text-sm">
          <p className="font-medium">{t("scenarioTemplates.wizard.catalogEmptyTitle")}</p>
          <p className="mt-1 text-xs text-muted-foreground">{t("scenarioTemplates.wizard.catalogEmpty")}</p>
        </div>
      ) : null}

      {step === 0 && catalogState === "ready" && templates.length > 0 ? (
        <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {templates.map((item) => (
            <button
              key={item.id}
              type="button"
              onClick={() => selectTemplate(item)}
              aria-pressed={templateId === item.id}
              className={cn(
                "rounded border p-4 text-left transition-colors",
                templateId === item.id ? "border-primary bg-primary/5" : "bg-background hover:bg-muted/40",
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
              </div>
              {item.sampleQuestions.length > 0 ? (
                <p className="mt-3 text-[11px] text-muted-foreground">
                  {t("scenarioTemplates.wizard.sampleHint", { question: item.sampleQuestions[0] })}
                </p>
              ) : null}
            </button>
          ))}
        </section>
      ) : null}

      {step === 1 && template ? (
        <section className="space-y-4">
          <div className="grid gap-3 md:grid-cols-3">
            {APP_TYPES.map((option) => {
              const enabled =
                template.appTypes.includes(option.value) &&
                availableAppTypes.some((item) => item.value === option.value);
              const Icon = option.icon;
              const active = appType === option.value;
              return (
                <button
                  key={option.value}
                  type="button"
                  disabled={!enabled}
                  onClick={() => {
                    setAppType(option.value);
                    setModelId("");
                  }}
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
                </button>
              );
            })}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="scenario-name">{t("scenarioTemplates.wizard.name")}</Label>
            <Input
              id="scenario-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder={t("scenarioTemplates.wizard.namePlaceholder")}
            />
          </div>
          {appType !== "agent" ? (
            <div className="space-y-1.5">
              <Label>{t("scenarioTemplates.wizard.model")}</Label>
              <select
                className="w-full rounded border bg-background px-2 py-1.5 text-sm"
                value={modelId}
                onChange={(event) => setModelId(event.target.value)}
                aria-label={t("scenarioTemplates.wizard.model")}
              >
                <option value="">{t("scenarioTemplates.wizard.modelDefault")}</option>
                {(modelOptions?.chat ?? []).map((option) => (
                  <option key={option.model_id} value={option.model_id}>
                    {option.name}
                  </option>
                ))}
              </select>
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
                <p className="mt-1 whitespace-pre-line rounded bg-muted/40 p-2 text-xs">
                  {template.prompt.system}
                </p>
              </div>
            ) : null}
          </div>
        </section>
      ) : null}

      {step === 2 && template ? (
        <section className="grid gap-4 lg:grid-cols-[1.2fr_1fr]">
          <div className="rounded border p-4">
            <h2 className="flex items-center gap-2 text-sm font-medium">
              <Database className="size-4" aria-hidden="true" />
              {t("scenarioTemplates.wizard.bindDatasets")}
            </h2>
            <div className="mt-3 max-h-64 space-y-1 overflow-y-auto rounded border p-2">
              {datasetValues.map((item: DatasetLike) => {
                const suggested = template.suggestedDatasetNames.some((keyword) => item.name.includes(keyword));
                return (
                  <label key={item.id} className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={selectedDatasets.includes(item.id)}
                      onChange={() => toggleDataset(item.id)}
                    />
                    <span>{item.name}</span>
                    {suggested ? (
                      <span className="rounded bg-primary/10 px-1 text-[10px] text-primary">
                        {t("scenarioTemplates.wizard.suggested")}
                      </span>
                    ) : null}
                  </label>
                );
              })}
              {datasetValues.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t("scenarioTemplates.wizard.noDatasets")}</p>
              ) : null}
            </div>
          </div>
          <div className="space-y-3">
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
            </div>
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
          </div>
        </section>
      ) : null}

      <footer className="flex flex-col gap-3 rounded border p-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="text-sm text-muted-foreground">
          {template ? (
            <>
              {t("scenarioTemplates.wizard.selected", {
                template: template.label,
                app: t(`scenarioTemplates.appType.${appType}.label`),
                datasets: selectedDatasetNames.length,
              })}
            </>
          ) : (
            t("scenarioTemplates.wizard.pickFirst")
          )}
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            disabled={step === 0 || saving}
            onClick={() => setStep((prev) => Math.max(0, prev - 1))}
          >
            <ArrowLeft className="size-4" /> {t("scenarioTemplates.wizard.previous")}
          </Button>
          {step < 2 ? (
            <Button disabled={!template || !name.trim()} onClick={() => setStep((prev) => Math.min(2, prev + 1))}>
              {t("scenarioTemplates.wizard.next")} <ArrowRight className="size-4" />
            </Button>
          ) : (
            <Button
              disabled={saving || !template || !name.trim() || selectedDatasets.length === 0}
              onClick={() => void createApp()}
            >
              {saving ? <Loader2 className="size-4 animate-spin" /> : null}
              {saving ? t("scenarioTemplates.wizard.creating") : t("scenarioTemplates.wizard.create")}
            </Button>
          )}
        </div>
      </footer>
    </div>
  );
};
