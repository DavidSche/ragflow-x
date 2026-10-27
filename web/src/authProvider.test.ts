// ScenarioID: SC-FE-UX-001
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./lib/api", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
  },
  ApiError: class extends Error {},
  clearSession: vi.fn(),
  waitForPendingSessionClear: vi.fn(),
}));

const authMe = (approvalEnabled: boolean) => ({
  data: {
    code: 0,
    data: {
      id: "user-1",
      username: "alice",
      tenant_id: "tenant-1",
      role: "tenant_admin",
      platform: false,
      approval_enabled: approvalEnabled,
      permissions: [
        { action: "read", resource: "audit", effect: "allow" },
        { action: "read", resource: "approval", effect: "allow" },
        { action: "read", resource: "approval-policy", effect: "allow" },
      ],
    },
  },
});

describe("authProvider approval feature gate", () => {
  beforeEach(async () => {
    vi.resetModules();
    const { api } = await import("./lib/api") as any;
    vi.mocked(api.get).mockReset();
  });

  it("hides approval resources while the feature is disabled", async () => {
    const { api } = await import("./lib/api") as any;
    vi.mocked(api.get).mockResolvedValue(authMe(false));
    const { authProvider } = await import("./authProvider") as any;

    await expect(authProvider.getPermissions({})).resolves.toEqual({
      approvalsEnabled: false,
      platform: false,
      role: "tenant_admin",
      permissions: [
        { action: "read", resource: "audit", effect: "allow" },
        { action: "read", resource: "approval", effect: "allow" },
        { action: "read", resource: "approval-policy", effect: "allow" },
      ],
    });
    await expect(authProvider.canAccess({ resource: "approvals", action: "list" })).resolves.toBe(false);
    await expect(authProvider.canAccess({ resource: "approval-policies", action: "list" })).resolves.toBe(false);
    await expect(authProvider.canAccess({ resource: "audit", action: "list" })).resolves.toBe(true);
  });

  it("exposes approval resources while the feature is enabled", async () => {
    const { api } = await import("./lib/api") as any;
    vi.mocked(api.get).mockResolvedValue(authMe(true));
    const { authProvider } = await import("./authProvider") as any;

    await expect(authProvider.getPermissions({})).resolves.toEqual({
      approvalsEnabled: true,
      platform: false,
      role: "tenant_admin",
      permissions: [
        { action: "read", resource: "audit", effect: "allow" },
        { action: "read", resource: "approval", effect: "allow" },
        { action: "read", resource: "approval-policy", effect: "allow" },
      ],
    });
    await expect(authProvider.canAccess({ resource: "approvals", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "approval-policies", action: "list" })).resolves.toBe(true);
  });

  it("refreshes cached identity permissions after the TTL expires", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-01T00:00:00Z"));
    const { api } = await import("./lib/api") as any;
    vi.mocked(api.get).mockResolvedValue(authMe(false));
    const { authProvider } = await import("./authProvider") as any;

    await expect(authProvider.canAccess({ resource: "approvals", action: "list" })).resolves.toBe(false);

    vi.setSystemTime(new Date("2026-01-01T00:00:30Z"));
    vi.mocked(api.get).mockResolvedValue(authMe(true));
    await expect(authProvider.canAccess({ resource: "approvals", action: "list" })).resolves.toBe(true);
    expect(api.get).toHaveBeenCalledTimes(2);
    vi.useRealTimers();
  });
});

describe("authProvider CRUD action mapping", () => {
  beforeEach(async () => {
    vi.resetModules();
    const { api } = await import("./lib/api") as any;
    vi.mocked(api.get).mockReset();
  });

  it("maps frontend create access to backend manage access", async () => {
    const { api } = await import("./lib/api") as any;
    vi.mocked(api.get).mockResolvedValue({
      data: {
        code: 0,
        data: {
          id: "user-1", username: "alice", tenant_id: "tenant-1", role: "tenant_admin",
          platform: false, approval_enabled: true,
          permissions: [
            { action: "read", resource: "project", effect: "allow" },
            { action: "manage", resource: "project", effect: "allow" },
            { action: "read", resource: "dataset", effect: "allow" },
          ],
        },
      },
    });
    const { authProvider } = await import("./authProvider") as any;

    await expect(authProvider.canAccess({ resource: "projects", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "projects", action: "create" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "datasets", action: "create" })).resolves.toBe(false);
  });
});

describe("authProvider login/logout session ordering", () => {
  beforeEach(async () => {
    vi.resetModules();
    const mocks = await import("./lib/api") as any;
    vi.mocked(mocks.api.get).mockReset();
    vi.mocked(mocks.api.post).mockReset();
    vi.mocked(mocks.clearSession).mockReset();
    vi.mocked(mocks.waitForPendingSessionClear).mockReset();
  });

  it("waits for a pending logout before issuing a new login", async () => {
    const { api, waitForPendingSessionClear } = await import("./lib/api") as any;
    const { authProvider } = await import("./authProvider") as any;
    let waited = false;
    vi.mocked(waitForPendingSessionClear).mockImplementation(async () => {
      waited = true;
    });
    vi.mocked(api.post).mockImplementation(async () => {
      expect(waited).toBe(true);
      return { data: { code: 0, data: { token: "token" } } };
    });

    await expect(authProvider.login({ username: "admin", password: "admin123" })).resolves.toBeUndefined();
    expect(waitForPendingSessionClear).toHaveBeenCalledTimes(1);
    expect(api.post).toHaveBeenCalledWith("/auth/login", { username: "admin", password: "admin123" });
  });

  it("does not redirect before the logout request completes", async () => {
    const { clearSession } = await import("./lib/api") as any;
    const { authProvider } = await import("./authProvider") as any;
    let resolveLogout: (() => void) | undefined;
    vi.mocked(clearSession).mockImplementation(() => new Promise<void>((resolve) => {
      resolveLogout = resolve;
    }));

    const pending = authProvider.logout();
    expect(clearSession).toHaveBeenCalledTimes(1);
    resolveLogout?.();
    await expect(pending).resolves.toBeUndefined();
  });
});
