import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslate } from "ra-core";
import { Check, ChevronsUpDown } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Command,
  CommandEmpty,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { cn } from "@/lib/utils";
import { api, unwrap, type ApiEnvelope } from "../../lib/api";
import { optionalWarning } from "../../lib/optional-error";

/** One selectable value plus the label the server resolved for it. */
export interface UsageFilterOption {
  value: string;
  label: string;
}

/**
 * The distinct filter values present in usage data. Options are scoped by the
 * server to what the caller may see, so an operator never picks a model or a
 * workspace that cannot appear in the report.
 */
export interface UsageFilterOptions {
  models: UsageFilterOption[];
  scenarios: UsageFilterOption[];
  users: UsageFilterOption[];
  keys: UsageFilterOption[];
  chats: UsageFilterOption[];
  tenants: UsageFilterOption[];
  projects: UsageFilterOption[];
  assistants: UsageFilterOption[];
  releases: UsageFilterOption[];
}

export const emptyUsageFilterOptions: UsageFilterOptions = {
  models: [],
  scenarios: [],
  users: [],
  keys: [],
  chats: [],
  tenants: [],
  projects: [],
  assistants: [],
  releases: [],
};

/** Loads the dropdown values; `search` narrows them server-side. */
export function fetchUsageFilterOptions(search = ""): Promise<UsageFilterOptions> {
  const term = search.trim();
  const query = term ? `?search=${encodeURIComponent(term)}` : "";
  return unwrap(api.get<ApiEnvelope<UsageFilterOptions>>(`/usage/filter-options${query}`));
}

/**
 * Loads the dropdown values once per panel. Free-text filters made operators
 * type a 32-char user ID or a chat ID they had to look up elsewhere; the
 * server-side distinct list is the only source that stays true as data ages.
 *
 * A failed load leaves the selects empty (only "全部") instead of blocking the
 * report: the report itself is still useful without narrowing it.
 */
export function useUsageFilterOptions(): UsageFilterOptions {
  const [options, setOptions] = useState<UsageFilterOptions>(emptyUsageFilterOptions);

  useEffect(() => {
    let alive = true;
    fetchUsageFilterOptions()
      .then((data) => {
        if (!alive || !data) return;
        setOptions({ ...emptyUsageFilterOptions, ...data });
      })
      .catch((error) => optionalWarning(error, "用量过滤选项加载失败"));
    return () => {
      alive = false;
    };
  }, []);

  return options;
}

/** Native select styled like the rest of the filter row; "" means "no filter". */
export function UsageFilterSelect({
  filterKey,
  value,
  options,
  onChange,
}: {
  filterKey: string;
  value: string;
  options: UsageFilterOption[];
  onChange: (value: string) => void;
}) {
  const t = useTranslate();
  return (
    <select
      aria-label={t(`usage.filter_${filterKey}`)}
      className="h-9 w-full min-w-0 rounded-md border border-input bg-background px-2 text-sm"
      value={value}
      onChange={(event) => onChange(event.target.value)}
    >
      <option value="">{t("usage.filter_all")}</option>
      {options.map((option) => (
        <option key={option.value} value={option.value} title={option.value}>
          {option.label || option.value}
        </option>
      ))}
    </select>
  );
}

/**
 * Searchable filter for dimensions whose value count can outgrow a plain
 * select (assistants, chats). The full option list is already loaded, so the
 * list is usable immediately; typing asks `/usage/filter-options?search=` and
 * the server narrows by the resolved label (name) or ID, which a local filter
 * over a truncated list cannot guarantee.
 */
export function UsageFilterCombobox({
  filterKey,
  dimension,
  value,
  options,
  onChange,
}: {
  filterKey: string;
  dimension: keyof UsageFilterOptions;
  value: string;
  options: UsageFilterOption[];
  onChange: (value: string) => void;
}) {
  const t = useTranslate();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [searchResults, setSearchResults] = useState<UsageFilterOption[] | null>(null);
  const [searching, setSearching] = useState(false);
  // Remember the chosen label: once a search narrows the option list the
  // selected row may no longer be in it, and the trigger must not fall back to
  // showing a raw ID. A ref (not state) keeps the label current without adding
  // a render pass of its own.
  const knownLabels = useRef<Record<string, string>>({});

  const selected = useMemo(
    () => options.find((option) => option.value === value),
    [options, value],
  );
  if (selected) knownLabels.current[selected.value] = selected.label;

  useEffect(() => {
    if (!open) return;
    const term = query.trim();
    if (!term) {
      setSearchResults(null);
      setSearching(false);
      return;
    }
    let alive = true;
    setSearching(true);
    const timer = setTimeout(() => {
      fetchUsageFilterOptions(term)
        .then((data) => {
          if (alive) setSearchResults(data[dimension] ?? []);
        })
        .catch((error) => optionalWarning(error, "用量过滤选项搜索失败"))
        .finally(() => {
          if (alive) setSearching(false);
        });
    }, 250);
    return () => {
      alive = false;
      clearTimeout(timer);
    };
  }, [dimension, open, query]);

  const list = query.trim() ? (searchResults ?? []) : options;
  const selectedLabel = value ? (knownLabels.current[value] ?? value) : t("usage.filter_all");

  const close = () => {
    setOpen(false);
    setQuery("");
    setSearchResults(null);
  };

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        if (next) {
          setOpen(true);
          return;
        }
        close();
      }}
    >
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          role="combobox"
          aria-expanded={open}
          aria-label={t(`usage.filter_${filterKey}`)}
          title={value || undefined}
          className="h-9 w-full min-w-0 justify-between font-normal"
        >
          <span className={cn("truncate", !value && "text-muted-foreground")}>
            {selectedLabel}
          </span>
          <ChevronsUpDown className="size-4 shrink-0 opacity-50" aria-hidden="true" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-(--radix-popover-trigger-width) min-w-64 p-0" align="start">
        <Command shouldFilter={false}>
          <CommandInput
            aria-label={t("usage.filter_search_placeholder")}
            placeholder={t("usage.filter_search_placeholder")}
            value={query}
            onValueChange={setQuery}
          />
          <CommandList>
            <CommandEmpty>{t("usage.filter_no_match")}</CommandEmpty>
            <CommandItem
              value="__all__"
              onSelect={() => {
                onChange("");
                close();
              }}
            >
              <Check className={cn("size-4", value ? "opacity-0" : "opacity-100")} aria-hidden="true" />
              {t("usage.filter_all")}
            </CommandItem>
            {list.map((option) => (
              <CommandItem
                key={option.value}
                value={option.value}
                title={option.value}
                onSelect={() => {
                  onChange(option.value);
                  close();
                }}
              >
                <Check
                  className={cn("size-4 shrink-0", option.value === value ? "opacity-100" : "opacity-0")}
                  aria-hidden="true"
                />
                <div className="min-w-0">
                  <p className="truncate">{option.label || option.value}</p>
                  {option.label && option.label !== option.value ? (
                    <p className="truncate font-mono text-xs text-muted-foreground">{option.value}</p>
                  ) : null}
                </div>
              </CommandItem>
            ))}
          </CommandList>
          {searching ? (
            <p className="border-t px-3 py-2 text-xs text-muted-foreground">
              {t("usage.filter_searching")}
            </p>
          ) : null}
        </Command>
      </PopoverContent>
    </Popover>
  );
}
