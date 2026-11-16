import { describe, expect, it } from "vitest";
import { normalizeCapabilityState, requirementBadgeState } from "./CapabilityBadge";

/**
 * Requirement rows carry a *level* (`unknown | configured | ready | operational`)
 * in `achieved`. Passing that level to the badge normaliser made every satisfied
 * requirement render as `blocked` — the four-state badge must describe the
 * requirement's verdict, and the level stays in the text next to it.
 */
describe("requirementBadgeState", () => {
  it("marks a satisfied requirement as satisfied regardless of its level", () => {
    for (const achieved of ["unknown", "configured", "ready", "operational"]) {
      expect(requirementBadgeState({ satisfied: true })).toBe("satisfied");
    }
  });

  it("marks an unsatisfied requirement as blocked", () => {
    expect(requirementBadgeState({ satisfied: false })).toBe("blocked");
  });

  it("still normalises the four real states and nothing else", () => {
    expect(normalizeCapabilityState("satisfied")).toBe("satisfied");
    expect(normalizeCapabilityState("probe_limited")).toBe("probe_limited");
    expect(normalizeCapabilityState("repairable")).toBe("repairable");
    expect(normalizeCapabilityState("blocked")).toBe("blocked");
    // Levels are not states: this is exactly the bug the helper prevents.
    expect(normalizeCapabilityState("operational")).toBe("blocked");
  });
});
