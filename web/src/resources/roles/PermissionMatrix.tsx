import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
  NAVIGATION_PERMISSION_ALIASES,
  applyNavigationPermission,
  isAliasSupported,
  navigationPermissionState,
  type NavigationPermissionAlias,
} from "../../lib/navigationPermissions";

export interface CatEntry {
  resource: string;
  actions: string[];
}

const MODULES: { key: string; labelKey: string; resources: string[]; aliases?: string[] }[] = [
  { key: "org", labelKey: "roles.mod_org", resources: ["tenant", "user", "role", "team", "project"] },
  {
    key: "content", labelKey: "roles.mod_content",
    resources: ["dataset", "dataset-export", "document", "task", "eval-set", "knowledge-lifecycle", "knowledge-ops", "scenario-template", "prompt-policy"],
    aliases: ["asset-governance"],
  },
  {
    key: "chat", labelKey: "roles.mod_chat",
    resources: ["chat", "search-app", "memory", "agent", "assistant"],
    aliases: ["conversation-center", "workbench"],
  },
  { key: "gateway", labelKey: "roles.mod_gateway", resources: ["model-provider", "model-route", "api-key", "usage", "usage-export", "release-governance"] },
  { key: "governance", labelKey: "roles.mod_governance", resources: ["enterprise-connection", "approval", "approval-policy"] },
  { key: "obs", labelKey: "roles.mod_obs", resources: ["alert", "audit", "audit-anchor", "branding", "dashboard", "system-health", "system"] },
];

const ACTIONS = [
  "read", "append", "delete:own", "manage", "execute", "session:create", "test", "governance.read",
  "governance.read_sensitive", "governance.manage", "tenant.resource.manage",
  "tenant.resource.delete", "tenant.connection.manage", "tenant.connection.bind",
  "tenant.connection.unbind", "tenant.connection.rotate_secret",
  "tenant.audit.read", "tenant.usage.read",
];

export function resourceLabel(t: (k: string) => string, res: string): string {
  const k = `roles.res_${res.replace(/[-]+/g, "_")}`;
  const v = t(k);
  return v === k ? res : v;
}

export function actionLabel(t: (k: string) => string, action: string): string {
  const key = `roles.act_${action.replace(/[.:]+/g, "_")}`;
  const label = t(key);
  return label === key ? action : label;
}

export function PermissionMatrix({
  catalog,
  mapState,
  builtin,
  disabled,
  onChange,
}: {
  catalog: CatEntry[];
  mapState: Record<string, string>;
  builtin: boolean;
  disabled: boolean;
  onChange?: (resource: string, action: string, effect: string) => void;
}) {
  const t = useTranslate();
  const byRes = new Map<string, string[]>();
  for (const entry of catalog) {
    const actions = ACTIONS.filter((action) => entry.actions.includes(action));
    if (actions.length) byRes.set(entry.resource, actions);
  }

  const availableRules = new Set(
    [...byRes.entries()].flatMap(([resource, actions]) =>
      actions.map((action) => `${resource}|${action}`),
    ),
  );
  const byAlias = new Map<string, NavigationPermissionAlias>();
  for (const alias of NAVIGATION_PERMISSION_ALIASES) {
    if (isAliasSupported(alias, availableRules)) byAlias.set(alias.resource, alias);
  }

  const allResources = catalog.map((e) => e.resource);
  const groups: { key: string; label: string; resources: string[]; aliases: NavigationPermissionAlias[] }[] = [];
  for (const m of MODULES) {
    const resources = m.resources.filter((r) => byRes.has(r));
    const aliases = (m.aliases ?? [])
      .map((name) => byAlias.get(name))
      .filter((alias): alias is NavigationPermissionAlias => !!alias);
    if (resources.length || aliases.length) {
      groups.push({ key: m.key, label: t(m.labelKey), resources, aliases });
    }
  }
  const leftover = allResources.filter(
    (r) => !MODULES.some((m) => m.resources.includes(r)) &&
      ![...byAlias.values()].some((alias) => alias.rules.some((rule) => rule.resource === r)),
  );
  if (leftover.length) groups.push({ key: "other", label: t("roles.mod_other"), resources: leftover, aliases: [] });

  const setAll = (resources: string[], effect: string) => {
    if (!onChange) return;
    for (const res of resources) {
      for (const act of byRes.get(res) ?? []) onChange(res, act, effect);
    }
  };

  return (
    <div className="max-h-[62vh] overflow-auto rounded border">
      <Table>
        <TableHeader className="sticky top-0 bg-background">
          <TableRow>
        <TableHead className="w-44">{t("roles.mod_resource")}</TableHead>
        {ACTIONS.map((action) => (
          <TableHead key={action} className="min-w-28">
            {actionLabel(t, action)}
          </TableHead>
        ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {groups.map((g) => (
            <GroupRows
              key={g.key}
              label={g.label}
              resources={g.resources}
              aliases={g.aliases}
              byRes={byRes}
              mapState={mapState}
              builtin={builtin}
              disabled={disabled}
              onChange={onChange}
              onSetAll={setAll}
              t={t}
            />
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function GroupRows({
  label,
  resources,
  aliases,
  byRes,
  mapState,
  builtin,
  disabled,
  onChange,
  onSetAll,
  t,
}: {
  label: string;
  resources: string[];
  aliases: NavigationPermissionAlias[];
  byRes: Map<string, string[]>;
  mapState: Record<string, string>;
  builtin: boolean;
  disabled: boolean;
  onChange?: (r: string, a: string, e: string) => void;
  onSetAll: (resources: string[], effect: string) => void;
  t: (k: string) => string;
}) {
  const editable = onChange !== undefined;
  return (
    <>
      <TableRow className="bg-muted/40">
        <TableCell colSpan={ACTIONS.length + 1} className="py-1.5">
          <div className="flex items-center justify-between gap-2">
            <span className="text-xs font-semibold">{label}</span>
            {editable && !builtin && !disabled ? (
              <div className="flex gap-1">
                <Button type="button" variant="ghost" size="sm" onClick={() => onSetAll(resources, "allow")}>
                  {t("roles.class_all")}
                </Button>
                <Button type="button" variant="ghost" size="sm" onClick={() => onSetAll(resources, "")}>
                  {t("roles.class_inherit")}
                </Button>
              </div>
            ) : null}
          </div>
        </TableCell>
      </TableRow>
      {aliases.map((alias) => {
        const label = t(alias.labelKey);
        const state = navigationPermissionState(alias, mapState);
        return (
          <TableRow key={alias.resource}>
            <TableCell className="font-medium">
              {label}
              <span className="ml-2 text-xs text-muted-foreground">{t("roles.navigation_entry")}</span>
            </TableCell>
            {ACTIONS.map((a) => (
              <TableCell key={a}>
                {a === "execute" ? (
                  <select
                    className="h-7 w-20 rounded border bg-background px-1 text-xs"
                    aria-label={`${label} ${actionLabel(t, a)}`}
                    value={state}
                    disabled={disabled || builtin}
                    onChange={(e) => applyNavigationPermission(
                      alias,
                      e.target.value as "" | "allow" | "deny",
                      (resource, action, effect) => onChange?.(resource, action, effect),
                    )}
                  >
                    <option value="">{t("roles.inherit")}</option>
                    <option value="allow">{t("roles.allow")}</option>
                    <option value="deny">{t("roles.deny")}</option>
                    {state === "mixed" ? <option value="mixed">{t("roles.mixed")}</option> : null}
                  </select>
                ) : (
                  <span className="text-xs text-muted-foreground">—</span>
                )}
              </TableCell>
            ))}
          </TableRow>
        );
      })}
      {resources.map((res) => {
        const acts = byRes.get(res) ?? [];
        const label = resourceLabel(t, res);
        return (
          <TableRow key={res}>
            <TableCell className="font-medium">{label}{builtin ? <span className="ml-2 text-xs text-muted-foreground">{t("roles.builtin")}</span> : null}</TableCell>
      {ACTIONS.map((a) => (
			<TableCell key={a}>
				{acts.includes(a) ? (
					<select
						className="h-7 w-20 rounded border bg-background px-1 text-xs"
						aria-label={`${resourceLabel(t, res)} ${actionLabel(t, a)}`}
						value={mapState[`${res}|${a}`] ?? ""}
                    disabled={disabled || builtin}
                    onChange={(e) => onChange?.(res, a, e.target.value)}
                  >
                    <option value="">{t("roles.inherit")}</option>
                    <option value="allow">{t("roles.allow")}</option>
                    <option value="deny">{t("roles.deny")}</option>
                  </select>
                ) : (
                  <span className="text-xs text-muted-foreground">—</span>
                )}
              </TableCell>
            ))}
          </TableRow>
        );
      })}
    </>
  );
}
