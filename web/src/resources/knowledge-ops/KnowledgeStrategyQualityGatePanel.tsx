import { useEffect, useState } from "react";
import { useCanAccess, useNotify, useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api, ApiError } from "@/lib/api";

interface KnowledgeStrategy {
  id: string;
  dataset_id: string;
  strategy_type: string;
  ragflow_capability: string;
  active: boolean;
}

interface KnowledgeStrategyQualityGate {
  run_id: string;
  run_status: string;
  gate_status: string;
  pass?: boolean | null;
  capability: string;
  execution_mode: string;
  eval_set_id: string;
  eval_set_version: number;
  metrics?: Record<string, unknown>;
  evidence_url?: string;
}

interface PageEnvelope<T> {
  items: T[];
  total: number;
}

export const KnowledgeStrategyQualityGatePanel = () => {
  const t = useTranslate();
  const notify = useNotify();
  const { canAccess } = useCanAccess({ resource: "knowledge-strategy", action: "read" });
  const { canAccess: canAccessEvidence } = useCanAccess({ resource: "release-governance", action: "read" });
  const [loading, setLoading] = useState(true);
  const [strategies, setStrategies] = useState<KnowledgeStrategy[]>([]);
  const [qualityGates, setQualityGates] = useState<Record<string, KnowledgeStrategyQualityGate>>({});

  useEffect(() => {
    const loadQualityGates = async () => {
      if (!canAccess) return;
      setLoading(true);
      try {
        const strategyResponse = await api.get<{ code: number; data: PageEnvelope<KnowledgeStrategy> }>(
          "/knowledge-strategies?page=1&page_size=50",
        );
        const data = strategyResponse.data.data;
        const items = data.items ?? [];
        const results = await Promise.all(items.map(async (strategy) => {
          try {
            const qualityResponse = await api.get<{ code: number; data: KnowledgeStrategyQualityGate }>(
              `/knowledge-strategies/${strategy.id}/quality-gate`,
            );
            const quality = qualityResponse.data.data;
            return [strategy.id, quality] as const;
          } catch {
            return [strategy.id, null] as const;
          }
        }));
        setStrategies(items);
        setQualityGates(Object.fromEntries(
          results.filter((entry): entry is readonly [string, KnowledgeStrategyQualityGate] => entry[1] !== null),
        ));
      } catch (error) {
        notify(error instanceof ApiError ? error.displayMessage : t("knowledgeOps.strategy_quality_load_failed"), { type: "error" });
      } finally {
        setLoading(false);
      }
    };
    void loadQualityGates();
  }, [canAccess, notify, t]);

  if (!canAccess) return null;

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("knowledgeOps.strategy_quality_title")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-2">
        {loading ? (
          <div className="text-sm text-muted-foreground">{t("common.loading")}</div>
        ) : strategies.length === 0 ? (
          <div className="text-sm text-muted-foreground">{t("knowledgeOps.strategy_quality_empty")}</div>
        ) : (
          strategies.map((strategy) => {
            const quality = qualityGates[strategy.id];
            const pairCount = typeof quality?.metrics?.pair_count === "number" ? quality.metrics.pair_count : null;
            const hitLift = typeof quality?.metrics?.hit_lift_pp === "number" ? quality.metrics.hit_lift_pp : null;
            return (
              <div key={strategy.id} className="flex flex-wrap items-center gap-2 rounded border p-2 text-sm">
                <span className="font-mono text-xs">{strategy.id}</span>
                <Badge variant="outline">{strategy.strategy_type}</Badge>
                <span className="text-xs text-muted-foreground">{strategy.ragflow_capability}</span>
                {quality ? (
                  <>
                    <Badge variant={quality.gate_status === "PASS" ? "default" : quality.gate_status === "FAIL" ? "destructive" : "secondary"}>
                      {quality.gate_status}
                    </Badge>
                    <span className="text-xs text-muted-foreground">{quality.run_status}</span>
                    {pairCount !== null ? <span>{t("knowledgeOps.strategy_quality_pairs")}: {pairCount}</span> : null}
                    {hitLift !== null ? <span>{t("knowledgeOps.strategy_quality_hit_lift")}: {hitLift}pp</span> : null}
                    {quality.evidence_url && canAccessEvidence ? (
                      <a href={quality.evidence_url} target="_blank" rel="noreferrer" className="underline">
                        {t("knowledgeOps.strategy_quality_evidence")}
                      </a>
                    ) : null}
                  </>
                ) : (
                  <Badge variant="secondary">{t("knowledgeOps.strategy_quality_not_evaluated")}</Badge>
                )}
              </div>
            );
          })
        )}
      </CardContent>
    </Card>
  );
};
