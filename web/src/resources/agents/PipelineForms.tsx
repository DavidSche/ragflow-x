// Dedicated property forms for data-pipeline operators (dataflow_canvas).
//
// The generic operator form renders unknown fields as JSON boxes; pipeline
// components (Parser/TokenChunker/TitleChunker/Extractor/…) get real controls
// here instead, mirroring the per-operator forms in ragflow
// (`web/src/pages/agent/form/<operator>-form`). Each form reads/writes the
// same `node.data.form` shape the canvas seed and round-trip use, so nothing
// else in the DSL bridge changes.

import { useEffect, useState, type ReactElement, type ReactNode } from "react";
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
  { value: "spreadsheet", label: "表格（Excel/CSV）" },
  { value: "image", label: "图片" },
  { value: "email", label: "邮件" },
  { value: "markdown", label: "Markdown" },
  { value: "text&code", label: "文本 / 代码" },
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
  image: [{ value: "text", label: "文本" }],
  email: [
    { value: "text", label: "文本" },
    { value: "json", label: "JSON" },
  ],
  markdown: [{ value: "json", label: "JSON" }],
  "text&code": [
    { value: "json", label: "JSON" },
    { value: "text", label: "文本" },
  ],
  html: [
    { value: "json", label: "JSON" },
    { value: "text", label: "文本" },
  ],
  doc: [{ value: "json", label: "JSON" }],
  docx: [{ value: "json", label: "JSON" }],
  slides: [{ value: "json", label: "JSON" }],
};

const PREPROCESS_OPTIONS = [
  { value: "main_content", label: "正文" },
  { value: "title", label: "标题" },
  { value: "abstract", label: "摘要" },
  { value: "author", label: "作者" },
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
  return (
    <select
      className="w-full rounded border bg-background px-2 py-1 text-sm"
      value={value ?? ""}
      disabled={disabled}
      onChange={(e) => onChange(e.target.value)}
    >
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
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
  return (
    <NumericRangeField
      className="h-8"
      hideRangeHint
      value={value ?? 0}
      min={min ?? 0}
      max={max ?? 100000}
      disabled={disabled}
      ariaLabel="数值"
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
      <Field label="输出汇总" hint="解析后统一输出 html / json / markdown / text 四种字段（只读）。">
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
                <button type="button" disabled={disabled || rows.length <= 1} onClick={() => sync(rows.filter((_, idx) => idx !== i))} className="rounded p-1 text-destructive disabled:opacity-40" title="删除该格式">
                  <Trash2 className="size-3.5" aria-hidden="true" />
                </button>
              </div>
              <div className="grid grid-cols-2 gap-2">
                <Field label="输出格式">
                  <SelectField value={row.output_format} disabled={disabled} options={OUTPUT_FORMAT_OPTIONS[fmt] ?? [{ value: "json", label: "JSON" }]} onChange={(v) => updateRow(i, { output_format: v })} />
                </Field>
                {(fmt === "pdf" || fmt === "spreadsheet" || fmt === "slides" || fmt === "image") && (
                  <Field label="解析方式">
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
                <Field label="预处理">
                  <SelectField value={row.preprocess} disabled={disabled} options={PREPROCESS_OPTIONS} onChange={(v) => updateRow(i, { preprocess: v })} />
                </Field>
                {fmt === "pdf" && (
                  <Field label="页范围 (from / to)">
                    <div className="flex items-center gap-1.5">
                      <NumberField value={row.pages?.[0]?.from ?? 1} disabled={disabled} min={1} onChange={(v) => updateRow(i, { pages: [{ from: v, to: row.pages?.[0]?.to ?? 100000 }] })} />
                      <span className="text-muted-foreground">～</span>
                      <NumberField value={row.pages?.[0]?.to ?? 100000} disabled={disabled} min={1} onChange={(v) => updateRow(i, { pages: [{ from: row.pages?.[0]?.from ?? 1, to: v }] })} />
                    </div>
                  </Field>
                )}
                {fmt === "email" && (
                  <div className="col-span-2 space-y-1">
                    <Label className="text-xs">提取字段</Label>
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
                    <Label className="text-xs">系统提示词（OCR）</Label>
                    <textarea className="min-h-12 w-full rounded border bg-background p-2 text-xs" value={row.system_prompt ?? ""} disabled={disabled} onChange={(e) => updateRow(i, { system_prompt: e.target.value })} />
                  </div>
                )}
                {(fmt === "pdf" || fmt === "spreadsheet" || fmt === "markdown" || fmt === "doc" || fmt === "docx") && (
                  <div className="col-span-2 flex flex-wrap gap-3">
                    <ToggleField label="图片转文本" checked={row.flatten_media_to_text} disabled={disabled} onChange={(v) => updateRow(i, { flatten_media_to_text: v })} />
                    {(fmt === "pdf" || fmt === "html" || fmt === "doc" || fmt === "docx") && (
                      <ToggleField label="去除页眉页脚" checked={row.remove_header_footer} disabled={disabled} onChange={(v) => updateRow(i, { remove_header_footer: v })} />
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
        添加文件格式
      </button>
    </div>
  );
}

// ─── TokenChunker / Tokenizer ───────────────────────────────────────────

function TokenChunkerForm({ values, disabled, onChange }: FormProps) {
  const delimiters: string[] = Array.isArray(values?.delimiters) ? values.delimiters : ["\n"];
  return (
    <div className="space-y-3">
      <Field label="分块 Token 数">
        <NumberField value={values?.chunk_token_size ?? 512} disabled={disabled} min={16} max={2048} onChange={(v) => onChange("chunk_token_size", v)} />
      </Field>
      <Field label="重叠比例（%）">
        <NumberField value={values?.overlapped_percent ?? 0} disabled={disabled} min={0} max={100} onChange={(v) => onChange("overlapped_percent", v)} />
      </Field>
      <Field label="分隔方式">
        <SelectField
          value={values?.delimiter_mode ?? "delimiter"}
          disabled={disabled}
          options={[
            { value: "delimiter", label: "分隔符" },
            { value: "one", label: "纯 Token" },
          ]}
          onChange={(v) => onChange("delimiter_mode", v)}
        />
      </Field>
      {values?.delimiter_mode !== "one" && (
        <div className="space-y-1.5">
          <Label className="text-xs">分隔符</Label>
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
            添加分隔符
          </button>
        </div>
      )}
    </div>
  );
}

function TokenizerForm({ values, disabled, onChange }: FormProps) {
  const methods: string[] = Array.isArray(values?.search_method) ? values.search_method : [];
  return (
    <div className="space-y-3">
      <div className="space-y-1">
        <Label className="text-xs">检索方式</Label>
        {["embedding", "full_text"].map((m) => (
          <ToggleField
            key={m}
            label={m === "embedding" ? "向量检索（Embedding）" : "全文检索（Full Text）"}
            checked={methods.includes(m)}
            disabled={disabled}
            onChange={(v) => onChange("search_method", v ? [...new Set([...methods, m])] : methods.filter((x) => x !== m))}
          />
        ))}
      </div>
      <Field label="文件名校验权重">
        <NumberField value={values?.filename_embd_weight ?? 0.1} disabled={disabled} min={0} max={1} onChange={(v) => onChange("filename_embd_weight", v)} />
      </Field>
      <Field label="索引字段">
        <SelectField
          value={values?.fields ?? "text"}
          disabled={disabled}
          options={[
            { value: "text", label: "正文" },
            { value: "questions", label: "问题" },
            { value: "summary", label: "摘要" },
          ]}
          onChange={(v) => onChange("fields", v)}
        />
      </Field>
    </div>
  );
}

// ─── TitleChunker ───────────────────────────────────────────────────────

function TitleChunkerForm({ values, disabled, onChange }: FormProps) {
  return (
    <div className="space-y-3">
      <Field label="分块方法">
        <SelectField
          value={values?.method ?? "hierarchy"}
          disabled={disabled}
          options={[
            { value: "hierarchy", label: "标题层级（H1…H4）" },
            { value: "group", label: "分组" },
          ]}
          onChange={(v) => onChange("method", v)}
        />
      </Field>
      <p className="text-xs text-muted-foreground">
        层级规则（每组标题正则表达式）可在“JSON”页签中精确编辑，本表单用于常用设置。
      </p>
    </div>
  );
}

// ─── Extractor ──────────────────────────────────────────────────────────

function ExtractorForm({ values, disabled, onChange }: FormProps) {
  const rules: { meta?: string; list?: string[] }[] = Array.isArray(values?.extraction_rules)
    ? values.extraction_rules
    : [{ meta: "", list: [] }];
  const sync = (next: { meta?: string; list?: string[] }[]) => onChange("extraction_rules", next);
  return (
    <div className="space-y-3">
      {rules.map((r, i) => (
        <div key={i} className="space-y-1.5 rounded border p-2">
          <div className="flex items-center gap-1.5">
            <Input className="h-8 flex-1 text-xs" placeholder="字段名（meta），例如 keyword / 作者" value={r.meta ?? ""} disabled={disabled} onChange={(e) => sync(rules.map((x, idx) => (idx === i ? { ...x, meta: e.target.value } : x)))} />
            <button type="button" disabled={disabled || rules.length <= 1} onClick={() => sync(rules.filter((_, idx) => idx !== i))} className="rounded p-1 text-destructive disabled:opacity-40">
              <Trash2 className="size-3.5" aria-hidden="true" />
            </button>
          </div>
          <textarea
            className="min-h-14 w-full rounded border bg-background p-2 font-mono text-xs"
            placeholder="每行一条抽取表达式（正则）"
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
        添加抽取字段
      </button>
    </div>
  );
}

// ─── Compiler ───────────────────────────────────────────────────────────

function CompilerForm({ values, disabled, onChange }: FormProps) {
  return (
    <div className="space-y-3">
      <Field label="知识模板">
        <SelectField
          value={values?.template ?? "general"}
          disabled={disabled}
          options={[
            { value: "general", label: "通用模板" },
            { value: "entity", label: "实体（Entity）" },
            { value: "concept", label: "概念（Concept）" },
            { value: "topic", label: "主题（Topic）" },
          ]}
          onChange={(v) => onChange("template", v)}
        />
      </Field>
      <Field label="编译提示词">
        <textarea className="min-h-16 w-full rounded border bg-background p-2 text-xs" value={values?.subtask_prompt ?? ""} disabled={disabled} onChange={(e) => onChange("subtask_prompt", e.target.value)} />
      </Field>
    </div>
  );
}

// ─── Loop / ExcelProcessor / File ───────────────────────────────────────

function LoopForm({ values, disabled, onChange }: FormProps) {
  return (
    <div className="space-y-3">
      <Field label="循环列表变量" hint="例如 {sys.files}">
        <Input className="h-8 text-sm" value={values?.items_list ?? ""} disabled={disabled} onChange={(e) => onChange("items_list", e.target.value)} />
      </Field>
      <Field label="输出目标">
        <Input className="h-8 text-sm" value={values?.to ?? ""} disabled={disabled} onChange={(e) => onChange("to", e.target.value)} />
      </Field>
    </div>
  );
}

function ExcelForm({ values, disabled, onChange }: FormProps) {
  return (
    <div className="space-y-3">
      <Field label="处理操作">
        <SelectField
          value={values?.operation ?? "read"}
          disabled={disabled}
          options={[
            { value: "read", label: "读取" },
            { value: "write", label: "写入" },
          ]}
          onChange={(v) => onChange("operation", v)}
        />
      </Field>
    </div>
  );
}

function FileForm({ values }: FormProps) {
  return (
    <div className="space-y-2">
      <p className="text-xs text-muted-foreground">
        数据管道起点：接收上传文件，输出 <code>name</code>（文件名）与 <code>file</code>（文件对象）。无需配置参数。
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
