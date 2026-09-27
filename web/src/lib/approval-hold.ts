import axios from "axios";
import type { AxiosError, AxiosResponse } from "axios";

export interface ApprovalHold {
  approval_id: string;
  request_no: string;
  object_type: string;
  action: string;
  status: string;
  current_step: number;
  expire_at: string;
  approval_path: string;
}

interface ApprovalEnvelope {
  code?: number;
  message?: string;
  data?: unknown;
}

export class ApprovalHoldError extends Error {
  readonly status = 202;
  readonly hold: ApprovalHold;

  constructor(hold: ApprovalHold) {
    super("approval submitted");
    this.name = "ApprovalHoldError";
    this.hold = hold;
  }
}

function normalize(value: unknown): ApprovalHold | null {
  if (!value || typeof value !== "object") return null;
  const candidate = value as Record<string, unknown>;
  const approvalID = candidate.approval_id;
  const requestNo = candidate.request_no;
  if (typeof approvalID !== "string" || typeof requestNo !== "string") return null;
  return {
    approval_id: approvalID,
    request_no: requestNo,
    object_type: typeof candidate.object_type === "string" ? candidate.object_type : "",
    action: typeof candidate.action === "string" ? candidate.action : "",
    status: typeof candidate.status === "string" ? candidate.status : "pending_approval",
    current_step: Number(candidate.current_step ?? 1) || 1,
    expire_at: typeof candidate.expire_at === "string" ? candidate.expire_at : "",
    approval_path:
      typeof candidate.approval_path === "string"
        ? candidate.approval_path
        : `/approvals/${approvalID}/show`,
  };
}

export function approvalHoldFromResponse<T>(
  response: AxiosResponse<ApprovalEnvelope & T>,
): ApprovalHold | null {
  if (response.status !== 202 || response.data?.code !== 0) return null;
  return normalize(response.data.data);
}

export function approvalHoldFromError(error: unknown): ApprovalHold | null {
  if (!axios.isAxiosError(error)) return null;
  const axiosError = error as AxiosError<ApprovalEnvelope>;
  if (axiosError.response?.status !== 202 || axiosError.response.data?.code !== 0) return null;
  return normalize(axiosError.response.data.data);
}

export function approvalIdempotencyKey(prefix = "web"): string {
  // randomUUID is preferred; the time-and-random fallback keeps older browsers
  // and non-secure contexts usable while producing URL-safe unique-enough keys.
  const uuid = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `${prefix}:${uuid}`.replace(/[^A-Za-z0-9_.:-]/g, "-").slice(0, 128);
}
