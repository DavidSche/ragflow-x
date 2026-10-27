import { useState, useMemo } from "react";
import { DataTable, List, ListLoadingBar, SearchInput } from "@/components/admin";
import { AutocompleteInput, ReferenceInput } from "@/components/admin";
import { useCanAccess, useListContext, useNotify } from "ra-core";
import { useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Download, BarChart3 } from "lucide-react";
import { UsageDetailPanel } from "./UsageDetailTable";
import { OperationalAttributionPanel } from "./OperationalAttribution";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  Legend,
} from "recharts";
import { api } from "../../lib/api";

function formatNumber(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(n);
}

function formatCost(n: number): string {
  return `$${n.toFixed(4)}`;
}

const ExportButton = () => {
  const t = useTranslate();
  const notify = useNotify();
  const { data } = useListContext();

  const doExport = () => {
    if (!data || data.length === 0) {
      notify(t("usage.no_data"), { type: "warning" });
      return;
    }
    const header = t("usage.csv_header") + "\n";
    const rows = data
      .map(
        (r: Record<string, unknown>) =>
          `${r.date},${r.key_id},${r.tokens_in},${r.tokens_out},${r.requests},${r.estimated_cost ?? 0}`,
      )
      .join("\n");
    const blob = new Blob(["\uFEFF" + header + rows], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `usage-${new Date().toISOString().slice(0, 10)}.csv`;
    a.click();
    URL.revokeObjectURL(url);
    notify(t("usage.exported"), { type: "success" });
  };

  return (
    <Button size="sm" variant="outline" onClick={doExport} aria-label={t("usage.export_csv")}>
      <Download className="size-4" /> {t("ra.action.export")}
    </Button>
  );
};

const UsageSummary = () => {
  const t = useTranslate();
  const { data } = useListContext();
  const totalTokensIn = (data ?? []).reduce((sum: number, r: Record<string, unknown>) => sum + (Number(r.tokens_in) || 0), 0);
  const totalTokensOut = (data ?? []).reduce((sum: number, r: Record<string, unknown>) => sum + (Number(r.tokens_out) || 0), 0);
  const totalRequests = (data ?? []).reduce((sum: number, r: Record<string, unknown>) => sum + (Number(r.requests) || 0), 0);
  const totalEstimatedCost = (data ?? []).reduce((sum: number, r: Record<string, unknown>) => sum + (Number(r.estimated_cost) || 0), 0);

  return (
    <div className="flex flex-wrap gap-3 rounded border p-3 text-sm">
      <div className="flex items-center gap-1.5">
        <BarChart3 className="size-4 text-muted-foreground" />
        <span className="text-muted-foreground">{t("usage.summary")}</span>
      </div>
      <Badge variant="outline">{t("usage.tokens_in")} {formatNumber(totalTokensIn)}</Badge>
      <Badge variant="outline">{t("usage.tokens_out")} {formatNumber(totalTokensOut)}</Badge>
      <Badge variant="outline">{t("usage.requests")} {formatNumber(totalRequests)}</Badge>
      <Badge variant="outline">{t("usage.estimated_cost")} {formatCost(totalEstimatedCost)}</Badge>
    </div>
  );
};

const UsageChart = () => {
  const t = useTranslate();
  const { data, isLoading } = useListContext();

  const chartData = useMemo(() => {
    if (!data || data.length === 0) return [];
    return data
      .slice()
      .sort((a: Record<string, unknown>, b: Record<string, unknown>) =>
        String(a.date).localeCompare(String(b.date)),
      )
      .map((row: Record<string, unknown>) => ({
        date: String(row.date).slice(5),
        [t("usage.tokens_in")]: Number(row.tokens_in) || 0,
        [t("usage.tokens_out")]: Number(row.tokens_out) || 0,
      }));
  }, [data, t]);

  if (isLoading) {
    return <Skeleton className="h-[200px] w-full" />;
  }

  if (chartData.length === 0) {
    return (
      <div className="flex h-[200px] items-center justify-center text-sm text-muted-foreground">
        {t("usage.empty")}
      </div>
    );
  }

  return (
    <ResponsiveContainer width="100%" height={200}>
      <BarChart data={chartData}>
        <CartesianGrid strokeDasharray="3 3" className="stroke-muted" />
        <XAxis dataKey="date" className="text-xs" />
        <YAxis className="text-xs" tickFormatter={formatNumber} />
        <Tooltip
          contentStyle={{
            backgroundColor: "hsl(var(--background))",
            border: "1px solid hsl(var(--border))",
            borderRadius: "8px",
            fontSize: "12px",
          }}
        />
        <Legend wrapperStyle={{ fontSize: "12px" }} />
        <Bar dataKey={t("usage.tokens_in")} fill="hsl(var(--chart-1))" radius={[4, 4, 0, 0]} />
        <Bar dataKey={t("usage.tokens_out")} fill="hsl(var(--chart-2))" radius={[4, 4, 0, 0]} />
      </BarChart>
    </ResponsiveContainer>
  );
};

export const UsageList = () => {
  const t = useTranslate();
  const { canAccess: canUseGovernanceScope } = useCanAccess({ resource: "usage", action: "governance.read" });
  const filters = [
    <ReferenceInput source="key_id" reference="api-keys" key="key_id">
      <AutocompleteInput label={t("usage.filter_key")} optionText="name" />
    </ReferenceInput>,
  ];
  if (canUseGovernanceScope) {
    filters.push(
      <ReferenceInput source="tenant_id" reference="tenants" key="tenant_id">
        <AutocompleteInput label={t("tenants.tenant_name")} optionText="name" />
      </ReferenceInput>,
    );
  }
  return (
    <List perPage={20} filters={filters} actions={<ExportButton />} aria-label={t("usage.list_title")}>
      <ListLoadingBar />
      <UsageSummary />
      <Card className="mt-4">
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-sm font-medium">{t("usage.tokens_in")} / {t("usage.tokens_out")}</CardTitle>
        </CardHeader>
        <CardContent>
          <UsageChart />
        </CardContent>
      </Card>
      <DataTable className="mt-4" aria-label={t("usage.data_table")}>
        <DataTable.Col source="date" label={t("usage.date")} />
        <DataTable.Col
          source="key_id"
          label={t("usage.key_id")}
          render={(r) => (
            <code className="rounded bg-muted px-1.5 py-0.5 text-xs font-mono">
              {r.key_id ? r.key_id.slice(0, 8) + "…" : "-"}
            </code>
          )}
        />
        <DataTable.NumberCol source="tokens_in" label={t("usage.tokens_in")} />
        <DataTable.NumberCol source="tokens_out" label={t("usage.tokens_out")} />
        <DataTable.NumberCol source="requests" label={t("usage.requests")} />
        <DataTable.Col
          source="estimated_cost"
          label={t("usage.estimated_cost")}
          render={(r) => (
            <span className="tabular-nums">{formatCost(r.estimated_cost ?? 0)}</span>
          )}
        />
      </DataTable>
      <UsageDetailPanel />
      <OperationalAttributionPanel />
    </List>
  );
};
