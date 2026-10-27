import type { ResourceProps } from "ra-core";
import { LayoutTemplate } from "lucide-react";
import { ScenarioTemplateWizard } from "./ScenarioTemplateWizard";

export const scenarioTemplates: ResourceProps = {
  name: "scenario-templates",
  list: ScenarioTemplateWizard,
  recordRepresentation: () => "场景模板",
  options: { label: "场景模板" },
  icon: LayoutTemplate,
};
