import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { AttachmentDraft } from "../workbench/workbench-types";
import type { ConversationRouteDecision } from "./types";

export type PendingRequestState =
  | "routing"
  | "awaiting-choice"
  | "countdown"
  | "binding-assistant"
  | "loading-session"
  | "attachment-blocked"
  | "submitting"
  | "stream-connected"
  | "executing"
  | "execution-unknown"
  | "failed";

export type AttachmentCapability = "local-only" | "upload-enabled";
export type PendingOrigin = "direct-submit" | "mention-select" | "recommendation-select";
export type PendingFailureKind = "deterministic" | "unknown";
export type RetryAuthorization = "none" | "safe-retry";
export type PendingStage = "route" | "select" | "bootstrap" | "load-session" | "upload" | "submit" | "stream";
export type PendingOperationKind = "route" | "select" | "load-session" | "bootstrap" | "submit";

export interface ComposerDraft {
  composerDraftId: string;
  text: string;
  attachments: AttachmentDraft[];
  selectionStart: number;
  selectionEnd: number;
  attachmentCapability: AttachmentCapability;
  dirtySinceSubmit: boolean;
  updatedAt: string;
}

export interface PendingRequestError {
  code?: string;
  message: string;
  stage: PendingStage;
  failureKind: PendingFailureKind;
  retryAuthorization: RetryAuthorization;
}

export interface PendingConversationRequest {
  id: string;
  revision: number;
  sourceDraftId: string;
  textSnapshot: string;
  attachmentSnapshot: AttachmentDraft[];
  attachmentCapability: AttachmentCapability;
  origin: PendingOrigin;
  routeId?: string;
  routeDecisionRevision?: number;
  routeExpiresAt?: string;
  selectionExpiresAt?: string;
  candidateId?: string;
  candidateKind?: "chat" | "agent";
  targetId?: string;
  targetKind?: "chat" | "search" | "agent";
  contextId?: string;
  routeSelectionId?: string;
  bootstrapResourceId?: string;
  bootstrapSessionId?: string;
  routeSelectionIdempotencyKey?: string;
  bootstrapIdempotencyKey?: string;
  submitRequestId?: string;
  countdownEndsAt?: string;
  activeOperations: PendingOperation[];
  state: PendingRequestState;
  requestedAt: string;
  updatedAt: string;
  error?: PendingRequestError;
}

export interface PendingOperation {
  pendingId: string;
  kind: PendingOperationKind;
  operationId: string;
  revision: number;
  routeId?: string;
  routeSelectionId?: string;
  targetId?: string;
  contextId?: string;
  attachmentId?: string;
  startedAt: string;
}

export interface PendingRuntimeOperation extends PendingOperation {
  controller: AbortController;
}

export function createCspId(prefix?: string): string {
  const cryptoObject = typeof globalThis !== "undefined" ? globalThis.crypto : undefined;
  const id = cryptoObject?.randomUUID?.();
  if (!id) throw new Error("CSPRNG unavailable");
  return prefix ? `${prefix}-${id}` : id;
}

export function createIdempotencyKey(): string {
  return createCspId("rgx").replace(/[^A-Za-z0-9_-]/g, "_").slice(0, 128);
}

export function createComposerDraft(now = new Date().toISOString()): ComposerDraft {
  return {
    composerDraftId: createCspId("draft"),
    text: "",
    attachments: [],
    selectionStart: 0,
    selectionEnd: 0,
    attachmentCapability: "local-only",
    dirtySinceSubmit: true,
    updatedAt: now,
  };
}

export function routeAutoEligible(
  decision: ConversationRouteDecision,
  attachments: AttachmentDraft[],
  capability: AttachmentCapability,
): boolean {
  return decision.effective_mode === "auto_low_risk"
    && decision.score_status === "calibrated"
    && decision.selected?.auto_select_enabled === true
    && decision.selected.pre_execution_risk === "low"
    && (attachments.length === 0 || (capability === "upload-enabled" && attachments.every((item) => item.status === "ready")));
}

function updatePending(pending: PendingConversationRequest, patch: Partial<PendingConversationRequest>): PendingConversationRequest {
  return { ...pending, ...patch, updatedAt: new Date().toISOString() };
}

export function usePendingRequest() {
  const [composer, setComposer] = useState<ComposerDraft>(createComposerDraft);
  const [pending, setPendingState] = useState<PendingConversationRequest | null>(null);
  const composerRef = useRef(composer);
  const pendingRef = useRef(pending);
  const operationsRef = useRef(new Map<string, PendingRuntimeOperation>());

  useEffect(() => {
    composerRef.current = composer;
  }, [composer]);
  useEffect(() => {
    pendingRef.current = pending;
  }, [pending]);
  useEffect(() => () => {
    operationsRef.current.forEach((operation) => operation.controller.abort());
    operationsRef.current.clear();
  }, []);

  const setComposerState = useCallback((updater: (current: ComposerDraft) => ComposerDraft) => {
    setComposer((current) => updater(current));
  }, []);

  const updateDraft = useCallback((patch: Partial<Pick<ComposerDraft, "text" | "selectionStart" | "selectionEnd" | "attachments">>) => {
    setComposerState((current) => ({
      ...current,
      ...patch,
      dirtySinceSubmit: true,
      updatedAt: new Date().toISOString(),
    }));
  }, [setComposerState]);

  const setText = useCallback((value: string | ((current: string) => string)) => {
    setComposerState((current) => ({
      ...current,
      text: typeof value === "function" ? value(current.text) : value,
      dirtySinceSubmit: true,
      updatedAt: new Date().toISOString(),
    }));
  }, [setComposerState]);

  const setAttachments = useCallback((value: AttachmentDraft[] | ((current: AttachmentDraft[]) => AttachmentDraft[])) => {
    setComposerState((current) => ({
      ...current,
      attachments: typeof value === "function" ? value(current.attachments) : value,
      dirtySinceSubmit: true,
      updatedAt: new Date().toISOString(),
    }));
  }, [setComposerState]);

  const setSelection = useCallback((start: number, end: number) => {
    setComposerState((current) => ({
      ...current,
      selectionStart: start,
      selectionEnd: end,
      dirtySinceSubmit: true,
      updatedAt: new Date().toISOString(),
    }));
  }, [setComposerState]);

  const restoreDraft = useCallback((draft: Pick<ComposerDraft, "text" | "selectionStart" | "selectionEnd" | "composerDraftId">) => {
    setComposerState((current) => ({
      ...createComposerDraft(),
      composerDraftId: draft.composerDraftId,
      text: draft.text,
      selectionStart: draft.selectionStart,
      selectionEnd: draft.selectionEnd,
      dirtySinceSubmit: true,
    }));
  }, [setComposerState]);

  const markPending = useCallback((patch: Partial<PendingConversationRequest>) => {
    const current = pendingRef.current;
    if (!current) return null;
    const next = updatePending(current, patch);
    pendingRef.current = next;
    setPendingState(next);
    return next;
  }, []);

  const createPending = useCallback((origin: PendingOrigin): PendingConversationRequest => {
    if (pendingRef.current) throw new Error("Only one pending conversation request is allowed");
    const now = new Date().toISOString();
    const next: PendingConversationRequest = {
      id: createCspId("pending"),
      revision: 1,
      sourceDraftId: composerRef.current.composerDraftId,
      textSnapshot: composerRef.current.text.trim(),
      attachmentSnapshot: composerRef.current.attachments.map((item) => ({ ...item })),
      attachmentCapability: "local-only",
      origin,
      activeOperations: [],
      state: origin === "mention-select" ? "binding-assistant" : "routing",
      requestedAt: now,
      updatedAt: now,
    };
    composerRef.current = { ...composerRef.current, dirtySinceSubmit: false, updatedAt: now };
    setComposer(composerRef.current);
    pendingRef.current = next;
    setPendingState(next);
    return next;
  }, []);

  const beginOperation = useCallback((
    kind: PendingOperationKind,
    extras: Partial<Pick<PendingOperation, "routeId" | "routeSelectionId" | "targetId" | "contextId" | "attachmentId">> = {},
  ): PendingRuntimeOperation | null => {
    const current = pendingRef.current;
    if (!current) return null;
    const isDuplicate = current.activeOperations.some((item) =>
      item.pendingId === current.id
      && item.revision === current.revision
      && item.kind === kind
      && item.routeId === extras.routeId
      && item.routeSelectionId === extras.routeSelectionId
      && item.targetId === extras.targetId
      && item.contextId === extras.contextId
      && item.attachmentId === extras.attachmentId);
    if (isDuplicate) return null;
    const operation: PendingRuntimeOperation = {
      pendingId: current.id,
      kind,
      operationId: createCspId("op"),
      revision: current.revision,
      startedAt: new Date().toISOString(),
      ...extras,
      controller: new AbortController(),
    };
    const activeOperations = [...current.activeOperations, operation];
    const next = updatePending(current, { activeOperations });
    pendingRef.current = next;
    setPendingState(next);
    operationsRef.current.set(operation.operationId, operation);
    return operation;
  }, []);

  const endOperation = useCallback((operationId: string) => {
    const operation = operationsRef.current.get(operationId);
    if (!operation) return;
    operationsRef.current.delete(operationId);
    const current = pendingRef.current;
    if (!current || current.id !== operation.pendingId) return;
    const next = updatePending(current, {
      activeOperations: current.activeOperations.filter((item) => item.operationId !== operationId),
    });
    pendingRef.current = next;
    setPendingState(next);
  }, []);

  const cancelOperations = useCallback(() => {
    operationsRef.current.forEach((operation) => operation.controller.abort());
    operationsRef.current.clear();
  }, []);

  const cancelPending = useCallback(() => {
    cancelOperations();
    const current = pendingRef.current;
    pendingRef.current = null;
    setPendingState(null);
    if (current) {
      composerRef.current = { ...composerRef.current, updatedAt: new Date().toISOString() };
      setComposer(composerRef.current);
    }
  }, [cancelOperations]);

  const failPending = useCallback((error: PendingRequestError, state: PendingRequestState = "failed") => {
    markPending({ state, error, activeOperations: [] });
    cancelOperations();
  }, [cancelOperations, markPending]);

  const completePending = useCallback(() => {
    const current = pendingRef.current;
    pendingRef.current = null;
    setPendingState(null);
    cancelOperations();
    if (!current) return;
    composerRef.current = current.sourceDraftId === composerRef.current.composerDraftId && !composerRef.current.dirtySinceSubmit
      ? { ...composerRef.current, text: "", attachments: [], selectionStart: 0, selectionEnd: 0, updatedAt: new Date().toISOString() }
      : { ...composerRef.current, updatedAt: new Date().toISOString() };
    setComposer(composerRef.current);
  }, [cancelOperations]);

  const setPending = useCallback((updater: (current: PendingConversationRequest) => PendingConversationRequest) => {
    const current = pendingRef.current;
    if (!current) return null;
    const next = updater(current);
    pendingRef.current = next;
    setPendingState(next);
    return next;
  }, []);

  const getPending = useCallback(() => pendingRef.current, []);

  const rejectOperation = useCallback((operation: PendingRuntimeOperation, expected: Partial<PendingOperation>) => {
    const current = pendingRef.current;
    return !current
      || current.id !== operation.pendingId
      || current.revision !== operation.revision
      || (expected.kind !== undefined && operation.kind !== expected.kind)
      || operation.routeId !== expected.routeId
      || operation.routeSelectionId !== expected.routeSelectionId
      || operation.targetId !== expected.targetId
      || operation.contextId !== expected.contextId
      || (expected.attachmentId !== undefined && operation.attachmentId !== expected.attachmentId);
  }, []);

  return useMemo(() => ({
    composer,
    pending,
    createPending,
    beginOperation,
    endOperation,
    cancelOperations,
    cancelPending,
    completePending,
    failPending,
    markPending,
    getPending,
    setPending,
    setText,
    setAttachments,
    setSelection,
    restoreDraft,
    rejectOperation,
    hasActivePending: pending !== null,
  }), [
    beginOperation,
    cancelOperations,
    cancelPending,
    completePending,
    composer,
    createPending,
    endOperation,
    failPending,
    markPending,
    pending,
    rejectOperation,
    restoreDraft,
    setAttachments,
    setPending,
    setSelection,
    setText,
  ]);
}
