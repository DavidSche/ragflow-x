export interface PermissionRule {
  resource: string;
  action: string;
}

export type NavigationPermissionMode = "all" | "any";

export interface NavigationPermissionAlias {
  resource: string;
  labelKey: string;
  mode: NavigationPermissionMode;
  rules: PermissionRule[];
}

export type NavigationPermissionState = "" | "allow" | "deny" | "mixed";

export const NAVIGATION_PERMISSION_ALIASES: NavigationPermissionAlias[] = [
  {
    resource: "workbench",
    labelKey: "roles.res_workbench",
    mode: "all",
    rules: [{ resource: "chat", action: "execute" }],
  },
  {
    resource: "conversation-center",
    labelKey: "roles.res_conversation_center",
    mode: "any",
    rules: [
      { resource: "chat", action: "execute" },
      { resource: "search-app", action: "execute" },
      { resource: "agent", action: "execute" },
    ],
  },
  {
    resource: "asset-governance",
    labelKey: "roles.res_asset_governance",
    mode: "all",
    rules: [
      { resource: "scenario-template", action: "read" },
      { resource: "prompt-policy", action: "read" },
      { resource: "knowledge-lifecycle", action: "read" },
      { resource: "eval-set", action: "read" },
    ],
  },
];

const ruleKey = ({ resource, action }: PermissionRule) => `${resource}|${action}`;

export function isAliasSupported(
  alias: NavigationPermissionAlias,
  availableRules: ReadonlySet<string>,
): boolean {
  return alias.rules.every((rule) => availableRules.has(ruleKey(rule)));
}

export function navigationPermissionState(
  alias: NavigationPermissionAlias,
  mapState: Record<string, string>,
): NavigationPermissionState {
  const states = alias.rules.map((rule) => mapState[ruleKey(rule)] ?? "");
  const allAllow = states.every((state) => state === "allow");
  const allDeny = states.every((state) => state === "deny");
  const unchanged = states.every((state) => state === "");

  if (allAllow) return "allow";
  if (allDeny) return "deny";
  if (unchanged) return "";
  if (states.some((state) => state === "deny")) return "deny";
  if (alias.mode === "all") {
    return "mixed";
  }
  return states.some((state) => state) ? "allow" : "";
}

export function applyNavigationPermission(
  alias: NavigationPermissionAlias,
  effect: "" | "allow" | "deny",
  onChange: (resource: string, action: string, effect: string) => void,
): void {
  if (alias.mode === "all" || effect !== "allow") {
    for (const rule of alias.rules) {
      onChange(rule.resource, rule.action, effect);
    }
    return;
  }

  const [primary, ...rest] = alias.rules;
  onChange(primary.resource, primary.action, effect);
  for (const rule of rest) {
    onChange(rule.resource, rule.action, "");
  }
}
