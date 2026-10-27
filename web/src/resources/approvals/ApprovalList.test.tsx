// ScenarioID: SC-APPROVAL-UI-001
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/api";
import { ApprovalList } from "./ApprovalList";

const mocks = vi.hoisted(() => ({
  useListContext: vi.fn(),
  useCanAccess: vi.fn(),
  useNotify: vi.fn(),
  useGetIdentity: vi.fn(),
  useRefresh: vi.fn(),
  onUnselectItems: vi.fn(),
  refetch: vi.fn(),
  setFilters: vi.fn(),
  notify: vi.fn(),
  invalidateQueries: vi.fn(),
  apiPost: vi.fn(),
  apiGet: vi.fn(),
  navigate: vi.fn(),
}));

// Mock ra-core hooks
vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
  useNotify: mocks.useNotify,
  useRefresh: mocks.useRefresh,
  useGetIdentity: mocks.useGetIdentity,
  useListContext: mocks.useListContext,
  useCanAccess: mocks.useCanAccess,
  useRecordContext: () => null,
  useNavigate: () => mocks.navigate,
  useCreatePath: () => ({ resource, type }: { resource: string; type: string }) =>
    `/${resource}/${type}`,
}));

// Mock api
vi.mock("../../lib/api", () => ({
  api: {
    get: mocks.apiGet,
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

// Mock List component to render children directly
vi.mock("@/components/admin", () => ({
  List: ({ actions, children }: { actions?: React.ReactNode; children: React.ReactNode }) => (
    <div>
      {actions && <div data-testid="actions">{actions}</div>}
      {children}
    </div>
  ),
  ListLoadingBar: () => <div data-testid="loading-bar" />,
  DataTable: Object.assign(
    ({ children, empty, bulkActionButtons }: { children: React.ReactNode; empty?: string; bulkActionButtons?: React.ReactNode }) => (
      <div data-testid="data-table">
        <div data-testid="bulk-actions">{bulkActionButtons}</div>
        {children}
        {empty && <div data-testid="empty-state">{empty}</div>}
      </div>
    ),
    {
      Col: ({ children, label, render, source }: any) => (
        <div data-testid={`col-${source || label}`}>
          {render ? render({}) : children}
        </div>
      ),
    }
  ),
  SearchInput: (props: any) => <input data-testid="search-input" placeholder={props.placeholder} />,
  SelectInput: (props: any) => <select data-testid={`select-${props.source}`} />,
  TextInput: (props: any) => <input data-testid={`text-${props.source}`} />,
  DateInput: (props: any) => <input data-testid={`date-${props.source}`} />,
  FilterButton: () => <button data-testid="filter-button">Filter</button>,
  FilterForm: ({ children }: { children: React.ReactNode }) => <form>{children}</form>,
}));

// Mock UI components
vi.mock("@/components/ui/tabs", () => ({
  Tabs: ({ children, value }: any) => (
    <div data-testid="tabs" data-value={value}>
      {children}
    </div>
  ),
  TabsList: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  TabsTrigger: ({ children, value }: any) => (
    <button data-testid={`tab-${value}`}>
      {children}
    </button>
  ),
}));

vi.mock("@/components/ui/badge", () => ({
  Badge: ({ children, variant }: any) => (
    <span data-testid="badge" data-variant={variant}>
      {children}
    </span>
  ),
}));

vi.mock("@/components/ui/button", () => ({
  Button: ({ children, onClick, disabled, variant, size }: any) => (
    <button onClick={onClick} disabled={disabled} data-variant={variant} data-size={size}>
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

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: mocks.invalidateQueries }),
}));

describe("ApprovalList", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.useGetIdentity.mockReturnValue({ data: { id: "user-1" } });
    mocks.useNotify.mockReturnValue(mocks.notify);
    mocks.useCanAccess.mockReturnValue({ canAccess: true });
    mocks.useListContext.mockReturnValue({
      filterValues: { scope: "pending_for_me", status: "pending_approval" },
      displayedFilters: {},
      setFilters: mocks.setFilters,
      selectedIds: ["approval-1"],
      onUnselectItems: mocks.onUnselectItems,
      refetch: mocks.refetch,
    });
  });

  it("renders the component without errors", () => {
    render(<ApprovalList />);
    expect(screen.getByTestId("tabs")).toBeTruthy();
    expect(screen.getByTestId("data-table")).toBeTruthy();
  });

  it("renders scope tabs", () => {
    render(<ApprovalList />);
    expect(screen.getByTestId("tab-pending_for_me")).toBeTruthy();
    expect(screen.getByTestId("tab-created_by_me")).toBeTruthy();
    expect(screen.getByTestId("tab-all")).toBeTruthy();
  });

  it("renders table columns", () => {
    render(<ApprovalList />);
    expect(screen.getByTestId("col-request_no")).toBeTruthy();
    expect(screen.getByTestId("col-object_type")).toBeTruthy();
    expect(screen.getByTestId("col-action")).toBeTruthy();
    expect(screen.getByTestId("col-title")).toBeTruthy();
    expect(screen.getByTestId("col-current_step")).toBeTruthy();
    expect(screen.getByTestId("col-status")).toBeTruthy();
    expect(screen.getByTestId("col-created_at")).toBeTruthy();
    expect(screen.getByTestId("col-expires_at")).toBeTruthy();
  });

  it("renders loading bar", () => {
    render(<ApprovalList />);
    expect(screen.getByTestId("loading-bar")).toBeTruthy();
  });

  it("renders bulk approval actions for managers", () => {
    render(<ApprovalList />);
    expect(screen.getByRole("button", { name: /approvals\.bulk\.approve/ })).toBeTruthy();
    expect(screen.getByRole("button", { name: /approvals\.bulk\.reject/ })).toBeTruthy();
  });

  it("exports approvals using the current approver filter", async () => {
    const user = userEvent.setup();
    const createObjectURL = vi.fn(() => "blob:approvals");
    const revokeObjectURL = vi.fn();
    const originalCreateObjectURL = URL.createObjectURL;
    const originalRevokeObjectURL = URL.revokeObjectURL;
    URL.createObjectURL = createObjectURL;
    URL.revokeObjectURL = revokeObjectURL;
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    mocks.useListContext.mockReturnValue({
      filterValues: { scope: "pending_for_me", status: "pending_approval", approver_id: "user-1" },
      displayedFilters: {},
      setFilters: mocks.setFilters,
      selectedIds: ["approval-1"],
      onUnselectItems: mocks.onUnselectItems,
      refetch: mocks.refetch,
    });
    mocks.apiGet.mockResolvedValue({ data: new Blob(["request_no\nAPR-1\n"]) });
    render(<ApprovalList />);

    try {
      await user.click(screen.getByRole("button", { name: "ra.action.export" }));
      await waitFor(() => expect(mocks.apiGet).toHaveBeenCalledWith(
        "/approvals/export?status=pending_approval&approver_id=user-1",
        { responseType: "blob" },
      ));
      expect(createObjectURL).toHaveBeenCalledTimes(1);
      expect(click).toHaveBeenCalledTimes(1);
      expect(revokeObjectURL).toHaveBeenCalledWith("blob:approvals");
      expect(mocks.notify).toHaveBeenCalledWith("approvals.exported", { type: "success" });
    } finally {
      click.mockRestore();
      URL.createObjectURL = originalCreateObjectURL;
      URL.revokeObjectURL = originalRevokeObjectURL;
    }
  });

  it("shows a structured error when export fails", async () => {
    const user = userEvent.setup();
    mocks.apiGet.mockRejectedValue(new ApiError(400, 40095, "export result is too large"));
    render(<ApprovalList />);

    await user.click(screen.getByRole("button", { name: "ra.action.export" }));

    await waitFor(() => expect(mocks.notify).toHaveBeenCalledWith(
      "export result is too large",
      { type: "error" },
    ));
  });

  it("hides export action without approval read permission", () => {
    mocks.useCanAccess.mockReturnValue({ canAccess: false });
    render(<ApprovalList />);
    expect(screen.queryByRole("button", { name: "ra.action.export" })).toBeNull();
  });

  it("submits batch approval, reports partial results and refreshes the list", async () => {
    const user = userEvent.setup();
    mocks.useListContext.mockReturnValue({
      filterValues: { scope: "pending_for_me", status: "pending_approval" },
      displayedFilters: {},
      setFilters: mocks.setFilters,
      selectedIds: ["approval-1", "approval-2"],
      onUnselectItems: mocks.onUnselectItems,
      refetch: mocks.refetch,
    });
    mocks.apiPost.mockResolvedValue({
      data: { code: 0, data: [{ id: "approval-1", ok: true }, { id: "approval-2", ok: false }] },
    });
    render(<ApprovalList />);

    await user.click(screen.getByRole("button", { name: /approvals\.bulk\.approve/ }));
    await user.type(screen.getByLabelText("approvals.comment"), "bulk decision");
    await user.click(screen.getByRole("button", { name: "ra.action.confirm" }));

    await waitFor(() => expect(mocks.apiPost).toHaveBeenCalledWith("/approvals/batch-approve", {
      ids: ["approval-1", "approval-2"],
      comment: "bulk decision",
    }));
    await waitFor(() => expect(mocks.notify).toHaveBeenCalledWith(
      "approvals.bulk.partial",
      { type: "warning", messageArgs: { success: "1", failed: "1" } },
    ));
    await waitFor(() => expect(mocks.invalidateQueries).toHaveBeenCalledWith({ queryKey: ["approvals"] }));
    expect(mocks.onUnselectItems).toHaveBeenCalledTimes(1);
    expect(mocks.refetch).toHaveBeenCalledTimes(1);
  });

  it("shows a backend failure without clearing the selection", async () => {
    const user = userEvent.setup();
    mocks.apiPost.mockRejectedValue(Object.assign(new Error("rejected"), { displayMessage: "decision rejected" }));
    render(<ApprovalList />);

    await user.click(screen.getByRole("button", { name: /approvals\.bulk\.reject/ }));
    await user.type(screen.getByLabelText("approvals.comment"), "bulk decision");
    await user.click(screen.getByRole("button", { name: "ra.action.confirm" }));

    await waitFor(() => expect(mocks.notify).toHaveBeenCalledWith("approvals.bulk.failed", { type: "error" }));
    expect(mocks.onUnselectItems).not.toHaveBeenCalled();
    expect(mocks.invalidateQueries).not.toHaveBeenCalled();
    expect(screen.getByLabelText("approvals.comment")).toBeTruthy();
  });

  it("hides bulk approval actions from non-managers", () => {
    mocks.useCanAccess.mockReturnValue({ canAccess: false });
    render(<ApprovalList />);
    expect(screen.queryByRole("button", { name: /approvals\.bulk\.approve/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /approvals\.bulk\.reject/ })).toBeNull();
  });

  it("disables bulk decisions above the backend batch limit", () => {
    mocks.useListContext.mockReturnValue({
      filterValues: { scope: "all" },
      displayedFilters: {},
      setFilters: mocks.setFilters,
      selectedIds: Array.from({ length: 101 }, (_, index) => `approval-${index}`),
      onUnselectItems: mocks.onUnselectItems,
      refetch: mocks.refetch,
    });
    render(<ApprovalList />);
    expect(screen.getByRole("button", { name: /approvals\.bulk\.approve/ })).toHaveProperty("disabled", true);
    expect(screen.getByRole("button", { name: /approvals\.bulk\.reject/ })).toHaveProperty("disabled", true);
    expect(screen.getByText("approvals.bulk.too_many")).toBeTruthy();
  });
});
