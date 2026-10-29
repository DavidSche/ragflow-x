import type { ResourceProps } from "ra-core";
import { FlaskConical } from "lucide-react";
import { TrialRunList } from "./TrialRunList";
import { TrialRunShow } from "./TrialRunShow";

export const trialRuns: ResourceProps = {
  name: "trial-runs",
  list: TrialRunList,
  show: TrialRunShow,
  recordRepresentation: (record: { id: string }) => record.id,
  options: { label: "scenarioTemplates.trial.resourceLabel" },
  icon: FlaskConical,
};

export { TrialRunList, TrialRunShow };
