// ScenarioID: SC-AUDIT-002
import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { AlertDeliveryList, canManuallyRetryDelivery } from "./AlertDeliveryList";

vi.mock("ra-core", async (importOriginal) => ({
  ...(await importOriginal<typeof import("ra-core")>()),
  ListBase: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  useTranslate: () => (key: string) => key,
  useResourceContext: () => "alert-deliveries",
  useGetResourceLabel: () => () => "Alert deliveries",
  useHasDashboard: () => false,
  List: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  ListLoadingBar: () => <div data-testid="loading" />,
  DataTable: Object.assign(
    ({ children }: { children?: ReactNode }) => <div>{children}</div>,
    { Col: () => null },
  ),
  SelectInput: () => null,
}));

vi.mock("@/components/admin", () => ({
  List: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
  ListLoadingBar: () => <div data-testid="loading" />,
  DataTable: Object.assign(
    ({ children }: { children?: ReactNode }) => <div>{children}</div>,
    { Col: () => null },
  ),
  SelectInput: () => null,
}));

vi.mock("@/components/ui/badge", () => ({
  Badge: ({ children }: { children?: ReactNode }) => <span>{children}</span>,
}));

vi.mock("lucide-react", async (importOriginal) => ({
  ...(await importOriginal<typeof import("lucide-react")>()),
  Send: () => <span />,
}));

describe("AlertDeliveryList", () => {
  it("renders the read-only delivery lifecycle list", () => {
    render(<AlertDeliveryList />);
    expect(screen.getByTestId("loading")).toBeInTheDocument();
  });

  it("allows manual recovery for pending, failed and abandoned deliveries only", () => {
    expect(canManuallyRetryDelivery("pending")).toBe(true);
    expect(canManuallyRetryDelivery("failed")).toBe(true);
    expect(canManuallyRetryDelivery("abandoned")).toBe(true);
    expect(canManuallyRetryDelivery("succeeded")).toBe(false);
  });
});
