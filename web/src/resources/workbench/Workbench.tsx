/**
 * Workbench – Q&A workbench page.
 *
 * Sub-components are split into:
 * - workbench-types.ts   (types, utils, stream reader)
 * - SessionNav.tsx       (left chat assistant + conversation history nav)
 * - CitationLink.tsx     (inline citation pill with popover)
 * - AnswerMarkdown.tsx   (markdown renderer with citations)
 * - SourceCard.tsx       (sidebar citation card)
 * - InlineCitations.tsx  (citation buttons below answer)
 * - ChatInput.tsx        (input area with send/stop)
 * - MessageBubble.tsx    (message with actions and feedback)
 */
import { useCallback, useEffect, useRef, useState } from "react";
import { api, ApiError } from "../../lib/api";
import { useGetIdentity, useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

import { CircleAlert, Loader2 } from "lucide-react";
import { CitationWindow } from "./CitationWindow";
import { ChatInput } from "./ChatInput";

import { SessionNav } from "./SessionNav";
import { CrossAppSessionIndex } from "./CrossAppSessionIndex";
import { ConversationShell } from "./ConversationShell";
import { ConversationExportMenu } from "./ConversationExportMenu";
import { OriginalDocumentViewer } from "@/components/documents/original-document-viewer";
import { isTextPreview } from "@/components/documents/document-preview";
import {
  type AttachmentDraft,
  type ChatLike,
  type Citation,
  type Message,
  type Rating,
  type SessionLike,
  type UploadedFileMeta,
  newId,
  mergeCitations,
  streamRead,
  citationsFromReference,
} from "./workbench-types";

const TIMEOUT_MS = 120000;

const GENERIC_SESSION_NAMES = new Set(["new session", "新会话", "new conversation", "会话", "untitled", "default"]);

const isGenericSessionName = (name?: string) => {
  const n = (name ?? "").trim();
  if (!n) return true;
  return GENERIC_SESSION_NAMES.has(n.toLowerCase());
};

export const Workbench = () => {
  const notify = useNotify();
  const translate = useTranslate();
  const GREETING = translate("workbench.greeting");
  const newSystemMessage = (content: string): Message => ({
    id: newId(), role: "assistant", content, kind: "chat",
    createdAt: new Date().toISOString(), status: "completed",
  });
  const runningRef = useRef(false);
  const abortRef = useRef<AbortController | null>(null);
  const userAbortRef = useRef(false);
  const sessionReadyRef = useRef(false);
  const sessionAttachmentsRef = useRef<UploadedFileMeta[]>([]);

  const [chats, setChats] = useState<ChatLike[]>([]);
  const [chatsLoaded, setChatsLoaded] = useState(false);
  const [sessions, setSessions] = useState<Record<string, SessionLike[]>>({});
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const [firstQuestions, setFirstQuestions] = useState<Record<string, string>>({});
  const [expandedChats, setExpandedChats] = useState<string[]>([]);
  const [navOpen, setNavOpen] = useState(true);
  const [recentSessionsRefresh, setRecentSessionsRefresh] = useState(0);

  const [chatId, setChatId] = useState("");
  const [sessionId, setSessionId] = useState(() => newId());
  const [selectedSessionId, setSelectedSessionId] = useState("");
  const [messages, setMessages] = useState<Message[]>([{ role: "assistant", content: GREETING }]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [commentTurn, setCommentTurn] = useState("");
  const [commentText, setCommentText] = useState("");
  const [openChunk, setOpenChunk] = useState<Citation | null>(null);
  const [chunkLoading, setChunkLoading] = useState(false);
  const [chunkContent, setChunkContent] = useState("");
  const [docViewer, setDocViewer] = useState<{ datasetId: string; docId: string; name: string } | null>(null);
  const [docChunks, setDocChunks] = useState<{ id: string; content: string }[]>([]);
  const [docLoading, setDocLoading] = useState(false);
  const [docView, setDocView] = useState<"chunks" | "original">("chunks");
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [previewCt, setPreviewCt] = useState("");
  const [previewText, setPreviewText] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);

  const [attachDrafts, setAttachDrafts] = useState<AttachmentDraft[]>([]);
  const { data: identityData } = useGetIdentity();
  const identity = identityData as { id?: string } | undefined;

  const loadSessionsForChat = useCallback(async (chatId: string) => {
    try {
      const res = await api.get<{ code: number; data: SessionLike[] }>(`/chats/${chatId}/sessions`);
      const list = res.data.data ?? [];
      setSessions((prev) => ({ ...prev, [chatId]: list }));
      const unnamed = list.filter((s) => isGenericSessionName(s.name));
      await Promise.allSettled(
        unnamed.map(async (s) => {
          try {
            const d = await api.get<{
              code: number;
              data: { messages?: { role?: string; content?: string }[] };
            }>(`/chats/${chatId}/sessions/${s.id}`);
            const question = (d.data.data?.messages ?? []).find((m) => m.role === "user")?.content?.trim() ?? "";
            if (question) {
              setFirstQuestions((prev) => ({ ...prev, [`${chatId}::${s.id}`]: question }));
            }
          } catch {
            /* keep placeholder */
          }
        }),
      );
    } catch {
      setSessions((prev) => ({ ...prev, [chatId]: [] }));
    }
  }, []);

  useEffect(() => {
    (async () => {
      try {
        const res = await api.get<{ code: number; data: { items: ChatLike[] } }>("/chats");
        const list = res.data.data?.items ?? [];
        setChats(list);
        const target = new URLSearchParams(window.location.search).get("chat");
        if (target && list.some((c) => c.id === target)) {
          setChatId(target);
          setExpandedChats((prev) => (prev.includes(target) ? prev : [...prev, target]));
        }
        setSessionsLoading(true);
        await Promise.allSettled(list.map((c) => loadSessionsForChat(c.id)));
      } catch {
        /* keep empty */
      } finally {
        setSessionsLoading(false);
        setChatsLoaded(true);
      }
    })();
  }, [loadSessionsForChat]);

  const sessionTitle = useCallback(
    (chatId: string, s: SessionLike) => {
      const name = (s.name ?? "").trim();
      const question = firstQuestions[`${chatId}::${s.id}`]?.trim();
      if (name && !isGenericSessionName(name)) return name;
      if (question) return question;
      return translate("workbench.default_session_title");
    },
    [firstQuestions, translate],
  );

  const toggleExpand = (chatId: string) => {
    setExpandedChats((prev) => (prev.includes(chatId) ? prev.filter((id) => id !== chatId) : [...prev, chatId]));
  };

  const startNewSession = (chatId: string) => {
    if (busy || runningRef.current) return;
    if (abortRef.current) abortRef.current.abort();
    setChatId(chatId);
    setSelectedSessionId("");
    setSessionId(newId());
    sessionReadyRef.current = false;
    sessionAttachmentsRef.current = [];
    setCommentTurn("");
    setCommentText("");
    setMessages([newSystemMessage(GREETING)]);
    if (!chatId) return;
    (async () => {
      try {
        const res = await api.get<{ code: number; data: { prompt_config?: { prologue?: string } } }>(
          `/chats/${chatId}/config`,
        );
        const prologue = res.data.data?.prompt_config?.prologue?.trim();
        if (prologue) setMessages([newSystemMessage(prologue)]);
      } catch {
        /* keep greeting */
      }
    })();
  };

  const selectSession = async (chatId: string, sessionIdToLoad: string) => {
    if (busy || runningRef.current) return;
    if (abortRef.current) abortRef.current.abort();
    setChatId(chatId);
    setSelectedSessionId(sessionIdToLoad);
    setSessionId(sessionIdToLoad);
    sessionReadyRef.current = true;
    sessionAttachmentsRef.current = [];
    setCommentTurn("");
    setCommentText("");
    setMessages([newSystemMessage(translate("workbench.loading"))]);
    try {
      const res = await api.get<{ code: number; data: SessionLike }>(
        `/chats/${chatId}/sessions/${sessionIdToLoad}`,
      );
      const sess = res.data.data;
      const refs = sess?.reference ?? [];
      const msgs = (sess?.messages ?? []).filter((m) => m.role === "user" || m.role === "assistant");
      let answerCount = 0;
      const mapped = msgs.map((m, i) => {
        const base: Message = {
          id: `${sessionIdToLoad}-${i}`,
          role: m.role as "user" | "assistant",
          content: m.content,
          turnId: `${sessionIdToLoad}-${i}`,
          kind: "chat",
          createdAt: (m as Message & { create_time?: number }).create_time
            ? new Date(Number((m as Message & { create_time?: number }).create_time) * 1000).toISOString()
            : undefined,
          status: "completed",
          ...(m.files && m.files.length ? { files: m.files } : {}),
        };
        if (m.role === "assistant" && m.content && !m.content.startsWith("**ERROR**")) {
          if (answerCount > 0) {
            const entry = refs[answerCount - 1] as Record<string, unknown> | undefined;
            const cites = citationsFromReference(entry);
            if (cites.length) base.citations = cites;
          }
          answerCount += 1;
        }
        return base;
      });
      setMessages(msgs.length ? mapped : [newSystemMessage(GREETING)]);
    } catch {
      setMessages([newSystemMessage(translate("workbench.error"))]);
    }
  };

  const appendAssistantContent = (turnId: string, delta: string, refs: Citation[]) => {
    setMessages((prev) =>
      prev.map((m) => {
        if (m.role !== "assistant" || m.turnId !== turnId) return m;
        const next = { ...m, content: m.content + delta };
        if (refs.length) next.citations = mergeCitations(m.citations ?? [], refs);
        return next;
      }),
    );
  };

  const appendAssistant = (turnId: string, patch: Partial<Message>) => {
    setMessages((prev) =>
      prev.map((m) => (m.role === "assistant" && m.turnId === turnId ? { ...m, ...patch } : m)),
    );
  };

  const runCompletion = async (history: Message[], turnId: string): Promise<string> => {
    setBusy(true);
    runningRef.current = true;
    const controller = new AbortController();
    abortRef.current = controller;
    const timer = setTimeout(() => controller.abort(), TIMEOUT_MS);
    const payload: Record<string, unknown> = {
      chat_id: chatId,
      messages: history.map((m) => ({
        role: m.role,
        content: m.content,
        ...(m.files && m.files.length ? { files: m.files } : {}),
      })),
      stream: true,
    };
    if (sessionReadyRef.current && sessionId) payload.session_id = sessionId;
    const body = JSON.stringify(payload);
    try {
    const res = await fetch("/api/v1/chat/completions", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Request-Id": turnId },
      body,
        signal: controller.signal,
      });
      if (!res.ok || !res.body) {
        const raw = await res.text().catch(() => "");
        if (res.status === 429) return translate("workbench.error_429");
        if (res.status === 401 || res.status === 403) return translate("workbench.error_403");
        return raw || translate("workbench.error_with_status", { status: String(res.status) });
      }
      let gotSession = false;
      let streamError = "";
      await streamRead(
        res.body,
        (delta, refs, raw) => {
          const sid =
            raw && typeof raw === "object" ? (raw as Record<string, unknown>).session_id : undefined;
          if (typeof sid === "string" && sid && !gotSession) {
            gotSession = true;
            sessionReadyRef.current = true;
            setSessionId(sid);
            setSelectedSessionId(sid);
          }
          appendAssistantContent(turnId, delta, refs);
        },
        (message) => {
          streamError = message || translate("workbench.error");
        },
      );
      return streamError;
    } catch (err) {
      const isAbort = err instanceof DOMException && err.name === "AbortError";
      if (isAbort) {
        return userAbortRef.current ? translate("workbench.stopped") : translate("workbench.timeout");
      }
      return translate("workbench.error");
    } finally {
      clearTimeout(timer);
      abortRef.current = null;
      userAbortRef.current = false;
      runningRef.current = false;
      setBusy(false);
    }
  };

  const stopGeneration = () => {
    userAbortRef.current = true;
    abortRef.current?.abort();
  };

  const uploadAttachment = async (file: File) => {
    const draftId = newId();
    setAttachDrafts((prev) => [
      ...prev,
      { id: draftId, name: file.name, size: file.size, mime: file.type || "application/octet-stream", status: "uploading", progress: 0 },
    ]);
    const form = new FormData();
    form.append("file", file);
    try {
      const res = await api.post<{ code: number; data: UploadedFileMeta }>("/chat/upload", form, {
        headers: { "Content-Type": "multipart/form-data" },
        onUploadProgress: (e) => {
          const total = e.total || 0;
          if (total > 0) {
            const pct = Math.min(99, Math.round((e.loaded * 100) / total));
            setAttachDrafts((prev) => prev.map((d) => (d.id === draftId ? { ...d, progress: pct } : d)));
          }
        },
      });
      const meta = res.data?.data as UploadedFileMeta | undefined;
      setAttachDrafts((prev) =>
        prev.map((d) =>
          d.id === draftId
            ? { ...d, status: "done", progress: 100, meta, name: meta?.name ?? d.name, size: meta?.size ?? d.size, mime: meta?.mime_type ?? d.mime }
            : d,
        ),
      );
    } catch {
      setAttachDrafts((prev) => prev.map((d) => (d.id === draftId ? { ...d, status: "error" } : d)));
    }
  };

  const addAttachments = (files: File[]) => {
    files.forEach((f) => void uploadAttachment(f));
  };

  const removeAttachment = (id: string) => {
    setAttachDrafts((prev) => prev.filter((d) => d.id !== id));
  };

  const attachmentCites = (files: UploadedFileMeta[]) =>
    files.map((f) => ({ name: f.name ?? "附件", content: f.content ?? "", attachmentId: f.id }));

  const send = async () => {
    const text = input.trim();
    if (!text || busy || runningRef.current) return;
    if (!chatId) {
      notify(translate("workbench.no_chat"), { type: "warning" });
      return;
    }
    setInput("");
    const turnId = newId();
    const history = messages.filter((m) => m.role === "user" || (m.role === "assistant" && m.content));
    const doneFiles = attachDrafts
      .filter((d) => d.status === "done" && d.meta)
      .map((d) => d.meta as UploadedFileMeta);
    const userMsg: Message = {
      id: `${turnId}-user`,
      role: "user",
      content: text,
      turnId,
      kind: "chat",
      createdAt: new Date().toISOString(),
      status: "completed",
      ...(doneFiles.length ? { files: doneFiles } : {}),
    };
    setAttachDrafts([]);
    setMessages([
      ...history,
      userMsg,
      { id: `${turnId}-assistant`, role: "assistant", content: "", turnId, kind: "chat", citations: [], createdAt: new Date().toISOString(), status: "streaming" },
    ]);
    const reason = await runCompletion([...history, userMsg], turnId);
    if (doneFiles.length) sessionAttachmentsRef.current = doneFiles;
    const citeFiles = doneFiles.length ? doneFiles : sessionAttachmentsRef.current;
    if (citeFiles.length) {
      appendAssistant(turnId, { citations: attachmentCites(citeFiles) });
    }
    if (reason) appendAssistant(turnId, { error: reason, status: "failed" });
    else appendAssistant(turnId, { status: "completed" });
    if (chatId) void loadSessionsForChat(chatId);
    setRecentSessionsRefresh((value) => value + 1);
  };

  const regenerate = async (turnId: string) => {
    if (busy || runningRef.current || !chatId) return;
    const i = messages.findIndex((m) => m.role === "assistant" && m.turnId === turnId);
    if (i < 0) return;
    const before = messages.slice(0, i);
    const oldAssistant = messages[i];
    const pairedUser = messages.slice(0, i).reverse().find((m) => m.role === "user" && m.turnId === turnId);
    const turnFiles = (pairedUser?.files ?? []) as UploadedFileMeta[];
    const citeFiles = turnFiles.length ? turnFiles : sessionAttachmentsRef.current;
    setMessages([...before, { id: `${turnId}-assistant`, role: "assistant", content: "", turnId, kind: "chat", citations: [], createdAt: new Date().toISOString(), status: "streaming" }]);
    const reason = await runCompletion(before, turnId);
    if (citeFiles.length) {
      const fileCites = attachmentCites(citeFiles);
      if (reason) setMessages([...before, { ...oldAssistant, error: reason, citations: fileCites, status: "failed" }]);
      else appendAssistant(turnId, { citations: fileCites, status: "completed" });
    } else if (reason) {
      setMessages([...before, { ...oldAssistant, error: reason }]);
    }
  };

  const rate = async (turnId: string, rating: Rating, comment?: string, attribution?: string) => {
    if (!turnId || !chatId) return;
    try {
      const res = await api.post("/chat/feedback", {
        chat_id: chatId,
        session_id: sessionId,
        message_id: turnId,
        request_id: turnId,
        rating,
        attribution: rating === "positive" ? undefined : attribution,
        comment: comment?.trim() || undefined,
      });
      if (res.data.code !== 0) {
        throw new ApiError(
          res.status ?? 200,
          res.data.code,
          res.data.message || "feedback failed",
          res.data.trace_id,
        );
      }
      notify(rating === "positive" ? translate("workbench.feedback_positive") : translate("workbench.feedback_negative"), { type: "success" });
      appendAssistant(turnId, { feedback: rating });
      setCommentTurn("");
      setCommentText("");
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : translate("workbench.feedback_fail"), { type: "error" });
    }
  };

  const copyText = async (content: string) => {
    try {
      await navigator.clipboard.writeText(content);
      notify(translate("workbench.copied"), { type: "success" });
    } catch {
      notify(translate("workbench.copy_failed"), { type: "error" });
    }
  };

  const createKnowledgeTask = async (turnId: string) => {
    if (!identity?.id) return;
    const assistantIndex = messages.findIndex((message) => message.id === turnId || message.turnId === turnId);
    const question = [...messages.slice(0, assistantIndex < 0 ? messages.length : assistantIndex)]
      .reverse()
      .find((message) => message.role === "user")?.content;
    const failedMessage = assistantIndex >= 0 ? messages[assistantIndex] : undefined;
    try {
      await api.post("/knowledge-tasks", {
        source_request_id: turnId.startsWith("assistant-") ? turnId.slice("assistant-".length) : turnId,
        title: question ? `跟进：${question.slice(0, 80)}` : `跟进无答案或负面反馈 ${turnId}`,
        description: [
          question ? `问题摘要：${question}` : "",
          failedMessage?.feedback === "negative" ? "反馈类型：negative" : "反馈类型：no_answer",
          failedMessage?.content ? `回答摘要：${failedMessage.content.slice(0, 500)}` : "",
        ].filter(Boolean).join("\n"),
        category: "knowledge",
        owner_id: identity.id,
        priority: "medium",
      });
      notify(translate("workbench.knowledge_task_created"), { type: "success" });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : translate("workbench.knowledge_task_failed"), { type: "error" });
    }
  };

  const currentSession = chatId
    ? sessions[chatId]?.find((s) => s.id === selectedSessionId || s.id === sessionId)
    : undefined;
  const exportTitle = currentSession ? sessionTitle(chatId, currentSession) : "conversation";
  const deleteSessions = async (items: { chatId: string; sessionId: string }[]) => {
    if (!items.length) return;
    const byChat = new Map<string, string[]>();
    for (const it of items) {
      const arr = byChat.get(it.chatId) ?? [];
      arr.push(it.sessionId);
      byChat.set(it.chatId, arr);
    }
    const deletedIds = new Set(items.map((i) => i.sessionId));
    for (const [cid, ids] of byChat) {
      try {
        if (ids.length === 1) {
          await api.delete(`/chats/${cid}/sessions/${ids[0]}`);
        } else {
          await api.delete(`/chats/${cid}/sessions`, { data: { ids } });
        }
        await loadSessionsForChat(cid);
      } catch {
        notify(translate("workbench.delete_fail"), { type: "error" });
        return;
      }
    }
    if (chatId && deletedIds.has(selectedSessionId)) {
      startNewSession(chatId);
    }
    notify(translate("workbench.delete_done", { count: String(items.length) }), { type: "success" });
  };

  const openSourceChunk = async (c: Citation) => {
    if (c.datasetId && c.docId && c.chunkId) {
      setOpenChunk(c);
      setChunkContent(c.content || "");
      setChunkLoading(true);
      try {
        const q = `dataset=${encodeURIComponent(c.datasetId)}&doc=${encodeURIComponent(c.docId)}&chunk=${encodeURIComponent(c.chunkId)}`;
        const res = await api.get(`/chat/reference?${q}`);
        const fetched = res.data.data?.content ?? res.data.data?.content_with_weight ?? "";
        if (fetched && !c.content) setChunkContent(fetched);
      } catch {
        if (!c.content) setChunkContent(translate("workbench.citation_load_fail"));
      }
      setChunkLoading(false);
    } else if (c.attachmentId || c.content) {
      setOpenChunk(c);
      setChunkContent(c.content || translate("workbench.citation_no_content"));
      setChunkLoading(false);
    } else {
      await copyText(`${c.name}\n${c.content}`);
    }
  };

  const isLastStreaming = (i: number) => busy && i === messages.length - 1;

  const openDocument = async (c: Citation) => {
    if (!c.datasetId || !c.docId) return;
    setDocViewer({ datasetId: c.datasetId, docId: c.docId, name: c.name });
    setDocLoading(true);
    setDocChunks([]);
    setDocView("chunks");
    setPreviewLoading(true);
    try {
      const res = await api.get<{ code: number; data: { items?: { id?: string; content?: string }[] } }>(`/chat/document/chunks?dataset=${encodeURIComponent(c.datasetId)}&doc=${encodeURIComponent(c.docId)}&page=1&page_size=100`);
      setDocChunks((res.data.data?.items ?? []).map((it) => ({ id: String(it?.id ?? ""), content: String(it?.content ?? "") })));
    } catch {
      setDocChunks([{ id: "", content: translate("workbench.doc_load_fail") }]);
    }
    setDocLoading(false);
    try {
      const pr = await api.get(`/chat/document/preview?doc=${encodeURIComponent(c.docId)}`, { responseType: "blob" });
      const blob = pr.data as Blob;
      if (previewUrl) URL.revokeObjectURL(previewUrl);
      const ct = (pr.headers["content-type"] as string) || "";
      const url = URL.createObjectURL(blob);
      setPreviewUrl(url);
      setPreviewCt(ct);
      setPreviewText(isTextPreview(ct, docViewer?.name) ? await blob.text() : "");
    } catch {
      setPreviewText("");
    }
    setPreviewLoading(false);
  };

  return (
    <div className="flex h-[calc(100svh-8rem)] min-w-0" aria-label={translate("workbench.title")}>
      <div className="flex h-full min-h-0 shrink-0">
        <SessionNav
          chats={chats}
          sessions={sessions}
          getTitle={sessionTitle}
          selectedChatId={chatId}
          selectedSessionId={selectedSessionId}
          loading={sessionsLoading}
          open={navOpen}
          expanded={expandedChats}
          onToggleOpen={() => setNavOpen((v) => !v)}
          onToggleExpand={toggleExpand}
          onSelectSession={(cid, sid) => void selectSession(cid, sid)}
          onNewSession={(cid) => startNewSession(cid)}
          onDeleteSessions={(items) => void deleteSessions(items)}
          footer={navOpen ? <CrossAppSessionIndex refreshToken={recentSessionsRefresh} className="shrink-0" /> : null}
        />
      </div>

      <div className="flex min-w-0 flex-1 flex-col p-4">
        <div className="mx-auto flex w-full max-w-4xl flex-1 flex-col gap-3">


          {chatsLoaded && chats.length === 0 ? (
            <div className="flex items-center gap-2 rounded border border-dashed px-3 py-2 text-sm text-muted-foreground">
              <CircleAlert className="size-4" />
              {translate("workbench.scope_empty")}
            </div>
          ) : null}

          <ConversationShell
            className="min-h-0 flex-1"
            messages={messages}
            busy={busy}
            messageListClassName="space-y-3"
            messagesAriaLabel={translate("workbench.messages_list")}
            commentTurn={commentTurn}
            commentText={commentText}
            onCommentTextChange={setCommentText}
            onRate={rate}
            onOpenComment={(turnId) => {
              setCommentTurn(turnId);
              setCommentText("");
            }}
            onCancelComment={() => setCommentTurn("")}
            onCopy={copyText}
            onRegenerate={regenerate}
            onOpenCitation={openSourceChunk}
            onOpenDocument={openDocument}
            onCreateKnowledgeTask={(turnId) => void createKnowledgeTask(turnId)}
            beforeMessages={
              chatsLoaded && chats.length === 0 ? (
                <div className="mb-3 flex items-center gap-2 rounded border border-dashed px-3 py-2 text-sm text-muted-foreground">
                  <CircleAlert className="size-4" />
                  {translate("workbench.scope_empty")}
                </div>
              ) : null
            }
            toolbar={
              <>
                <select
                  className="min-w-0 flex-1 rounded border bg-background px-2 py-1.5 text-sm"
                  value={chatId}
                  onChange={(e) => startNewSession(e.target.value)}
                  aria-label={translate("workbench.scope_label")}
                >
                  <option value="">{translate("workbench.scope_select")}</option>
                  {chats.map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
                </select>
                <ConversationExportMenu
                  messages={messages}
                  title={exportTitle}
                  filenamePrefix="conversation"
                />
              </>
            }
            input={
              <ChatInput
                value={input}
                onChange={setInput}
                onSend={() => void send()}
                onStop={stopGeneration}
                busy={busy}
                attachments={attachDrafts}
                onAddFiles={addAttachments}
                onRemoveFile={removeAttachment}
              />
            }
          />
        </div>
      </div>

            <CitationWindow
        open={!!openChunk}
        onOpenChange={(v) => !v && setOpenChunk(null)}
        citation={openChunk}
        content={chunkContent}
        loading={chunkLoading}
        onOpenDocument={openDocument}
      />

      <Dialog
        open={!!docViewer}
        onOpenChange={(v) => {
          if (!v) {
            if (previewUrl) URL.revokeObjectURL(previewUrl);
            setPreviewUrl(null);
            setPreviewText("");
            setPreviewCt("");
            setDocViewer(null);
          }
        }}
      >
        <DialogContent className="sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>{translate("workbench.doc_view_title")} · {docViewer?.name}</DialogTitle>
          </DialogHeader>
          <div className="mb-2 flex gap-1">
            <Button variant={docView === "chunks" ? "default" : "outline"} size="sm" onClick={() => setDocView("chunks")}>
              {translate("workbench.doc_chunks")}
            </Button>
            <Button variant={docView === "original" ? "default" : "outline"} size="sm" onClick={() => setDocView("original")}>
              {translate("workbench.doc_original")}
            </Button>
          </div>
          {docView === "chunks" ? (
            <div className="max-h-[64vh] space-y-2 overflow-y-auto text-sm">
              {docLoading ? (
                <span className="flex items-center gap-1 text-muted-foreground">
                  <Loader2 className="size-3 animate-spin" /> {translate("workbench.loading")}
                </span>
              ) : docChunks.length === 0 ? (
                <div className="text-muted-foreground">{translate("workbench.no_content")}</div>
              ) : (
                docChunks.map((ch, ci) => (
                  <div key={ch.id || ci} className="rounded border bg-muted/40 p-2">
                    <pre className="whitespace-pre-wrap">{ch.content}</pre>
                  </div>
                ))
              )}
            </div>
          ) : (
            <div className="max-h-[64vh] overflow-auto rounded-md border bg-muted/20 p-2">
              <OriginalDocumentViewer
                blobUrl={previewUrl ?? undefined}
                contentType={previewCt}
                text={previewText}
                filename={docViewer?.name}
                loading={previewLoading}
              />
            </div>
          )}
        </DialogContent>
      </Dialog>
    </div>
  );
};
