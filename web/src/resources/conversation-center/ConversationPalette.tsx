import { useTranslate } from "ra-core";
import type { SlashCommandOption } from "./logic";
import type { ConversationTargetRef } from "./types";

interface ConversationPaletteProps {
  paletteId: string;
  activeOptionIndex: number;
  activeOptionId: string | undefined;
  commandOptions: SlashCommandOption[];
  targetOptions: ConversationTargetRef[];
  onRunCommand: (id: string) => void;
  onChooseTarget: (target: ConversationTargetRef) => void;
}

export function ConversationPalette({
  paletteId,
  activeOptionIndex,
  activeOptionId,
  commandOptions,
  targetOptions,
  onRunCommand,
  onChooseTarget,
}: ConversationPaletteProps) {
  const t = useTranslate();
  return (
    <div
      id={paletteId}
      role="listbox"
      aria-label={t("conversationCenter.palette")}
      className="absolute bottom-full left-0 z-20 mb-2 max-h-56 w-72 overflow-y-auto rounded border bg-background shadow-lg"
    >
      {commandOptions.length > 0 ? commandOptions.map((option, index) => (
        <button
          key={option.id}
          id={`${paletteId}-option-${index}`}
          type="button"
          role="option"
          aria-selected={index === activeOptionIndex}
          aria-describedby={index === activeOptionIndex ? activeOptionId : undefined}
          className={`w-full px-2 py-1.5 text-left text-sm hover:bg-muted ${index === activeOptionIndex ? "bg-muted" : ""}`}
          onClick={() => onRunCommand(option.id)}
        >
          <div className="font-medium">{option.label}</div>
          {option.hint && <div className="text-xs text-muted-foreground">{t(option.hint)}</div>}
        </button>
      )) : targetOptions.map((option, index) => (
        <button
          key={`${option.kind}:${option.id}`}
          id={`${paletteId}-option-${index}`}
          type="button"
          role="option"
          aria-selected={index === activeOptionIndex}
          aria-describedby={index === activeOptionIndex ? activeOptionId : undefined}
          className={`w-full px-2 py-1.5 text-left text-sm hover:bg-muted ${index === activeOptionIndex ? "bg-muted" : ""}`}
          onClick={() => onChooseTarget(option)}
        >
          <div className="font-medium">{option.name}</div>
          <div className="text-xs text-muted-foreground">{t(`conversationCenter.kind_${option.kind}`)}</div>
        </button>
      ))}
    </div>
  );
}
