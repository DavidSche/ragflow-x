// ScenarioID: SC-FE-DATA-001
// ScenarioID: SC-AUDIT-002
import { beforeEach, describe, expect, it, vi } from "vitest";

// dataProvider talks through the shared axios singleton (src/lib/api). Mock the
// module so each test controls the raw envelope without real HTTP or interceptors.
const { requestMock, canAccessMock, getPermissionsMock } = vi.hoisted(() => ({
  requestMock: vi.fn(),
  canAccessMock: vi.fn(),
  getPermissionsMock: vi.fn(),
}));

vi.mock("@/lib/api", () => {
  class MockApiError extends Error {
    status: number;
    code: number;
    traceId?: string;
    constructor(status: number, code: number, message: string, traceId?: string) {
      super(message);
      this.name = "ApiError";
      this.status = status;
      this.code = code;
      this.traceId = traceId;
    }
  }
  return {
    api: { request: requestMock },
    ApiError: MockApiError,
  };
});

import { ApiError } from "@/lib/api";
import { dataProvider } from "@/dataProvider";

vi.mock("@/authProvider", () => ({
  authProvider: { canAccess: canAccessMock, getPermissions: getPermissionsMock },
}));

// Backend envelope: { code: 0, message, data } inside the axios response.
function ok(data: unknown): { status: number; data: { code: number; message: string; data: unknown } } {
  return { status: 200, data: { code: 0, message: "", data } };
}

beforeEach(() => {
  requestMock.mockReset();
  canAccessMock.mockReset();
  getPermissionsMock.mockReset();
  canAccessMock.mockResolvedValue(false);
  getPermissionsMock.mockResolvedValue({ platform: false });
});

describe("dataProvider.getList", () => {
  it("short-circuits branding without a network call", async () => {
    const res = await dataProvider.getList("branding", { filter: {} } as never);
    expect(res).toEqual({ data: [], total: 0 });
    expect(requestMock).not.toHaveBeenCalled();
  });

  it("loads a paginated resource via /resource and maps items+total", async () => {
    requestMock.mockResolvedValue(ok({ items: [{ id: "c1" }, { id: "c2" }], total: 2 }));
    const res = await dataProvider.getList("chats", { filter: {} } as never);
    expect(requestMock).toHaveBeenCalledWith({ method: "get", url: "/chats", data: undefined });
    expect(res).toEqual({ data: [{ id: "c1" }, { id: "c2" }], total: 2 });
  });

  it("loads users with the scope=all prefix", async () => {
    canAccessMock.mockResolvedValue(true);
    requestMock.mockResolvedValue(ok({ items: [{ id: "u1" }], total: 1 }));
    const res = await dataProvider.getList("users", { filter: { role: "admin" } } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "get",
      url: "/users?scope=all&role=admin",
      data: undefined,
    });
    expect(res.total).toBe(1);
  });

  it("loads users with current scope without governance access", async () => {
    requestMock.mockResolvedValue(ok({ items: [{ id: "u1" }], total: 1 }));
    const res = await dataProvider.getList("users", { filter: { role: "admin" } } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "get",
      url: "/users?role=admin",
      data: undefined,
    });
    expect(res.total).toBe(1);
  });

  it("uses the authoritative platform flag for cross-workspace user scope", async () => {
    canAccessMock.mockResolvedValue(false);
    getPermissionsMock.mockResolvedValue({ platform: true });
    requestMock.mockResolvedValue(ok({ items: [{ id: "u1" }], total: 1 }));
    await dataProvider.getList("users", { filter: {} } as never);
    expect(requestMock).toHaveBeenCalledWith({ method: "get", url: "/users?scope=all", data: undefined });
  });

  it("loads alert deliveries with cross-workspace scope and backend query contract", async () => {
    getPermissionsMock.mockResolvedValue({ platform: true });
    requestMock.mockResolvedValue(ok({
      items: [{ alert_event_id: "event-1", channel: "webhook", status: "failed" }],
      total: 1,
    }));
    const res = await dataProvider.getList("alert-deliveries", {
      filter: { status: "failed", channel: "webhook" },
      pagination: { page: 2, perPage: 25 },
      sort: { field: "last_attempt_at", order: "DESC" },
    } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "get",
      url: "/alerts/deliveries?scope=all&status=failed&channel=webhook&page=2&page_size=25&sort_by=last_attempt_at&order=desc",
      data: undefined,
    });
    expect(res).toEqual({
      data: [{ alert_event_id: "event-1", channel: "webhook", status: "failed", id: "event-1:webhook" }],
      total: 1,
    });
  });

  it("uses specific alert delivery scope for an authorized tenant filter", async () => {
    canAccessMock.mockResolvedValue(true);
    requestMock.mockResolvedValue(ok({ items: [], total: 0 }));
    await dataProvider.getList("alert-deliveries", {
      filter: { status: "pending", tenant_id: "tenant-2" },
    } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "get",
      url: "/alerts/deliveries?scope=specific&status=pending&tenant_id=tenant-2",
      data: undefined,
    });
  });

  it("keeps alert deliveries in the current tenant without governance scope", async () => {
    requestMock.mockResolvedValue(ok({ items: [], total: 0 }));
    await dataProvider.getList("alert-deliveries", { filter: { status: "failed" } } as never);
    expect(requestMock).toHaveBeenLastCalledWith(
      expect.objectContaining({ url: "/alerts/deliveries?status=failed" }),
    );
  });

  it("strips null/empty filters but keeps 0", async () => {
    canAccessMock.mockResolvedValue(true);
    requestMock.mockResolvedValue(ok({ items: [], total: 0 }));
    await dataProvider.getList(
      "audit",
      { filter: { action: "login", empty: "", skip: null, keep: 0 } } as never,
    );
    expect(requestMock).toHaveBeenCalledWith({
      method: "get",
      url: "/audit?scope=all&action=login&keep=0",
      data: undefined,
    });
  });

  it("maps pagination and sort to backend query parameters", async () => {
    requestMock.mockResolvedValue(ok({ items: [], total: 0 }));
    await dataProvider.getList("chats", {
      filter: { name: "assistant" },
      pagination: { page: 3, perPage: 50 },
      sort: { field: "created_at", order: "DESC" },
    } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "get",
      url: "/chats?name=assistant&page=3&page_size=50&sort_by=created_at&order=desc",
      data: undefined,
    });
  });

  it("maps a dataset list without governance scope for a caller without permission", async () => {
    requestMock.mockResolvedValue(ok([{ id: "d1" }, { id: "d2" }]));
    const res = await dataProvider.getList("datasets", { filter: {} } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "get",
      url: "/datasets",
      data: undefined,
    });
    expect(res.total).toBe(2);
  });

  it("normalizes null list payloads to empty arrays", async () => {
    requestMock.mockResolvedValueOnce(ok(null));
    await expect(dataProvider.getList("datasets", { filter: {} } as never)).resolves.toEqual({
      data: [],
      total: 0,
    });

    requestMock.mockResolvedValueOnce(ok({ items: null, total: null }));
    await expect(dataProvider.getList("users", { filter: {} } as never)).resolves.toEqual({
      data: [],
      total: 0,
    });

    requestMock.mockResolvedValueOnce(ok(null));
    await expect(dataProvider.getMany("datasets", { ids: ["d1"] } as never)).resolves.toEqual({
      data: [],
    });
  });

  it("maps dataset governance scopes from permission and tenant filter", async () => {
    canAccessMock.mockResolvedValue(true);
    requestMock.mockResolvedValue(ok([{ id: "d1" }]));
    await dataProvider.getList("datasets", { filter: { tenant_id: "t1" } } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "get",
      url: "/datasets?scope=specific&tenant_id=t1",
      data: undefined,
    });
    await dataProvider.getList("datasets", { filter: {} } as never);
    expect(requestMock).toHaveBeenLastCalledWith({
      method: "get",
      url: "/datasets?scope=all",
      data: undefined,
    });
  });

  it("maps api-keys to the /keys endpoint", async () => {
    requestMock.mockResolvedValue(ok([{ id: "k1" }]));
    const res = await dataProvider.getList("api-keys", { filter: {} } as never);
    expect(requestMock).toHaveBeenCalledWith({ method: "get", url: "/keys", data: undefined });
    expect(res.data).toHaveLength(1);
  });

  it("flattens paginated enterprise connection views", async () => {
    canAccessMock.mockResolvedValue(true);
    requestMock.mockResolvedValue(ok({
      items: [{
        connection: { id: "c1", lifecycle_status: "ACTIVE" },
        version: { provider_name: "openai-api-compatible", display_name: "Private LLM" },
        tenant_name: "raven-demo",
      }],
      total: 1,
    }));
    const result = await dataProvider.getList("enterprise-connections", {
      filter: { provider: "openai", tenant_id: "t1", lifecycle_status: "ACTIVE" },
      pagination: { page: 2, perPage: 20 },
    } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "get",
      url: "/enterprise-connections?scope=all&provider=openai&tenant_id=t1&lifecycle_status=ACTIVE&page=2&page_size=20",
      data: undefined,
    });
    expect(result.data[0]).toMatchObject({
      id: "c1",
      provider_name: "openai-api-compatible",
      display_name: "Private LLM",
      tenant_name: "raven-demo",
    });
    expect(result.total).toBe(1);
  });

  it("returns an empty list for unknown resources", async () => {
    const res = await dataProvider.getList("nope", { filter: {} } as never);
    expect(res).toEqual({ data: [], total: 0 });
    expect(requestMock).not.toHaveBeenCalled();
  });
});

describe("dataProvider.getOne", () => {
  it("loads a workspace dataset by id", async () => {
    requestMock.mockResolvedValue(ok({ id: "d9", name: "Docs" }));
    const res = await dataProvider.getOne("datasets", { id: "d9" } as never);
    expect(requestMock).toHaveBeenCalledWith({ method: "get", url: "/datasets/d9", data: undefined });
    expect(res.data).toMatchObject({ id: "d9", name: "Docs" });
  });

  it("loads a governed dataset with the all-workspace scope", async () => {
    getPermissionsMock.mockResolvedValue({ platform: true });
    requestMock.mockResolvedValue(ok({ id: "d9", name: "Docs", tenant_id: "tenant-2" }));
    const res = await dataProvider.getOne("datasets", { id: "d9" } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "get",
      url: "/datasets/d9?scope=all",
      data: undefined,
    });
    expect(res.data).toMatchObject({ id: "d9", tenant_id: "tenant-2" });
  });

  it("falls back to a stub row for unknown resources", async () => {
    const res = await dataProvider.getOne("unknown", { id: "x1" } as never);
    expect(res.data).toEqual({ id: "x1" });
    expect(requestMock).not.toHaveBeenCalled();
  });
});

describe("dataProvider.getMany", () => {
  it("filters workspace datasets client-side by ids", async () => {
    requestMock.mockResolvedValue(ok([{ id: "a" }, { id: "b" }, { id: "c" }]));
    const res = await dataProvider.getMany("datasets", { ids: ["b", "c"] } as never);
    expect(requestMock).toHaveBeenCalledWith({ method: "get", url: "/datasets", data: undefined });
    expect(res.data.map((r: { id: string }) => r.id)).toEqual(["b", "c"]);
  });

  it("filters governed datasets with the all-workspace scope", async () => {
    getPermissionsMock.mockResolvedValue({ platform: true });
    requestMock.mockResolvedValue(ok([{ id: "b" }]));
    const res = await dataProvider.getMany("datasets", { ids: ["b"] } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "get",
      url: "/datasets?scope=all",
      data: undefined,
    });
    expect(res.data).toEqual([{ id: "b" }]);
  });

  it("returns an empty list for unknown resources", async () => {
    const res = await dataProvider.getMany("nope", { ids: ["a"] } as never);
    expect(res).toEqual({ data: [] });
  });
});

describe("dataProvider.create", () => {
  it("creates a dataset with only its name", async () => {
    requestMock.mockResolvedValue(ok({ id: "d1" }));
    const res = await dataProvider.create("datasets", { data: { name: "KB", extra: 1 } } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "post",
      url: "/datasets",
      data: { name: "KB" },
    });
    expect(res.data).toEqual({ id: "d1" });
  });

  it("coerces api-key quota values to numbers", async () => {
    requestMock.mockResolvedValue(ok({ key: { id: "k1" }, secret: "s3cr3t" }));
    const res = await dataProvider.create("api-keys", {
      data: { name: "key", token_quota: "5000", request_quota: "120" },
    } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "post",
      url: "/keys",
      data: { name: "key", token_quota: 5000, request_quota: 120 },
    });
    expect(res.data).toMatchObject({ id: "k1", secret: "s3cr3t" });
  });

  it("throws ApiError for unsupported resources", async () => {
    await expect(
      dataProvider.create("branding", { data: {} } as never),
    ).rejects.toBeInstanceOf(ApiError);
    expect(requestMock).not.toHaveBeenCalled();
  });
});

describe("dataProvider.update / delete", () => {
  it("updates a tenant via PUT", async () => {
    requestMock.mockResolvedValue(ok({ id: "t1", name: "Renamed" }));
    const res = await dataProvider.update("tenants", { id: "t1", data: { name: "Renamed" } } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "put",
      url: "/tenants/t1",
      data: { name: "Renamed" },
    });
    expect(res.data.name).toBe("Renamed");
  });

  it("deletes a dataset via DELETE", async () => {
    requestMock.mockResolvedValue(ok(null));
    const res = await dataProvider.delete("datasets", { id: "d1" } as never);
    expect(requestMock).toHaveBeenCalledWith({ method: "delete", url: "/datasets/d1", data: undefined });
    expect(res.data).toEqual({ id: "d1" });
  });

  it("batch deletes tasks via DELETE", async () => {
    requestMock.mockResolvedValue(ok({ deleted: 2 }));
    const res = await dataProvider.deleteMany("tasks", { ids: ["task-1", "task-2"] } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "delete",
      url: "/tasks",
      data: { ids: ["task-1", "task-2"] },
    });
    expect(res.data).toEqual(["task-1", "task-2"]);
  });

  it("returns a stub on delete for unsupported resources", async () => {
    const res = await dataProvider.delete("branding", { id: "b1" } as never);
    expect(res.data).toEqual({});
    expect(requestMock).not.toHaveBeenCalled();
  });
});

describe("dataProvider error handling", () => {
  it("throws ApiError carrying the non-zero business code", async () => {
    requestMock.mockResolvedValue({
      status: 502,
      data: { code: 50032, message: "boom", trace_id: "tr-1" },
    });
    const err = await dataProvider.getOne("datasets", { id: "d1" } as never).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 502, code: 50032, message: "boom", traceId: "tr-1" });
  });
});

describe("dataProvider governance and CRUD contracts", () => {
  type ListCase = {
    resource: string;
    platform?: boolean;
    params: Record<string, unknown>;
    response: unknown;
    url: string;
    expected?: unknown;
  };

  it.each<ListCase>([
    {
      resource: "tenants",
      platform: true,
      params: { filter: { tenant_id: "tenant-2", status: "active" }, pagination: { page: 2, perPage: 25 }, sort: { field: "created_at", order: "DESC" } },
      response: { items: [{ id: "tenant-2" }], total: 1 },
      url: "/tenants?scope=specific&tenant_id=tenant-2&status=active&page=2&page_size=25&sort_by=created_at&order=desc",
    },
    {
      resource: "audit",
      platform: true,
      params: { filter: { action: "login" } },
      response: { items: [{ id: "audit-1" }], total: 1 },
      url: "/audit?scope=all&action=login",
    },
    {
      resource: "alerts",
      params: { filter: { status: "failed" } },
      response: { items: [{ id: "alert-1" }], total: 1 },
      url: "/alerts?status=failed",
    },
    {
      resource: "approvals",
      params: { filter: { status: "pending" } },
      response: { items: [{ id: "approval-1" }], total: 1 },
      url: "/approvals?status=pending",
    },
    {
      resource: "enterprise-connections",
      platform: true,
      params: { filter: {} },
      response: { items: [{ connection: { id: "conn-1" }, version: { version: 2 }, tenant_name: "Tenant A" }], total: 1 },
      url: "/enterprise-connections?scope=all",
      expected: { data: [{ id: "conn-1", version: 2, tenant_name: "Tenant A" }], total: 1 },
    },
    {
      resource: "model-providers",
      platform: true,
      params: { filter: { tenant_id: "tenant-2", brand: "openai" } },
      response: [{ id: "provider-1" }],
      url: "/model-providers?tenant_id=tenant-2&brand=openai&scope=specific",
    },
    {
      resource: "agents",
      platform: true,
      params: { filter: { status: "active" } },
      response: { items: [{ id: "agent-1" }], total: 1 },
      url: "/agents?scope=all&status=active",
    },
    {
      resource: "usage",
      platform: true,
      params: { filter: { user_id: "user-1" } },
      response: [{ request_id: "request-1" }],
      url: "/usage?user_id=user-1&scope=all",
    },
    {
      resource: "roles",
      params: { filter: { status: "active" } },
      response: [{ id: "role-1" }],
      url: "/roles?status=active",
    },
  ])("lists $resource with the backend contract", async (testCase) => {
    getPermissionsMock.mockResolvedValue({ platform: testCase.platform === true });
    requestMock.mockResolvedValue(ok(testCase.response));
    const result = await dataProvider.getList(testCase.resource, testCase.params as never);
    expect(requestMock).toHaveBeenCalledWith({ method: "get", url: testCase.url, data: undefined });
    expect(result).toEqual(testCase.expected ?? {
      data: Array.isArray(testCase.response)
        ? testCase.response
        : (testCase.response as { items: unknown[] }).items,
      total: Array.isArray(testCase.response)
        ? testCase.response.length
        : (testCase.response as { total?: number }).total ?? 0,
    });
  });

  it.each([
    { resource: "approvals", url: "/approvals/approval-1", response: { approval: { id: "approval-1" }, steps: [{ step_no: 1 }] }, expected: { id: "approval-1", steps: [{ step_no: 1 }] } },
    { resource: "teams", url: "/teams/team-1", response: { id: "team-1" }, expected: { id: "team-1" } },
    { resource: "chats", url: "/chats/chat-1", response: { id: "chat-1" }, expected: { id: "chat-1" } },
    { resource: "search-apps", url: "/search-apps/app-1", response: { id: "app-1" }, expected: { id: "app-1" } },
    { resource: "memories", url: "/memories/memory-1", response: { id: "memory-1" }, expected: { id: "memory-1" } },
    { resource: "agents", url: "/agents/agent-1", response: { id: "agent-1" }, expected: { id: "agent-1" } },
  ])("loads $resource detail", async (testCase) => {
    requestMock.mockResolvedValue(ok(testCase.response));
    const result = await dataProvider.getOne(testCase.resource, { id: testCase.url.split("/").pop() } as never);
    expect(requestMock).toHaveBeenCalledWith({ method: "get", url: testCase.url, data: undefined });
    expect(result.data).toEqual(testCase.expected);
  });

  it.each([
    { resource: "users", url: "/users", data: { username: "user-1" } },
    { resource: "roles", url: "/roles", data: { name: "Editor" } },
    { resource: "teams", url: "/teams", data: { name: "Platform" } },
    { resource: "tenants", url: "/tenants", data: { name: "Tenant A" } },
    { resource: "projects", url: "/projects", data: { name: "Project A" } },
    { resource: "chats", url: "/chats", data: { name: "Assistant" } },
    { resource: "search-apps", url: "/search-apps", data: { name: "Search" } },
    { resource: "memories", url: "/memories", data: { name: "Memory" } },
    { resource: "agents", url: "/agents", data: { title: "Agent" } },
    { resource: "approvals", url: "/approvals", data: { reason: "create" } },
    { resource: "approval-policies", url: "/approval-policies", data: { action: "delete" } },
  ])("creates $resource", async (testCase) => {
    requestMock.mockResolvedValue(ok({ id: "new-1" }));
    const result = await dataProvider.create(testCase.resource, { data: testCase.data } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "post",
      url: testCase.url,
      data: testCase.data,
      headers: undefined,
    });
    expect(result.data).toEqual({ id: "new-1" });
  });

  it("creates a cross-workspace model provider with scoped tenant and idempotency key", async () => {
    requestMock.mockResolvedValue(ok({ id: "provider-1" }));
    const result = await dataProvider.create("model-providers", {
      data: { name: "Private", tenant_id: "tenant 2" },
    } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "post",
      url: "/model-providers?scope=specific&tenant_id=tenant%202",
      data: { name: "Private" },
      headers: expect.objectContaining({ "Idempotency-Key": expect.any(String) }),
    });
    expect(result.data).toEqual({ id: "provider-1" });
  });

  it.each([
    { resource: "users", url: "/users/user-1" },
    { resource: "teams", url: "/teams/team-1" },
    { resource: "roles", url: "/roles/role-1" },
    { resource: "branding", url: "/branding" },
    { resource: "datasets", url: "/datasets/dataset-1" },
    { resource: "chats", url: "/chats/chat-1" },
    { resource: "search-apps", url: "/search-apps/app-1" },
    { resource: "memories", url: "/memories/memory-1" },
    { resource: "agents", url: "/agents/agent-1" },
    { resource: "approval-policies", url: "/approval-policies/policy-1" },
  ])("updates $resource", async (testCase) => {
    requestMock.mockResolvedValue(ok({ id: "updated-1" }));
    const result = await dataProvider.update(testCase.resource, {
      id: testCase.url.split("/").pop(),
      data: { name: "Updated" },
    } as never);
    expect(requestMock).toHaveBeenCalledWith({
      method: "put",
      url: testCase.url,
      data: { name: "Updated" },
    });
    expect(result.data).toEqual({ id: "updated-1" });
  });

  it.each([
    "model-providers",
    "projects",
    "roles",
    "teams",
    "chats",
    "tenants",
    "search-apps",
    "memories",
    "agents",
  ])("deletes %s", async (resource) => {
    requestMock.mockResolvedValue(ok(null));
    const result = await dataProvider.delete(resource, { id: "row-1" } as never);
    expect(requestMock).toHaveBeenCalledWith({ method: "delete", url: `/${resource}/row-1`, data: undefined });
    expect(result.data).toEqual({ id: "row-1" });
  });

  it("batch deletes datasets", async () => {
    requestMock.mockResolvedValue(ok(null));
    const result = await dataProvider.deleteMany("datasets", { ids: ["d1", "d2"] } as never);
    expect(requestMock).toHaveBeenCalledWith({ method: "delete", url: "/datasets", data: { ids: ["d1", "d2"] } });
    expect(result.data).toEqual(["d1", "d2"]);
  });
});
