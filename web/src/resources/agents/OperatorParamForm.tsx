import { useEffect, useRef, useState } from "react";
import { useTranslate } from "ra-core";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { FieldDef } from "./operatorForms";
import {
  defaultModelOf,
  defaultModelRef,
  modelLabel,
  modelRef,
  useAgentAuthoringOptions,
  type AgentAuthoringOptionsState,
} from "./agentAuthoringOptions";

interface Props {
  fields: FieldDef[];
  values: Record<string, any>;
  disabled?: boolean;
  onChange: (name: string, value: any) => void;
}

/** Translate signature shared by the sub-fields (`ra-core` useTranslate). */
type Translate = (key: string, options?: Record<string, any>) => string;

/**
 * Evaluate a field's `visibleWhen` rule.
 *
 * The sibling value falls back to that sibling's declared default: an existing
 * node DSL often omits `retrieval_from` (the engine then defaults to
 * `dataset`), and reading `undefined` here would silently hide the knowledge
 * base picker for every legacy node.
 */
function isFieldVisible(field: FieldDef, fields: FieldDef[], values: Record<string, any>): boolean {
  const rule = field.visibleWhen;
  if (!rule) return true;
  const raw = values[rule.field];
  const fallback = fields.find((f) => f.name === rule.field)?.default;
  const current = String(raw ?? fallback ?? "");
  return rule.equals.includes(current);
}

/** Coerce a stored parameter into an id array (legacy values may be a CSV string). */
function asIdArray(value: unknown): string[] {
  if (Array.isArray(value)) return value.map((v) => String(v)).filter(Boolean);
  if (typeof value === "string" && value.trim()) {
    return value
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
  }
  return [];
}

/**
 * Pick a model for a required-but-empty `modelSelect` field.
 *
 * The engine refuses to run an LLM node without `model_id`
 * (`component/llm.go`: `ParamError model_id required`), so leaving the field
 * blank is not a state the operator can ship. Preference order: the tenant
 * default of that type, then the first catalog entry.
 */
function seedModelValue(
  field: FieldDef,
  options: AgentAuthoringOptionsState,
): string {
  const kind = field.modelKind ?? "chat";
  const catalog = kind === "rerank" ? options.options.models?.rerank : options.options.models?.chat;
  const def = defaultModelOf(options.options.models, kind);
  const fromDefault = def ? defaultModelRef(def) : "";
  if (fromDefault) return fromDefault;
  return catalog && catalog.length > 0 ? modelRef(catalog[0]) : "";
}

function ModelSelectField({
  field,
  value,
  state,
  disabled,
  onChange,
  t,
}: {
  field: FieldDef;
  value: string;
  state: AgentAuthoringOptionsState;
  disabled?: boolean;
  onChange: (name: string, value: any) => void;
  t: Translate;
}) {
  const kind = field.modelKind ?? "chat";
  const catalog = (kind === "rerank" ? state.options.models?.rerank : state.options.models?.chat) ?? [];
  const sectionError = state.options.models_error;
  const def = defaultModelOf(state.options.models, kind);
  const defRef = def ? defaultModelRef(def) : "";
  const defName = def ? def.model_name ?? def.name ?? defRef : "";

  if (state.loading) {
    return <p className="text-xs text-muted-foreground">{t("agents.picker_loading")}</p>;
  }
  // Both the whole-request failure and this section's failure mean "no list":
  // fall back to the manual id so authoring is never blocked by the catalog.
  const reason = sectionError ?? (state.error ? state.error.displayMessage : "");
  if (reason || catalog.length === 0) {
    return (
      <>
        <Input
          data-testid={`model-input-${field.name}`}
          className="h-8 font-mono text-xs"
          value={value}
          disabled={disabled}
          onChange={(e) => onChange(field.name, e.target.value)}
          placeholder="name@provider"
        />
        <p className="text-xs text-muted-foreground">
          {reason
            ? t("agents.picker_failed", { reason })
            : kind === "rerank"
              ? t("agents.picker_rerank_empty")
              : t("agents.picker_model_empty")}
        </p>
      </>
    );
  }

  const options = catalog.map((m) => ({ value: modelRef(m), label: modelLabel(m) }));
  if (value && !options.some((o) => o.value === value)) {
    options.unshift({ value, label: `${value} ${t("agents.picker_configured_not_in_list")}` });
  }

  return (
    <>
      <select
        data-testid={`model-select-${field.name}`}
        className="w-full rounded border bg-background px-2 py-1 text-sm"
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(field.name, e.target.value)}
      >
        {field.required ? (
          value ? null : <option value="">{t("agents.picker_model_required")}</option>
        ) : (
          <option value="">
            {defName
              ? t("agents.picker_model_follow_default", { name: defName })
              : t("agents.picker_model_follow_default_unnamed")}
          </option>
        )}
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      {field.help ? <p className="text-xs text-muted-foreground">{t(field.help)}</p> : null}
    </>
  );
}

function ResourceMultiField({
  field,
  value,
  state,
  disabled,
  onChange,
  t,
}: {
  field: FieldDef;
  value: unknown;
  state: AgentAuthoringOptionsState;
  disabled?: boolean;
  onChange: (name: string, value: any) => void;
  t: Translate;
}) {
  const resource = field.resource ?? "dataset";
  const selected = asIdArray(value);
  const [query, setQuery] = useState("");
  const pickable =
    resource === "memory"
      ? state.options.memories.map((m) => ({ id: m.id, name: m.name, detail: "" }))
      : state.options.datasets.map((d) => ({
          id: d.ragflow_dataset_id,
          name: d.name,
          detail:
            d.source === "engine"
              ? t("agents.picker_engine_dataset")
              : `${d.document_count ?? 0} ${t("agents.picker_doc_unit")}`,
        }));
  const sectionError = resource === "memory" ? state.options.memories_error : state.options.datasets_error;
  const reason = sectionError ?? (state.error ? state.error.displayMessage : "");

  if (state.loading) {
    return <p className="text-xs text-muted-foreground">{t("agents.picker_loading")}</p>;
  }
  if (reason) {
    return (
      <>
        <Input
          data-testid={`resource-input-${field.name}`}
          className="h-8 font-mono text-xs"
          value={selected.join(",")}
          disabled={disabled}
          onChange={(e) => onChange(field.name, asIdArray(e.target.value))}
          placeholder={t("agents.picker_ids_placeholder")}
        />
        <p className="text-xs text-muted-foreground">{t("agents.picker_failed", { reason })}</p>
      </>
    );
  }
  if (pickable.length === 0) {
    return (
      <p className="text-xs text-muted-foreground">
        {resource === "memory" ? t("agents.picker_memory_empty") : t("agents.picker_dataset_empty")}
      </p>
    );
  }

  // A stored id can disappear from the picker (deleted / unlinked library).
  // Keep it visible and checked so the operator can see the dangling reference
  // instead of losing it on the next save.
  const unknown = selected.filter((id) => !pickable.some((p) => p.id === id));
  const toggle = (id: string) =>
    onChange(field.name, selected.includes(id) ? selected.filter((x) => x !== id) : [...selected, id]);
  // A live tenant can hold dozens of libraries (drill leftovers included), so a
  // long list gets a filter. Below that the input would be noise.
  const filterable = pickable.length > 8;
  const needle = query.trim().toLowerCase();
  const shown = needle ? pickable.filter((p) => p.name.toLowerCase().includes(needle)) : pickable;

  return (
    <>
      {filterable ? (
        <Input
          data-testid={`resource-filter-${field.name}`}
          className="mb-1 h-8 text-xs"
          value={query}
          disabled={disabled}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("agents.picker_filter_placeholder")}
        />
      ) : null}
      <div
        className="max-h-40 space-y-1 overflow-auto rounded border bg-background p-2"
        data-testid={`resource-multi-${field.name}`}
      >
        {shown.map((p) => (
          <label key={p.id} className="flex items-start gap-2 text-xs">
            <input
              type="checkbox"
              className="mt-0.5 size-3.5"
              checked={selected.includes(p.id)}
              disabled={disabled}
              onChange={() => toggle(p.id)}
            />
            <span className="flex-1">
              {p.name}
              {p.detail ? <span className="ml-1 text-muted-foreground">{p.detail}</span> : null}
            </span>
          </label>
        ))}
        {shown.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t("agents.picker_filter_empty")}</p>
        ) : null}
        {unknown.map((id) => (
          <label key={id} className="flex items-start gap-2 text-xs text-amber-600">
            <input
              type="checkbox"
              className="mt-0.5 size-3.5"
              checked
              disabled={disabled}
              onChange={() => toggle(id)}
            />
            <span className="flex-1 font-mono">{t("agents.picker_unknown_id", { id })}</span>
          </label>
        ))}
      </div>
      <p className="text-xs text-muted-foreground">
        {t("agents.picker_selected_count", { count: selected.length })}
        {field.help ? ` · ${t(field.help)}` : ""}
      </p>
    </>
  );
}

export function OperatorParamForm({ fields, values, disabled, onChange }: Props) {
  const t = useTranslate();
  const [jsonTexts, setJsonTexts] = useState<Record<string, string>>({});
  const state = useAgentAuthoringOptions();
  const seeded = useRef<Set<string>>(new Set());

  useEffect(() => {
    const next: Record<string, string> = {};
    for (const f of fields) {
      if (f.type === "jsonArray") {
        next[f.name] = JSON.stringify(values[f.name] ?? f.default ?? (f.name === "headers" ? {} : []), null, 2);
      }
    }
    setJsonTexts(next);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fields]);

  // A required model field starts empty on every freshly added node; fill it
  // once the catalog arrives so the node is runnable without typing an id.
  // `seeded` keeps a deliberate clear (operator picks the blank entry) from
  // being undone on the next render.
  useEffect(() => {
    if (state.loading) return;
    for (const f of fields) {
      if (f.type !== "modelSelect" || !f.required) continue;
      if (seeded.current.has(f.name)) continue;
      if (String(values[f.name] ?? "") !== "") {
        seeded.current.add(f.name);
        continue;
      }
      const next = seedModelValue(f, state);
      if (!next) continue;
      seeded.current.add(f.name);
      onChange(f.name, next);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [fields, state.loading, state.options]);

  if (fields.length === 0) {
    return <p className="text-sm text-muted-foreground">{t("agents.operator_no_params")}</p>;
  }

  const setJson = (f: FieldDef, raw: string) => {
    setJsonTexts((p) => ({ ...p, [f.name]: raw }));
    try {
      onChange(f.name, JSON.parse(raw));
    } catch {
      /* keep previous value while JSON is invalid */
    }
  };

  const visible = fields.filter((f) => isFieldVisible(f, fields, values));

  return (
    <div className="space-y-3">
      {visible.map((f) => (
        <div key={f.name} className="space-y-1">
          <Label className="text-xs">{t(f.label)}</Label>
          {f.type === "text" && (
            <Input className="h-8 text-sm" value={values[f.name] ?? f.default ?? ""} disabled={disabled} onChange={(e) => onChange(f.name, e.target.value)} placeholder={f.placeholder ? t(f.placeholder) : undefined} />
          )}
          {f.type === "textarea" && (
            <textarea
              className="min-h-16 w-full rounded border bg-background p-2 text-xs"
              value={values[f.name] ?? f.default ?? ""}
              disabled={disabled}
              onChange={(e) => onChange(f.name, e.target.value)}
              spellCheck={false}
            />
          )}
          {f.type === "number" && (
            <Input
              className="h-8 text-sm"
              type="number"
              min={f.min}
              max={f.max}
              step={f.step}
              value={values[f.name] ?? f.default ?? 0}
              disabled={disabled}
              onChange={(e) => onChange(f.name, Number(e.target.value))}
            />
          )}
          {f.type === "slider" && (
            <div className="flex items-center gap-2">
              <input
                type="range"
                className="flex-1 accent-foreground"
                min={f.min}
                max={f.max}
                step={f.step}
                value={values[f.name] ?? f.default ?? 0}
                disabled={disabled}
                onChange={(e) => onChange(f.name, Number(e.target.value))}
              />
              <span className="w-12 text-right text-xs text-muted-foreground">{values[f.name] ?? f.default ?? 0}</span>
            </div>
          )}
          {f.type === "select" && (
            <select
              className="w-full rounded border bg-background px-2 py-1 text-sm"
              value={values[f.name] ?? f.default ?? ""}
              disabled={disabled}
              onChange={(e) => onChange(f.name, e.target.value)}
            >
              {(f.options ?? []).map((o) => (
                <option key={o.value} value={o.value}>
                  {t(o.label)}
                </option>
              ))}
            </select>
          )}
          {f.type === "switch" && (
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" className="size-4" checked={!!(values[f.name] ?? f.default)} disabled={disabled} onChange={(e) => onChange(f.name, e.target.checked)} />
              {t(f.label)}
            </label>
          )}
          {f.type === "modelSelect" && (
            <ModelSelectField
              field={f}
              value={String(values[f.name] ?? "")}
              state={state}
              disabled={disabled}
              onChange={onChange}
              t={t}
            />
          )}
          {f.type === "resourceMulti" && (
            <ResourceMultiField
              field={f}
              value={values[f.name]}
              state={state}
              disabled={disabled}
              onChange={onChange}
              t={t}
            />
          )}
          {f.type === "jsonArray" && (
            <>
              <textarea
                className="min-h-16 w-full rounded border bg-background p-2 font-mono text-xs"
                value={jsonTexts[f.name] ?? JSON.stringify(values[f.name] ?? f.default ?? [], null, 2)}
                disabled={disabled}
                onChange={(e) => setJson(f, e.target.value)}
                spellCheck={false}
              />
              {f.help ? <p className="text-xs text-muted-foreground">{t(f.help)}</p> : null}
            </>
          )}
        </div>
      ))}
    </div>
  );
}
