import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { useCanAccess, useGetIdentity, useNotify, useTranslate } from "ra-core";
import { useSearchParams } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Loader2, MessageSquare, Search } from "lucide-react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { api, ApiError } from "../../lib/api";
import { ConversationShell } from "../workbench/ConversationShell";
import { ConversationCitationViewer } from "./CitationViewer";
import { ConversationCenterInput } from "./ConversationCenterInput";
import { RouteDecisionCard } from "./RouteDecisionCard";
import { AssistantEmptyState } from "./AssistantEmptyState";
import { AssistantHeader } from "./AssistantHeader";
import { CrossAppSessionIndex } from "../workbench/CrossAppSessionIndex";
import type { AttachmentDraft, Message, UploadedFileMeta } from "../workbench/workbench-types";
import type { Citation } from "../workbench/workbench-types";
import { feedbackRequestId } from "../workbench/workbench-types";
import { createAgentAdapter, createChatAdapter, createSearchAdapter } from "./adapters";
import { entryTimestamp, rememberRecentTimestamp, targetSubtitle } from "./utils";
import { useAssistantDirectory } from "./use-assistant-directory";
import { useConversationRouter } from "./use-conversation-router";
import { useTargetDirectory } from "./use-target-directory";
import { useContextList } from "./use-context-list";
import {
  DraftStore,
  RecentTargetStore,
  ConversationEventSink,
  assertConversationContext,
  conversationKey,
  contextTitle,
  groupContextsByTime,
  isConversationTarget,
  matchesCommand,
  parseInputTrigger,
  replaceInputToken,
  slashCommandOptions,
  stripLeadingCommand,
  newConversationRunId,
  createOpaqueScope,
} from "./logic";
import { createCspId, createIdempotencyKey, routeAutoEligible, usePendingRequest } from "./use-pending-request";
import type {
  ActiveRun,
  ConversationAdapter,
  ConversationContext,
  ConversationKind,
  ConversationStatus,
  ConversationStream,
  ConversationTargetRef,
  ConversationTargetStatus,
  ConversationCenterTelemetryEventName,
  RecentTargetEntry,
  RunFinalStatus,
} from "./types";
import { noopTelemetrySink, type ConversationTelemetrySink } from "./types";

const KINDS: { kind: ConversationKind; labelKey: string }[] = [
  { kind: "chat", labelKey: "conversationCenter.kind_chat" },
  { kind: "search", labelKey: "conversationCenter.kind_search" },
  { kind: "agent", labelKey: "conversationCenter.kind_agent" },
];

const CONTEXT_TIME_GROUPS: { key: "today" | "yesterday" | "earlier"; labelKey: string }[] = [
  { key: "today", labelKey: "conversationCenter.time_today" },
  { key: "yesterday", labelKey: "conversationCenter.time_yesterday" },
  { key: "earlier", labelKey: "conversationCenter.time_earlier" },
];

function TargetMeta({ target, translate }: {
  target: ConversationTargetRef;
  translate: (key: string) => string;
}) {
  const scope = target.knowledgeScope?.length
    ? `${translate("conversationCenter.knowledge_scope")}${target.knowledgeScope.slice(0, 2).join("、")}${target.knowledgeScope.length > 2 ? ` +${target.knowledgeScope.length - 2}` : ""}`
    : target.description;
  return (
    <div className="min-w-0 text-left">
      <div className="truncate text-sm font-medium">{target.name}</div>
      <div className="truncate text-xs text-muted-foreground">{translate(`conversationCenter.kind_${target.kind}`)}</div>
      {scope ? <div className="mt-0.5 truncate text-xs text-muted-foreground">{scope}</div> : null}
    </div>
  );
}

export function ConversationCenter({ telemetry }: { telemetry?: ConversationTelemetrySink } = {}) {
  const t = useTranslate();
  const notify = useNotify();
  const { data: identity, isLoading: identityLoading } = useGetIdentity();
  const chatAccessResult = useCanAccess({ resource: "workbench", action: "execute" });
  const chatReadResult = useCanAccess({ resource: "chats", action: "show" });
  const searchAccessResult = useCanAccess({ resource: "search-apps", action: "execute" });
  const agentAccessResult = useCanAccess({ resource: "agents", action: "execute" });
  const agentSessionCreateResult = useCanAccess({ resource: "agents", action: "session:create" });
  const chatAccess = chatAccessResult?.canAccess === true;
  const chatRead = chatReadResult?.canAccess === true;
  const searchAccess = searchAccessResult?.canAccess === true;
  const agentAccess = agentAccessResult?.canAccess === true;
  const agentSessionCreate = agentSessionCreateResult?.canAccess === true;
  const [searchParams, setSearchParams] = useSearchParams();
  const initialUrlKind = searchParams.get("kind");
  const [kind, setKind] = useState<ConversationKind>(
    initialUrlKind === "chat" || initialUrlKind === "search" || initialUrlKind === "agent" ? initialUrlKind : "chat",
  );

  const adapters = useMemo<Record<ConversationKind, ConversationAdapter>>(() => ({
    chat: createChatAdapter(),
    search: createSearchAdapter(),
    agent: createAgentAdapter(),
  }), []);

  const scopeKey = `${String(identity?.tenant_id ?? "")}:${String(identity?.id ?? "")}`;
  const [target, setTarget] = useState<ConversationTargetRef | null>(null);
  const [context, setContext] = useState<ConversationContext | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [status, setStatus] = useState<ConversationStatus>("IDLE");
  const requestState = usePendingRequest();
  const input = requestState.composer.text;
  const attachments = requestState.composer.attachments;
  const setInput = requestState.setText;
  const setAttachments = requestState.setAttachments;
  const [messageCursor, setMessageCursor] = useState<string>();
  const [composing, setComposing] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const [openCitation, setOpenCitation] = useState<Citation | null>(null);
  const [lastSubmission, setLastSubmission] = useState<{ text: string; context: ConversationContext | null } | null>(null);
  const [agentSessionUnknown, setAgentSessionUnknown] = useState(false);

  const abortRef = useRef<ConversationStream | null>(null);
  const activeRunRef = useRef<ActiveRun | null>(null);
  const sinkRef = useRef<{ push: (event: import("./types").ConversationEvent, run: ActiveRun) => void } | null>(null);
  const submitRef = useRef<((text?: string, context?: ConversationContext, target?: ConversationTargetRef) => Promise<void>) | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const activeOption = useRef(0);
  const [activeOptionIndex, setActiveOptionIndex] = useState(0);
  const paletteId = useId();
  const composingRef = useRef(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const mountedRef = useRef(true);
  const savedDraftKeyRef = useRef<string | null>(null);
  const defaultTargetKeyRef = useRef<string | null>(null);
  const urlRestoreRef = useRef<string | null>(null);

  const adapter = adapters[kind];
  const {
    contexts,
    contextCursor,
    contextsLoading,
    reset: resetContexts,
    update: updateContext,
    loadMore: loadMoreContexts,
  } = useContextList({
    adapter,
    identity,
    target,
    notify,
    translate: t,
  });
  const draftScope = useMemo(() => createOpaqueScope(scopeKey), [scopeKey]);
  const draftStore = useMemo(() => new DraftStore(sessionStorage, draftScope), [draftScope]);
  const recentStore = useMemo(() => new RecentTargetStore(localStorage, scopeKey), [scopeKey]);
  const [recentTargets, setRecentTargets] = useState<RecentTargetEntry[]>([]);
  const conversationId = context?.id ?? "new";
  const draftKey = conversationKey(scopeKey, kind, target?.id ?? "none", conversationId);
  const busy = status === "PREPARING" || status === "STREAMING" || status === "CANCELLING";
  const agentContextMissing = kind === "agent" && (!context || context.type !== "session");
  const [inputFocusNonce, setInputFocusNonce] = useState(0);
  useEffect(() => {
    if (inputFocusNonce > 0) inputRef.current?.focus();
  }, [inputFocusNonce]);
  const focusInput = useCallback(() => setInputFocusNonce((nonce) => nonce + 1), []);
  const trigger = parseInputTrigger(input, inputRef.current?.selectionStart ?? input.length);
  const commandOptions = trigger?.type === "command" ? slashCommandOptions().filter((option) => matchesCommand(option, trigger.query)) : [];
  const capabilities = adapter.capabilities();
  const telemetrySink = telemetry ?? noopTelemetrySink;
  const trackEvent = useCallback((name: ConversationCenterTelemetryEventName, event: Omit<Parameters<ConversationTelemetrySink>[0], "name" | "occurredAt"> = {}) => {
    telemetrySink({ ...event, name, occurredAt: new Date().toISOString() });
  }, [telemetrySink]);
  const accessibleKinds = useMemo(() => KINDS.filter((item) =>
    item.kind === "chat" ? chatAccess : item.kind === "search" ? searchAccess : agentAccess), [agentAccess, chatAccess, searchAccess]);
  const unifiedDirectory = useAssistantDirectory({
    accessible: chatAccess || agentAccess,
    identity,
    notify,
    translate: t,
  });
  const router = useConversationRouter({
    enabled: !target && (chatAccess || agentAccess),
    notify,
    translate: t,
  });
  const searchDirectory = useTargetDirectory({
    adapter,
    accessible: kind === "search" && searchAccess,
    identity,
    notify,
    translate: t,
  });
  const activeDirectory = kind === "search" ? searchDirectory : unifiedDirectory;
  const {
    targetQuery,
    setTargetQuery,
    targets,
    targetTotal,
    targetsLoading,
    targetStatus,
  } = activeDirectory;
  const searchTargetCursor = kind === "search" ? searchDirectory.targetCursor : undefined;
  const loadMoreSearchTargets = searchDirectory.loadMore;
  const resetTargets = useCallback(() => {
    unifiedDirectory.reset();
    searchDirectory.reset();
  }, [searchDirectory, unifiedDirectory]);
  const targetOptions = trigger?.type === "mention" ? unifiedDirectory.targets.filter((item) => item.name.toLowerCase().includes(trigger.query.toLowerCase())) : [];
  const paletteOpen = !composing && trigger !== null && (commandOptions.length > 0 || targetOptions.length > 0);
  const activeOptionId = paletteOpen ? `${paletteId}-option-${activeOptionIndex}` : undefined;
  const groupedContexts = useMemo(() => groupContextsByTime(contexts), [contexts]);

  const syncUrl = useCallback((nextKind: ConversationKind, nextTarget: ConversationTargetRef | null, nextContext: ConversationContext | null) => {
    const params = new URLSearchParams();
    params.set("kind", nextKind);
    if (nextTarget) params.set("targetId", nextTarget.id);
    if (nextContext && nextKind !== "search") params.set("contextId", nextContext.id);
    urlRestoreRef.current = `${scopeKey}:${nextKind}:${nextTarget?.id ?? ""}:${nextContext?.id ?? ""}`;
    setSearchParams(params, { replace: true });
  }, [scopeKey, setSearchParams]);

  const restoreDraft = (nextKey: string) => {
    const draft = draftStore.get(nextKey);
    setInput(draft?.text ?? "");
    setAttachments([]);
    savedDraftKeyRef.current = nextKey;
  };

  const saveDraft = (nextKey: string) => {
    draftStore.set(nextKey, {
      composerDraftId: requestState.composer.composerDraftId,
      text: input,
      selectionStart: inputRef.current?.selectionStart ?? input.length,
      selectionEnd: inputRef.current?.selectionEnd ?? input.length,
      source: nextKey,
    });
  };

  const cancelActive = useCallback((nextStatus: ConversationStatus = "CANCELLING") => {
    abortRef.current?.abort();
    if (abortRef.current) setStatus(nextStatus);
  }, []);

  const discardActiveRun = () => {
    abortRef.current?.abort();
    abortRef.current = null;
    activeRunRef.current = null;
    sinkRef.current = null;
  };

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      abortRef.current?.abort();
    };
  }, []);

  useEffect(() => {
    if (!scopeKey) return;
    const entries = recentStore.list();
    entries.forEach((entry) => rememberRecentTimestamp(entry.target, entry.lastUsedAt));
    setRecentTargets(entries);
  }, [recentStore, scopeKey]);

  useEffect(() => {
    if (!identity || !target) return;
    if (savedDraftKeyRef.current !== draftKey) return;
    saveDraft(draftKey);
  }, [attachments, draftKey, draftStore, identity, input, target]);

  const selectKind = (nextKind: ConversationKind) => {
    if (nextKind === kind) return;
    discardActiveRun();
    syncUrl(nextKind, null, null);
    saveDraft(draftKey);
    setKind(nextKind);
    setTarget(null);
    setContext(null);
    setMessages([]);
    setAgentSessionUnknown(false);
    setAttachments([]);
    resetContexts();
    resetTargets();
    setStatus("IDLE");
    restoreDraft(conversationKey(scopeKey, nextKind, "none", "new"));
  };

  const selectTarget = (nextTarget: ConversationTargetRef) => {
    discardActiveRun();
    const nextKind = nextTarget.kind;
    const stagedFiles = attachments.filter((item) => item.status === "staged" && item.file);
    syncUrl(nextKind, nextTarget, null);
    saveDraft(draftKey);
    setKind(nextKind);
    setTarget(nextTarget);
    setContext(null);
    setMessages([]);
    setAttachments([]);
    resetContexts();
    setStatus("READY");
    setMessageCursor(undefined);
    const entries = recentStore.touch(nextTarget);
    entries.forEach((entry) => rememberRecentTimestamp(entry.target, entry.lastUsedAt));
    setRecentTargets(entries);
    restoreDraft(conversationKey(scopeKey, nextKind, nextTarget.id, "new"));
    setAttachments(nextKind === "search" ? [] : stagedFiles);
    trackEvent("target_selected", {
      conversationKey: draftKey,
      target: nextTarget,
      attributes: { kind: nextKind },
    });
  };

  const selectContext = async (nextContext: ConversationContext, nextTargetOverride?: ConversationTargetRef): Promise<ConversationContext | null> => {
    const activeTarget = nextTargetOverride ?? target;
    if (!activeTarget) return null;
    const safeContext = assertConversationContext(nextContext);
    if (safeContext.targetId !== activeTarget.id || safeContext.kind !== activeTarget.kind) {
      setStatus("FAILED");
      notify(t("conversationCenter.context_load_failed"), { type: "error" });
      return null;
    }
    const activeKind = activeTarget.kind;
    const activeAdapter = adapters[activeKind];
    discardActiveRun();
    saveDraft(draftKey);
    setKind(activeKind);
    syncUrl(activeKind, activeTarget, safeContext);
    setContext(safeContext);
    restoreDraft(conversationKey(scopeKey, activeKind, activeTarget.id, safeContext.id));
    setStatus("LOADING_CONTEXT");
    setMessages([]);
    try {
      const snapshot = await activeAdapter.loadContext(safeContext);
      if (!mountedRef.current) return null;
      setAgentSessionUnknown(false);
      setMessages(snapshot.messages);
      setMessageCursor(snapshot.nextCursor);
      setStatus("READY");
      const firstQuestion = snapshot.messages.find((message) => message.role === "user" && message.content.trim())?.content.trim();
      if (safeContext.type === "session" && !safeContext.title && firstQuestion) {
        const titledContext = { ...safeContext, title: firstQuestion };
        setContext(titledContext);
        updateContext(titledContext);
      }
      focusInput();
      trackEvent("context_opened", {
        conversationKey: conversationKey(scopeKey, activeKind, activeTarget.id, safeContext.id),
        target: activeTarget,
        attributes: { kind: activeKind, contextId: safeContext.id, contextType: safeContext.type },
      });
    } catch (error) {
      setStatus("FAILED");
      notify(error instanceof ApiError ? error.displayMessage : t("conversationCenter.context_load_failed"), { type: "error" });
      return null;
    }
    return safeContext;
  };

  const chooseRouteCandidate = async (candidate: NonNullable<ReturnType<typeof useConversationRouter>["decision"]>["candidates"][number]) => {
    if (!router.decision || !requestState.getPending()) return;
    const routeTarget: ConversationTargetRef = {
      id: candidate.target_id,
      kind: candidate.kind,
      name: candidate.name,
      description: candidate.reasons.join(" · "),
      status: "active",
    };
    const routeDecision = router.decision;
    const stagedFiles = attachments.filter((item) => item.status === "staged" && item.file);
    const routeSelectionIdempotencyKey = createIdempotencyKey();
    const bootstrapIdempotencyKey = createIdempotencyKey();
    requestState.markPending({
      state: "binding-assistant",
      candidateId: candidate.id,
      candidateKind: candidate.kind,
      routeSelectionIdempotencyKey,
      bootstrapIdempotencyKey,
    });
    const selectOperation = requestState.beginOperation("select", { routeId: routeDecision.route_id, targetId: candidate.target_id });
    selectTarget(routeTarget);
    setAttachments(stagedFiles);
    router.reset();
    trackEvent("route_candidate_selected", {
      conversationKey: draftKey,
      target: routeTarget,
      attributes: { routeId: routeDecision.route_id, scoreStatus: routeDecision.score_status },
    });
    const result = await router.select(candidate, routeDecision.route_id, {
      routeSelectionIdempotencyKey,
      bootstrapIdempotencyKey,
      signal: selectOperation?.controller.signal,
    });
    if (
      !selectOperation
      || requestState.rejectOperation(selectOperation, { kind: "select", routeId: routeDecision.route_id, targetId: candidate.target_id })
      || !result
      || result.operation.kind !== candidate.kind
      || result.operation.target_id !== candidate.target_id
      || result.operation.route_selection_id !== result.routeSelectionId
      || result.operation.bootstrap_type !== "CREATE_SESSION"
      || !result.operation.bootstrap_operation_id
      || !result.operation.session_id
    ) {
      if (selectOperation) requestState.endOperation(selectOperation.operationId);
      requestState.failPending({
        message: router.error ?? t("conversationCenter.route_select_failed"),
        stage: "select",
        failureKind: "unknown",
        retryAuthorization: "none",
      });
      notify(router.error ?? t("conversationCenter.route_select_failed"), { type: "error" });
      return;
    }
    if (selectOperation) requestState.endOperation(selectOperation.operationId);
    requestState.markPending({
      routeSelectionId: result.routeSelectionId,
      bootstrapResourceId: result.operation.bootstrap_operation_id,
      bootstrapSessionId: result.operation.session_id,
      targetId: result.operation.target_id,
      targetKind: result.operation.kind,
      state: "loading-session",
    });
    const loadedContext = await selectContext({
      id: result.operation.session_id,
      targetId: result.operation.target_id,
      kind: result.operation.kind,
      type: "session",
    }, routeTarget);
    setAttachments(stagedFiles);
    if (loadedContext) {
      requestState.markPending({ contextId: loadedContext.id, bootstrapSessionId: loadedContext.id });
    }
    if (requestState.pending?.attachmentSnapshot.length) {
      requestState.markPending({
        state: "attachment-blocked",
        error: {
          message: t("conversationCenter.attachment_blocked"),
          stage: "upload",
          failureKind: "deterministic",
          retryAuthorization: "none",
        },
      });
      return;
    }
    void submitRef.current?.(requestState.getPending()?.textSnapshot, loadedContext ?? undefined, routeTarget);
    focusInput();
  };

  useEffect(() => {
    const activePending = requestState.pending;
    if (activePending?.state !== "countdown" || !activePending.countdownEndsAt) return;
    const delay = Math.max(0, Date.parse(activePending.countdownEndsAt) - Date.now());
    const timer = setTimeout(() => {
      const candidate = router.decision?.selected;
      if (candidate) void chooseRouteCandidate(candidate);
    }, delay);
    return () => clearTimeout(timer);
  }, [requestState.pending, router.decision]);

  useEffect(() => {
    const onVisibilityChange = () => {
      if (!document.hidden || requestState.pending?.state !== "countdown") return;
      requestState.markPending({ state: "awaiting-choice", countdownEndsAt: undefined });
    };
    document.addEventListener("visibilitychange", onVisibilityChange);
    return () => document.removeEventListener("visibilitychange", onVisibilityChange);
  }, [requestState]);

  const resetContext = async () => {
    discardActiveRun();
    if (kind === "agent") {
      if (!agentSessionCreate || !target || !adapters.agent.createSession) {
        notify(t("conversationCenter.agent_new_denied"), { type: "warning" });
        return;
      }
      try {
        const created = await adapters.agent.createSession(target.id);
        setAgentSessionUnknown(false);
        await selectContext(created);
      } catch (error) {
        if (error instanceof ApiError && error.status === 0) {
          setAgentSessionUnknown(true);
          notify(t("conversationCenter.agent_session_unknown"), { type: "warning" });
          return;
        }
        notify(error instanceof ApiError ? error.displayMessage : t("conversationCenter.agent_new_failed"), { type: "error" });
      }
      return;
    }
    saveDraft(draftKey);
    syncUrl(kind, target, null);
    setContext(null);
    setMessages([]);
    setStatus("READY");
    setMessageCursor(undefined);
    restoreDraft(conversationKey(scopeKey, kind, target?.id ?? "none", "new"));
    focusInput();
  };

  const urlKind = searchParams.get("kind");
  const urlTargetId = searchParams.get("targetId");
  const urlContextId = searchParams.get("contextId");
  const urlRestoreSignature = `${scopeKey}:${urlKind ?? ""}:${urlTargetId ?? ""}:${urlContextId ?? ""}`;

  useEffect(() => {
    if (!identity || identityLoading || activeRunRef.current) return;
    const kindAccess: Record<ConversationKind, boolean> = {
      chat: chatAccess === true,
      search: searchAccess === true,
      agent: agentAccess === true,
    };
    const requestedKind = urlKind === "chat" || urlKind === "search" || urlKind === "agent" ? urlKind : "chat";
    const clearContext = (message?: string) => {
      if (message) notify(message, { type: "warning" });
      const params = new URLSearchParams();
      params.set("kind", requestedKind);
      setSearchParams(params, { replace: true });
      if (urlTargetId) {
        const nextRecent = recentStore.invalidate(requestedKind, urlTargetId);
        setRecentTargets(nextRecent);
        draftStore.delete(conversationKey(scopeKey, requestedKind, urlTargetId, urlContextId ?? "new"));
        draftStore.delete(conversationKey(scopeKey, requestedKind, urlTargetId));
      }
      setTarget(null);
      setContext(null);
      setMessages([]);
      setStatus("IDLE");
      setMessageCursor(undefined);
    };

    if (!kindAccess[requestedKind] || !urlTargetId) {
      if (urlTargetId || urlContextId) clearContext();
      return;
    }
    if (urlRestoreRef.current === urlRestoreSignature) return;
    urlRestoreRef.current = urlRestoreSignature;
    let active = true;
    const controller = new AbortController();

    (async () => {
      try {
        const restoredTarget = await adapter.getTarget(urlTargetId, { signal: controller.signal });
        if (!active || restoredTarget.id !== urlTargetId) return;
        if (!isConversationTarget(restoredTarget)) {
          clearContext(t("conversationCenter.agent_type_denied"));
          return;
        }
        setTarget(restoredTarget);
        const entries = recentStore.touch(restoredTarget);
        entries.forEach((entry) => rememberRecentTimestamp(entry.target, entry.lastUsedAt));
        setRecentTargets(entries);
        if (requestedKind === "search" || !urlContextId) {
          setContext(null);
          setMessages([]);
          setMessageCursor(undefined);
          setStatus("READY");
          return;
        }
        const restoredContext = assertConversationContext({ id: urlContextId, targetId: urlTargetId, kind: requestedKind, type: "session" });
        setContext(restoredContext);
        setStatus("LOADING_CONTEXT");
        setMessages([]);
        const snapshot = await adapter.loadContext(restoredContext);
        if (!active) return;
        setMessages(snapshot.messages);
        setMessageCursor(snapshot.nextCursor);
        setStatus("READY");
      } catch (error) {
        if (!active || controller.signal.aborted) return;
        if (error instanceof ApiError && (error.status === 403 || error.status === 404)) {
          clearContext(t("conversationCenter.restore_invalid"));
        } else {
          notify(error instanceof ApiError ? error.displayMessage : t("conversationCenter.restore_failed"), { type: "error" });
        }
      }
    })();

    return () => {
      active = false;
      controller.abort();
    };
  }, [
    adapter,
    agentAccess,
    chatAccess,
    identity,
    identityLoading,
    notify,
    recentStore,
    searchAccess,
    setSearchParams,
    t,
    urlContextId,
    urlKind,
    urlRestoreSignature,
    urlTargetId,
  ]);

  useEffect(() => {
    if (!identity || identityLoading || target || urlTargetId || targetQuery || targetsLoading || status !== "IDLE") return;
    if (!accessibleKinds.some((item) => item.kind === kind)) return;
    const defaultKey = `${scopeKey}:${kind}`;
    if (defaultTargetKeyRef.current === defaultKey) return;

    const preferredRecent = recentTargets.find((item) => item.target.kind === kind)?.target;
    if (preferredRecent) {
      defaultTargetKeyRef.current = defaultKey;
      let active = true;
      const controller = new AbortController();
      adapter.getTarget(preferredRecent.id, { signal: controller.signal })
        .then((restoredTarget) => {
          if (!active || restoredTarget.kind !== kind) return;
          selectTarget(restoredTarget);
        })
        .catch((error) => {
          if (!active) return;
          if (error instanceof ApiError && (error.status === 403 || error.status === 404)) {
            const nextRecent = recentStore.invalidate(kind, preferredRecent.id);
            setRecentTargets(nextRecent);
            defaultTargetKeyRef.current = null;
          } else {
            notify(error instanceof ApiError ? error.displayMessage : t("conversationCenter.target_load_failed"), { type: "error" });
          }
        });
      return () => {
        active = false;
        controller.abort();
      };
    }

    if (targets.length > 0) {
      defaultTargetKeyRef.current = defaultKey;
      if (targets[0].kind === kind) selectTarget(targets[0]);
    }
  }, [
    accessibleKinds,
    adapter,
    identity,
    identityLoading,
    kind,
    notify,
    recentStore,
    recentTargets,
    selectTarget,
    status,
    scopeKey,
    t,
    target,
    targetQuery,
    targets,
    targetsLoading,
    urlTargetId,
  ]);

  const runCommand = (commandId: string) => {
    setHelpOpen(false);
    if (commandId === "help") {
      setHelpOpen(true);
      return;
    }
    if (commandId === "new") {
      resetContext();
      return;
    }
    selectKind(commandId as ConversationKind);
  };

  const chooseValidatedTarget = async (nextTarget: ConversationTargetRef) => {
    const activePending = requestState.getPending();
    const remainingText = trigger?.type === "mention"
      ? replaceInputToken(input, trigger).replace(/\s{2,}/g, " ").trim()
      : undefined;
    if (trigger?.type === "mention") {
      setInput((previous) => replaceInputToken(previous, trigger).replace(/\s{2,}/g, " "));
    }
    try {
      const liveTarget = await adapters[nextTarget.kind].getTarget(nextTarget.id);
      if (liveTarget.kind !== nextTarget.kind) throw new Error("Target kind mismatch");
      if (!isConversationTarget(liveTarget)) {
        const nextRecent = recentStore.invalidate(nextTarget.kind, nextTarget.id);
        setRecentTargets(nextRecent);
        inputRef.current?.focus();
        notify(t("conversationCenter.agent_type_denied"), { type: "warning" });
        return;
      }
      if (trigger?.type === "mention" && requestState.getPending()) {
        setInput((previous) => replaceInputToken(previous, trigger).replace(/\s{2,}/g, " "));
        inputRef.current?.focus();
        notify(t("conversationCenter.pending_conflict"), { type: "warning" });
        return;
      }
      selectTarget(liveTarget);
      inputRef.current?.focus();
      if (remainingText && !activePending) {
        try {
          requestState.createPending("mention-select");
          requestState.markPending({
            textSnapshot: remainingText,
            candidateId: liveTarget.id,
            candidateKind: liveTarget.kind === "agent" ? "agent" : "chat",
            targetId: liveTarget.id,
            targetKind: liveTarget.kind === "agent" ? "agent" : "chat",
          });
          setInput(remainingText);
          if (liveTarget.kind === "search") {
            void submitRef.current?.(remainingText, undefined, liveTarget);
            return;
          }
          if (liveTarget.kind === "agent" && !agentSessionCreate) {
            requestState.cancelPending();
            notify(t("conversationCenter.agent_new_denied"), { type: "warning" });
            return;
          }
          const loadSessionOperation = requestState.beginOperation("load-session", { targetId: liveTarget.id });
          try {
            const createdContext = await adapters[liveTarget.kind].createSession!(liveTarget.id);
            if (loadSessionOperation && requestState.rejectOperation(loadSessionOperation, { kind: "load-session", targetId: liveTarget.id })) {
              loadSessionOperation.controller.abort();
              return;
            }
            const loadedContext = await selectContext(createdContext, liveTarget);
            if (loadSessionOperation) requestState.endOperation(loadSessionOperation.operationId);
            if (!loadedContext || !requestState.getPending()) return;
            requestState.markPending({ contextId: loadedContext.id });
            void submitRef.current?.(remainingText, loadedContext, liveTarget);
          } catch (error) {
            if (loadSessionOperation) requestState.endOperation(loadSessionOperation.operationId);
            if (error instanceof ApiError && error.status === 0) {
              setAgentSessionUnknown(true);
              requestState.failPending({
                message: t("conversationCenter.agent_session_unknown"),
                stage: "load-session",
                failureKind: "unknown",
                retryAuthorization: "none",
              });
              notify(t("conversationCenter.agent_session_unknown"), { type: "warning" });
              return;
            }
            requestState.failPending({
              message: error instanceof ApiError ? error.displayMessage : t("conversationCenter.agent_new_failed"),
              stage: "load-session",
              failureKind: "deterministic",
              retryAuthorization: "none",
            });
            notify(error instanceof ApiError ? error.displayMessage : t("conversationCenter.agent_new_failed"), { type: "error" });
          }
        } catch {
          notify(t("conversationCenter.pending_conflict"), { type: "warning" });
        }
      }
    } catch (error) {
      if (error instanceof ApiError && (error.status === 403 || error.status === 404)) {
        const nextRecent = recentStore.invalidate(nextTarget.kind, nextTarget.id);
        setRecentTargets(nextRecent);
        inputRef.current?.focus();
        notify(t("conversationCenter.restore_invalid"), { type: "warning" });
        return;
      }
      inputRef.current?.focus();
      notify(error instanceof ApiError ? error.displayMessage : t("conversationCenter.target_load_failed"), { type: "error" });
    }
  };

  const addFiles = async (files: File[]) => {
    if (kind === "search") {
      notify(t("conversationCenter.attachment_blocked"), { type: "warning" });
      return;
    }
    setAttachments((previous) => [...previous, ...files.map((file): AttachmentDraft => ({
      id: createCspId("file"), name: file.name, size: file.size, mime: file.type,
      status: "staged", progress: 0, file,
    }))]);
    if (target) notify(t("conversationCenter.attachment_blocked"), { type: "warning" });
  };

  const patchMessage = (messageId: string, patch: (message: Message) => Message) => {
    setMessages((previous) => previous.map((message) => message.id === messageId || message.turnId === messageId ? patch(message) : message));
  };

  const submit = async (requestedText?: string, requestedContext?: ConversationContext, requestedTarget?: ConversationTargetRef) => {
    const pendingBeforePlainText = requestState.getPending();
    const attachmentBlocked = pendingBeforePlainText?.state === "attachment-blocked";
    const plainTextOverride = attachmentBlocked && requestedText !== undefined;
    if (plainTextOverride && requestedText?.trim()) {
      requestState.cancelPending();
    }
    const activePending = requestState.getPending();
    if (!identity || busy || (activePending && requestedText === undefined && !(attachmentBlocked && attachments.length === 0))) return;
    if (activePending?.state === "attachment-blocked" && attachments.length === 0) {
      requestState.cancelPending();
    }
    if (trigger?.type === "command" && commandOptions[0]) {
      runCommand(commandOptions[0].id);
      setInput("");
      return;
    }
    const rawInput = requestedText ?? input;
    let text = trigger?.type === "mention" && requestedText === undefined ? replaceInputToken(input, trigger).replace(/\s{2,}/g, " ").trim() : rawInput.trim();
    text = stripLeadingCommand(text);
    if (!text) return;
    const targetForSubmission = requestedTarget ?? target;
    if (!targetForSubmission) {
      try {
        requestState.createPending("direct-submit");
      } catch {
        return;
      }
      trackEvent("route_requested", { conversationKey: draftKey, attributes: { requestedMode: "suggest" } });
      const routeOperation = requestState.beginOperation("route");
      const decision = await router.route(text, routeOperation?.controller.signal);
      if (routeOperation) requestState.endOperation(routeOperation.operationId);
      if (!decision) {
        requestState.failPending({
          message: router.error ?? t("conversationCenter.route_failed"),
          stage: "route",
          failureKind: "unknown",
          retryAuthorization: "none",
        });
        return;
      }
      const autoSelected = routeAutoEligible(decision, attachments, "local-only");
      requestState.markPending({
        state: autoSelected ? "countdown" : "awaiting-choice",
        routeId: decision.route_id,
        routeDecisionRevision: activePending?.revision ?? 1,
        routeExpiresAt: decision.expires_at,
        countdownEndsAt: autoSelected ? new Date(Date.now() + 3000).toISOString() : undefined,
      });
      return;
    }
    if (targetForSubmission.kind === "agent" && (!context || context.type !== "session")) {
      notify(t("conversationCenter.agent_requires_context"), { type: "warning" });
      return;
    }
    if (attachments.length > 0 && !plainTextOverride) {
      if (!activePending) {
        requestState.createPending("direct-submit");
      }
      requestState.markPending({
        state: "attachment-blocked",
        error: {
          message: t("conversationCenter.attachment_blocked"),
          stage: "upload",
          failureKind: "deterministic",
          retryAuthorization: "none",
        },
      });
      notify(t("conversationCenter.attachment_blocked"), { type: "warning" });
      return;
    }
    if (!activePending) {
      requestState.createPending("direct-submit");
    }
    const readyAttachments: AttachmentDraft[] = [];
    const requestId = newConversationRunId();
    const runContext: ConversationContext | null = requestedContext ?? context ?? (kind === "search" ? { id: requestId, targetId: targetForSubmission.id, kind: "search", type: "run" } : null);
    const nextMessages: Message[] = [
      ...messages,
      { id: `user-${requestId}`, role: "user", content: text, kind, createdAt: new Date().toISOString(), files: readyAttachments.map((item) => item.meta!) },
      { id: `assistant-${requestId}`, turnId: `assistant-${requestId}`, role: "assistant", content: "", kind, status: "streaming", createdAt: new Date().toISOString() },
    ];
    setMessages(nextMessages);
    setInput("");
    setAttachments([]);
    setLastSubmission({ text, context: runContext });
    trackEvent("message_submitted", {
      conversationKey: draftKey,
      target: targetForSubmission,
      runId: requestId,
      requestId,
      attributes: { kind, textLength: text.length, attachmentCount: readyAttachments.length },
    });
    setStatus("PREPARING");
    requestState.markPending({
      state: "submitting",
      submitRequestId: requestId,
      targetId: targetForSubmission.id,
      targetKind: targetForSubmission.kind,
      contextId: runContext?.id,
    });
    const activeRun: ActiveRun = {
      runId: requestId,
      requestId,
      streamId: `stream-${requestId}`,
      conversationKey: draftKey,
      context: runContext,
    };
    const sink = new ConversationEventSink();
    sinkRef.current = sink;
    activeRunRef.current = activeRun;
    let streamConnected = false;
    try {
      const stream = adapter.start({ target: targetForSubmission, context: runContext ?? undefined, text, attachments: readyAttachments, requestId, conversationKey: draftKey });
      abortRef.current = stream;
      trackEvent("run_started", {
        conversationKey: draftKey,
        target: targetForSubmission,
        runId: requestId,
        requestId,
        attributes: { kind, contextId: runContext?.id ?? "" },
      });
      for await (const event of stream.events) {
        if (activeRunRef.current !== activeRun) break;
        if (event.target.id !== targetForSubmission.id || event.target.kind !== targetForSubmission.kind) {
          abortRef.current?.abort();
          requestState.failPending({
            message: t("conversationCenter.route_select_failed"),
            stage: "stream",
            failureKind: "unknown",
            retryAuthorization: "none",
          });
          break;
        }
        if (!streamConnected) {
          streamConnected = true;
          setStatus("STREAMING");
          requestState.markPending({ state: "stream-connected" });
        }
        if (event.type !== "stream.completed") {
          requestState.markPending({ state: "executing" });
        }
        sink.push(event, activeRun);
        if (event.context) {
          const eventContext = event.context;
          const contextChanged = activeRun.context?.id !== event.context.id;
          const previousTitle = activeRun.context ? contextTitle(activeRun.context) : undefined;
          const nextContext = eventContext.type === "run"
            ? eventContext
            : { ...eventContext, title: contextTitle(eventContext) || previousTitle || text, ...(eventContext.updatedAt ? {} : { updatedAt: Date.now() }) };
          setContext(nextContext);
          updateContext(nextContext);
          activeRun.context = nextContext;
          if (contextChanged) syncUrl(kind, targetForSubmission, nextContext);
        }
        if (event.type === "message.started") {
          patchMessage(`assistant-${requestId}`, (message) => ({ ...message, id: event.messageId, turnId: event.messageId }));
        } else if (event.type === "message.delta") {
          patchMessage(event.messageId, (message) => ({ ...message, content: message.content + event.delta }));
        } else if (event.type === "citation.added") {
          patchMessage(event.messageId, (message) => ({
            ...message,
            citations: [...(message.citations ?? []).filter((item) => !(item.name === event.citation.name && item.content === event.citation.content)), event.citation],
          }));
        } else if (event.type === "message.completed") {
          patchMessage(event.messageId, (message) => ({ ...message, status: "completed" }));
        } else if (event.type === "usage.updated") {
          patchMessage(event.messageId ?? `assistant-${requestId}`, (message) => ({ ...message, usage: event.usage }));
        } else if (event.type === "error.occurred") {
          if (event.error.failureKind === "unknown") {
            requestState.markPending({
              state: "execution-unknown",
              error: {
                code: event.error.errorCode,
                message: event.error.displayMessage,
                stage: event.context ? "stream" : "submit",
                failureKind: "unknown",
                retryAuthorization: "none",
              },
            });
          } else {
            requestState.failPending({
              code: event.error.errorCode,
              message: event.error.displayMessage,
              stage: "stream",
              failureKind: "deterministic",
              retryAuthorization: event.error.retryAuthorization,
            });
          }
          patchMessage(event.messageId ?? `assistant-${requestId}`, (message) => ({ ...message, status: "failed", error: event.error.displayMessage, errorCode: event.error.errorCode }));
        } else if (event.type === "cancelled") {
          patchMessage(event.messageId ?? `assistant-${requestId}`, (message) => ({ ...message, status: "cancelled" }));
        } else if (event.type === "stream.completed") {
          const finalStatus: RunFinalStatus = event.finalStatus;
          setStatus(finalStatus === "completed" ? "COMPLETED" : finalStatus === "failed" ? "FAILED" : "CANCELLED");
          patchMessage(`assistant-${requestId}`, (message) => ({
            ...message,
              status: finalStatus === "completed" ? "completed" : finalStatus,
          }));
          trackEvent(
            finalStatus === "completed" ? "run_completed" : finalStatus === "failed" ? "run_failed" : "run_cancelled",
            {
              conversationKey: draftKey,
              target: targetForSubmission,
              runId: requestId,
              requestId,
              traceId: event.traceId,
              attributes: { kind, finalStatus },
            },
          );
          if (finalStatus === "completed") {
            requestState.completePending();
          } else if (finalStatus === "failed"
            && requestState.getPending()?.state !== "failed"
            && requestState.getPending()?.state !== "execution-unknown") {
            requestState.failPending({
              message: t("conversationCenter.run_failed"),
              stage: "stream",
              failureKind: "unknown",
              retryAuthorization: "none",
            }, "execution-unknown");
          } else if (finalStatus === "cancelled"
            && requestState.getPending()?.state !== "failed"
            && requestState.getPending()?.state !== "execution-unknown") {
            requestState.cancelPending();
          }
        }
      }
    } catch (error) {
      const message = error instanceof ApiError ? error.displayMessage : error instanceof Error ? error.message : String(error);
      trackEvent("run_failed", {
        conversationKey: draftKey,
        target: targetForSubmission,
        runId: requestId,
        requestId,
        attributes: { kind },
      });
      setStatus("FAILED");
      if (error instanceof DOMException && error.name === "TimeoutError") {
        requestState.failPending({
          message,
          stage: "submit",
          failureKind: "unknown",
          retryAuthorization: "none",
        });
      } else {
        requestState.failPending({
          message,
          stage: "submit",
          failureKind: "deterministic",
          retryAuthorization: "none",
        });
      }
      patchMessage(`assistant-${requestId}`, (item) => ({ ...item, status: "failed", error: message, errorCode: "model_failure" }));
      notify(message, { type: "error" });
    } finally {
      abortRef.current = null;
      activeRunRef.current = null;
    }
  };
  submitRef.current = submit;

  const retry = () => {
    const activePending = requestState.getPending();
    if (!lastSubmission || busy || (activePending && activePending.error?.retryAuthorization !== "safe-retry")) return;
    trackEvent("retry_clicked", {
      conversationKey: draftKey,
      target: target ?? undefined,
      attributes: { kind },
    });
    void submit(lastSubmission.text);
  };

  const retryMessage = (turnId: string) => {
    const activePending = requestState.getPending();
    if (busy || (activePending && activePending.error?.retryAuthorization !== "safe-retry")) return;
    const failedIndex = messages.findIndex((message) => message.id === turnId || message.turnId === turnId);
    if (failedIndex < 0) {
      retry();
      return;
    }
    const userMessage = [...messages.slice(0, failedIndex)].reverse().find((message) => message.role === "user");
    trackEvent("retry_clicked", {
      conversationKey: draftKey,
      target: target ?? undefined,
      attributes: { kind, messageId: turnId },
    });
    void submit(userMessage?.content ?? lastSubmission?.text);
  };

  const loadEarlierMessages = async () => {
    if (!context || !messageCursor || busy) return;
    try {
      const page = await adapter.loadMoreMessages(context, messageCursor);
      setMessages((previous) => [...page.items, ...previous]);
      setMessageCursor(page.nextCursor);
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("conversationCenter.context_load_failed"), { type: "error" });
    }
  };

  const onRate = async (turnId: string, rating: "positive" | "negative") => {
    if (!target || kind !== "chat" || !context || context.type !== "session") return;
    try {
      await api.post("/chat/feedback", {
        chat_id: target.id,
        session_id: context.id,
        message_id: turnId,
        request_id: feedbackRequestId(turnId),
        rating,
      });
      patchMessage(turnId, (message) => ({ ...message, feedback: rating }));
      trackEvent("feedback_submitted", {
        conversationKey: draftKey,
        target,
        attributes: { kind, rating, messageId: turnId },
      });
      notify(t("conversationCenter.feedback_saved"), { type: "success" });
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("conversationCenter.feedback_failed"), { type: "error" });
    }
  };

  const createKnowledgeTask = async (turnId: string) => {
    if (!identity?.id) return;
    const requestId = feedbackRequestId(turnId);
    const assistantIndex = messages.findIndex((message) => message.id === turnId || message.turnId === turnId);
    const question = [...messages.slice(0, assistantIndex < 0 ? messages.length : assistantIndex)]
      .reverse()
      .find((message) => message.role === "user")?.content;
    const failedMessage = assistantIndex >= 0 ? messages[assistantIndex] : undefined;
    const description = [
      question ? `问题摘要：${question}` : "",
      failedMessage?.feedback === "negative" ? "反馈类型：negative" : "反馈类型：no_answer",
      failedMessage?.content ? `回答摘要：${failedMessage.content.slice(0, 500)}` : "",
      failedMessage?.traceId ? `Trace ID：${failedMessage.traceId}` : "",
    ].filter(Boolean).join("\n");
    try {
      await api.post("/knowledge-tasks", {
        source_request_id: requestId,
        title: question ? `跟进：${question.slice(0, 80)}` : `跟进无答案或负面反馈 ${requestId}`,
        description,
        category: "knowledge",
        owner_id: identity.id,
        priority: "medium",
      });
      trackEvent("knowledge_task_created", {
        conversationKey: draftKey,
        target: target ?? undefined,
        attributes: { kind, requestId, messageId: turnId },
      });
      notify(t("conversationCenter.knowledge_task_created"), { type: "success" });
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("conversationCenter.knowledge_task_failed"), { type: "error" });
    }
  };

  const handleKeyDown = (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (composingRef.current) return;
    if (paletteOpen) {
      if (event.key === "ArrowDown" || event.key === "ArrowUp") {
        event.preventDefault();
        const count = trigger?.type === "command" ? commandOptions.length : targetOptions.length;
        const nextIndex = (activeOption.current + (event.key === "ArrowDown" ? 1 : count - 1)) % Math.max(1, count);
        activeOption.current = nextIndex;
        setActiveOptionIndex(nextIndex);
        return;
      }
      if (event.key === "Enter") {
        event.preventDefault();
        if (trigger?.type === "command") runCommand(commandOptions[activeOptionIndex]?.id ?? commandOptions[0]?.id ?? "");
        else if (targetOptions[activeOptionIndex]) void chooseValidatedTarget(targetOptions[activeOptionIndex]);
        return;
      }
      if (event.key === "Tab") {
        event.preventDefault();
        if (trigger?.type === "command" && commandOptions[activeOptionIndex]) setInput(replaceInputToken(input, trigger, commandOptions[activeOptionIndex].label));
        return;
      }
      if (event.key === "Escape") {
        event.preventDefault();
        if (trigger) setInput((previous) => replaceInputToken(previous, trigger));
        return;
      }
    }
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      void submit();
    }
  };

  if (identityLoading) {
    return <div className="flex h-full items-center justify-center text-muted-foreground"><Loader2 className="mr-2 size-4 animate-spin" />{t("conversationCenter.loading")}</div>;
  }

  if (accessibleKinds.length === 0) {
    return <div className="p-10 text-muted-foreground">{t("conversationCenter.no_access")}</div>;
  }

  return (
    <div className="h-full min-h-0 overflow-hidden p-4">
      <ConversationShell
        className="h-full max-md:flex-col"
        messages={messages}
        busy={busy}
        emptyText={target ? t("conversationCenter.empty") : ""}
        messagesAriaLabel={t("conversationCenter.title")}
        sidebarClassName="max-md:h-[60vh] max-md:overflow-y-auto shrink-0 border-b pr-3 md:w-80 md:border-b-0 md:border-r"
        beforeMessages={!target ? <AssistantEmptyState /> : undefined}
        sidebar={(
          <div className="flex h-full min-h-0 flex-col gap-3">
            <div className="grid shrink-0 grid-cols-2 gap-1" role="group" aria-label={t("conversationCenter.title")}>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    size="icon"
                    variant={kind === "chat" || kind === "agent" ? "default" : "outline"}
                    className="h-9 w-full"
                    disabled={!chatAccess && !agentAccess}
                    aria-label={t("conversationCenter.tab_unified")}
                    aria-pressed={kind === "chat" || kind === "agent"}
                    title={kind === "agent" ? t("conversationCenter.kind_agent") : t("conversationCenter.kind_chat")}
                    onClick={() => selectKind(chatAccess ? "chat" : "agent")}
                  >
                    <MessageSquare className="size-4" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>{t("conversationCenter.tab_unified")}</TooltipContent>
              </Tooltip>
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    size="icon"
                    variant={kind === "search" ? "default" : "outline"}
                    className="h-9 w-full"
                    disabled={!searchAccess}
                    aria-label={t("conversationCenter.kind_search")}
                    aria-pressed={kind === "search"}
                    title={t("conversationCenter.kind_search")}
                    onClick={() => selectKind("search")}
                  >
                    <Search className="size-4" />
                  </Button>
                </TooltipTrigger>
                <TooltipContent>{t("conversationCenter.kind_search")}</TooltipContent>
              </Tooltip>
            </div>
            {kind === "search" && (
              <p className="rounded border bg-muted/40 px-2 py-1 text-xs text-muted-foreground">{t("conversationCenter.search_ephemeral")}</p>
            )}
            <div className="relative shrink-0">
              <Search className="absolute left-2 top-2.5 size-4 text-muted-foreground" />
              <Input value={targetQuery} onChange={(event) => setTargetQuery(event.target.value)} placeholder={t("conversationCenter.search_targets")} aria-label={t("conversationCenter.search_targets")} className="pl-8" />
              {targetsLoading && <Loader2 className="absolute right-2 top-2 size-4 animate-spin text-muted-foreground" />}
            </div>
            <div className="min-h-0 flex-1 space-y-3 overflow-y-auto pr-1">
            {recentTargets.length > 0 && (
              <div className="space-y-1">
                <div className="text-xs font-medium text-muted-foreground">{t("conversationCenter.recent")}</div>
                {recentTargets.map((entry) => (
                  <button
                    key={`${entry.target.kind}:${entry.target.id}`}
                    type="button"
                    aria-current={target?.id === entry.target.id && target?.kind === entry.target.kind}
                    className={`w-full rounded px-2 py-1.5 text-left text-sm hover:bg-muted ${target?.id === entry.target.id && target?.kind === entry.target.kind ? "bg-muted" : ""}`}
                    onClick={() => void chooseValidatedTarget(entry.target)}
                  >
                    <div className="truncate">{entry.displayName}</div>
                    <div className="truncate text-xs text-muted-foreground">{t(`conversationCenter.kind_${entry.target.kind}`)}</div>
                    <div className="text-xs text-muted-foreground">{new Date(entryTimestamp(entry.target)).toLocaleString()}</div>
                  </button>
                ))}
              </div>
            )}
              <div className="text-xs font-medium text-muted-foreground">
                {targetQuery.trim() ? t("conversationCenter.search_results") : t("conversationCenter.available_targets")}
              </div>
              {targets.map((item) => (
                <button
                  key={`${item.kind}:${item.id}`}
                  type="button"
                  aria-current={target?.id === item.id && target?.kind === item.kind}
                  className={`w-full rounded px-2 py-2 text-left hover:bg-muted ${target?.id === item.id && target?.kind === item.kind ? "bg-muted" : ""}`}
                  onClick={() => void chooseValidatedTarget(item)}
                >
                  <TargetMeta target={item} translate={t} />
                </button>
              ))}
              {!targetsLoading && targets.length === 0 && <div className="p-2 text-sm text-muted-foreground">{t("conversationCenter.no_targets")}</div>}
              {kind === "search" && searchTargetCursor && (
                <Button size="sm" variant="ghost" className="mt-1 w-full" disabled={targetsLoading} onClick={() => void loadMoreSearchTargets()}>
                  {targetsLoading ? <Loader2 className="size-4 animate-spin" /> : t("conversationCenter.load_more")}
                </Button>
              )}
            </div>
            {target && (
              <div className="shrink-0 rounded border bg-muted/30 p-2">
                <div className="text-xs font-medium text-muted-foreground">{t("conversationCenter.current_target")}</div>
                <div className="truncate text-base font-semibold">{target.name}</div>
                <div className="truncate text-xs text-muted-foreground">{targetSubtitle(target, t)}</div>
                {target.knowledgeScope?.length ? (
                  <div className="mt-1 truncate text-xs text-muted-foreground">
                    {t("conversationCenter.knowledge_scope")}
                    {target.knowledgeScope.slice(0, 2).join("、")}
                    {target.knowledgeScope.length > 2 ? ` +${target.knowledgeScope.length - 2}` : ""}
                  </div>
                ) : null}
              </div>
            )}
            {target && kind !== "search" && (
              <div className="flex min-h-0 flex-1 flex-col border-t pt-2">
                <div className="mb-1 flex items-center justify-between gap-2">
                  <span className="text-xs font-medium text-muted-foreground">{t("conversationCenter.contexts")}</span>
                  <Button
                    size="sm"
                    variant={context ? "outline" : "default"}
                    disabled={kind === "agent" && (!agentSessionCreate || !adapters.agent.createSession)}
                    aria-current={!context}
                    onClick={() => void resetContext()}
                  >
                    {t("conversationCenter.new_context")}
                  </Button>
                </div>
                <div className="min-h-0 flex-1 space-y-2 overflow-y-auto">
                {contextsLoading ? <Loader2 className="size-4 animate-spin" /> : CONTEXT_TIME_GROUPS.map(({ key, labelKey }) => {
                  const group = groupedContexts[key];
                  if (!group.length) return null;
                  return (
                    <div key={key} className="space-y-1">
                      <div className="text-xs font-medium text-muted-foreground">{t(labelKey)}</div>
                      {group.map((item) => (
                        <button
                          key={item.id}
                          type="button"
                          aria-current={context?.id === item.id}
                          className={`w-full rounded px-2 py-1 text-left text-sm hover:bg-muted ${context?.id === item.id ? "bg-muted" : ""}`}
                          onClick={() => void selectContext(item)}
                        >
                          <div className="truncate">{contextTitle(item) || t("conversationCenter.unnamed_context")}</div>
                          {item.updatedAt ? (
                            <div className="text-xs text-muted-foreground">{new Date(item.updatedAt).toLocaleString()}</div>
                          ) : null}
                        </button>
                      ))}
                    </div>
                  );
                })}
                {contextCursor && (
                  <Button size="sm" variant="ghost" className="mt-1 w-full" disabled={contextsLoading} onClick={() => void loadMoreContexts()}>
                    {contextsLoading ? <Loader2 className="size-4 animate-spin" /> : t("conversationCenter.load_more")}
                  </Button>
                )}
                </div>
              </div>
            )}
            <CrossAppSessionIndex className="shrink-0" />
          </div>
        )}
        toolbar={(
          <>
            <AssistantHeader
              target={target}
              status={status}
              busy={busy}
              kind={kind}
              context={context}
              messages={messages}
              exportDisabled={capabilities.export === false}
              onHelpToggle={() => setHelpOpen((previous) => !previous)}
              onExport={(format) => trackEvent("export_clicked", { conversationKey: draftKey, target: target ?? undefined, attributes: { kind, format } })}
            />
            {(capabilities.attachments || (!target && (chatAccess || agentAccess))) && (
              <input ref={fileInputRef} type="file" multiple className="hidden" onChange={(event) => {
                const files = Array.from(event.target.files ?? []);
                if (files.length) void addFiles(files);
                event.target.value = "";
              }} />
            )}
          </>
        )}
        afterMessages={(
          <>
            {messageCursor && !busy && (
              <div className="mb-2 flex justify-center">
                <Button size="sm" variant="outline" onClick={() => void loadEarlierMessages()}>{t("conversationCenter.load_earlier")}</Button>
              </div>
            )}
            {helpOpen && (
              <div className="rounded border bg-muted/30 p-3 text-sm">
                <div className="mb-1 font-medium">{t("conversationCenter.help_title")}</div>
                <div>{t("conversationCenter.help_body")}</div>
              </div>
            )}
            {requestState.pending?.state === "attachment-blocked" ? (
              <div role="alert" className="mb-2 flex flex-wrap items-center justify-between gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-sm">
                <span>{t("conversationCenter.attachment_blocked_hint")}</span>
                <Button type="button" size="sm" variant="outline" onClick={() => {
                  const text = input.trim();
                  if (text) void submit(text);
                }}>
                  {t("conversationCenter.attachment_send_plain_text")}
                </Button>
              </div>
            ) : null}
            {router.decision && ((!target && router.status !== "routing") || router.status === "selecting" || router.status === "bootstrapping") ? (
              <RouteDecisionCard
                decision={router.decision}
                status={router.status}
                autoEligible={routeAutoEligible(router.decision, requestState.getPending()?.attachmentSnapshot ?? requestState.composer.attachments, "local-only")}
                error={router.error}
                onChoose={(candidate) => void chooseRouteCandidate(candidate)}
                onDismiss={router.reset}
                translate={t}
              />
            ) : null}
            {!target && router.status === "failed" && !router.decision && router.error ? (
              <p role="alert" className="rounded-md border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">{router.error}</p>
            ) : null}
          </>
        )}
        input={(
          <ConversationCenterInput
            input={input}
            inputRef={inputRef}
            attachments={attachments}
            paletteOpen={paletteOpen}
            paletteId={paletteId}
            activeOptionIndex={activeOptionIndex}
            activeOptionId={activeOptionId}
            commandOptions={commandOptions}
            targetOptions={targetOptions}
            disabled={agentContextMissing}
            busy={busy}
            canAttach={capabilities.attachments || (!target && (chatAccess || agentAccess))}
            onAttachClick={() => fileInputRef.current?.click()}
            onChange={(value) => {
              setInput(value);
              activeOption.current = 0;
              setActiveOptionIndex(0);
            }}
            onKeyDown={handleKeyDown}
            onCompositionStart={() => {
              composingRef.current = true;
              setComposing(true);
            }}
            onCompositionEnd={() => {
              composingRef.current = false;
              setComposing(false);
              activeOption.current = 0;
            }}
            onRemoveAttachment={(id) => setAttachments((previous) => previous.filter((file) => file.id !== id))}
            onRunCommand={runCommand}
            onChooseTarget={(target) => void chooseValidatedTarget(target)}
            onCancel={() => cancelActive()}
            onSubmit={() => void submit()}
          />
        )}
        onRate={kind === "chat" ? onRate : undefined}
        onCreateKnowledgeTask={kind === "chat" || kind === "agent" ? (turnId) => void createKnowledgeTask(turnId) : undefined}
        onRegenerate={retryMessage}
        onOpenCitation={(citation) => {
          setOpenCitation(citation);
          if (citation) trackEvent("citation_clicked", {
            conversationKey: draftKey,
            target: target ?? undefined,
            attributes: { kind, citationName: citation.name },
          });
        }}
      />
      <ConversationCitationViewer citation={openCitation} onCitationChange={setOpenCitation} documentEnabled={chatRead} />
    </div>
  );
}
