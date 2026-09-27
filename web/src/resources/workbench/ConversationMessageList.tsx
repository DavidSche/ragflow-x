import { MessageBubble } from "./MessageBubble";
import type { Citation, Message, Rating } from "./workbench-types";

interface ConversationMessageListProps {
  messages: Message[];
  busy?: boolean;
  emptyText?: string;
  className?: string;
  commentTurn?: string;
  commentText?: string;
  onCommentTextChange?: (v: string) => void;
  onRate?: (turnId: string, rating: Rating, comment?: string) => void;
  onOpenComment?: (turnId: string) => void;
  onCancelComment?: () => void;
  onCopy?: (content: string) => void;
  onRegenerate?: (turnId: string) => void;
  onOpenCitation?: (c: Citation) => void;
  onOpenDocument?: (c: Citation) => void;
  onCreateKnowledgeTask?: (turnId: string) => void;
}

export function ConversationMessageList({
  messages,
  busy = false,
  emptyText,
  className,
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
}: ConversationMessageListProps) {
  if (!messages.length && emptyText) {
    return <p className="m-auto text-sm text-muted-foreground">{emptyText}</p>;
  }

  return (
    <div className={className}>
      {messages.map((message, index) => (
        <MessageBubble
          key={index}
          message={message}
          index={index}
          isLastStreaming={busy && index === messages.length - 1}
          commentTurn={commentTurn}
          commentText={commentText}
          onCommentTextChange={onCommentTextChange}
          onRate={onRate}
          onOpenComment={onOpenComment}
          onCancelComment={onCancelComment}
          onCopy={onCopy ?? (() => {})}
          onRegenerate={onRegenerate}
          onOpenCitation={onOpenCitation}
          onOpenDocument={onOpenDocument}
          onCreateKnowledgeTask={onCreateKnowledgeTask}
        />
      ))}
    </div>
  );
}
