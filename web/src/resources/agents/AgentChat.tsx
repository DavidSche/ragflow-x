import { useEffect, useRef, useState } from "react";
import { useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Send, Square } from "lucide-react";
import { ApiError, api } from "../../lib/api";
import { ConversationShell } from "../workbench/ConversationShell";
import { ConversationExportMenu } from "../workbench/ConversationExportMenu";
import { classifyConversationError, streamRead, type Message } from "../workbench/workbench-types";

interface SessionLike {
  id: string;
  name?: string;
}

interface Props {
  agentId: string;
  sessions: SessionLike[];
  createSession: (name: string) => Promise<void>;
  messageListClassName?: string;
}

export function AgentChat({ agentId, sessions, createSession, messageListClassName }: Props) {
  const t = useTranslate();
  const notify = useNotify();
  const [sessionId, setSessionId] = useState("");
  const [messages, setMessages] = useState<Message[]>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const abortRef = useRef<AbortController | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!sessions[0]) return;
    if (!sessions.some((s) => s.id === sessionId)) {
      setSessionId(sessions[0].id);
    }
  }, [sessions]); // eslint-disable-line react-hooks/exhaustive-deps

  // Load session message history when sessionId changes
  useEffect(() => {
    if (!sessionId) {
      setMessages([]);
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const res = await api.get<{ code: number; data: any }>(
          `/agents/${agentId}/sessions/${sessionId}`,
        );
        if (cancelled) return;
        const sessionData = res.data?.data;
        const rawMessages: any[] = Array.isArray(sessionData?.message)
          ? sessionData.message
          : Array.isArray(sessionData?.messages)
            ? sessionData.messages
            : [];
        const loaded: Message[] = rawMessages
          .filter((m: any) => m.role === "user" || m.role === "assistant")
          .map((m: any, index: number) => ({
            role: m.role as "user" | "assistant",
            content: m.content ?? "",
            turnId: `${sessionId}-${index}`,
            id: `${sessionId}-${index}`,
            kind: "agent",
            createdAt: m.create_time ? new Date(Number(m.create_time) * 1000).toISOString() : undefined,
            status: "completed",
          }));
        setMessages(loaded);
      } catch {
        // Best-effort: if loading fails, start with empty messages
        if (!cancelled) setMessages([]);
      }
    })();
    return () => { cancelled = true; };
  }, [agentId, sessionId]);

  // Auto-scroll to bottom when messages change
  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [messages]);

  const stopStreaming = () => {
    abortRef.current?.abort();
    abortRef.current = null;
    setBusy(false);
  };

  const send = async () => {
    const text = input.trim();
    if (!text || !sessionId || busy) return;

    const userMsg: Message = {
      id: `${sessionId}-${messages.length}-user`,
      role: "user", content: text, turnId: `${sessionId}-${messages.length}`, kind: "agent",
      createdAt: new Date().toISOString(), status: "completed",
    };
    const allMessages = [...messages, userMsg];
    setMessages([...allMessages, { id: `${userMsg.turnId}-assistant`, role: "assistant", content: "", turnId: userMsg.turnId, kind: "agent", createdAt: new Date().toISOString(), status: "streaming" }]);
    setInput("");
    setBusy(true);

    const controller = new AbortController();
    abortRef.current = controller;

    try {
      const res = await fetch(`/api/v1/agents/${agentId}/chat/completions/stream`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          session_id: sessionId,
          messages: allMessages,
        }),
        signal: controller.signal,
      });

      if (!res.ok || !res.body) {
        const failText = res.ok ? "Empty response body" : `HTTP ${res.status}`;
        setMessages((m) => m.map((message, index) => index === m.length - 1 && message.role === "assistant"
          ? { ...message, content: message.content || `⚠️ ${failText}`, status: "failed", error: failText, errorCode: classifyConversationError(res.status, failText) }
          : message));
        return;
      }

      await streamRead(
        res.body,
        (delta) => {
          setMessages((m) => m.map((message, index) => index === m.length - 1 && message.role === "assistant"
            ? { ...message, content: message.content + delta, status: "streaming" }
            : message));
        },
        (message) => {
          setMessages((m) => m.map((item, index) => index === m.length - 1 && item.role === "assistant"
            ? { ...item, status: "failed", error: message, errorCode: classifyConversationError(undefined, message) }
            : item));
        },
      );
      setMessages((m) => {
        const next = [...m];
        const last = next[next.length - 1];
        if (last && last.role === "assistant" && !last.error) last.status = "completed";
        return next;
      });
    } catch (err) {
      if (err instanceof DOMException && err.name === "AbortError") {
        // User cancelled — keep whatever was streamed so far
        return;
      }
      const fail = err instanceof ApiError ? err.displayMessage : String(err);
      setMessages((m) => m.map((message, index) => index === m.length - 1 && message.role === "assistant"
        ? { ...message, content: message.content || `⚠️ ${fail}`, status: "failed", error: fail, errorCode: classifyConversationError(undefined, fail) }
        : message));
    } finally {
      abortRef.current = null;
      setBusy(false);
    }
  };

  const exportTitle = sessions.find((s) => s.id === sessionId)?.name || sessionId || "agent-conversation";

  return (
    <ConversationShell
      className={messageListClassName ?? "max-h-64 min-h-40"}
      messages={messages}
      busy={busy}
      emptyText="选择会话后输入问题开始对话。"
      scrollRef={scrollRef}
      messageListClassName="space-y-3"
      messagesAriaLabel="Agent 对话消息"
      toolbar={
        <>
          <select
            className="min-w-0 flex-1 rounded border bg-background px-2 py-1 text-sm"
            value={sessionId}
            onChange={(e) => setSessionId(e.target.value)}
          >
            <option value="">选择会话</option>
            {sessions.map((s) => (
              <option key={s.id} value={s.id}>
                {s.name || s.id}
              </option>
            ))}
          </select>
          {!sessionId && (
            <Button size="sm" variant="outline" onClick={() => void createSession(`session-${Date.now()}`)}>
              新建会话
            </Button>
          )}
          {sessions.length > 0 ? (
            <ConversationExportMenu
              messages={messages}
              title={exportTitle}
              filenamePrefix="agent-conversation"
            />
          ) : null}
        </>
      }
      input={
        <div className="flex gap-2">
          <Input
            placeholder="输入问题"
            value={input}
            disabled={!sessionId || busy}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                if (busy) {
                  stopStreaming();
                } else {
                  void send();
                }
              }
            }}
          />
          {busy ? (
            <Button onClick={stopStreaming} size="sm" variant="destructive">
              <Square className="size-4" aria-hidden="true" />
              停止
            </Button>
          ) : (
            <Button onClick={() => void send()} disabled={!sessionId || busy} size="sm">
              <Send className="size-4" aria-hidden="true" />
              发送
            </Button>
          )}
        </div>
      }
      onCopy={async (content) => {
        try {
          await navigator.clipboard.writeText(content);
          notify("已复制", { type: "success" });
        } catch {
          notify("复制失败", { type: "error" });
        }
      }}
    />
  );
}
