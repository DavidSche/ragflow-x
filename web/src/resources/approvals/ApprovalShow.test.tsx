// ScenarioID: SC-APPROVAL-UI-001
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApprovalShow } from "./ApprovalShow";
import { ApiError } from "../../lib/api";
import type { ApprovalRecord, ApprovalStepRecord } from "./approval-types";

const mocks = vi.hoisted(() => ({
  useRecordContext: vi.fn(),
  useGetIdentity: vi.fn(),
  useCanAccess: vi.fn(),
  useRefresh: vi.fn(),
  useNotify: vi.fn(),
  useNavigate: vi.fn(),
  refresh: vi.fn(),
  notify: vi.fn(),
  navigate: vi.fn(),
  apiPost: vi.fn(),
}));

const approvalRecord: ApprovalRecord = {
  id: "approval-1",
  request_no: "APR-20260831-001",
  object_type: "dataset",
  object_id: "ds-123",
  action: "delete",
  title: "Delete test dataset",
  reason: "Testing approval workflow",
  status: "pending_approval",
  current_step: 1,
  requester_id: "user-1",
  created_at: "2026-08-31T10:00:00Z",
  submitted_at: "2026-08-31T10:00:00Z",
  expires_at: "2026-09-02T10:00:00Z",
  payload_json: '{"name": "test"}',
  snapshot_json: '{"before": {"name": "old"}}',
  result_json: "{}",
  steps: [
    {
      id: "step-1",
      step_no: 1,
      name: "Admin approval",
      approver_type: "role",
      approver_value: "tenant_admin",
      status: "current",
      due_at: "2026-09-02T10:00:00Z",
    },
  ],
} as ApprovalRecord;

// Mock ra-core hooks
vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
  useNotify: mocks.useNotify,
  useRefresh: mocks.useRefresh,
  useNavigate: mocks.useNavigate,
  useGetIdentity: mocks.useGetIdentity,
  useGetList: () => ({
    data: [{ id: "user-1", username: "user-1" }],
    isLoading: false,
  }),
  useRecordContext: mocks.useRecordContext,
  useCanAccess: mocks.useCanAccess,
  useDataProvider: () => ({
    getOne: vi.fn(),
  }),
}));

// Mock api
vi.mock("../../lib/api", () => ({
  api: {
    post: mocks.apiPost,
  },
  ApiError: class extends Error {
    status: number;
    code: number;
    traceId?: string;
    displayMessage: string;
    constructor(status: number, code: number, message: string, traceId?: string) {
      super(message);
      this.status = status;
      this.code = code;
      this.traceId = traceId;
      this.displayMessage = message;
    }
  },
}));

// Mock Show component
vi.mock("@/components/admin", () => ({
  Show: ({ children, title, actions }: any) => (
    <div>
      <h1>{title}</h1>
      <div data-testid="actions">{actions}</div>
      <div data-testid="show-content">{children}</div>
    </div>
  ),
}));

// Mock UI components
vi.mock("@/components/ui/badge", () => ({
  Badge: ({ children, variant }: any) => (
    <span data-testid="badge" data-variant={variant}>
      {children}
    </span>
  ),
}));

vi.mock("@/components/ui/button", () => ({
  Button: ({ children, onClick, disabled, variant }: any) => (
    <button onClick={onClick} disabled={disabled} data-variant={variant}>
      {children}
    </button>
  ),
}));

vi.mock("@/components/ui/dialog", () => ({
  Dialog: ({ children, open }: any) => (open ? <div data-testid="dialog">{children}</div> : null),
  DialogContent: ({ children }: any) => <div>{children}</div>,
  DialogHeader: ({ children }: any) => <div>{children}</div>,
  DialogTitle: ({ children }: any) => <h2>{children}</h2>,
  DialogFooter: ({ children }: any) => <div>{children}</div>,
}));

vi.mock("@/components/ui/textarea", () => ({
  Textarea: (props: any) => <textarea {...props} />,
}));

describe("ApprovalShow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.useRecordContext.mockReturnValue(approvalRecord);
    mocks.useGetIdentity.mockReturnValue({ data: { id: "user-1" } });
    mocks.useCanAccess.mockReturnValue({ canAccess: true });
    mocks.useRefresh.mockReturnValue(mocks.refresh);
    mocks.useNotify.mockReturnValue(mocks.notify);
    mocks.useNavigate.mockReturnValue(mocks.navigate);
  });

  it("renders the component without errors", () => {
    render(<ApprovalShow />);
    expect(screen.getByTestId("show-content")).toBeTruthy();
  });

  it("renders approval title", () => {
    render(<ApprovalShow />);
    expect(screen.getAllByText("Delete test dataset").length).toBeGreaterThan(0);
  });

  it("renders approval request number", () => {
    render(<ApprovalShow />);
    expect(screen.getByText("APR-20260831-001")).toBeTruthy();
  });

  it("renders approval status badge", () => {
    render(<ApprovalShow />);
    const badges = screen.getAllByTestId("badge");
    expect(badges.length).toBeGreaterThan(0);
  });

  it("renders approval details", () => {
    render(<ApprovalShow />);
    expect(screen.getByText("dataset")).toBeTruthy();
    expect(screen.getByText("delete")).toBeTruthy();
    expect(screen.getAllByText("Delete test dataset").length).toBeGreaterThan(0);
  });

  it("renders approval reason", () => {
    render(<ApprovalShow />);
    expect(screen.getByText("Testing approval workflow")).toBeTruthy();
  });

  it("renders steps timeline", () => {
    render(<ApprovalShow />);
    expect(screen.getByText("1. Admin approval")).toBeTruthy();
  });

  it("renders JSON sections for payload, snapshot, and result", () => {
    render(<ApprovalShow />);
    // JSON sections should be rendered with formatted JSON
    expect(screen.getByText(/name.*test/)).toBeTruthy();
    expect(screen.getByText(/before/)).toBeTruthy();
  });

  it("renders action buttons for pending approval", () => {
    render(<ApprovalShow />);
    const actions = screen.getByTestId("actions");
    expect(actions.textContent).toContain("approvals.approve_label");
    expect(actions.textContent).toContain("approvals.reject_label");
    expect(actions.textContent).toContain("approvals.cancel_label");
  });

  it("submits an approval decision once and refreshes the current record", async () => {
    const user = userEvent.setup();
    mocks.apiPost.mockResolvedValue({ data: { code: 0, data: {} } });
    render(<ApprovalShow />);

    await user.click(screen.getByRole("button", { name: "approvals.approve_label" }));
    await user.type(screen.getByLabelText("approvals.comment"), "approved by regression");
    await user.click(screen.getByRole("button", { name: "ra.action.confirm" }));

    await waitFor(() => expect(mocks.apiPost).toHaveBeenCalledTimes(1));
    expect(mocks.apiPost).toHaveBeenCalledWith("/approvals/approval-1/approve", {
      comment: "approved by regression",
    });
    await waitFor(() => expect(mocks.refresh).toHaveBeenCalledTimes(1));
    expect(mocks.notify).toHaveBeenCalledWith("approvals.approved", { type: "success" });
    expect(screen.queryByLabelText("approvals.comment")).toBeNull();
  });

  it("keeps the decision dialog and current record on an API error", async () => {
    const user = userEvent.setup();
    mocks.apiPost.mockRejectedValue(new ApiError(400, 40000, "approval state changed"));
    render(<ApprovalShow />);

    await user.click(screen.getByRole("button", { name: "approvals.reject_label" }));
    await user.type(screen.getByLabelText("approvals.comment"), "rejected by regression");
    await user.click(screen.getByRole("button", { name: "ra.action.confirm" }));

    await waitFor(() => expect(mocks.notify).toHaveBeenCalledWith("approval state changed", { type: "error" }));
    expect(mocks.refresh).not.toHaveBeenCalled();
    expect(screen.getByLabelText("approvals.comment")).toBeTruthy();
  });

  it("hides approver actions for a requester without manage permission", () => {
    mocks.useCanAccess.mockReturnValue({ canAccess: false });
    render(<ApprovalShow />);
    expect(screen.queryByRole("button", { name: "approvals.approve_label" })).toBeNull();
    expect(screen.queryByRole("button", { name: "approvals.reject_label" })).toBeNull();
    expect(screen.getByRole("button", { name: "approvals.cancel_label" })).toBeTruthy();
  });
});

describe("ApprovalShow - Execution Failed State", () => {
  it("renders retry button for execution_failed status", () => {
    mocks.useCanAccess.mockReturnValue({ canAccess: true });
    mocks.useRecordContext.mockReturnValue({
      ...approvalRecord,
      status: "execution_failed",
      steps: [],
    });
    render(<ApprovalShow />);
    expect(screen.getByRole("button", { name: "approvals.retry_label" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "approvals.approve_label" })).toBeNull();
    expect(screen.queryByRole("button", { name: "approvals.reject_label" })).toBeNull();
  });

  it("submits a retry and refreshes the current record", async () => {
    const user = userEvent.setup();
    mocks.useCanAccess.mockReturnValue({ canAccess: true });
    mocks.useRecordContext.mockReturnValue({
      ...approvalRecord,
      status: "execution_failed",
      steps: [],
    });
    mocks.apiPost.mockResolvedValue({ data: { code: 0, data: {} } });
    render(<ApprovalShow />);

    await user.click(screen.getByRole("button", { name: "approvals.retry_label" }));
    await user.type(screen.getByLabelText("approvals.comment"), "retry after upstream recovery");
    await user.click(screen.getByRole("button", { name: "ra.action.confirm" }));

    await waitFor(() => expect(mocks.apiPost).toHaveBeenCalledTimes(1));
    expect(mocks.apiPost).toHaveBeenCalledWith("/approvals/approval-1/retry", {
      comment: "retry after upstream recovery",
    });
    await waitFor(() => expect(mocks.refresh).toHaveBeenCalledTimes(1));
    expect(mocks.notify).toHaveBeenCalledWith("approvals.retried", { type: "success" });
  });
});
