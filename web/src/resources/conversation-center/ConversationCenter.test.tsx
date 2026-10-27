import type { ReactElement } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import { ConversationCenter } from "./ConversationCenter";
import type { ConversationEvent } from "./types";

const api = vi.hoisted(() => ({
  post: vi.fn(),
  get: vi.fn(),
}));

vi.mock("../../lib/api", () => ({
  api,
  ApiError: class ApiError extends Error {
    displayMessage: string;
    constructor(public status: number, public code: number, message: string) {
      super(message);
      this.displayMessage = message;
    }
  },
}));

const notify = vi.fn();
const translate = vi.fn((key: string) => key);
const target = { id: "chat-1", kind: "chat" as const, name: "Contract Assistant" };
const unifiedAgentTarget = { id: "agent-1", kind: "agent" as const, name: "Approval Workflow", canvasCategory: "agent_canvas" };
const context = { id: "session-1", targetId: "chat-1", kind: "chat" as const, type: "session" as const, title: "合同到期如何续签" };
const adapter = {
  kind: "chat" as const,
  capabilities: () => ({ attachments: true }),
  getTarget: vi.fn().mockResolvedValue(target),
  searchTargets: vi.fn().mockResolvedValue({ items: [target], total: 1 }),
  listContexts: vi.fn().mockResolvedValue({ items: [context], total: 1 }),
  loadContext: vi.fn().mockResolvedValue({
    context,
    messages: [{ id: "m1", role: "assistant", content: "restored answer", kind: "chat", citations: [], status: "completed", createdAt: new Date().toISOString() }],
    nextCursor: undefined,
  }),
  loadMoreMessages: vi.fn().mockResolvedValue({ items: [] }),
  createSession: vi.fn(),
  start: vi.fn(),
};
const identity = { id: "user-1", tenant_id: "tenant-1" };
let assistantDirectoryTargets = [
  { id: "chat-1", kind: "chat", name: "Contract Assistant" },
  { id: "agent-1", kind: "agent", name: "Approval Workflow" },
];

vi.mock("ra-core", () => ({
  useCanAccess: () => ({ canAccess: true }),
  useGetIdentity: () => ({ data: identity, isLoading: false }),
  useNotify: () => notify,
  useTranslate: () => translate,
}));

vi.mock("./adapters", () => ({
  createAgentAdapter: () => adapter,
  createChatAdapter: () => adapter,
  createSearchAdapter: () => adapter,
}));

vi.mock("./use-assistant-directory", () => ({
  useAssistantDirectory: () => ({
    targetQuery: "",
    setTargetQuery: vi.fn(),
    targets: assistantDirectoryTargets,
    targetTotal: 2,
    targetsLoading: false,
    targetStatus: "READY",
    reset: vi.fn(),
    refresh: vi.fn(),
  }),
}));

vi.mock("../workbench/ConversationShell", () => ({
  ConversationShell: ({ messages, input, sidebar, toolbar, afterMessages, onRegenerate }: { messages: Array<{ id: string; content: string; error?: string }>; input: ReactElement; sidebar: ReactElement; toolbar?: ReactElement; afterMessages?: ReactElement; onRegenerate?: (turnId: string) => void }) => (
    <div>
      {sidebar}
      {messages.map((message) => <p key={message.id}>{message.content}</p>)}
          {onRegenerate && [...messages].reverse().find((message) => message.error) ? (
            <button type="button" onClick={() => onRegenerate([...messages].reverse().find((message) => message.error)!.id!)}>conversationCenter.retry</button>
      ) : null}
      {input}
      {toolbar}
      {afterMessages}
    </div>
  ),
}));

describe("ConversationCenter URL restore", () => {
  it("restores an authorized target and session without exposing prompt content in the URL", async () => {
    const { container } = render(
      <MemoryRouter initialEntries={["/conversation-center?kind=chat&targetId=chat-1&contextId=session-1"]}>
        <Routes>
          <Route path="/conversation-center" element={<ConversationCenter />} />
        </Routes>
      </MemoryRouter>,
    );

    await waitFor(() => expect(adapter.getTarget).toHaveBeenCalledWith("chat-1", expect.anything()));
    const restoreSignal = adapter.getTarget.mock.calls[0]?.[1]?.signal;
    expect(restoreSignal?.aborted).toBe(false);
    await waitFor(() => expect(adapter.loadContext).toHaveBeenCalledWith(expect.objectContaining({
      id: "session-1",
      targetId: "chat-1",
      kind: "chat",
      type: "session",
    })));
    await waitFor(() => expect(screen.getByText("restored answer")).toBeInTheDocument());
    expect(screen.getByRole("region", { name: "workbench.cross_app_sessions" })).toBeInTheDocument();
    expect(window.location.search).not.toContain("restored%20answer");
  });

  it("focuses the input after switching to a new conversation", async () => {
    const { container } = render(
      <MemoryRouter initialEntries={["/conversation-center?kind=chat&targetId=chat-1&contextId=session-1"]}>
        <Routes>
          <Route path="/conversation-center" element={<ConversationCenter />} />
        </Routes>
      </MemoryRouter>,
    );

    await waitFor(() => expect(screen.getByText("合同到期如何续签")).toBeInTheDocument());
    const input = container.querySelector("textarea")!;
    fireEvent.click(screen.getByRole("button", { name: "conversationCenter.new_context" }));

    expect(input).toHaveFocus();
  });
});

describe("ConversationCenter palette accessibility", () => {
  it("exposes a listbox, selectable options and keyboard state", async () => {
    const { container } = render(
      <MemoryRouter initialEntries={["/conversation-center?kind=chat&targetId=chat-1"]}>
        <Routes>
          <Route path="/conversation-center" element={<ConversationCenter />} />
        </Routes>
      </MemoryRouter>,
    );
    await waitFor(() => expect(container.querySelector("textarea")).not.toBeNull());
    const input = container.querySelector("textarea")!;
    fireEvent.change(input, { target: { value: "/" } });
    const listbox = screen.getByRole("listbox");

    expect(input).toHaveAttribute("aria-expanded", "true");
    expect(input).toHaveAttribute("aria-controls", listbox.id);
    expect(input).toHaveAttribute("aria-activedescendant", `${listbox.id}-option-0`);
    expect(screen.getAllByRole("option")[0]).toHaveAttribute("aria-selected", "true");

    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect(screen.getAllByRole("option")[1]).toHaveAttribute("aria-selected", "true");
  });
});

describe("ConversationCenter unified assistant palette", () => {
  it("lists Chat and Agent targets from the unified catalog and selects an Agent", async () => {
    adapter.getTarget.mockResolvedValueOnce(unifiedAgentTarget);
    const { container } = render(
      <MemoryRouter initialEntries={["/conversation-center?kind=chat"]}>
        <Routes>
          <Route path="/conversation-center" element={<ConversationCenter />} />
        </Routes>
      </MemoryRouter>,
    );
    const input = container.querySelector("textarea")!;
    fireEvent.change(input, { target: { value: "@" } });

    await waitFor(() => expect(screen.getByRole("option", { name: /Approval Workflow/ })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("option", { name: /Approval Workflow/ }));

    await waitFor(() => expect(adapter.getTarget).toHaveBeenCalledWith("agent-1"));
    await waitFor(() => expect(screen.getAllByText("Approval Workflow").length).toBeGreaterThan(1));
  });
});

describe("ConversationCenter Agent session creation", () => {
  it("creates a new Agent session when permitted", async () => {
    const restoredTarget = { id: "agent-1", kind: "agent" as const, name: "Approval Workflow", canvasCategory: "agent_canvas" };
    const restoredContext = { id: "session-old", targetId: "agent-1", kind: "agent" as const, type: "session" as const };
    const createdContext = { id: "session-new", targetId: "agent-1", kind: "agent" as const, type: "session" as const };
    adapter.getTarget.mockResolvedValueOnce(restoredTarget);
    adapter.listContexts.mockResolvedValueOnce({ items: [restoredContext], total: 1 });
    adapter.loadContext.mockResolvedValueOnce({ context: restoredContext, messages: [], nextCursor: undefined });
    adapter.createSession.mockResolvedValueOnce(createdContext);

    const { container } = render(
      <MemoryRouter initialEntries={["/conversation-center?kind=agent&targetId=agent-1"]}>
        <Routes>
          <Route path="/conversation-center" element={<ConversationCenter />} />
        </Routes>
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByRole("button", { name: "conversationCenter.new_context" })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "conversationCenter.new_context" }));

    await waitFor(() => expect(adapter.createSession).toHaveBeenCalledWith("agent-1"));
    await waitFor(() => expect(adapter.loadContext).toHaveBeenCalledWith(createdContext));
    expect(container.querySelector("textarea")).toHaveFocus();
  });
});

describe("ConversationCenter attachment gate", () => {
  it("submits explicit plain text from the attachment-blocked state", async () => {
    adapter.start.mockClear();
    adapter.start.mockImplementationOnce((input: { requestId: string; text: string; conversationKey: string }) => ({
      requestId: input.requestId,
      streamId: `stream-${input.requestId}`,
      abort: vi.fn(),
      events: (async function* (): AsyncGenerator<ConversationEvent> {
        yield {
          type: "stream.completed",
          eventId: "plain-text-completed",
          sequence: 1,
          timestamp: new Date().toISOString(),
          schemaVersion: 1,
          requestId: input.requestId,
          streamId: `stream-${input.requestId}`,
          conversationKey: input.conversationKey,
          target: { id: "chat-1", kind: "chat" },
          context,
          finalStatus: "completed" as const,
        };
      })(),
    }));

    const { container } = render(
      <MemoryRouter initialEntries={["/conversation-center?kind=chat&targetId=chat-1&contextId=session-1"]}>
        <Routes>
          <Route path="/conversation-center" element={<ConversationCenter />} />
        </Routes>
      </MemoryRouter>,
    );

    await waitFor(() => expect(adapter.getTarget).toHaveBeenCalledWith("chat-1", expect.anything()));
    await waitFor(() => expect(screen.getAllByText("Contract Assistant").length).toBeGreaterThan(0));
    await waitFor(() => expect(container.querySelector('input[type="file"]')).not.toBeNull());
    const fileInput = container.querySelector('input[type="file"]')!;
    const file = new File(["contract"], "contract.pdf", { type: "application/pdf" });
    fireEvent.change(fileInput, { target: { files: [file] } });
    await waitFor(() => expect(screen.getByText("contract.pdf")).toBeInTheDocument());

    const input = container.querySelector("textarea")!;
    fireEvent.change(input, { target: { value: "review contract" } });
    fireEvent.click(screen.getByRole("button", { name: "conversationCenter.send" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("conversationCenter.attachment_blocked_hint"));

    fireEvent.click(screen.getByRole("button", { name: "conversationCenter.attachment_send_plain_text" }));
    await waitFor(() => expect(adapter.start).toHaveBeenCalledTimes(1));
    expect(adapter.start.mock.calls[0]?.[0]).toMatchObject({
      text: "review contract",
      attachments: [],
    });
  });
});

describe("ConversationCenter unknown execution safety", () => {
  it("does not retry an unknown stream failure", async () => {
    const requestId = "req-1";
    adapter.start.mockClear();
    adapter.start.mockImplementationOnce(() => ({
      requestId,
      streamId: `stream-${requestId}`,
      abort: vi.fn(),
      events: (async function* (): AsyncGenerator<ConversationEvent> {
        yield {
          type: "error.occurred",
          eventId: "unknown-error",
          sequence: 1,
          timestamp: new Date().toISOString(),
          schemaVersion: 1,
          requestId,
          streamId: `stream-${requestId}`,
          conversationKey: "tenant-1:user-1:chat:chat-1:session-1",
          target: { id: "chat-1", kind: "chat" },
          context,
          error: {
            errorCode: "execution_unknown",
            displayMessage: "unknown execution",
            failureKind: "unknown",
            retryAuthorization: "none",
          },
        };
        yield {
          type: "stream.completed",
          eventId: "unknown-completed",
          sequence: 2,
          timestamp: new Date().toISOString(),
          schemaVersion: 1,
          requestId,
          streamId: `stream-${requestId}`,
          conversationKey: "tenant-1:user-1:chat:chat-1:session-1",
          target: { id: "chat-1", kind: "chat" },
          context,
          finalStatus: "failed" as const,
        };
      })(),
    }));

    const { container } = render(
      <MemoryRouter initialEntries={["/conversation-center?kind=chat&targetId=chat-1&contextId=session-1"]}>
        <Routes>
          <Route path="/conversation-center" element={<ConversationCenter />} />
        </Routes>
      </MemoryRouter>,
    );

    await waitFor(() => expect(container.querySelector("textarea")).not.toBeNull());
    const input = container.querySelector("textarea")!;
    fireEvent.change(input, { target: { value: "first question" } });
    fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() => expect(screen.getByRole("button", { name: "conversationCenter.retry" })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "conversationCenter.retry" }));
    expect(adapter.start).toHaveBeenCalledTimes(1);
  });
});

describe("ConversationCenter automatic route", () => {
  it("selects the auto target and sends the original question as the first message", { timeout: 10_000 }, async () => {
    const originalDirectoryTargets = assistantDirectoryTargets;
    assistantDirectoryTargets = [];
    try {
      await assertAutomaticRouteSendsFirstQuestion();
    } finally {
      assistantDirectoryTargets = originalDirectoryTargets;
    }
  });

  async function assertAutomaticRouteSendsFirstQuestion() {
    adapter.start.mockClear();
    api.post.mockClear();
    const autoCandidate = {
      kind: "chat",
      id: "chat-1",
      target_id: "chat-1",
      name: "Contract Assistant",
      normalized_score: 0.94,
      normalized_margin: 0.3,
      confidence: 0.94,
      confidence_status: "calibrated",
      confidence_margin: 0.3,
      candidate_count: 2,
      has_competitor: true,
      routing_readiness: 1,
      routing_readiness_type: "METADATA_COMPLETENESS",
      agent_flow_readiness: 1,
      pre_execution_risk: "low",
      auto_select_enabled: true,
      catalog_freshness_sec: 0,
      reasons: ["contract"],
    };
    const autoDecision = {
      route_id: "route-auto",
      score_status: "calibrated",
      candidate_count: 2,
      requested_mode: "suggest",
      effective_mode: "auto_low_risk",
      selected: autoCandidate,
      candidates: [autoCandidate],
      expires_at: new Date(Date.now() + 60_000).toISOString(),
      router_version: "v1-m3",
      policy_mode: "auto_low_risk",
      policy_version: "route-policy-v2:test",
      rerank_status: "not_applicable",
      route_budget_ms: 1500,
      budget_exceeded: false,
      latency_ms: 10,
    };
    const createdContext = { id: "session-auto", targetId: "chat-1", kind: "chat" as const, type: "session" as const };
    api.post.mockResolvedValueOnce({ status: 200, data: { code: 0, data: autoDecision } });
    api.post.mockResolvedValueOnce({ status: 200, data: { code: 0, data: { route_selection_id: "rs-auto", state: "NEW" } } });
    api.post.mockResolvedValueOnce({
      status: 200,
      data: {
        code: 0,
        data: {
          route_selection_id: "rs-auto",
          bootstrap_operation_id: "boot-auto",
          bootstrap_type: "CREATE_SESSION",
          bootstrap_state: "SUCCEEDED",
          session_id: "session-auto",
          kind: "chat",
          target_id: "chat-1",
        },
      },
    });
    adapter.getTarget.mockResolvedValueOnce(target);
    adapter.listContexts.mockResolvedValueOnce({ items: [], total: 0 });
    adapter.loadContext.mockResolvedValueOnce({ context: createdContext, messages: [], nextCursor: undefined });
    adapter.start.mockImplementationOnce((input: { requestId: string; text: string; conversationKey: string }) => ({
      requestId: input.requestId,
      streamId: `stream-${input.requestId}`,
      abort: vi.fn(),
      events: (async function* (): AsyncGenerator<ConversationEvent> {
        yield {
          type: "stream.completed",
          eventId: "completed",
          sequence: 1,
          timestamp: new Date().toISOString(),
          schemaVersion: 1,
          requestId: input.requestId,
          streamId: `stream-${input.requestId}`,
          conversationKey: input.conversationKey,
          target: { id: "chat-1", kind: "chat" },
          context: createdContext,
          finalStatus: "completed" as const,
        };
      })(),
    }));

    const { container } = render(
      <MemoryRouter initialEntries={["/conversation-center?kind=chat"]}>
        <Routes>
          <Route path="/conversation-center" element={<ConversationCenter />} />
        </Routes>
      </MemoryRouter>,
    );
    const input = container.querySelector("textarea")!;
    fireEvent.change(input, { target: { value: "合同到期如何续签" } });
    fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() => expect(api.post).toHaveBeenCalled());
    await waitFor(() => expect(adapter.start).toHaveBeenCalled(), { timeout: 8000 });
    expect(adapter.start.mock.calls[0]?.[0]).toMatchObject({
      target: { id: "chat-1", kind: "chat" },
      context: { id: "session-auto", type: "session" },
      text: "合同到期如何续签",
    });
    await waitFor(() => expect(container.querySelector("textarea")).toHaveValue(""));
  }
});

describe("ConversationCenter telemetry", () => {
  it("emits submission and run lifecycle events without prompt content", async () => {
    const telemetry = vi.fn();
    const context = { id: "session-1", targetId: "chat-1", kind: "chat" as const, type: "session" as const };
    adapter.start.mockImplementation((input: { requestId: string; conversationKey: string }) => ({
      requestId: input.requestId,
      streamId: `stream-${input.requestId}`,
      abort: vi.fn(),
      events: (async function* (): AsyncGenerator<ConversationEvent> {
        yield {
          type: "message.delta",
          eventId: "e1",
          sequence: 1,
          timestamp: new Date().toISOString(),
          schemaVersion: 1,
          requestId: input.requestId,
          streamId: `stream-${input.requestId}`,
          conversationKey: input.conversationKey,
          target: { id: "chat-1", kind: "chat" },
          context,
          messageId: "assistant-1",
          delta: "ok",
        };
        yield {
          type: "stream.completed",
          eventId: "e2",
          sequence: 2,
          timestamp: new Date().toISOString(),
          schemaVersion: 1,
          requestId: input.requestId,
          streamId: `stream-${input.requestId}`,
          conversationKey: input.conversationKey,
          target: { id: "chat-1", kind: "chat" },
          context,
          finalStatus: "completed" as const,
        };
      })(),
    }));

    const { container } = render(
      <MemoryRouter initialEntries={["/conversation-center?kind=chat&targetId=chat-1"]}>
        <Routes>
          <Route path="/conversation-center" element={<ConversationCenter telemetry={telemetry} />} />
        </Routes>
      </MemoryRouter>,
    );
    await waitFor(() => expect(container.querySelector("textarea")).not.toBeNull());
    const input = container.querySelector("textarea")!;
    fireEvent.change(input, { target: { value: "secret prompt" } });
    fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() => expect(telemetry.mock.calls.some(([event]) => event.name === "run_completed")).toBe(true));
    const names = telemetry.mock.calls.map(([event]) => event.name);
    expect(names).toContain("message_submitted");
    expect(names).toContain("run_started");
    expect(names).toContain("run_completed");
    expect(JSON.stringify(telemetry.mock.calls)).not.toContain("secret prompt");
  });
});
