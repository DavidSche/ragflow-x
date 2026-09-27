import { useEffect, useState } from "react";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RotateCcw } from "lucide-react";
import { cn } from "@/lib/utils";
import { api } from "../../lib/api";
import { NumericRangeField } from "@/components/NumericRangeField";
import { PromptPresetPicker } from "../workbench/PromptPresetPicker";
import {
  applyParameterProfileToChat,
  applyPromptPreset,
  matchParameterProfileId,
  matchPromptPresetId,
} from "../workbench/preset-mapping";
import type {
  AddedModelLike,
  ChatConfigValues,
  DefaultModelLike,
} from "./chat-types";
import { RECOMMENDED_PROMPT, parseLLMSetting, rangeVal } from "./chat-types";

/* ── useModelOptions hook ────────────────────────────────────────── */

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function useModelOptions() {
  const [chat, setChat] = useState<AddedModelLike[]>([]);
  const [rerank, setRerank] = useState<AddedModelLike[]>([]);
  const [defaults, setDefaults] = useState<DefaultModelLike[]>([]);

  useEffect(() => {
    (async () => {
      try {
        const res = await api.get<{
          code: number;
          data: { chat: AddedModelLike[]; rerank: AddedModelLike[]; defaults: DefaultModelLike[] };
        }>("/chats/model-options");
        const d = res.data.data;
        setChat(d.chat ?? []);
        setRerank(d.rerank ?? []);
        setDefaults(d.defaults ?? []);
      } catch {
        /* keep empty */
      }
    })();
  }, []);

  const defaultName = (t: "chat" | "rerank") =>
    defaults.find((m) => m.model_type === t)?.name ?? "";

  return {
    chat,
    rerank,
    defaultChatName: defaultName("chat"),
    defaultRerankName: defaultName("rerank"),
  };
}

/* ── ModelField ──────────────────────────────────────────────────── */

interface ModelFieldProps {
  label: string;
  options: { id: string; name: string }[];
  value: string;
  onChange: (v: string) => void;
  custom: boolean;
  onCustomChange: (v: boolean) => void;
  defaultLabel: string;
}

function ModelField({
  label,
  options,
  value,
  onChange,
  custom,
  onCustomChange,
  defaultLabel,
}: ModelFieldProps) {
  const t = useTranslate();
  return (
    <div className="space-y-1">
      <Label>{label}</Label>
      {custom ? (
        <Input
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={t("chats.config_input_model_id")}
          aria-label={label}
        />
      ) : (
        <select
          className="w-full rounded border bg-background px-2 py-1.5 text-sm"
          value={value}
          onChange={(e) => {
            if (e.target.value === "__custom__") {
              onCustomChange(true);
              onChange("");
            } else {
              onChange(e.target.value);
            }
          }}
          aria-label={label}
        >
          <option value="">{defaultLabel}</option>
          {options.map((n) => (
            <option key={n.id} value={n.id}>
              {n.name}
            </option>
          ))}
          <option value="__custom__">{t("chats.config_custom")}</option>
        </select>
      )}
      <button
        type="button"
        className="block text-xs text-muted-foreground"
        onClick={() => {
          onCustomChange(!custom);
          onChange("");
        }}
      >
        {custom ? t("chats.config_use_dropdown") : t("chats.config_manual_input")}
      </button>
    </div>
  );
}

/* ── Slider (internal) ───────────────────────────────────────────── */

interface SliderProps {
  label: string;
  field: keyof Pick<ChatConfigValues, "topN" | "similarity" | "vectorWeight">;
  min: number;
  max: number;
  step: number;
  fmt: (n: number) => string;
  hint: string;
  value: ChatConfigValues;
  onChange: (v: ChatConfigValues) => void;
}

function ConfigSlider({
  label,
  field,
  min,
  max,
  step,
  fmt,
  hint,
  value,
  onChange,
}: SliderProps) {
  const t = useTranslate();
  const raw = value[field];
  const num = rangeVal(raw, min === 0 ? 0.2 : min);
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between text-sm">
        <Label>{label}</Label>
        <span className="rounded bg-muted px-1.5 py-0.5 text-xs tabular-nums">
          {raw === "" ? t("chats.config_default") : fmt(num)}
        </span>
      </div>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={num}
        onChange={(e) =>
          onChange({ ...value, [field]: e.target.value } as ChatConfigValues)
        }
        className="h-2 w-full cursor-pointer accent-primary"
        aria-label={label}
      />
      <p className="text-xs text-muted-foreground">{hint}</p>
    </div>
  );
}

/* ── Number field for model params ───────────────────────────────── */

function ParamNumber({
  label,
  value,
  onChange,
  step,
  min,
  max,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  step: number;
  min: number;
  max: number;
}) {
  return (
    <NumericRangeField
      label={label}
      value={value}
      step={step}
      min={min}
      max={max}
      allowEmpty
      fallbackValue={min}
      onChange={(next) => onChange(next === null ? "" : String(next))}
    />
  );
}

/* ── ChatConfigEditor ────────────────────────────────────────────── */

type EditorTab = "basic" | "params" | "advanced";

export function ChatConfigEditor({
  value,
  onChange,
}: {
  value: ChatConfigValues;
  onChange: (v: ChatConfigValues) => void;
  initialExpanded?: boolean;
}) {
  const t = useTranslate();
  const [tab, setTab] = useState<EditorTab>("basic");
  const [customLlm, setCustomLlm] = useState(false);
  const [customRerank, setCustomRerank] = useState(false);
  const { chat, rerank, defaultChatName, defaultRerankName } = useModelOptions();
  const patch = (p: Partial<ChatConfigValues>) => onChange({ ...value, ...p });

  const loadDefaults = () =>
    onChange({
      ...value,
      system: RECOMMENDED_PROMPT.system,
      prologue: RECOMMENDED_PROMPT.prologue,
      emptyResponse: RECOMMENDED_PROMPT.emptyResponse,
    });

  const resolveOptions = (models: AddedModelLike[], currentId: string) => {
    const list = models.map((m) => ({ id: m.model_id, name: m.name }));
    if (currentId && !list.some((o) => o.id === currentId))
      list.unshift({
        id: currentId,
        name: `${currentId} ${t("chats.config_configured_not_in_list")}`,
      });
    return list;
  };

  // Model params are stored as one JSON string; parse to a record of numbers.
  let setting: Record<string, number> = {};
  try {
    setting = (parseLLMSetting(value.llmSetting) as Record<string, number>) ?? {};
  } catch {
    setting = {};
  }

  const updateSetting = (key: string, val: string) => {
    const next: Record<string, number> = { ...setting };
    if (val === "") {
      delete next[key];
    } else {
      next[key] = Number(val);
    }
    const has = Object.keys(next).length > 0;
    patch({ llmSetting: has ? JSON.stringify(next) : "" });
  };

  const clearSetting = () => patch({ llmSetting: "" });

  const tabButton = (key: EditorTab, label: string) => (
    <button
      type="button"
      role="tab"
      aria-selected={tab === key}
      onClick={() => setTab(key)}
      className={cn(
        "-mb-px border-b-2 px-3 py-1.5 text-sm font-medium transition-colors",
        tab === key
          ? "border-primary text-primary"
          : "border-transparent text-muted-foreground hover:text-foreground",
      )}
    >
      {label}
    </button>
  );

  return (
    <div className="space-y-4">
      <div
        className="flex items-center gap-1 border-b"
        role="tablist"
        aria-label={t("chats.config_advanced")}
      >
        {tabButton("basic", t("chats.config_basic"))}
        {tabButton("params", t("chats.config_params"))}
        {tabButton("advanced", t("chats.config_advanced"))}
      </div>

      {tab === "basic" ? (
        <div className="grid gap-4 sm:grid-cols-2">
          <ModelField
            label={t("chats.config_llm")}
            options={resolveOptions(chat, value.llmId)}
            value={value.llmId}
            onChange={(v) => patch({ llmId: v })}
            custom={customLlm}
            onCustomChange={setCustomLlm}
            defaultLabel={
              defaultChatName
                ? t("chats.config_llm_default_with_name", { name: defaultChatName })
                : t("chats.config_llm_default_unset")
            }
          />
          <ModelField
            label={t("chats.config_rerank")}
            options={resolveOptions(rerank, value.rerankId)}
            value={value.rerankId}
            onChange={(v) => patch({ rerankId: v })}
            custom={customRerank}
            onCustomChange={setCustomRerank}
            defaultLabel={
              defaultRerankName
                ? t("chats.config_llm_default_with_name", { name: defaultRerankName })
                : t("chats.config_llm_default_unset")
            }
          />
          <ConfigSlider
            label={t("chats.config_top_n")}
            field="topN"
            min={1}
            max={20}
            step={1}
            fmt={(n) => `${n} 条`}
            hint={t("chats.config_top_n_hint")}
            value={value}
            onChange={onChange}
          />
          <ConfigSlider
            label={t("chats.config_similarity")}
            field="similarity"
            min={0}
            max={1}
            step={0.05}
            fmt={(n) => `${Math.round(n * 100)}%`}
            hint={t("chats.config_similarity_hint")}
            value={value}
            onChange={onChange}
          />
          <ConfigSlider
            label={t("chats.config_vector_weight")}
            field="vectorWeight"
            min={0}
            max={1}
            step={0.05}
            fmt={(n) => `${Math.round(n * 100)}%`}
            hint={t("chats.config_vector_weight_hint")}
            value={value}
            onChange={onChange}
          />
          <div className="flex items-end sm:col-span-2 text-xs text-muted-foreground">
            {t("chats.config_model_note")}
          </div>
        </div>
      ) : tab === "params" ? (
        <div className="space-y-4">
          <PromptPresetPicker
            showPrompts={false}
            activeProfileId={matchParameterProfileId(value)}
            onSelectPreset={() => undefined}
            onSelectProfile={(profileId) => onChange(applyParameterProfileToChat(value, profileId))}
          />
          <div className="grid gap-4 sm:grid-cols-2">
            <ParamNumber
              label={t("chats.config_temperature")}
              value={(setting?.temperature ?? "") as unknown as string}
              onChange={(v) => updateSetting("temperature", v)}
              step={0.1}
              min={0}
              max={2}
            />
            <ParamNumber
              label={t("chats.config_top_p")}
              value={(setting?.top_p ?? "") as unknown as string}
              onChange={(v) => updateSetting("top_p", v)}
              step={0.05}
              min={0}
              max={1}
            />
            <ParamNumber
              label={t("chats.config_frequency_penalty")}
              value={(setting?.frequency_penalty ?? "") as unknown as string}
              onChange={(v) => updateSetting("frequency_penalty", v)}
              step={0.1}
              min={-2}
              max={2}
            />
            <ParamNumber
              label={t("chats.config_presence_penalty")}
              value={(setting?.presence_penalty ?? "") as unknown as string}
              onChange={(v) => updateSetting("presence_penalty", v)}
              step={0.1}
              min={-2}
              max={2}
            />
            <ParamNumber
              label={t("chats.config_max_tokens")}
              value={(setting?.max_tokens ?? "") as unknown as string}
              onChange={(v) => updateSetting("max_tokens", v)}
              step={1}
              min={1}
              max={32768}
            />
          </div>
          <div className="flex items-center justify-between gap-2">
            <p className="text-xs text-muted-foreground">{t("chats.config_setting_hint")}</p>
            <Button variant="outline" size="sm" onClick={clearSetting}>
              <RotateCcw className="size-3.5" /> {t("chats.config_clear")}
            </Button>
            {value.llmSetting ? (
              <pre className="max-h-24 flex-1 overflow-auto rounded border bg-muted/40 p-2 text-[11px] text-muted-foreground">
                {value.llmSetting}
              </pre>
            ) : null}
          </div>
        </div>
      ) : (
        <div className="space-y-3">
          <PromptPresetPicker
            activePresetId={matchPromptPresetId(value)}
            activeProfileId={matchParameterProfileId(value)}
            onSelectPreset={(presetId) => onChange(applyPromptPreset(value, presetId))}
            onSelectProfile={(profileId) => onChange(applyParameterProfileToChat(value, profileId))}
          />
          <div className="flex justify-end">
            <Button variant="outline" size="sm" onClick={loadDefaults}>
              <RotateCcw className="size-3.5" /> {t("chats.config_load_defaults")}
            </Button>
          </div>
          <div className="space-y-1">
            <Label>{t("chats.config_system_prompt")}</Label>
            <textarea
              className="min-h-[140px] w-full resize-y rounded border bg-background p-2 text-sm"
              value={value.system}
              onChange={(e) => patch({ system: e.target.value })}
              placeholder="system prompt"
              aria-label={t("chats.config_system_prompt")}
            />
            <p className="text-xs text-muted-foreground">{t("chats.config_system_hint")}</p>
          </div>
          <div className="space-y-1">
            <Label>{t("chats.config_prologue")}</Label>
            <textarea
              className="min-h-[84px] w-full resize-y rounded border bg-background p-2 text-sm"
              value={value.prologue}
              onChange={(e) => patch({ prologue: e.target.value })}
              aria-label={t("chats.config_prologue")}
            />
          </div>
          <div className="space-y-1">
            <Label>{t("chats.config_empty_response")}</Label>
            <textarea
              className="min-h-[84px] w-full resize-y rounded border bg-background p-2 text-sm"
              value={value.emptyResponse}
              onChange={(e) => patch({ emptyResponse: e.target.value })}
              aria-label={t("chats.config_empty_response")}
            />
          </div>
        </div>
      )}
    </div>
  );
}
