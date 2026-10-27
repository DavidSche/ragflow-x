import { useEffect, useLayoutEffect, type ReactNode, type RefObject } from "react";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { ArrowDown } from "lucide-react";
import { featureFlags } from "../../lib/featureFlags";
import { ConversationMessageList } from "./ConversationMessageList";
import { useConversationScrollPinning } from "./use-conversation-scroll-pinning";
import { useTopScrollFade } from "./use-top-scroll-fade";
import type { Citation, Message, Rating } from "./workbench-types";

export interface ConversationScrollPinningApi {
  scrollToLatest: () => void;
  beginLoadEarlier: (batchCount: number) => void;
}

interface ConversationShellProps {
  messages: Message[];
  busy?: boolean;
  emptyText?: string;
  sidebar?: ReactNode;
  toolbar?: ReactNode;
  input?: ReactNode;
  beforeMessages?: ReactNode;
  afterMessages?: ReactNode;
  className?: string;
  sidebarClassName?: string;
  messageListClassName?: string;
  scrollClassName?: string;
  scrollRef?: RefObject<HTMLDivElement | null>;
  legacyScrollBehavior?: "force-bottom" | "none";
  scrollSessionKey?: string;
  scrollPinningRef?: RefObject<ConversationScrollPinningApi | null>;
  messagesAriaLabel?: string;
  commentTurn?: string;
  commentText?: string;
  onCommentTextChange?: (value: string) => void;
  onRate?: (turnId: string, rating: Rating, comment?: string) => void;
  onOpenComment?: (turnId: string) => void;
  onCancelComment?: () => void;
  onCopy?: (content: string) => void;
  onRegenerate?: (turnId: string) => void;
  onOpenCitation?: (citation: Citation) => void;
  onOpenDocument?: (citation: Citation) => void;
  onCreateKnowledgeTask?: (turnId: string) => void;
  onOpenContextSource?: (turnId: string) => void;
}

export function ConversationShell({
  messages,
  busy = false,
  emptyText,
  sidebar,
  toolbar,
  input,
  beforeMessages,
  afterMessages,
  className = "",
  sidebarClassName,
  messageListClassName = "space-y-3",
  scrollClassName,
  scrollRef,
  legacyScrollBehavior = "none",
  scrollSessionKey = "",
  scrollPinningRef,
  messagesAriaLabel,
  commentTurn,
  commentText,
  onCommentTextChange,
  onRate,
  onOpenComment,
  onCancelComment,
  onCopy,
  onRegenerate,
  onOpenCitation,
  onOpenDocument,
  onCreateKnowledgeTask,
  onOpenContextSource,
}: ConversationShellProps) {
  const t = useTranslate();
  const scrollPinningEnabled = featureFlags.scrollPinning;
  const scrollFadeEnabled = featureFlags.scrollFade;
  const scrollPinning = useConversationScrollPinning({
    enabled: scrollPinningEnabled,
    messagesVersion: messages,
    sessionKey: scrollSessionKey,
    scrollRef,
  });
  const showTopScrollFade = useTopScrollFade({
    enabled: scrollFadeEnabled,
    containerRef: scrollRef,
  });

  useLayoutEffect(() => {
    if (
      !scrollPinningEnabled &&
      legacyScrollBehavior === "force-bottom" &&
      scrollRef?.current
    ) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [legacyScrollBehavior, messages, scrollPinningEnabled, scrollRef]);

  useEffect(() => {
    if (!scrollPinningRef) return;
    scrollPinningRef.current = {
      scrollToLatest: scrollPinning.scrollToLatest,
      beginLoadEarlier: scrollPinning.beginLoadEarlier,
    };
  }, [
    scrollPinning.beginLoadEarlier,
    scrollPinning.scrollToLatest,
    scrollPinningRef,
  ]);

  return (
    <div className={`flex min-h-0 min-w-0 ${className}`}>
      {sidebar ? <div className={sidebarClassName}>{sidebar}</div> : null}
      <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3">
        {toolbar ? (
          <div className="flex shrink-0 flex-wrap items-center gap-2">
            {toolbar}
          </div>
        ) : null}
        {beforeMessages}
        <div
          className={`relative grid min-h-0 grid-rows-[minmax(0,1fr)] ${
            sidebar ? "max-md:h-[30dvh] md:flex-1" : "flex-1"
          }`}
        >
          <div
            ref={scrollRef}
            className={`flex h-full min-h-0 flex-col overflow-y-auto rounded border p-3 ${
              scrollPinningEnabled ? "[overflow-anchor:none]" : ""
            } ${scrollClassName ?? ""}`}
          >
            <div aria-label={messagesAriaLabel}>
              <ConversationMessageList
                className={messageListClassName}
                messages={messages}
                busy={busy}
                emptyText={emptyText}
                commentTurn={commentTurn}
                commentText={commentText}
                onCommentTextChange={onCommentTextChange}
                onRate={onRate}
                onOpenComment={onOpenComment}
                onCancelComment={onCancelComment}
                onCopy={onCopy}
                onRegenerate={onRegenerate}
                onOpenCitation={onOpenCitation}
                onOpenDocument={onOpenDocument}
                onCreateKnowledgeTask={onCreateKnowledgeTask}
                onOpenContextSource={onOpenContextSource}
              />
            </div>
          </div>
          {scrollFadeEnabled && showTopScrollFade ? (
            <div
              aria-hidden="true"
              data-testid="conversation-top-scroll-fade"
              className="pointer-events-none absolute inset-x-0 top-0 z-10 h-6 bg-background"
              style={{
                maskImage: "linear-gradient(to bottom, transparent, black)",
                WebkitMaskImage:
                  "linear-gradient(to bottom, transparent, black)",
              }}
            />
          ) : null}
          {scrollPinningEnabled && busy && !scrollPinning.pinned ? (
            <Button
              type="button"
              size="sm"
              variant="secondary"
              className="absolute bottom-3 right-3 shadow-sm"
              onClick={scrollPinning.scrollToLatest}
            >
              <ArrowDown className="size-4" aria-hidden="true" />
              {t("conversationCenter.scroll_to_latest")}
            </Button>
          ) : null}
        </div>
        {afterMessages}
        {input ? <div className="shrink-0">{input}</div> : null}
      </div>
    </div>
  );
}
