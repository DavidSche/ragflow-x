import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ConversationShell } from "./ConversationShell";

describe("<ConversationShell />", () => {
  it("shares one shell across sidebar, toolbar, messages and input", () => {
    render(
      <ConversationShell
        messages={[{ id: "m1", role: "assistant", content: "统一答案" }]}
        sidebar={<div>会话列表</div>}
        toolbar={<div>操作栏</div>}
        input={<div>输入区</div>}
        messagesAriaLabel="统一消息"
      />,
    );

    expect(screen.getByText("会话列表")).toBeInTheDocument();
    expect(screen.getByText("操作栏")).toBeInTheDocument();
    expect(screen.getByText("统一答案")).toBeInTheDocument();
    expect(screen.getByText("输入区")).toBeInTheDocument();
    expect(screen.getByLabelText("统一消息")).toBeInTheDocument();
  });
});
