import type { ResourceProps } from "ra-core";
import { Gauge } from "lucide-react";
import { UsageList } from "./UsageList";

export const usage: ResourceProps = {
  name: "usage",
  list: UsageList,
  icon: Gauge,
};
