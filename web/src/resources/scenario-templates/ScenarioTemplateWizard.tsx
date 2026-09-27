import { useEffect, useMemo, useState } from "react";
import {
  useCanAccess,
  useCreatePath,
  useGetList,
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
import { PromptPresetPicker } from "../workbench/PromptPresetPicker";
import type { ParameterProfileId } from "../workbench/prompt-presets";
import {
  SCENARIO_TEMPLATES,
  scenarioParameterProfile,
  scenarioPromptPreset,
  type ScenarioAppType,
  type ScenarioTemplate,
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

const APP_TYPES: { value: ScenarioAppType; label: string; description: string; icon: typeof MessageSquare }[] = [
  { value: "chat", label: "对话助手", description: "多轮问答、开场白、引用和反馈。", icon: MessageSquare },
  { value: "search", label: "检索应用", description: "面向片段检索和事实核对的轻量入口。", icon: Search },
  { value: "agent", label: "Agent 工作流", description: "检索 + 任务执行 + 结构化输出。", icon: Network },
];

const STEP_LABELS = ["选择场景", "选择应用", "绑定数据"];

export const ScenarioTemplateWizard = () => {
  const t = useTranslate();
  const notify = useNotify();
  const navigate = useNavigate();
  const createPath = useCreatePath();
  const [step, setStep] = useState(0);
  const [templateId, setTemplateId] = useState<string>("");
  const [appType, setAppType] = useState<ScenarioAppType>("chat");
  const [name, setName] = useState("");
  const [profileId, setProfileId] = useState<ParameterProfileId>("balanced");
  const [modelId, setModelId] = useState("");
  const [selectedDatasets, setSelectedDatasets] = useState<string[]>([]);
  const [modelOptions, setModelOptions] = useState<ModelOptionsResponse | null>(null);
  const [saving, setSaving] = useState(false);

  const { data: datasets } = useGetList("datasets", { pagination: { page: 1, perPage: 200 } });
  const { values: datasetValues, mapId } = datasetOptions(datasets as DatasetLike[] | undefined);

  const { canAccess: canManageChat } = useCanAccess({ resource: "chat", action: "manage" });
  const { canAccess: canManageSearch } = useCanAccess({ resource: "search-app", action: "manage" });
  const { canAccess: canManageAgent } = useCanAccess({ resource: "agent", action: "manage" });

  const availableAppTypes = useMemo(
    () =>
      APP_TYPES.filter(({ value }) =>
        value === "chat" ? canManageChat !== false : value === "search" ? canManageSearch !== false : canManageAgent !== false,
      ),
    [canManageAgent, canManageChat, canManageSearch],
  );

  const template = SCENARIO_TEMPLATES.find((item) => item.id === templateId);
  const profile = scenarioParameterProfile(
    template ? { ...template, profileId } : SCENARIO_TEMPLATES[0],
  );
  const preset = template ? scenarioPromptPreset(template) : undefined;

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

  const selectTemplate = (item: ScenarioTemplate) => {
    setTemplateId(item.id);
    setProfileId(item.profileId);
    setAppType(item.appTypes[0]);
    setName(`${item.label}助手`);
    setModelId("");
  };

  const toggleDataset = (datasetId: string) => {
    setSelectedDatasets((prev) =>
      prev.includes(datasetId) ? prev.filter((id) => id !== datasetId) : [...prev, datasetId],
    );
  };

  const createApp = async () => {
    if (!template || !preset || !name.trim()) return;
    setSaving(true);
    try {
      const datasetIds = selectedDatasets.map((id) => {
        const dataset = datasetValues.find((item: DatasetLike) => item.id === id);
        return dataset ? mapId(dataset) : id;
      });
      if (appType === "chat") {
        const res = await api.post<{ code: number; data: { id: string } }>("/chats", {
          name: name.trim(),
          dataset_ids: datasetIds,
          llm_id: modelId || undefined,
          system: preset.system,
          prologue: preset.prologue,
          empty_response: preset.emptyResponse,
          top_n: profile.topN,
          similarity_threshold: profile.similarityThreshold,
          vector_similarity_weight: profile.vectorWeight,
          llm_setting: {
            temperature: profile.temperature,
            top_p: profile.topP,
            max_tokens: profile.maxTokens,
          },
        });
        navigate(createPath({ resource: "chats", type: "show", id: res.data.data.id }));
      } else if (appType === "search") {
        const res = await api.post<{ code: number; data: { id: string } }>("/search-apps", {
          name: name.trim(),
          description: template.description,
          dataset_ids: datasetIds,
          search_config: {
            kb_ids: datasetIds,
            similarity_threshold: profile.similarityThreshold,
            vector_similarity_weight: profile.vectorWeight,
            top_k: profile.topN,
            summary: true,
            chat_id: modelId,
            llm_setting: { llm_id: modelId, temperature: profile.temperature },
          },
        });
        navigate(createPath({ resource: "search-apps", type: "show", id: res.data.data.id }));
      } else {
        const res = await api.post<{ code: number; data: { id: string } }>("/agents", {
          title: name.trim(),
          release: false,
          canvas_category: AGENT_CANVAS_CATEGORY,
          dsl: createScenarioAgentDsl({
            description: template.description,
            preset,
            profile,
            datasetIds,
            modelId,
          }),
        });
        navigate(createPath({ resource: "agents", type: "show", id: res.data.data.id }));
      }
      notify("场景应用已创建", { type: "success" });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : "场景应用创建失败", { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  const selectedDatasetNames = datasetValues
    .filter((item: DatasetLike) => selectedDatasets.includes(item.id))
    .map((item: DatasetLike) => item.name);

  return (
    <div className="mx-auto w-full max-w-6xl space-y-5 p-4">
      <header className="flex items-center gap-3">
        <LayoutTemplate className="size-6 text-primary" aria-hidden="true" />
        <div>
          <h1 className="text-xl font-semibold">场景模板向导</h1>
          <p className="text-sm text-muted-foreground">
            选择业务场景、应用类型和数据集，快速创建可试用的知识应用。
          </p>
        </div>
      </header>

      <ol className="flex flex-wrap items-center gap-2 text-sm">
        {STEP_LABELS.map((label, index) => (
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

      {step === 0 ? (
        <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {SCENARIO_TEMPLATES.map((item) => (
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
                    {APP_TYPES.find((option) => option.value === type)?.label}
                  </span>
                ))}
              </div>
            </button>
          ))}
        </section>
      ) : null}

      {step === 1 && template ? (
        <section className="space-y-4">
          <div className="grid gap-3 md:grid-cols-3">
            {APP_TYPES.map((option) => {
              const enabled = template.appTypes.includes(option.value) && availableAppTypes.some((item) => item.value === option.value);
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
                    {option.label}
                  </span>
                  <p className="mt-1 text-xs text-muted-foreground">{option.description}</p>
                </button>
              );
            })}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="scenario-name">应用名称</Label>
            <Input
              id="scenario-name"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="例如：制度问答助手"
            />
          </div>
          {appType !== "agent" ? (
            <div className="space-y-1.5">
              <Label>默认模型</Label>
              <select
                className="w-full rounded border bg-background px-2 py-1.5 text-sm"
                value={modelId}
                onChange={(event) => setModelId(event.target.value)}
              >
                <option value="">使用引擎默认模型</option>
                {(modelOptions?.chat ?? []).map((option) => (
                  <option key={option.model_id} value={option.model_id}>
                    {option.name}
                  </option>
                ))}
              </select>
            </div>
          ) : null}
          <PromptPresetPicker
            activePresetId={template.promptPresetId}
            activeProfileId={profileId}
            onSelectPreset={() => undefined}
            onSelectProfile={setProfileId}
          />
        </section>
      ) : null}

      {step === 2 && template && preset ? (
        <section className="grid gap-4 lg:grid-cols-[1.2fr_1fr]">
          <div className="rounded border p-4">
            <h2 className="flex items-center gap-2 text-sm font-medium">
              <Database className="size-4" aria-hidden="true" />
              绑定数据集
            </h2>
            <div className="mt-3 max-h-64 space-y-1 overflow-y-auto rounded border p-2">
              {datasetValues.map((item: DatasetLike) => {
                const datasetId = mapId(item);
                const suggested = template.suggestedDatasetNames.some((keyword) => item.name.includes(keyword));
                return (
                  <label key={item.id} className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={selectedDatasets.includes(item.id)}
                      onChange={() => toggleDataset(item.id)}
                    />
                    <span>{item.name}</span>
                    {suggested ? <span className="rounded bg-primary/10 px-1 text-[10px] text-primary">推荐</span> : null}
                  </label>
                );
              })}
              {datasetValues.length === 0 ? (
                <p className="text-sm text-muted-foreground">暂无可绑定数据集。</p>
              ) : null}
            </div>
          </div>
          <div className="space-y-3">
            <div className="rounded border p-4">
              <h2 className="flex items-center gap-2 text-sm font-medium">
                <ClipboardCheck className="size-4" aria-hidden="true" />
                试运行评测
              </h2>
              <ul className="mt-2 list-disc space-y-1 pl-5 text-sm text-muted-foreground">
                {template.evaluationQuestions.map((question) => (
                  <li key={question}>{question}</li>
                ))}
              </ul>
            </div>
            <div className="rounded border p-4 text-sm">
              <h2 className="font-medium">发布检查</h2>
              <ul className="mt-2 list-disc space-y-1 pl-5 text-muted-foreground">
                {template.publishChecklist.map((item) => (
                  <li key={item}>{item}</li>
                ))}
              </ul>
              <p className="mt-3 text-xs text-muted-foreground">权限建议：{template.permissionAdvice}</p>
            </div>
          </div>
        </section>
      ) : null}

      <footer className="flex flex-col gap-3 rounded border p-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="text-sm text-muted-foreground">
          {template ? (
            <>
              已选：{template.label} · {APP_TYPES.find((item) => item.value === appType)?.label}
              {selectedDatasetNames.length > 0 ? ` · ${selectedDatasetNames.length} 个数据集` : " · 未绑定数据集"}
            </>
          ) : (
            "请先选择一个业务场景。"
          )}
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" disabled={step === 0 || saving} onClick={() => setStep((prev) => Math.max(0, prev - 1))}>
            <ArrowLeft className="size-4" /> 上一步
          </Button>
          {step < 2 ? (
            <Button disabled={!template || !name.trim()} onClick={() => setStep((prev) => Math.min(2, prev + 1))}>
              下一步 <ArrowRight className="size-4" />
            </Button>
          ) : (
            <Button disabled={saving || !template || !name.trim() || selectedDatasets.length === 0} onClick={() => void createApp()}>
              {saving ? <Loader2 className="size-4 animate-spin" /> : null}
              创建应用
            </Button>
          )}
        </div>
      </footer>
    </div>
  );
};
