import { useState, useId, type ReactNode } from "react";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

interface FormFieldProps {
  label: string;
  required?: boolean;
  error?: string;
  hint?: string;
  children: ReactNode;
  className?: string;
}

/**
 * Accessible form field wrapper.
 *
 * - Adds visual required indicator (*)
 * - Shows error message below the input with red styling + role="alert"
 * - Shows optional hint text
 *
 * The wrapper provides a labelled grouping; individual inputs should carry
 * their own aria-invalid / aria-describedby when managed by useFieldValidation.
 *
 * @example
 * <FormField label="用户名" required error={nameError}>
 *   <Input value={name} onChange={...} aria-invalid={!!nameError} />
 * </FormField>
 */
export function FormField({
  label,
  required,
  error,
  hint,
  children,
  className,
}: FormFieldProps) {
  const id = useId();
  const errorId = `${id}-error`;
  const hintId = `${id}-hint`;

  const describedBy = [hint ? hintId : "", error ? errorId : ""]
    .filter(Boolean)
    .join(" ") || undefined;

  return (
    <div className={cn("space-y-1.5", className)}>
      <Label htmlFor={id} className={cn(error && "text-destructive")}>
        {label}
        {required && (
          <span className="ml-0.5 text-destructive" aria-hidden="true">
            *
          </span>
        )}
      </Label>
      <div aria-describedby={describedBy}>{children}</div>
      {hint && !error && (
        <p id={hintId} className="text-xs text-muted-foreground">
          {hint}
        </p>
      )}
      {error && (
        <p id={errorId} className="text-xs text-destructive" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

/**
 * Hook to manage field validation state with ARIA attributes.
 *
 * @example
 * const field = useFieldValidation(name, { required: true, minLength: 2 });
 * <FormField label="名称" required error={field.error}>
 *   <Input {...field.inputProps} />
 * </FormField>
 */
export function useFieldValidation(
  initialValue: string,
  rules: {
    required?: boolean | string;
    minLength?: number;
    maxLength?: number;
    pattern?: { regex: RegExp; message: string };
    custom?: (value: string) => string | undefined;
  } = {},
) {
  const [value, setValue] = useState(initialValue);
  const [touched, setTouched] = useState(false);
  const [error, setError] = useState<string | undefined>(undefined);

  const validate = (v: string) => {
    if (rules.required) {
      if (!v.trim()) {
        setError(
          typeof rules.required === "string"
            ? rules.required
            : "此字段为必填项",
        );
        return;
      }
    }
    if (rules.minLength !== undefined && v.length < rules.minLength) {
      setError(`最少输入 ${rules.minLength} 个字符`);
      return;
    }
    if (rules.maxLength !== undefined && v.length > rules.maxLength) {
      setError(`最多输入 ${rules.maxLength} 个字符`);
      return;
    }
    if (rules.pattern && !rules.pattern.regex.test(v)) {
      setError(rules.pattern.message);
      return;
    }
    if (rules.custom) {
      const msg = rules.custom(v);
      if (msg) {
        setError(msg);
        return;
      }
    }
    setError(undefined);
  };

  const onChange = (newValue: string) => {
    setValue(newValue);
    if (touched) validate(newValue);
  };

  const onBlur = () => {
    setTouched(true);
    validate(value);
  };

  /** Validate immediately and return true if valid. */
  const validateNow = (): boolean => {
    setTouched(true);
    validate(value);
    // Re-validate synchronously (validate sets state async, so check inline)
    let valid = true;
    if (rules.required && !value.trim()) valid = false;
    if (rules.minLength !== undefined && value.length < rules.minLength) valid = false;
    if (rules.maxLength !== undefined && value.length > rules.maxLength) valid = false;
    if (rules.pattern && !rules.pattern.regex.test(value)) valid = false;
    if (valid && rules.custom) {
      const msg = rules.custom(value);
      if (msg) valid = false;
    }
    return valid;
  };

  const inputProps = {
    value,
    onChange: (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
      onChange(e.target.value),
    onBlur,
    "aria-invalid": touched && !!error || undefined,
    "aria-describedby": error ? undefined : undefined, // caller can add
  };

  return {
    value,
    onChange,
    onBlur,
    error: touched ? error : undefined,
    touched,
    validate: validateNow,
    setValue,
    /** Spread onto <Input> to get value/onChange/onBlur/aria-invalid */
    inputProps,
  };
}
