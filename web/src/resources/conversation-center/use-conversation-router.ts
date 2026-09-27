import { useCallback, useEffect, useRef, useState } from "react";
import { api, ApiError } from "../../lib/api";
import {
  type ConversationBootstrapOperation,
  type ConversationRouteCandidate,
  type ConversationRouteDecision,
  type ConversationRouteSelection,
  type ConversationRouteStatus,
} from "./types";

function routeIdempotencyKey() {
  return `rgx-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 12)}`;
}

interface UseConversationRouterOptions {
  enabled: boolean;
  notify: (message: string, options: { type: "error" | "warning" }) => void;
  translate: (key: string) => string;
  onAutoSelected?: (operation: ConversationBootstrapOperation) => void;
}

export function useConversationRouter({ enabled, notify, translate, onAutoSelected }: UseConversationRouterOptions) {
  const [decision, setDecision] = useState<ConversationRouteDecision | null>(null);
  const [status, setStatus] = useState<ConversationRouteStatus>("idle");
  const [error, setError] = useState<string | null>(null);
  const controllerRef = useRef<AbortController | null>(null);
  const autoRouteAttemptedRef = useRef<string | null>(null);

  const route = useCallback(async (query: string) => {
    const text = query.trim();
    if (!enabled || !text) return;
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    setStatus("routing");
    setError(null);
    try {
      const response = await api.post<{ code: number; data?: ConversationRouteDecision; message?: string }>(
        "/conversation/route",
        { query: text, requested_mode: "suggest" },
        { signal: controller.signal },
      );
      if (!response.data || response.data.code !== 0 || !response.data.data) {
        throw new ApiError(response.status, response.data?.code ?? 0, response.data?.message ?? "route request failed");
      }
      const decision = response.data.data;
      setDecision(decision);
      setStatus(decision.candidates.length ? "suggest" : "clarify");
    } catch (caught) {
      if (caught instanceof DOMException && caught.name === "AbortError") return;
      setDecision(null);
      setStatus("failed");
      setError(caught instanceof ApiError ? caught.displayMessage : translate("conversationCenter.route_failed"));
    }
  }, [enabled, translate]);

  const reset = useCallback(() => {
    controllerRef.current?.abort();
    controllerRef.current = null;
    autoRouteAttemptedRef.current = null;
    setDecision(null);
    setStatus("idle");
    setError(null);
  }, []);

  const select = useCallback(async (candidate: ConversationRouteCandidate, routeId: string) => {
    if (!routeId) return null;
    setStatus("selecting");
    setError(null);
    try {
      const selectionResponse = await api.post<{ code: number; data?: ConversationRouteSelection; message?: string }>(
        `/conversation/route/${encodeURIComponent(routeId)}/select`,
        { candidate_id: candidate.target_id, candidate_kind: candidate.kind },
        { headers: { "Idempotency-Key": routeIdempotencyKey() } },
      );
      const selection = selectionResponse.data?.data;
      if (!selection || selectionResponse.data?.code !== 0) {
        throw new ApiError(selectionResponse.status, selectionResponse.data?.code ?? 0, selectionResponse.data?.message ?? "selection failed");
      }
      setStatus("bootstrapping");
      const bootstrapResponse = await api.post<{ code: number; data?: ConversationBootstrapOperation; message?: string }>(
        `/conversation/route-selections/${encodeURIComponent(selection.route_selection_id)}/bootstrap`,
        {},
        { headers: { "Idempotency-Key": routeIdempotencyKey() } },
      );
      const operation = bootstrapResponse.data?.data;
      if (!operation || bootstrapResponse.data?.code !== 0 || bootstrapResponse.data?.data?.bootstrap_state !== "SUCCEEDED") {
        throw new ApiError(bootstrapResponse.status, bootstrapResponse.data?.code ?? 0, bootstrapResponse.data?.message ?? "bootstrap failed");
      }
      reset();
      return operation;
    } catch (caught) {
      setStatus("suggest");
      setError(caught instanceof ApiError ? caught.displayMessage : translate("conversationCenter.route_select_failed"));
      return null;
    }
  }, [reset, translate]);

  const onAutoSelectedRef = useRef(onAutoSelected);
  onAutoSelectedRef.current = onAutoSelected;

  useEffect(() => {
    if (decision?.selected && status === "suggest") {
      const autoKey = `${decision.route_id}:${decision.selected.target_id}`;
      if (autoRouteAttemptedRef.current === autoKey) return;
      autoRouteAttemptedRef.current = autoKey;
      void select(decision.selected, decision.route_id).then((operation) => {
        if (operation && onAutoSelectedRef.current) {
          onAutoSelectedRef.current(operation);
        }
      });
    }
  }, [decision, select, status]);

  return { decision, status, error, route, reset, select };
}
