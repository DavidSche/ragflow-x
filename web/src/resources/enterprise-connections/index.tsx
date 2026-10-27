import type { ResourceProps } from "ra-core";
import { Network } from "lucide-react";
import { EnterpriseConnectionList } from "./EnterpriseConnectionList";

export const enterpriseConnections: ResourceProps = {
  name: "enterprise-connections",
  list: EnterpriseConnectionList,
  recordRepresentation: (record: { display_name?: string; provider_name?: string }): string =>
    record.display_name || record.provider_name || "",
  options: { label: "企业连接" },
  icon: Network,
};
