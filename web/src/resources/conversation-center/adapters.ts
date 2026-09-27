import { api, ApiError, unwrap } from "../../lib/api";
import { newId } from "../workbench/workbench-types";
import {
  citationsFromReference,
  extractCitations,
  type Citation,
  type Message,
} from "../workbench/workbench-types";
import {
  CONVERSATION_EVENT_SCHEMA_VERSION,
  CONVERSATION_MESSAGE_PAGE_LIMIT,
  type ConversationAdapter,
  type ConversationContext,
  type ConversationContextPage,
  type ConversationError,
  type ConversationEvent,
  type ConversationTargetPage,
  type ConversationTargetRef,
  type ConversationStream,
  type RunFinalStatus,
} from "./types";

interface PageEnvelope<T> {
  items: T[];
  total?: number;
  page?: number;
  page_size?: number;
}

const EMPTY_CAPABILITIES = {
  streaming: true,
  attachments: false,
  feedback: false,
  artifacts: false,
  toolCalls: false,
  export: true,
  stopGeneration: true,
  retry: true,
  remoteCancel: false,
  serverSessions: false,
  contextModel: "run" as const,
  persistence: "ephemeral_run" as const,
};

function capabilities(overrides: Partial<typeof EMPTY_CAPABILITIES> = {}) {
  return {
    streaming: true,
    attachments: false,
    feedback: false,
    artifacts: false,
    toolCalls: false,
    export: true,
    stopGeneration: true,
    retry: true,
    remoteCancel: false,
    serverSessions: false,
    contextModel: "session" as const,
    persistence: "server_session" as const,
    ...overrides,
  };
}

function conversationError(
  errorCode: string,
  displayMessage: string,
  options: { httpStatus?: number; traceId?: string } = {},
): ConversationError {
  const { httpStatus } = options;
  const retriable = httpStatus === undefined || httpStatus === 0 || httpStatus === 408 || httpStatus === 425 || httpStatus === 429 || httpStatus >= 500;
  return { errorCode, displayMessage, httpStatus, retriable, traceId: options.traceId };
}

function page<T>(envelope: PageEnvelope<T>, limit: number, pageValue?: number): {
  items: T[];
  nextCursor?: string;
  total?: number;
} {
  const items = envelope.items ?? [];
  const hasMore = items.length >= limit;
  return {
    items,
    nextCursor: hasMore && pageValue ? String(pageValue + 1) : undefined,
    total: envelope.total,
  };
}

function targetFromRecord(kind: ConversationTargetRef["kind"], record: Record<string, unknown>): ConversationTargetRef {
  const knowledgeScope = Array.isArray(record.kb_names)
    ? (record.kb_names as unknown[]).map((name) => String(name)).filter(Boolean)
    : undefined;
  const model = record.llm_id ? String(record.llm_id) : undefined;
  return {
    id: String(record.id ?? record.ID ?? ""),
    kind,
    name: String(record.name ?? record.title ?? record.Name ?? "未命名"),
    canvasCategory: String(record.canvas_category ?? record.canvasCategory ?? record.CanvasCategory ?? ""),
    description: record.description ? String(record.description) : undefined,
    status: record.status ? String(record.status) : undefined,
    ownerId: record.owner_id ? String(record.owner_id) : record.OwnerID ? String(record.OwnerID) : undefined,
    ...(knowledgeScope?.length ? { knowledgeScope } : {}),
    ...(model ? { model } : {}),
    updatedAt: typeof record.update_time === "number" ? record.update_time : typeof record.updated_at === "number" ? record.updated_at : undefined,
  };
}

function contextFromRecord(
  kind: ConversationContext["kind"],
  record: Record<string, unknown>,
  targetId: string,
): ConversationContext {
  const title = String(record.name ?? record.title ?? "").trim();
  const updatedAt = typeof record.update_time === "number"
    ? record.update_time
    : typeof record.create_time === "number" ? record.create_time : undefined;
  if (kind === "chat") {
    return { id: String(record.id ?? ""), targetId, kind: "chat", type: "session", ...(title ? { title } : {}), ...(updatedAt ? { updatedAt } : {}) };
  }
  return { id: String(record.id ?? ""), targetId, kind: "agent", type: "session", ...(title ? { title } : {}), ...(updatedAt ? { updatedAt } : {}) };
}

function messageCitations(citations: Message["citations"]): Citation[] {
  const items = Array.isArray(citations) ? citations : [];
  if (items.some((item) => item && typeof item === "object" && ("datasetId" in item || "docId" in item))) {
    return items as Citation[];
  }
  return citationsFromReference({ chunks: items } as unknown as Record<string, unknown>);
}

function messageFromRecord(record: Message, sessionId: string, index: number): Message {
  return {
    ...record,
    id: record.id ?? `${sessionId}:${index}`,
    kind: record.kind ?? "chat",
    citations: messageCitations(record.citations),
    status: record.status ?? "completed",
  };
}

function pagedMessages(messages: Message[] | undefined, contextId: string): { items: Message[]; cursor?: string } {
  const all = messages ?? [];
  if (all.length <= CONVERSATION_MESSAGE_PAGE_LIMIT) {
    return { items: all.map((message, index) => messageFromRecord(message, contextId, index)) };
  }
  const cursor = all.length - CONVERSATION_MESSAGE_PAGE_LIMIT;
  return {
    items: all.slice(cursor).map((message, index) => messageFromRecord(message, contextId, cursor + index)),
    cursor: String(cursor),
  };
}

function citationsFromObject(obj: unknown): Citation[] {
  const source = (obj ?? {}) as Record<string, unknown>;
  const direct = extractCitations(source);
  if (direct.length) return direct;
  const raw = source.reference;
  if (Array.isArray(raw)) return raw.flatMap((entry) => citationsFromReference(entry as Record<string, unknown>));
  if (raw && typeof raw === "object") return citationsFromReference(raw as Record<string, unknown>);
  return [];
}

async function requestPage<T>(url: string, signal?: AbortSignal): Promise<PageEnvelope<T>> {
  return unwrap<PageEnvelope<T>>(api.get<import("../../lib/api").ApiEnvelope<PageEnvelope<T>>>(url, { signal }));
}

interface StreamState {
  eventSequence: number;
  assistantId: string;
  controller: AbortController;
  streamId: string;
  traceId?: string;
}

function eventBase(state: StreamState, input: { target: { id: string; kind: ConversationTargetRef["kind"] }, context: ConversationContext | null, conversationKey: string, requestId: string }, type: ConversationEvent["type"]) {
  state.eventSequence += 1;
  return {
    type,
    eventId: newId(),
    sequence: state.eventSequence,
    timestamp: new Date().toISOString(),
    schemaVersion: CONVERSATION_EVENT_SCHEMA_VERSION,
    requestId: input.requestId,
    streamId: state.streamId,
    traceId: state.traceId,
    conversationKey: input.conversationKey,
    target: input.target,
    context: input.context,
  };
}

async function* readSse(body: ReadableStream<Uint8Array>): AsyncGenerator<Record<string, unknown> | null> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";
    for (const rawLine of lines) {
      const line = rawLine.trim();
      if (!line.startsWith("data:")) continue;
      const payload = line.slice(5).trim();
      if (!payload) continue;
      if (payload === "[DONE]") {
        yield null;
        continue;
      }
      try {
        yield JSON.parse(payload) as Record<string, unknown>;
      } catch {
        console.debug("conversation-center: malformed SSE payload", payload);
      }
    }
  }
}

async function* consumeStream(
  response: Response,
  body: ReadableStream<Uint8Array>,
  state: StreamState,
  input: { target: { id: string; kind: ConversationTargetRef["kind"] }, context: ConversationContext | null, conversationKey: string, requestId: string },
): AsyncGenerator<ConversationEvent> {
  state.traceId = response.headers.get("X-Request-Id") ?? undefined;
  const frames = readSse(body);
  let messageStarted = false;
  let context = input.context;
  let failed = false;
  let pendingDelta = "";
  let pendingCitations: Citation[] = [];
  let thinking = false;

  function* startIfBound(): Generator<ConversationEvent> {
    if (messageStarted || !context) return;
    const started = eventBase(state, input, "message.started");
    yield { ...started, type: "message.started", context, messageId: state.assistantId, role: "assistant" };
    messageStarted = true;
    if (pendingDelta) {
      const base = eventBase(state, input, "message.delta");
      yield { ...base, type: "message.delta", context, messageId: state.assistantId, delta: pendingDelta };
    }
    for (const citation of pendingCitations) {
      const base = eventBase(state, input, "citation.added");
      yield { ...base, type: "citation.added", context, messageId: state.assistantId, citation };
    }
    pendingDelta = "";
    pendingCitations = [];
  }

  try {
    for (;;) {
      const frame = await frames.next();
      if (frame.done) break;
      const obj = frame.value;
      if (!obj) {
        break;
      }
      if (obj.error) {
        failed = true;
        const message = typeof obj.error === "string" ? obj.error : String((obj.error as Record<string, unknown>).message ?? "Completion failed");
        yield { ...eventBase(state, input, "error.occurred"), type: "error.occurred", context, messageId: messageStarted ? state.assistantId : undefined, error: conversationError("model_failure", message, { traceId: state.traceId }) };
        continue;
      }
      const inner = obj.data && typeof obj.data === "object" ? obj.data as Record<string, unknown> : undefined;
      let delta = "";
      let citations: Citation[] = [];
      if (inner?.start_to_think === true || obj.start_to_think === true) thinking = true;
      if (inner?.end_to_think === true || obj.end_to_think === true) thinking = false;
      const choices = obj.choices as Array<Record<string, unknown>> | undefined;
      if (input.target.kind === "agent") {
        const choice = choices?.[0];
        const object = choice ?? obj;
        const deltaPart = (object?.delta as Record<string, unknown> | undefined) ?? (object?.message as Record<string, unknown> | undefined);
        delta = typeof deltaPart?.content === "string" ? deltaPart.content : "";
        citations = citationsFromObject(deltaPart);
      } else {
        const chatDelta = choices?.[0]?.delta as Record<string, unknown> | undefined;
        delta = String(inner?.answer ?? obj.answer ?? chatDelta?.content ?? "");
        citations = citationsFromObject(inner ?? obj);
      }
      const sessionCandidate = inner?.session_id ?? obj.session_id;
      if (!context && typeof sessionCandidate === "string" && sessionCandidate) {
        context = { id: sessionCandidate, targetId: input.target.id, kind: input.target.kind === "agent" ? "agent" : "chat", type: "session" };
      }
      if (!thinking) pendingDelta += delta;
      pendingCitations.push(...citations);
      if (context) {
        if (!messageStarted) {
          yield* startIfBound();
        } else {
          if (pendingDelta) {
            const base = eventBase(state, input, "message.delta");
            yield { ...base, type: "message.delta", context, messageId: state.assistantId, delta: pendingDelta };
            pendingDelta = "";
          }
          for (const citation of pendingCitations) {
            const base = eventBase(state, input, "citation.added");
            yield { ...base, type: "citation.added", context, messageId: state.assistantId, citation };
          }
          pendingCitations = [];
        }
      }
      if (inner?.usage || obj.usage) {
        const rawUsage = (inner?.usage ?? obj.usage) as Record<string, unknown>;
        const usage = {
          inputTokens: Number(rawUsage.prompt_tokens ?? 0),
          outputTokens: Number(rawUsage.completion_tokens ?? 0),
          totalTokens: Number(rawUsage.total_tokens ?? 0),
        };
        yield { ...eventBase(state, input, "usage.updated"), type: "usage.updated", context, messageId: messageStarted ? state.assistantId : undefined, usage };
      }
    }
    yield* startIfBound();
    if (!messageStarted && !failed && (pendingDelta || context === null)) {
      const message = pendingDelta ? "Conversation session binding failed" : "Completion returned no content";
      yield { ...eventBase(state, input, "error.occurred"), type: "error.occurred", context, error: conversationError("model_failure", message, { traceId: state.traceId }) };
      failed = true;
    }
    if (messageStarted) yield { ...eventBase(state, input, "message.completed"), type: "message.completed", context: context!, messageId: state.assistantId };
    yield { ...eventBase(state, input, "stream.completed"), type: "stream.completed", context, finalStatus: failed ? "failed" : "completed" };
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") {
      yield { ...eventBase(state, input, "cancelled"), type: "cancelled", context, messageId: messageStarted ? state.assistantId : undefined, reason: "user" };
      yield { ...eventBase(state, input, "stream.completed"), type: "stream.completed", context, finalStatus: "cancelled" };
      return;
    }
    const apiError = error instanceof ApiError ? error : null;
    const message = apiError ? apiError.displayMessage : error instanceof Error ? error.message : String(error);
    yield { ...eventBase(state, input, "error.occurred"), type: "error.occurred", context, messageId: messageStarted ? state.assistantId : undefined, error: conversationError("model_failure", message, { traceId: state.traceId ?? apiError?.traceId, httpStatus: apiError?.status }) };
    yield { ...eventBase(state, input, "stream.completed"), type: "stream.completed", context, finalStatus: "failed" };
  }
}

function startStream(
  url: string,
  payload: Record<string, unknown>,
  target: { id: string; kind: ConversationTargetRef["kind"] },
  context: ConversationContext | null,
  conversationKey: string,
  requestId: string,
  streamId: string,
): ConversationStream {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 120000);
  const state: StreamState = { eventSequence: 0, assistantId: `assistant-${streamId}`, controller, streamId };
  const request = async () => fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Request-Id": requestId },
    body: JSON.stringify(payload),
    signal: controller.signal,
  });
  const input = { target, context, conversationKey, requestId };
  const consume = async function* () {
    try {
      const response = await request();
      clearTimeout(timer);
      state.traceId = response.headers.get("X-Request-Id") ?? undefined;
      if (!response.ok || !response.body) {
        const raw = await response.text().catch(() => "");
        throw new ApiError(response.status, response.status, raw || `Completion request failed (${response.status})`, state.traceId);
      }
      yield* consumeStream(response, response.body, state, input);
    } catch (error) {
      clearTimeout(timer);
      if (error instanceof DOMException && error.name === "AbortError") {
        const cancelledEvent: ConversationEvent = { ...eventBase(state, input, "cancelled"), type: "cancelled", context, reason: "user" };
        const completedEvent: ConversationEvent = { ...eventBase(state, input, "stream.completed"), type: "stream.completed", context, finalStatus: "cancelled" };
        yield cancelledEvent;
        yield completedEvent;
        return;
      }
      const protocolError = conversationError(
        "model_failure",
        error instanceof ApiError ? error.displayMessage : error instanceof Error ? error.message : String(error),
        { httpStatus: error instanceof ApiError ? error.status : undefined, traceId: error instanceof ApiError ? error.traceId : undefined },
      );
      const errorEvent: ConversationEvent = { ...eventBase(state, input, "error.occurred"), type: "error.occurred", context, error: protocolError };
      const completedEvent: ConversationEvent = { ...eventBase(state, input, "stream.completed"), type: "stream.completed", context, finalStatus: "failed" };
      yield errorEvent;
      yield completedEvent;
    }
  };
  return { requestId, streamId, events: consume(), abort: () => controller.abort() };
}

export function createChatAdapter(): ConversationAdapter {
  return {
    kind: "chat",
    capabilities: () => capabilities({ attachments: true, feedback: true }),
    async getTarget(targetId, { signal } = {}) {
      const record = await unwrap<Record<string, unknown>>(api.get(`/chats/${encodeURIComponent(targetId)}/config`, { signal }));
      return targetFromRecord("chat", record);
    },
    async searchTargets(query, { limit, cursor, signal }) {
      const pageNumber = Number(cursor ?? 1);
      const data = await requestPage<Record<string, unknown>>(`/chats?page=${pageNumber}&page_size=${limit}&name=${encodeURIComponent(query)}`, signal);
      return { ...page(data, limit, pageNumber), items: data.items.map((item) => targetFromRecord("chat", item)) };
    },
    async listContexts(targetId, { limit, cursor, signal }) {
      const pageNumber = Number(cursor ?? 1);
      const data = await requestPage<Record<string, unknown>>(`/chats/${encodeURIComponent(targetId)}/sessions?page=${pageNumber}&page_size=${limit}`, signal);
      const rawPage = page(data, limit, pageNumber);
      return { ...rawPage, items: rawPage.items.map((item) => contextFromRecord("chat", item, targetId)) };
    },
    async loadContext(context) {
      const result = await unwrap<{ items: Message[]; next_cursor?: string }>(api.get(`/chats/${encodeURIComponent(context.targetId)}/sessions/${encodeURIComponent(context.id)}/messages?limit=${CONVERSATION_MESSAGE_PAGE_LIMIT}`));
      return { context, messages: result.items.map((message, index) => messageFromRecord(message, context.id, index)), nextCursor: result.next_cursor };
    },
    async loadMoreMessages(context, cursor, options) {
      if (cursor === undefined) return { items: [] };
      const limit = options?.limit ?? CONVERSATION_MESSAGE_PAGE_LIMIT;
      const result = await unwrap<{ items: Message[]; next_cursor?: string }>(api.get(`/chats/${encodeURIComponent(context.targetId)}/sessions/${encodeURIComponent(context.id)}/messages?limit=${limit}&cursor=${encodeURIComponent(cursor)}`));
      return { items: result.items.map((message, index) => messageFromRecord(message, context.id, index)), nextCursor: result.next_cursor };
    },
    async createSession(targetId) {
      const record = await unwrap<Record<string, unknown>>(api.post(`/chats/${encodeURIComponent(targetId)}/sessions`, {}));
      return contextFromRecord("chat", record, targetId);
    },
    start({ target, context, text, attachments, requestId, conversationKey: key }) {
      const payload: Record<string, unknown> = {
        chat_id: target.id,
      messages: [{ role: "user", content: text, ...(attachments?.length ? { files: attachments.map((item) => item.meta) } : {}) }],
        stream: true,
      };
      if (context?.type === "session") payload.session_id = context.id;
      return startStream("/api/v1/chat/completions", payload, target, context ?? null, key, requestId, `stream-${requestId}`);
    },
  };
}

export function createSearchAdapter(): ConversationAdapter {
  return {
    kind: "search",
    capabilities: () => capabilities({ contextModel: "run", persistence: "ephemeral_run" }),
    async getTarget(targetId, { signal } = {}) {
      const record = await unwrap<Record<string, unknown>>(api.get(`/search-apps/${encodeURIComponent(targetId)}`, { signal }));
      return targetFromRecord("search", record);
    },
    async searchTargets(query, { limit, cursor, signal }) {
      const pageNumber = Number(cursor ?? 1);
      const data = await requestPage<Record<string, unknown>>(`/search-apps?page=${pageNumber}&page_size=${limit}&name=${encodeURIComponent(query)}`, signal);
      return { ...page(data, limit, pageNumber), items: data.items.map((item) => targetFromRecord("search", item)) };
    },
    async listContexts() {
      return { items: [], total: 0 };
    },
    async loadContext(context) {
      return { context, messages: [] };
    },
    async loadMoreMessages() {
      return { items: [] };
    },
    start({ target, text, context, requestId, conversationKey: key }) {
      return startStream(`/api/v1/search-apps/${encodeURIComponent(target.id)}/completion/stream`, { question: text }, target, context ?? null, key, requestId, `stream-${requestId}`);
    },
  };
}

export function createAgentAdapter(): ConversationAdapter {
  return {
    kind: "agent",
    capabilities: () => capabilities({ attachments: true, serverSessions: true }),
    async getTarget(targetId, { signal } = {}) {
      const record = await unwrap<Record<string, unknown>>(api.get(`/agents/${encodeURIComponent(targetId)}/detail`, { signal }));
      return targetFromRecord("agent", record);
    },
    async searchTargets(query, { limit, cursor, signal }) {
      const pageNumber = Number(cursor ?? 1);
      const data = await requestPage<Record<string, unknown>>(`/agents?page=${pageNumber}&page_size=${limit}&title=${encodeURIComponent(query)}`, signal);
      return {
        ...page(data, limit, pageNumber),
        items: data.items.map((item) => targetFromRecord("agent", item)).filter((item) => item.canvasCategory === "agent_canvas"),
      };
    },
    async listContexts(targetId, { limit, cursor, signal }) {
      const pageNumber = Number(cursor ?? 1);
      const data = await requestPage<Record<string, unknown>>(`/agents/${encodeURIComponent(targetId)}/sessions?page=${pageNumber}&page_size=${limit}`, signal);
      const rawPage = page(data, limit, pageNumber);
      return { ...rawPage, items: rawPage.items.map((item) => contextFromRecord("agent", item, targetId)) };
    },
    async loadContext(context) {
      const result = await unwrap<{ items: Message[]; next_cursor?: string }>(api.get(`/agents/${encodeURIComponent(context.targetId)}/sessions/${encodeURIComponent(context.id)}/messages?limit=${CONVERSATION_MESSAGE_PAGE_LIMIT}`));
      return { context, messages: result.items.map((message, index) => messageFromRecord(message, context.id, index)), nextCursor: result.next_cursor };
    },
    async loadMoreMessages(context, cursor, options) {
      if (cursor === undefined) return { items: [] };
      const limit = options?.limit ?? CONVERSATION_MESSAGE_PAGE_LIMIT;
      const result = await unwrap<{ items: Message[]; next_cursor?: string }>(api.get(`/agents/${encodeURIComponent(context.targetId)}/sessions/${encodeURIComponent(context.id)}/messages?limit=${limit}&cursor=${encodeURIComponent(cursor)}`));
      return { items: result.items.map((message, index) => messageFromRecord(message, context.id, index)), nextCursor: result.next_cursor };
    },
    start({ target, context, text, attachments, requestId, conversationKey: key }) {
      if (!context || context.type !== "session") throw new ConversationBootstrapError("Agent requires an existing session");
      const files = (attachments ?? []).map((item) => item.meta).filter(Boolean);
      return startStream(`/api/v1/agents/${encodeURIComponent(target.id)}/chat/completions/stream`, {
        session_id: context.id,
        messages: [{ role: "user", content: text }],
        ...(files.length ? { files } : {}),
      }, target, context, key, requestId, `stream-${requestId}`);
    },
  };
}

class ConversationBootstrapError extends Error {
  readonly errorCode = "missing_agent_context";
  constructor(message: string) {
    super(message);
    this.name = "ConversationBootstrapError";
  }
}
