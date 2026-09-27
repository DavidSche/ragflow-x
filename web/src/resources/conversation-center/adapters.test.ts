import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../lib/api";
import { createAgentAdapter, createChatAdapter, createSearchAdapter } from "./adapters";
import { contextTitle } from "./logic";

vi.mock("../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/api")>()),
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
}));

function envelope<T>(data: T) {
  return Promise.resolve({ data: { code: 0, message: "", data } });
}

describe("conversation adapters", () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset();
    vi.mocked(api.post).mockReset();
  });

  it("maps session names, live chat knowledge scope and readable context titles", async () => {
    vi.mocked(api.get).mockImplementation((url) => {
      if (url === "/chats/chat-1/config") {
        return envelope({
          id: "chat-1",
          name: "Contract Assistant",
          kb_names: ["产品资料", "技术文档", "FAQ"],
          llm_id: "deepseek-v4-flash",
        });
      }
      if (url === "/chats/chat-1/sessions?page=1&page_size=20") {
        return envelope({ items: [{ id: "chat-session-1", name: "合同到期如何续签" }], total: 1 });
      }
      if (url === "/agents/agent-1/sessions?page=1&page_size=20") {
        return envelope({ items: [{ id: "agent-session-1", name: "处理采购审批" }], total: 1 });
      }
      return Promise.reject(new Error(`unexpected request: ${url}`));
    });

    const [chatContext] = (await createChatAdapter().listContexts("chat-1", { limit: 20 })).items;
    const [agentContext] = (await createAgentAdapter().listContexts("agent-1", { limit: 20 })).items;
    const chatTarget = await createChatAdapter().getTarget("chat-1");

    expect(chatTarget.knowledgeScope).toEqual(["产品资料", "技术文档", "FAQ"]);
    expect(chatTarget.model).toBe("deepseek-v4-flash");
    expect(contextTitle(chatContext)).toBe("合同到期如何续签");
    expect(contextTitle(agentContext)).toBe("处理采购审批");
  });

  it("only exposes workflow agents as conversation targets", async () => {
    vi.mocked(api.get).mockImplementation((url) => {
      if (url === "/agents?page=1&page_size=20&title=") {
        return envelope({
          items: [
            { id: "workflow-agent", title: "Workflow", canvas_category: "agent_canvas" },
            { id: "dataflow-agent", title: "Pipeline", canvas_category: "dataflow_canvas" },
          ],
          total: 2,
        });
      }
      if (url === "/agents/dataflow-agent/detail") {
        return envelope({ id: "dataflow-agent", title: "Pipeline", canvas_category: "dataflow_canvas" });
      }
      return Promise.reject(new Error(`unexpected request: ${url}`));
    });

    const targets = (await createAgentAdapter().searchTargets("", { limit: 20 })).items;
    expect(targets.map((target) => target.id)).toEqual(["workflow-agent"]);
  });

  it("maps session timestamps for sidebar time grouping", async () => {
    vi.mocked(api.get).mockImplementation((url) => {
      if (url === "/chats/chat-1/sessions?page=1&page_size=20") {
        return envelope({ items: [{ id: "chat-session-1", name: "合同到期如何续签", create_time: 1756800000000 }], total: 1 });
      }
      if (url === "/agents/agent-1/sessions?page=1&page_size=20") {
        return envelope({ items: [{ id: "agent-session-1", name: "处理采购审批", update_time: 1756800000000 }], total: 1 });
      }
      return Promise.reject(new Error(`unexpected request: ${url}`));
    });

    const [chatContext] = (await createChatAdapter().listContexts("chat-1", { limit: 20 })).items;
    const [agentContext] = (await createAgentAdapter().listContexts("agent-1", { limit: 20 })).items;
    expect(chatContext.updatedAt).toBe(1756800000000);
    expect(agentContext.updatedAt).toBe(1756800000000);
  });

  it("normalizes RAGFlow session citations for historical chat messages", async () => {
    vi.mocked(api.get).mockResolvedValueOnce(envelope({
      items: [{
        role: "assistant",
        content: "answer [ID:0]",
        citations: [{
          id: "chunk-1",
          content: "source text",
          dataset_id: "dataset-1",
          document_id: "doc-1",
          document_name: "policy.md",
        }],
      }],
      next_cursor: undefined,
    }));

    const result = await createChatAdapter().loadContext({
      id: "session-1", targetId: "chat-1", kind: "chat", type: "session",
    });
    expect(result.messages[0].citations).toEqual([expect.objectContaining({
      id: "chunk-1",
      name: "policy.md",
      content: "source text",
      datasetId: "dataset-1",
      docId: "doc-1",
      chunkId: "chunk-1",
    })]);
  });

  it("emits citations that arrive after the streaming message has started", async () => {
    const frames = [
      { code: 0, data: { answer: "answer", session_id: "session-1", reference: { chunks: [] } } },
      {
        code: 0,
        data: {
          answer: " [ID:0]",
          final: true,
          reference: { chunks: [{ id: "chunk-1", content: "source text", dataset_id: "dataset-1", document_id: "doc-1", document_name: "policy.md" }] },
        },
      },
      { code: 0, data: true },
    ];
    const encoder = new TextEncoder();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        for (const frame of frames) controller.enqueue(encoder.encode(`data:${JSON.stringify(frame)}\n\n`));
        controller.close();
      },
    });
    vi.stubGlobal("fetch", vi.fn(async () => new Response(body, { status: 200, headers: { "content-type": "text/event-stream" } })));

    const stream = createChatAdapter().start({
      target: { id: "chat-1", kind: "chat" },
      text: "question",
      requestId: "run-1",
      conversationKey: "conversation-key",
    });
    const events = [];
    for await (const event of stream.events) events.push(event);

    expect(events).toContainEqual(expect.objectContaining({
      type: "citation.added",
      messageId: "assistant-stream-run-1",
      citation: expect.objectContaining({ name: "policy.md", content: "source text", datasetId: "dataset-1", docId: "doc-1", chunkId: "chunk-1" }),
    }));
  });

  it("suppresses RAGFlow thinking deltas before the visible answer", async () => {
    const frames = [
      { code: 0, data: { answer: "", session_id: "session-1", start_to_think: true } },
      { code: 0, data: { answer: "English reasoning", final: false } },
      { code: 0, data: { answer: "", end_to_think: true, final: false } },
      { code: 0, data: { answer: "最终答案", final: false } },
      { code: 0, data: true },
    ];
    const encoder = new TextEncoder();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        for (const frame of frames) controller.enqueue(encoder.encode(`data:${JSON.stringify(frame)}\n\n`));
        controller.close();
      },
    });
    vi.stubGlobal("fetch", vi.fn(async () => new Response(body, { status: 200, headers: { "content-type": "text/event-stream" } })));

    const stream = createChatAdapter().start({
      target: { id: "chat-1", kind: "chat" },
      text: "question",
      requestId: "run-1",
      conversationKey: "conversation-key",
    });
    const events = [];
    for await (const event of stream.events) events.push(event);

    expect(events.filter((event) => event.type === "message.delta")).toEqual([
      expect.objectContaining({ type: "message.delta", delta: "最终答案" }),
    ]);
  });

  it("declares attachments for Chat and Agent", () => {
    expect(createChatAdapter().capabilities().attachments).toBe(true);
    expect(createSearchAdapter().capabilities().attachments).toBe(false);
    expect(createAgentAdapter().capabilities().attachments).toBe(true);
  });

  it("creates chat sessions through the chat session endpoint", async () => {
    vi.mocked(api.post!).mockReturnValueOnce(envelope({ id: "chat-session-new" }));
    const adapter = createChatAdapter();
    if (!adapter.createSession) throw new Error("chat adapter must support session creation");
    const context = await adapter.createSession("chat-1");
    expect(api.post).toHaveBeenCalledWith("/chats/chat-1/sessions", {});
    expect(context).toEqual({ id: "chat-session-new", targetId: "chat-1", kind: "chat", type: "session" });
  });
});
