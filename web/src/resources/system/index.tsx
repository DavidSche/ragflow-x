import type { ResourceProps } from "ra-core";
import { Settings } from "lucide-react";
import { SystemConfig } from "./SystemConfig";

export const system: ResourceProps = {
  name: "system",
  list: SystemConfig,
  options: { label: "系统配置" },
  icon: Settings,
};
