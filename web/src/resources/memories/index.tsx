import type { ResourceProps } from "ra-core";
import { Brain } from "lucide-react";
import { MemoryList } from "./MemoryList";
import { MemoryShow } from "./MemoryShow";

export const memories: ResourceProps = {
  name: "memories",
  list: MemoryList,
  show: MemoryShow,
  recordRepresentation: (record: { name?: string }): string => record.name ?? "",
  options: { label: "记忆管理" },
  icon: Brain,
};
