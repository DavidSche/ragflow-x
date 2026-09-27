/**
 * MessageBubble – renders a single message (user or assistant) with actions.
 */
import { useEffect, useRef, useState, type RefObject } from "react";
import { useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import {
  Loader2,
  ThumbsDown,
  ThumbsUp,
  Copy,
  RotateCcw,
  FileText,
  Square,
  Volume2,
} from "lucide-react";
import { AnswerMarkdown } from "./AnswerMarkdown";
import { AgentExecutionTimeline } from "./AgentExecutionTimeline";
import { ConversationArtifacts } from "./ConversationArtifacts";
import { InlineCitations } from "./InlineCitations";
import { CitationImage } from "./CitationImage";
import type { Citation, Message, Rating } from "./workbench-types";

interface MessageBubbleProps {
  message: Message;
  index: number;
  isLastStreaming: boolean;
  commentTurn?: string;
  commentText?: string;
  onCommentTextChange?: (v: string) => void;
  onRate?: (turnId: string, rating: Rating, comment?: string, attribution?: string) => void;
  onOpenComment?: (turnId: string) => void;
  onCancelComment?: () => void;
  onCopy: (content: string) => void;
  onRegenerate?: (turnId: string) => void;
  onOpenCitation?: (c: Citation) => void;
  onOpenDocument?: (c: Citation) => void;
  onCreateKnowledgeTask?: (turnId: string) => void;
}

export function MessageBubble({
  message: m,
  index,
  isLastStreaming,
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
}: MessageBubbleProps) {
  const commentInputRef = useRef<HTMLInputElement>(null);
  const translate = useTranslate();
  const finished = m.role === "assistant" && (m.turnId ?? "") !== "" && !isLastStreaming;
  const isNoHit = finished && !m.error && m.content.trim() === "" && (m.citations?.length ?? 0) === 0;
  const imageCitations = m.citations?.filter((c) => c.imageId) ?? [];
  const handleRate = onRate ?? (() => {});
  const handleOpenComment = onOpenComment ?? (() => {});
  const handleCancelComment = onCancelComment ?? (() => {});
  const handleCommentTextChange = onCommentTextChange ?? (() => {});
  const handleRegenerate = onRegenerate ?? (() => {});
  const handleOpenCitation = onOpenCitation ?? (() => {});
  const handleOpenDocument = onOpenDocument ?? (() => {});

  const [speaking, setSpeaking] = useState(false);
  const [attribution, setAttribution] = useState("");
  const speechRef = useRef<SpeechSynthesisUtterance | null>(null);

  useEffect(() => () => window.speechSynthesis?.cancel(), []);
  useEffect(() => {
    setAttribution("");
  }, [commentTurn]);
  useEffect(() => {
    if (commentTurn && commentTurn === m.turnId) commentInputRef.current?.focus();
  }, [commentTurn, m.turnId]);

  const toggleSpeak = () => {
    if (!window.speechSynthesis || !m.content) return;
    if (speaking) {
      window.speechSynthesis.cancel();
      setSpeaking(false);
      return;
    }
    const utterance = new SpeechSynthesisUtterance(m.content);
    utterance.lang = /[\u4e00-\u9fff]/.test(m.content) ? "zh-CN" : "en-US";
    utterance.onend = () => setSpeaking(false);
    utterance.onerror = () => setSpeaking(false);
    speechRef.current = utterance;
    window.speechSynthesis.cancel();
    window.speechSynthesis.speak(utterance);
    setSpeaking(true);
  };

  return (
    <div className={`text-sm ${m.role === "user" ? "text-right" : ""}`}>
      <div className="flex items-center gap-1" role="group" aria-label={translate("workbench.assistant_actions")}>
        <Badge variant={m.role === "user" ? "default" : "secondary"}>
          {m.role === "user" ? translate("workbench.user_label") : m.role === "system" ? "系统" : translate("workbench.ai_label")}
        </Badge>
        {m.role === "assistant" && m.turnId ? (
          <>
            {m.content && !isLastStreaming ? (
              <SmallIconButton
                label={speaking ? translate("workbench.stop_speak") : translate("workbench.speak")}
                onClick={() => void toggleSpeak()}
              >
                {speaking ? <Square className="size-3.5" /> : <Volume2 className="size-3.5" />}
              </SmallIconButton>
            ) : null}
            {onRate && onOpenComment ? (
              <>
                <SmallIconButton label={translate("workbench.helpful")} onClick={() => void handleRate(m.turnId as string, "positive")}>
                  <ThumbsUp className="size-3.5" />
                </SmallIconButton>
                <SmallIconButton label={translate("workbench.needs_improvement")} onClick={() => handleOpenComment(m.turnId as string)}>
                  <ThumbsDown className="size-3.5" />
                </SmallIconButton>
              </>
            ) : null}
            <SmallIconButton label={translate("workbench.copy_answer")} onClick={() => onCopy(m.content)}>
              <Copy className="size-3.5" />
            </SmallIconButton>
            {onRegenerate ? (
              <SmallIconButton
                label={m.error ? translate("workbench.retry") : translate("workbench.regenerate")}
                onClick={() => handleRegenerate(m.turnId as string)}
              >
                <RotateCcw className="size-3.5" />
              </SmallIconButton>
            ) : null}
          </>
        ) : null}
      </div>

      {commentTurn && commentTurn === m.turnId ? (
        <div className="mt-1 flex max-w-md flex-wrap gap-1">
          <select
            value={attribution}
            onChange={(e) => setAttribution(e.target.value)}
            aria-label={translate("workbench.attribution_label")}
            className="h-9 rounded border bg-background px-2 text-sm"
          >
            <option value="">{translate("workbench.attribution_placeholder")}</option>
            {["knowledge", "retrieval", "template", "model", "routing", "tool"].map((category) => (
              <option key={category} value={category}>
                {translate(`knowledgeOps.attribution_${category}`)}
              </option>
            ))}
          </select>
          <Input
            ref={commentInputRef}
            value={commentText}
            onChange={(e) => handleCommentTextChange(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && attribution) void handleRate(m.turnId as string, "negative", commentText, attribution);
            }}
            placeholder={translate("workbench.comment_placeholder")}
            aria-label={translate("workbench.feedback_label")}
          />
          <Button size="sm" variant="outline" disabled={!attribution} onClick={() => void handleRate(m.turnId as string, "negative", commentText, attribution)}>
            {translate("workbench.feedback_submit")}
          </Button>
          <Button size="sm" variant="ghost" onClick={handleCancelComment}>
            {translate("confirm.cancel")}
          </Button>
          {onCreateKnowledgeTask ? (
            <Button size="sm" variant="outline" onClick={() => onCreateKnowledgeTask(m.turnId as string)}>
              {translate("workbench.create_knowledge_task")}
            </Button>
          ) : null}
        </div>
      ) : null}

      <div className="mt-1 inline-block max-w-full rounded bg-muted/60 px-3 py-2 text-left">
        {m.role === "assistant" && m.content ? (
          <AnswerMarkdown content={m.content} citations={m.citations} onOpenCitation={handleOpenCitation} />
        ) : m.role === "system" ? (
          <span className="text-xs text-muted-foreground">{m.content}</span>
        ) : m.error ? (
          <div className="space-y-2" role="alert">
            <div className="text-destructive">
              {m.errorCode ? <Badge variant="outline">{m.errorCode}</Badge> : null} {m.error}
            </div>
            {onRegenerate ? (
              <Button size="sm" variant="outline" onClick={() => handleRegenerate(m.turnId as string)}>
                <RotateCcw className="size-4" />
                {translate("workbench.retry")}
              </Button>
            ) : null}
          </div>
        ) : m.role === "assistant" && isLastStreaming ? (
          <span className="flex items-center gap-1 text-muted-foreground">
            <Loader2 className="size-3 animate-spin" /> {translate("workbench.generating")}
          </span>
        ) : isNoHit ? (
          <div className="space-y-2">
            <span className="text-muted-foreground">{translate("workbench.no_hit")}</span>
            {onCreateKnowledgeTask ? (
              <div>
                <Button size="sm" variant="outline" onClick={() => onCreateKnowledgeTask(m.turnId as string)}>
                  {translate("workbench.create_knowledge_task")}
                </Button>
              </div>
            ) : null}
          </div>
        ) : (
          <span className="whitespace-pre-wrap">{m.content}</span>
        )}
        {m.role === "assistant" && m.turnId && m.feedback ? (
          <div className="mt-1 text-xs text-muted-foreground">
            {m.feedback === "positive" ? translate("workbench.marked_positive") : translate("workbench.marked_negative")}
          </div>
        ) : null}
        {m.role === "assistant" && m.kind === "agent" ? (
          <AgentExecutionTimeline message={m} isStreaming={isLastStreaming} />
        ) : null}
        <ConversationArtifacts artifacts={m.artifacts} />
        {m.role === "assistant" && (m.createdAt || m.usage || m.model) ? (
          <div className="mt-2 flex flex-wrap items-center gap-1 text-[11px] text-muted-foreground">
            {m.model ? <Badge variant="outline">{m.model}</Badge> : null}
            {m.createdAt ? <time>{new Date(m.createdAt).toLocaleString()}</time> : null}
            {m.usage?.totalTokens ? <span>{m.usage.totalTokens} tokens</span> : null}
            {m.status && m.status !== "completed" ? <Badge variant="secondary">{m.status}</Badge> : null}
            {m.errorCode ? <Badge variant="secondary">{m.errorCode}</Badge> : null}
          </div>
        ) : null}
      </div>

      {m.role === "user" && m.files && m.files.length > 0 ? (
        <div className="mt-1 flex flex-wrap justify-end gap-1">
          {m.files.map((f, fi) => (
            <span key={fi} className="inline-flex max-w-[14rem] items-center gap-1 truncate rounded border bg-muted/60 px-2 py-0.5 text-xs text-muted-foreground">
              <FileText className="size-3 shrink-0" />
              <span className="truncate">{String((f as { name?: string }).name ?? "附件")}</span>
            </span>
          ))}
        </div>
      ) : null}

      {m.role === "assistant" && m.citations && m.citations.length > 0 ? (
        <div className="space-y-1">
          <InlineCitations content={m.content} citations={m.citations} onOpen={handleOpenCitation} onOpenDocument={handleOpenDocument} />
          {imageCitations.length > 0 ? (
            <div className="mt-1 flex flex-wrap gap-1.5">
              {imageCitations.map((c, ci) => (
                <button
                  key={ci}
                  type="button"
                  onClick={() => handleOpenCitation(c)}
                  title={translate("workbench.citation_view")}
                  className="overflow-hidden rounded border"
                >
                  <CitationImage imageId={c.imageId!} alt={c.name} className="h-24 w-28 object-cover" />
                </button>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function SmallIconButton({
  label,
  onClick,
  children,
}: {
  label: string;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <Button size="icon" variant="ghost" className="h-6 w-6" onClick={onClick} title={label} aria-label={label}>
      {children}
    </Button>
  );
}
