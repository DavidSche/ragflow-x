import { newId, type AttachmentDraft } from "../workbench/workbench-types";
import {
  CONVERSATION_EVENT_SCHEMA_VERSION,
  ConversationContext,
  ConversationEvent,
  ConversationProtocolError,
  ConversationTargetRef,
  RecentTargetEntry,
} from "./types";

export function conversationKey(
  scopeKey: string,
  kind: ConversationContext["kind"],
  targetId: string,
  contextId?: string,
): string {
  return JSON.stringify([scopeKey, kind, targetId, contextId ?? "new"]);
}

export function assertConversationContext(context: ConversationContext): ConversationContext {
  const valid = (context.kind === "search") === (context.type === "run");
  if (!valid || !context.id || !context.targetId) throw new ConversationProtocolError("invalid_context", "Invalid conversation context");
  return context;
}

export function contextTitle(context: ConversationContext): string | undefined {
  return context.type === "session" ? context.title : undefined;
}

export function isConversationTarget(target: ConversationTargetRef): boolean {
  return target.kind !== "agent" || target.canvasCategory === "agent_canvas";
}

export type ConversationTimeGroup = "today" | "yesterday" | "earlier";

export function conversationTimeGroup(timestamp: number | undefined, now: number = Date.now()): ConversationTimeGroup {
  if (!timestamp) return "earlier";
  const startOfDay = (value: number) => {
    const date = new Date(value);
    date.setHours(0, 0, 0, 0);
    return date.getTime();
  };
  const difference = startOfDay(now) - startOfDay(timestamp);
  if (difference <= 0) return "today";
  if (difference <= 86_400_000) return "yesterday";
  return "earlier";
}

export function groupContextsByTime(
  contexts: ConversationContext[],
  now: number = Date.now(),
): Record<ConversationTimeGroup, ConversationContext[]> {
  const groups: Record<ConversationTimeGroup, ConversationContext[]> = {
    today: [],
    yesterday: [],
    earlier: [],
  };
  for (const context of contexts) {
    groups[conversationTimeGroup(context.updatedAt, now)].push(context);
  }
  return groups;
}

export interface InputTrigger {
  type: "command" | "mention";
  query: string;
  start: number;
  end: number;
}

export function parseInputTrigger(text: string, caret: number): InputTrigger | null {
  const position = Math.max(0, Math.min(caret, text.length));
  let start = position;
  while (start > 0 && !/\s/.test(text[start - 1] ?? "")) start -= 1;
  const token = text.slice(start, position);
  const trigger = token[0];
  const validStart = start === 0 || /\s/.test(text[start - 1] ?? "");
  if (!validStart || (trigger !== "/" && trigger !== "@")) return null;
  return {
    type: trigger === "/" ? "command" : "mention",
    query: token.slice(1),
    start,
    end: position,
  };
}

export interface SlashCommandOption {
  id: string;
  label: string;
  hint?: string;
}

export function slashCommandOptions(): SlashCommandOption[] {
  return [
    { id: "chat", label: "/chat", hint: "conversationCenter.kind_chat" },
    { id: "search", label: "/search", hint: "conversationCenter.kind_search" },
    { id: "agent", label: "/agent", hint: "conversationCenter.kind_agent" },
    { id: "new", label: "/new", hint: "conversationCenter.new_context" },
    { id: "help", label: "/help", hint: "conversationCenter.help" },
  ];
}

export function matchesCommand(option: SlashCommandOption, query: string): boolean {
  const q = query.trim().toLowerCase();
  return option.id.toLowerCase().startsWith(q) || option.label.toLowerCase().startsWith(q.startsWith("/") ? q : `/${q}`);
}

export function matchesTarget(target: ConversationTargetRef, query: string): boolean {
  return target.name.toLowerCase().includes(query.trim().toLowerCase());
}

export function replaceInputToken(text: string, range: { start: number; end: number }, replacement = ""): string {
  return text.slice(0, range.start) + replacement + text.slice(range.end);
}

export function stripLeadingCommand(text: string): string {
  const token = text.trimStart().split(/\s+/, 1)[0] ?? "";
  if (!token.startsWith("/") || !slashCommandOptions().some((o) => o.label === token)) return text;
  return text.replace(token, "").trimStart();
}

export class ConversationEventSink {
  private expectedSequence = 0;
  private readonly eventIds = new Set<string>();

  push(event: ConversationEvent, activeRun: { requestId: string; streamId: string; conversationKey: string }): ConversationEvent {
    if (event.schemaVersion !== CONVERSATION_EVENT_SCHEMA_VERSION) {
      throw new ConversationProtocolError("schema_version", "Unsupported conversation event schema");
    }
    const active = event.requestId === activeRun.requestId
      && event.streamId === activeRun.streamId
      && event.conversationKey === activeRun.conversationKey
      && (event.context === null || (event.context.kind === event.target.kind
        && event.context.targetId === event.target.id
        && !!event.context.id));
    if (!active) throw new ConversationProtocolError("inactive_event", "Inactive conversation event");
    if (this.eventIds.has(event.eventId)) throw new ConversationProtocolError("duplicate_event", "Duplicate conversation event");
    if (event.sequence !== this.expectedSequence + 1) {
      throw new ConversationProtocolError("sequence_gap", "Conversation event sequence gap");
    }
    if (["message.started", "message.delta", "message.completed", "citation.added"].includes(event.type) && !event.context) {
      throw new ConversationProtocolError("missing_context", "Message event has no bound context");
    }
    this.expectedSequence = event.sequence;
    this.eventIds.add(event.eventId);
    return event;
  }
}

export interface DraftState {
  text: string;
  attachments: AttachmentDraft[];
  selectionStart: number;
  selectionEnd: number;
}

export class DraftStore {
  constructor(private readonly storage: Pick<Storage, "getItem" | "setItem" | "removeItem">) {}

  get(key: string): DraftState | undefined {
    const raw = this.storage.getItem(key);
    if (!raw) return undefined;
    try {
    const parsed = JSON.parse(raw) as Partial<DraftState>;
    if (typeof parsed.text !== "string") return undefined;
    if (!Array.isArray(parsed.attachments)) return undefined;
    const attachments = parsed.attachments.filter((attachment) => {
      if (!attachment || typeof attachment !== "object") return false;
      const value = attachment as Partial<AttachmentDraft>;
      return typeof value.id === "string"
        && typeof value.name === "string"
        && typeof value.size === "number"
        && typeof value.mime === "string"
        && (value.status === "staged" || value.status === "uploading" || value.status === "done" || value.status === "error")
        && typeof value.progress === "number";
    });
    return {
      text: parsed.text,
      attachments,
      selectionStart: parsed.selectionStart ?? parsed.text.length,
      selectionEnd: parsed.selectionEnd ?? parsed.text.length,
    };
    } catch {
      this.storage.removeItem(key);
      return undefined;
    }
  }

  set(key: string, draft: DraftState): void {
    this.storage.setItem(key, JSON.stringify({
      ...draft,
      attachments: draft.attachments.filter((attachment) => attachment.status !== "staged"),
    }));
  }

  delete(key: string): void {
    this.storage.removeItem(key);
  }
}

export class RecentTargetStore {
  constructor(
    private readonly storage: Pick<Storage, "getItem" | "setItem">,
    private readonly scopeKey: string,
  ) {}

  list(): RecentTargetEntry[] {
    try {
      const parsed = JSON.parse(this.storage.getItem(`conversation-center:recent:${this.scopeKey}`) ?? "[]") as RecentTargetEntry[];
      return parsed
        .filter((entry) => entry.target && entry.source === "local" && !!entry.target.id && !!entry.target.name)
        .map((entry) => ({ ...entry, displayName: entry.displayName ?? entry.target.name }));
    } catch {
      return [];
    }
  }

  touch(target: ConversationTargetRef, now = Date.now()): RecentTargetEntry[] {
    const next = [
      { target, displayName: target.name, lastUsedAt: now, source: "local" as const },
      ...this.list().filter((entry) => entry.target.kind !== target.kind || entry.target.id !== target.id),
    ].slice(0, 12);
    this.storage.setItem(`conversation-center:recent:${this.scopeKey}`, JSON.stringify(next));
    return next;
  }

  invalidate(kind?: ConversationTargetRef["kind"], targetId?: string): RecentTargetEntry[] {
    const next = this.list().filter((entry) =>
      (kind && entry.target.kind !== kind) || (targetId && entry.target.id !== targetId));
    this.storage.setItem(`conversation-center:recent:${this.scopeKey}`, JSON.stringify(next));
    return next;
  }
}

export function newConversationRunId(): string {
  return `run-${newId()}`;
}
