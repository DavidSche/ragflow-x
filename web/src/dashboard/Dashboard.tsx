/**
 * Dashboard – platform overview with summary cards, charts, and health status.
 * Content is rendered and fetched according to the caller's permissions so
 * lower-privilege roles never hit permission-denied errors.
 */
import { useEffect, useState, useMemo, type ReactElement } from "react";
import { Breadcrumb, BreadcrumbPage } from "@/components/admin";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Activity,
  Cpu,
  Database,
  Download,
  FileText,
  KeyRound,
  Landmark,
  Users,
} from "lucide-react";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
  PieChart,
  Pie,
  Cell,
  Legend,
} from "recharts";
import { api } from "../lib/api";
import { LinkBase, useCanAccess, useNotify, useTranslate } from "ra-core";

interface Summary {
  tenants: number;
  users: number;
  datasets: number;
  documents: number;
  providers: number;
  api_keys: number;
}

interface UsageRow {
  date: string;
  tokens_in: number;
  tokens_out: number;
  requests: number;
  estimated_cost: number;
}

interface Envelope<T> {
  code: number;
  data: T;
}

type TimeRange = "7d" | "30d" | "90d";

const PIE_COLORS = [
  "hsl(var(--chart-1))",
  "hsl(var(--chart-2))",
  "hsl(var(--chart-3))",
  "hsl(var(--chart-4))",
  "hsl(var(--chart-5))",
];

function dateRange(days: number): { from: string; to: string } {
  const now = new Date();
  const to = now.toISOString().slice(0, 10);
  const from = new Date(now.getTime() - days * 86400000)
    .toISOString()
    .slice(0, 10);
  return { from, to };
}

function formatNumber(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
  return String(n);
}

function exportCsv(rows: Record<string, unknown>[], filename: string) {
  if (rows.length === 0) return;
  const headers = Object.keys(rows[0]);
  const csvContent = [
    headers.join(","),
    ...rows.map((row) =>
      headers
        .map((h) => {
          const val = row[h];
          const str = String(val ?? "");
          if (str.includes(",") || str.includes("\n") || str.includes('"')) {
            return `"${str.replace(/"/g, '""')}"`;
          }
          return str;
        })
        .join(","),
    ),
  ].join("\n");

  const blob = new Blob(["\uFEFF" + csvContent], {
    type: "text/csv;charset=utf-8;",
  });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `${filename}.csv`;
  a.click();
  URL.revokeObjectURL(url);
}

export const Dashboard = () => {
  const notify = useNotify();
  const [summary, setSummary] = useState<Summary | null>(null);
  const [health, setHealth] = useState<Record<string, string> | null>(null);
  const [usage, setUsage] = useState<UsageRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [usageLoading, setUsageLoading] = useState(false);
  const [timeRange, setTimeRange] = useState<TimeRange>("7d");
  const t = useTranslate();

  const { canAccess: canTenants } = useCanAccess({ resource: "tenant", action: "read" });
  const { canAccess: canUsers } = useCanAccess({ resource: "user", action: "read" });
  const { canAccess: canDatasets } = useCanAccess({ resource: "dataset", action: "read" });
  const { canAccess: canDocuments } = useCanAccess({ resource: "document", action: "read" });
  const { canAccess: canProviders } = useCanAccess({ resource: "model-provider", action: "read" });
  const { canAccess: canApiKeys } = useCanAccess({ resource: "api-key", action: "read" });
  const { canAccess: canDashboard } = useCanAccess({ resource: "dashboard", action: "read" });
  const { canAccess: canUsage } = useCanAccess({ resource: "usage", action: "read" });
  const { canAccess: canHealth } = useCanAccess({ resource: "system-health", action: "read" });

  const showUsage = canUsage;

  const TIME_RANGE_OPTIONS: { value: TimeRange; label: string; days: number }[] = [
    { value: "7d", label: t("dashboard.range_7d"), days: 7 },
    { value: "30d", label: t("dashboard.range_30d"), days: 30 },
    { value: "90d", label: t("dashboard.range_90d"), days: 90 },
  ];

  // Load summary + health on mount (only when permitted).
  useEffect(() => {
    setLoading(true);
    const jobs: Promise<void>[] = [];
    if (canDashboard) {
      jobs.push(
        api
          .get<Envelope<Summary>>("/dashboard")
          .then((r) => {
            if (r.data.code === 0) setSummary(r.data.data);
          })
          .then(() => undefined),
      );
    }
    if (canHealth) {
      jobs.push(
        api
          .get<Envelope<Record<string, string>>>("/system/health")
          .then((r) => {
            if (r.data.code === 0) setHealth(r.data.data);
          })
          .then(() => undefined),
      );
    }
    Promise.allSettled(jobs)
      .then((results) => {
        if (results.some((r) => r.status === "rejected")) {
          notify(t("dashboard.load_fail"), { type: "error" });
        }
      })
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [notify, canDashboard, canHealth]);

  // Load usage data when time range changes (only when permitted).
  useEffect(() => {
    if (!showUsage) return;
    setUsageLoading(true);
    const { from } = dateRange(TIME_RANGE_OPTIONS.find((o) => o.value === timeRange)!.days);
    api
      .get<Envelope<UsageRow[]>>(`/usage?date_from=${from}`)
      .then((res) => {
        if (res.data.code === 0) setUsage(res.data.data ?? []);
      })
      .catch(() => {
        // usage may not have data yet – show empty chart
      })
      .finally(() => setUsageLoading(false));
  }, [timeRange, showUsage]);

  const engineUp = health?.engine === "up";

  const chartData = useMemo(() => {
    if (usage.length === 0) return [];
    return usage
      .slice()
      .sort((a, b) => a.date.localeCompare(b.date))
      .map((row) => ({
        date: row.date.slice(5),
        [t("dashboard.tokens_in")]: row.tokens_in,
        [t("dashboard.tokens_out")]: row.tokens_out,
        [t("dashboard.requests")]: row.requests,
      }));
  }, [usage, t]);

  const tokenDistribution = useMemo(() => {
    const totalIn = usage.reduce((s, r) => s + r.tokens_in, 0);
    const totalOut = usage.reduce((s, r) => s + r.tokens_out, 0);
    if (totalIn === 0 && totalOut === 0) return [];
    return [
      { name: t("dashboard.tokens_in"), value: totalIn },
      { name: t("dashboard.tokens_out"), value: totalOut },
    ].filter((d) => d.value > 0);
  }, [usage, t]);

  const totalTokens = usage.reduce((s, r) => s + r.tokens_in + r.tokens_out, 0);
  const totalRequests = usage.reduce((s, r) => s + r.requests, 0);
  const totalEstimatedCost = usage.reduce((s, r) => s + r.estimated_cost, 0);

  return (
    <>
      <Breadcrumb>
        <BreadcrumbPage>{t("ra.page.dashboard")}</BreadcrumbPage>
      </Breadcrumb>

      {/* Summary Cards – only for resources the caller may read */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {loading && (canDashboard || canTenants || canUsers || canDatasets || canProviders || canApiKeys || canDocuments) ? (
          Array.from({ length: 6 }).map((_, i) => (
            <Card key={i}>
              <CardContent className="p-4">
                <Skeleton className="h-4 w-20 mb-2" />
                <Skeleton className="h-8 w-16" />
              </CardContent>
            </Card>
          ))
        ) : (
          <>
            {canTenants ? (
              <StatCard icon={<Landmark className="size-6" />} title={t("dashboard.tenants")} value={summary?.tenants ?? 0} to="/tenants" />
            ) : null}
            {canUsers ? <StatCard icon={<Users className="size-6" />} title={t("dashboard.users")} value={summary?.users ?? 0} to="/users" /> : null}
            {canDatasets ? <StatCard icon={<Database className="size-6" />} title={t("dashboard.datasets")} value={summary?.datasets ?? 0} to="/datasets" /> : null}
            {canDocuments ? <StatCard icon={<FileText className="size-6" />} title={t("dashboard.documents")} value={summary?.documents ?? 0} to="/datasets" /> : null}
            {canProviders ? <StatCard icon={<Cpu className="size-6" />} title={t("dashboard.providers")} value={summary?.providers ?? 0} to="/model-providers" /> : null}
            {canApiKeys ? <StatCard icon={<KeyRound className="size-6" />} title={t("dashboard.api_keys")} value={summary?.api_keys ?? 0} to="/api-keys" /> : null}
          </>
        )}
      </div>

      {/* Usage + charts – only when the caller may read usage */}
      {showUsage ? (
        <>
          {!loading && (
            <div className="mt-4 grid gap-4 sm:grid-cols-3">
              <Card>
                <CardContent className="p-4">
                  <p className="text-sm text-muted-foreground">{t("dashboard.total_tokens")}</p>
                  <p className="mt-1 text-2xl font-semibold">{formatNumber(totalTokens)}</p>
                </CardContent>
              </Card>
              <Card>
                <CardContent className="p-4">
                  <p className="text-sm text-muted-foreground">{t("dashboard.total_requests")}</p>
                  <p className="mt-1 text-2xl font-semibold">{formatNumber(totalRequests)}</p>
                </CardContent>
              </Card>
              <Card>
                <CardContent className="p-4">
                  <p className="text-sm text-muted-foreground">{t("dashboard.total_estimated_cost")}</p>
                  <p className="mt-1 text-2xl font-semibold">¥{totalEstimatedCost.toFixed(2)}</p>
                </CardContent>
              </Card>
            </div>
          )}

          <div className="mt-6 grid gap-4 lg:grid-cols-3">
            <Card className="lg:col-span-2">
              <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                <CardTitle className="text-sm font-medium">{t("dashboard.token_trend")}</CardTitle>
                <div className="flex items-center gap-1">
                  {TIME_RANGE_OPTIONS.map((opt) => (
                    <Button
                      key={opt.value}
                      size="sm"
                      variant={timeRange === opt.value ? "default" : "ghost"}
                      onClick={() => setTimeRange(opt.value)}
                      className="h-7 text-xs"
                    >
                      {opt.label}
                    </Button>
                  ))}
                  <Button
                    size="sm"
                    variant="ghost"
                    className="h-7 text-xs"
                    onClick={() => {
                      const csvData = usage
                        .slice()
                        .sort((a, b) => a.date.localeCompare(b.date))
                        .map((row) => ({
                          date: row.date,
                          tokens_in: row.tokens_in,
                          tokens_out: row.tokens_out,
                          requests: row.requests,
                          estimated_cost: row.estimated_cost,
                        }));
                      exportCsv(csvData, `token-usage-${timeRange}`);
                      notify(t("dashboard.exported"), { type: "success" });
                    }}
                    disabled={usage.length === 0}
                  >
                    <Download className="size-3" /> {t("dashboard.export_csv")}
                  </Button>
                </div>
              </CardHeader>
              <CardContent>
                {usageLoading ? (
                  <Skeleton className="h-[250px] w-full" />
                ) : chartData.length === 0 ? (
                  <div className="flex h-[250px] items-center justify-center text-sm text-muted-foreground">
                    {t("dashboard.no_usage")}
                  </div>
                ) : (
                  <ResponsiveContainer width="100%" height={250}>
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
                      <Bar dataKey={t("dashboard.tokens_in")} fill="hsl(var(--chart-1))" radius={[4, 4, 0, 0]} />
                      <Bar dataKey={t("dashboard.tokens_out")} fill="hsl(var(--chart-2))" radius={[4, 4, 0, 0]} />
                      <Bar dataKey={t("dashboard.requests")} fill="hsl(var(--chart-3))" radius={[4, 4, 0, 0]} />
                    </BarChart>
                  </ResponsiveContainer>
                )}
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
                <CardTitle className="text-sm font-medium">{t("dashboard.token_dist")}</CardTitle>
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-7 text-xs"
                  onClick={() => {
                    const csvData = tokenDistribution.map((d) => ({
                      type: d.name,
                      value: d.value,
                      percentage:
                        totalTokens > 0 ? `${((d.value / totalTokens) * 100).toFixed(1)}%` : "0%",
                    }));
                    exportCsv(csvData, `token-distribution-${timeRange}`);
                    notify(t("dashboard.exported"), { type: "success" });
                  }}
                  disabled={tokenDistribution.length === 0}
                >
                  <Download className="size-3" /> {t("dashboard.export_csv")}
                </Button>
              </CardHeader>
              <CardContent>
                {usageLoading ? (
                  <Skeleton className="h-[250px] w-full" />
                ) : tokenDistribution.length === 0 ? (
                  <div className="flex h-[250px] items-center justify-center text-sm text-muted-foreground">
                    {t("dashboard.no_data")}
                  </div>
                ) : (
                  <ResponsiveContainer width="100%" height={250}>
                    <PieChart>
                      <Pie
                        data={tokenDistribution}
                        cx="50%"
                        cy="50%"
                        innerRadius={60}
                        outerRadius={90}
                        paddingAngle={5}
                        dataKey="value"
                      >
                        {tokenDistribution.map((_, index) => (
                          <Cell key={`cell-${index}`} fill={PIE_COLORS[index % PIE_COLORS.length]} />
                        ))}
                      </Pie>
                      <Tooltip
                        contentStyle={{
                          backgroundColor: "hsl(var(--background))",
                          border: "1px solid hsl(var(--border))",
                          borderRadius: "8px",
                          fontSize: "12px",
                        }}
                        formatter={(value) =>
                          typeof value === "number" ? formatNumber(value) : value
                        }
                      />
                      <Legend wrapperStyle={{ fontSize: "12px" }} />
                    </PieChart>
                  </ResponsiveContainer>
                )}
              </CardContent>
            </Card>
          </div>
        </>
      ) : null}

      {/* Health Status – only when the caller may read system health */}
      {canHealth ? (
        <Card className="mt-6">
          <CardContent className="flex items-center justify-between p-4 text-sm">
            <span className="flex items-center gap-2">
              <Activity className="size-4" />
              {t("dashboard.engine")}
            </span>
            <div className="flex items-center gap-4">
              {health?.db && (
                <span className="text-xs text-muted-foreground">
                  DB: {health.db === "up" ? "✅" : "❌"}
                </span>
              )}
              {health?.redis && (
                <span className="text-xs text-muted-foreground">
                  Redis: {health.redis === "up" ? "✅" : "❌"}
                </span>
              )}
              {health?.doc_engine && (
                <span className="text-xs text-muted-foreground">
                  DocEngine: {health.doc_engine === "up" ? "✅" : "❌"}
                </span>
              )}
              <span className={engineUp ? "text-green-600" : "text-red-600"}>
                {engineUp ? t("dashboard.engine_online") : t("dashboard.engine_offline")}
              </span>
            </div>
          </CardContent>
        </Card>
      ) : null}
    </>
  );
};

function StatCard({
  icon,
  title,
  value,
  to,
}: {
  icon: ReactElement;
  title: string;
  value: number | string;
  to?: string;
}) {
  return (
    <Card>
      <CardContent className="flex items-center justify-between p-4">
        <div>
          <p className="text-sm text-muted-foreground">{title}</p>
          <p className="mt-1 text-2xl font-semibold">{value}</p>
        </div>
        <div className="text-muted-foreground">{icon}</div>
      </CardContent>
      {to ? (
        <LinkBase
          to={to}
          className="block border-t px-4 py-2 text-xs text-muted-foreground hover:text-foreground"
        >
          {title} →
        </LinkBase>
      ) : null}
    </Card>
  );
}


