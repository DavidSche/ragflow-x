import type { ResourceProps } from "ra-core";
import { Bot } from "lucide-react";
import { AgentList } from "./AgentList";
import { AgentShow } from "./AgentShow";

export const agents: ResourceProps = {
  name: "agents",
  list: AgentList,
  show: AgentShow,
  recordRepresentation: (record: { title?: string }): string => record.title ?? "",
  options: { label: "智能体" },
  icon: Bot,
};
