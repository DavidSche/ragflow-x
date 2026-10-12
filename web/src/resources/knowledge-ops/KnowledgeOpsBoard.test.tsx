import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { KnowledgeOpsBoard } from "./KnowledgeOpsBoard";

const apiGet = vi.fn();
const apiPost = vi.fn();

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
  useNotify: () => vi.fn(),
  useCanAccess: ({ action }: { action: string }) => ({ canAccess: action === "execute" }),
}));

vi.mock("../../lib/api", () => ({
  ApiError: class ApiError extends Error {},
  api: {
    get: (...args: unknown[]) => apiGet(...args),
    post: (...args: unknown[]) => apiPost(...args),
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
          cost_attribution: [{
            tenant_id: "tenant-1",
            project_id: "project-1",
            dataset_id: "dataset-1",
            profile_id: "profile-1",
            assistant_id: "assistant-1",
            run_id: "run-1",
            requests: 1,
            tokens_in: 120,
            tokens_out: 0,
            estimated_cost: 0.1,
            budget_token_limit: 100,
            used_tokens: 120,
            status: "exceeded",
          }],
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
          total: 2,
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
      if (url.startsWith("/knowledge-ops/compilation/recovery?")) {
        return envelope({
          items: [{
            run: {
              id: "run-1", dataset_id: "dataset-1", kind: "page_index", state: "unknown",
              recovery_state: "needs_approval", failure_code: "unknown_state", approval_id: "",
              unknown_at: "2026-09-20T00:00:00Z", recovery_first_no_match_at: "2026-09-20T01:00:00Z",
              recovery_no_match_count: 2,
            },
            evidence: { queried_at: "2026-09-20T02:00:00Z", manifest_complete: true, no_match: true, artifact_total: 0 },
            unknown_seconds: 25 * 3600,
            unknown_age_bucket: "24-72h",
          },
          {
            run: {
              id: "run-2", dataset_id: "dataset-1", kind: "page_index", state: "reconciled",
              recovery_state: "downstream_absent_confirmed", failure_code: "downstream_not_found", approval_id: "approval-1",
              unknown_at: "2026-09-20T00:00:00Z", recovery_first_no_match_at: "2026-09-20T01:00:00Z",
              recovery_no_match_count: 2,
            },
            evidence: { queried_at: "2026-09-20T02:00:00Z", manifest_complete: true, no_match: true, artifact_total: 0 },
            unknown_seconds: 25 * 3600,
            unknown_age_bucket: "24-72h",
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

  it("shows enhancement cost attribution and budget status", async () => {
    render(<KnowledgeOpsBoard />);

    await waitFor(() => expect(screen.getByText("knowledgeOps.cost_attribution_title")).toBeInTheDocument());
    expect(screen.getByText("knowledgeOps.cost_status_exceeded")).toBeInTheDocument();
    expect(screen.getByText("knowledgeOps.cost_tokens")).toBeInTheDocument();
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

  it("loads unknown recovery queue and probes safely", async () => {
    apiPost.mockResolvedValue({ data: { code: 0, data: {} } });
    render(<KnowledgeOpsBoard />);

    await waitFor(() => expect(screen.getByText("knowledgeOps.recovery_queue")).toBeInTheDocument());
    expect(screen.getByText("run-1")).toBeInTheDocument();
    expect(screen.getByText("knowledgeOps.recovery_state_needs_approval")).toBeInTheDocument();
    fireEvent.click(screen.getAllByText("knowledgeOps.recovery_probe")[0]);

    await waitFor(() => expect(apiPost).toHaveBeenCalledWith("/knowledge-ops/compilation/runs/run-1/recovery-probe", {}));
  });

  it("submits approval for two consistent no-match results", async () => {
    apiPost.mockResolvedValue({ data: { code: 0, data: {} } });
    render(<KnowledgeOpsBoard />);

    await waitFor(() => expect(screen.getByText("knowledgeOps.recovery_request_confirmation")).toBeInTheDocument());
    fireEvent.click(screen.getByText("knowledgeOps.recovery_request_confirmation"));

    await waitFor(() => expect(apiPost).toHaveBeenCalledWith(
      "/knowledge-ops/compilation/runs/run-1/recovery-approval",
      expect.objectContaining({ action: "confirm_downstream_absent" }),
    ));
  });

  it("submits approval for a confirmed replacement run", async () => {
    apiPost.mockResolvedValue({ data: { code: 0, data: {} } });
    render(<KnowledgeOpsBoard />);

    await waitFor(() => expect(screen.getAllByText("knowledgeOps.recovery_request_replacement")).toHaveLength(1));
    fireEvent.click(screen.getByText("knowledgeOps.recovery_request_replacement"));

    await waitFor(() => expect(apiPost).toHaveBeenCalledWith(
      "/knowledge-ops/compilation/runs/run-2/recovery-approval",
      expect.objectContaining({ action: "create_replacement_run" }),
    ));
  });
});
