import { useEffect, useState } from "react";
import { Show } from "@/components/admin";
import { useNotify, useRecordContext, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Loader2, Plus, RefreshCw, Copy, Pencil, Trash2, Check, X, Share2, Code } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { ConfirmDialog } from "../../components/ConfirmDialog";

interface ChatLike {
  id: string;
  name: string;
}

interface SessionLike {
  id: string;
  name: string;
  chat_id?: string;
}

interface MessageLike {
  role?: string;
  content?: string;
}

const SessionList = () => {
  const record = useRecordContext<ChatLike>();
  const notify = useNotify();
  const [sessions, setSessions] = useState<SessionLike[]>([]);
  const [selectedId, setSelectedId] = useState<string>("");
  const [messages, setMessages] = useState<MessageLike[]>([]);
  const [newName, setNewName] = useState("");
  const [loading, setLoading] = useState(false);
  const [editId, setEditId] = useState("");
  const [editName, setEditName] = useState("");
  const [sessionFilter, setSessionFilter] = useState("");
  const t = useTranslate();

  const loadSessions = async () => {
    if (!record) return;
    setLoading(true);
    try {
      const res = await api.get<{ code: number; data: SessionLike[] }>(`/chats/${record.id}/sessions`);
      setSessions(res.data.data ?? []);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("chats.session_load_error"), { type: "error" });
    } finally {
      setLoading(false);
    }
  };

  const loadMessages = async (sessionId: string) => {
    if (!record) return;
    try {
      const res = await api.get<{ code: number; data: { messages?: MessageLike[] } }>(
        `/chats/${record.id}/sessions/${sessionId}`,
      );
      setMessages(res.data.data?.messages ?? []);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("chats.session_messages_error"), { type: "error" });
    }
  };

  const createSession = async (name?: string) => {
    if (!record) return;
    try {
      const res = await api.post(`/chats/${record.id}/sessions`, { name: name || newName || t("chats.new_session") });
      setNewName("");
      await loadSessions();
      return res.data.data?.id as string | undefined;
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("chats.session_create_error"), { type: "error" });
      return undefined;
    }
  };

  const startRename = (s: SessionLike) => {
    setEditId(s.id);
    setEditName(s.name);
  };

  const saveRename = async () => {
    if (!record || !editId || !editName.trim()) return;
    try {
      await api.patch(`/chats/${record.id}/sessions/${editId}`, { name: editName.trim() });
      notify(t("chats.session_renamed"), { type: "success" });
      setEditId("");
      setEditName("");
      await loadSessions();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("chats.session_rename_error"), { type: "error" });
    }
  };

  const duplicate = async (s: SessionLike) => {
    const id = await createSession(s.name);
    if (id) {
      notify(t("chats.session_duplicated"), { type: "success" });
      setSelectedId(id);
    }
  };

  const [confirmSession, setConfirmSession] = useState<SessionLike | null>(null);
  const [deleting, setDeleting] = useState(false);

  const removeSession = async (s: SessionLike) => {
    if (!record) return;
    setDeleting(true);
    try {
      await api.delete(`/chats/${record.id}/sessions/${s.id}`);
      notify(t("chats.session_deleted"), { type: "success" });
      if (selectedId === s.id) {
        setSelectedId("");
        setMessages([]);
      }
      await loadSessions();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("chats.session_delete_error"), { type: "error" });
    } finally {
      setDeleting(false);
      setConfirmSession(null);
    }
  };

  useEffect(() => {
    void loadSessions();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [record?.id]);

  useEffect(() => {
    if (selectedId) void loadMessages(selectedId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedId]);

  if (!record) return null;

  return (
    <div className="grid gap-4 md:grid-cols-[280px_1fr]" aria-label={t("chats.session_management")}>
      <div className="space-y-2 rounded border p-3">
        <div className="flex items-center justify-between text-sm font-medium">
          <span>{t("chats.session_list")}</span>
          <div className="flex gap-1">
            <Button size="sm" variant="outline" onClick={() => void loadSessions()}>
              <RefreshCw />
            </Button>
            {loading && <Loader2 className="size-4 animate-spin" />}
          </div>
        </div>
        <Input
          value={sessionFilter}
          onChange={(e) => setSessionFilter(e.target.value)}
          placeholder={t("chats.session_search")}
          aria-label={t("chats.session_search")}
          className="h-7 text-sm"
        />
        <div className="h-px bg-border" />
        <div className="max-h-96 space-y-1 overflow-y-auto">
          {sessions.filter((s) => !sessionFilter.trim() || (s.name || s.id).toLowerCase().includes(sessionFilter.trim().toLowerCase())).map((s) => (
            <div key={s.id} className={`rounded border px-2 py-1.5 text-sm ${s.id === selectedId ? "bg-muted" : ""}`}>
              {editId === s.id ? (
                <div className="flex items-center gap-1">
                  <Input
                    value={editName}
                    onChange={(e) => setEditName(e.target.value)}
                    className="h-7 text-sm"
                    onKeyDown={(e) => {
                      if (e.key === "Enter") void saveRename();
                      if (e.key === "Escape") setEditId("");
                    }}
                  />
                  <Button size="icon" variant="ghost" className="h-6 w-6" onClick={() => void saveRename()}>
                    <Check className="size-3.5" />
                  </Button>
                  <Button size="icon" variant="ghost" className="h-6 w-6" onClick={() => setEditId("")}>
                    <X className="size-3.5" />
                  </Button>
                </div>
              ) : (
                <>
                  <button className="w-full truncate text-left" onClick={() => setSelectedId(s.id)}>
                    {s.name || s.id}
                  </button>
                  <div className="mt-1 flex items-center gap-1">
                    <Button size="icon" variant="ghost" className="h-6 w-6" onClick={() => startRename(s)} title={t("chats.rename")}>
                      <Pencil className="size-3.5" />
                    </Button>
                    <Button size="icon" variant="ghost" className="h-6 w-6" onClick={() => void duplicate(s)} title={t("chats.duplicate")}>
                      <Copy className="size-3.5" />
                    </Button>
                    <Button size="icon" variant="ghost" className="h-6 w-6" onClick={() => setConfirmSession(s)} title={t("confirm.delete_label")}>
                      <Trash2 className="size-3.5" />
                    </Button>
                  </div>
                </>
              )}
            </div>
          ))}
          {sessions.length === 0 && <div className="text-sm text-muted-foreground">{t("chats.session_empty")}</div>}
        </div>
        <div className="flex gap-2 pt-2">
          <Input value={newName} onChange={(e) => setNewName(e.target.value)} placeholder={t("chats.session_name_label")} />
          <Button size="sm" variant="outline" onClick={() => void createSession()}>
            <Plus /> {t("chats.session_new")}
          </Button>
        </div>
        <ConfirmDialog
          open={!!confirmSession}
          onOpenChange={(v) => !v && setConfirmSession(null)}
          title={t("chats.session_delete_title")}
          message={t("chats.session_delete_confirm", { name: confirmSession?.name || confirmSession?.id || "" })}
          confirmLabel={t("confirm.delete_label")}
          onConfirm={() => confirmSession && void removeSession(confirmSession)}
          loading={deleting}
        />
      </div>

      <div className="rounded border p-3">
        <div className="mb-2 text-sm font-medium">{t("chats.messages_label")}</div>
        <div className="max-h-[28rem] space-y-2 overflow-y-auto">
          {messages.map((m, idx) => (
            <div key={idx} className="flex gap-2 text-sm">
              <Badge variant="outline">{m.role === "assistant" ? "AI" : "我"}</Badge>
              <div className="whitespace-pre-wrap rounded bg-muted/50 px-3 py-2">{m.content}</div>
            </div>
          ))}
          {messages.length === 0 && <div className="text-sm text-muted-foreground">{t("chats.messages_empty")}</div>}
        </div>
      </div>
    </div>
  );
};

export const ChatShow = () => {
  const t = useTranslate();
  return (
  <Show aria-label={t("chats.aria_chat_detail")}>
    <div className="space-y-4 p-4">
      <ChatMetaPanel />
      <SessionList />
    </div>
  </Show>
  );
};

const ChatMetaPanel = () => {
  const record = useRecordContext<ChatLike>();
  const notify = useNotify();
  const t = useTranslate();
  const [usage, setUsage] = useState<{ requests?: number; tokens_in?: number; tokens_out?: number; cost?: number } | null>(null);
  useEffect(() => {
    if (!record) return;
    (async () => {
      try {
        const res = await api.get<{ code: number; data: { requests?: number; tokens_in?: number; tokens_out?: number; cost?: number } }>(`/chats/${record.id}/usage`);
        setUsage(res.data.data ?? null);
      } catch {
        /* keep null */
      }
    })();
  }, [record?.id]);
  if (!record) return null;
  const shareUrl = `${window.location.origin}/conversation-center?kind=chat&targetId=${encodeURIComponent(record.id)}`;
  const embed = `<iframe src="${shareUrl}" style="width:100%;height:600px;border:0"></iframe>`;
  const copy = async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text);
      notify(label, { type: "success" });
    } catch {
      notify(t("chats.copy_error"), { type: "error" });
    }
  };
  return (
    <div className="flex flex-wrap items-center gap-3 rounded border p-3 text-sm" aria-label={t("chats.aria_usage_stats")}>
      <div className="flex flex-wrap gap-4">
        <div>{t("chats.usage_requests")}：<b>{usage?.requests ?? 0}</b></div>
        <div>{t("chats.usage_tokens_in")}：<b>{usage?.tokens_in ?? 0}</b></div>
        <div>{t("chats.usage_tokens_out")}：<b>{usage?.tokens_out ?? 0}</b></div>
        <div>{t("chats.usage_cost")}：<b>{usage?.cost ?? 0}</b></div>
      </div>
      <div className="ml-auto flex gap-2">
        <Button size="sm" variant="outline" onClick={() => void copy(shareUrl, t("chats.share_copied"))} aria-label={t("chats.share_link")}>
          <Share2 aria-hidden="true" /> {t("chats.share_link")}
        </Button>
        <Button size="sm" variant="outline" onClick={() => void copy(embed, t("chats.embed_copied"))} aria-label={t("chats.share_embed")}>
          <Code aria-hidden="true" /> {t("chats.share_embed")}
        </Button>
      </div>
    </div>
  );
};
