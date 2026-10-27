import { useCallback, useEffect, useState } from "react";
import { useCanAccess, useTranslate } from "ra-core";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { api, ApiError, unwrap, type ApiEnvelope } from "../../lib/api";

interface OperationalAttributionRow {
  tenant_id: string;
  project_id: string;
  assistant_id: string;
  assistant_release_id: string;
  scenario: string;
  requests: number;
  failed: number;
  no_answer: number;
  tokens_in: number;
  tokens_out: number;
  estimated_cost: number;
  avg_latency_ms: number;
  quota_consumed_tokens: number;
}

const emptyFilters = {
  project_id: "",
  assistant_id: "",
  assistant_release_id: "",
  scenario: "",
  tenant_id: "",
  date_from: "",
  date_to: "",
};

export const buildOperationalAttributionParams = (
  filters: typeof emptyFilters,
  governanceScope: boolean,
): URLSearchParams => {
  const source = governanceScope ? filters : { ...filters, tenant_id: "" };
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(source)) {
    if (value.trim()) params.set(key, value.trim());
  }
  return params;
};

const shortID = (value: string) => (value ? `${value.slice(0, 8)}…` : "-");

export const OperationalAttributionPanel = () => {
  const t = useTranslate();
  const { data: governanceScope } = useCanAccess({
    resource: "usage",
    action: "governance.read",
  });
  const [filters, setFilters] = useState(emptyFilters);
  const [items, setItems] = useState<OperationalAttributionRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const result = await unwrap(api.get<ApiEnvelope<OperationalAttributionRow[]>>(
        `/usage/attribution?${buildOperationalAttributionParams(filters, governanceScope === true)}`,
      ));
      setItems(result ?? []);
      setError("");
    } catch (err) {
      setItems([]);
      setError(err instanceof ApiError ? err.displayMessage : t("usage.attribution_load_failed"));
    } finally {
      setLoading(false);
    }
  }, [filters, governanceScope, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const setFilter = (key: keyof typeof emptyFilters, value: string) => {
    setFilters((prev) => ({ ...prev, [key]: value }));
  };

  return (
    <Card className="mt-4">
      <CardHeader>
        <CardTitle className="text-sm font-medium">{t("usage.attribution_title")}</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="grid gap-2 md:grid-cols-3 xl:grid-cols-6">
          {(["project_id", "assistant_id", "assistant_release_id", "scenario"] as const).map((key) => (
            <Input
              key={key}
              aria-label={t(`usage.filter_${key}`)}
              placeholder={t(`usage.filter_${key}`)}
              value={filters[key]}
              onChange={(event) => setFilter(key, event.target.value)}
            />
          ))}
          <Input
            type="date"
            aria-label={t("usage.filter_date_from")}
            value={filters.date_from}
            onChange={(event) => setFilter("date_from", event.target.value)}
          />
          <Input
            type="date"
            aria-label={t("usage.filter_date_to")}
            value={filters.date_to}
            onChange={(event) => setFilter("date_to", event.target.value)}
          />
          {governanceScope ? (
            <Input
              aria-label={t("usage.filter_tenant_id")}
              placeholder={t("usage.filter_tenant_id")}
              value={filters.tenant_id}
              onChange={(event) => setFilter("tenant_id", event.target.value)}
            />
          ) : null}
        </div>
        {loading ? (
          <Skeleton className="mt-3 h-24 w-full" />
        ) : error ? (
          <p className="mt-3 text-sm text-destructive">{error}</p>
        ) : items.length === 0 ? (
          <p className="mt-3 text-sm text-muted-foreground">{t("usage.attribution_empty")}</p>
        ) : (
          <div className="mt-3 overflow-x-auto">
            <table className="w-full min-w-[1400px] text-sm">
              <thead>
                <tr className="border-b text-left text-xs uppercase text-muted-foreground">
                  <th className="px-2 py-2">{t("usage.project")}</th>
                  <th className="px-2 py-2">{t("usage.assistant")}</th>
                  <th className="px-2 py-2">{t("usage.assistant_release")}</th>
                  <th className="px-2 py-2">{t("usage.scenario")}</th>
                  <th className="px-2 py-2">{t("usage.requests")}</th>
                  <th className="px-2 py-2">{t("usage.failed")}</th>
                  <th className="px-2 py-2">{t("usage.no_answer")}</th>
                  <th className="px-2 py-2">{t("usage.tokens_in")}</th>
                  <th className="px-2 py-2">{t("usage.tokens_out")}</th>
                  <th className="px-2 py-2">{t("usage.estimated_cost")}</th>
                  <th className="px-2 py-2">{t("usage.avg_latency")}</th>
                  <th className="px-2 py-2">{t("usage.quota_tokens")}</th>
                </tr>
              </thead>
              <tbody>
                {items.map((item) => (
                  <tr key={`${item.tenant_id}-${item.project_id}-${item.assistant_id}-${item.assistant_release_id}-${item.scenario}`} className="border-b last:border-0">
                    <td className="px-2 py-2 font-mono text-xs">{shortID(item.project_id)}</td>
                    <td className="px-2 py-2 font-mono text-xs">{shortID(item.assistant_id)}</td>
                    <td className="px-2 py-2 font-mono text-xs">{shortID(item.assistant_release_id)}</td>
                    <td className="px-2 py-2">{item.scenario || "-"}</td>
                    <td className="px-2 py-2 tabular-nums">{item.requests}</td>
                    <td className="px-2 py-2 tabular-nums">{item.failed}</td>
                    <td className="px-2 py-2 tabular-nums">{item.no_answer}</td>
                    <td className="px-2 py-2 tabular-nums">{item.tokens_in}</td>
                    <td className="px-2 py-2 tabular-nums">{item.tokens_out}</td>
                    <td className="px-2 py-2 tabular-nums">${Number(item.estimated_cost ?? 0).toFixed(4)}</td>
                    <td className="px-2 py-2 tabular-nums">{Math.round(Number(item.avg_latency_ms ?? 0))} ms</td>
                    <td className="px-2 py-2 tabular-nums">{item.quota_consumed_tokens}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  );
};
