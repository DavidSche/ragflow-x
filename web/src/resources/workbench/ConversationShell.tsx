import { useEffect, type ReactNode, type RefObject } from "react";
import { ConversationMessageList } from "./ConversationMessageList";
import type { Citation, Message, Rating } from "./workbench-types";

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
}: ConversationShellProps) {
  useEffect(() => {
    if (scrollRef?.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [messages, scrollRef]);

  return (
    <div className={`flex min-h-0 min-w-0 ${className}`}>
      {sidebar ? <div className={sidebarClassName}>{sidebar}</div> : null}
      <div className="flex min-w-0 flex-1 flex-col gap-3">
        {toolbar ? <div className="flex shrink-0 flex-wrap items-center gap-2">{toolbar}</div> : null}
        {beforeMessages}
        <div ref={scrollRef} className={`flex min-h-0 flex-col overflow-y-auto rounded border p-3 ${scrollClassName ?? ""}`}>
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
            />
          </div>
        </div>
        {afterMessages}
        {input ? <div className="shrink-0">{input}</div> : null}
      </div>
    </div>
  );
}
