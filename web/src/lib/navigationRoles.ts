export type NavigationView =
  | "platform-admin-view"
  | "workspace-admin-view"
  | "operator-view"
  | "business-user-view"
  | "read-only-view"
  | "audit-view";

export interface NavigationPermissionRule {
  resource: string;
  action: string;
  effect?: string;
}

export interface NavigationGroupProfile {
  key: string;
  labelKey: string;
  resources: string[];
}

export interface NavigationViewProfile {
  labelKey: string;
  groups: NavigationGroupProfile[];
}

export const NAVIGATION_VIEW_LABELS = {
  "platform-admin-view": "sidebar.view_platform_admin",
  "workspace-admin-view": "sidebar.view_workspace_admin",
  "operator-view": "sidebar.view_operator",
  "business-user-view": "sidebar.view_business_user",
  "read-only-view": "sidebar.view_read_only",
  "audit-view": "sidebar.view_audit",
} as const satisfies Record<NavigationView, string>;

export const NAVIGATION_BUILTIN_ROLE_VIEWS: Readonly<Record<string, NavigationView>> = {
  platform_admin: "platform-admin-view",
  tenant_admin: "workspace-admin-view",
  operator: "operator-view",
  business_user: "business-user-view",
  viewer: "read-only-view",
  team_admin: "workspace-admin-view",
} as const satisfies Record<string, NavigationView>;

const group = (
  key: string,
  labelKey: string,
  resources: readonly string[],
): NavigationGroupProfile => ({
  key,
  labelKey,
  resources: [...resources],
});

const KNOWLEDGE_RESOURCES = ["datasets", "tasks", "projects"] as const;
const ASSISTANT_APP_RESOURCES = [
  "scenario-templates",
  "agents",
  "chats",
  "search-apps",
] as const;
const INTERACTION_RESOURCES = [
  "conversation-center",
  "memories",
] as const;
const LIFECYCLE_RESOURCES = [
  "knowledge-ops",
  "asset-governance",
  "release-governance",
  "assistant-releases",
] as const;

const PLATFORM_ADMIN_GROUPS: NavigationGroupProfile[] = [
  group("workspace", "sidebar.group_workspace", [
    "tenants", "users", "teams", "roles", "enterprise-connections", "branding",
  ]),
  group("knowledge", "sidebar.group_knowledge", KNOWLEDGE_RESOURCES),
  group("assistant-apps", "sidebar.group_assistant_apps", ASSISTANT_APP_RESOURCES),
  group("interaction", "sidebar.group_interaction", INTERACTION_RESOURCES),
  group("lifecycle", "sidebar.group_lifecycle", LIFECYCLE_RESOURCES),
  group("gateway", "sidebar.group_gateway", ["model-providers", "api-keys", "usage"]),
  group("governance", "sidebar.group_governance", [
    "approvals", "approval-policies", "alerts", "alert-deliveries",
  ]),
  group("audit", "sidebar.group_audit", ["audit"]),
  group("system", "sidebar.group_system", ["system"]),
];

const WORKSPACE_ADMIN_GROUPS: NavigationGroupProfile[] = [
  group("workspace", "sidebar.group_workspace", [
    "users", "teams", "roles", "enterprise-connections", "branding",
  ]),
  group("knowledge", "sidebar.group_knowledge", KNOWLEDGE_RESOURCES),
  group("assistant-apps", "sidebar.group_assistant_apps", ASSISTANT_APP_RESOURCES),
  group("interaction", "sidebar.group_interaction", INTERACTION_RESOURCES),
  group("lifecycle", "sidebar.group_lifecycle", LIFECYCLE_RESOURCES),
  group("access-usage", "sidebar.group_access_usage", [
    "model-providers", "api-keys", "usage",
  ]),
  group("governance", "sidebar.group_governance", [
    "approvals", "approval-policies", "alerts", "alert-deliveries",
  ]),
  group("audit", "sidebar.group_audit", ["audit"]),
  group("system", "sidebar.group_system", ["system"]),
];

const OPERATOR_GROUPS: NavigationGroupProfile[] = [
  group("knowledge", "sidebar.group_knowledge", KNOWLEDGE_RESOURCES),
  group("assistant-apps", "sidebar.group_assistant_apps", ASSISTANT_APP_RESOURCES),
  group("interaction", "sidebar.group_interaction", INTERACTION_RESOURCES),
  group("lifecycle", "sidebar.group_lifecycle", LIFECYCLE_RESOURCES),
  group("access-usage", "sidebar.group_access_usage", [
    "enterprise-connections", "model-providers", "api-keys", "usage",
  ]),
  group("audit", "sidebar.group_audit", ["audit", "alerts", "alert-deliveries"]),
  group("system", "sidebar.group_system", ["system"]),
];

const BUSINESS_USER_GROUPS: NavigationGroupProfile[] = [
  group("knowledge", "sidebar.group_knowledge", ["datasets"]),
  group("assistant-apps", "sidebar.group_assistant_apps", [
    "agents", "chats", "search-apps",
  ]),
  group("interaction", "sidebar.group_interaction", INTERACTION_RESOURCES),
];

const READ_ONLY_GROUPS: NavigationGroupProfile[] = [
  group("assistant-apps", "sidebar.group_assistant_apps", ASSISTANT_APP_RESOURCES),
  group("interaction", "sidebar.group_interaction", INTERACTION_RESOURCES),
  group("knowledge", "sidebar.group_knowledge", KNOWLEDGE_RESOURCES),
  group("lifecycle", "sidebar.group_lifecycle", LIFECYCLE_RESOURCES),
  group("governance", "sidebar.group_governance", ["approvals", "approval-policies"]),
  group("access-usage", "sidebar.group_access_usage", [
    "api-keys", "usage", "alerts", "alert-deliveries",
  ]),
  group("audit", "sidebar.group_audit", ["audit"]),
  group("system", "sidebar.group_system", ["system"]),
];

const AUDIT_VIEW_GROUPS: NavigationGroupProfile[] = [
  group("audit", "sidebar.group_audit", ["audit", "alerts", "alert-deliveries"]),
  group("governance", "sidebar.group_governance", ["approvals", "approval-policies"]),
  group("lifecycle", "sidebar.group_lifecycle", LIFECYCLE_RESOURCES),
  group("assistant-apps", "sidebar.group_assistant_apps", [
    "chats", "search-apps", "agents",
  ]),
  group("interaction", "sidebar.group_interaction", INTERACTION_RESOURCES),
  group("workspace", "sidebar.group_workspace", [
    "tenants", "users", "teams", "roles",
  ]),
  group("knowledge", "sidebar.group_knowledge", [
    "projects", "datasets", "tasks",
  ]),
  group("access-usage", "sidebar.group_access_usage", [
    "enterprise-connections", "model-providers", "api-keys", "usage", "branding",
  ]),
  group("system", "sidebar.group_system", ["system"]),
];

export const NAVIGATION_VIEW_PROFILES: Record<
  NavigationView,
  NavigationViewProfile
> = {
  "platform-admin-view": {
    labelKey: NAVIGATION_VIEW_LABELS["platform-admin-view"],
    groups: PLATFORM_ADMIN_GROUPS,
  },
  "workspace-admin-view": {
    labelKey: NAVIGATION_VIEW_LABELS["workspace-admin-view"],
    groups: WORKSPACE_ADMIN_GROUPS,
  },
  "operator-view": {
    labelKey: NAVIGATION_VIEW_LABELS["operator-view"],
    groups: OPERATOR_GROUPS,
  },
  "business-user-view": {
    labelKey: NAVIGATION_VIEW_LABELS["business-user-view"],
    groups: BUSINESS_USER_GROUPS,
  },
  "read-only-view": {
    labelKey: NAVIGATION_VIEW_LABELS["read-only-view"],
    groups: READ_ONLY_GROUPS,
  },
  "audit-view": {
    labelKey: NAVIGATION_VIEW_LABELS["audit-view"],
    groups: AUDIT_VIEW_GROUPS,
  },
};

const matchesAllowedRule = (
  permissions: readonly NavigationPermissionRule[],
  resource: string,
  action: string,
): boolean =>
  permissions.some(
    (permission) =>
      permission.effect !== "deny" &&
      (permission.resource === "*" || permission.resource === resource) &&
      (permission.action === "*" || permission.action === action),
  );

export function resolveNavigationView(
  role: string | undefined | null,
  permissions: readonly NavigationPermissionRule[] = [],
): NavigationView {
  const normalizedRole = (role ?? "").toLowerCase();

  const builtInView = NAVIGATION_BUILTIN_ROLE_VIEWS[normalizedRole];
  if (builtInView) return builtInView;

  if (matchesAllowedRule(permissions, "audit", "read")) return "audit-view";
  if (
    matchesAllowedRule(permissions, "release-governance", "manage") &&
    matchesAllowedRule(permissions, "user", "read")
  ) {
    return "workspace-admin-view";
  }
  if (
    matchesAllowedRule(permissions, "dataset", "manage") ||
    matchesAllowedRule(permissions, "document", "execute") ||
    matchesAllowedRule(permissions, "enterprise-connection", "read")
  ) {
    return "operator-view";
  }
  return "read-only-view";
}

export function resolveNavigationGroups(
  view: NavigationView,
  availableResources: readonly string[],
): NavigationGroupProfile[] {
  const available = new Set(availableResources);
  return NAVIGATION_VIEW_PROFILES[view].groups
    .map((item) => ({
      ...item,
      resources: item.resources.filter((resource) => available.has(resource)),
    }))
    .filter((item) => item.resources.length > 0);
}
