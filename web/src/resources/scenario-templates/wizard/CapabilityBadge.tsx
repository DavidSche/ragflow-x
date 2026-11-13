import { useTranslate } from "ra-core";
import { cn } from "@/lib/utils";
import type { CapabilityState } from "./types";

const STATE_STYLES: Record<CapabilityState, string> = {
  satisfied: "border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  probe_limited: "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  repairable: "border-sky-500/40 bg-sky-500/10 text-sky-700 dark:text-sky-300",
  blocked: "border-destructive/40 bg-destructive/10 text-destructive",
};

const STATE_ICONS: Record<CapabilityState, string> = {
  satisfied: "✅",
  probe_limited: "⚠️",
  repairable: "🔧",
  blocked: "⛔",
};

export function normalizeCapabilityState(value: string | undefined | null): CapabilityState {
  switch (value) {
    case "satisfied":
    case "probe_limited":
    case "repairable":
      return value;
    default:
      return "blocked";
  }
}

interface CapabilityBadgeProps {
  state: string | undefined | null;
  /** Text already localised by the caller; the badge never renders colour alone. */
  label: string;
  detail?: string;
  className?: string;
}

/**
 * Four-state capability badge (doc/276 §12). Accessibility rules that are easy
 * to lose in a refactor: the state is spelled out in text, the glyph is
 * `aria-hidden`, and the `aria-label` repeats state + label so a screen reader
 * user gets the same information as a sighted one.
 */
export function CapabilityBadge({ state, label, detail, className }: CapabilityBadgeProps) {
  const t = useTranslate();
  const normalized = normalizeCapabilityState(state);
  const stateLabel = t(`scenarioTemplates.capability.state.${normalized}`);
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium",
        STATE_STYLES[normalized],
        className,
      )}
      aria-label={`${stateLabel}: ${label}`}
      title={detail}
    >
      <span aria-hidden="true">{STATE_ICONS[normalized]}</span>
      <span>{label}</span>
      <span className="sr-only">{stateLabel}</span>
    </span>
  );
}
