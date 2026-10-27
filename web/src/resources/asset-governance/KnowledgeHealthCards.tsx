import { useTranslate } from "ra-core";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

export interface KnowledgeHealthSummary {
  dataset_count: number;
  lifecycle: {
    current: number;
    due: number;
    expired: number;
    missing_owner: number;
    unreviewed: number;
  };
  parse: {
    task_count: number;
    done: number;
    running: number;
    queued: number;
    failed: number;
    stopped: number;
    ready_rate: number;
  };
  average_quality_score: number;
  low_quality_count: number;
  missing_classification: number;
  citation_missing_count: number;
  citation_window_days: number;
}

const percent = (value?: number) => `${Math.round((value || 0) * 1000) / 10}%`;

export const KnowledgeHealthCards = ({ summary }: { summary: KnowledgeHealthSummary | null }) => {
  const t = useTranslate();
  const rows = [
    {
      key: "parse_ready_rate",
      value: percent(summary?.parse.ready_rate),
      hint: `${summary?.parse.done ?? 0} / ${summary?.parse.task_count ?? 0}`,
    },
    {
      key: "governed_datasets",
      value: `${summary?.lifecycle.current ?? 0} / ${summary?.dataset_count ?? 0}`,
      hint: t("assetGovernance.knowledge_health_governed_hint"),
    },
    {
      key: "quality",
      value: `${Math.round(summary?.average_quality_score ?? 0)}`,
      hint: `${summary?.low_quality_count ?? 0} ${t("assetGovernance.knowledge_health_low_quality")}`,
    },
    {
      key: "citation_missing",
      value: String(summary?.citation_missing_count ?? 0),
      hint: t("assetGovernance.knowledge_health_citation_hint"),
    },
  ];
  const risks = [
    { key: "expired", value: summary?.lifecycle.expired ?? 0 },
    { key: "due", value: summary?.lifecycle.due ?? 0 },
    { key: "missing_owner", value: summary?.lifecycle.missing_owner ?? 0 },
    { key: "missing_classification", value: summary?.missing_classification ?? 0 },
    { key: "parse_failed", value: summary?.parse.failed ?? 0 },
  ];

  return (
    <div className="space-y-3">
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        {rows.map((row) => (
          <Card key={row.key}>
            <CardHeader className="pb-2">
              <CardTitle className="text-xs text-muted-foreground">
                {t(`assetGovernance.knowledge_health_${row.key}`)}
              </CardTitle>
            </CardHeader>
            <CardContent>
              <p className="text-xl font-semibold">{row.value}</p>
              <p className="mt-1 text-xs text-muted-foreground">{row.hint}</p>
            </CardContent>
          </Card>
        ))}
      </div>
      <div className="flex flex-wrap gap-2 text-xs">
        {risks.map((risk) => (
          <span key={risk.key} className="rounded border bg-muted/50 px-2 py-1">
            {t(`assetGovernance.knowledge_health_risk_${risk.key}`)}: {risk.value}
          </span>
        ))}
      </div>
    </div>
  );
};
