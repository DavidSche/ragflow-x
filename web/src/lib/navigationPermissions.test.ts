// ScenarioID: SC-FE-UX-001
import { describe, expect, it, vi } from "vitest";
import {
  NAVIGATION_PERMISSION_ALIASES,
  applyNavigationPermission,
  isAliasSupported,
  navigationPermissionState,
} from "./navigationPermissions";

const byResource = (name: string) =>
  NAVIGATION_PERMISSION_ALIASES.find((alias) => alias.resource === name)!;

describe("navigation permission aliases", () => {
  it("maps navigation entries to backend catalog rules", () => {
    const available = new Set([
      "chat|execute", "search-app|execute", "agent|execute",
      "scenario-template|read", "prompt-policy|read",
      "knowledge-lifecycle|read", "eval-set|read",
    ]);

    for (const alias of NAVIGATION_PERMISSION_ALIASES) {
      expect(isAliasSupported(alias, available)).toBe(true);
    }
  });

  it("derives aggregate navigation states", () => {
    const asset = byResource("asset-governance");
    const center = byResource("conversation-center");

    expect(navigationPermissionState(asset, {})).toBe("");
    expect(navigationPermissionState(asset, {
      "scenario-template|read": "allow",
      "prompt-policy|read": "allow",
      "knowledge-lifecycle|read": "allow",
      "eval-set|read": "allow",
    })).toBe("allow");
    expect(navigationPermissionState(asset, {
      "scenario-template|read": "allow",
      "prompt-policy|read": "deny",
    })).toBe("deny");
    expect(navigationPermissionState(center, { "agent|execute": "allow" })).toBe("allow");
    expect(navigationPermissionState(center, {
      "chat|execute": "allow",
      "agent|execute": "deny",
    })).toBe("deny");
  });

  it("saves only backend catalog permissions", () => {
    const onChange = vi.fn();
    const center = byResource("conversation-center");

    applyNavigationPermission(center, "allow", onChange);
    expect(onChange).toHaveBeenNthCalledWith(1, "chat", "execute", "allow");
    expect(onChange).toHaveBeenNthCalledWith(2, "search-app", "execute", "");
    expect(onChange).toHaveBeenNthCalledWith(3, "agent", "execute", "");

    applyNavigationPermission(center, "deny", onChange);
    expect(onChange).toHaveBeenNthCalledWith(4, "chat", "execute", "deny");
    expect(onChange).toHaveBeenNthCalledWith(5, "search-app", "execute", "deny");
    expect(onChange).toHaveBeenNthCalledWith(6, "agent", "execute", "deny");
  });
});
