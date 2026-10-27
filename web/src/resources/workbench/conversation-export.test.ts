import { describe, expect, it, vi } from "vitest";
import { conversationToHtml, conversationToMarkdown, downloadConversation } from "./conversation-export";
import type { Message } from "./workbench-types";

describe("conversation export", () => {
    const messages: Message[] = [
      { role: "user", content: "政策是什么？" },
      {
      role: "assistant",
      content: "答案 <b>bold</b>\n\n**结论**：可用",
      id: "assistant-1",
      kind: "chat",
      citations: [{ name: "政策文档", content: "条款内容" }],
      artifacts: [{ type: "card", title: "摘要", content: "一句话" }],
      toolCalls: [{ name: "search", status: "success", summary: "检索完成" }],
      traceId: "trace-1",
      createdAt: "2026-09-02T01:00:00.000Z",
      usage: { inputTokens: 10, outputTokens: 20, totalTokens: 30 },
    },
  ];

  it("renders markdown with roles, citations and trace", () => {
    const md = conversationToMarkdown(messages, "政策问答");
    expect(md).toContain("# 政策问答");
    expect(md).toContain("## 用户");
    expect(md).toContain("## AI");
    expect(md).toContain("### 引用");
    expect(md).toContain("trace_id: `trace-1`");
    expect(md).toContain("time: `2026-09-02T01:00:00.000Z`");
    expect(md).toContain("tokens: `30`");
  });

  it("escapes model-provided html", () => {
    const html = conversationToHtml(messages, "政策问答");
    expect(html).toContain("&lt;b&gt;bold&lt;/b&gt;");
    expect(html).not.toContain("<b>bold</b>");
    expect(html).toContain("<strong>结论</strong>");
  });

  it("exports full protocol metadata as replayable json", () => {
    const created = vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:test");
    const revoke = vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
    const append = vi.spyOn(document.body, "appendChild").mockImplementation((node) => node);
    const click = vi.fn();
    const anchor = document.createElement("a") as HTMLAnchorElement & { click: () => void };
    anchor.click = click;
    const createElement = vi.spyOn(document, "createElement").mockReturnValue(anchor);

    downloadConversation(messages, "政策问答", "json");
    expect(click).toHaveBeenCalled();

    created.mockRestore();
    revoke.mockRestore();
    append.mockRestore();
    createElement.mockRestore();
  });
});
