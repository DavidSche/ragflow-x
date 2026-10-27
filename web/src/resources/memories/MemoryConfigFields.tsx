/**
 * MemoryConfigFields – reusable memory configuration form fields.
 * Create mode: memory types + embedding + LLM. Edit mode also shows storage
 * size, permissions, forgetting policy, temperature and prompts, mirroring
 * RAGFlow's memory settings.
 */
import { useEffect, useState } from "react";
import type { ChangeEvent } from "react";
import { useTranslate } from "ra-core";
import { api } from "../../lib/api";
import { optionalWarning } from "../../lib/optional-error";
import { NumericRangeField } from "@/components/NumericRangeField";
import { clampInteger } from "@/lib/numeric";

export interface MemoryConfigValues {
  memory_type: string[];
  embd_id: string;
  llm_id: string;
  memory_size: string;
  permissions: string;
  forgetting_policy: string;
  temperature: string;
  description: string;
  system_prompt: string;
  user_prompt: string;
}

export const emptyMemoryConfig = (): MemoryConfigValues => ({
  memory_type: ["raw"],
  embd_id: "",
  llm_id: "",
  memory_size: "5242880",
  permissions: "me",
  forgetting_policy: "FIFO",
  temperature: "0.5",
  description: "",
  system_prompt: "",
  user_prompt: "",
});

const MEMORY_TYPES = ["raw", "semantic", "episodic", "procedural"];

interface ModelOption {
  model_id: string;
  name: string;
}

export const MemoryConfigFields = ({
  value,
  onChange,
  disabled,
  mode,
}: {
  value: MemoryConfigValues;
  onChange: (v: MemoryConfigValues) => void;
  disabled?: boolean;
  mode: "create" | "edit";
}) => {
  const t = useTranslate();

  const set = (patch: Partial<MemoryConfigValues>) => onChange({ ...value, ...patch });

  const toggleType = (type: string) =>
    set({
      memory_type: value.memory_type.includes(type)
        ? value.memory_type.filter((x) => x !== type)
        : [...value.memory_type, type],
    });

  return (
    <div className="space-y-3">
      <div>
        <span className="mb-1 block text-sm">
          {t("memories.memory_type")}
          <span className="ml-0.5 text-destructive">*</span>
        </span>
        <div className="flex flex-wrap gap-3">
          {MEMORY_TYPES.map((type) => (
            <label key={type} className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                className="accent-foreground"
                checked={value.memory_type.includes(type)}
                disabled={disabled || (type === "raw" && value.memory_type.includes(type))}
                onChange={() => toggleType(type)}
              />
              {t(`memories.type_${type}`)}
            </label>
          ))}
        </div>
      </div>

      <ModelSelect
        label={t("memories.embedding_model")}
        kind="embedding"
        selectedId={value.embd_id}
        disabled={disabled}
        required
        onChange={(id) => set({ embd_id: id })}
      />
      <ModelSelect
        label={t("memories.llm_model")}
        kind="chat"
        selectedId={value.llm_id}
        disabled={disabled}
        required
        onChange={(id) => set({ llm_id: id })}
      />

      {mode === "edit" && (
        <>
          <NumericRangeField
            label={t("memories.memory_size")}
            value={value.memory_size ?? "5242880"}
            min={1}
            max={10485760}
            step={1024}
            unit="bytes"
            disabled={disabled}
            required
            onChange={(next) => set({ memory_size: String(clampInteger(next ?? 5242880, 1, 10485760, 5242880)) })}
          />
          <div className="grid gap-3 md:grid-cols-2">
            <label className="space-y-1 text-sm">
              <span>{t("memories.permissions")}</span>
              <select
                className="w-full rounded border bg-background px-2 py-1 text-sm"
                value={value.permissions ?? "me"}
                disabled={disabled}
                onChange={(e) => set({ permissions: e.target.value })}
              >
                <option value="me">{t("memories.permission_me")}</option>
                <option value="team">{t("memories.permission_team")}</option>
              </select>
            </label>
            <label className="space-y-1 text-sm">
              <span>{t("memories.forgetting_policy")}</span>
              <select
                className="w-full rounded border bg-background px-2 py-1 text-sm"
                value={value.forgetting_policy ?? "FIFO"}
                disabled={disabled}
                onChange={(e) => set({ forgetting_policy: e.target.value })}
              >
                <option value="FIFO">FIFO</option>
                <option value="LRU">LRU</option>
              </select>
            </label>
          </div>
          <div className="space-y-1 text-sm">
            <div className="flex items-center justify-between">
              <span>{t("memories.temperature")}</span>
              <span className="text-xs text-muted-foreground">
                {Number(value.temperature || 0).toFixed(2)}
              </span>
            </div>
            <input
              type="range"
              className="w-full accent-foreground"
              min={0}
              max={1}
              step={0.01}
              value={Number(value.temperature || 0)}
              disabled={disabled}
              onChange={(e) => set({ temperature: e.target.value })}
            />
          </div>
          <label className="space-y-1 text-sm">
            <span>{t("memories.description")}</span>
            <input
              className="w-full rounded border bg-background px-2 py-1 text-sm"
              value={value.description ?? ""}
              disabled={disabled}
              onChange={(e) => set({ description: e.target.value })}
            />
          </label>
          <TextareaField
            label={t("memories.system_prompt")}
            value={value.system_prompt ?? ""}
            disabled={disabled}
            onChange={(v) => set({ system_prompt: v })}
          />
          <TextareaField
            label={t("memories.user_prompt")}
            value={value.user_prompt ?? ""}
            disabled={disabled}
            onChange={(v) => set({ user_prompt: v })}
          />
        </>
      )}
    </div>
  );
};

const ModelSelect = ({
  label,
  kind,
  selectedId,
  disabled,
  required,
  onChange,
}: {
  label: string;
  kind: "embedding" | "chat";
  selectedId: string;
  disabled?: boolean;
  required?: boolean;
  onChange: (id: string) => void;
}) => {
  const t = useTranslate();
  const [models, setModels] = useState<ModelOption[]>([]);
  const [choice, setChoice] = useState("");

  useEffect(() => {
    api
      .get<{ code: number; message: string; data: { embedding: ModelOption[]; chat: ModelOption[] } }>("/memories/model-options")
      .then((res) => setModels(res.data?.data?.[kind] ?? []))
      .catch((error) => optionalWarning(error, "模型选项加载失败"));
  }, [kind]);

  useEffect(() => {
    setChoice(
      models.find((m) => m.model_id === selectedId)?.model_id ||
        models.find((m) => m.name === selectedId)?.model_id ||
        "",
    );
  }, [selectedId, models]);

  const resolvedId =
    models.find((m) => m.model_id === selectedId)?.model_id ||
    models.find((m) => m.name === selectedId)?.model_id ||
    "";

  return (
    <label className="space-y-1 text-sm">
      <span>
        {label}
        {required ? <span className="ml-0.5 text-destructive">*</span> : null}
      </span>
      <select
        className="w-full rounded border bg-background px-2 py-1 text-sm"
        value={choice}
        disabled={disabled}
        onChange={(e: ChangeEvent<HTMLSelectElement>) => {
          setChoice(e.target.value);
          onChange(e.target.value);
        }}
      >
        <option value="">{t("memories.select_model")}</option>
        {selectedId && !resolvedId ? (
          <option value={selectedId}>{selectedId}</option>
        ) : null}
        {models.map((m) => (
          <option key={m.model_id} value={m.model_id}>
            {m.name}
          </option>
        ))}
      </select>
    </label>
  );
};

const TextareaField = ({
  label,
  value,
  disabled,
  onChange,
}: {
  label: string;
  value: string;
  disabled?: boolean;
  onChange: (v: string) => void;
}) => (
  <label className="space-y-1 text-sm">
    <span className="inline-block">{label}</span>
    <textarea
      className="min-h-16 w-full rounded border bg-background p-2 text-sm"
      value={value}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
    />
  </label>
);
