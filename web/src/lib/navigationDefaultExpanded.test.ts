import { describe, expect, it } from "vitest";
import {
  NAVIGATION_VIEW_DEFAULT_EXPANDED,
  NAVIGATION_VIEW_PROFILES,
  type NavigationView,
} from "./navigationRoles";

// Compact helper: the set of real group keys defined for a view.
const groupKeys = (view: NavigationView) =>
  NAVIGATION_VIEW_PROFILES[view].groups.map((g) => g.key);

describe("NAVIGATION_VIEW_DEFAULT_EXPANDED (doc/223 §3 T8 — five-role navigation)", () => {
  it("every default-expanded key must be a real group key of its view", () => {
    for (const view of Object.keys(NAVIGATION_VIEW_DEFAULT_EXPANDED) as NavigationView[]) {
      const defaults = NAVIGATION_VIEW_DEFAULT_EXPANDED[view];
      expect(defaults, `view ${view} must define a default set`).toBeDefined();
      for (const key of defaults!) {
        expect(
          groupKeys(view),
          `view ${view} expanded group "${key}" must exist in its profile groups`,
        ).toContain(key);
      }
    }
  });

  it("platform admin surfaces workspace/governance/audit first, collapses the rest", () => {
    const defaults = NAVIGATION_VIEW_DEFAULT_EXPANDED["platform-admin-view"];
    expect(defaults).toContain("workspace");
    expect(defaults).toContain("governance");
    expect(defaults).toContain("audit");
    // Secondary operational groups are folded to reduce noise.
    expect(defaults).not.toContain("assistant-apps");
    expect(defaults).not.toContain("lifecycle");
  });

  it("business user opens assistant/interaction, keeps datasets on demand", () => {
    const defaults = NAVIGATION_VIEW_DEFAULT_EXPANDED["business-user-view"];
    expect(defaults).toContain("assistant-apps");
    expect(defaults).toContain("interaction");
    expect(defaults).not.toContain("knowledge");
  });

  it("data engineer opens knowledge + lifecycle as the working set", () => {
    const defaults = NAVIGATION_VIEW_DEFAULT_EXPANDED["operator-view"];
    expect(defaults).toContain("knowledge");
    expect(defaults).toContain("lifecycle");
    expect(defaults).not.toContain("system");
  });

  it("auditor opens audit + governance; read-only observer starts fully collapsed", () => {
    expect(NAVIGATION_VIEW_DEFAULT_EXPANDED["audit-view"]).toEqual(
      expect.arrayContaining(["audit", "governance"]),
    );
    expect(NAVIGATION_VIEW_DEFAULT_EXPANDED["read-only-view"]).toEqual([]);
  });

  it("every view still surfaces at least one expanded group (no dead sidebar)", () => {
    for (const view of Object.keys(NAVIGATION_VIEW_DEFAULT_EXPANDED) as NavigationView[]) {
      if (view === "read-only-view") continue; // intentionally fully collapsed
      expect(
        NAVIGATION_VIEW_DEFAULT_EXPANDED[view]!.length,
        `view ${view} must keep its primary group expanded`,
      ).toBeGreaterThan(0);
    }
  });
});
