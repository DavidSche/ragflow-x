import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { KnowledgeOpsBoard } from "./KnowledgeOpsBoard";

const apiGet = vi.fn();

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
  useNotify: () => vi.fn(),
  useCanAccess: () => ({ canAccess: false }),
}));

vi.mock("../../lib/api", () => ({
  ApiError: class ApiError extends Error {},
  api: {
    get: (...args: unknown[]) => apiGet(...args),
  },
}));

const envelope = <T,>(data: T) => Promise.resolve({ data: { code: 0, data } });

describe("<KnowledgeOpsBoard />", () => {
  beforeEach(() => {
    apiGet.mockReset();
    apiGet.mockImplementation((url: string) => {
      if (url.startsWith("/knowledge-ops?")) {
        return envelope({
          total_turns: 1,
          active_users: 1,
          active_sessions: 1,
          completed: 1,
          no_answer: 0,
          failed: 0,
          with_citations: 1,
          tokens_in: 10,
          tokens_out: 5,
          positive: 0,
          negative: 1,
          attribution_summary: { knowledge: 1, unclassified: 0 },
          citation_rate: 1,
          no_answer_rate: 0,
          failure_rate: 0,
          satisfaction_rate: 0,
          avg_latency_ms: 100,
          avg_resolution_hours: 0,
        });
      }
      if (url.startsWith("/knowledge-ops/top-queries?")) return envelope([]);
      if (url.startsWith("/knowledge-ops/events?")) {
        return envelope({
          items: [{
            id: "event-1",
            request_id: "request-1",
            app_type: "chat",
            app_id: "chat-1",
            question: "badcase",
            status: "completed",
            citations_count: 0,
            duration_ms: 100,
            tokens_in: 10,
            tokens_out: 5,
            created_at: "2026-09-20T00:00:00Z",
            review_status: "open",
            feedback_rating: "negative",
            feedback_attribution: "knowledge",
          }],
          total: 1,
        });
      }
      if (url.startsWith("/knowledge-tasks/summary")) {
        return envelope({ open: 1, in_progress: 1, blocked: 0, pending_approval: 0, resolved: 2, canceled: 0, overdue: 0 });
      }
      if (url.startsWith("/knowledge-tasks?")) {
        return envelope({
          items: [{
            id: "task-1",
            source_event_id: "event-1",
            source_request_id: "request-1",
            source_attribution: "knowledge",
            title: "Update policy document",
            description: "Add the missing rule",
            category: "knowledge",
            owner_id: "owner-1",
            due_at: "2026-09-21T00:00:00Z",
            priority: "high",
            status: "in_progress",
            requires_approval: false,
            regression_eval_set_id: "set-1",
            regression_eval_case_id: "case-1",
            regression_status: "pending",
            created_at: "2026-09-20T00:00:00Z",
          }],
          total: 1,
        });
      }
      return envelope({ items: [], total: 0 });
    });
  });

  it("loads attribution summary and shows structured feedback attribution", async () => {
    render(<KnowledgeOpsBoard />);

    await waitFor(() => expect(screen.getByText("knowledgeOps.pending_badcases")).toBeInTheDocument());
    expect(screen.getAllByText("knowledgeOps.attribution_knowledge").length).toBeGreaterThan(1);
    expect(apiGet).toHaveBeenCalledWith(
      expect.stringContaining("/knowledge-ops/events?"),
    );
  });

  it("filters pending badcases by attribution", async () => {
    render(<KnowledgeOpsBoard />);
    await waitFor(() => expect(screen.getByText("knowledgeOps.pending_badcases")).toBeInTheDocument());

    fireEvent.change(screen.getByLabelText("knowledgeOps.attribution_title"), {
      target: { value: "knowledge" },
    });

    await waitFor(() => expect(apiGet.mock.calls.some(([url]) => String(url).includes("attribution=knowledge"))).toBe(true));
  });

  it("loads knowledge task loop data", async () => {
    render(<KnowledgeOpsBoard />);

    await waitFor(() => expect(screen.getByText("knowledgeOps.task_title")).toBeInTheDocument());
    expect(screen.getByText("Update policy document")).toBeInTheDocument();
    expect(screen.getByText("knowledgeOps.task_status_in_progress")).toBeInTheDocument();
    expect(apiGet).toHaveBeenCalledWith(expect.stringContaining("/knowledge-tasks/summary"));
  });
});
