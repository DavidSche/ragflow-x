import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../../lib/api";
import {
  createAgentAdapter,
  createChatAdapter,
  createReportAdapter,
  createSearchAdapter,
} from "./adapters";
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

  it("restores server snapshot messages with traceable request turns", async () => {
    vi.mocked(api.get).mockResolvedValueOnce(envelope({
      items: [
        { role: "assistant", content: "first answer", request_id: "run-1" },
        { role: "assistant", content: "unmatched answer" },
      ],
    }));

    const result = await createChatAdapter().loadContext({
      id: "session-1", targetId: "chat-1", kind: "chat", type: "session",
    });
    expect(result.messages[0]).toEqual(expect.objectContaining({
      id: "assistant-run-1",
      turnId: "assistant-run-1",
    }));
    expect(result.messages[1].turnId).toBeUndefined();
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
      messageId: "assistant-run-1",
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

  it("does not repeat the answer RAGFlow resends on its final frame", async () => {
    const answer = "结论 [ID:0]";
    const frames = [
      { code: 0, data: { answer: "结论", session_id: "session-1", final: false } },
      { code: 0, data: { answer: " [ID:0]", final: false } },
      { code: 0, data: { answer, final: true, reference: { chunks: [] } } },
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
    const answerText = async () => {
      let text = "";
      for await (const event of stream.events) {
        if (event.type === "message.delta") text += event.delta;
      }
      return text;
    };
    expect(await answerText()).toBe(answer);
  });

  it("declares attachments for Chat and Agent", () => {
    expect(createChatAdapter().capabilities().attachments).toBe(true);
    expect(createSearchAdapter().capabilities().attachments).toBe(false);
    expect(createAgentAdapter().capabilities().attachments).toBe(true);
  });

  it("asks for the bindable datasets inside this workspace, not the platform catalogue", async () => {
    // A report is created in the caller's workspace and `freezeReportDatasets`
    // resolves each id there. The governance catalogue (`useGetList("datasets")`)
    // sends `scope=all` for a user with governance.read, which offered other
    // workspaces' datasets and ended in 404 `dataset not found` after the click
    // (live 4.151, 2026-11-18).
    vi.mocked(api.get).mockReturnValueOnce(
      envelope({ items: [{ id: "ds-1", name: "安全生产制度" }, { id: "", name: "broken" }] }),
    );
    const rows = await createReportAdapter().listReportDatasets();
    expect(api.get).toHaveBeenCalledWith("/datasets?scope=current&page=1&page_size=200", {
      signal: undefined,
    });
    // A row without an id cannot be bound, so it is dropped instead of rendered as
    // a chip whose click creates a 400.
    expect(rows).toEqual([{ id: "ds-1", name: "安全生产制度" }]);
  });

  it("sends a report follow-up as three keys, never as report text", async () => {
    // A14: the browser may only say *which* report/revision/section. If it could send
    // the body, the prompt would rest on an unverifiable second source of truth.
    const fetchMock = vi.fn().mockRejectedValue(new Error("no network"));
    vi.stubGlobal("fetch", fetchMock);
    try {
      const adapter = createChatAdapter();
      const stream = adapter.start({
        target: { id: "chat-1", kind: "chat" },
        text: "第 2 节的结论站得住吗？",
        requestId: "req-1",
        conversationKey: "k",
        reportContext: { report_id: "rep1", revision: 3, section_key: "compliance" },
      });
      for await (const _event of stream.events) {
        // The transport failure is irrelevant here; the payload was already sent.
      }
      const body = JSON.parse(String(fetchMock.mock.calls[0][1].body));
      expect(body.report_context).toEqual({ report_id: "rep1", revision: 3, section_key: "compliance" });
      expect(Object.keys(body.report_context)).toEqual(["report_id", "revision", "section_key"]);
      expect(body.messages).toEqual([{ role: "user", content: "第 2 节的结论站得住吗？" }]);

      fetchMock.mockClear();
      const plain = adapter.start({
        target: { id: "chat-1", kind: "chat" },
        text: "hi",
        requestId: "req-2",
        conversationKey: "k",
      });
      for await (const _event of plain.events) {
        // ditto
      }
      const plainBody = JSON.parse(String(fetchMock.mock.calls[0][1].body));
      // A turn without an attachment must not send an empty object: the server would
      // have to distinguish "absent" from "present but blank".
      expect("report_context" in plainBody).toBe(false);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("reads follow-up chips for a revision and keeps the scope in the query", async () => {
    vi.mocked(api.get).mockReturnValueOnce(
      envelope({
        report_id: "rep1",
        revision: 3,
        completeness: "partial",
        items: [{ text: "第 2 节的两个来源冲突吗？", kind: "evidence_conflict", section_key: "compliance", section_ord: 2 }],
      }),
    );
    const view = await createReportAdapter().loadFollowUps("rep1", { revision: 3, sectionKey: "compliance" });
    expect(api.get).toHaveBeenCalledWith("/reports/rep1/follow-ups?revision=3&section_key=compliance", {
      signal: undefined,
    });
    expect(view.items).toHaveLength(1);
    expect(view.completeness).toBe("partial");
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
