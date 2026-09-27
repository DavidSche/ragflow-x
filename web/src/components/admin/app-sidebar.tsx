import { createElement } from "react";
import {
  useCanAccess,
  useCreatePath,
  useGetResourceLabel,
  useHasDashboard,
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
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from "@/components/ui/sidebar";
import { Skeleton } from "@/components/ui/skeleton";
import { House, List, Shell } from "lucide-react";

/**
 * Menu groups rendered in the sidebar, in display order. This is the single
 * source of truth for menu grouping and ordering so navigation stays stable
 * regardless of resource registration order.
 */
const MENU_GROUPS: { key: string; title: string }[] = [
  { key: "governance", title: "组织与权限" },
  { key: "content", title: "内容治理" },
  { key: "conversation", title: "对话运营" },
  { key: "gateway", title: "网关与模型" },
  { key: "obs", title: "观测与运维" },
];

/** Maps each registered resource to its menu group and intra-group order. */
const RESOURCE_MENU: Record<string, { group: string; order: number }> = {
  tenants: { group: "governance", order: 1 },
  users: { group: "governance", order: 2 },
  teams: { group: "governance", order: 3 },
  roles: { group: "governance", order: 4 },
  projects: { group: "governance", order: 5 },
  "enterprise-connections": { group: "governance", order: 6 },
  datasets: { group: "content", order: 1 },
  tasks: { group: "content", order: 2 },
  "scenario-templates": { group: "content", order: 3 },
  "knowledge-ops": { group: "content", order: 4 },
  "asset-governance": { group: "content", order: 5 },
  "release-governance": { group: "content", order: 6 },
  "assistant-releases": { group: "content", order: 7 },
  workbench: { group: "conversation", order: 1 },
	chats: { group: "conversation", order: 2 },
	"search-apps": { group: "conversation", order: 3 },
	memories: { group: "conversation", order: 4 },
	agents: { group: "conversation", order: 5 },
	"conversation-center": { group: "conversation", order: 6 },
	"model-providers": { group: "gateway", order: 1 },
  "api-keys": { group: "gateway", order: 2 },
  usage: { group: "gateway", order: 3 },
  approvals: { group: "obs", order: 1 },
  "approval-policies": { group: "obs", order: 2 },
  audit: { group: "obs", order: 3 },
  alerts: { group: "obs", order: 4 },
  "alert-deliveries": { group: "obs", order: 5 },
  branding: { group: "obs", order: 5 },
  system: { group: "obs", order: 6 },
};

/**
 * Navigation sidebar displaying grouped menu items. The sidebar can collapse to
 * an icon-only view and renders as a collapsible drawer on mobile devices. It
 * automatically includes the dashboard (if defined) and the list views of all
 * resources, grouped and ordered by MENU_GROUPS / RESOURCE_MENU.
 */
export function AppSidebar() {
  const hasDashboard = useHasDashboard();
  const resources = useResourceDefinitions();
  const { openMobile, setOpenMobile } = useSidebar();
  const handleClick = () => {
    if (openMobile) {
      setOpenMobile(false);
    }
  };

  const visible = Object.keys(resources).filter((name) => resources[name].hasList);

  const itemsByGroup = new Map<string, string[]>();
  for (const name of visible) {
    const meta = RESOURCE_MENU[name] ?? { group: "__other__", order: 999 };
    const list = itemsByGroup.get(meta.group) ?? [];
    list.push(name);
    itemsByGroup.set(meta.group, list);
  }
  const groups = MENU_GROUPS.filter((g) => (itemsByGroup.get(g.key)?.length ?? 0) > 0);
  const otherNames = (itemsByGroup.get("__other__") ?? []).slice().sort();

  return (
    <Sidebar variant="floating" collapsible="icon">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton asChild className="data-[slot=sidebar-menu-button]:!p-1.5">
              <LinkBase to="/">
                <Shell className="!size-5" />
                <span className="text-base font-semibold">RAGFlow-X</span>
              </LinkBase>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        {hasDashboard ? (
          <SidebarGroup>
            <SidebarGroupContent>
              <SidebarMenu>
                <DashboardMenuItem onClick={handleClick} />
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        ) : null}
        {groups.map((group) => {
          const names = (itemsByGroup.get(group.key) ?? []).sort(
            (a, b) => (RESOURCE_MENU[a]?.order ?? 999) - (RESOURCE_MENU[b]?.order ?? 999),
          );
          return (
            <SidebarGroup key={group.key}>
              <SidebarGroupLabel>{group.title}</SidebarGroupLabel>
              <SidebarGroupContent>
                <SidebarMenu>
                  {names.map((name) => (
                    <ResourceMenuItem key={name} name={name} onClick={handleClick} />
                  ))}
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
          );
        })}
        {otherNames.length > 0 ? (
          <SidebarGroup>
            <SidebarGroupLabel>其他</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {otherNames.map((name) => (
                  <ResourceMenuItem key={name} name={name} onClick={handleClick} />
                ))}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        ) : null}
      </SidebarContent>
      <SidebarFooter />
    </Sidebar>
  );
}

/**
 * Menu item for the dashboard link in the sidebar.
 */
export const DashboardMenuItem = ({ onClick }: { onClick?: () => void }) => {
  const translate = useTranslate();
  const label = translate("ra.page.dashboard", {
    _: "Dashboard",
  });
  const match = useMatch({ path: "/", end: true });
  return (
    <SidebarMenuItem>
      <SidebarMenuButton asChild isActive={!!match}>
        <LinkBase to="/" onClick={onClick}>
          <House />
          {label}
        </LinkBase>
      </SidebarMenuButton>
    </SidebarMenuItem>
  );
};

/**
 * Menu item for a resource link in the sidebar.
 */
export const ResourceMenuItem = ({
  name,
  onClick,
}: {
  name: string;
  onClick?: () => void;
}) => {
  const { canAccess, isPending } = useCanAccess({
    resource: name,
    action: "list",
  });
  const resources = useResourceDefinitions();
  const getResourceLabel = useGetResourceLabel();
  const createPath = useCreatePath();
  const to = createPath({
    resource: name,
    type: "list",
  });
  const match = useMatch({ path: to, end: false });

  if (isPending) {
    return <Skeleton className="h-8 w-full" />;
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
};
