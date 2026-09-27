import type { ResourceProps } from "ra-core";
import { MessageSquareText } from "lucide-react";
import { Workbench } from "./Workbench";

export const workbench: ResourceProps = {
  name: "workbench",
  list: Workbench,
  recordRepresentation: () => "问答工作台",
  options: { label: "问答工作台" },
  icon: MessageSquareText,
};
