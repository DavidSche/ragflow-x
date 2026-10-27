import type { ResourceProps } from "ra-core";
import { Search } from "lucide-react";
import { SearchAppList } from "./SearchAppList";
import { SearchAppShow } from "./SearchAppShow";

export const searchApps: ResourceProps = {
	name: "search-apps",
	list: SearchAppList,
	show: SearchAppShow,
	recordRepresentation: (record: { name?: string }): string => record.name ?? "",
  options: { label: "检索应用" },
  icon: Search,
};
