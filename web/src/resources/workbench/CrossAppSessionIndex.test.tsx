import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({
  get: vi.fn(),
}));

vi.mock("../../lib/api", () => ({ api }));
vi.mock("ra-core", () => ({ useTranslate: () => (key: string) => key }));
vi.mock("react-router-dom", () => ({ useNavigate: () => navigate }));

const navigate = vi.hoisted(() => vi.fn());

import { CrossAppSessionIndex } from "./CrossAppSessionIndex";

const sessions = [
  {
    app_type: "chat",
    target_id: "chat-1",
    target_name: "Contract Assistant",
    context_id: "session-1",
    request_id: "request-1",
    title: "合同期限",
    status: "completed",
    last_activity: new Date().toISOString(),
    turn_count: 3,
  },
  {
    app_type: "search",
    target_id: "search-1",
    target_name: "Policy Search",
    context_id: "request-2",
    request_id: "request-2",
    title: "检索政策",
    status: "completed",
    last_activity: new Date().toISOString(),
    turn_count: 1,
  },
];

describe("<CrossAppSessionIndex />", () => {
  it("loads, searches and opens cross-app sessions", async () => {
    api.get.mockResolvedValue({ data: { code: 0, data: sessions } });
    render(<CrossAppSessionIndex />);

    await waitFor(() => expect(api.get).toHaveBeenCalledWith("/workbench/recent-sessions?limit=30&search="));
    expect(screen.getByText("合同期限")).toBeInTheDocument();
    expect(screen.getByText("检索政策")).toBeInTheDocument();

    const user = userEvent.setup();
    await user.click(screen.getByText("合同期限"));
    expect(navigate).toHaveBeenCalledWith("/conversation-center?kind=chat&targetId=chat-1&contextId=session-1");

    await user.type(screen.getByRole("textbox", { name: "workbench.search_cross_app_sessions" }), "合同");
    await waitFor(() => expect(api.get).toHaveBeenLastCalledWith("/workbench/recent-sessions?limit=30&search=%E5%90%88%E5%90%8C"));
  });
});
