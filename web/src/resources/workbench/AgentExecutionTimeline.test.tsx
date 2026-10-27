import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { AgentExecutionTimeline } from "./AgentExecutionTimeline";
import type { Message } from "./workbench-types";

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
}));

describe("<AgentExecutionTimeline />", () => {
  it("renders completed tool steps and result generation", () => {
    const message: Message = {
      role: "assistant",
      kind: "agent",
      content: "已完成",
      status: "completed",
      toolCalls: [{ name: "retrieval", status: "success", summary: "命中 2 条" }],
    };

    render(<AgentExecutionTimeline message={message} isStreaming={false} />);

    expect(screen.getByRole("region", { name: "workbench.agent_execution_timeline" })).toBeInTheDocument();
    expect(screen.getByText("retrieval")).toBeInTheDocument();
    expect(screen.getByText("命中 2 条")).toBeInTheDocument();
    expect(screen.getByText("workbench.agent_execution_generate")).toBeInTheDocument();
  });

  it("marks the current generation as running", () => {
    const message: Message = { role: "assistant", kind: "agent", content: "", status: "streaming" };

    render(<AgentExecutionTimeline message={message} isStreaming />);

    expect(screen.getByText("workbench.agent_execution_generate")).toBeInTheDocument();
    expect(screen.getByText("workbench.agent_execution_running")).toBeInTheDocument();
  });
});
