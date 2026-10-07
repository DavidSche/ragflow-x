import type { AuthProvider } from "ra-core";
import { api, ApiError, clearSession, waitForPendingSessionClear } from "./lib/api";
import { NAVIGATION_PERMISSION_ALIASES } from "./lib/navigationPermissions";

interface Envelope<T> {
  code: number;
  message: string;
  data: T;
  trace_id?: string;
}

interface LoginPayload {
  token: string;
  refresh_token: string;
}

interface MePermission {
  action: string;
  resource: string;
  effect: string;
}

interface MePayload {
  id: string;
  username: string;
  email?: string;
  tenant_id: string;
  role: string;
  platform?: boolean;
  approval_enabled?: boolean;
  permissions?: MePermission[];
}

let identityCache: MePayload | null | undefined;
let identityCacheAt = 0;
const IDENTITY_CACHE_TTL_MS = 30_000;

async function checkIdentity(): Promise<MePayload | null> {
  if (identityCache !== undefined && Date.now() - identityCacheAt < IDENTITY_CACHE_TTL_MS) return identityCache;
  try {
    const res = await api.get<Envelope<MePayload>>("/auth/me");
    identityCache = res.data.code === 0 ? res.data.data : null;
  } catch {
    identityCache = null;
  }
  identityCacheAt = Date.now();
  return identityCache;
}

// Maps each frontend resource (shown in the sidebar) to the RBAC (resource,
// action) pair that its list view requires. Keeps navigation aligned with the
// backend permission matrix.
const RESOURCE_ACCESS: Record<string, { resource: string; action: string }> = {
  tenants: { resource: "tenant", action: "read" },
  users: { resource: "user", action: "read" },
  teams: { resource: "team", action: "read" },
  roles: { resource: "role", action: "read" },
  projects: { resource: "project", action: "read" },
  "enterprise-connections": { resource: "enterprise-connection", action: "read" },
  datasets: { resource: "dataset", action: "read" },
  tasks: { resource: "task", action: "read" },
  workbench: { resource: "chat", action: "execute" },
  chats: { resource: "chat", action: "read" },
  "search-apps": { resource: "search-app", action: "read" },
  memories: { resource: "memory", action: "read" },
  agents: { resource: "agent", action: "read" },
  "model-providers": { resource: "model-provider", action: "read" },
  "api-keys": { resource: "api-key", action: "read" },
  approvals: { resource: "approval", action: "read" },
  "approval-policies": { resource: "approval-policy", action: "read" },
  usage: { resource: "usage", action: "read" },
  audit: { resource: "audit", action: "read" },
  alerts: { resource: "alert", action: "read" },
  "alert-deliveries": { resource: "alert", action: "read" },
  "knowledge-ops": { resource: "knowledge-ops", action: "read" },
  "scenario-templates": { resource: "scenario-template", action: "read" },
  "parser-policies": { resource: "parser-policy", action: "read" },
  "quality-profiles": { resource: "quality-profile", action: "read" },
  "logical-documents": { resource: "logical-document", action: "read" },
  "release-governance": { resource: "release-governance", action: "read" },
  "assistant-releases": { resource: "release-governance", action: "read" },
  branding: { resource: "branding", action: "read" },
  system: { resource: "system", action: "read" },
};

async function canAccessNavigationAlias(resource: string): Promise<boolean> {
  const alias = NAVIGATION_PERMISSION_ALIASES.find((item) => item.resource === resource);
  if (!alias) return false;
  const checks = alias.rules;
  const results = await Promise.all(checks.map(async (check) => {
    try {
      const canAccess = authProvider.canAccess;
      if (!canAccess) return false;
      return await canAccess({ resource: check.resource, action: check.action });
    } catch {
      return false;
    }
  }));
  return alias.mode === "all" ? results.every(Boolean) : results.some(Boolean);
}

function permMatches(p: MePermission, resource: string, action: string): boolean {
  return (p.resource === "*" || p.resource === resource) && (p.action === "*" || p.action === action);
}

export const authProvider: AuthProvider = {
  login: async (params: unknown) => {
    const { username, password } = (params ?? {}) as Record<string, string>;
    await waitForPendingSessionClear();
    const res = await api.post<Envelope<LoginPayload>>("/auth/login", {
      username,
      password,
    });
    if (res.data.code !== 0 || !res.data.data?.token) {
      throw new ApiError(res.status ?? 200, res.data.code, res.data.message || "登录失败", res.data.trace_id);
    }
    identityCache = undefined;
    return Promise.resolve();
  },
  logout: () => {
    identityCache = undefined;
    return clearSession();
  },
  checkError: (error) => {
    if (error instanceof ApiError && error.status === 401) {
      clearSession();
      return Promise.reject(error);
    }
    return Promise.resolve();
  },
  checkAuth: () =>
    api.get("/auth/me").then(() => undefined).catch(() => {
      throw new Error("unauthenticated");
    }),
  getPermissions: async () => {
    const me = await checkIdentity();
    return {
      approvalsEnabled: me?.approval_enabled ?? false,
      platform: me?.platform ?? false,
      role: me?.role ?? "",
      permissions: me?.permissions ?? [],
    };
  },
  canAccess: async ({ resource, action }: { resource: string; action?: string }) => {
    const me = await checkIdentity();
    if (!me) return false;
    if (action === undefined || action === "list" || action === "show") {
      const alias = NAVIGATION_PERMISSION_ALIASES.find((item) => item.resource === resource);
      if (alias) return canAccessNavigationAlias(resource);
    }
    if ((resource === "approvals" || resource === "approval-policies") && me.approval_enabled !== true) {
      return false;
    }
    const mapped = RESOURCE_ACCESS[resource];
    const writeActions = new Set(["create", "edit", "delete", "export"]);
    const requestedAction = writeActions.has(action ?? "") ? "manage" : action ?? "read";
    const target = mapped
      ? {
          resource: mapped.resource,
          action: requestedAction === "list" || requestedAction === "show" ? mapped.action : requestedAction,
        }
      : { resource, action: requestedAction };
    // deny takes precedence over allow
    for (const p of me.permissions ?? []) {
      if (p.effect === "deny" && permMatches(p, target.resource, target.action)) return false;
    }
    for (const p of me.permissions ?? []) {
      if (p.effect === "allow" && permMatches(p, target.resource, target.action)) return true;
    }
    return false;
  },
  getIdentity: async () => {
    try {
      const me = await checkIdentity();
      if (!me) throw new ApiError(401, 401, "获取用户信息失败");
      return { id: me.id, fullName: me.username, role: me.role, tenant_id: me.tenant_id };
    } catch (err) {
      if (err instanceof ApiError) throw err;
      console.error("[getIdentity] failed to resolve identity", err);
      return { id: "", fullName: "" };
    }
  },
};
