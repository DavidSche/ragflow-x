// chat 详情页的"新建 / 复制会话"是除对话中心之外唯一会创建 session 的入口。
// doc/304 §4 E155 把这件事从 `chat|manage` 拆成了 `chat|session:create`，
// doc/315 复审发现这个页面没有跟着改：只有 `chat|read` 的账号看得到按钮，
// 点了必然被服务端拒绝。这里钉住三件事：没有权限就不渲染按钮、探测未决时
// 渲染成禁用而不是消失、有权限时照常出现。
import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ChatShow } from "./ChatShow";

const mocks = vi.hoisted(() => ({
  useCanAccess: vi.fn(),
  useRecordContext: vi.fn(),
  notify: vi.fn(),
  apiGet: vi.fn(),
  apiPost: vi.fn(),
  apiPatch: vi.fn(),
  apiDelete: vi.fn(),
}));

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
  useNotify: () => mocks.notify,
  useCanAccess: mocks.useCanAccess,
  useRecordContext: mocks.useRecordContext,
}));

vi.mock("@/components/admin", () => ({
  Show: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));

vi.mock("../../lib/api", () => ({
  api: {
    get: mocks.apiGet,
    post: mocks.apiPost,
    patch: mocks.apiPatch,
    delete: mocks.apiDelete,
  },
  ApiError: class extends Error {
    status: number;
    displayMessage: string;
    constructor(status: number, _code: number, message: string) {
      super(message);
      this.status = status;
      this.displayMessage = message;
    }
  },
}));

const CHAT = { id: "chat-1", name: "合同助手" };

const sessionPayload = {
  data: { code: 0, data: { items: [{ id: "session-1", name: "既有会话" }], total: 1 } },
};

function grant(value: { canAccess?: boolean; isPending?: boolean }) {
  mocks.useCanAccess.mockReturnValue({ ...value, error: undefined });
}

beforeEach(() => {
  vi.clearAllMocks();
  mocks.useRecordContext.mockReturnValue(CHAT);
  mocks.apiGet.mockResolvedValue(sessionPayload);
  mocks.apiPost.mockResolvedValue({ data: { code: 0, data: { id: "session-2" } } });
});

describe("ChatShow session creation", () => {
  it("hides every session-creating button without chats|session:create", async () => {
    grant({ canAccess: false, isPending: false });
    render(<ChatShow />);
    await waitFor(() => expect(mocks.apiGet).toHaveBeenCalled());
    expect(screen.queryByText("chats.session_new")).toBeNull();
    expect(screen.queryByTitle("chats.duplicate")).toBeNull();
    // The sessions themselves are readable — only the write affordance is gone,
    // so the page must not have collapsed into an error.
    expect(screen.getByText("既有会话")).toBeTruthy();
  });

  it("renders the button disabled while the probe is in flight", async () => {
    grant({ canAccess: undefined, isPending: true });
    render(<ChatShow />);
    const button = await screen.findByText("chats.session_new");
    expect((button.closest("button") as HTMLButtonElement).disabled).toBe(true);
  });

  it("offers the button when the permission is granted", async () => {
    grant({ canAccess: true, isPending: false });
    render(<ChatShow />);
    const button = await screen.findByText("chats.session_new");
    expect((button.closest("button") as HTMLButtonElement).disabled).toBe(false);
  });
});
