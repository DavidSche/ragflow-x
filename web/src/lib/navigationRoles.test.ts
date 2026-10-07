// ScenarioID: SC-FE-UX-001
import { describe, expect, it } from "vitest";
import {
  NAVIGATION_BUILTIN_ROLE_VIEWS,
  NAVIGATION_VIEW_LABELS,
  NAVIGATION_VIEW_PROFILES,
  resolveNavigationGroups,
  resolveNavigationView,
} from "./navigationRoles";
import { NAVIGATION_PERMISSION_ALIASES } from "./navigationPermissions";

describe("navigation views", () => {
  it("defines permission-derived views without turning them into roles", () => {
    const views = Object.keys(NAVIGATION_VIEW_PROFILES);

    expect(views).toEqual([
      "platform-admin-view",
      "workspace-admin-view",
      "operator-view",
      "business-user-view",
      "read-only-view",
      "audit-view",
    ]);
    expect(NAVIGATION_VIEW_LABELS["audit-view"]).toBe("sidebar.view_audit");
  });

  it("maps built-in roles to views without granting navigation by role", () => {
    expect(Object.keys(NAVIGATION_BUILTIN_ROLE_VIEWS).sort()).toEqual([
      "business_user", "operator", "platform_admin", "team_admin", "tenant_admin", "viewer",
    ].sort());
    expect(NAVIGATION_BUILTIN_ROLE_VIEWS.platform_admin).toBe("platform-admin-view");
    expect(NAVIGATION_BUILTIN_ROLE_VIEWS.tenant_admin).toBe("workspace-admin-view");
    expect(NAVIGATION_BUILTIN_ROLE_VIEWS.team_admin).toBe("workspace-admin-view");
    expect(NAVIGATION_BUILTIN_ROLE_VIEWS.operator).toBe("operator-view");
    expect(NAVIGATION_BUILTIN_ROLE_VIEWS.business_user).toBe("business-user-view");
    expect(NAVIGATION_BUILTIN_ROLE_VIEWS.viewer).toBe("read-only-view");
  });

  it("resolves built-in roles without granting permissions", () => {
    expect(resolveNavigationView("platform_admin")).toBe("platform-admin-view");
    expect(resolveNavigationView("tenant_admin")).toBe("workspace-admin-view");
    expect(resolveNavigationView("team_admin")).toBe("workspace-admin-view");
    expect(resolveNavigationView("operator")).toBe("operator-view");
    expect(resolveNavigationView("business_user")).toBe("business-user-view");
    expect(resolveNavigationView("viewer")).toBe("read-only-view");
    expect(resolveNavigationView("user")).toBe("read-only-view");
  });

  it("derives the audit view only from explicit allow permissions", () => {
    expect(resolveNavigationView("custom", [
      { resource: "audit", action: "read", effect: "allow" },
    ])).toBe("audit-view");
    expect(resolveNavigationView("audit_manager")).toBe("read-only-view");
  });

  it("keeps the platform administrator view lossless", () => {
    const resources = NAVIGATION_VIEW_PROFILES["platform-admin-view"].groups
      .flatMap((group) => group.resources);

    expect(new Set(resources).size).toBe(resources.length);
    expect(resources).toContain("enterprise-connections");
    expect(resources).toContain("alert-deliveries");
    expect(resources).toContain("release-governance");
  });

  it("organizes the platform admin workflow and isolates system configuration", () => {
    const groups = NAVIGATION_VIEW_PROFILES["platform-admin-view"].groups;

    expect(groups.map((group) => group.key)).toEqual([
      "workspace", "knowledge", "assistant-apps", "interaction", "lifecycle",
      "gateway", "governance", "audit", "system",
    ]);
    expect(groups.find((group) => group.key === "knowledge")?.resources).toEqual([
      "datasets", "tasks", "projects",
    ]);
    expect(groups.find((group) => group.key === "assistant-apps")?.resources).toEqual([
      "scenario-templates", "agents", "chats", "search-apps",
    ]);
    expect(groups.find((group) => group.key === "interaction")?.resources).toEqual([
      "conversation-center", "memories",
    ]);
    expect(groups.find((group) => group.key === "gateway")?.resources).toEqual([
      "model-providers", "api-keys", "usage",
    ]);
    expect(groups.find((group) => group.key === "system")?.resources).toEqual(["system"]);
    expect(groups.every((group) => group.resources.length <= 6)).toBe(true);
  });

  it("infers a custom role only from explicit allow permissions", () => {
    expect(resolveNavigationView("custom", [
      { resource: "release-governance", action: "manage", effect: "allow" },
      { resource: "user", action: "read", effect: "allow" },
    ])).toBe("workspace-admin-view");
    expect(resolveNavigationView("custom", [
      { resource: "audit", action: "read", effect: "deny" },
      { resource: "dataset", action: "manage", effect: "allow" },
    ])).toBe("operator-view");
  });

  it("filters views by available resources while preserving order", () => {
    const groups = resolveNavigationGroups("read-only-view", [
      "conversation-center", "workbench", "chats", "search-apps", "memories",
      "agents", "datasets", "projects",
    ]);

    expect(groups.map((group) => group.key)).toEqual([
      "assistant-apps", "interaction", "knowledge",
    ]);
    expect(groups[0].resources).toEqual(["agents", "chats", "search-apps"]);
    expect(groups[1].resources).toEqual([
      "conversation-center", "memories",
    ]);
    expect(groups[2].resources).toEqual(["datasets", "projects"]);
  });

  it("keeps the business user view scoped to consumption and knowledge contribution", () => {
    const resources = NAVIGATION_VIEW_PROFILES["business-user-view"].groups
      .flatMap((group) => group.resources);

    expect(resources).toEqual([
      "datasets", "agents", "chats", "search-apps",
      "conversation-center", "memories",
    ]);
    expect(resources).not.toContain("model-providers");
    expect(resources).not.toContain("api-keys");
    expect(resources).not.toContain("roles");
  });

  it("separates business-user data, assistant apps, and conversation experience", () => {
    const groups = resolveNavigationGroups("business-user-view", [
      "datasets", "agents", "chats", "search-apps", "workbench",
      "conversation-center", "memories",
    ]);

    expect(groups.map((group) => group.key)).toEqual([
      "knowledge", "assistant-apps", "interaction",
    ]);
  });

  it("keeps navigation aliases mapped to backend permissions", () => {
    expect(NAVIGATION_PERMISSION_ALIASES.map((alias) => alias.resource)).toEqual([
      "workbench", "conversation-center", "asset-governance",
    ]);
  });

  it("keeps Workbench as a redirect-only compatibility alias", () => {
    const navigationResources = Object.values(NAVIGATION_VIEW_PROFILES)
      .flatMap((view) => view.groups)
      .flatMap((group) => group.resources);

    expect(navigationResources).not.toContain("workbench");
    expect(navigationResources).toContain("conversation-center");
  });
});
