// ScenarioID: SC-WORKBENCH-001
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MessageBubble } from "./MessageBubble";
import type { Message } from "./workbench-types";

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
}));

const baseMessage: Message = {
  id: "message-1",
  role: "assistant",
  content: "知识库回答 [ID:0]",
  kind: "agent",
  turnId: "turn-1",
  status: "completed",
  citations: [{ name: "manual.pdf", content: "操作步骤" }],
  artifacts: [{ type: "card", title: "结论", content: "合同有效" }],
  toolCalls: [{ name: "retrieval", status: "success", summary: "命中 2 条" }],
  model: "deepseek-v4-flash",
  createdAt: "2026-09-02T01:00:00.000Z",
  usage: { totalTokens: 128 },
};

describe("<MessageBubble />", () => {
  it("renders answer, citations, artifacts and unified metadata", () => {
    const { container } = render(
      <MessageBubble
        message={baseMessage}
        index={0}
        isLastStreaming={false}
        onCopy={vi.fn()}
        onOpenCitation={vi.fn()}
      />,
    );

    expect(container.querySelector(".workbench-answer")?.textContent).toContain("知识库回答");
    expect(screen.getByText("结论")).toBeInTheDocument();
    expect(screen.getByText("合同有效")).toBeInTheDocument();
    expect(screen.getByText("retrieval")).toBeInTheDocument();
    expect(screen.getByText("deepseek-v4-flash")).toBeInTheDocument();
    expect(screen.getByText("128 tokens")).toBeInTheDocument();
    expect(screen.getByText("2026/9/2 09:00:00")).toBeInTheDocument();
  });

  it("invokes copy, feedback and regenerate actions", () => {
    const onCopy = vi.fn();
    const onRate = vi.fn();
    const onOpenComment = vi.fn();
    const onRegenerate = vi.fn();
    render(
      <MessageBubble
        message={baseMessage}
        index={0}
        isLastStreaming={false}
        onCopy={onCopy}
        onRate={onRate}
        onOpenComment={onOpenComment}
        onRegenerate={onRegenerate}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "workbench.copy_answer" }));
    fireEvent.click(screen.getByRole("button", { name: "workbench.helpful" }));
    fireEvent.click(screen.getByRole("button", { name: "workbench.needs_improvement" }));
    fireEvent.click(screen.getByRole("button", { name: "workbench.regenerate" }));

    expect(onCopy).toHaveBeenCalledWith("知识库回答 [ID:0]");
    expect(onRate).toHaveBeenCalledWith("turn-1", "positive");
    expect(onOpenComment).toHaveBeenCalledWith("turn-1");
    expect(onRegenerate).toHaveBeenCalledWith("turn-1");
  });

  it("shows the stable error code for failed turns", () => {
    const onRegenerate = vi.fn();
    render(
      <MessageBubble
        message={{ role: "assistant", content: "", error: "模型调用失败", errorCode: "model_failure" }}
        index={0}
        isLastStreaming={false}
        onCopy={vi.fn()}
        onRegenerate={onRegenerate}
      />,
    );
    expect(screen.getByText("模型调用失败")).toBeInTheDocument();
    expect(screen.getAllByText("model_failure").length).toBeGreaterThan(0);
    expect(screen.getByRole("alert").textContent).toContain("\u6a21\u578b\u8c03\u7528\u5931\u8d25");
    fireEvent.click(screen.getByRole("button", { name: "workbench.retry" }));
    expect(onRegenerate).toHaveBeenCalledTimes(1);
  });

  it("restores focus to the negative feedback input", () => {
    render(
      <MessageBubble
        message={baseMessage}
        index={0}
        isLastStreaming={false}
        commentTurn="turn-1"
        commentText="????"
        onCopy={vi.fn()}
      />,
    );

    expect(screen.getByLabelText("workbench.feedback_label")).toHaveFocus();
  });

  it("requires an attribution category before submitting negative feedback", async () => {
    const onRate = vi.fn();
    render(
      <MessageBubble
        message={baseMessage}
        index={0}
        isLastStreaming={false}
        commentTurn="turn-1"
        commentText="答案错误"
        onCopy={vi.fn()}
        onRate={onRate}
        onOpenComment={vi.fn()}
      />,
    );

    const submit = screen.getByRole("button", { name: "workbench.feedback_submit" });
    expect(submit).toBeDisabled();
    fireEvent.change(screen.getByLabelText("workbench.attribution_label"), {
      target: { value: "knowledge" },
    });
    expect(submit).toBeEnabled();
    fireEvent.click(submit);

    expect(onRate).toHaveBeenCalledWith("turn-1", "negative", "答案错误", "knowledge");
  });

  it("creates a knowledge task from negative feedback and no-answer states", () => {
    const onCreateKnowledgeTask = vi.fn();
    const { rerender } = render(
      <MessageBubble
        message={baseMessage}
        index={0}
        isLastStreaming={false}
        commentTurn="turn-1"
        commentText="答案错误"
        onCopy={vi.fn()}
        onCreateKnowledgeTask={onCreateKnowledgeTask}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "workbench.create_knowledge_task" }));
    expect(onCreateKnowledgeTask).toHaveBeenCalledWith("turn-1");

    onCreateKnowledgeTask.mockClear();
    rerender(
      <MessageBubble
        message={{ role: "assistant", content: "", turnId: "turn-2", citations: [] }}
        index={1}
        isLastStreaming={false}
        onCopy={vi.fn()}
        onCreateKnowledgeTask={onCreateKnowledgeTask}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "workbench.create_knowledge_task" }));
    expect(onCreateKnowledgeTask).toHaveBeenCalledWith("turn-2");
  });
});
