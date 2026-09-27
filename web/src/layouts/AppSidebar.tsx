import { createElement, useEffect, useMemo, useState } from "react";
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
import { ChevronRight, House, List, Shell } from "lucide-react";
import { useBranding, loadBranding } from "../lib/branding";
import {
  NAVIGATION_VIEW_PROFILES,
  resolveNavigationGroups,
  resolveNavigationView,
} from "../lib/navigationRoles";

export function AppSidebar() {
  const hasDashboard = useHasDashboard();
  const resources = useResourceDefinitions();
  const { openMobile, setOpenMobile } = useSidebar();
  const brand = useBranding();
  const translate = useTranslate();
  const { data: identity } = useGetIdentity();
  const { data: permissionsData } = usePermissions();
  const view = useMemo(
    () => resolveNavigationView(
      identity && "role" in identity ? String(identity.role ?? "") : "",
      permissionsData && "permissions" in permissionsData
        ? Array.isArray(permissionsData.permissions)
          ? permissionsData.permissions
          : []
        : [],
    ),
    [identity, permissionsData],
  );
  const profile = NAVIGATION_VIEW_PROFILES[view];
  const groups = resolveNavigationGroups(
    view,
    Object.keys(resources ?? {}).filter((name) => resources[name]?.hasList),
  );
  useEffect(() => { loadBranding(); }, []);
  const handleClick = () => {
    if (openMobile) setOpenMobile(false);
  };

  const [expanded, setExpanded] = useState<Record<string, boolean>>({});

  const toggleGroup = (i18nKey: string) => {
    setExpanded((prev) => ({ ...prev, [i18nKey]: !prev[i18nKey] }));
  };

  return (
    <Sidebar variant="floating" collapsible="icon" aria-label="主导航侧边栏">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton
              asChild
              className="data-[slot=sidebar-menu-button]:!p-1.5"
            >
              <LinkBase to="/">
                <Shell className="!size-5" />
                <span className="flex flex-col">
                  <span className="text-base font-semibold">RAGFlow-X</span>
                  <span className="text-xs font-normal text-sidebar-foreground/70">
                    {translate(profile.labelKey)}
                  </span>
                </span>
              </LinkBase>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        {hasDashboard ? <DashboardItem onClick={handleClick} /> : null}
        {groups.map((group) => {
          const visibleResources = group.resources;
          const isExpanded = expanded[group.key] ?? true;
          const groupLabel = translate(group.labelKey);
          return (
            <SidebarGroup key={group.key}>
              <button
                type="button"
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
                <SidebarGroupContent>
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

