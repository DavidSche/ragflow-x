import type { AttachmentDraft, Citation, ConversationArtifact, ConversationKind, ConversationToolCall, Message } from "../workbench/workbench-types";

export const CONVERSATION_EVENT_SCHEMA_VERSION = 1;
export const CONVERSATION_MESSAGE_PAGE_LIMIT = 50;

export type { ConversationKind };

export type ConversationContextType = "session" | "run";

export type ConversationContext =
  | { kind: "chat"; type: "session"; id: string; targetId: string; title?: string; updatedAt?: number }
  | { kind: "search"; type: "run"; id: string; targetId: string; updatedAt?: number }
  | { kind: "agent"; type: "session"; id: string; targetId: string; title?: string; updatedAt?: number };

export interface ConversationTargetRef {
  id: string;
  kind: ConversationKind;
  name: string;
  description?: string;
  canvasCategory?: string;
  status?: string;
  ownerId?: string;
  knowledgeScope?: string[];
  model?: string;
  updatedAt?: number;
}

export interface ConversationTargetPage {
  items: ConversationTargetRef[];
  nextCursor?: string;
  total?: number;
}

export interface ConversationContextPage {
  items: ConversationContext[];
  nextCursor?: string;
  total?: number;
}

export interface ConversationSnapshot {
  context: ConversationContext;
  messages: Message[];
  nextCursor?: string;
}

export interface ConversationMessagePage {
  items: Message[];
  nextCursor?: string;
}

export interface ConversationStartInput {
  target: Pick<ConversationTargetRef, "id" | "kind">;
  context?: ConversationContext;
  text: string;
  attachments?: AttachmentDraft[];
  requestId: string;
  conversationKey: string;
}

export type RunFinalStatus = "completed" | "failed" | "cancelled";

export type ConversationStatus =
  | "IDLE"
  | "LOADING_CONTEXT"
  | "READY"
  | "PREPARING"
  | "STREAMING"
  | "CANCELLING"
  | "COMPLETED"
  | "FAILED"
  | "CANCELLED";

export type ConversationTargetStatus = "EMPTY" | "LOADING" | "READY" | "REFRESHING";

export interface ConversationUsage {
  inputTokens?: number;
  outputTokens?: number;
  totalTokens?: number;
}

export interface ConversationError {
  errorCode: string;
  displayMessage: string;
  httpStatus?: number;
  retriable: boolean;
  traceId?: string;
}

export interface ConversationEventBase {
  type: string;
  eventId: string;
  sequence: number;
  timestamp: string;
  schemaVersion: number;
  requestId: string;
  streamId: string;
  traceId?: string;
  conversationKey: string;
  target: Pick<ConversationTargetRef, "id" | "kind">;
  context: ConversationContext | null;
}

export type ConversationEvent =
  | (ConversationEventBase & { type: "message.started"; context: ConversationContext; messageId: string; role: Message["role"] })
  | (ConversationEventBase & { type: "message.delta"; context: ConversationContext; messageId: string; delta: string })
  | (ConversationEventBase & { type: "message.completed"; context: ConversationContext; messageId: string })
  | (ConversationEventBase & { type: "citation.added"; context: ConversationContext; messageId: string; citation: Citation })
  | (ConversationEventBase & { type: "artifact.added"; context: ConversationContext; messageId: string; artifact: ConversationArtifact })
  | (ConversationEventBase & { type: "tool.started"; context: ConversationContext; messageId: string; toolCall: ConversationToolCall })
  | (ConversationEventBase & { type: "tool.completed"; context: ConversationContext; messageId: string; toolCall: ConversationToolCall })
  | (ConversationEventBase & { type: "usage.updated"; context: ConversationContext | null; messageId?: string; usage: ConversationUsage })
  | (ConversationEventBase & { type: "status.changed"; context: ConversationContext | null; status: ConversationStatus })
  | (ConversationEventBase & { type: "cancelled"; context: ConversationContext | null; messageId?: string; reason: "user" | "target_switched" | "context_switched" | "timeout" })
  | (ConversationEventBase & { type: "error.occurred"; context: ConversationContext | null; messageId?: string; error: ConversationError })
  | (ConversationEventBase & { type: "stream.completed"; context: ConversationContext | null; finalStatus: RunFinalStatus });

export interface ConversationStream {
  requestId: string;
  streamId: string;
  events: AsyncIterable<ConversationEvent>;
  abort(): void;
}

export interface ActiveRun {
  runId: string;
  requestId: string;
  streamId: string;
  conversationKey: string;
  context: ConversationContext | null;
}

export interface ConversationCapabilities {
  streaming: boolean;
  attachments: boolean;
  feedback: boolean;
  artifacts: boolean;
  toolCalls: boolean;
  export: boolean;
  stopGeneration: boolean;
  retry: boolean;
  remoteCancel: boolean;
  serverSessions: boolean;
  contextModel: "session" | "run";
  persistence: "server_session" | "ephemeral_run";
}

export interface RecentTargetEntry {
  target: ConversationTargetRef;
  displayName: string;
  lastUsedAt: number;
  source: "local";
}

export interface ConversationAdapter {
  kind: ConversationKind;
  capabilities(): ConversationCapabilities;
  getTarget(targetId: string, options?: { signal?: AbortSignal }): Promise<ConversationTargetRef>;
  searchTargets(query: string, options: { limit: number; cursor?: string; signal?: AbortSignal }): Promise<ConversationTargetPage>;
  listContexts(targetId: string, options: { limit: number; cursor?: string; signal?: AbortSignal }): Promise<ConversationContextPage>;
  loadContext(context: ConversationContext): Promise<ConversationSnapshot>;
  loadMoreMessages(context: ConversationContext, cursor?: string, options?: { limit?: number }): Promise<ConversationMessagePage>;
  createSession?(targetId: string): Promise<ConversationContext>;
  start(input: ConversationStartInput): ConversationStream;
}

export type ConversationCenterTelemetryEventName =
  | "target_selected"
  | "context_opened"
  | "route_requested"
  | "route_candidate_selected"
  | "route_failed"
  | "message_submitted"
  | "run_started"
  | "run_completed"
  | "run_failed"
  | "run_cancelled"
  | "retry_clicked"
  | "citation_clicked"
  | "export_clicked"
  | "feedback_submitted"
  | "knowledge_task_created";

export interface ConversationCenterTelemetryEvent {
  name: ConversationCenterTelemetryEventName;
  occurredAt: string;
  conversationKey?: string;
  target?: Pick<ConversationTargetRef, "id" | "kind">;
  runId?: string;
  requestId?: string;
  traceId?: string;
  attributes?: Record<string, string | number | boolean>;
}

export type ConversationTelemetrySink = (event: ConversationCenterTelemetryEvent) => void;

export const noopTelemetrySink: ConversationTelemetrySink = () => undefined;

export type ConversationRouteKind = "chat" | "agent";

export type ConversationRouteStatus = "idle" | "routing" | "suggest" | "clarify" | "selecting" | "bootstrapping" | "failed";

export interface ConversationRouteCandidate {
  kind: ConversationRouteKind;
  id: string;
  target_id: string;
  name: string;
  normalized_score: number;
  normalized_margin: number | null;
  confidence: number | null;
  confidence_status: "not_calibrated" | "calibrated";
  candidate_count: number;
  has_competitor: boolean;
  routing_readiness: number;
  routing_readiness_type: string;
  agent_flow_readiness: number;
  pre_execution_risk: "low" | "medium" | "high";
  auto_select_enabled: boolean;
  catalog_freshness_sec: number;
  reasons: string[];
}

export interface ConversationRouteDecision {
  route_id: string;
  score_status: "provisional" | "calibrated";
  candidate_count: number;
  requested_mode: "suggest" | "auto-request";
  effective_mode: "manual" | "suggest" | "clarify" | "recommend_only" | "auto_low_risk" | "disabled";
  selected: ConversationRouteCandidate | null;
  candidates: ConversationRouteCandidate[];
  expires_at: string;
  router_version: string;
  policy_mode: "disabled" | "recommend_only" | "auto_low_risk";
  policy_version: string;
  rerank_status: "not_applicable" | "disabled" | "applied" | "degraded";
  route_budget_ms: number;
  budget_exceeded: boolean;
  latency_ms: number;
}

export interface ConversationRouteSelection {
  route_selection_id: string;
  kind: ConversationRouteKind;
  target_id: string;
  expires_at: string;
  state: "NEW" | "RESERVED" | "CONSUMED" | "FAILED" | "EXPIRED";
  requires_session: boolean;
}

export interface ConversationBootstrapOperation {
  route_selection_id: string;
  bootstrap_operation_id: string;
  bootstrap_type: "CREATE_SESSION";
  kind: ConversationRouteKind;
  target_id: string;
  session_id: string;
  selection_state: "NEW" | "RESERVED" | "CONSUMED" | "FAILED" | "EXPIRED";
  bootstrap_state: "PENDING" | "RESERVED" | "SUCCEEDED" | "FAILED" | "EXPIRED";
}

export class ConversationProtocolError extends Error {
  readonly errorCode: string;

  constructor(errorCode: string, message: string) {
    super(message);
    this.name = "ConversationProtocolError";
    this.errorCode = errorCode;
  }
}
