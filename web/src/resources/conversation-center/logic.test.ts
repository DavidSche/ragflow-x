// ScenarioID: SC-WORKBENCH-001
import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  DraftStore,
  ConversationEventSink,
  RecentTargetStore,
  conversationKey,
  assertConversationContext,
  parseInputTrigger,
  replaceInputToken,
  stripLeadingCommand,
  groupContextsByTime,
} from "./logic";
import type { ConversationEvent } from "./types";

const baseEvent = {
  type: "message.delta",
  eventId: "e1",
  sequence: 1,
  timestamp: new Date().toISOString(),
  schemaVersion: 1,
  requestId: "r1",
  streamId: "s1",
  conversationKey: JSON.stringify(["tenant", "chat", "chat1", "new"]),
  target: { id: "chat1", kind: "chat" as const },
  context: { id: "session1", targetId: "chat1", kind: "chat" as const, type: "session" as const },
  messageId: "m1",
  delta: "hi",
} satisfies ConversationEvent;

describe("conversation logic", () => {
  it("isolates keys by scope, kind, target and context", () => {
    expect(conversationKey("tenant", "chat", "1", "2")).not.toBe(conversationKey("other", "chat", "1", "2"));
    expect(conversationKey("tenant", "chat", "1")).toBe(conversationKey("tenant", "chat", "1", undefined));
    expect(conversationKey("tenant", "chat", "1", "2")).not.toBe(conversationKey("tenant", "agent", "1", "2"));
  });

  it("rejects illegal context combinations", () => {
    expect(assertConversationContext({ id: "r", targetId: "search1", kind: "search", type: "run" }).id).toBe("r");
    expect(assertConversationContext({ id: "s", targetId: "chat1", kind: "chat", type: "session" }).id).toBe("s");
    expect(() => assertConversationContext({ id: "s", targetId: "chat1", kind: "chat", type: "run" as never })).toThrow();
    expect(() => assertConversationContext({ id: "s", targetId: "search1", kind: "search", type: "session" as never })).toThrow();
    expect(() => assertConversationContext({ id: "r", targetId: "agent1", kind: "agent", type: "run" as never })).toThrow();
    expect(() => assertConversationContext({ id: "", targetId: "agent1", kind: "agent", type: "session" })).toThrow();
    expect(() => assertConversationContext({ id: "s", targetId: "", kind: "agent", type: "session" })).toThrow();
  });

  it("triggers grammar only at a token start", () => {
    expect(parseInputTrigger("@合同", 3)).toEqual({ type: "mention", query: "合同", start: 0, end: 3 });
    expect(parseInputTrigger("/chat", 5)).toEqual({ type: "command", query: "chat", start: 0, end: 5 });
    expect(parseInputTrigger("/", 1)).toEqual({ type: "command", query: "", start: 0, end: 1 });
    expect(parseInputTrigger("@", 1)).toEqual({ type: "mention", query: "", start: 0, end: 1 });
    expect(parseInputTrigger("2026/09", 7)).toBeNull();
    expect(parseInputTrigger("foo@example.com", 15)).toBeNull();
    expect(parseInputTrigger("http://example.com", 18)).toBeNull();
    expect(parseInputTrigger("总结\n@合同", 6)).toEqual({ type: "mention", query: "合同", start: 3, end: 6 });
    expect(parseInputTrigger("第一条\n/chat", 9)).toEqual({ type: "command", query: "chat", start: 4, end: 9 });
    expect(parseInputTrigger("hello @world", 12)).toEqual({ type: "mention", query: "world", start: 6, end: 12 });
  });

  it("strips action prefixes and removes selected mentions", () => {
    expect(stripLeadingCommand("/new 问题")).toBe("问题");
    expect(stripLeadingCommand("什么是新会话 /new")).toBe("什么是新会话 /new");
    expect(replaceInputToken("选择 @合同 结果", { start: 3, end: 6 })).toBe("选择  结果");
  });

  it("groups conversation history by local day", () => {
    const now = new Date("2026-09-03T12:00:00").getTime();
    const contexts = [
      { id: "today", targetId: "chat1", kind: "chat" as const, type: "session" as const, title: "Today", updatedAt: now },
      { id: "yesterday", targetId: "chat1", kind: "chat" as const, type: "session" as const, title: "Yesterday", updatedAt: now - 86_400_000 },
      { id: "earlier", targetId: "chat1", kind: "chat" as const, type: "session" as const, title: "Earlier", updatedAt: now - 3 * 86_400_000 },
    ];
    expect(groupContextsByTime(contexts, now)).toEqual({
      today: [contexts[0]],
      yesterday: [contexts[1]],
      earlier: [contexts[2]],
    });
  });

  it("validates active events, schema, order and message context", () => {
    const sink = new ConversationEventSink();
    const active = { requestId: "r1", streamId: "s1", conversationKey: baseEvent.conversationKey };
    expect(sink.push(baseEvent, active).eventId).toBe("e1");
    expect(() => sink.push({ ...baseEvent, eventId: "e1", sequence: 2 }, active)).toThrow();
    expect(() => sink.push({ ...baseEvent, eventId: "e2", sequence: 3 }, active)).toThrow();
    expect(() => sink.push({ ...baseEvent, schemaVersion: 2, eventId: "e3", sequence: 2 }, active)).toThrow();
    expect(() => sink.push({ ...baseEvent, requestId: "r2", eventId: "e4", sequence: 2 }, active)).toThrow();
    expect(() => sink.push({ ...baseEvent, streamId: "s2", eventId: "e7", sequence: 2 }, active)).toThrow();
    expect(() => sink.push({ ...baseEvent, conversationKey: "other", eventId: "e8", sequence: 2 }, active)).toThrow();
    expect(() => sink.push({ ...baseEvent, context: null, eventId: "e5", sequence: 2 } as unknown as ConversationEvent, active)).toThrow();
    expect(() => sink.push({
      ...baseEvent,
      eventId: "e6",
      sequence: 2,
      context: { ...baseEvent.context, targetId: "chat2" },
    }, active)).toThrow();
  });

  it("stores and restores drafts in isolated session storage", () => {
    const storage = new Map<string, string>();
    const store = new DraftStore({
      getItem: (key) => storage.get(key) ?? null,
      setItem: (key, value) => storage.set(key, value),
      removeItem: (key) => storage.delete(key),
    });
    const key = conversationKey("tenant", "search", "search1", "run1");
    store.set(key, {
      text: "draft",
      attachments: [{ id: "file-1", name: "contract.pdf", size: 12, mime: "application/pdf", status: "done", progress: 100 }],
      selectionStart: 2,
      selectionEnd: 2,
    });
    expect(store.get(key)?.text).toBe("draft");
    expect(store.get(key)?.attachments).toHaveLength(1);
    store.delete(key);
    expect(store.get(key)).toBeUndefined();
  });

  it("keeps pre-route staged attachments in memory only", () => {
    const storage = new Map<string, string>();
    const store = new DraftStore({
      getItem: (key) => storage.get(key) ?? null,
      setItem: (key, value) => storage.set(key, value),
      removeItem: (key) => storage.delete(key),
    });
    const key = conversationKey("tenant", "chat", "none", "new");
    const staged = { id: "file-2", name: "policy.pdf", size: 24, mime: "application/pdf", status: "staged" as const, progress: 0 };
    store.set(key, { text: "draft", attachments: [staged], selectionStart: 0, selectionEnd: 0 });
    expect(store.get(key)?.attachments).toEqual([]);
  });
});

describe("recent target store", () => {
  it("keeps local recent entries at the front", () => {
    const values = new Map<string, string>();
    const store = new RecentTargetStore({ getItem: (key) => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) }, "tenant");
    store.touch({ id: "1", kind: "chat", name: "Chat" });
    store.touch({ id: "2", kind: "search", name: "Search" });
    store.touch({ id: "1", kind: "chat", name: "Chat" }, 2);
    expect(store.list().map((entry) => entry.target.id)).toEqual(["1", "2"]);
    expect(store.list()[0].source).toBe("local");
    expect(store.list()[0].displayName).toBe("Chat");
  });

  it("isolates recent targets by scope", () => {
    const values = new Map<string, string>();
    const create = (scope: string) => new RecentTargetStore({ getItem: (key) => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) }, scope);
    const target = { id: "1", kind: "chat" as const, name: "Chat" };
    create("tenant:user-1").touch(target);
    expect(create("tenant:user-2").list()).toHaveLength(0);
    expect(create("tenant:user-1").list()).toHaveLength(1);
  });

  it("invalidates a target after permission is revoked", () => {
    const values = new Map<string, string>();
    const store = new RecentTargetStore({ getItem: (key) => values.get(key) ?? null, setItem: (key, value) => values.set(key, value) }, "tenant:user-1");
    store.touch({ id: "1", kind: "chat", name: "Chat" });
    store.touch({ id: "2", kind: "agent", name: "Agent" });
    const next = store.invalidate("chat", "1");
    expect(next.map((entry) => entry.target.id)).toEqual(["2"]);
    expect(store.list().map((entry) => entry.target.id)).toEqual(["2"]);
  });
});
