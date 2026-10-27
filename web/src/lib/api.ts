import axios, { type AxiosResponse, type InternalAxiosRequestConfig } from "axios";
import { getErrorMessage } from "./errors";

// ------------------------------------------------------------------
// ApiError – unified error type carrying backend envelope fields.
// ------------------------------------------------------------------

/** Structured error carrying the full backend envelope. */
export class ApiError extends Error {
  /** HTTP status code returned by the server. */
  readonly status: number;
  /** Business error code from the response envelope (0 = success). */
  readonly code: number;
  /** Server-side request trace ID for log correlation. */
  readonly traceId?: string;

  constructor(
    status: number,
    code: number,
    message: string,
    traceId?: string,
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.traceId = traceId;
  }

  /**
   * User-facing message in Chinese, resolved from the error-code mapping.
   * Falls back to the original backend message when no mapping exists.
   * Use this in `notify()` calls instead of `err.message`.
   */
  get displayMessage(): string {
    return getErrorMessage(this.code, this.status, this.message);
  }
}

// ------------------------------------------------------------------
// Axios instance + interceptors
// ------------------------------------------------------------------

/** Base axios instance pointed at the RAGFlow-X API gateway. */
export const api = axios.create({
  baseURL: "/api/v1",
  withCredentials: true,
});

let logoutRequest: Promise<void> | null = null;

export function clearSession(): Promise<void> {
  if (!logoutRequest) {
    const request = api
      .post("/auth/logout", {})
      .then(() => undefined)
      .catch((error) => {
        console.warn("logout request failed", error);
      })
      .finally(() => {
        logoutRequest = null;
      });
    logoutRequest = request;
  }
  return logoutRequest;
}

/** Waits for an in-flight logout without starting a new one. */
export function waitForPendingSessionClear(): Promise<void> {
  return logoutRequest ?? Promise.resolve();
}

api.interceptors.request.use((config) => {
  return config;
});

type RetriableConfig = InternalAxiosRequestConfig & { _retry?: boolean };

const authSessionEndpoint = /\/auth\/(?:login|refresh)(?:[?#]|$)/;

function isAuthSessionEndpoint(config: InternalAxiosRequestConfig): boolean {
  const path = `${config.baseURL ?? ""}/${config.url ?? ""}`;
  return authSessionEndpoint.test(path);
}

let refreshRequest: Promise<string> | null = null;

function refreshSession(): Promise<string> {
  if (!refreshRequest) {
    refreshRequest = axios
      .post<{ code: number; data: { token: string; refresh_token: string } }>(
        "/api/v1/auth/refresh",
        {},
        { withCredentials: true },
      )
      .then((response) => {
        if (response.data.code !== 0 || !response.data.data?.token) {
          throw new ApiError(401, 401, "refresh failed");
        }
        return response.data.data.token;
      })
      .finally(() => {
        refreshRequest = null;
      });
  }
  return refreshRequest;
}

/**
 * Extract the structured ApiError from an Axios error.
 *
 * The backend always returns `{ code, message, trace_id? }` in the
 * response body — even for HTTP-level errors.  This helper normalises
 * both "backend returned a non-zero code" and "Axios threw because of
 * a non-2xx status" into a single `ApiError` instance.
 */
async function toApiError(error: unknown): Promise<ApiError> {
  if (error instanceof ApiError) return error;
  if (!axios.isAxiosError(error)) {
    // Network error or something unexpected
    return new ApiError(0, 0, String(error));
  }

  const httpStatus = error.response?.status ?? 0;
  const rawBody = error.response?.data;
  let body: { code?: number; message?: string; trace_id?: string } | undefined;
  if (rawBody instanceof Blob) {
    try {
      body = JSON.parse(await rawBody.text());
    } catch {
      body = undefined;
    }
  } else {
    body = rawBody as { code?: number; message?: string; trace_id?: string } | undefined;
  }

  const code = body?.code ?? httpStatus;
  const message =
    body?.message ||
    (httpStatus === 0
      ? "网络连接失败，请检查网络"
      : httpStatus === 401
        ? "认证信息异常，请重新登录"
        : `请求失败 (${httpStatus})`);
  const traceId = body?.trace_id;

  return new ApiError(httpStatus, code, message, traceId);
}

api.interceptors.response.use(
  (response) => response,
  async (error) => {
    const apiErr = await toApiError(error);

    // --- 401: refresh a protected request once, then fall through to login redirect
    if (apiErr.status === 401) {
      const original = error.config as RetriableConfig | undefined;
      const isAuthSession = original !== undefined && isAuthSessionEndpoint(original);
      if (original && !isAuthSession && !original._retry) {
        original._retry = true;
        return refreshSession()
          .then((token) => {
            if (original.headers) {
              (original.headers as Record<string, string>).Authorization = `Bearer ${token}`;
            }
            return api(original);
          })
          .catch(() => {
            void clearSession();
            if (window.location.pathname !== "/login") {
              window.location.href = "/login";
            }
            return Promise.reject(apiErr);
          });
      }
      if (!isAuthSession) {
        void clearSession();
        if (window.location.pathname !== "/login") {
          window.location.href = "/login";
        }
      }
    }

    // Log trace_id for debugging (not shown to end-users)
    if (apiErr.traceId) {
      console.error(`[${apiErr.traceId}] ${apiErr.code}: ${apiErr.message}`);
    }

    return Promise.reject(apiErr);
  },
);

/** Standard API envelope returned by the backend. */
export interface ApiEnvelope<T> {
  code: number;
  message: string;
  data: T;
  trace_id?: string;
}

export async function unwrap<T>(promise: Promise<AxiosResponse<ApiEnvelope<T>>>): Promise<T> {
  const res = await promise;
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
