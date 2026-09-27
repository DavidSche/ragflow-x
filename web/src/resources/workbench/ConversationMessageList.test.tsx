import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ConversationMessageList } from "./ConversationMessageList";
import type { Message } from "./workbench-types";

describe("<ConversationMessageList />", () => {
  it("renders empty state", () => {
    render(<ConversationMessageList messages={[]} emptyText="暂无消息" />);
    expect(screen.getByText("暂无消息")).toBeInTheDocument();
  });

  it("renders messages with shared bubble", () => {
    const messages: Message[] = [
      { role: "user", content: "什么是 RAGFlow-X？" },
      { role: "assistant", content: "企业级 RAG 控制面" },
    ];
    render(<ConversationMessageList messages={messages} onCopy={() => {}} />);
    expect(screen.getByText("什么是 RAGFlow-X？")).toBeInTheDocument();
    expect(screen.getByText("企业级 RAG 控制面")).toBeInTheDocument();
  });

  it("renders unified protocol metadata and safe artifacts", () => {
    const messages: Message[] = [
      { id: "m2", role: "assistant", content: "结构化回答", kind: "agent", status: "completed", createdAt: "2026-09-02T01:00:00.000Z", toolCalls: [{ name: "retrieval", status: "success", summary: "已检索 2 条" }], artifacts: [{ type: "card", title: "结论", content: "合同有效" }] },
    ];
    render(<ConversationMessageList messages={messages} onCopy={() => {}} />);
    expect(screen.getByText("结构化回答")).toBeInTheDocument();
    expect(screen.getByText("retrieval")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "workbench.agent_execution_timeline" })).toBeInTheDocument();
    expect(screen.getByText("结论")).toBeInTheDocument();
    expect(screen.getByText("2026/9/2 09:00:00")).toBeInTheDocument();
  });
});
