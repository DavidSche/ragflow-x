import { describe, expect, it, vi } from "vitest";

/**
 * The `enable_pushdown_and_sync_meta` repair channel (doc/285 §10).
 *
 * `PUT /datasets/:id` answers **202 + approval payload** when an approval policy
 * holds the write — a plain axios success. Reporting that as "已开启下推" is the
 * same class of defect as the parse-profile one: the UI claims a write that has
 * not happened. These cases pin the two shapes apart.
 */

const push = vi.hoisted(() => vi.fn());
const post = vi.hoisted(() => vi.fn());

vi.mock("../../lib/api", () => ({
  api: { put: push, post },
}));

const { applyDatasetParseProfile, updateDatasetPushdown } = await import("./scenario-templates");

function response(status: number, data: unknown) {
  return { status, data };
}

describe("updateDatasetPushdown", () => {
  it("reports `applied` for the plain dataset response", async () => {
    push.mockResolvedValueOnce(
      response(200, { code: 0, data: { id: "ds-1", pushdown_enabled: true } }),
    );
    await expect(updateDatasetPushdown("ds-1", { pushdown_enabled: true })).resolves.toEqual({
      state: "applied",
    });
    expect(push).toHaveBeenCalledWith("/datasets/ds-1", { pushdown_enabled: true });
  });

  it("reports `pending_approval` when an approval policy holds the write", async () => {
    push.mockResolvedValueOnce(
      response(202, {
        code: 0,
        message: "approval submitted",
        data: {
          approval_id: "ap-1",
          request_no: "AP-2026-0001",
          object_type: "dataset",
          action: "update",
          status: "pending_approval",
          current_step: 1,
          expire_at: "2026-11-17T00:00:00Z",
          approval_path: "/approvals/ap-1/show",
        },
      }),
    );
    const result = await updateDatasetPushdown("ds-1", { pushdown_enabled: true });
    expect(result.state).toBe("pending_approval");
    if (result.state !== "pending_approval") throw new Error("unreachable");
    expect(result.hold.request_no).toBe("AP-2026-0001");
  });
});

describe("applyDatasetParseProfile", () => {
  it("returns the profile when the write went through", async () => {
    post.mockResolvedValueOnce(
      response(200, {
        code: 0,
        data: { dataset_id: "ds-1", chunk_method: "book", pushdown_enabled: false, document_count: 1, parsed_count: 1, reparse_triggered: true },
      }),
    );
    const result = await applyDatasetParseProfile("ds-1", { chunk_method: "book", rewrite_existing: true });
    expect(result.state).toBe("applied");
    if (result.state !== "applied") throw new Error("unreachable");
    expect(result.data.reparse_triggered).toBe(true);
  });

  it("does not read reparse_triggered off an approval ticket", async () => {
    // Before this, the 202 payload was read as a profile: `reparse_triggered`
    // was undefined and the wizard claimed the re-parse had been queued.
    post.mockResolvedValueOnce(
      response(202, {
        code: 0,
        message: "approval submitted",
        data: {
          approval_id: "ap-2",
          request_no: "AP-2026-0002",
          object_type: "dataset",
          action: "update",
          status: "pending_approval",
          current_step: 1,
          expire_at: "2026-11-17T00:00:00Z",
          approval_path: "/approvals/ap-2/show",
        },
      }),
    );
    const result = await applyDatasetParseProfile("ds-1", { chunk_method: "book", rewrite_existing: true });
    expect(result.state).toBe("pending_approval");
    if (result.state !== "pending_approval") throw new Error("unreachable");
    expect(result.hold.request_no).toBe("AP-2026-0002");
  });
});
