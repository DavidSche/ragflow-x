import { describe, expect, it } from "vitest";
import { canApplyRelease, canActivateRelease, canCompensateRelease, canPromoteCanary, canRollbackRelease, canRollbackToTarget, releaseStateTone } from "./index";

describe("assistant release state contracts", () => {
  it("permits only contract-valid actions", () => {
    expect(canApplyRelease("APPROVED")).toBe(true);
    expect(canApplyRelease("VERIFIED")).toBe(false);
    expect(canActivateRelease("VERIFIED")).toBe(true);
    expect(canActivateRelease("APPROVED")).toBe(false);
    expect(canPromoteCanary("CANARY_ACTIVE")).toBe(true);
    expect(canPromoteCanary("ACTIVE")).toBe(false);
    expect(canRollbackRelease("ACTIVE")).toBe(true);
    expect(canRollbackRelease("VERIFIED")).toBe(false);
    expect(canRollbackToTarget("RETIRED")).toBe(true);
    expect(canRollbackToTarget("VERIFIED")).toBe(true);
    expect(canRollbackToTarget("CANARY_ACTIVE")).toBe(false);
    expect(canRollbackToTarget("ACTIVE")).toBe(false);
    expect(canCompensateRelease("APPLY_FAILED")).toBe(true);
    expect(canCompensateRelease("COMPENSATING")).toBe(true);
    expect(canCompensateRelease("ACTIVE")).toBe(false);
  });

  it("maps health and release states to visual tone without merging semantics", () => {
    expect(releaseStateTone("ACTIVE")).toBe("default");
    expect(releaseStateTone("CANARY_ACTIVE")).toBe("secondary");
    expect(releaseStateTone("APPLY_FAILED")).toBe("destructive");
    expect(releaseStateTone("UNKNOWN")).toBe("outline");
  });
});
