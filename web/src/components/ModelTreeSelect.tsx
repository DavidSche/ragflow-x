import { useEffect, useMemo, useState } from "react";
import { useTranslate } from "ra-core";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { ModelDefaultOption } from "@/lib/api";

function unique(values: (string | undefined)[]): string[] {
  return Array.from(new Set(values.filter((v): v is string => !!v))).sort();
}

interface Props {
  options: ModelDefaultOption[];
  /** Currently selected model_id ("" = nothing chosen). */
  value: string;
  onSelect: (modelId: string) => void;
  disabled?: boolean;
}

/**
 * Three-level Provider ▸ Instance ▸ Model tree selector for choosing a
 * default model (doc/269 §4.4, mirrors RAGFlow buildModelTree). Selecting a
 * leaf (concrete model) is required; provider/instance nodes are only expanders.
 * Independent component under components/ (spec 26 R1——R7: reusable, ≤400 lines).
 */
export function ModelTreeSelect({ options, value, onSelect, disabled }: Props) {
  const t = useTranslate();
  const [provider, setProvider] = useState("");
  const [instance, setInstance] = useState("");

  const providers = useMemo(() => unique(options.map((o) => o.provider_name)), [options]);
  const instances = useMemo(
    () => unique(options.filter((o) => o.provider_name === provider).map((o) => o.instance_name)),
    [options, provider],
  );
  const models = useMemo(
    () =>
      options
        .filter((o) => o.provider_name === provider && o.instance_name === instance)
        .sort((a, b) => a.name.localeCompare(b.name)),
    [options, provider, instance],
  );

  // Re-seed provider/instance from a preset value (e.g. current default).
  useEffect(() => {
    if (!value) {
      setProvider("");
      setInstance("");
      return;
    }
    const match = options.find((o) => o.model_id === value);
    if (match) {
      setProvider(match.provider_name ?? "");
      setInstance(match.instance_name ?? "");
    }
  }, [value, options]);

  const noOptions = options.length === 0;
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Select
        value={provider}
        onValueChange={(v) => {
          setProvider(v);
          setInstance("");
          onSelect("");
        }}
        disabled={disabled || noOptions}
        data-testid="tree-provider"
      >
        <SelectTrigger className="w-40" aria-label={t("providers.default_provider")}>
          <SelectValue placeholder={t("providers.default_select_provider")} />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectLabel>{t("providers.default_provider")}</SelectLabel>
            {providers.map((p) => (
              <SelectItem key={p} value={p}>
                {p}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>

      <Select
        value={instance}
        onValueChange={(v) => {
          setInstance(v);
          onSelect("");
        }}
        disabled={disabled || !provider || instances.length === 0}
        data-testid="tree-instance"
      >
        <SelectTrigger className="w-44" aria-label={t("providers.default_instance")}>
          <SelectValue placeholder={t("providers.default_select_instance")} />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectLabel>{t("providers.default_instance")}</SelectLabel>
            {instances.map((i) => (
              <SelectItem key={i} value={i}>
                {i}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>

      <Select
        value={value}
        onValueChange={onSelect}
        disabled={disabled || !provider || !instance || models.length === 0}
        data-testid="tree-model"
      >
        <SelectTrigger className="w-56" aria-label={t("providers.default_model")}>
          <SelectValue placeholder={t("providers.default_select_model")} />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectLabel>{t("providers.default_model")}</SelectLabel>
            {models.map((m) => (
              <SelectItem key={m.model_id} value={m.model_id}>
                {m.name}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    </div>
  );
}
