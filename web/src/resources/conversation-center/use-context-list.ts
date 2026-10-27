import { useCallback, useEffect, useMemo, useState } from "react";
import { ApiError } from "../../lib/api";
import type { ConversationAdapter, ConversationContext, ConversationTargetRef } from "./types";

const CONTEXT_PAGE_LIMIT = 20;

interface UseContextListOptions {
  adapter: ConversationAdapter;
  identity: unknown;
  target: ConversationTargetRef | null;
  notify: (message: string, options: { type: "error" }) => void;
  translate: (key: string) => string;
}

export function useContextList({ adapter, identity, target, notify, translate }: UseContextListOptions) {
  const [contexts, setContexts] = useState<ConversationContext[]>([]);
  const [contextCursor, setContextCursor] = useState<string>();
  const [contextsLoading, setContextsLoading] = useState(false);

  useEffect(() => {
    if (!identity || !target) return;
    let active = true;
    const controller = new AbortController();
    setContextsLoading(true);
    setContexts([]);
    setContextCursor(undefined);
    adapter.listContexts(target.id, { limit: CONTEXT_PAGE_LIMIT, signal: controller.signal })
      .then((result) => {
        if (active) setContexts(result.items);
      })
      .catch((error) => {
        if (!active || error instanceof DOMException && error.name === "AbortError") return;
        notify(error instanceof ApiError ? error.displayMessage : translate("conversationCenter.context_load_failed"), { type: "error" });
      })
      .finally(() => {
        if (active) setContextsLoading(false);
      });

    return () => {
      active = false;
      controller.abort();
    };
  }, [adapter, identity, notify, target, translate]);

  const reset = useCallback(() => {
    setContexts([]);
    setContextCursor(undefined);
    setContextsLoading(false);
  }, []);

  const loadMore = useCallback(async () => {
    if (!target || !contextCursor || contextsLoading) return;
    setContextsLoading(true);
    try {
      const result = await adapter.listContexts(target.id, { limit: CONTEXT_PAGE_LIMIT, cursor: contextCursor });
      setContexts((previous) => [...previous, ...result.items.filter((item) => !previous.some((existing) => existing.id === item.id))]);
      setContextCursor(result.nextCursor);
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : translate("conversationCenter.context_load_failed"), { type: "error" });
    } finally {
      setContextsLoading(false);
    }
  }, [adapter, contextCursor, contextsLoading, notify, target, translate]);

  const update = useCallback((nextContext: ConversationContext) => {
    setContexts((previous) => {
      const exists = previous.some((item) => item.id === nextContext.id && item.targetId === nextContext.targetId);
      if (exists) {
        return previous.map((item) =>
          item.id === nextContext.id && item.targetId === nextContext.targetId ? nextContext : item);
      }
      return [nextContext, ...previous];
    });
  }, []);

  return useMemo(() => ({
    contexts,
    contextCursor,
    contextsLoading,
    reset,
    loadMore,
    update,
  }), [contextCursor, contexts, contextsLoading, loadMore, reset, update]);
}
