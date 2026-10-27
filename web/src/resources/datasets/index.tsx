import type { ResourceProps } from "ra-core";
import { Database } from "lucide-react";
import { DatasetList } from "./DatasetList";
import { DatasetCreate } from "./DatasetCreate";
import { DatasetShow } from "./DatasetShow";

export const datasets: ResourceProps = {
  name: "datasets",
  list: DatasetList,
  create: DatasetCreate,
  show: DatasetShow,
  recordRepresentation: (record) => record.name,
  icon: Database,
};
