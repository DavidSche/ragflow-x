/**
 * SessionNav – left conversation/navigation panel for the workbench.
 *
 * List accessible chat assistants and each assistant's conversation history.
 * Each group can expand/collapse, the whole panel can collapse to a slim rail,
 * a search box filters conversations, and a multi-select mode deletes several
 * conversations at once. Long session titles fall back to the first question
 * with a hover tooltip that preserves line breaks.
 */
import { useMemo, useState, type ReactNode } from "react";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import {
  ChevronLeft,
  ChevronRight,
  ChevronsRight,
  ListChecks,
  Loader2,
  MessageSquareText,
  Plus,
  Search,
  Trash2,
  X,
} from "lucide-react";
import { cn } from "@/lib/utils";
import type { ChatLike, SessionLike } from "./workbench-types";

interface SessionNavProps {
  chats: ChatLike[];
  sessions: Record<string, SessionLike[]>;
  getTitle: (chatId: string, session: SessionLike) => string;
  selectedChatId: string;
  selectedSessionId: string;
  loading: boolean;
  open: boolean;
  expanded: string[];
  onToggleOpen: () => void;
  onToggleExpand: (chatId: string) => void;
  onSelectSession: (chatId: string, sessionId: string) => void;
  onNewSession: (chatId: string) => void;
  onDeleteSessions: (items: { chatId: string; sessionId: string }[]) => void;
  footer?: ReactNode;
}

export function SessionNav({
  chats,
  sessions,
  getTitle,
  selectedChatId,
  selectedSessionId,
  loading,
  open,
  expanded,
  onToggleOpen,
  onToggleExpand,
  onSelectSession,
  onNewSession,
  onDeleteSessions,
  footer,
}: SessionNavProps) {
  const translate = useTranslate();
  const [query, setQuery] = useState("");
  const [selectMode, setSelectMode] = useState(false);
  const [selectedByKey, setSelectedByKey] = useState<Map<string, { chatId: string; sessionId: string }>>(new Map());
  const q = query.trim().toLowerCase();

  const selKey = (chatId: string, sessionId: string) => `${chatId}::${sessionId}`;

  const visibleChats = useMemo(() => {
    if (!q) {
      return chats.map((c) => ({ chat: c, list: sessions[c.id] ?? [] }));
    }
    return chats
      .map((c) => {
        const all = sessions[c.id] ?? [];
        const chatMatches = c.name.toLowerCase().includes(q);
        const source = chatMatches ? all : all.filter((s) => getTitle(c.id, s).toLowerCase().includes(q));
        return { chat: c, list: source, show: chatMatches || source.length > 0 };
      })
      .filter((x) => x.show);
  }, [chats, sessions, q, getTitle]);

  const exitSelectMode = () => {
    setSelectMode(false);
    setSelectedByKey(new Map());
  };

  const toggleSelect = (chatId: string, sessionId: string) => {
    const k = selKey(chatId, sessionId);
    setSelectedByKey((prev) => {
      const next = new Map(prev);
      if (next.has(k)) next.delete(k);
      else next.set(k, { chatId, sessionId });
      return next;
    });
  };

  const confirmDelete = () => {
    if (selectedByKey.size === 0) return;
    onDeleteSessions([...selectedByKey.values()]);
    exitSelectMode();
  };

  if (!open) {
    return (
      <div
        className="flex w-11 shrink-0 flex-col items-center gap-2 border-r pt-2"
        aria-label={translate("workbench.conversations")}
      >
        <Button
          size="icon"
          variant="ghost"
          className="h-8 w-8"
          onClick={onToggleOpen}
          title={translate("workbench.expand_nav")}
          aria-label={translate("workbench.expand_nav")}
        >
          <ChevronsRight className="size-4" />
        </Button>
      </div>
    );
  }

  return (
    <aside
      className="flex w-64 shrink-0 flex-col border-r bg-sidebar/40"
      aria-label={translate("workbench.conversations")}
    >
      <div className="flex h-12 shrink-0 items-center justify-between border-b px-3">
        <span className="text-sm font-semibold">{translate("workbench.conversations")}</span>
        <div className="flex items-center gap-0.5">
          {selectMode ? (
            <Button
              size="icon"
              variant="ghost"
              className="h-7 w-7"
              onClick={exitSelectMode}
              title={translate("workbench.cancel_select")}
              aria-label={translate("workbench.cancel_select")}
            >
              <X className="size-4" />
            </Button>
          ) : (
            <Button
              size="icon"
              variant="ghost"
              className="h-7 w-7"
              onClick={() => setSelectMode(true)}
              title={translate("workbench.select_mode")}
              aria-label={translate("workbench.select_mode")}
            >
              <ListChecks className="size-4" />
            </Button>
          )}
          <Button
            size="icon"
            variant="ghost"
            className="h-7 w-7"
            onClick={onToggleOpen}
            title={translate("workbench.collapse_nav")}
            aria-label={translate("workbench.collapse_nav")}
          >
            <ChevronLeft className="size-4" />
          </Button>
        </div>
      </div>

      <div className="shrink-0 border-b p-2">
        <div className="relative">
          <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={translate("workbench.search_sessions")}
            aria-label={translate("workbench.search_sessions")}
            className="h-7 pl-7 text-sm"
          />
        </div>
      </div>

      <div className="flex-1 space-y-1 overflow-y-auto p-2">
        {loading ? (
          <div className="flex items-center gap-2 px-2 py-1 text-xs text-muted-foreground">
            <Loader2 className="size-3 animate-spin" />
            {translate("workbench.loading")}
          </div>
        ) : null}
        {!loading && chats.length === 0 ? (
          <div className="px-2 py-1 text-xs text-muted-foreground">{translate("workbench.scope_empty")}</div>
        ) : null}
        {!loading && chats.length > 0 && visibleChats.length === 0 ? (
          <div className="px-2 py-1 text-xs text-muted-foreground">{translate("workbench.no_sessions")}</div>
        ) : null}

        {visibleChats.map(({ chat, list }) => {
          const isExpanded = expanded.includes(chat.id);
          const isActiveChat = chat.id === selectedChatId;
          return (
            <div key={chat.id}>
              <div
                className={cn(
                  "flex items-center rounded-md pr-1 hover:bg-accent/60",
                  isActiveChat && "bg-accent/60",
                )}
              >
                <button
                  type="button"
                  className="flex min-w-0 flex-1 items-center gap-2 px-2 py-1.5 text-left"
                  onClick={() => onToggleExpand(chat.id)}
                  aria-expanded={isExpanded}
                  aria-label={chat.name}
                >
                  <ChevronRight
                    className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", isExpanded && "rotate-90")}
                  />
                  <MessageSquareText className="size-4 shrink-0 text-muted-foreground" />
                  <span className="truncate text-sm">{chat.name}</span>
                  <span className="ml-auto shrink-0 text-xs tabular-nums text-muted-foreground">{list.length}</span>
                </button>
                <Button
                  size="icon"
                  variant="ghost"
                  className="h-6 w-6 shrink-0"
                  onClick={() => onNewSession(chat.id)}
                  title={translate("workbench.new_session")}
                  aria-label={translate("workbench.new_session")}
                >
                  <Plus className="size-3.5" />
                </Button>
              </div>

              {isExpanded ? (
                <div className="ml-[1.375rem] mt-0.5 space-y-0.5 border-l pl-2">
                  {list.length === 0 ? (
                    <div className="px-2 py-1 text-xs text-muted-foreground">{translate("workbench.no_sessions")}</div>
                  ) : (
                    list.map((s) => {
                      const title = getTitle(chat.id, s);
                      const active = chat.id === selectedChatId && s.id === selectedSessionId;
                      const checked = selectedByKey.has(selKey(chat.id, s.id));
                      return (
                        <div key={s.id} className="group relative">
                          <button
                            type="button"
                            onClick={() =>
                              selectMode ? toggleSelect(chat.id, s.id) : onSelectSession(chat.id, s.id)
                            }
                            className={cn(
                              "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-sm",
                              selectMode
                                ? checked
                                  ? "bg-primary/10 font-medium text-primary"
                                  : "hover:bg-accent/60"
                                : active
                                  ? "bg-primary/10 font-medium text-primary"
                                  : "hover:bg-accent/60",
                            )}
                          >
                            {selectMode ? (
                              <Checkbox checked={checked} className="pointer-events-none size-4 shrink-0" aria-hidden />
                            ) : null}
                            <span className="truncate">{title}</span>
                          </button>
                          <div
                            role="tooltip"
                            className="pointer-events-none absolute left-2 top-full z-30 hidden max-w-[15rem] whitespace-pre-wrap break-words rounded-md border bg-popover p-2 text-xs leading-relaxed text-popover-foreground shadow-md group-hover:block"
                          >
                            {title}
                          </div>
                        </div>
                      );
                    })
                  )}
                </div>
              ) : null}
            </div>
          );
        })}
      </div>

      {selectMode ? (
        <div className="flex shrink-0 items-center justify-between gap-2 border-t p-2">
          <span className="text-xs text-muted-foreground">
            {translate("workbench.select_count", { count: String(selectedByKey.size) })}
          </span>
          <div className="flex gap-1">
            <Button size="sm" variant="outline" onClick={exitSelectMode}>
              {translate("workbench.cancel_select")}
            </Button>
            <Button size="sm" variant="destructive" disabled={selectedByKey.size === 0} onClick={confirmDelete}>
              <Trash2 className="size-3.5" />
              {translate("workbench.delete_selected")}
            </Button>
          </div>
        </div>
      ) : null}

      {footer}
    </aside>
  );
}
