import { api, ApiError, unwrap } from "../../lib/api";
import { newId, restoredTurnId } from "../workbench/workbench-types";
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
  type ConversationSnapshot,
  type ConversationStartInput,
  type ConversationTargetPage,
  type ConversationTargetRef,
  type ConversationStream,
  type RunFinalStatus,
} from "./types";
import {
  openReportEventPoller,
  rememberReportJob,
  rememberedReportJob,
  reportContextFromRecords,
  type CreateReportResult,
  type ExportArtifactRecord,
  type ReportDetailRecord,
  type ReportFollowUpView,
  type ReportJobSnapshot,
  type ReportTemplateView,
} from "./report/report-run";
import { IDEMPOTENCY_HEADER, isReportTerminalEvent, type ReportExportFormat } from "./report/report-contract";
import type {
  ReportRevisionSummary,
} from "./report/report-run";
import type {
  AssuranceMode,
  AssuranceRun,
  FindingStatus,
  ReportReview,
  ReviewFinding,
} from "./report/report-assurance";

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
  options: { httpStatus?: number; traceId?: string; failureKind?: "deterministic" | "unknown"; retryAuthorization?: "none" | "safe-retry" } = {},
): ConversationError {
  const { httpStatus } = options;
  const unknown = options.failureKind === "unknown"
    || httpStatus === undefined || httpStatus === 0 || httpStatus === 408 || httpStatus === 425 || httpStatus === 429 || httpStatus >= 500;
  const retryAuthorization = options.retryAuthorization ?? (unknown ? "none" : "none");
  return {
    errorCode,
    displayMessage,
    httpStatus,
    failureKind: unknown ? "unknown" : "deterministic",
    retryAuthorization,
    retryHint: retryAuthorization === "safe-retry",
    traceId: options.traceId,
  };
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
  const typedRecord = record as Message & { request_id?: string };
  return {
    ...record,
    id: restoredTurnId(typedRecord, record.id ?? `${sessionId}:${index}`),
    kind: record.kind ?? "chat",
    citations: messageCitations(record.citations),
    status: record.status ?? "completed",
    ...(typedRecord.request_id ? { turnId: `assistant-${typedRecord.request_id}` } : {}),
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

async function* readSse(
  body: ReadableStream<Uint8Array>,
  onTransportData?: () => void,
  onApplicationEvent?: (event: Record<string, unknown> | null) => void,
): AsyncGenerator<Record<string, unknown> | null> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { done, value } = await reader.read();
    onTransportData?.();
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
        const parsed = JSON.parse(payload) as Record<string, unknown>;
        onApplicationEvent?.(parsed);
        yield parsed;
      } catch {
        console.debug("conversation-center: malformed SSE payload");
      }
    }
  }
}

async function* consumeStream(
  response: Response,
  body: ReadableStream<Uint8Array>,
  state: StreamState,
  input: { target: { id: string; kind: ConversationTargetRef["kind"] }, context: ConversationContext | null, conversationKey: string, requestId: string },
  onApplicationEvent?: () => void,
): AsyncGenerator<ConversationEvent> {
  state.traceId = response.headers.get("X-Request-Id") ?? undefined;
  const frames = readSse(body, undefined, onApplicationEvent);
  let messageStarted = false;
  let context = input.context;
  let failed = false;
  let pendingDelta = "";
  let pendingCitations: Citation[] = [];
  let thinking = false;
  // Text already handed to the UI. RAGFlow repeats the whole answer on its
  // `final` frame, so without this the message doubles on screen.
  let emittedText = "";

  function* startIfBound(): Generator<ConversationEvent> {
    if (messageStarted || !context) return;
    const started = eventBase(state, input, "message.started");
    yield { ...started, type: "message.started", context, messageId: state.assistantId, role: "assistant" };
    messageStarted = true;
    if (pendingDelta) {
      const base = eventBase(state, input, "message.delta");
      yield { ...base, type: "message.delta", context, messageId: state.assistantId, delta: pendingDelta };
      emittedText += pendingDelta;
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
      if (inner?.final === true || obj.final === true) {
        const seen = emittedText + pendingDelta;
        delta = delta.startsWith(seen) ? delta.slice(seen.length) : "";
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
            emittedText += pendingDelta;
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
  let connectTimer: ReturnType<typeof setTimeout> | undefined;
  let transportTimer: ReturnType<typeof setTimeout> | undefined;
  let applicationTimer: ReturnType<typeof setTimeout> | undefined;
  let timedOut = false;
  let timeoutKind: "connect" | "transport" | "application" | undefined;

  const failTimeout = (kind: "connect" | "transport" | "application") => {
    timedOut = true;
    timeoutKind = kind;
    controller.abort(new DOMException(`${kind} timeout`, "TimeoutError"));
  };
  const resetTransport = () => {
    clearTimeout(transportTimer);
    transportTimer = setTimeout(() => failTimeout("transport"), 30_000);
  };
  const resetApplication = () => {
    clearTimeout(applicationTimer);
    applicationTimer = setTimeout(() => failTimeout("application"), 60_000);
  };
  const clearTimers = () => {
    clearTimeout(connectTimer);
    clearTimeout(transportTimer);
    clearTimeout(applicationTimer);
  };
  // Retrieval plus the first LLM token routinely takes longer than 15s on a
  // shared GPU cluster; a tight connect budget aborted perfectly healthy runs.
  connectTimer = setTimeout(() => failTimeout("connect"), 60_000);
  resetTransport();
  resetApplication();
  const state: StreamState = { eventSequence: 0, assistantId: `assistant-${requestId}`, controller, streamId };
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
      clearTimeout(connectTimer);
      resetTransport();
      state.traceId = response.headers.get("X-Request-Id") ?? undefined;
      if (!response.ok || !response.body) {
        const raw = await response.text().catch(() => "");
        throw new ApiError(response.status, response.status, raw || `Completion request failed (${response.status})`, state.traceId);
      }
      yield* consumeStream(response, response.body, state, input, resetApplication);
    } catch (error) {
      clearTimers();
      if (error instanceof DOMException && error.name === "AbortError") {
        const cancelledEvent: ConversationEvent = { ...eventBase(state, input, "cancelled"), type: "cancelled", context, reason: "user" };
        const completedEvent: ConversationEvent = { ...eventBase(state, input, "stream.completed"), type: "stream.completed", context, finalStatus: "cancelled" };
        yield cancelledEvent;
        yield completedEvent;
        return;
      }
      if (timedOut || (error instanceof DOMException && error.name === "TimeoutError")) {
        const errorEvent: ConversationEvent = {
          ...eventBase(state, input, "error.occurred"),
          type: "error.occurred",
          context,
          error: conversationError(
            "execution_unknown",
            timeoutKind === "connect"
              ? "Timed out before the assistant started answering"
              : "Conversation execution status unknown",
            { failureKind: "unknown" },
          ),
        };
        const completedEvent: ConversationEvent = { ...eventBase(state, input, "stream.completed"), type: "stream.completed", context, finalStatus: "failed" };
        yield errorEvent;
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
    start({ target, context, text, attachments, requestId, conversationKey: key, reportContext }) {
      const payload: Record<string, unknown> = {
        chat_id: target.id,
        messages: [{ role: "user", content: text, ...(attachments?.length ? { files: attachments.map((item) => item.meta) } : {}) }],
        stream: true,
      };
      if (context?.type === "session") payload.session_id = context.id;
      // A14: only the three keys travel. The server reads the report body itself, so
      // an attachment here is a *reference*, not a copy of the document.
      if (reportContext?.report_id) payload.report_context = reportContext;
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

// ---------------------------------------------------------------------------
// Report adapter (doc/288 T8, `kind: "report"`)
// ---------------------------------------------------------------------------
//
// A report run is not a chat turn: it POSTs an aggregate (202), then the UI
// follows the job through `/report-jobs/:id/events` (§8.3 SSE) until a terminal
// event, and `POST /reports/:id/cancel` records the stop intent (202). The
// adapter therefore exposes a small imperative surface on top of the
// `ConversationAdapter` contract; the workspace (`report/ReportWorkspace.tsx`)
// drives those, and `start()` implements the stream protocol for callers that
// consume the center's event pipeline.

const REPORT_CREATE_IDEMPOTENCY_PREFIX = "rgx-r";

export interface ReportExportSuccess {
  export_job: Record<string, unknown>;
  export_artifact: ExportArtifactRecord;
  canonical_hash: string;
  /** Server-side download decision for this fresh delivery (see ReportArtifactEntry). */
  downloadable: boolean;
}

export interface ReportAdapter extends ConversationAdapter {
  kind: "report";
  listTemplates(): Promise<ReportTemplateView[]>;
  /** Datasets the run may bind: the caller's workspace, never the platform-wide list. */
  listReportDatasets(signal?: AbortSignal): Promise<ReportDatasetOption[]>;
  loadDetail(reportId: string, options?: { signal?: AbortSignal }): Promise<ReportDetailRecord>;
  /**
   * A14: the chips a reader may click to start the next turn.
   *
   * Derived server-side from the stored revision, so two loads of the same revision
   * agree — which is what makes them cacheable and testable. `revision` 0 asks for
   * whatever the report currently shows.
   */
  loadFollowUps(reportId: string, options?: { revision?: number; sectionKey?: string; signal?: AbortSignal }): Promise<ReportFollowUpView>;
  createReport(body: {
    title: string;
    question: string;
    template_key: string;
    /** R18's retrieval depth (`standard` / `deep`), frozen on the revision. */
    research_depth?: string;
    datasets?: Array<{ dataset_id: string; engine_dataset_id?: string; role?: string; engine_id?: string }>;
    outline_confirmation?: string;
    /** The chat/target this run was started from (`origin_chat_id`). */
    origin_chat_id?: string;
    /** The session inside that chat (`origin_session_id`). */
    origin_session_id?: string;
  }): Promise<CreateReportResult>;
  cancelReport(reportId: string): Promise<unknown>;
  loadSnapshot(reportId: string, jobId: string, options?: { signal?: AbortSignal }): Promise<ReportJobSnapshot>;
  confirmOutline(reportId: string, revisionNo: number, sections: Array<{ section_key: string; title: string }>): Promise<unknown>;
  /**
   * `format` is the report delivery closed set (`REPORT_EXPORT_FORMATS`), not the
   * old three-name alias: the drawer and this panel must offer the same five, and a
   * wire name here keeps one vocabulary for the artifact row we render back.
   */
  createExport(
    reportId: string,
    revisionNo: number,
    format: ReportExportFormat,
    options?: { acknowledgeAssurance?: boolean },
  ): Promise<ReportExportSuccess>;
  artifactDownloadUrl(reportId: string, revisionNo: number, artifactId: string): string;

  /* ---- assurance surface (doc/301 §7.1) ---- */

  /**
   * Start one review run. The `Idempotency-Key` is minted here for the same reason
   * `createReport` mints one: a double click must resolve to one run, and the server
   * answers 44114 without the header rather than guessing an identity for us.
   */
  triggerAssurance(
    reportId: string,
    revisionNo: number,
    options?: { mode?: AssuranceMode; signal?: AbortSignal },
  ): Promise<AssuranceRun>;
  listAssuranceRuns(
    reportId: string,
    revisionNo: number,
    options?: { limit?: number; signal?: AbortSignal },
  ): Promise<AssuranceRun[]>;
  /** Poll one run while it is `queued`/`running`. */
  loadAssuranceRun(
    reportId: string,
    revisionNo: number,
    runId: string,
    options?: { signal?: AbortSignal },
  ): Promise<AssuranceRun>;
  /**
   * The workbench projection. `sectionCursor`/`findingCursor` come straight from
   * `next_section_cursor`/`next_finding_cursor` — the client never computes a cursor,
   * because an offset it computed itself would shift under a partial retry.
   */
  loadReview(
    reportId: string,
    revisionNo: number,
    options?: { runId?: string; sectionCursor?: string; findingCursor?: string; signal?: AbortSignal },
  ): Promise<ReportReview>;
  /**
   * Revision history, newest first. Needed by the workbench for one specific job:
   * a `resolved` decision must name a LATER revision, and the panel can only offer
   * one if it knows which exist. Without it the "标记已修复" button would be
   * permanently disabled, which is a dead end rather than a safety check.
   */
  /** Optional: a caller that does not need later-revision targets may omit it. */
  listRevisions?(reportId: string, options?: { signal?: AbortSignal }): Promise<ReportRevisionSummary[]>;
  /** Adjudicate one finding. `resolvedRevisionId` is required for `resolved`. */
  updateFinding(
    reportId: string,
    revisionNo: number,
    findingId: string,
    body: { status: FindingStatus; note?: string; resolvedRevisionId?: string },
  ): Promise<ReviewFinding>;
}

/**
 * Datasets a report run may bind.
 *
 * Deliberately *not* `useGetList("datasets")`: that is the governance catalogue, and
 * for a user with `governance.read` it asks for `scope=all` — every workspace in the
 * platform. A report is created inside the caller's workspace and
 * `freezeReportDatasets` resolves each id there, so offering the platform catalogue
 * hands the user rows the run must reject. Picking one used to end in a toast that
 * said only "报告任务启动失败。" (found live on 4.151, 2026-11-18: the POST answered
 * 404 `dataset not found` for a dataset displayed one click earlier).
 *
 * `scope=current` is the server's default and answers the caller's own workspace.
 */
export interface ReportDatasetOption {
  id: string;
  name: string;
}

async function listReportDatasets(signal?: AbortSignal): Promise<ReportDatasetOption[]> {
  const data = await unwrap<unknown>(
    api.get("/datasets?scope=current&page=1&page_size=200", { signal }),
  );
  const rows = Array.isArray(data)
    ? (data as Array<Record<string, unknown>>)
    : ((data as { items?: Array<Record<string, unknown>> })?.items ?? []);
  return rows
    .map((row) => ({ id: String(row.id ?? ""), name: String(row.name ?? row.id ?? "") }))
    .filter((row) => row.id !== "");
}

export function createReportAdapter(): ReportAdapter {
  const createIdempotencyKey = () =>
    `${REPORT_CREATE_IDEMPOTENCY_PREFIX}-${Date.now().toString(36)}-${newId()}`;

  async function listTemplates(): Promise<ReportTemplateView[]> {
    return templateList();
  }

  async function listReportDatasetsForAdapter(signal?: AbortSignal): Promise<ReportDatasetOption[]> {
    try {
      return await listReportDatasets(signal);
    } catch {
      // An unreadable dataset list is not a broken workspace: the template step and
      // the rest of the panel still work, and the user sees "no bindable datasets"
      // instead of a dead screen.
      return [];
    }
  }

  async function loadDetail(reportId: string, options: { signal?: AbortSignal } = {}): Promise<ReportDetailRecord> {
    return unwrap<ReportDetailRecord>(api.get(`/reports/${encodeURIComponent(reportId)}`, { signal: options.signal }));
  }

  async function loadFollowUps(
    reportId: string,
    options: { revision?: number; sectionKey?: string; signal?: AbortSignal } = {},
  ): Promise<ReportFollowUpView> {
    const query = new URLSearchParams();
    if (options.revision && options.revision > 0) query.set("revision", String(options.revision));
    if (options.sectionKey) query.set("section_key", options.sectionKey);
    const suffix = query.toString() ? `?${query.toString()}` : "";
    return unwrap<ReportFollowUpView>(
      api.get(`/reports/${encodeURIComponent(reportId)}/follow-ups${suffix}`, { signal: options.signal }),
    );
  }

  async function createReport(body: {
    title: string;
    question: string;
    template_key: string;
    research_depth?: string;
    datasets?: Array<{ dataset_id: string; engine_dataset_id?: string; role?: string; engine_id?: string }>;
    outline_confirmation?: string;
    origin_chat_id?: string;
    origin_session_id?: string;
  }): Promise<CreateReportResult> {
    return unwrap<CreateReportResult>(api.post("/reports", body, {
      headers: { "Idempotency-Key": createIdempotencyKey() },
    }));
  }

  async function cancelReport(reportId: string): Promise<unknown> {
    return unwrap<unknown>(api.post(`/reports/${encodeURIComponent(reportId)}/cancel`, {}));
  }

  async function loadSnapshot(reportId: string, jobId: string, options: { signal?: AbortSignal } = {}): Promise<ReportJobSnapshot> {
    return unwrap<ReportJobSnapshot>(api.get(`/report-jobs/${encodeURIComponent(jobId)}`, { signal: options.signal }));
  }

  async function confirmOutline(
    reportId: string,
    revisionNo: number,
    sections: Array<{ section_key: string; title: string }>,
  ): Promise<unknown> {
    return unwrap<unknown>(api.post(
      `/reports/${encodeURIComponent(reportId)}/revisions/${revisionNo}/outline/confirm`,
      { sections },
    ));
  }

  async function createExport(
    reportId: string,
    revisionNo: number,
    format: ReportExportFormat,
    options?: { acknowledgeAssurance?: boolean },
  ): Promise<ReportExportSuccess> {
    return unwrap<ReportExportSuccess>(api.post(
      `/reports/${encodeURIComponent(reportId)}/revisions/${revisionNo}/artifacts`,
      // `acknowledge_assurance` is sent only when the caller opted in: an omitted
      // field and an explicit `false` mean the same thing to the server, but sending
      // `false` on every export would invite a later default flip to change what an
      // already-written client does (doc/301 §7.2).
      options?.acknowledgeAssurance
        ? { format, acknowledge_assurance: true }
        : { format },
    ));
  }

  function assuranceBase(reportId: string, revisionNo: number): string {
    return `/reports/${encodeURIComponent(reportId)}/revisions/${revisionNo}/assurance-runs`;
  }

  async function triggerAssurance(
    reportId: string,
    revisionNo: number,
    options?: { mode?: AssuranceMode; signal?: AbortSignal },
  ): Promise<AssuranceRun> {
    return unwrap<AssuranceRun>(api.post(
      assuranceBase(reportId, revisionNo),
      { mode: options?.mode ?? "full" },
      {
        signal: options?.signal,
        headers: { [IDEMPOTENCY_HEADER]: createIdempotencyKey() },
      },
    ));
  }

  async function listAssuranceRuns(
    reportId: string,
    revisionNo: number,
    options?: { limit?: number; signal?: AbortSignal },
  ): Promise<AssuranceRun[]> {
    const limit = options?.limit ?? 20;
    const data = await unwrap<{ items?: AssuranceRun[] }>(api.get(
      assuranceBase(reportId, revisionNo),
      { params: { limit }, signal: options?.signal },
    ));
    return data?.items ?? [];
  }

  async function loadAssuranceRun(
    reportId: string,
    revisionNo: number,
    runId: string,
    options?: { signal?: AbortSignal },
  ): Promise<AssuranceRun> {
    return unwrap<AssuranceRun>(api.get(
      `${assuranceBase(reportId, revisionNo)}/${encodeURIComponent(runId)}`,
      { signal: options?.signal },
    ));
  }

  async function loadReview(
    reportId: string,
    revisionNo: number,
    options?: { runId?: string; sectionCursor?: string; findingCursor?: string; signal?: AbortSignal },
  ): Promise<ReportReview> {
    return unwrap<ReportReview>(api.get(
      `/reports/${encodeURIComponent(reportId)}/revisions/${revisionNo}/review`,
      {
        params: {
          run_id: options?.runId,
          section_cursor: options?.sectionCursor,
          finding_cursor: options?.findingCursor,
        },
        signal: options?.signal,
      },
    ));
  }

  async function listRevisions(
    reportId: string,
    options?: { signal?: AbortSignal },
  ): Promise<ReportRevisionSummary[]> {
    const data = await unwrap<{ revisions?: ReportRevisionSummary[] }>(api.get(
      `/reports/${encodeURIComponent(reportId)}/revisions`,
      { signal: options?.signal },
    ));
    return data?.revisions ?? [];
  }

  async function updateFinding(
    reportId: string,
    revisionNo: number,
    findingId: string,
    body: { status: FindingStatus; note?: string; resolvedRevisionId?: string },
  ): Promise<ReviewFinding> {
    return unwrap<ReviewFinding>(api.patch(
      `/reports/${encodeURIComponent(reportId)}/revisions/${revisionNo}/findings/${encodeURIComponent(findingId)}`,
      {
        status: body.status,
        note: body.note,
        resolved_revision_id: body.resolvedRevisionId,
      },
    ));
  }

  function artifactDownloadUrl(reportId: string, revisionNo: number, artifactId: string): string {
    return `/api/v1/reports/${encodeURIComponent(reportId)}/revisions/${revisionNo}/artifacts/${encodeURIComponent(artifactId)}/download`;
  }

  async function getTarget(targetId: string, { signal }: { signal?: AbortSignal } = {}): Promise<ConversationTargetRef> {
    const list = await templateList(signal);
    const view = list.find((item) => String(item.template_key ?? item.template_id) === targetId)
      ?? list.find((item) => String(item.template_id) === targetId);
    if (!view) {
      // The catalog is the only template read surface (there is no single-key
      // route in P0-A), so an unknown key degrades to a minimal ref rather than
      // a hard failure – the workspace substitutes the payload when it opens.
      return { id: targetId, kind: "report", name: targetId };
    }
    return reportTemplateTarget(view);
  }

  async function searchTargets(query: string, { limit }: { limit: number; cursor?: string; signal?: AbortSignal }): Promise<ConversationTargetPage> {
    const list = await templateList();
    const q = query.trim().toLowerCase();
    const filtered = (q ? list.filter((item) =>
      itemTemplateName(item).toLowerCase().includes(q) ||
      String(item.template_key).toLowerCase().includes(q),
    ) : list).slice(0, limit);
    return { items: filtered.map(reportTemplateTarget), nextCursor: list.length > limit ? "more" : undefined, total: list.length };
  }

  async function listContexts(_targetId: string, { limit, cursor, signal }: { limit: number; cursor?: string; signal?: AbortSignal }): Promise<ConversationContextPage> {
    const pageNumber = Number(cursor ? String(cursor).replace(/[^0-9]/g, "") : 1) || 1;
    const data = await requestPage<Record<string, unknown>>(`/reports?page=${pageNumber}&page_size=${limit}&keyword=${encodeURIComponent("")}`, signal)
      .catch(() => ({ items: [], total: 0 }));
    const rawPage = page(data, limit, pageNumber);
    return {
      ...rawPage,
      items: rawPage.items.map((item) => reportContextFromListItem(item)),
    };
  }

  async function loadContext(context: ConversationContext): Promise<ConversationSnapshot> {
    if (context.kind !== "report") throw new Error("not a report context");
    const detail = await unwrap<ReportDetailRecord>(api.get(`/reports/${encodeURIComponent(context.id)}`));
    // The aggregate's latest revision is the reporting surface; the live job is
    // followed through the snapshot, whose ids are remembered per report.
    const jobId = rememberedReportJob(context.id) ?? ("reportStatus" in context ? undefined : undefined);
    return {
      context: reportContextFromRecords(detail, { jobId }),
      messages: [],
    };
  }

  function reportContextFromListItem(item: Record<string, unknown>): ConversationContext & { kind: "report" } {
    const updatedAt = typeof item.updated_at === "string" ? Date.parse(String(item.updated_at)) : undefined;
    return {
      id: String(item.id ?? ""),
      targetId: String(item.template_key ?? ""),
      kind: "report",
      type: "run",
      ...(item.title ? { title: String(item.title) } : {}),
      ...(item.status ? { reportStatus: String(item.status) } : {}),
      ...(item.question ? { question: String(item.question) } : {}),
      ...(updatedAt ? { updatedAt } : {}),
    };
  }

  async function templateList(signal?: AbortSignal): Promise<ReportTemplateView[]> {
    const views = await unwrap<ReportTemplateView[]>(api.get("/report-templates", { signal }));
    return Array.isArray(views) ? views : [];
  }

  function itemTemplateName(view: ReportTemplateView): string {
    return view.payload?.name?.zh ?? view.payload?.name?.en ?? String(view.template_key);
  }

  function reportTemplateTarget(view: ReportTemplateView): ConversationTargetRef {
    const name = itemTemplateName(view);
    return {
      id: String(view.template_key),
      kind: "report" as const,
      name,
      description: name,
      status: view.retired ? "retired" : view.status,
    };
  }

  function start({ target, text, requestId, conversationKey }: ConversationStartInput): ConversationStream {
    const controller = new AbortController();

    async function* run(): AsyncGenerator<ConversationEvent> {
      let result: CreateReportResult;
      let context: (ConversationContext & { kind: "report" }) | null = null;
      try {
        // The stream's inherent question is the composer text; the title falls
        // back to the question (the workspace crafts both explicitly).
        result = await createReport({ title: text, question: text, template_key: target.id });
      } catch (error) {
        const e: ConversationEvent = { ...eventBase(state, { target, context: null, conversationKey, requestId }, "error.occurred"), type: "error.occurred", context: null, error: conversationError("report_create_failed", errorMessage(error), { httpStatus: errorStatus(error), traceId: errorTrace(error) }) };
        yield e;
        yield { ...eventBase(state, { target, context: null, conversationKey, requestId }, "stream.completed"), type: "stream.completed", context: null, finalStatus: "failed" };
        return;
      }
      rememberReportJob(result.report.id, result.job.id);
      context = reportContextFromRecords(result);
      const createBase = { target, context, conversationKey, requestId };
      yield { ...eventBase(state, createBase, "status.changed"), type: "status.changed", context, status: "STREAMING" };
      const poller = openReportEventPoller(result.job.id, {
        signal: controller.signal,
        onReset: async () => {
          const snap = await loadSnapshot(result.report.id, result.job.id).catch(() => null);
          return snap ? snap.event_seq : 0;
        },
      });
      let finalStatus: RunFinalStatus = "completed";
      try {
        for await (const frame of poller.frames()) {
          if (frame.type.endsWith(".progress") || frame.type.startsWith("section.") || frame.type.startsWith("job.") && frame.type !== "job.queued") {
            yield { ...eventBase(state, createBase, "status.changed"), type: "status.changed", context, status: "STREAMING" };
          }
          if (isReportTerminalEvent(frame.type)) {
            finalStatus = frame.type === "job.cancelled" ? "cancelled" : frame.type === "job.failed" ? "failed" : "completed";
          }
        }
      } catch (error) {
        if (controller.signal.aborted) throw error;
        const e: ConversationEvent = { ...eventBase(state, createBase, "error.occurred"), type: "error.occurred", context, error: conversationError("report_stream_failed", errorMessage(error), { failureKind: "unknown", traceId: errorTrace(error) }) };
        yield e;
        yield { ...eventBase(state, createBase, "stream.completed"), type: "stream.completed", context, finalStatus: "failed" };
        return;
      } finally {
        poller.abort();
      }
      yield { ...eventBase(state, createBase, "stream.completed"), type: "stream.completed", context, finalStatus };
    }

    const state: StreamState = { eventSequence: 0, assistantId: `report-${requestId}`, controller, streamId: `report-${requestId}` };
    return { requestId, streamId: `report-${requestId}`, events: run(), abort: () => controller.abort() };
  }

  return {
    kind: "report",
    capabilities: () => capabilities({
      streaming: false,
      feedback: false,
      artifacts: true,
      toolCalls: false,
      retry: true,
      stopGeneration: true,
      remoteCancel: true,
      contextModel: "run",
      persistence: "ephemeral_run",
    }),
    getTarget,
    searchTargets,
    listContexts,
    loadContext,
    async loadMoreMessages() {
      return { items: [] };
    },
    start,
    listTemplates,
    listReportDatasets: listReportDatasetsForAdapter,
    loadDetail,
    loadFollowUps,
    createReport,
    cancelReport,
    loadSnapshot,
    confirmOutline,
    createExport,
    artifactDownloadUrl,
    triggerAssurance,
    listAssuranceRuns,
    loadAssuranceRun,
    loadReview,
    listRevisions,
    updateFinding,
  };
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function errorStatus(error: unknown): number | undefined {
  return error instanceof ApiError ? error.status : undefined;
}

function errorTrace(error: unknown): string | undefined {
  return error instanceof ApiError ? error.traceId : undefined;
}
