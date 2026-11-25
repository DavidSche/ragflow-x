import { createElement, useEffect, useMemo, useRef, useState } from "react";
import {
  useCanAccess,
  useCreatePath,
  useGetResourceLabel,
  useGetIdentity,
  useHasDashboard,
  usePermissions,
  useResourceDefinitions,
  useTranslate,
  LinkBase,
  useMatch,
} from "ra-core";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar";
import { Skeleton } from "@/components/ui/skeleton";
import { ChevronRight, House, List } from "lucide-react";
import { loadBranding } from "../lib/branding";
import { BrandLogo, BrandName } from "../components/BrandIdentity";
import {
  NAVIGATION_VIEW_DEFAULT_EXPANDED,
  resolveNavigationGroups,
  resolveNavigationView,
} from "../lib/navigationRoles";

export function AppSidebar() {
  const hasDashboard = useHasDashboard();
  const resources = useResourceDefinitions();
  const { openMobile, setOpenMobile } = useSidebar();
  const translate = useTranslate();
  const { data: identity } = useGetIdentity();
  const { data: permissionsData } = usePermissions();
  const identityRole = identity && "role" in identity ? String(identity.role ?? "") : "";
  const view = useMemo(
    () => resolveNavigationView(identityRole,
      permissionsData && "permissions" in permissionsData
        ? Array.isArray(permissionsData.permissions)
          ? permissionsData.permissions
          : []
        : [],
    ),
    [identityRole, permissionsData],
  );
  const groups = resolveNavigationGroups(
    view,
    Object.keys(resources ?? {}).filter((name) => resources[name]?.hasList),
  );
  useEffect(() => { loadBranding(); }, []);
  const handleClick = () => {
    if (openMobile) setOpenMobile(false);
  };

  // The dashboard item is the sidebar's only link to "/". For the chat view
  // that link is a detour: "/" redirects to the conversation center, so the
  // item would read "仪表板" and land somewhere else (doc/304 §4 E155).
  const showDashboard = hasDashboard && view !== "chat-user-view";

  const [userExpanded, setUserExpanded] = useState<Record<string, boolean>>({});
  // Collapse defaults are seeded once the role/identity resolves; the guard is
  // a render-phase ref so a later identity refresh or manual fold never
  // clobbers the user state (doc/223 §3 T8 — five-role default collapse).
  const seededDefaultsRef = useRef<Record<string, boolean> | null>(null);
  if (!seededDefaultsRef.current) {
    const defaults = NAVIGATION_VIEW_DEFAULT_EXPANDED[view];
    // Seed only once the identity role has actually resolved: usePermissions
    // can beat useGetIdentity, and an empty role with wildcard permissions
    // would mis-seed an inferred audit view and burn the guard (doc/223 §3 T8).
    if (identityRole && defaults && groups.length > 0) {
      const seed: Record<string, boolean> = {};
      for (const g of groups) seed[g.key] = defaults.includes(g.key);
      seededDefaultsRef.current = seed;
    }
  }
  const expanded: Record<string, boolean> = { ...(seededDefaultsRef.current ?? {}), ...userExpanded };

  const toggleGroup = (i18nKey: string) => {
    setUserExpanded((prev) => ({ ...prev, [i18nKey]: !expanded[i18nKey] }));
  };

  return (
    <Sidebar variant="floating" collapsible="icon" aria-label="主导航侧边栏" data-view={view} data-seed-map={JSON.stringify(seededDefaultsRef.current)}>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              asChild
              className="data-[slot=sidebar-menu-button]:!p-1.5"
            >
              {/* The brand lockup carries only the product's own identity. The
                  view label (平台管理员视图 / …) used to sit under the name and was
                  clipped in a 256px sidebar; the view is already expressed by the
                  navigation groups themselves and by `data-view`, so it is not
                  repeated here. */}
              <LinkBase to="/">
                <BrandLogo />
                <BrandName />
              </LinkBase>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        {showDashboard ? <DashboardItem onClick={handleClick} /> : null}
        {groups.map((group) => {
          const visibleResources = group.resources;
          const isExpanded = expanded[group.key] ?? true;
          const groupLabel = translate(group.labelKey);
          return (
            <SidebarGroup key={group.key}>
              <button
                type="button"
                data-group-key={group.key}
                className="flex w-full items-center gap-1 px-2 py-1 text-xs font-medium text-sidebar-foreground/70 transition-colors hover:text-sidebar-foreground"
                onClick={() => toggleGroup(group.key)}
                aria-label={`${groupLabel} ${isExpanded ? 'collapse' : 'expand'}`}
                aria-expanded={isExpanded}
              >
                <ChevronRight
                  className={`size-3 shrink-0 transition-transform duration-200 ${
                    isExpanded ? "rotate-90" : ""
                  }`}
                />
                {groupLabel}
              </button>
              {isExpanded && (
                <SidebarGroupContent data-sidebar-group-content={group.key}>
                  <SidebarMenu>
                    {visibleResources.map((name) => (
                      <ResourceGroupItem
                        key={name}
                        name={name}
                        onClick={handleClick}
                      />
                    ))}
                  </SidebarMenu>
                </SidebarGroupContent>
              )}
            </SidebarGroup>
          );
        })}
      </SidebarContent>
      <SidebarFooter />
    </Sidebar>
  );
}

function DashboardItem({ onClick }: { onClick?: () => void }) {
  const translate = useTranslate();
  const label = translate("ra.page.dashboard", { _: "仪表板" });
  const match = useMatch({ path: "/", end: true });
  return (
    <SidebarMenu>
      <SidebarMenuItem>
        <SidebarMenuButton asChild isActive={!!match}>
          <LinkBase to="/" onClick={onClick}>
            <House />
            {label}
          </LinkBase>
        </SidebarMenuButton>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}

function ResourceGroupItem({
  name,
  onClick,
}: {
  name: string;
  onClick?: () => void;
}) {
  const { canAccess, isPending } = useCanAccess({
    resource: name,
    action: "list",
  });
  const resources = useResourceDefinitions();
  const getResourceLabel = useGetResourceLabel();
  const createPath = useCreatePath();
  const to = createPath({ resource: name, type: "list" });
  const match = useMatch({ path: to, end: false });

  if (isPending) {
    return (
      <SidebarMenuItem>
        <Skeleton className="h-8 w-full" />
      </SidebarMenuItem>
    );
  }
  if (!resources || !resources[name] || !canAccess) return null;

  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={!!match}>
        <LinkBase to={to} state={{ _scrollToTop: true }} onClick={onClick}>
          {resources[name].icon ? (
            createElement(resources[name].icon)
          ) : (
            <List />
          )}
          {getResourceLabel(name, 2)}
        </LinkBase>
      </SidebarMenuButton>
    </SidebarMenuItem>
  );
}

