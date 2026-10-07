/**
 * Shared types and utils for the workbench modules.
 */

export interface Citation {
  id?: string;
  name: string;
  content: string;
  datasetId?: string;
  docId?: string;
  chunkId?: string;
  attachmentId?: string;
  imageId?: string;
  sourceUri?: string;
}

export type ConversationKind = "chat" | "search" | "agent";

export type ConversationErrorCode =
  | "no_result"
  | "insufficient_citation"
  | "model_failure"
  | "permission_denied"
  | "gateway_rate_limited";

export function isSafeSourceUri(uri: string): boolean {
  try {
    if (!uri.startsWith("/") && !/^https?:\/\//i.test(uri)) return false;
    const url = new URL(uri, window.location.href);
    return url.protocol === "https:" || url.origin === window.location.origin;
  } catch {
    return false;
  }
}

export type ConversationArtifactType = "table" | "chart" | "card" | "file" | "link";

export type ConversationTableData =
  | { columns: unknown[]; rows: unknown[][] }
  | unknown[][]
  | Record<string, unknown>[];

export interface ConversationChartData {
  type?: "bar" | "line" | "area";
  labels?: unknown[];
  datasets?: { label?: string; data?: unknown[]; color?: string }[];
  unit?: string;
}

interface ConversationArtifactBase {
  title?: string;
  contentType?: string;
  content?: string;
  url?: string;
}

export interface ConversationTableArtifact extends ConversationArtifactBase {
  type: "table";
  data?: ConversationTableData;
}

export interface ConversationChartArtifact extends ConversationArtifactBase {
  type: "chart";
  data?: ConversationChartData;
}

export interface ConversationCardArtifact extends ConversationArtifactBase {
  type: "card";
}

export interface ConversationFileArtifact extends ConversationArtifactBase {
  type: "file";
}

export interface ConversationLinkArtifact extends ConversationArtifactBase {
  type: "link";
}

export type ConversationArtifact =
  | ConversationTableArtifact
  | ConversationChartArtifact
  | ConversationCardArtifact
  | ConversationFileArtifact
  | ConversationLinkArtifact;

export type ConversationToolCallStatus = "running" | "success" | "failed";

export type AttachmentStatus = "staged" | "uploading" | "uploaded" | "ready" | "error";

export interface ConversationToolCall {
  name?: string;
  status?: ConversationToolCallStatus;
  summary?: string;
}

export interface Message {
  id?: string;
  role: "user" | "assistant" | "system";
  content: string;
  kind?: ConversationKind;
  turnId?: string;
  citations?: Citation[];
  artifacts?: ConversationArtifact[];
  toolCalls?: ConversationToolCall[];
  feedback?: "positive" | "negative" | "";
  error?: string;
  files?: UploadedFileMeta[];
  model?: string;
  errorCode?: string;
  traceId?: string;
  answerRunId?: string;
  answerSnapshotId?: string;
  usage?: { inputTokens?: number; outputTokens?: number; totalTokens?: number };
  createdAt?: string;
  status?: "streaming" | "completed" | "failed" | "cancelled";
}

export function classifyConversationError(status?: number, message?: string, citationCount?: number): ConversationErrorCode {
  const text = (message ?? "").toLowerCase();
  if (status === 401 || status === 403 || text.includes("permission")) return "permission_denied";
  if (status === 429 || text.includes("rate limit") || text.includes("quota")) return "gateway_rate_limited";
  if (status && status >= 500) return "model_failure";
  if (text.includes("no result") || text.includes("empty response")) {
    return citationCount === 0 ? "insufficient_citation" : "no_result";
  }
  return "model_failure";
}

export interface ChatLike {
  id: string;
  name: string;
}

export interface SessionLike {
  id: string;
  name: string;
  chat_id?: string;
  messages?: Message[];
  reference?: Record<string, unknown>[];
}

export type Rating = "positive" | "negative";

export function newId(): string {
  const id = globalThis.crypto?.randomUUID?.();
  if (!id) throw new Error("CSPRNG unavailable");
  return id;
}

export function feedbackRequestId(turnId?: string): string {
  if (!turnId) return "";
  return turnId.startsWith("assistant-") ? turnId.slice("assistant-".length) : turnId;
}

export function stableCitationId(value: string): string {
  let hash = 0x811c9dc5;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 0x01000193);
  }
  return `cit-${(hash >>> 0).toString(36)}`;
}

export function mergeCitations(base: Citation[], incoming: Citation[]): Citation[] {
  const seen = new Set(base.map((c) => c.name + c.content));
  const out = base.slice();
  for (const c of incoming) {
    if (c.content && !seen.has(c.name + c.content)) out.push(c);
  }
  return out;
}

// Extract the distinct numeric IDs cited in an answer, e.g. [ID:3], [ID:0ID:2] -> [3], [0,2].
export function extractCitedIds(content: string): number[] {
  const ids = new Set<number>();
  const re = /\[ID:\s*([^\]]*)\]/gi;
  let m: RegExpExecArray | null;
  while ((m = re.exec(content)) !== null) {
    (String(m[1]).match(/\d+/g) || []).forEach((d) => {
      const n = Number(d);
      if (Number.isFinite(n)) ids.add(n);
    });
  }
  return [...ids].sort((a, b) => a - b);
}

export function extractCitations(obj: unknown): Citation[] {
  const source = (obj ?? {}) as Record<string, unknown>;
  const raw = source.reference ?? source.citations;
  if (!raw) return [];
  let list: unknown[];
  if (Array.isArray(raw)) {
    list = raw;
  } else {
    const rec = raw as Record<string, unknown>;
    if (Array.isArray(rec.chunks)) list = rec.chunks;
    else if (Array.isArray(rec.citations)) list = rec.citations;
    else list = Object.values(rec);
  }
  return list
    .filter(Boolean)
    .map((r, index): Citation => {
      if (typeof r === "string") return { name: r, content: "" };
      const o = r as Record<string, unknown>;
      const name = String(o.name ?? o.doc_name ?? o.document_name ?? o.chunk_id ?? o.id ?? "引用");
      const content = String(o.content ?? o.question ?? "");
      const chunkId = o.chunk_id ? String(o.chunk_id) : o.id ? String(o.id) : undefined;
      return {
        id: chunkId ?? stableCitationId(`${name}\n${content}:${index}`),
        name,
        content,
        datasetId: o.dataset_id ? String(o.dataset_id) : o.kb_id ? String(o.kb_id) : undefined,
        docId: o.doc_id ? String(o.doc_id) : o.document_id ? String(o.document_id) : undefined,
        chunkId,
        sourceUri: o.source_uri ? String(o.source_uri) : undefined,
      };
    })
    .filter((c) => c.content);
}

export async function streamRead(
  body: ReadableStream<Uint8Array>,
  onEvent: (delta: string, refs: Citation[], raw?: Record<string, unknown>) => void,
  onError?: (message: string) => void,
): Promise<void> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split("\n");
    buffer = lines.pop() ?? "";
    for (const raw of lines) {
      const line = raw.trim();
      if (!line.startsWith("data:")) continue;
      const payload = line.slice(5).trim();
      if (payload === "[DONE]") continue;
      try {
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        const obj = JSON.parse(payload) as any;
        // OpenAI-compatible terminal error frame: data: {"error": {...}}.
        if (obj && typeof obj === "object" && obj.error) {
          const msg =
            typeof obj.error === "string"
              ? obj.error
              : String(obj.error?.message ?? JSON.stringify(obj.error));
          if (onError) onError(msg);
          else onEvent(msg, []);
          continue;
        }
        const inner = obj?.data;
        let delta = "";
        let refs: Citation[] = [];
        const choice = obj?.choices?.[0];
        const openAIDelta = choice?.delta;
        const openAIMessage = choice?.message;
        if (inner && typeof inner === "object") {
          delta = inner?.answer ?? "";
          refs = extractCitations(inner);
        }
        if (!delta) {
          const fallback = obj?.answer ?? openAIDelta?.content ?? openAIMessage?.content ?? "";
          delta = typeof fallback === "string" ? fallback : "";
        }
        refs = refs.length
          ? refs
          : openAIDelta?.reference
            ? citationsFromReference(openAIDelta.reference as Record<string, unknown>)
            : openAIMessage?.reference
              ? citationsFromReference(openAIMessage.reference as Record<string, unknown>)
              : refs;
        onEvent(delta, refs, typeof inner === "object" ? (inner as Record<string, unknown>) : undefined);
      } catch {
        console.debug("ragflow-x: malformed SSE payload");
        if (payload) onEvent(payload + "\n", []);
      }
    }
  }
}

export interface UploadedFileMeta {
  id: string;
  name: string;
  size: number;
  extension?: string;
  mime_type?: string;
  preview_url?: string | null;
  created_at?: number;
  created_by?: string;
  content?: string;
  upload_ticket?: string;
}

export interface AttachmentDraft {
  id: string;
  name: string;
  size: number;
  mime: string;
  status: AttachmentStatus;
  progress: number;
  meta?: UploadedFileMeta;
  file?: File;
  error?: string;
}

// Convert a session reference entry into workbench citations (text + image).
export function citationsFromReference(entry?: Record<string, unknown> | null): Citation[] {
  const chunks = entry?.chunks;
  if (!Array.isArray(chunks)) return [];
  return chunks.filter((r) => r && typeof r === "object").map((r, index) => {
    const o = r as Record<string, unknown>;
    const chunkId = o.chunk_id ? String(o.chunk_id) : o.id ? String(o.id) : undefined;
    const docId = o.document_id ? String(o.document_id) : o.doc_id ? String(o.doc_id) : undefined;
    const name = String(o.document_name ?? o.docnm_kwd ?? o.name ?? o.id ?? "引用");
    const content = String(o.content ?? o.content_with_weight ?? "");
    return {
      id: chunkId ?? (docId ? `${docId}:${index}` : stableCitationId(`${name}\n${content}:${index}`)),
      datasetId: o.dataset_id ? String(o.dataset_id) : o.kb_id ? String(o.kb_id) : undefined,
      name,
      content,
      docId,
      chunkId,
      sourceUri: o.source_uri ? String(o.source_uri) : undefined,
      imageId: o.image_id ? String(o.image_id) : o.img_id ? String(o.img_id) : undefined,
    };
  });
}
