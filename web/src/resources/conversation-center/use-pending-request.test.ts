import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { usePendingRequest } from "./use-pending-request";

describe("pending request state", () => {
  it("allows only one active operation for the same pending, revision, kind and resource identity", () => {
    const { result } = renderHook(() => usePendingRequest());

    act(() => {
      result.current.setText("question");
      result.current.createPending("direct-submit");
      const operation = result.current.beginOperation("submit", { targetId: "target-1" });
      expect(operation).not.toBeNull();
      expect(result.current.beginOperation("submit", { targetId: "target-1" })).toBeNull();
      expect(result.current.beginOperation("submit", { targetId: "target-2" })).not.toBeNull();
    });

    expect(result.current.getPending()?.activeOperations).toHaveLength(2);
  });
});
