import type { ResourceProps } from "ra-core";
import { Shield } from "lucide-react";
import { RoleList } from "./RoleList";

export const roles: ResourceProps = {
  name: "roles",
  list: RoleList,
  icon: Shield,
};
