import { useCallback, useRef, useState } from "react";
import { api, ApiError } from "../../lib/api";
import { createIdempotencyKey } from "./use-pending-request";
import {
  type ConversationBootstrapOperation,
  type ConversationRouteCandidate,
  type ConversationRouteDecision,
  type ConversationRouteSelection,
  type ConversationRouteStatus,
} from "./types";

export interface RouteSelectionContext {
  routeSelectionId?: string;
  routeSelectionIdempotencyKey?: string;
  bootstrapIdempotencyKey?: string;
  signal?: AbortSignal;
}

export interface RouteSelectionResult {
  operation: ConversationBootstrapOperation;
  routeSelectionId: string;
  routeSelectionIdempotencyKey: string;
  bootstrapIdempotencyKey: string;
}

interface UseConversationRouterOptions {
  enabled: boolean;
  notify: (message: string, options: { type: "error" | "warning" }) => void;
  translate: (key: string) => string;
}

export function useConversationRouter({ enabled, translate }: UseConversationRouterOptions) {
  const [decision, setDecision] = useState<ConversationRouteDecision | null>(null);
  const [status, setStatus] = useState<ConversationRouteStatus>("idle");
  const [error, setError] = useState<string | null>(null);
  const controllerRef = useRef<AbortController | null>(null);

  const route = useCallback(async (query: string, signal?: AbortSignal) => {
    const text = query.trim();
    if (!enabled || !text) return null;
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    setStatus("routing");
    setError(null);
    try {
      const response = await api.post<{ code: number; data?: ConversationRouteDecision; message?: string }>(
        "/conversation/route",
        { query: text, requested_mode: "suggest" },
        { signal: signal ?? controller.signal },
      );
      if (!response.data || response.data.code !== 0 || !response.data.data) {
        throw new ApiError(response.status, response.data?.code ?? 0, response.data?.message ?? "route request failed");
      }
      const nextDecision = response.data.data;
      setDecision(nextDecision);
      setStatus(nextDecision.candidates.length ? "suggest" : "clarify");
      return nextDecision;
    } catch (caught) {
      if (caught instanceof DOMException && caught.name === "AbortError") return null;
      setDecision(null);
      setStatus("failed");
      setError(caught instanceof ApiError ? caught.displayMessage : translate("conversationCenter.route_failed"));
      return null;
    }
  }, [enabled, translate]);

  const reset = useCallback(() => {
    controllerRef.current?.abort();
    controllerRef.current = null;
    setDecision(null);
    setStatus("idle");
    setError(null);
  }, []);

  const select = useCallback(async (
    candidate: ConversationRouteCandidate,
    routeId: string,
    context: RouteSelectionContext = {},
  ): Promise<RouteSelectionResult | null> => {
    if (!routeId) return null;
    const selectionKey = context.routeSelectionIdempotencyKey ?? createIdempotencyKey();
    const bootstrapKey = context.bootstrapIdempotencyKey ?? createIdempotencyKey();
    setStatus("selecting");
    setError(null);
    let routeSelectionId = context.routeSelectionId;
    try {
      if (!routeSelectionId) {
        const selectionResponse = await api.post<{ code: number; data?: ConversationRouteSelection; message?: string }>(
          `/conversation/route/${encodeURIComponent(routeId)}/select`,
          { candidate_id: candidate.target_id, candidate_kind: candidate.kind },
          { headers: { "Idempotency-Key": selectionKey }, signal: context.signal },
        );
        const selection = selectionResponse.data?.data;
        if (!selection || selectionResponse.data?.code !== 0) {
          throw new ApiError(selectionResponse.status, selectionResponse.data?.code ?? 0, selectionResponse.data?.message ?? "selection failed");
        }
        if (selection.state === "FAILED" || selection.state === "EXPIRED") {
          throw new ApiError(410, 41092, "route selection is no longer usable");
        }
        routeSelectionId = selection.route_selection_id;
      }
      const deadline = Date.now() + 15_000;
      for (;;) {
        const bootstrapResponse = await api.post<{ code: number; data?: ConversationBootstrapOperation; message?: string }>(
          `/conversation/route-selections/${encodeURIComponent(routeSelectionId)}/bootstrap`,
          {},
          { headers: { "Idempotency-Key": bootstrapKey }, signal: context.signal },
        );
        const operation = bootstrapResponse.data?.data;
        if (operation && bootstrapResponse.data?.code === 0
          && (operation.bootstrap_state === "SUCCEEDED" || operation.selection_state === "CONSUMED")) {
          reset();
          return {
            operation,
            routeSelectionId,
            routeSelectionIdempotencyKey: selectionKey,
            bootstrapIdempotencyKey: bootstrapKey,
          };
        }
        if (operation && (operation.bootstrap_state === "PENDING" || operation.bootstrap_state === "RESERVED") && Date.now() < deadline) {
          await new Promise((resolve) => setTimeout(resolve, 500));
          continue;
        }
        throw new ApiError(
          bootstrapResponse.status,
          bootstrapResponse.data?.code ?? 0,
          bootstrapResponse.data?.message ?? "bootstrap did not complete",
        );
      }
    } catch (caught) {
      setStatus("suggest");
      setError(caught instanceof ApiError ? caught.displayMessage : translate("conversationCenter.route_select_failed"));
      return null;
    }
  }, [reset, translate]);

  return { decision, status, error, route, reset, select };
}
