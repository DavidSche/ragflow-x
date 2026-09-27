import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApprovalDelegationButton } from "./ApprovalDelegations";

const apiMock = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  delete: vi.fn(),
}));

vi.mock("../../lib/api", () => ({
  api: apiMock,
  ApiError: class extends Error {
    displayMessage = "api error";
  },
}));

vi.mock("ra-core", () => ({
  useTranslate: () => (key: string) => key,
  useNotify: () => vi.fn(),
  useCanAccess: () => ({ canAccess: true }),
  useGetIdentity: () => ({ data: { id: "principal-1" } }),
  useGetList: () => ({
    data: [
      { id: "principal-1", username: "principal-1" },
      { id: "delegate-1", username: "delegate-1" },
    ],
    isLoading: false,
  }),
}));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({
    isLoading: false,
    error: null,
    data: [
      {
        id: "delegation-1",
        principal_id: "principal-1",
        delegate_id: "delegate-1",
        object_type: "",
        action: "",
        starts_at: "2026-01-01T00:00:00Z",
        ends_at: "2099-01-01T00:00:00Z",
      },
    ],
    refetch: vi.fn(),
  }),
  useQueryClient: () => ({ invalidateQueries: vi.fn() }),
}));

vi.mock("@/components/ui/button", () => ({
  Button: ({ children, onClick, disabled, variant }: any) => (
    <button onClick={onClick} disabled={disabled} data-variant={variant}>{children}</button>
  ),
}));

vi.mock("@/components/ui/input", () => ({
  Input: (props: any) => <input {...props} />,
}));

vi.mock("@/components/ui/label", () => ({
  Label: ({ children, htmlFor }: any) => <label htmlFor={htmlFor}>{children}</label>,
}));

vi.mock("@/components/ui/badge", () => ({
  Badge: ({ children }: any) => <span>{children}</span>,
}));

vi.mock("@/components/ui/select", () => ({
  Select: ({ children, value, onValueChange }: any) => (
    <select value={value} onChange={(event) => onValueChange?.(event.target.value)}>{children}</select>
  ),
  SelectTrigger: ({ children }: any) => <>{children}</>,
  SelectValue: () => null,
  SelectContent: ({ children }: any) => <>{children}</>,
  SelectItem: ({ children, value }: any) => <option value={value}>{children}</option>,
}));

vi.mock("@/components/ui/dialog", () => ({
  Dialog: ({ children, open }: any) => (open ? <div>{children}</div> : null),
  DialogContent: ({ children }: any) => <div>{children}</div>,
  DialogHeader: ({ children }: any) => <div>{children}</div>,
  DialogTitle: ({ children }: any) => <h2>{children}</h2>,
  DialogFooter: ({ children }: any) => <div>{children}</div>,
}));

describe("ApprovalDelegations", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("creates a scoped delegation", async () => {
    apiMock.post.mockResolvedValue({ data: { code: 0, data: {} } });
    render(<ApprovalDelegationButton />);
    fireEvent.click(screen.getByRole("button", { name: "approvals.delegations.manage" }));
    fireEvent.change(screen.getByLabelText("approvals.delegations.delegate_id"), {
      target: { value: "delegate-2" },
    });
    fireEvent.click(screen.getByRole("button", { name: "approvals.delegations.create" }));
    await waitFor(() => expect(apiMock.post).toHaveBeenCalledWith("/approval-delegations", expect.objectContaining({
      delegate_id: "delegate-2",
      object_type: "",
      action: "",
    })));
  });

  it("shows delegation records and delete control", async () => {
    render(<ApprovalDelegationButton />);
    fireEvent.click(screen.getByRole("button", { name: "approvals.delegations.manage" }));
    expect(screen.getByText("delegate-1")).toBeTruthy();
    expect(screen.getByRole("button", { name: "ra.action.delete" })).toBeTruthy();
  });
});
