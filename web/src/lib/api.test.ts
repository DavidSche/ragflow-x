import { afterEach, describe, expect, it, vi } from "vitest";
import { AxiosError, type AxiosResponse, type InternalAxiosRequestConfig } from "axios";
import {
  api,
  getModelDefaultOptions,
  getModelDefaults,
  patchModelDefault,
  redirectToLogin,
} from "./api";

describe("redirectToLogin", () => {
  it("navigates within the hash router instead of hitting the real /login path", () => {
    window.location.hash = "";
    redirectToLogin();
    expect(window.location.hash).toBe("#/login");
    // pathname stays "/" so no server request for /login is ever issued
    expect(window.location.pathname).toBe("/");
  });

  it("is a no-op when already on the login route", () => {
    window.location.hash = "#/login";
    redirectToLogin();
    expect(window.location.hash).toBe("#/login");
    redirectToLogin();
    expect(window.location.hash).toBe("#/login");
  });

  it("honours a login route with a query string", () => {
    window.location.hash = "#/login?next=%2Fworkbench";
    redirectToLogin();
    expect(window.location.hash).toBe("#/login?next=%2Fworkbench");
  });
});

describe("api auth interceptor", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("does not refresh, retry, or logout after a failed login request", async () => {
    const requests: string[] = [];
    const adapter = api.defaults.adapter;
    api.defaults.adapter = async (config: InternalAxiosRequestConfig) => {
      requests.push(config.url ?? "");
      if (config.url === "/auth/login") {
        const response = {
          status: 401,
          statusText: "Unauthorized",
          headers: {},
          config,
          data: { code: 401, message: "invalid username or password" },
        } as AxiosResponse;
        throw new AxiosError("Request failed", AxiosError.ERR_BAD_REQUEST, config, undefined, response);
      }
      return {
        data: { code: 0, message: "", data: {} },
        status: 200,
        statusText: "OK",
        headers: {},
        config,
      } as AxiosResponse;
    };

    let loginError: unknown;
    await api.post("/auth/login", {}).catch((error: unknown) => {
      loginError = error;
    });
    expect(loginError).toMatchObject({
      status: 401,
      code: 401,
      message: "invalid username or password",
    });
    await new Promise((resolve) => setTimeout(resolve, 20));

    expect(requests.filter((url) => url === "/auth/refresh")).toHaveLength(0);
    expect(requests.filter((url) => url === "/auth/login")).toHaveLength(1);
    expect(requests.filter((url) => url === "/auth/logout")).toHaveLength(0);

    api.defaults.adapter = adapter;
  });

  it("converts a blob error response into a structured ApiError", async () => {
    const adapter = api.defaults.adapter;
    api.defaults.adapter = async (config: InternalAxiosRequestConfig) => {
      const response = {
        status: 400,
        statusText: "Bad Request",
        headers: {},
        config,
        data: new Blob(
          [JSON.stringify({ code: 40095, message: "export result is too large", trace_id: "trace-1" })],
          { type: "application/json" },
        ),
      } as AxiosResponse;
      throw new AxiosError("Request failed", AxiosError.ERR_BAD_REQUEST, config, undefined, response);
    };

    let error: unknown;
    await api.get("/approvals/export", { responseType: "blob" }).catch((caught: unknown) => {
      error = caught;
    });

    api.defaults.adapter = adapter;
    expect(error).toMatchObject({
      status: 400,
      code: 40095,
      message: "export result is too large",
      traceId: "trace-1",
    });
  });
});

describe("default model API helpers", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("cache-busts both defaults reads so external changes are always visible", async () => {
    vi.spyOn(api, "get").mockResolvedValue({
      data: { code: 0, message: "ok", data: [] },
    } as AxiosResponse);
    await getModelDefaults();
    await getModelDefaultOptions();

    const urls = vi.mocked(api.get).mock.calls.map((c) => String(c[0]));
    expect(urls).toEqual([
      "/model-providers/defaults",
      "/model-providers/defaults/options",
    ]);
    for (const call of vi.mocked(api.get).mock.calls) {
      const params = call[1]?.params as Record<string, number>;
      expect(typeof params._).toBe("number");
    }
  });

  it("sends the mandatory CAS declaration on PATCH", async () => {
    vi.spyOn(api, "patch").mockResolvedValue({
      data: { code: 0, message: "ok", data: { status: "succeeded" } },
    } as AxiosResponse);

    await patchModelDefault({
      model_type: "chat",
      model_id: "m1",
      expected_model_id: null,
    });

    expect(vi.mocked(api.patch).mock.calls[0]).toEqual([
      "/model-providers/defaults",
      { model_type: "chat", model_id: "m1", expected_model_id: null },
    ]);
  });
});
