import { useEffect, useState } from "react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { FieldDef } from "./operatorForms";

interface Props {
  fields: FieldDef[];
  values: Record<string, any>;
  disabled?: boolean;
  onChange: (name: string, value: any) => void;
}

export function OperatorParamForm({ fields, values, disabled, onChange }: Props) {
  const [jsonTexts, setJsonTexts] = useState<Record<string, string>>({});

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

  if (fields.length === 0) {
    return <p className="text-sm text-muted-foreground">该算子无配置参数。</p>;
  }

  const setJson = (f: FieldDef, raw: string) => {
    setJsonTexts((p) => ({ ...p, [f.name]: raw }));
    try {
      onChange(f.name, JSON.parse(raw));
    } catch {
      /* keep previous value while JSON is invalid */
    }
  };

  return (
    <div className="space-y-3">
      {fields.map((f) => (
        <div key={f.name} className="space-y-1">
          <Label className="text-xs">{f.label}</Label>
          {f.type === "text" && (
            <Input className="h-8 text-sm" value={values[f.name] ?? f.default ?? ""} disabled={disabled} onChange={(e) => onChange(f.name, e.target.value)} placeholder={f.placeholder} />
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
                  {o.label}
                </option>
              ))}
            </select>
          )}
          {f.type === "switch" && (
            <label className="flex items-center gap-2 text-sm">
              <input type="checkbox" className="size-4" checked={!!(values[f.name] ?? f.default)} disabled={disabled} onChange={(e) => onChange(f.name, e.target.checked)} />
              {f.label}
            </label>
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
              {f.help ? <p className="text-xs text-muted-foreground">{f.help}</p> : null}
            </>
          )}
        </div>
      ))}
    </div>
  );
}
