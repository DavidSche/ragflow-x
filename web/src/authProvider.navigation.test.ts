// ScenarioID: SC-FE-UX-001
// ScenarioID: SC-AUDIT-002
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./lib/api", () => ({
  api: { get: vi.fn(), post: vi.fn() },
  ApiError: class extends Error {},
  clearSession: vi.fn(),
}));

const allow = (resource: string, action: string) => ({ action, resource, effect: "allow" });
const viewerPermissions = [
  ...["user", "team", "project", "dataset", "document", "task", "usage", "dashboard", "knowledge-ops", "chat", "search-app", "assistant", "scenario-template", "prompt-policy", "knowledge-lifecycle", "eval-set", "release-governance"]
    .map((resource) => allow(resource, "read")),
];
const operatorPermissions = [
  ...viewerPermissions,
  allow("task", "execute"),
  allow("chat", "execute"),
  allow("assistant", "execute"),
  allow("search-app", "execute"),
  allow("alert", "read"),
  allow("memory", "execute"),
  allow("memory", "read"),
  allow("memory", "manage"),
  allow("agent", "execute"),
  allow("agent", "read"),
  allow("agent", "manage"),
  allow("agent", "session:create"),
  allow("knowledge-ops", "manage"),
  allow("prompt-policy", "manage"),
  allow("eval-set", "manage"),
];
const adminPermissions = [
  ...operatorPermissions,
  allow("tenant", "read"),
  allow("tenant", "governance.manage"),
  allow("role", "read"),
  allow("role", "manage"),
  allow("enterprise-connection", "read"),
  allow("model-provider", "read"),
  allow("api-key", "read"),
  allow("approval", "read"),
  allow("approval-policy", "read"),
  allow("audit", "read"),
  allow("alert", "manage"),
  allow("release-governance", "manage"),
];
const businessUserPermissions = [
  allow("user", "read"),
  allow("dataset", "read"),
  allow("document", "read"),
  allow("document", "append"),
  allow("assistant", "read"),
  allow("assistant", "execute"),
  allow("chat", "read"),
  allow("chat", "execute"),
  allow("search-app", "read"),
  allow("search-app", "execute"),
  allow("memory", "read"),
  allow("memory", "execute"),
  allow("agent", "read"),
  allow("agent", "execute"),
  allow("agent", "session:create"),
];

const authMe = (permissions: unknown[]) => ({
  data: {
    code: 0,
    data: {
      id: "user-1", username: "alice", tenant_id: "tenant-1", role: "operator",
      platform: false, approval_enabled: true, permissions,
    },
  },
});

describe("authProvider navigation permission matrix", () => {
  beforeEach(async () => {
    vi.resetModules();
    const { api } = await import("./lib/api") as any;
    vi.mocked(api.get).mockReset();
  });

  it("does not grant platform navigation by role alone", async () => {
    const { api } = await import("./lib/api") as any;
    vi.mocked(api.get).mockResolvedValue({
      data: {
        code: 0,
        data: {
          id: "user-1", username: "limited", tenant_id: "tenant-1", role: "connection-manager",
          platform: true, approval_enabled: true, permissions: [allow("enterprise-connection", "read")],
        },
      },
    });
    const { authProvider } = await import("./authProvider") as any;

    await expect(authProvider.canAccess({ resource: "enterprise-connections", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "users", action: "list" })).resolves.toBe(false);
  });

  it("maps governance resources to backend RBAC names", async () => {
    const { api } = await import("./lib/api") as any;
    vi.mocked(api.get).mockResolvedValue(authMe(adminPermissions));
    const { authProvider } = await import("./authProvider") as any;

    await expect(authProvider.canAccess({ resource: "alerts", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "alert-deliveries", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "scenario-templates", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "release-governance", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "enterprise-connections", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "roles", action: "manage" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "tenants", action: "governance.manage" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "asset-governance", action: "list" })).resolves.toBe(true);
  });

  it("keeps viewer navigation read-only", async () => {
    const { api } = await import("./lib/api") as any;
    vi.mocked(api.get).mockResolvedValue(authMe(viewerPermissions));
    const { authProvider } = await import("./authProvider") as any;

    await expect(authProvider.canAccess({ resource: "alerts", action: "list" })).resolves.toBe(false);
    await expect(authProvider.canAccess({ resource: "scenario-templates", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "release-governance", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "asset-governance", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "workbench", action: "list" })).resolves.toBe(false);
  });

  it("keeps administrator resources hidden from operators", async () => {
    const { api } = await import("./lib/api") as any;
    vi.mocked(api.get).mockResolvedValue(authMe(operatorPermissions));
    const { authProvider } = await import("./authProvider") as any;

    await expect(authProvider.canAccess({ resource: "alerts", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "scenario-templates", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "asset-governance", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "workbench", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "model-providers", action: "list" })).resolves.toBe(false);
    await expect(authProvider.canAccess({ resource: "api-keys", action: "list" })).resolves.toBe(false);
    await expect(authProvider.canAccess({ resource: "roles", action: "manage" })).resolves.toBe(false);
  });

  it("allows business users to use assistants and append documents only", async () => {
    const { api } = await import("./lib/api") as any;
    vi.mocked(api.get).mockResolvedValue(authMe(businessUserPermissions));
    const { authProvider } = await import("./authProvider") as any;

    await expect(authProvider.canAccess({ resource: "workbench", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "conversation-center", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "datasets", action: "list" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "document", action: "append" })).resolves.toBe(true);
    await expect(authProvider.canAccess({ resource: "document", action: "execute" })).resolves.toBe(false);
    await expect(authProvider.canAccess({ resource: "dataset", action: "manage" })).resolves.toBe(false);
    await expect(authProvider.canAccess({ resource: "model-providers", action: "list" })).resolves.toBe(false);
    await expect(authProvider.canAccess({ resource: "roles", action: "list" })).resolves.toBe(false);
  });
});
