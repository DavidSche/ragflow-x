import type { KeyboardEvent as ReactKeyboardEvent, RefObject } from "react";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Loader2, Plus, Send, Square, X } from "lucide-react";
import type { AttachmentDraft } from "../workbench/workbench-types";
import type { SlashCommandOption } from "./logic";
import { ConversationPalette } from "./ConversationPalette";
import type { ConversationTargetRef } from "./types";

interface ConversationCenterInputProps {
  input: string;
  inputRef: RefObject<HTMLTextAreaElement | null>;
  attachments: AttachmentDraft[];
  paletteOpen: boolean;
  paletteId: string;
  activeOptionIndex: number;
  activeOptionId: string | undefined;
  commandOptions: SlashCommandOption[];
  targetOptions: ConversationTargetRef[];
  disabled: boolean;
  busy: boolean;
  canAttach?: boolean;
  onAttachClick?: () => void;
  onChange: (value: string) => void;
  onKeyDown: (event: ReactKeyboardEvent<HTMLTextAreaElement>) => void;
  onCompositionStart: () => void;
  onCompositionEnd: () => void;
  onRemoveAttachment: (id: string) => void;
  onRunCommand: (id: string) => void;
  onChooseTarget: (target: ConversationTargetRef) => void;
  onCancel: () => void;
  onSubmit: () => void;
}

export function ConversationCenterInput({
  input,
  inputRef,
  attachments,
  paletteOpen,
  paletteId,
  activeOptionIndex,
  activeOptionId,
  commandOptions,
  targetOptions,
  disabled,
  busy,
  canAttach = false,
  onAttachClick,
  onChange,
  onKeyDown,
  onCompositionStart,
  onCompositionEnd,
  onRemoveAttachment,
  onRunCommand,
  onChooseTarget,
  onCancel,
  onSubmit,
}: ConversationCenterInputProps) {
  const t = useTranslate();
  return (
    <div className="relative rounded-md border bg-background">
        {paletteOpen && (
          <ConversationPalette
            paletteId={paletteId}
            activeOptionIndex={activeOptionIndex}
            activeOptionId={activeOptionId}
            commandOptions={commandOptions}
            targetOptions={targetOptions}
            onRunCommand={onRunCommand}
            onChooseTarget={onChooseTarget}
          />
        )}
        {attachments.length > 0 && (
          <div className="flex flex-wrap gap-1 px-2 pt-2">
            {attachments.map((item) => (
              <div key={item.id} className="flex items-center gap-1 rounded border bg-muted/40 px-2 py-1 text-xs">
                <span className="max-w-40 truncate">{item.name}</span>
                {item.status === "uploading" && <Loader2 className="size-3 animate-spin" />}
                {item.status === "error" && <span className="text-destructive">!</span>}
                <Button size="icon" variant="ghost" className="size-4" onClick={() => onRemoveAttachment(item.id)}><X className="size-3" /></Button>
              </div>
            ))}
          </div>
        )}
        <textarea
          ref={inputRef}
          value={input}
          onChange={(event) => onChange(event.target.value)}
          onKeyDown={onKeyDown}
          onCompositionStart={onCompositionStart}
          onCompositionEnd={onCompositionEnd}
          placeholder={t("conversationCenter.input_placeholder")}
          disabled={disabled}
          aria-autocomplete="list"
          aria-expanded={paletteOpen}
          aria-controls={paletteOpen ? paletteId : undefined}
          aria-activedescendant={activeOptionId}
          className="min-h-16 max-h-40 w-full resize-y border-0 bg-transparent p-2 text-sm focus-visible:outline-none focus-visible:ring-0"
          aria-label={t("conversationCenter.input_placeholder")}
        />
        <div className="flex items-center justify-between gap-2 p-2">
          {canAttach ? (
            <Button
              type="button"
              size="icon"
              variant="ghost"
              onClick={onAttachClick}
              disabled={busy}
              aria-label={t("conversationCenter.attach")}
              title={t("conversationCenter.attach")}
            >
              <Plus className="size-4" />
            </Button>
          ) : <span />}
          <div className="flex items-center gap-2">
            {busy ? (
              <Button size="sm" variant="outline" onClick={onCancel}><Square className="size-4" />{t("conversationCenter.stop")}</Button>
            ) : null}
            <Button size="sm" onClick={onSubmit} disabled={busy || disabled || !input.trim()}>
              <Send className="size-4" />{t("conversationCenter.send")}
            </Button>
          </div>
        </div>
    </div>
  );
}
