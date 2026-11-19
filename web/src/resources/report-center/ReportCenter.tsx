import { useCallback, useEffect, useMemo, useState } from "react";
import { useCanAccess, useNavigate, useNotify, useTranslate } from "ra-core";
import {
  BookOpen,
  FileText,
  Loader2,
  RefreshCw,
  Search,
  Trash2,
  Undo2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { api, ApiError, unwrap } from "../../lib/api";
import { ReportDetailDrawer } from "./ReportDetailDrawer";
import { ReportReader } from "./ReportReader";
import { ReportRetentionCard } from "./ReportRetentionCard";
import { ReportTemplatePanel } from "./ReportTemplatePanel";
import type { ReportTemplateView } from "../conversation-center/report/report-run";
import {
  formatRatio,
  reportOpenPath,
  reportDeliveryLabel,
  reportListViewLabel,
  REPORT_LIST_VIEWS,
  trendPoints,
  type ReportListView,
  type ReportRow,
  type TrendResult,
} from "./report-center-api";

type TrendView = "own" | "tenant";

interface ReportPage {
  items: ReportRow[];
  total: number;
  page: number;
  page_size: number;
}

const PAGE_SIZE = 20;
const TREND_DAYS = 14;

/**
 * Report centre: the discovery surface the report feature was missing.
 *
 * - list comes from `GET /reports` (read API, never from browser memory);
 * - the quality strip comes from `GET /report-metrics/trend`, and a ratio the
 *   server declined to judge stays "暂无判定" instead of being rendered as 0%;
 * - deleting offers an inline undo that calls `POST /reports/:id/restore`, so
 *   the 7-day recovery window is reachable instead of being a documented
 *   capability nobody can press.
 */
export function ReportCenter() {
  const t = useTranslate();
  const notify = useNotify();
  const navigate = useNavigate();

  const [rows, setRows] = useState<ReportRow[]>([]);
  const [total, setTotal] = useState(0);
  const [trend, setTrend] = useState<TrendResult | null>(null);
  const [loading, setLoading] = useState(true);
  // Held as the thrown value, not as translated text: `load` must stay independent
  // of `t` (see ReportDetailDrawer — an unstable translate identity would make the
  // mount effect re-fetch forever).
  const [error, setError] = useState<unknown>(null);
  // The reading view's target: the row plus which revision to show. Reading a
  // superseded revision is the point of the history list, so the revision travels
  // with the target instead of the reader re-guessing "current".
  const [reader, setReader] = useState<{ row: ReportRow; revision: number } | null>(null);
  const [query, setQuery] = useState("");
  const [pendingDelete, setPendingDelete] = useState<ReportRow | null>(null);
  const [detailRow, setDetailRow] = useState<ReportRow | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [lastDeleted, setLastDeleted] = useState<ReportRow | null>(null);
  const [restoring, setRestoring] = useState(false);
  // Without a page cursor the centre would silently hide report 21 onwards: the
  // list is the only discovery surface, so "there are more" has to be reachable.
  const [page, setPage] = useState(1);
  // Which lifecycle view the list is asking for (doc/297 §2.3). The default is
  // `active`, mirroring the server: an archived report leaves the daily list, and a
  // list that kept showing it would make the archive button do nothing visible.
  const [view, setView] = useState<ReportListView>("active");
  const [trendView, setTrendView] = useState<TrendView>("own");
  // The list carries only `template_key`, which is an identifier for machines. A
  // column headed 「模板」 has to show the template's name, so the catalogue is read
  // once and the key is kept as the hover title. A failed read degrades to the key
  // rather than blanking the column: an identifier is ugly, an empty cell is wrong.
  const [templateNames, setTemplateNames] = useState<Record<string, string>>({});
  // The workspace view is a governance read (`manage:report`); the server enforces
  // it, and asking the same question here stops the toggle being offered to a
  // caller who could only get 403. `useCanAccess` is the client mirror of the RBAC
  // rule, not a second policy.
  const { canAccess: canManageReports } = useCanAccess({ resource: "report", action: "manage" });
  const canManage = canManageReports === true;
  // R19 governance is its own resource: a workspace may let someone manage report
  // clocks without letting them author templates, so the panel follows the same gate
  // the publish/retire routes carry.
  const { canAccess: canManageTemplates } = useCanAccess({
    resource: "report-template",
    action: "manage",
  });
  const canManageTemplate = canManageTemplates === true;
  // Reading the list is `read:report-template` — every report author needs it (the
  // creation flow picks a template), so the panel is shown and its actions are not.
  const { canAccess: canReadTemplates } = useCanAccess({
    resource: "report-template",
    action: "read",
  });
  const canReadTemplate = canReadTemplates === true;

  const loadList = useCallback(async (pageNo: number, status: ReportListView) => {
    setLoading(true);
    setError(null);
    try {
      const result = await unwrap<ReportPage>(
        api.get("/reports", {
          params: { page: pageNo, page_size: PAGE_SIZE, status },
        }),
      );
      setRows(result.items ?? []);
      setTotal(result.total ?? 0);
      // A delete can empty the last page; stepping back keeps the surface from
      // showing "0 of N" over an empty table.
      if ((result.items ?? []).length === 0 && pageNo > 1) setPage(pageNo - 1);
    } catch (err) {
      setError(err);
      setRows([]);
      setTotal(0);
    } finally {
      setLoading(false);
    }
  }, []);

  // The trend is about the workspace, not the page: it is fetched once and kept
  // across page changes. It also must not fail the list — a trend the server
  // declines to compute ("暂无判定") is not a reason to hide the reports.
  const loadTrend = useCallback(async (view: TrendView) => {
    const result = await unwrap<TrendResult>(
      api.get("/report-metrics/trend", {
        params: { days: TREND_DAYS, bucket: "day", view },
      }),
    ).catch(() => null);
    setTrend(result);
  }, []);

  useEffect(() => {
    void loadTrend(trendView);
  }, [loadTrend, trendView]);

  useEffect(() => {
    let alive = true;
    void (async () => {
      const views = await unwrap<ReportTemplateView[]>(api.get("/report-templates")).catch(() => null);
      if (!alive || !Array.isArray(views)) return;
      const names: Record<string, string> = {};
      for (const view of views) {
        const key = String(view.template_key ?? "");
        const name = view.payload?.name?.zh || view.payload?.name?.en || key;
        if (key) names[key] = name;
      }
      setTemplateNames(names);
    })();
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    void loadList(page, view);
  }, [loadList, page, view]);

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return rows;
    return rows.filter(
      (row) =>
        row.title.toLowerCase().includes(needle) ||
        (row.question ?? "").toLowerCase().includes(needle) ||
        row.template_key.toLowerCase().includes(needle) ||
        (templateNames[row.template_key] ?? "").toLowerCase().includes(needle),
    );
  }, [rows, query, templateNames]);

  const points = trendPoints(trend);
  const peak = points.reduce((max, point) => Math.max(max, point.revisions), 0);

  const confirmDelete = async () => {
    if (!pendingDelete) return;
    setDeleting(true);
    try {
      await unwrap(api.delete(`/reports/${encodeURIComponent(pendingDelete.id)}`));
      setRows((current) => current.filter((row) => row.id !== pendingDelete.id));
      setTotal((current) => Math.max(0, current - 1));
      setLastDeleted(pendingDelete);
      setPendingDelete(null);
      notify(t("reportCenter.deleted"));
      void loadList(page, view);
      void loadTrend(trendView);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("reportCenter.delete_fail"), { type: "error" });
    } finally {
      setDeleting(false);
    }
  };

  const restore = async () => {
    if (!lastDeleted) return;
    setRestoring(true);
    try {
      await unwrap(api.post(`/reports/${encodeURIComponent(lastDeleted.id)}/restore`));
      notify(t("reportCenter.restored"));
      setLastDeleted(null);
      void loadList(page, view);
      void loadTrend(trendView);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("reportCenter.restore_fail"), { type: "error" });
    } finally {
      setRestoring(false);
    }
  };

  return (
    <div className="flex flex-col gap-6 p-6">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold">{t("reportCenter.title")}</h1>
          <p className="text-sm text-muted-foreground">{t("reportCenter.subtitle")}</p>
        </div>
        <Button variant="outline" size="sm" onClick={() => {
            void loadList(page, view);
            void loadTrend(trendView);
          }}
          disabled={loading}>
          {loading ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
          {t("reportCenter.refresh")}
        </Button>
      </header>

      <section
        aria-label={t("reportCenter.trend_title", { days: String(TREND_DAYS) })}
        className="rounded-lg border p-4"
      >
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h2 className="text-sm font-medium">
            {t("reportCenter.trend_title", { days: String(TREND_DAYS) })}
          </h2>
          <div className="flex items-center gap-3">
            {canManage && (
              // `view=tenant` is the governance ladder: it needs `manage:report`
              // (the service checks it too), so the toggle only appears for a
              // caller who could actually get an answer.
              <div className="flex items-center gap-1">
                {(["own", "tenant"] as const).map((option) => (
                  <Button
                    key={option}
                    variant={trendView === option ? "secondary" : "ghost"}
                    size="sm"
                    aria-pressed={trendView === option}
                    onClick={() => setTrendView(option)}
                    data-testid={`trend-view-${option}`}
                  >
                    {t(`reportCenter.trend_view_${option}`)}
                  </Button>
                ))}
              </div>
            )}
            <span className="text-xs text-muted-foreground">
              {t(`reportCenter.trend_scope_${trendView}`)}
            </span>
          </div>
        </div>
        {trend ? (
          <>
            <dl className="mt-3 grid grid-cols-2 gap-3 sm:grid-cols-4">
              <Metric label={t("reportCenter.metric_revisions")} value={String(trend.totals.revisions ?? 0)} />
              <Metric label={t("reportCenter.metric_published")} value={String(trend.totals.published ?? 0)} />
              <Metric
                label={t("reportCenter.metric_pass_rate")}
                value={formatRatio(t, trend.totals.quality_gate_pass_rate)}
              />
              <Metric
                label={t("reportCenter.metric_citation")}
                value={formatRatio(t, trend.totals.citation_coverage as number | null | undefined)}
              />
            </dl>
            {points.length > 0 ? (
              <ul className="mt-4 flex h-16 items-end gap-1" aria-hidden="true">
                {points.map((point) => (
                  <li key={point.key} className="flex flex-1 flex-col items-center gap-1">
                    <div
                      className="w-full rounded-sm bg-primary/70"
                      style={{ height: `${peak > 0 ? Math.max(4, (point.revisions / peak) * 56) : 4}px` }}
                      title={`${point.key}: ${point.revisions}`}
                    />
                  </li>
                ))}
              </ul>
            ) : (
              <p className="mt-3 text-sm text-muted-foreground">{t("reportCenter.trend_empty")}</p>
            )}
            {trend.truncated && (
              <p className="mt-2 text-xs text-amber-600">{t("reportCenter.trend_truncated")}</p>
            )}
          </>
        ) : (
          <p className="mt-3 text-sm text-muted-foreground">{t("reportCenter.trend_empty")}</p>
        )}
      </section>

      {lastDeleted && (
        <div role="status" className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-sm dark:border-amber-800 dark:bg-amber-950">
          <span>{t("reportCenter.undo_hint", { title: lastDeleted.title })}</span>
          <Button variant="outline" size="sm" onClick={() => void restore()} disabled={restoring}>
            {restoring ? <Loader2 className="size-4 animate-spin" /> : <Undo2 className="size-4" />}
            {t("reportCenter.undo_action")}
          </Button>
        </div>
      )}

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative max-w-sm flex-1">
          <Search className="pointer-events-none absolute left-2 top-2.5 size-4 text-muted-foreground" />
          <Input
            className="pl-8"
            placeholder={t("reportCenter.search_placeholder")}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>
        {/* The three views are a closed set from the contract, and the server owns
            the list query: pressing 「已归档」 re-reads with `?status=archived`
            instead of filtering the page in the browser, which would only ever
            archive-search the twenty rows that happened to be loaded. */}
        <div
          className="flex items-center gap-1"
          role="group"
          aria-label={t("reportCenter.view_label")}
        >
          {REPORT_LIST_VIEWS.map((option) => (
            <Button
              key={option}
              variant={view === option ? "secondary" : "ghost"}
              size="sm"
              aria-pressed={view === option}
              onClick={() => {
                setView(option);
                // A view change is a new query, so the page cursor goes back to 1:
                // staying on page 3 of a view with two rows shows an empty table.
                setPage(1);
              }}
              data-testid={`report-view-${option}`}
            >
              {reportListViewLabel(t, option)}
            </Button>
          ))}
        </div>
        <span className="text-sm text-muted-foreground">
          {t("reportCenter.count", { count: String(total) })}
        </span>
      </div>

      {error !== null && (
        <div role="alert" className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive">
          {error instanceof ApiError ? error.displayMessage : t("reportCenter.load_fail")}
        </div>
      )}

      {loading && rows.length === 0 ? (
        <div className="flex items-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" /> {t("reportCenter.loading")}
        </div>
      ) : visible.length === 0 ? (
        <div className="rounded-lg border border-dashed p-10 text-center text-sm text-muted-foreground">
          <FileText className="mx-auto mb-2 size-6" />
          {query ? (
            t("reportCenter.no_match")
          ) : view === "archived" ? (
            // An empty archived view is the normal state of a workspace that never
            // archived anything, so it explains the feature instead of saying "no
            // data" and leaving the button looking broken.
            <span data-testid="report-empty-archived">
              {t("reportCenter.empty_archived")}
              <span className="mt-1 block text-xs">{t("reportCenter.empty_archived_hint")}</span>
            </span>
          ) : (
            t("reportCenter.empty")
          )}
        </div>
      ) : (
        <div className="overflow-x-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-left">
              <tr>
                <th className="px-3 py-2 font-medium">{t("reportCenter.col_title")}</th>
                <th className="px-3 py-2 font-medium">{t("reportCenter.col_template")}</th>
                <th className="px-3 py-2 font-medium">{t("reportCenter.col_status")}</th>
                <th className="px-3 py-2 font-medium">{t("reportCenter.col_created")}</th>
                <th className="px-3 py-2 font-medium">{t("reportCenter.col_expires")}</th>
                <th className="px-3 py-2 text-right font-medium">{t("reportCenter.col_actions")}</th>
              </tr>
            </thead>
            <tbody>
              {visible.map((row) => {
                const openPath = reportOpenPath(row);
                return (
                  <tr key={row.id} className="border-t">
                    <td className="max-w-[28rem] px-3 py-2">
                      <button
                        type="button"
                        className="truncate text-left font-medium hover:underline"
                        title={row.title}
                        onClick={() => setDetailRow(row)}
                      >
                        {row.title}
                      </button>
                      {row.status === "archived" && (
                        // In the `all` view an archived row has to say so, or it looks
                        // like any other report that simply will not accept a rerun.
                        <span
                          className="ml-2 rounded bg-muted px-1 text-[10px] text-muted-foreground align-middle"
                          data-testid={`report-archived-badge-${row.id}`}
                        >
                          {t("reportCenter.archived_badge")}
                        </span>
                      )}
                      {row.question && (
                        <div className="truncate text-xs text-muted-foreground" title={row.question}>
                          {row.question}
                        </div>
                      )}
                    </td>
                    <td className="px-3 py-2" title={row.template_key}>
                      {templateNames[row.template_key] ?? row.template_key}
                    </td>
                    <td className="px-3 py-2">{reportDeliveryLabel(t, row)}</td>
                    <td className="px-3 py-2 text-xs text-muted-foreground">{formatTime(row.created_at)}</td>
                    <td className="px-3 py-2 text-xs text-muted-foreground">{formatTime(row.expires_at)}</td>
                    <td className="px-3 py-2">
                      <div className="flex justify-end gap-2">
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setReader({ row, revision: 0 })}
                          disabled={!row.current_revision_id}
                          title={
                            row.current_revision_id
                              ? t("reportCenter.read")
                              : t("reportCenter.read_unavailable")
                          }
                          data-testid={`report-read-${row.id}`}
                        >
                          <BookOpen className="size-4" />
                          {t("reportCenter.read")}
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={!openPath}
                          title={openPath ? t("reportCenter.open") : t("reportCenter.open_unavailable")}
                          onClick={() => openPath && navigate(openPath)}
                        >
                          {t("reportCenter.open")}
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setPendingDelete(row)}
                          aria-label={t("reportCenter.delete")}
                        >
                          <Trash2 className="size-4" />
                        </Button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {(total > PAGE_SIZE || page > 1) && (
        <div className="flex items-center justify-between text-sm">
          <Button variant="outline" size="sm" disabled={page <= 1 || loading} onClick={() => setPage(page - 1)}>
            {t("reportCenter.prev_page")}
          </Button>
          <span className="text-muted-foreground tabular-nums">
            {t("reportCenter.page_indicator", {
              page: String(page),
              pages: String(Math.max(1, Math.ceil(total / PAGE_SIZE))),
            })}
          </span>
          <Button
            variant="outline"
            size="sm"
            disabled={page * PAGE_SIZE >= total || loading}
            onClick={() => setPage(page + 1)}
          >
            {t("reportCenter.next_page")}
          </Button>
        </div>
      )}

      <ReportRetentionCard canManage={canManage} />

      {canReadTemplate && <ReportTemplatePanel canManage={canManageTemplate} />}

      <ReportDetailDrawer
        report={detailRow}
        onOpenChange={(open) => !open && setDetailRow(null)}
        onRead={(row, revision) => {
          // One reading surface: the drawer's 「阅读」 hands off to the reader and
          // closes, so two dialogs never stack.
          setDetailRow(null);
          setReader({ row, revision });
        }}
      />

      <ReportReader
        report={reader?.row ?? null}
        revision={reader?.revision ?? null}
        onOpenChange={(open) => !open && setReader(null)}
      />

      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(open) => !open && setPendingDelete(null)}
        title={t("reportCenter.delete_title")}
        message={t("reportCenter.delete_message", { title: pendingDelete?.title ?? "" })}
        confirmLabel={t("reportCenter.delete")}
        loading={deleting}
        onConfirm={() => void confirmDelete()}
      />
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="text-lg font-semibold tabular-nums">{value}</dd>
    </div>
  );
}

function formatTime(value: string | null | undefined): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}
