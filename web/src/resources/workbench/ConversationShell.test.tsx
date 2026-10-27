import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ConversationShell } from "./ConversationShell";

describe("<ConversationShell /> scrolling", () => {
  it("does not scroll when the feature is disabled without legacy behavior", () => {
    const scrollRef = { current: null as HTMLDivElement | null };
    const view = render(
      <ConversationShell
        messages={[{ role: "assistant", content: "initial" }]}
        scrollRef={scrollRef}
        messagesAriaLabel="messages"
      />,
    );
    const container = scrollRef.current!;
    createContainer(container);
    view.rerender(
      <ConversationShell
        messages={[{ role: "assistant", content: "new" }]}
        scrollRef={scrollRef}
        messagesAriaLabel="messages"
      />,
    );

    expect(container.scrollTop).toBe(0);
    expect(screen.getByText("new")).toBeInTheDocument();
  });

  it("preserves legacy force-bottom behavior only when explicitly requested", () => {
    const scrollRef = { current: null as HTMLDivElement | null };
    const view = render(
      <ConversationShell
        messages={[{ role: "assistant", content: "initial" }]}
        scrollRef={scrollRef}
        legacyScrollBehavior="force-bottom"
        messagesAriaLabel="messages"
      />,
    );
    const container = scrollRef.current!;
    createContainer(container);
    view.rerender(
      <ConversationShell
        messages={[{ role: "assistant", content: "new" }]}
        scrollRef={scrollRef}
        legacyScrollBehavior="force-bottom"
        messagesAriaLabel="messages"
      />,
    );

    expect(container.scrollTop).toBe(800);
  });
});

function createContainer(container: HTMLDivElement) {
  Object.defineProperty(container, "clientHeight", {
    configurable: true,
    value: 200,
  });
  Object.defineProperty(container, "scrollHeight", {
    configurable: true,
    value: 800,
  });
}

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
