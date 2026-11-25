import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { AlertList, alertStatusPath } from "./AlertList";

vi.mock("ra-core", async (importOriginal) => ({
  ...(await importOriginal<typeof import("ra-core")>()),
  ListBase: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  useTranslate: () => (key: string) => key,
  useResourceContext: () => "alerts",
  useGetResourceLabel: () => () => "Alerts",
  useHasDashboard: () => false,
  useNotify: () => vi.fn(),
  useRefresh: () => vi.fn(),
  useRecordContext: () => null,
  List: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  ListLoadingBar: () => <div data-testid="loading" />,
  DataTable: Object.assign(
    ({ children }: { children?: ReactNode }) => <div>{children}</div>,
    { Col: () => null },
  ),
  SearchInput: () => null,
  SelectInput: () => null,
}));

vi.mock("@/components/ui/badge", () => ({
  Badge: ({ children }: { children?: ReactNode }) => <span>{children}</span>,
}));
vi.mock("@/components/ui/button", () => ({
  Button: ({ children }: { children?: ReactNode }) => <button>{children}</button>,
}));
vi.mock("@/components/admin", () => ({
  List: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  ListLoadingBar: () => <div data-testid="loading" />,
  DataTable: Object.assign(
    ({ children }: { children?: ReactNode }) => <div>{children}</div>,
    { Col: () => null },
  ),
  SearchInput: () => null,
  SelectInput: () => null,
}));
vi.mock("lucide-react", async (importOriginal) => ({
  ...(await importOriginal<typeof import("lucide-react")>()),
  BellRing: () => <span />,
  Check: () => <span />,
  UserCheck: () => <span />,
}));

describe("AlertList", () => {
  it("renders the alert worklist", () => {
    render(<AlertList />);
    expect(screen.getByTestId("loading")).toBeInTheDocument();
  });
});

// The list used to post to /alerts/<id>/claimed while the router only serves
// POST /alerts/:id/claim — a 404 that looked like a permissions problem.
describe("alertStatusPath", () => {
  it("uses the routes the backend actually serves", () => {
    expect(alertStatusPath("alert-1", "claimed")).toBe("/alerts/alert-1/claim");
    expect(alertStatusPath("alert-1", "read")).toBe("/alerts/alert-1/read");
  });

  it("keeps the id in the path so the server can identify the row", () => {
    expect(alertStatusPath("", "claimed")).toBe("/alerts//claim");
  });
});
