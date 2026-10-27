import type { ResourceProps } from "ra-core";
import { Building2 } from "lucide-react";
import { TenantList } from "./TenantList";

export const tenants: ResourceProps = {
  name: "tenants",
  list: TenantList,
  recordRepresentation: (record) => record.name,
  icon: Building2,
};
