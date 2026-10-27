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

export function createOpaqueScope(value: string): string {
  let hash = 0x811c9dc5;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 0x01000193);
  }
  return `op-${(hash >>> 0).toString(36)}`;
}

function createCspValue(prefix: string): string {
  const id = globalThis.crypto?.randomUUID?.();
  if (!id) throw new Error("CSPRNG unavailable");
  return `${prefix}-${id}`;
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
  composerDraftId?: string;
  createdAt?: string;
  text: string;
  selectionStart: number;
  selectionEnd: number;
  source: string;
}

export class DraftStore {
  private static readonly TTL_MS = 60 * 60 * 1000;

  constructor(
    private readonly storage: Pick<Storage, "getItem" | "setItem" | "removeItem"> & Partial<Pick<Storage, "key" | "length">>,
    private readonly opaqueScope: string,
  ) {
    this.cleanupLegacy();
  }

  private cleanupLegacy() {
    const { length, key } = this.storage;
    if (typeof length !== "number" || typeof key !== "function") return;
    for (let index = length - 1; index >= 0; index -= 1) {
      const storageKey = key.call(this.storage, index);
      if (!storageKey) continue;
      if (storageKey.startsWith("rgx:pending:") || storageKey.startsWith("conversation-center:draft:")) {
        this.storage.removeItem(storageKey);
      }
    }
  }

  private opaqueKey(key: string): string {
    let hash = 0x811c9dc5;
    for (let index = 0; index < key.length; index += 1) {
      hash ^= key.charCodeAt(index);
      hash = Math.imul(hash, 0x01000193);
    }
    return (hash >>> 0).toString(36);
  }

  private indexKey(key: string): string {
    return `rgx:composer-draft-index:${this.opaqueScope}:${this.opaqueKey(key)}`;
  }

  get(key: string): DraftState | undefined {
    try {
    const composerDraftId = this.storage.getItem(this.indexKey(key));
    if (!composerDraftId) return undefined;
    const raw = this.storage.getItem(`rgx:composer-draft:${this.opaqueScope}:${composerDraftId}`);
    if (!raw) {
      this.storage.removeItem(this.indexKey(key));
      return undefined;
    }
    const parsed = JSON.parse(raw) as Partial<DraftState & { schemaVersion?: number; expiresAt?: string }>;
    if (parsed.schemaVersion !== 3 || !parsed.expiresAt || Date.parse(parsed.expiresAt) <= Date.now()) {
      this.storage.removeItem(this.indexKey(key));
      this.storage.removeItem(`rgx:composer-draft:${this.opaqueScope}:${composerDraftId}`);
      return undefined;
    }
    if (typeof parsed.text !== "string") return undefined;
    return {
      composerDraftId,
      text: parsed.text,
      selectionStart: parsed.selectionStart ?? parsed.text.length,
      selectionEnd: parsed.selectionEnd ?? parsed.text.length,
      source: key,
    };
    } catch {
      this.storage.removeItem(key);
      return undefined;
    }
  }

  set(key: string, draft: DraftState): void {
    const composerDraftId = draft.composerDraftId ?? createCspValue("draft");
    const now = Date.now();
    this.storage.setItem(`rgx:composer-draft:${this.opaqueScope}:${composerDraftId}`, JSON.stringify({
      schemaVersion: 3,
      composerDraftId,
      text: draft.text,
      selectionStart: draft.selectionStart,
      selectionEnd: draft.selectionEnd,
      source: draft.source,
      createdAt: draft.createdAt ?? new Date(now).toISOString(),
      updatedAt: new Date(now).toISOString(),
      expiresAt: new Date(now + DraftStore.TTL_MS).toISOString(),
    }));
    this.storage.setItem(this.indexKey(key), composerDraftId);
  }

  delete(key: string): void {
    const composerDraftId = this.storage.getItem(this.indexKey(key));
    if (composerDraftId) {
      this.storage.removeItem(`rgx:composer-draft:${this.opaqueScope}:${composerDraftId}`);
    }
    this.storage.removeItem(this.indexKey(key));
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
      (kind ? entry.target.kind !== kind : false)
      || (targetId ? entry.target.id !== targetId : false));
    this.storage.setItem(`conversation-center:recent:${this.scopeKey}`, JSON.stringify(next));
    return next;
  }
}

export function newConversationRunId(): string {
  return createCspValue("run");
}
