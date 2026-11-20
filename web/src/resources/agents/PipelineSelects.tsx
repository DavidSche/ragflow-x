// Pickers for the data-pipeline property panel.
//
// Two classes of pipeline parameter store a reference to something the engine
// owns, and both used to be hand-typed (or missing entirely) in this panel:
//
//   - `vlm.llm_id` on a Parser and `llm_id` on an Extractor are ENGINE model ids
//     (`name@[instance@]provider`) — the same composite the LLM nodes store;
//   - `compilation_template_id` / `compilation_template_group_id` on a Compiler
//     are engine-owned template ids, and the engine refuses to save the canvas
//     without one (`[Compiler] Compilation Template Group does not support empty
//     value`).
//
// Copying an id by hand from another screen is how a canvas ends up naming a
// model that was never configured. These selects are fed by the sections
// `GET /api/v1/agents/authoring-options` already returns, and every one of them
// degrades to manual entry — never to a blank control — when the catalog is
// unavailable, so authoring is not blocked by a directory lookup.

import { useTranslate } from "ra-core";
import { Input } from "@/components/ui/input";
import { modelLabel, modelRef, useAgentAuthoringOptions } from "./agentAuthoringOptions";

/** Translate signature shared by these pickers (`ra-core` useTranslate). */
type Translate = (key: string, options?: Record<string, any>) => string;

function fallbackInput({
  testId,
  value,
  disabled,
  placeholder,
  onChange,
}: {
  testId: string;
  value: string;
  disabled?: boolean;
  placeholder?: string;
  onChange: (v: string) => void;
}) {
  return (
    <Input
      data-testid={testId}
      className="h-8 font-mono text-xs"
      value={value}
      disabled={disabled}
      placeholder={placeholder}
      onChange={(e) => onChange(e.target.value)}
    />
  );
}

function reasonOf(t: Translate, error: string | undefined, whole: string | undefined): string {
  const reason = error ?? whole ?? "";
  return reason ? t("agents.picker_failed", { reason }) : "";
}

/**
 * Model select for `vlm.llm_id` / Extractor `llm_id`.
 *
 * `kind="vision"` lists the image2text+ocr catalog (the engine resolves
 * `vlm.llm_id` as a vision model only); `kind="chat"` lists chat models. An
 * empty catalog — or a failed section — turns into a text input plus the reason,
 * because pasting an id is the documented escape hatch.
 */
export function PipelineModelSelect({
  name,
  kind,
  value,
  disabled,
  allowEmpty,
  onChange,
}: {
  name: string;
  kind: "chat" | "vision";
  value?: string;
  disabled?: boolean;
  /** Render a blank option ("follow the engine default") above the catalog. */
  allowEmpty?: boolean;
  onChange: (v: string) => void;
}) {
  const t = useTranslate();
  const state = useAgentAuthoringOptions();
  const current = String(value ?? "");

  const catalog = kind === "vision" ? state.options.vision_models : state.options.models?.chat ?? [];
  const sectionError = kind === "vision" ? state.options.vision_models_error : state.options.models_error;
  const reason = reasonOf(t, sectionError, state.error?.displayMessage);

  if (state.loading) {
    return <p className="text-xs text-muted-foreground">{t("agents.picker_loading")}</p>;
  }
  if (reason || catalog.length === 0) {
    return (
      <div className="space-y-1">
        {fallbackInput({
          testId: `pipeline-model-input-${name}`,
          value: current,
          disabled,
          placeholder: "name@provider",
          onChange,
        })}
        <p className="text-xs text-muted-foreground">
          {reason || (kind === "vision" ? t("agents.pipe_vision_empty") : t("agents.picker_model_empty"))}
        </p>
      </div>
    );
  }

  const options = catalog.map((m) => ({ value: modelRef(m), label: modelLabel(m) }));
  if (current && !options.some((o) => o.value === current)) {
    options.unshift({ value: current, label: `${current} ${t("agents.picker_configured_not_in_list")}` });
  }

  return (
    <select
      data-testid={`pipeline-model-select-${name}`}
      className="w-full rounded border bg-background px-2 py-1 text-sm"
      value={current}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
    >
      {(allowEmpty || !current) && (
        <option value="">{kind === "vision" ? t("agents.pipe_vision_none") : t("agents.picker_model_follow_default_unnamed")}</option>
      )}
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  );
}

/**
 * Select over an engine catalog keyed by id (compilation templates / groups).
 *
 * A stored id that is no longer in the catalog stays selectable — the engine is
 * the judge of what exists, and the validation panel reports the dangling
 * reference; silently dropping it here would rewrite the canvas on open.
 */
export function PipelineCatalogSelect({
  testId,
  section,
  value,
  placeholder,
  emptyHint,
  disabled,
  onChange,
}: {
  testId: string;
  section: "compilation_templates" | "compilation_template_groups";
  value?: string;
  placeholder: string;
  emptyHint: string;
  disabled?: boolean;
  onChange: (v: string) => void;
}) {
  const t = useTranslate();
  const state = useAgentAuthoringOptions();
  const current = String(value ?? "");
  const items = state.options[section] ?? [];
  const reason = reasonOf(t, state.options[`${section}_error`], state.error?.displayMessage);

  if (state.loading) {
    return <p className="text-xs text-muted-foreground">{t("agents.picker_loading")}</p>;
  }
  if (reason || items.length === 0) {
    return (
      <div className="space-y-1">
        {fallbackInput({ testId: `${testId}-input`, value: current, disabled, placeholder, onChange })}
        <p className="text-xs text-muted-foreground">{reason || emptyHint}</p>
      </div>
    );
  }

  const options = items.map((item) => ({ value: item.id, label: item.name || item.id }));
  if (current && !options.some((o) => o.value === current)) {
    options.unshift({ value: current, label: `${current} ${t("agents.picker_configured_not_in_list")}` });
  }

  return (
    <select
      data-testid={testId}
      className="w-full rounded border bg-background px-2 py-1 text-sm"
      value={current}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
    >
      <option value="">{placeholder}</option>
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  );
}
