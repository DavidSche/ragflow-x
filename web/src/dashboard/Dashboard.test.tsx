// doc/304 §4 E176: the app root used to decide where a user belongs from an
// empty identity and an empty permission list, because both queries were still
// in flight. For a chat-only user that meant painting the whole dashboard — the
// cards they cannot open, the requests they cannot make — and only then
// redirecting. These tests pin the three states of that decision: undecided,
// chat-only, and everything else.
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Dashboard } from "./Dashboard";

const state = vi.hoisted(() => ({
  identityPending: false,
  permissionsPending: false,
  identityError: false,
  permissionsError: false,
  refetch: vi.fn(),
  role: "viewer",
  permissions: [] as Array<{ resource: string; action: string }>,
}));

// Only the auth/data hooks are replaced; the rest of ra-core (the admin shell
// imports its store from here) stays real, so this test cannot pass because a
// stub returned the thing the assertion asked for.
vi.mock("ra-core", async (importOriginal) => {
  const actual = await importOriginal<typeof import("ra-core")>();
  return {
    ...actual,
    useGetIdentity: () => ({
      data: { id: "user-1", role: state.role },
      isPending: state.identityPending,
      isError: state.identityError,
      refetch: state.refetch,
    }),
    usePermissions: () => ({
      data: { permissions: state.permissions },
      isPending: state.permissionsPending,
      isError: state.permissionsError,
      refetch: state.refetch,
    }),
    useCanAccess: () => ({ canAccess: false, isPending: false }),
    useNotify: () => vi.fn(),
    useTranslate: () => (key: string) => key,
  };
});

vi.mock("../lib/api", () => ({
  api: { get: vi.fn().mockResolvedValue({ data: { code: 0, data: {} } }) },
  ApiError: class ApiError extends Error {},
}));

function LocationProbe() {
  const location = useLocation();
  return <output data-testid="location">{location.pathname}</output>;
}

function renderApp() {
  return render(
    <MemoryRouter initialEntries={["/"]}>
      <Routes>
        <Route
          path="/"
          element={
            <>
              <Dashboard />
              <LocationProbe />
            </>
          }
        />
        <Route path="/conversation-center" element={<LocationProbe />} />
      </Routes>
    </MemoryRouter>,
  );
}

afterEach(() => {
  state.identityPending = false;
  state.permissionsPending = false;
  state.identityError = false;
  state.permissionsError = false;
  state.refetch.mockClear();
  state.role = "viewer";
  state.permissions = [];
});

describe("Dashboard home routing", () => {
  it("waits instead of deciding from an empty identity and permission list", async () => {
    state.identityPending = true;
    state.permissionsPending = true;
    state.role = "custom-chat-role";
    state.permissions = [{ resource: "chat", action: "execute" }];

    renderApp();

    expect(await screen.findByTestId("home-loading")).toBeInTheDocument();
    // Still on "/" — the redirect has not been decided, and the dashboard is not
    // on screen either: only the placeholder.
    expect(screen.queryByTestId("dashboard-content")).not.toBeInTheDocument();
    expect(screen.getByTestId("location")).toHaveTextContent(/^\/$/);
  });

  it("redirects a chat-only view to the conversation centre once the probes answer", async () => {
    state.role = "custom-chat-role";
    state.permissions = [{ resource: "chat", action: "execute" }];

    renderApp();

    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent(
        "/conversation-center",
      ),
    );
  });

  it("keeps the dashboard for a view that has one", async () => {
    state.role = "tenant_admin";

    renderApp();

    await waitFor(() =>
      expect(screen.getByTestId("location")).toHaveTextContent(/^\/$/),
    );
    expect(screen.queryByTestId("home-loading")).not.toBeInTheDocument();
    expect(await screen.findByTestId("dashboard-content")).toBeInTheDocument();
  });

  // A probe that errored answered nothing. Reading that as "no identity, no
  // permissions" redirected or painted the dashboard for a user we know nothing
  // about; the root has to say so and offer a retry instead (doc/315 P3).
  it("stops on an error state instead of deciding from a failed probe", async () => {
    state.identityError = true;

    renderApp();

    expect(await screen.findByTestId("home-error")).toBeInTheDocument();
    expect(screen.queryByTestId("dashboard-content")).not.toBeInTheDocument();
    expect(screen.getByTestId("location")).toHaveTextContent(/^\/$/);

    fireEvent.click(screen.getByRole("button", { name: "dashboard.load_retry" }));
    expect(state.refetch).toHaveBeenCalled();
  });
});
