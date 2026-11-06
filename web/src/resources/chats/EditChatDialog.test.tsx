import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { EditChatDialog } from "./EditChatDialog";

vi.mock("ra-core", () => ({
  useGetList: () => ({ data: [] }),
  useNotify: () => vi.fn(),
  useTranslate: () => (key: string) => key,
}));

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: invalidateQueriesMock }),
}));

const { invalidateQueriesMock } = vi.hoisted(() => ({ invalidateQueriesMock: vi.fn() }));

vi.mock("../../lib/api", () => ({
  ApiError: class ApiError extends Error {},
  api: { get: vi.fn(), put: vi.fn() },
}));

import { api } from "../../lib/api";

// Isolate the config editor tree: this regression test asserts the
// load→form→save contract (a plain save must not re-shape what the backend
// receives) without rendering the heavy ChatConfigEditor sibling.
vi.mock("./ChatConfigEditor", () => ({
  ChatConfigEditor: () => null,
}));
vi.mock("./CreateChatDialog", () => ({
  CreateChatDialog: () => null,
  DatasetSelector: () => null,
}));
vi.mock("@/components/ui/button", () => ({
  Button: (props: React.ButtonHTMLAttributes<HTMLButtonElement>) => <button type="button" {...props} />,
}));
vi.mock("@/components/ui/input", () => ({
  Input: (props: React.InputHTMLAttributes<HTMLInputElement>) => <input {...props} />,
}));
vi.mock("@/components/ui/label", () => ({
  Label: ({ children }: { children: React.ReactNode }) => <span>{children}</span>,
}));
vi.mock("@/components/ui/dialog", () => ({
  Dialog: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogContent: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogHeader: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DialogTitle: ({ children }: { children: React.ReactNode }) => <h2>{children}</h2>,
}));

// Live chat config that pre-sets name + llm_setting so the test proves a plain
// save does NOT drop the loaded knobs.
const liveConfig = {
  data: {
    code: 0,
    data: {
      name: "法规问答",
      dataset_ids: ["kb1"],
      llm_id: "m1",
      rerank_id: "rr1",
      language: "Chinese",
      top_n: 8,
      similarity_threshold: 0.2,
      vector_similarity_weight: 0.3,
      prompt_config: { system: "你是助手", prologue: "", empty_response: "" },
      llm_setting: { llm_id: "m1", temperature: 0, top_p: 0.1, max_tokens: 800 },
    },
  },
} as never;

const setup = async () => {
  vi.mocked(api.get).mockReset();
  vi.mocked(api.put).mockReset();
  invalidateQueriesMock.mockReset();
  vi.mocked(api.get).mockResolvedValue(liveConfig);
  vi.mocked(api.put).mockResolvedValue({ data: { code: 0, data: {} } } as never);
  const onOpenChange = vi.fn();
  render(
    <EditChatDialog chat={{ id: "c1", name: "旧名" }} open onOpenChange={onOpenChange} />,
  );
  await waitFor(() => expect(api.get).toHaveBeenCalledWith("/chats/c1/config"));
  return { invalidateQueriesMock, onOpenChange };
};

describe("EditChatDialog load→save contract", () => {
  it("applies a renamed chat via PUT without dropping the loaded llm_setting", async () => {
    const { invalidateQueriesMock } = await setup();
    // load 回填：旧名 → 活配置名
    const nameInput = await screen.findByPlaceholderText("chats.chat_name_placeholder");
    await waitFor(() => expect(nameInput).toHaveValue("法规问答"));
    fireEvent.change(nameInput, { target: { value: "改名后" } });
    fireEvent.click(screen.getByText("ra.action.save"));

    await waitFor(() => expect(api.put).toHaveBeenCalledWith("/chats/c1", expect.any(Object)));
    const body = vi.mocked(api.put).mock.calls[0][1] as Record<string, unknown>;
    expect(body.name).toBe("改名后");
    expect(body.dataset_ids).toEqual(["kb1"]);
    expect(body.llm_id).toBe("m1");
    expect(body.rerank_id).toBe("rr1");
    expect(body.top_n).toBe(8);
    expect(body.similarity_threshold).toBe(0.2);
    expect(body.vector_similarity_weight).toBe(0.3);
    // 加载的 llm_setting 原样回写，不被表单重整形
    expect(body.llm_setting).toEqual({ llm_id: "m1", temperature: 0, top_p: 0.1, max_tokens: 800 });
    expect(invalidateQueriesMock).toHaveBeenCalledWith({ queryKey: ["chats"] });
  });

  it("surfaces load failure as a toast and never issues a save", async () => {
    vi.mocked(api.get).mockReset();
    vi.mocked(api.put).mockReset();
    invalidateQueriesMock.mockReset();
    vi.mocked(api.get).mockRejectedValue(new Error("boom"));
    vi.mocked(api.put).mockResolvedValue({ data: { code: 0, data: {} } } as never);
    render(<EditChatDialog chat={{ id: "c1", name: "旧名" }} open onOpenChange={vi.fn()} />);
    await waitFor(() => expect(api.get).toHaveBeenCalled());
    expect(api.put).not.toHaveBeenCalled();
  });
});
