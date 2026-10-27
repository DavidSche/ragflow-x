import { useCallback, useEffect, useState } from "react";
import { useCanAccess, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { api, ApiError, unwrap, type ApiEnvelope } from "../../lib/api";

interface CostMetric {
  id: string;
  request_id: string;
  tenant_id: string;
  user_id: string;
  key_id: string;
  model: string;
  scenario: string;
  chat_id: string;
  session_id: string;
  dataset_ids: string;
  tokens_in: number;
  tokens_out: number;
  estimated_cost: number;
  created_at: string;
}

interface PageEnvelope<T> {
  items: T[];
  total: number;
}

export interface UsageDetailFilters {
  tenant_id: string;
  user_id: string;
  key_id: string;
  chat_id: string;
  model: string;
  scenario: string;
}

const emptyFilters = {
  tenant_id: "", user_id: "", key_id: "", chat_id: "", model: "", scenario: "",
};

export const buildUsageDetailParams = (
  filters: typeof emptyFilters,
  governanceScope: boolean,
): URLSearchParams => {
  const params = new URLSearchParams({ page: "1", page_size: "20" });
  if (governanceScope) params.set("scope", "all");
  for (const [key, value] of Object.entries(filters)) {
    if (value.trim()) params.set(key, value.trim());
  }
  return params;
};

const shortID = (value: string) => (value ? `${value.slice(0, 8)}…` : "-");
const formatTime = (value: string) => (value ? new Date(value).toLocaleString() : "-");

export const UsageDetailPanel = () => {
  const t = useTranslate();
  const { data: governanceScope } = useCanAccess({
    resource: "usage",
    action: "governance.read",
  });
  const [filters, setFilters] = useState(emptyFilters);
  const [page, setPage] = useState(1);
  const [items, setItems] = useState<CostMetric[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const result = await unwrap(api.get<ApiEnvelope<PageEnvelope<CostMetric>>>(
        `/usage/details?${buildUsageDetailParams(filters, governanceScope === true)}`,
      ));
      setItems(result.items ?? []);
      setTotal(result.total ?? 0);
      setError("");
    } catch (err) {
      setItems([]);
      setTotal(0);
      setError(err instanceof ApiError ? err.displayMessage : t("usage.detail_load_failed"));
    } finally {
      setLoading(false);
    }
  }, [filters, governanceScope, t]);

  useEffect(() => {
    void load();
  }, [load, page]);

  const setFilter = (key: keyof typeof emptyFilters, value: string) => {
    setPage(1);
    setFilters((prev) => ({ ...prev, [key]: value }));
  };
  const totalPages = Math.max(1, Math.ceil(total / 20));

  return (
    <Card className="mt-4">
      <CardHeader>
        <CardTitle className="text-sm font-medium">{t("usage.detail_title")}</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="grid gap-2 md:grid-cols-3 xl:grid-cols-6">
          {(["model", "scenario", "chat_id", "user_id", "key_id"] as const).map((key) => (
            <Input
              key={key}
              aria-label={t(`usage.filter_${key}`)}
              placeholder={t(`usage.filter_${key}`)}
              value={filters[key]}
              onChange={(event) => setFilter(key, event.target.value)}
            />
          ))}
          {governanceScope ? (
            <Input
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
          <p className="mt-3 text-sm text-muted-foreground">{t("usage.detail_empty")}</p>
        ) : (
          <div className="mt-3 overflow-x-auto">
            <table className="w-full min-w-[900px] text-sm">
              <thead>
                <tr className="border-b text-left text-xs uppercase text-muted-foreground">
                  <th className="px-2 py-2">{t("usage.created_at")}</th>
                  <th className="px-2 py-2">{t("usage.request_id")}</th>
                  <th className="px-2 py-2">{t("usage.assistant")}</th>
                  <th className="px-2 py-2">{t("usage.model")}</th>
                  <th className="px-2 py-2">{t("usage.scenario")}</th>
                  <th className="px-2 py-2">{t("usage.tokens_in")}</th>
                  <th className="px-2 py-2">{t("usage.tokens_out")}</th>
                  <th className="px-2 py-2">{t("usage.estimated_cost")}</th>
                </tr>
              </thead>
              <tbody>
                {items.map((item) => (
                  <tr key={item.request_id} className="border-b last:border-0">
                    <td className="px-2 py-2">{formatTime(item.created_at)}</td>
                    <td className="px-2 py-2 font-mono text-xs">{shortID(item.request_id)}</td>
                    <td className="px-2 py-2 font-mono text-xs">{shortID(item.chat_id)}</td>
                    <td className="px-2 py-2">{item.model || "-"}</td>
                    <td className="px-2 py-2">{item.scenario || "-"}</td>
                    <td className="px-2 py-2 tabular-nums">{item.tokens_in}</td>
                    <td className="px-2 py-2 tabular-nums">{item.tokens_out}</td>
                    <td className="px-2 py-2 tabular-nums">{Number(item.estimated_cost ?? 0).toFixed(4)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        <div className="mt-3 flex items-center justify-between text-sm">
          <span className="text-muted-foreground">{t("usage.total")}: {total}</span>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" disabled={page <= 1 || loading} onClick={() => setPage((value) => Math.max(1, value - 1))}>
              {t("usage.previous")}
            </Button>
            <span className="self-center">{page} / {totalPages}</span>
            <Button variant="outline" size="sm" disabled={page >= totalPages || loading} onClick={() => setPage((value) => value + 1)}>
              {t("usage.next")}
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
};
