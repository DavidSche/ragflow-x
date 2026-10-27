import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ApprovalPoliciesList } from "./ApprovalPoliciesList";

// Mock ra-core hooks
vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
  useNotify: () => vi.fn(),
  useRefresh: () => vi.fn(),
  useRecordContext: () => ({
    id: "policy-1",
    object_type: "dataset",
    action: "delete",
    enabled: true,
    priority: 100,
    expire_hours: 72,
    version: 1,
    conditions_json: "{}",
    steps_json: '[{"step_no": 1, "name": "Admin", "approver_type": "role", "approver_value": "tenant_admin", "expire_hours": 48}]',
  }),
  useCanAccess: () => ({ canAccess: true }),
}));

// Mock api
vi.mock("../../lib/api", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  },
  ApiError: class extends Error {
    displayMessage: string;
    constructor(message: string) {
      super(message);
      this.displayMessage = message;
    }
  },
}));

// Mock List component
vi.mock("@/components/admin", () => ({
  List: ({ children, actions }: { children: React.ReactNode; actions?: React.ReactNode }) => (
    <div>
      <div data-testid="actions">{actions}</div>
      <div data-testid="list-content">{children}</div>
    </div>
  ),
  ListLoadingBar: () => <div data-testid="loading-bar" />,
  DataTable: Object.assign(
    ({ children, empty }: { children: React.ReactNode; empty?: string }) => (
      <div data-testid="data-table">
        {children}
        {empty && <div data-testid="empty-state">{empty}</div>}
      </div>
    ),
    {
      Col: ({ children, label, render, source }: any) => (
        <div data-testid={`col-${source || label}`}>
          {render ? render({ enabled: true, version: 1 }) : children}
        </div>
      ),
    }
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

vi.mock("@/components/ui/input", () => ({
  Input: (props: any) => <input {...props} />,
}));

vi.mock("@/components/ui/label", () => ({
  Label: ({ children }: any) => <label>{children}</label>,
}));

vi.mock("@/components/ui/switch", () => ({
  Switch: ({ checked, onCheckedChange, id }: any) => (
    <input
      type="checkbox"
      id={id}
      checked={checked}
      onChange={(e) => onCheckedChange?.(e.target.checked)}
    />
  ),
}));

vi.mock("@/components/ui/select", () => ({
  Select: ({ children, value, onValueChange, ...props }: any) => (
    <select {...props} value={value} onChange={(e) => onValueChange?.(e.target.value)}>
      {children}
    </select>
  ),
  SelectTrigger: ({ children }: any) => <>{children}</>,
  SelectValue: () => null,
  SelectContent: ({ children }: any) => <>{children}</>,
  SelectItem: ({ children, value }: any) => <option value={value}>{children}</option>,
}));

vi.mock("@/components/ui/dialog", () => ({
  Dialog: ({ children, open }: any) => (open ? <div data-testid="dialog">{children}</div> : null),
  DialogContent: ({ children }: any) => <div>{children}</div>,
  DialogHeader: ({ children }: any) => <div>{children}</div>,
  DialogTitle: ({ children }: any) => <h2>{children}</h2>,
  DialogFooter: ({ children }: any) => <div>{children}</div>,
}));

describe("ApprovalPoliciesList", () => {
  it("renders the component without errors", () => {
    render(<ApprovalPoliciesList />);
    expect(screen.getByTestId("list-content")).toBeTruthy();
    expect(screen.getByTestId("data-table")).toBeTruthy();
  });

  it("renders create policy button", () => {
    render(<ApprovalPoliciesList />);
    const actions = screen.getByTestId("actions");
    expect(actions.textContent).toContain("approvals.policies.create_title");
  });

  it("renders table columns", () => {
    render(<ApprovalPoliciesList />);
    expect(screen.getByTestId("col-object_type")).toBeTruthy();
    expect(screen.getByTestId("col-action")).toBeTruthy();
    expect(screen.getByTestId("col-enabled")).toBeTruthy();
    expect(screen.getByTestId("col-priority")).toBeTruthy();
    expect(screen.getByTestId("col-expire_hours")).toBeTruthy();
    expect(screen.getByTestId("col-version")).toBeTruthy();
  });

  it("renders loading bar", () => {
    render(<ApprovalPoliciesList />);
    expect(screen.getByTestId("loading-bar")).toBeTruthy();
  });
});

describe("PolicyDialog", () => {
  it("opens dialog when create button is clicked", async () => {
    render(<ApprovalPoliciesList />);
    const createButton = screen.getByRole("button", {
      name: /approvals\.policies\.create_title/,
    });
    fireEvent.click(createButton);
    expect(screen.getByTestId("dialog")).toBeTruthy();
  });

  it("loads approver options and submits a selected user", async () => {
    const post = vi.fn().mockResolvedValue({ data: { code: 0 } });
    const get = vi.fn((url: string) => Promise.resolve({
      data: {
        code: 0,
        data: url === "/users"
          ? { items: [{ id: "user-1", username: "alice" }] }
          : url === "/roles"
            ? [{ id: "tenant_admin", name: "Tenant Admin" }]
            : [{ id: "team-1", name: "Platform Team" }],
      },
    }));
    const { api } = (await import("../../lib/api")) as any;
    api.get = get;
    api.post = post;
    render(<ApprovalPoliciesList />);
    fireEvent.click(screen.getByRole("button", { name: /approvals\.policies\.create_title/ }));
    fireEvent.click(screen.getByRole("button", { name: /approvals\.policies\.add_step/ }));
    await screen.findByRole("option", { name: "alice" });
    fireEvent.change(screen.getByPlaceholderText("approvals.policies.step_name"), {
      target: { value: "Security review" },
    });
    fireEvent.change(screen.getByLabelText("approvals.policies.approver_value"), {
      target: { value: "user-1" },
    });
    fireEvent.click(screen.getByRole("button", { name: "ra.action.save" }));
    await vi.waitFor(() => expect(post).toHaveBeenCalledWith("/approval-policies", expect.objectContaining({
      steps: [expect.objectContaining({
        name: "Security review",
        approval_mode: "any",
        approvers: [{ type: "user", value: "user-1" }],
      })],
    })));
  });

  it("shows type-specific options and clears the value when approver type changes", async () => {
    const get = vi.fn((url: string) => Promise.resolve({
      data: {
        code: 0,
        data: url === "/users"
          ? { items: [{ id: "user-1", username: "alice" }] }
          : url === "/roles"
            ? [{ id: "tenant_admin", name: "Tenant Admin" }]
            : [{ id: "team-1", name: "Platform Team" }],
      },
    }));
    const { api } = (await import("../../lib/api")) as any;
    api.get = get;
    render(<ApprovalPoliciesList />);
    fireEvent.click(screen.getByRole("button", { name: /approvals\.policies\.create_title/ }));
    fireEvent.click(screen.getByRole("button", { name: /approvals\.policies\.add_step/ }));
    await vi.waitFor(() => expect(get).toHaveBeenCalledWith("/users", { params: { page: 1, page_size: 200 } }));
    await screen.findByRole("option", { name: "alice" });

    const valueSelect = screen.getByLabelText("approvals.policies.approver_value");
    expect(valueSelect).toBeTruthy();
    fireEvent.change(screen.getByLabelText("approvals.policies.approver_type"), {
      target: { value: "role" },
    });
    expect((valueSelect as HTMLSelectElement).value).not.toBe("user-1");
    await screen.findByRole("option", { name: "Tenant Admin" });
    expect(screen.queryByRole("option", { name: "alice" })).toBeNull();
  });
});
