import type { ResourceProps } from "ra-core";
import { FolderKanban } from "lucide-react";
import { ProjectList } from "./ProjectList";
import { ProjectShow } from "./ProjectShow";

export const projects: ResourceProps = {
  name: "projects",
  list: ProjectList,
  show: ProjectShow,
  recordRepresentation: (record) => record.name ?? "",
  icon: FolderKanban,
};


