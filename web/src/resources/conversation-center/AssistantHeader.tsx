import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { ConversationExportMenu } from "../workbench/ConversationExportMenu";
import { targetSubtitle } from "./utils";
import type { Message } from "../workbench/workbench-types";
import type { ConversationContext, ConversationKind, ConversationStatus, ConversationTargetRef } from "./types";

interface AssistantHeaderProps {
  target: ConversationTargetRef | null;
  status: ConversationStatus;
  busy: boolean;
  kind: ConversationKind;
  context: ConversationContext | null;
  messages: Message[];
  exportDisabled: boolean;
  onHelpToggle: () => void;
  onExport: (format: string) => void;
}

export function AssistantHeader({
  target,
  status,
  busy,
  kind,
  context,
  messages,
  exportDisabled,
  onHelpToggle,
  onExport,
}: AssistantHeaderProps) {
  const t = useTranslate();
  const statusText: Record<ConversationStatus, string> = {
    IDLE: "conversationCenter.status_idle",
    LOADING_CONTEXT: "conversationCenter.status_loading",
    READY: "conversationCenter.status_ready",
    PREPARING: "conversationCenter.status_preparing",
    STREAMING: "conversationCenter.status_streaming",
    CANCELLING: "conversationCenter.status_cancelling",
    COMPLETED: "conversationCenter.status_completed",
    FAILED: "conversationCenter.status_failed",
    CANCELLED: "conversationCenter.status_cancelled",
  };
  return (
    <>
      <div className="flex min-w-0 items-start gap-3">
        <div className="min-w-0">
          <h1 className="truncate text-lg font-semibold leading-6">{target ? target.name : t("conversationCenter.select_target")}</h1>
          <div className="truncate text-xs text-muted-foreground">
            {target ? targetSubtitle(target, t) : t("conversationCenter.select_target")}
          </div>
          {target?.knowledgeScope?.length ? (
            <div className="truncate text-xs text-muted-foreground">
              {t("conversationCenter.knowledge_scope")}
              {target.knowledgeScope.slice(0, 2).join("、")}
              {target.knowledgeScope.length > 2 ? ` +${target.knowledgeScope.length - 2}` : ""}
            </div>
          ) : null}
        </div>
        <div className="ml-auto flex shrink-0 items-center gap-1 rounded border px-1.5 py-0.5 text-xs text-muted-foreground">
          <span className={`size-1.5 rounded-full ${busy ? "bg-amber-500" : status === "FAILED" ? "bg-destructive" : "bg-emerald-500"}`} />
          <span>{t(statusText[status])}</span>
        </div>
        <span className="shrink-0 rounded border px-1.5 py-0.5 text-xs text-muted-foreground">
          {context?.type === "run" || kind === "search" ? t("conversationCenter.context_run") : t("conversationCenter.context_session")}
        </span>
      </div>
      <div className="ml-auto flex items-center gap-1">
        <Button size="sm" variant="ghost" onClick={onHelpToggle}>{t("conversationCenter.help")}</Button>
        <ConversationExportMenu
          messages={messages}
          title={target?.name ?? t("conversationCenter.title")}
          disabled={exportDisabled}
          onExport={onExport}
        />
      </div>
    </>
  );
}
