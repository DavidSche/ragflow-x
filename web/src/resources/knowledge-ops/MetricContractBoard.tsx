import { useCallback, useEffect, useState } from "react";
import { useTranslate } from "ra-core";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { api, ApiError } from "../../lib/api";

// Metric Contract board (doc/107 §3.3.2, doc/125 §5.2). Read-only rendering of
// the frozen contract: the backend computes gate states; this component only
// maps them to badges and emphasises failing rows.

interface MetricsContractItem {
  key: string;
  label: string;
  group: "routing" | "answer" | "operations";
  value: number | null;
  target: number | null;
  comparator: "gte" | "lte" | "eq" | "";
  format: "percent" | "ratio" | "ms" | "number" | "currency";
  sample_size: number;
  evaluation_version: string;
  judge_type: "rule" | "human_calibration_required";
  gate_state: "passed" | "failed" | "unknown";
  updated_at: string;
}

interface MetricsContractBoard {
  items: MetricsContractItem[];
  route_gate_state: string;
  route_run_name?: string;
  evaluated_at?: string | null;
  window_days: number;
}

const GROUP_KEYS = ["routing", "answer", "operations"] as const;

function formatValue(item: MetricsContractItem): string {
  if (item.value === null || item.value === undefined) return "-";
  switch (item.format) {
    case "percent":
      return `${(item.value * 100).toFixed(1)}%`;
    case "ms":
      return `${Math.round(item.value)} ms`;
    case "currency":
      return `¥${item.value.toFixed(4)}`;
    case "ratio":
      return item.value.toFixed(3);
    default:
      return String(item.value);
  }
}

function formatTarget(item: MetricsContractItem): string {
  if (item.target === null || item.target === undefined || !item.comparator) return "-";
  const symbols: Record<string, string> = { gte: "≥", lte: "≤", eq: "=" };
  const symbol = symbols[item.comparator] ?? "";
  switch (item.format) {
    case "percent":
      return `${symbol}${(item.target * 100).toFixed(0)}%`;
    case "ms":
      return `${symbol}${Math.round(item.target)}ms`;
    case "currency":
      return `${symbol}¥${item.target.toFixed(4)}`;
    case "ratio":
      return `${symbol}${item.target.toFixed(2)}`;
    default:
      return `${symbol}${item.target}`;
  }
}

function GateBadge({ state }: { state: MetricsContractItem["gate_state"] }) {
  const styles: Record<string, string> = {
    passed: "bg-green-100 text-green-800",
    failed: "bg-red-100 text-red-800",
    unknown: "bg-muted text-muted-foreground",
  };
  return (
    <span className={`inline-flex rounded px-1.5 py-0.5 text-xs font-medium ${styles[state] ?? styles.unknown}`}>
      {state}
    </span>
  );
}

function isBreached(item: MetricsContractItem): boolean {
  return item.gate_state === "failed";
}

export function MetricContractBoard() {
  const t = useTranslate();
  const [board, setBoard] = useState<MetricsContractBoard | null>(null);
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const response = await api.get<{ code: number; data: MetricsContractBoard }>("/knowledge-ops/metrics-contract");
      setBoard(response.data.data ?? null);
    } catch (err) {
      if (!(err instanceof ApiError)) throw err;
      setBoard(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const items = board?.items ?? [];

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">{t("knowledgeOps.metrics_contract_title")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        {loading ? (
          <Skeleton className="h-24 w-full" />
        ) : items.length === 0 ? (
          <p className="rounded border border-dashed p-4 text-center text-xs text-muted-foreground">
            {t("knowledgeOps.metrics_contract_empty")}
          </p>
        ) : (
          GROUP_KEYS.map((group) => {
            const groupItems = items.filter((item) => item.group === group);
            if (groupItems.length === 0) return null;
            return (
              <div key={group} className="rounded border">
                <p className="border-b bg-muted/40 px-3 py-1.5 text-xs font-medium">
                  {t(`knowledgeOps.metrics_contract_group_${group}`)}
                </p>
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-left text-muted-foreground">
                      <th className="px-3 py-1.5 font-medium">{t("knowledgeOps.metrics_contract_metric")}</th>
                      <th className="px-3 py-1.5 font-medium">{t("knowledgeOps.metrics_contract_value")}</th>
                      <th className="px-3 py-1.5 font-medium">{t("knowledgeOps.metrics_contract_target")}</th>
                      <th className="px-3 py-1.5 font-medium">{t("knowledgeOps.metrics_contract_sample")}</th>
                      <th className="px-3 py-1.5 font-medium">{t("knowledgeOps.metrics_contract_gate")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {groupItems.map((item) => (
                      <tr key={item.key} className={isBreached(item) ? "bg-red-50" : undefined}>
                        <td className="px-3 py-1.5">
                          {t(`knowledgeOps.metrics_contract_key_${item.key}`)}
                          {item.judge_type === "human_calibration_required" ? (
                            <span className="ml-1 rounded bg-amber-100 px-1 py-0.5 text-[10px] text-amber-800">
                              {t("knowledgeOps.metrics_contract_human_calibration")}
                            </span>
                          ) : null}
                        </td>
                        <td className="px-3 py-1.5 font-medium">{formatValue(item)}</td>
                        <td className="px-3 py-1.5">{formatTarget(item)}</td>
                        <td className="px-3 py-1.5">{item.sample_size}</td>
                        <td className="px-3 py-1.5">
                          <GateBadge state={item.gate_state} />
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            );
          })
        )}
        {board?.route_gate_state ? (
          <p className="text-xs text-muted-foreground">
            {t("knowledgeOps.metrics_contract_route_gate")}: {board.route_gate_state}
            {board.route_run_name ? ` · ${board.route_run_name}` : ""}
          </p>
        ) : null}
      </CardContent>
    </Card>
  );
}
