import type { ResourceProps } from "ra-core";
import { ScrollText } from "lucide-react";
import { AuditList } from "./AuditList";

export const audit: ResourceProps = {
  name: "audit",
  list: AuditList,
  icon: ScrollText,
};
