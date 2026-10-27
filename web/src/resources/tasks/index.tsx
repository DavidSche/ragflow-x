import type { ResourceProps } from "ra-core";
import { ListChecks } from "lucide-react";
import { TaskList } from "./TaskList";

export const tasks: ResourceProps = {
  name: "tasks",
  list: TaskList,
  icon: ListChecks,
};
