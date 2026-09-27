import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslate } from "ra-core";
import { useNavigate } from "react-router-dom";
import { api } from "../../lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Bot, RefreshCw, Search, MessageSquareText } from "lucide-react";
import { cn } from "@/lib/utils";

interface RecentConversationSession {
  app_type: "chat" | "search" | "agent";
  target_id: string;
  target_name: string;
  context_id: string;
  request_id: string;
  title: string;
  status: "completed" | "no_answer" | "failed";
  last_activity: string;
  turn_count: number;
}

interface CrossAppSessionIndexProps {
  refreshToken?: number;
  className?: string;
}

const appIcon = {
  chat: MessageSquareText,
  search: Search,
  agent: Bot,
} as const;

export function CrossAppSessionIndex({ refreshToken = 0, className }: CrossAppSessionIndexProps) {
  const translate = useTranslate();
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const [sessions, setSessions] = useState<RecentConversationSession[]>([]);
  const [loading, setLoading] = useState(true);
  const queryRef = useRef("");
  queryRef.current = query;

  const load = useCallback(async (search: string) => {
    setLoading(true);
    try {
      const response = await api.get<{ code: number; data: RecentConversationSession[] }>(
        `/workbench/recent-sessions?limit=30&search=${encodeURIComponent(search)}`,
      );
      setSessions(response.data.data ?? []);
    } catch {
      setSessions([]);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const timer = setTimeout(() => void load(query.trim()), query ? 250 : 0);
    return () => clearTimeout(timer);
  }, [load, query]);

  useEffect(() => {
    if (refreshToken > 0) void load(queryRef.current);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [refreshToken]);

  const openSession = (session: RecentConversationSession) => {
    const params = new URLSearchParams({ kind: session.app_type, targetId: session.target_id });
    if (session.app_type !== "search") params.set("contextId", session.context_id);
    navigate(`/conversation-center?${params.toString()}`);
  };

  return (
    <section className={cn("border-t bg-sidebar/40", className)} aria-label={translate("workbench.cross_app_sessions")}>
      <div className="flex items-center justify-between gap-2 border-b px-3 py-2">
        <h2 className="min-w-0 truncate text-sm font-semibold">{translate("workbench.cross_app_sessions")}</h2>
        <Button
          size="icon"
          variant="ghost"
          className="h-7 w-7"
          onClick={() => void load(query.trim())}
          disabled={loading}
          aria-label={translate("workbench.refresh_sessions")}
        >
          <RefreshCw className={cn("size-3.5", loading && "animate-spin")} />
        </Button>
      </div>
      <div className="p-2">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={translate("workbench.search_cross_app_sessions")}
            aria-label={translate("workbench.search_cross_app_sessions")}
            className="h-7 pl-7 text-sm"
          />
        </div>
      </div>
      <div className="max-h-56 space-y-1 overflow-y-auto px-2 pb-2">
        {loading && sessions.length === 0 ? (
          <p className="px-2 py-1 text-xs text-muted-foreground">{translate("workbench.loading")}</p>
        ) : null}
        {!loading && sessions.length === 0 ? (
          <p className="px-2 py-1 text-xs text-muted-foreground">{translate("workbench.no_cross_app_sessions")}</p>
        ) : null}
        {sessions.map((session) => {
          const Icon = appIcon[session.app_type];
          return (
            <button
              key={`${session.app_type}:${session.target_id}:${session.context_id}`}
              type="button"
              onClick={() => openSession(session)}
              className="w-full rounded-md px-2 py-1.5 text-left hover:bg-accent/60"
            >
              <span className="flex min-w-0 items-center gap-2">
                <Icon className="size-3.5 shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm">{session.title || session.target_name}</span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {session.target_name} · {translate(`workbench.app_${session.app_type}`)} · {session.turn_count}
                  </span>
                </span>
              </span>
            </button>
          );
        })}
      </div>
    </section>
  );
}
