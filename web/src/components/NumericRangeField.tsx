import { useEffect, useId, useState } from "react";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { clampInteger, clampNumber } from "@/lib/numeric";

interface NumericRangeFieldProps {
  id?: string;
  label?: string;
  value: number | string;
  min: number;
  max: number;
  step?: number;
  unit?: string;
  disabled?: boolean;
  required?: boolean;
  showNumberInput?: boolean;
  allowEmpty?: boolean;
  fallbackValue?: number;
  hideRangeHint?: boolean;
  ariaLabel?: string;
  className?: string;
  onChange: (value: number | null) => void;
}

export function NumericRangeField({
  id,
  label,
  value,
  min,
  max,
  step = 1,
  unit,
  disabled,
  required,
  showNumberInput = true,
  allowEmpty = false,
  fallbackValue = min,
  hideRangeHint,
  ariaLabel,
  className,
  onChange,
}: NumericRangeFieldProps) {
  const generatedId = useId();
  const inputId = id ?? generatedId;
  const [text, setText] = useState(value === "" ? "" : String(value));

  useEffect(() => {
    setText(String(value));
  }, [value]);

  const number = Number(text);
  const finite = Number.isFinite(number);
  const fallback = finite || !allowEmpty ? fallbackValue : min;
  const sliderValue = clampNumber(finite ? number : fallback, min, max, min);
  const display = `${sliderValue}${unit ? ` ${unit}` : ""}`;

  const commit = (raw: string) => {
    const parsed = Number(raw);
    const next = allowEmpty && raw.trim() === ""
      ? null
      : Number.isFinite(parsed)
        ? step < 1
          ? clampNumber(parsed, min, max, min)
          : clampInteger(parsed, min, max, min)
      : fallbackValue;
    setText(next === null ? "" : String(next));
    onChange(next);
  };

  return (
    <div className={cn("space-y-1.5", className)}>
      {label ? (
        <div className="flex items-center justify-between text-sm">
          <Label htmlFor={inputId}>
            {label}
            {required ? <span className="ml-0.5 text-destructive">*</span> : null}
          </Label>
          <span className="text-xs tabular-nums text-muted-foreground">{display}</span>
        </div>
      ) : null}
      <div className="flex items-center gap-2">
        <input
          type="range"
          min={min}
          max={max}
          step={step}
          value={sliderValue}
          disabled={disabled}
          onChange={(event) => {
            const next = Number(event.target.value);
            setText(String(next));
            onChange(next);
          }}
          aria-label={ariaLabel ?? `${label ?? ""} ${unit ?? ""}`.trim()}
          className="min-w-0 flex-1 accent-foreground"
        />
        {showNumberInput ? (
          <Input
            id={inputId}
            type="number"
            inputMode={step < 1 ? "decimal" : "numeric"}
            min={min}
            max={max}
            step={step}
            required={required}
            disabled={disabled}
            value={text}
            onChange={(event) => {
              setText(event.target.value);
              const parsed = Number(event.target.value);
              if (Number.isFinite(parsed) && parsed >= min && parsed <= max) {
                onChange(step < 1 ? parsed : Math.round(parsed));
              }
            }}
            onBlur={(event) => commit(event.target.value)}
            className="w-24"
          />
        ) : null}
      </div>
      {!hideRangeHint ? (
        <p className="text-xs text-muted-foreground">
          {min} ~ {max}
        </p>
      ) : null}
    </div>
  );
}
