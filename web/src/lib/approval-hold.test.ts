import { describe, expect, it } from "vitest";
import {
  ApprovalHoldError,
  approvalHoldFromError,
  approvalHoldFromResponse,
  approvalIdempotencyKey,
} from "./approval-hold";

const hold = {
  approval_id: "a1",
  request_no: "APR-1",
  object_type: "dataset",
  action: "delete",
  status: "pending_approval",
  current_step: 1,
  expire_at: "2026-01-01T00:00:00Z",
  approval_path: "/approvals/a1/show",
};

describe("approval hold protocol", () => {
  it("extracts a 202 approval hold", () => {
    expect(approvalHoldFromResponse({
      status: 202,
      data: { code: 0, data: hold },
    } as any)).toEqual(hold);
  });

  it("ignores ordinary responses", () => {
    expect(approvalHoldFromResponse({ status: 200, data: { code: 0 } } as any)).toBeNull();
  });

  it("extracts an approval hold from an axios error", () => {
    const error = Object.assign(new Error("202"), {
      isAxiosError: true,
      response: { status: 202, data: { code: 0, data: hold } },
    });
    expect(approvalHoldFromError(error)).toEqual(hold);
  });

  it("wraps a hold in a typed error", () => {
    const error = new ApprovalHoldError(hold);
    expect(error.status).toBe(202);
    expect(error.hold.request_no).toBe("APR-1");
  });

  it("creates URL-safe idempotency keys", () => {
    const key = approvalIdempotencyKey("test");
    expect(key).toMatch(/^test:[A-Za-z0-9_.:-]+$/);
    expect(key.length).toBeLessThanOrEqual(128);
  });

  it("falls back safely when crypto.randomUUID is unavailable", () => {
    const originalCrypto = globalThis.crypto;
    Object.defineProperty(globalThis, "crypto", {
      configurable: true,
      value: undefined,
    });
    try {
      const key = approvalIdempotencyKey("legacy");
      expect(key).toMatch(/^legacy:[A-Za-z0-9_.:-]+$/);
      expect(key.length).toBeLessThanOrEqual(128);
    } finally {
      Object.defineProperty(globalThis, "crypto", {
        configurable: true,
        value: originalCrypto,
      });
    }
  });
});
