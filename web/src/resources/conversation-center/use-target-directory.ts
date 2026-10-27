import { useCallback, useEffect, useMemo, useState } from "react";
import { ApiError } from "../../lib/api";
import type { ConversationAdapter, ConversationTargetRef, ConversationTargetStatus } from "./types";

const TARGET_PAGE_LIMIT = 50;
const TARGET_SEARCH_DEBOUNCE_MS = 250;
const TARGET_CACHE_TTL_MS = 30_000;

interface TargetCacheEntry {
  items: ConversationTargetRef[];
  total: number;
  nextCursor?: string;
  timestamp: number;
}

const targetCache = new Map<string, TargetCacheEntry>();

interface UseTargetDirectoryOptions {
  adapter: ConversationAdapter;
  accessible: boolean;
  identity: unknown;
  notify: (message: string, options: { type: "error" }) => void;
  translate: (key: string) => string;
}

export function useTargetDirectory({ adapter, accessible, identity, notify, translate }: UseTargetDirectoryOptions) {
  const [targetQuery, setTargetQuery] = useState("");
  const [targets, setTargets] = useState<ConversationTargetRef[]>([]);
  const [targetTotal, setTargetTotal] = useState(0);
  const [targetCursor, setTargetCursor] = useState<string>();
  const [targetsLoading, setTargetsLoading] = useState(false);
  const [targetStatus, setTargetStatus] = useState<ConversationTargetStatus>("EMPTY");
  const identityKey = `${String((identity as { tenant_id?: string } | undefined)?.tenant_id ?? "")}:${String((identity as { id?: string } | undefined)?.id ?? "")}`;

  useEffect(() => {
    if (!identity || !accessible) return;
    let active = true;
    const controller = new AbortController();
    const cacheKey = `${identityKey}:${adapter.kind}:${targetQuery}`;
    const cached = targetCache.get(cacheKey);
    if (cached && Date.now() - cached.timestamp < TARGET_CACHE_TTL_MS) {
      setTargets(cached.items);
      setTargetTotal(cached.total);
      setTargetCursor(cached.nextCursor);
      setTargetStatus("READY");
    }
    const timer = setTimeout(() => {
      if (!active) return;
      setTargetsLoading(true);
      setTargetStatus((previous) => previous === "READY" ? "REFRESHING" : "LOADING");
      adapter.searchTargets(targetQuery, { limit: TARGET_PAGE_LIMIT, signal: controller.signal })
        .then((result) => {
          if (!active) return;
          setTargets(result.items);
          setTargetTotal(result.total ?? result.items.length);
          setTargetCursor(result.nextCursor);
          setTargetStatus("READY");
          targetCache.set(cacheKey, {
            items: result.items,
            total: result.total ?? result.items.length,
            nextCursor: result.nextCursor,
            timestamp: Date.now(),
          });
        })
        .catch((error) => {
          if (!active || error instanceof DOMException && error.name === "AbortError") return;
          setTargetStatus((previous) => previous === "READY" ? "READY" : "EMPTY");
          notify(error instanceof ApiError ? error.displayMessage : translate("conversationCenter.target_load_failed"), { type: "error" });
        })
        .finally(() => {
          if (active) setTargetsLoading(false);
        });
    }, cached ? 0 : TARGET_SEARCH_DEBOUNCE_MS);

    return () => {
      active = false;
      clearTimeout(timer);
      controller.abort();
    };
  }, [accessible, adapter, identity, identityKey, notify, targetQuery, translate]);

  const reset = useCallback(() => {
    setTargetQuery("");
    setTargets([]);
    setTargetTotal(0);
    setTargetCursor(undefined);
    setTargetStatus("EMPTY");
  }, []);

  const loadMore = useCallback(async () => {
    if (!identity || !targetCursor || targetsLoading) return;
    setTargetsLoading(true);
    setTargetStatus("REFRESHING");
    try {
      const result = await adapter.searchTargets(targetQuery, { limit: TARGET_PAGE_LIMIT, cursor: targetCursor });
      setTargets((previous) => [...previous, ...result.items.filter((item) => !previous.some((existing) => existing.kind === item.kind && existing.id === item.id))]);
      setTargetCursor(result.nextCursor);
      if (result.total != null) setTargetTotal(result.total);
      setTargetStatus("READY");
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : translate("conversationCenter.target_load_failed"), { type: "error" });
    } finally {
      setTargetsLoading(false);
    }
  }, [adapter, identity, notify, targetCursor, targetQuery, targetsLoading, translate]);

  return useMemo(() => ({
    targetQuery,
    setTargetQuery,
    targets,
    targetTotal,
    targetCursor,
    targetsLoading,
    targetStatus,
    reset,
    loadMore,
  }), [loadMore, reset, targetCursor, targetQuery, targetStatus, targetTotal, targets, targetsLoading]);
}
