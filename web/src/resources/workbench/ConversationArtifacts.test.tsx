import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ConversationArtifacts } from "./ConversationArtifacts";

describe("<ConversationArtifacts />", () => {
  it("renders whitelisted tables, cards, links and charts", () => {
    render(
      <ConversationArtifacts
        artifacts={[
          { type: "table", title: "月度指标", data: { columns: ["月份", "工单"], rows: [["1月", 12]] } },
          { type: "card", title: "政策摘要", content: "保留原格式" },
          { type: "link", title: "操作手册", url: "https://example.com/manual" },
          { type: "chart", title: "趋势", data: { type: "bar", labels: ["一月", "二月"], datasets: [{ label: "量", data: [2, 4] }] } },
        ]}
      />,
    );

    expect(screen.getByText("月度指标")).toBeInTheDocument();
    expect(screen.getByText("政策摘要")).toBeInTheDocument();
    expect(screen.getByText("保留原格式")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "操作手册" })).toHaveAttribute("href", "https://example.com/manual");
    expect(screen.getByRole("img", { name: "趋势" })).toBeInTheDocument();
  });
});
