import { useCallback, useEffect, useMemo, useState } from "react";
import { api, ApiError } from "../../lib/api";
import type { ConversationTargetRef, ConversationTargetStatus } from "./types";

const DIRECTORY_PAGE_LIMIT = 50;
const DIRECTORY_SEARCH_DEBOUNCE_MS = 250;
const DIRECTORY_CACHE_TTL_MS = 30_000;

interface DirectoryCacheEntry {
  items: ConversationTargetRef[];
  total: number;
  timestamp: number;
}

interface AssistantRecord {
  id: string;
  kind: "chat" | "agent";
  name: string;
  description?: string;
  owner_id?: string;
  status: string;
  updated_at: string;
}

const directoryCache = new Map<string, DirectoryCacheEntry>();

function targetFromRecord(record: AssistantRecord): ConversationTargetRef {
  return {
    id: record.id,
    kind: record.kind,
    name: record.name,
    description: record.description,
    ownerId: record.owner_id,
    status: record.status,
    updatedAt: record.updated_at ? Date.parse(record.updated_at) : undefined,
  };
}

interface UseAssistantDirectoryOptions {
  accessible: boolean;
  identity: unknown;
  notify: (message: string, options: { type: "error" }) => void;
  translate: (key: string) => string;
}

export function useAssistantDirectory({ accessible, identity, notify, translate }: UseAssistantDirectoryOptions) {
  const [targetQuery, setTargetQuery] = useState("");
  const [targets, setTargets] = useState<ConversationTargetRef[]>([]);
  const [targetTotal, setTargetTotal] = useState(0);
  const [targetsLoading, setTargetsLoading] = useState(false);
  const [targetStatus, setTargetStatus] = useState<ConversationTargetStatus>("EMPTY");
  const [resetNonce, setResetNonce] = useState(0);

  const identityKey = `${String((identity as { tenant_id?: string } | undefined)?.tenant_id ?? "")}:${String((identity as { id?: string } | undefined)?.id ?? "")}`;

  useEffect(() => {
    if (!identity || !accessible) return;
    let active = true;
    const controller = new AbortController();
    const cacheKey = `${identityKey}:${targetQuery.trim().toLowerCase()}`;
    const cached = directoryCache.get(cacheKey);
    if (cached && Date.now() - cached.timestamp < DIRECTORY_CACHE_TTL_MS) {
      setTargets(cached.items);
      setTargetTotal(cached.total);
      setTargetStatus("READY");
    }
    const timer = setTimeout(() => {
      if (!active) return;
      setTargetsLoading(true);
      setTargetStatus((previous) => previous === "READY" ? "REFRESHING" : "LOADING");
      const params = new URLSearchParams({ page: "1", page_size: String(DIRECTORY_PAGE_LIMIT) });
      if (targetQuery.trim()) params.set("query", targetQuery.trim());
      api.get(`/conversation/assistants?${params.toString()}`, { signal: controller.signal })
        .then((response) => {
          if (!active) return;
          const envelope = response.data as { code?: number; data?: { items?: AssistantRecord[]; total?: number } };
          if (envelope.code !== 0) throw new ApiError(response.status, envelope.code ?? 0, "assistant catalog request failed");
          const items = (envelope.data?.items ?? []).map(targetFromRecord);
          setTargets(items);
          setTargetTotal(envelope.data?.total ?? items.length);
          setTargetStatus("READY");
          directoryCache.set(cacheKey, { items, total: envelope.data?.total ?? items.length, timestamp: Date.now() });
        })
        .catch((error) => {
          if (!active || error instanceof DOMException && error.name === "AbortError") return;
          setTargetStatus((previous) => previous === "READY" ? "READY" : "EMPTY");
          notify(error instanceof ApiError ? error.displayMessage : translate("conversationCenter.target_load_failed"), { type: "error" });
        })
        .finally(() => {
          if (active) setTargetsLoading(false);
        });
    }, cached ? 0 : DIRECTORY_SEARCH_DEBOUNCE_MS);

    return () => {
      active = false;
      clearTimeout(timer);
      controller.abort();
    };
  }, [accessible, identity, identityKey, notify, resetNonce, targetQuery, translate]);

  const reset = useCallback(() => {
    setTargetQuery("");
    setTargets([]);
    setTargetTotal(0);
    setTargetStatus("EMPTY");
    setResetNonce((nonce) => nonce + 1);
  }, []);

  const refresh = useCallback(() => {
    directoryCache.delete(`${identityKey}:${targetQuery.trim().toLowerCase()}`);
    setTargetStatus("LOADING");
  }, [identityKey, targetQuery]);

  return useMemo(() => ({
    targetQuery,
    setTargetQuery,
    targets,
    targetTotal,
    targetsLoading,
    targetStatus,
    reset,
    refresh,
  }), [refresh, reset, resetNonce, targetQuery, targetStatus, targetTotal, targets, targetsLoading]);
}
