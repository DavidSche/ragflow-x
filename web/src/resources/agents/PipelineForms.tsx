// Dedicated property forms for data-pipeline operators (dataflow_canvas).
//
// The generic operator form renders unknown fields as JSON boxes; pipeline
// components (Parser/TokenChunker/TitleChunker/Extractor/…) get real controls
// here instead, mirroring the per-operator forms in ragflow
// (`web/src/pages/agent/form/<operator>-form`). Each form reads/writes the
// same `node.data.form` shape the canvas seed and round-trip use, so nothing
// else in the DSL bridge changes.

import { useEffect, useState, type ReactElement, type ReactNode } from "react";
import { useTranslate } from "ra-core";
import { Plus, Trash2 } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NumericRangeField } from "@/components/NumericRangeField";

interface FormProps {
  values: Record<string, any>;
  disabled?: boolean;
  onChange: (name: string, value: any) => void;
}

const FILE_FORMATS = [
  { value: "pdf", label: "PDF" },
  { value: "spreadsheet", label: "agents.pipe_fmt_spreadsheet" },
  { value: "image", label: "agents.pipe_fmt_image" },
  { value: "email", label: "agents.pipe_fmt_email" },
  { value: "markdown", label: "Markdown" },
  { value: "text&code", label: "agents.pipe_fmt_text_code" },
  { value: "html", label: "HTML" },
  { value: "doc", label: "Word (doc)" },
  { value: "docx", label: "Word (docx)" },
  { value: "slides", label: "PPT" },
];

const OUTPUT_FORMAT_OPTIONS: Record<string, { value: string; label: string }[]> = {
  pdf: [
    { value: "json", label: "JSON" },
    { value: "markdown", label: "Markdown" },
  ],
  spreadsheet: [
    { value: "json", label: "JSON" },
    { value: "html", label: "HTML" },
  ],
  image: [{ value: "text", label: "agents.pipe_text" }],
  email: [
    { value: "text", label: "agents.pipe_text" },
    { value: "json", label: "JSON" },
  ],
  markdown: [{ value: "json", label: "JSON" }],
  "text&code": [
    { value: "json", label: "JSON" },
    { value: "text", label: "agents.pipe_text" },
  ],
  html: [
    { value: "json", label: "JSON" },
    { value: "text", label: "agents.pipe_text" },
  ],
  doc: [{ value: "json", label: "JSON" }],
  docx: [{ value: "json", label: "JSON" }],
  slides: [{ value: "json", label: "JSON" }],
};

const PREPROCESS_OPTIONS = [
  { value: "main_content", label: "agents.pipe_body" },
  { value: "title", label: "agents.pipe_title" },
  { value: "abstract", label: "agents.pipe_summary" },
  { value: "author", label: "agents.pipe_author" },
];

const EMAIL_FIELDS = ["from", "to", "cc", "bcc", "date", "subject", "body", "attachments"];

function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  return (
    <div className="space-y-1">
      <Label className="text-xs">{label}</Label>
      {children}
      {hint ? <p className="text-[11px] text-muted-foreground">{hint}</p> : null}
    </div>
  );
}

function SelectField({
  value,
  options,
  disabled,
  onChange,
}: {
  value?: string;
  options: { value: string; label: string }[];
  disabled?: boolean;
  onChange: (v: string) => void;
}) {
  const t = useTranslate();
  return (
    <select
      className="w-full rounded border bg-background px-2 py-1 text-sm"
      value={value ?? ""}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
    >
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {t(o.label)}
        </option>
      ))}
    </select>
  );
}

function NumberField({
  value,
  disabled,
  min,
  max,
  onChange,
}: {
  value?: number;
  disabled?: boolean;
  min?: number;
  max?: number;
  onChange: (v: number) => void;
}) {
  const t = useTranslate();
  return (
    <NumericRangeField
      className="h-8"
      hideRangeHint
      value={value ?? 0}
      min={min ?? 0}
      max={max ?? 100000}
      disabled={disabled}
      ariaLabel={t("agents.pipe_number")}
      onChange={(next) => onChange(next ?? 0)}
    />
  );
}

function ToggleField({
  label,
  checked,
  disabled,
  onChange,
}: {
  label: string;
  checked?: boolean;
  disabled?: boolean;
  onChange: (v: boolean) => void;
}) {
  const t = useTranslate();
  return (
    <label className="flex cursor-pointer items-center gap-2 text-sm">
      <input type="checkbox" className="size-4" checked={!!checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} />
      {label}
    </label>
  );
}

function editableSetups(values: Record<string, any>): Record<string, any>[] {
  const s = values?.setups;
  if (Array.isArray(s)) return s;
  // Engine-shaped object ({fileFormat: {...}}): rebuild the editor array.
  if (s && typeof s === "object") {
    return Object.keys(s).map((k) => ({ ...(s[k] ?? {}), fileFormat: k }));
  }
  return [];
}

// ─── Parser ─────────────────────────────────────────────────────────────

function ParserForm({ values, disabled, onChange }: FormProps) {
  const t = useTranslate();
  const [rows, setRows] = useState<Record<string, any>[]>(() => editableSetups(values));

  const sync = (next: Record<string, any>[]) => {
    setRows(next);
    onChange("setups", next);
  };

  useEffect(() => {
    const current = editableSetups(values);
    if (JSON.stringify(current) !== JSON.stringify(rows)) {
      setRows(current);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [values?.setups]);

  const updateRow = (index: number, patch: Record<string, any>) => {
    const next = rows.map((r, i) => (i === index ? { ...r, ...patch } : r));
    sync(next);
  };

  const addRow = () => {
    sync([...rows, { fileFormat: "pdf", output_format: "json", parse_method: "DeepDOC", preprocess: "main_content", flatten_media_to_text: false, remove_header_footer: false }]);
  };

  return (
    <div className="space-y-3">
      <Field label={t("agents.pipe_outputs")} hint={t("agents.pipe_outputs_hint")}>
        <div className="grid grid-cols-2 gap-1.5 rounded border bg-muted/40 p-2 text-xs">
          {Object.keys(values?.outputs ?? {}).map((k) => (
            <div key={k} className="flex items-center justify-between">
              <span className="text-muted-foreground">{k}</span>
              <span className="font-mono">{values.outputs[k]?.type ?? "-"}</span>
            </div>
          ))}
        </div>
      </Field>

      <div className="space-y-2">
        {rows.map((row, i) => {
          const fmt = row.fileFormat ?? "pdf";
          return (
            <div key={i} className="space-y-2 rounded border p-2">
              <div className="flex items-center justify-between gap-2">
                <SelectField
                  value={fmt}
                  disabled={disabled}
                  options={FILE_FORMATS}
                  onChange={(v) => updateRow(i, { fileFormat: v, output_format: OUTPUT_FORMAT_OPTIONS[v]?.[0]?.value })}
                />
                <button type="button" disabled={disabled || rows.length <= 1} onClick={() => sync(rows.filter((_, idx) => idx !== i))} className="rounded p-1 text-destructive disabled:opacity-40" title={t("agents.pipe_remove_format")}>
                  <Trash2 className="size-3.5" aria-hidden="true" />
                </button>
              </div>
              <div className="grid grid-cols-2 gap-2">
                <Field label={t("agents.pipe_output_format")}>
                  <SelectField value={row.output_format} disabled={disabled} options={OUTPUT_FORMAT_OPTIONS[fmt] ?? [{ value: "json", label: "JSON" }]} onChange={(v) => updateRow(i, { output_format: v })} />
                </Field>
                {(fmt === "pdf" || fmt === "spreadsheet" || fmt === "slides" || fmt === "image") && (
                  <Field label={t("agents.pipe_parse_method")}>
                    <SelectField
                      value={row.parse_method}
                      disabled={disabled}
                      options={[
                        { value: "DeepDOC", label: "DeepDOC" },
                        { value: "ocr", label: "OCR" },
                      ]}
                      onChange={(v) => updateRow(i, { parse_method: v })}
                    />
                  </Field>
                )}
                <Field label={t("agents.pipe_preprocess")}>
                  <SelectField value={row.preprocess} disabled={disabled} options={PREPROCESS_OPTIONS} onChange={(v) => updateRow(i, { preprocess: v })} />
                </Field>
                {fmt === "pdf" && (
                  <Field label={t("agents.pipe_pages")}>
                    <div className="flex items-center gap-1.5">
                      <NumberField value={row.pages?.[0]?.from ?? 1} disabled={disabled} min={1} onChange={(v) => updateRow(i, { pages: [{ from: v, to: row.pages?.[0]?.to ?? 100000 }] })} />
                      <span className="text-muted-foreground">～</span>
                      <NumberField value={row.pages?.[0]?.to ?? 100000} disabled={disabled} min={1} onChange={(v) => updateRow(i, { pages: [{ from: row.pages?.[0]?.from ?? 1, to: v }] })} />
                    </div>
                  </Field>
                )}
                {fmt === "email" && (
                  <div className="col-span-2 space-y-1">
                    <Label className="text-xs">{t("agents.pipe_email_fields")}</Label>
                    <div className="flex flex-wrap gap-2">
                      {EMAIL_FIELDS.map((f) => (
                        <label key={f} className="flex cursor-pointer items-center gap-1 text-xs">
                          <input
                            type="checkbox"
                            className="size-3.5"
                            checked={(row.fields ?? EMAIL_FIELDS).includes(f)}
                            disabled={disabled}
                            onChange={(e) => {
                              const cur = row.fields ?? EMAIL_FIELDS;
                              const next = e.target.checked ? [...cur, f] : cur.filter((x: string) => x !== f);
                              updateRow(i, { fields: next });
                            }}
                          />
                          {f}
                        </label>
                      ))}
                    </div>
                  </div>
                )}
                {fmt === "image" && (
                  <div className="col-span-2 space-y-1">
                    <Label className="text-xs">{t("agents.pipe_ocr_prompt")}</Label>
                    <textarea className="min-h-12 w-full rounded border bg-background p-2 text-xs" value={row.system_prompt ?? ""} disabled={disabled} onChange={(e) => updateRow(i, { system_prompt: e.target.value })} />
                  </div>
                )}
                {(fmt === "pdf" || fmt === "spreadsheet" || fmt === "markdown" || fmt === "doc" || fmt === "docx") && (
                  <div className="col-span-2 flex flex-wrap gap-3">
                    <ToggleField label={t("agents.pipe_flatten_media")} checked={row.flatten_media_to_text} disabled={disabled} onChange={(v) => updateRow(i, { flatten_media_to_text: v })} />
                    {(fmt === "pdf" || fmt === "html" || fmt === "doc" || fmt === "docx") && (
                      <ToggleField label={t("agents.pipe_remove_header_footer")} checked={row.remove_header_footer} disabled={disabled} onChange={(v) => updateRow(i, { remove_header_footer: v })} />
                    )}
                  </div>
                )}
              </div>
            </div>
          );
        })}
      </div>

      <button type="button" disabled={disabled} onClick={addRow} className="flex w-full items-center justify-center gap-1 rounded border border-dashed px-2 py-1.5 text-xs hover:bg-muted disabled:opacity-50">
        <Plus className="size-3.5" aria-hidden="true" />
        {t("agents.pipe_add_format")}
      </button>
    </div>
  );
}

// ─── TokenChunker / Tokenizer ───────────────────────────────────────────

function TokenChunkerForm({ values, disabled, onChange }: FormProps) {
  const t = useTranslate();
  const delimiters: string[] = Array.isArray(values?.delimiters) ? values.delimiters : ["\n"];
  return (
    <div className="space-y-3">
      <Field label={t("agents.pipe_chunk_tokens")}>
        <NumberField value={values?.chunk_token_size ?? 512} disabled={disabled} min={16} max={2048} onChange={(v) => onChange("chunk_token_size", v)} />
      </Field>
      <Field label={t("agents.pipe_chunk_overlap")}>
        <NumberField value={values?.overlapped_percent ?? 0} disabled={disabled} min={0} max={100} onChange={(v) => onChange("overlapped_percent", v)} />
      </Field>
      <Field label={t("agents.pipe_delimiter_mode")}>
        <SelectField
          value={values?.delimiter_mode ?? "delimiter"}
          disabled={disabled}
          options={[
            { value: "delimiter", label: "agents.pipe_delimiter" },
            { value: "one", label: "agents.pipe_token_only" },
          ]}
          onChange={(v) => onChange("delimiter_mode", v)}
        />
      </Field>
      {values?.delimiter_mode !== "one" && (
        <div className="space-y-1.5">
          <Label className="text-xs">{t("agents.pipe_delimiter")}</Label>
          {delimiters.map((d, i) => (
            <div key={i} className="flex items-center gap-1.5">
              <Input className="h-8 flex-1 font-mono text-xs" value={d} disabled={disabled} onChange={(e) => onChange("delimiters", delimiters.map((x, idx) => (idx === i ? e.target.value : x)))} />
              <button type="button" disabled={disabled || delimiters.length <= 1} onClick={() => onChange("delimiters", delimiters.filter((_, idx) => idx !== i))} className="rounded p-1 text-destructive disabled:opacity-40">
                <Trash2 className="size-3.5" aria-hidden="true" />
              </button>
            </div>
          ))}
          <button type="button" disabled={disabled} onClick={() => onChange("delimiters", [...delimiters, "。"])} className="flex items-center gap-1 text-xs text-primary hover:underline">
            <Plus className="size-3.5" aria-hidden="true" />
            {t("agents.pipe_add_delimiter")}
          </button>
        </div>
      )}
    </div>
  );
}

function TokenizerForm({ values, disabled, onChange }: FormProps) {
  const t = useTranslate();
  const methods: string[] = Array.isArray(values?.search_method) ? values.search_method : [];
  return (
    <div className="space-y-3">
      <div className="space-y-1">
        <Label className="text-xs">{t("agents.pipe_retrieval_mode")}</Label>
        {["embedding", "full_text"].map((m) => (
          <ToggleField
            key={m}
            label={t(m === "embedding" ? "agents.pipe_retrieval_embedding" : "agents.pipe_retrieval_fulltext")}
            checked={methods.includes(m)}
            disabled={disabled}
            onChange={(v) => onChange("search_method", v ? [...new Set([...methods, m])] : methods.filter((x) => x !== m))}
          />
        ))}
      </div>
      <Field label={t("agents.pipe_filename_weight")}>
        <NumberField value={values?.filename_embd_weight ?? 0.1} disabled={disabled} min={0} max={1} onChange={(v) => onChange("filename_embd_weight", v)} />
      </Field>
      <Field label={t("agents.pipe_index_field")}>
        <SelectField
          value={values?.fields ?? "text"}
          disabled={disabled}
          options={[
            { value: "text", label: "agents.pipe_body" },
            { value: "questions", label: "agents.pipe_question" },
            { value: "summary", label: "agents.pipe_summary" },
          ]}
          onChange={(v) => onChange("fields", v)}
        />
      </Field>
    </div>
  );
}

// ─── TitleChunker ───────────────────────────────────────────────────────

function TitleChunkerForm({ values, disabled, onChange }: FormProps) {
  const t = useTranslate();
  return (
    <div className="space-y-3">
      <Field label={t("agents.pipe_chunk_method")}>
        <SelectField
          value={values?.method ?? "hierarchy"}
          disabled={disabled}
          options={[
            { value: "hierarchy", label: "agents.pipe_chunk_hierarchy" },
            { value: "group", label: "agents.pipe_chunk_group" },
          ]}
          onChange={(v) => onChange("method", v)}
        />
      </Field>
      <p className="text-xs text-muted-foreground">
        {t("agents.pipe_hierarchy_hint")}
      </p>
    </div>
  );
}

// ─── Extractor ──────────────────────────────────────────────────────────

function ExtractorForm({ values, disabled, onChange }: FormProps) {
  const t = useTranslate();
  const rules: { meta?: string; list?: string[] }[] = Array.isArray(values?.extraction_rules)
    ? values.extraction_rules
    : [{ meta: "", list: [] }];
  const sync = (next: { meta?: string; list?: string[] }[]) => onChange("extraction_rules", next);
  return (
    <div className="space-y-3">
      {rules.map((r, i) => (
        <div key={i} className="space-y-1.5 rounded border p-2">
          <div className="flex items-center gap-1.5">
            <Input className="h-8 flex-1 text-xs" placeholder={t("agents.pipe_meta_placeholder")} value={r.meta ?? ""} disabled={disabled} onChange={(e) => sync(rules.map((x, idx) => (idx === i ? { ...x, meta: e.target.value } : x)))} />
            <button type="button" disabled={disabled || rules.length <= 1} onClick={() => sync(rules.filter((_, idx) => idx !== i))} className="rounded p-1 text-destructive disabled:opacity-40">
              <Trash2 className="size-3.5" aria-hidden="true" />
            </button>
          </div>
          <textarea
            className="min-h-14 w-full rounded border bg-background p-2 font-mono text-xs"
            placeholder={t("agents.pipe_regex_placeholder")}
            value={(r.list ?? []).join("\n")}
            disabled={disabled}
            onChange={(e) =>
              sync(
                rules.map((x, idx) =>
                  idx === i
                    ? {
                        ...x,
                        list: e.target.value.split("\n").filter((l) => l.trim() !== ""),
                      }
                    : x,
                ),
              )
            }
          />
        </div>
      ))}
      <button type="button" disabled={disabled} onClick={() => sync([...rules, { meta: "", list: [] }])} className="flex w-full items-center justify-center gap-1 rounded border border-dashed px-2 py-1.5 text-xs hover:bg-muted disabled:opacity-50">
        <Plus className="size-3.5" aria-hidden="true" />
        {t("agents.pipe_add_extract_field")}
      </button>
    </div>
  );
}

// ─── Compiler ───────────────────────────────────────────────────────────

function CompilerForm({ values, disabled, onChange }: FormProps) {
  const t = useTranslate();
  return (
    <div className="space-y-3">
      <Field label={t("agents.pipe_knowledge_template")}>
        <SelectField
          value={values?.template ?? "general"}
          disabled={disabled}
          options={[
            { value: "general", label: "agents.pipe_template_general" },
            { value: "entity", label: "agents.pipe_template_entity" },
            { value: "concept", label: "agents.pipe_template_concept" },
            { value: "topic", label: "agents.pipe_template_topic" },
          ]}
          onChange={(v) => onChange("template", v)}
        />
      </Field>
      <Field label={t("agents.pipe_compile_prompt")}>
        <textarea className="min-h-16 w-full rounded border bg-background p-2 text-xs" value={values?.subtask_prompt ?? ""} disabled={disabled} onChange={(e) => onChange("subtask_prompt", e.target.value)} />
      </Field>
    </div>
  );
}

// ─── Loop / ExcelProcessor / File ───────────────────────────────────────

function LoopForm({ values, disabled, onChange }: FormProps) {
  const t = useTranslate();
  return (
    <div className="space-y-3">
      <Field label={t("agents.pipe_loop_list")} hint={t("agents.pipe_loop_list_hint")}>
        <Input className="h-8 text-sm" value={values?.items_list ?? ""} disabled={disabled} onChange={(e) => onChange("items_list", e.target.value)} />
      </Field>
      <Field label={t("agents.pipe_output_target")}>
        <Input className="h-8 text-sm" value={values?.to ?? ""} disabled={disabled} onChange={(e) => onChange("to", e.target.value)} />
      </Field>
    </div>
  );
}

function ExcelForm({ values, disabled, onChange }: FormProps) {
  const t = useTranslate();
  return (
    <div className="space-y-3">
      <Field label={t("agents.pipe_operation")}>
        <SelectField
          value={values?.operation ?? "read"}
          disabled={disabled}
          options={[
            { value: "read", label: "agents.pipe_read" },
            { value: "write", label: "agents.pipe_write" },
          ]}
          onChange={(v) => onChange("operation", v)}
        />
      </Field>
    </div>
  );
}

function FileForm({ values }: FormProps) {
  const t = useTranslate();
  return (
    <div className="space-y-2">
      <p className="text-xs text-muted-foreground">
        {t("agents.pipe_file_hint")}
      </p>
      <div className="rounded border bg-muted/40 p-2 text-xs">
        {Object.keys(values?.outputs ?? {}).map((k) => (
          <div key={k} className="flex justify-between py-0.5">
            <span className="text-muted-foreground">{k}</span>
            <span className="font-mono">{values.outputs?.[k]?.type ?? "-"}</span>
          </div>
        ))}
      </div>
    </div>
  );
}

/** Whether `label` owns a dedicated pipeline form (else generic form applies). */
export function hasDedicatedPipelineForm(label?: string): boolean {
  return !!label && label in PIPELINE_FORM_BY_LABEL;
}

const PIPELINE_FORM_BY_LABEL: Record<string, (p: FormProps) => ReactElement> = {
  File: FileForm,
  Parser: ParserForm,
  TokenChunker: TokenChunkerForm,
  Tokenizer: TokenizerForm,
  TitleChunker: TitleChunkerForm,
  Extractor: ExtractorForm,
  Compiler: CompilerForm,
  Loop: LoopForm,
  ExcelProcessor: ExcelForm,
};

export function PipelineOperatorForm({ label, values, disabled, onChange }: FormProps & { label: string }) {
  const Form = PIPELINE_FORM_BY_LABEL[label];
  if (!Form) return null;
  return <Form values={values} disabled={disabled} onChange={onChange} />;
}
