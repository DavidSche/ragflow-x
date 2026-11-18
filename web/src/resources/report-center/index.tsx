import type { ResourceProps } from "ra-core";
import { FileBarChart } from "lucide-react";
import { ReportCenter } from "./ReportCenter";

export const reportCenter: ResourceProps = {
  name: "report-center",
  list: ReportCenter,
  recordRepresentation: () => "报告中心",
  options: { label: "reportCenter.title" },
  icon: FileBarChart,
};

export { ReportCenter };
