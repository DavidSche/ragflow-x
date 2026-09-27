import type { DataProvider } from "ra-core";
import { ApiError, api } from "./lib/api";
import { ApprovalHoldError, approvalHoldFromResponse, approvalIdempotencyKey } from "./lib/approval-hold";
import { authProvider } from "./authProvider";

interface Envelope<T> {
  code: number;
  message: string;
  data: T;
  trace_id?: string;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Row = any;

async function request<T>(
  method: string,
  url: string,
  body?: unknown,
  headers?: Record<string, string>,
): Promise<T> {
  const res = await api.request<Envelope<T>>({ method, url, data: body, headers });
  const hold = approvalHoldFromResponse(res);
  if (hold) throw new ApprovalHoldError(hold);
  if (res.data.code !== 0) {
    throw new ApiError(
      res.status ?? 200,
      res.data.code,
      res.data.message || "request failed",
      res.data.trace_id,
    );
  }
  return res.data.data;
}

const paginated = ["tenants", "users", "tasks", "audit", "alerts", "alert-deliveries", "chats", "search-apps", "memories", "agents", "approvals", "enterprise-connections"] as const;
const listable = ["datasets", "model-providers", "api-keys", "usage", "teams", "projects", "roles", "approval-policies"] as const;

async function canUseGovernanceScope(resource: string): Promise<boolean> {
  try {
    const permissions = (await authProvider.getPermissions?.({})) as { platform?: boolean } | undefined;
    if (permissions?.platform === true) return true;
    return (await authProvider.canAccess?.({ resource, action: "governance.read" })) === true;
  } catch {
    return false;
  }
}

function resourcePath(resource: string): string {
  switch (resource) {
    case "api-keys":
      return "/keys";
    case "approval-policies":
      return "/approval-policies";
    case "usage":
      return "/usage";
    default:
      return `/${resource}`;
  }
}

/**
 * Build a query string from filter params, stripping null/undefined/empty values.
 */
function buildFilterQuery(filter?: Record<string, unknown>): string {
  if (!filter) return "";
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filter)) {
    if (value != null && value !== "") {
      params.append(key, String(value));
    }
  }
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

function buildListQuery(
  filter?: Record<string, unknown>,
  pagination?: { page?: number; perPage?: number },
  sort?: { field?: string; order?: "ASC" | "DESC" },
): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filter ?? {})) {
    if (value != null && value !== "") {
      params.append(key, String(value));
    }
  }
  if (pagination?.page != null) params.append("page", String(pagination.page));
  if (pagination?.perPage != null) params.append("page_size", String(pagination.perPage));
  if (sort?.field) {
    params.append("sort_by", sort.field);
    params.append("order", sort.order === "DESC" ? "desc" : "asc");
  }
  const qs = params.toString();
  return qs ? `?${qs}` : "";
}

function asRows<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

function asPage<T>(result: { items?: T[] | null; total?: number } | null | undefined): {
  data: T[];
  total: number;
} {
  return { data: asRows(result?.items), total: result?.total ?? 0 };
}

export const dataProvider: DataProvider = {
  getList: async (resource, params) => {
    if (resource === "branding") {
      return { data: [], total: 0 };
    }
    if ((paginated as readonly string[]).includes(resource)) {
      const query = buildListQuery(
        params.filter as Record<string, unknown> | undefined,
        params.pagination,
        params.sort,
      );
      if (resource === "users") {
        const crossTenantScope = await canUseGovernanceScope(resource);
        const scopeQuery = crossTenantScope ? "scope=all" : "";
        const url = scopeQuery
          ? `/users?${scopeQuery}${query ? `&${query.slice(1)}` : ""}`
          : `/users${query}`;
        const result = await request<{ items: Row[]; total: number }>("get", url);
        return asPage<Row>(result);
      }
      if (resource === "tenants") {
        const crossTenantScope = await canUseGovernanceScope(resource);
        const filters = { ...(params.filter as Record<string, unknown> | undefined) };
        const tenantId = filters.tenant_id;
        const query = buildListQuery(filters, params.pagination, params.sort);
        let scopeQuery = "";
        if (crossTenantScope && tenantId) {
          scopeQuery = "scope=specific";
        } else if (crossTenantScope) {
          scopeQuery = "scope=all";
        }
        const url = `/tenants${scopeQuery ? `?${scopeQuery}` : ""}${scopeQuery && query ? `&${query.slice(1)}` : ""}`;
        const result = await request<{ items: Row[]; total: number }>("get", url);
        return asPage<Row>(result);
      }
      if (resource === "audit") {
        const crossTenantScope = await canUseGovernanceScope(resource);
        const scopeQuery = crossTenantScope ? "scope=all" : "";
        const url = `/audit${scopeQuery ? `?${scopeQuery}` : ""}${scopeQuery && query ? `&${query.slice(1)}` : ""}`;
        const result = await request<{ items: Row[]; total: number }>("get", url);
        return asPage<Row>(result);
      }
      if (resource === "alerts") {
        const url = `/alerts${query}`;
        const result = await request<{ items: Row[]; total: number }>("get", url);
        return asPage<Row>(result);
      }
      if (resource === "alert-deliveries") {
        const crossTenantScope = await canUseGovernanceScope(resource);
        const filters = { ...(params.filter as Record<string, unknown> | undefined) };
        const tenantId = filters.tenant_id;
        const query = buildListQuery(filters, params.pagination, params.sort);
        let scopeQuery = "";
        if (crossTenantScope && tenantId) {
          scopeQuery = "scope=specific";
        } else if (crossTenantScope) {
          scopeQuery = "scope=all";
        }
        const queryString = scopeQuery
          ? `${scopeQuery}${query ? `&${query.slice(1)}` : ""}`
          : query.slice(1);
        const url = `/alerts/deliveries${queryString ? `?${queryString}` : ""}`;
        const result = await request<{ items: Row[]; total: number }>("get", url);
        const data = asRows(result.items).map((item) => ({
          ...item,
          id: `${item.alert_event_id}:${item.channel}`,
        }));
        return { data, total: result.total ?? 0 };
      }
      if (resource === "approvals") {
        const url = `/approvals${query}`;
        const result = await request<{ items: Row[]; total: number }>("get", url);
        return asPage<Row>(result);
      }
      if (resource === "agents") {
        const crossTenantScope = await canUseGovernanceScope(resource);
        const filters = { ...(params.filter as Record<string, unknown> | undefined) };
        const tenantId = filters.tenant_id;
        const agentQuery = buildListQuery(filters, params.pagination, params.sort);
        let scopeQuery = "";
        if (crossTenantScope && tenantId) {
          scopeQuery = "scope=specific";
        } else if (crossTenantScope) {
          scopeQuery = "scope=all";
        }
        const url = `/agents${scopeQuery ? `?${scopeQuery}` : agentQuery}${scopeQuery && agentQuery ? `&${agentQuery.slice(1)}` : ""}`;
        const result = await request<{ items: Row[]; total: number }>("get", url);
        return asPage<Row>(result);
      }
      if (resource === "enterprise-connections") {
        const crossTenantScope = await canUseGovernanceScope(resource);
        const scopeQuery = crossTenantScope ? "scope=all" : "";
        const url = `/enterprise-connections${scopeQuery ? `?${scopeQuery}` : ""}${scopeQuery && query ? `&${query.slice(1)}` : ""}`;
        const result = await request<{
          items: Array<{
            connection: Record<string, unknown>;
            version: Record<string, unknown>;
            tenant_name?: string;
          }>;
          total: number;
        }>("get", url);
        const data = asRows(result.items).map((item) => ({
          ...item.connection,
          ...item.version,
          tenant_name: item.tenant_name,
        }));
        return { data, total: result.total ?? 0 };
      }
      // tenants, tasks, chats
      const url = `${resourcePath(resource)}${query}`;
      const result = await request<{ items: Row[]; total: number }>("get", url);
      return asPage<Row>(result);
    }
    if ((listable as readonly string[]).includes(resource)) {
      const query = buildListQuery(
        params.filter as Record<string, unknown> | undefined,
        params.pagination,
        params.sort,
      );
      if (resource === "datasets") {
        // Datasets are a cross-tenant governance surface for authorized users;
        // Governance scopes are resolved server-side; tenant users default to
        // their current workspace and cannot request another tenant.
        const crossTenantScope = await canUseGovernanceScope(resource);
        const filters = { ...(params.filter as Record<string, unknown> | undefined) };
        const tenantId = filters.tenant_id;
        const datasetQuery = buildListQuery(
          filters,
          params.pagination,
          params.sort,
        );
        let scopeQuery = "";
        if (crossTenantScope && tenantId) {
          scopeQuery = "scope=specific";
        } else if (crossTenantScope) {
          scopeQuery = "scope=all";
        }
        const url = `/datasets${scopeQuery ? `?${scopeQuery}` : datasetQuery}${scopeQuery && datasetQuery ? `&${datasetQuery.slice(1)}` : ""}`;
        const data = await request<Row[]>("get", url);
        return { data: asRows(data), total: asRows(data).length };
      }
      if (resource === "model-providers") {
        const crossTenantScope = await canUseGovernanceScope(resource);
        const filters = { ...(params.filter as Record<string, unknown> | undefined) };
        const tenantId = filters.tenant_id;
        let query = buildListQuery(filters, params.pagination, params.sort);
        if (crossTenantScope && tenantId) {
          query = `${query ? `${query}&` : "?"}scope=specific`;
        } else if (crossTenantScope) {
          query = `${query ? `${query}&` : "?"}scope=all`;
        }
        const url = `/model-providers${query}`;
        const data = await request<Row[]>("get", url);
        return { data: asRows(data), total: asRows(data).length };
      }
      if (resource === "usage") {
        const crossTenantScope = await canUseGovernanceScope(resource);
        let query = buildListQuery(
          params.filter as Record<string, unknown> | undefined,
          params.pagination,
          params.sort,
        );
        if (crossTenantScope) {
          query = `${query ? `${query}&` : "?"}scope=all`;
        }
        const url = `/usage${query}`;
        const data = await request<Row[]>("get", url);
        return { data: asRows(data), total: asRows(data).length };
      }
      const url = `${resourcePath(resource)}${query}`;
      const data = await request<Row[]>("get", url);
      return { data: asRows(data), total: asRows(data).length };
    }
    return { data: [], total: 0 };
  },

  getOne: async (resource, params) => {
    if (resource === "approvals") {
      const detail = await request<{ approval: Row; steps: Row[] }>(
        "get",
        `/approvals/${params.id}`,
      );
      return { data: { ...detail.approval, steps: detail.steps } };
    }
    if (resource === "datasets") {
      const crossTenantScope = await canUseGovernanceScope(resource);
      const scopeQuery = crossTenantScope ? "?scope=all" : "";
      const data = await request<Row>("get", `/datasets/${params.id}${scopeQuery}`);
      return { data };
    }
    if (resource === "teams") {
      const data = await request<Row>("get", `/teams/${params.id}`);
      return { data };
    }
	    if (resource === "chats") {
	      const data = await request<Row>("get", `/chats/${params.id}`);
	      return { data };
	    }
	    if (resource === "search-apps") {
	      const data = await request<Row>("get", `/search-apps/${params.id}`);
	      return { data };
	    }
	    if (resource === "memories") {
	      const data = await request<Row>("get", `/memories/${params.id}`);
	      return { data };
	    }
	    if (resource === "agents") {
	      const data = await request<Row>("get", `/agents/${params.id}`);
	      return { data };
	    }
    return { data: { id: params.id } };
  },

  getMany: async (resource, params) => {
    if (resource === "datasets") {
      const crossTenantScope = await canUseGovernanceScope(resource);
      const scopeQuery = crossTenantScope ? "?scope=all" : "";
      const list = await request<Row[]>("get", `/datasets${scopeQuery}`);
      return { data: asRows(list).filter((d) => params.ids.includes(d.id)) };
    }
    return { data: [] };
  },

  getManyReference: async () => ({ data: [], total: 0 }),

  create: async (resource, params) => {
    let data: Row;
    switch (resource) {
      case "datasets":
        data = await request<Row>("post", "/datasets", { name: params.data.name });
        break;
      case "users":
        data = await request<Row>("post", "/users", params.data);
        break;
      case "model-providers":
        {
          const payload = { ...(params.data as Record<string, unknown>) };
          const tenantID = payload.tenant_id;
          delete payload.tenant_id;
          const scopeQuery = tenantID
            ? `?scope=specific&tenant_id=${encodeURIComponent(String(tenantID))}`
            : "";
          data = await request<Row>("post", `/model-providers${scopeQuery}`, payload, {
            "Idempotency-Key": approvalIdempotencyKey("provider-create"),
          });
        }
        break;
      case "roles":
        data = await request<Row>("post", "/roles", params.data);
        break;
      case "teams":
        data = await request<Row>("post", "/teams", params.data);
        break;
      case "api-keys": {
        const payload: Record<string, unknown> = { name: params.data.name };
        if (
          params.data.token_quota !== undefined &&
          params.data.token_quota !== null &&
          params.data.token_quota !== ""
        ) {
          payload["token_quota"] = Number(params.data.token_quota);
        }
        if (
          params.data.request_quota !== undefined &&
          params.data.request_quota !== null &&
          params.data.request_quota !== ""
        ) {
          payload["request_quota"] = Number(params.data.request_quota);
        }
        const res = await request<{ key: Row; secret: string }>("post", "/keys", payload);
        data = { ...res.key, secret: res.secret };
        break;
      }
      case "tenants":
        data = await request<Row>("post", "/tenants", params.data);
        break;
      case "projects":
        data = await request<Row>("post", "/projects", params.data);
        break;
	      case "chats":
	        data = await request<Row>("post", "/chats", params.data);
	        break;
	      case "search-apps":
	        data = await request<Row>("post", "/search-apps", params.data);
	        break;
	      case "memories":
	        data = await request<Row>("post", "/memories", params.data);
	        break;
      case "agents":
        data = await request<Row>("post", "/agents", params.data);
        break;
      case "approvals":
        data = await request<Row>("post", "/approvals", params.data);
        break;
      case "approval-policies":
        data = await request<Row>("post", "/approval-policies", params.data);
        break;
      default:
        throw new ApiError(400, 40000, `create not supported for resource: ${resource}`);
    }
    return { data };
  },

  update: async (resource, params) => {
    let data: Row = {} as Row;
    switch (resource) {
      case "tenants":
        data = await request<Row>("put", `/tenants/${params.id}`, params.data);
        break;
      case "users":
        data = await request<Row>("put", `/users/${params.id}`, params.data);
        break;
      case "teams":
        data = await request<Row>("put", `/teams/${params.id}`, params.data);
        break;
      case "roles":
        data = await request<Row>("put", `/roles/${params.id}`, params.data);
        break;
      case "branding":
        data = await request<Row>("put", "/branding", params.data);
        break;
      case "datasets":
        {
          const tenantID = (params.previousData as Row | undefined)?.tenant_id;
          const scopeQuery = (await canUseGovernanceScope(resource)) && tenantID
            ? `?scope=specific&tenant_id=${encodeURIComponent(String(tenantID))}`
            : "";
          data = await request<Row>("put", `/datasets/${params.id}${scopeQuery}`, params.data);
        }
        break;
	      case "chats":
	        data = await request<Row>("put", `/chats/${params.id}`, params.data);
	        break;
	      case "search-apps":
	        data = await request<Row>("put", `/search-apps/${params.id}`, params.data);
	        break;
	      case "memories":
	        data = await request<Row>("put", `/memories/${params.id}`, params.data);
	        break;
      case "agents":
        data = await request<Row>("put", `/agents/${params.id}`, params.data);
        break;
      case "approval-policies":
        data = await request<Row>("put", `/approval-policies/${params.id}`, params.data);
        break;
      default:
        return { data: {} } as any;
    }
    return { data };
  },
  updateMany: async () => ({ data: [] }),
  delete: async (resource, params) => {
    if (
      resource === "model-providers" ||
      resource === "projects" ||
      resource === "roles" ||
      resource === "teams" ||
      resource === "datasets" ||
      resource === "chats" ||
      resource === "tenants" ||
      resource === "search-apps" ||
      resource === "memories" ||
      resource === "agents"
    ) {
      {
        const tenantID = (params.previousData as Row | undefined)?.tenant_id;
        const scopeQuery = resource === "datasets" && (await canUseGovernanceScope(resource)) && tenantID
          ? `?scope=specific&tenant_id=${encodeURIComponent(String(tenantID))}`
          : "";
        await request("delete", `/${resource}/${params.id}${scopeQuery}`);
      }
      return { data: { id: params.id } };
    }
    return { data: {} } as any;
  },
  deleteMany: async (resource, params) => {
    if (resource === "datasets") {
      await request("delete", "/datasets", { ids: params.ids });
      return { data: params.ids };
    }
    if (resource === "tasks") {
      await request("delete", "/tasks", { ids: params.ids });
      return { data: params.ids };
    }
    return { data: [] };
  },
};

