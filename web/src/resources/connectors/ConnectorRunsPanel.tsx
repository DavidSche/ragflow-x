import { Fragment, useCallback, useEffect, useRef, useState } from "react";
import { useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api, ApiError } from "@/lib/api";

export type ConnectorRunRow = {
  id: string;
  connector_id?: string;
  trigger?: string;
  triggered_by?: string;
  status?: string;
  started_at?: string;
  finished_at?: string;
  added?: number;
  changed?: number;
  deleted?: number;
  skipped?: number;
  failed?: number;
  duration_ms?: number;
  error_summary?: string;
  trace_id?: string;
  created_at?: string;
};

type SkippedItem = { key: string; reason: string; size?: number };
type RunDetail = {
  failures?: string[];
  skipped?: SkippedItem[];
  skipped_unchanged?: number;
};

interface Envelope<T> {
  code: number;
  data: T;
  message?: string;
}

const PAGE_SIZE = 10;

function runBadgeVariant(status?: string): "default" | "secondary" | "destructive" | "outline" {
  if (status === "succeeded") return "default";
  if (status === "failed") return "destructive";
  if (status === "running" || status === "pending") return "secondary";
  return "outline";
}

const formatTime = (value?: string) => (value ? new Date(value).toLocaleString() : "—");

/**
 * ConnectorRunsPanel – paged run history (doc/225 §11.1). Reads the scoped
 * `/connectors/:id/runs` endpoint and exposes per-run counters, the bounded
 * failure summary and the trace id for correlation.
 */
export function ConnectorRunsPanel({ connectorId }: { connectorId: string }) {
  const t = useTranslate();
  const [rows, setRows] = useState<ConnectorRunRow[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [expandedID, setExpandedID] = useState<string | null>(null);
  const [detail, setDetail] = useState<RunDetail | null>(null);
  const detailRequest = useRef(0);

  const load = useCallback(
    async (nextPage: number) => {
      if (!connectorId) return;
      setLoading(true);
      setError(null);
      try {
        const res = await api.get<Envelope<{ items: ConnectorRunRow[]; total: number }>>(
          `/connectors/${connectorId}/runs?page=${nextPage}&page_size=${PAGE_SIZE}`,
        );
        if (res.data.code !== 0) {
          throw new ApiError(200, res.data.code, res.data.message || "request failed");
        }
        const items = res.data.data?.items ?? [];
        setRows((prev) => (nextPage === 1 ? items : [...prev, ...items]));
        setTotal(res.data.data?.total ?? 0);
        setPage(nextPage);
      } catch (err) {
        setError(err instanceof ApiError ? err.displayMessage : t("connectors.runs.load_failed"));
      } finally {
        setLoading(false);
      }
    },
    [connectorId, t],
  );

  useEffect(() => {
    void load(1);
  }, [load]);

  const toggleDetail = async (runID: string) => {
    detailRequest.current += 1;
    if (expandedID === runID) {
      setExpandedID(null);
      setDetail(null);
      return;
    }
    const requestID = detailRequest.current;
    setExpandedID(runID);
    setDetail(null);
    try {
      const res = await api.get<Envelope<ConnectorRunRow & { detail?: RunDetail }>>(
        `/connectors/${connectorId}/runs/${runID}`,
      );
      if (res.data.code !== 0) {
        throw new ApiError(200, res.data.code, res.data.message || "request failed");
      }
      if (requestID === detailRequest.current) {
        setDetail(res.data.data?.detail ?? {});
      }
    } catch (err) {
      if (requestID === detailRequest.current) {
        setExpandedID(null);
        setDetail(null);
        setError(err instanceof ApiError ? err.displayMessage : t("connectors.runs.load_failed"));
      }
    }
  };

  return (
    <section className="space-y-3" aria-label={t("connectors.runs.title")}>
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-semibold">{t("connectors.runs.title")}</h3>
        <Button size="sm" variant="outline" onClick={() => void load(1)} disabled={loading}>
          {t("connectors.runs.refresh")}
        </Button>
      </div>
      {error ? (
        <div role="alert" className="rounded border border-destructive/40 bg-destructive/5 p-3 text-sm">
          {error}
        </div>
      ) : null}
      {rows.length === 0 && !loading ? (
        <div className="rounded border p-4 text-sm text-muted-foreground">{t("connectors.runs.empty")}</div>
      ) : (
        <div className="overflow-x-auto rounded border">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-left">
              <tr>
                <th className="p-2">{t("connectors.runs.started")}</th>
                <th className="p-2">{t("connectors.runs.trigger")}</th>
                <th className="p-2">{t("connectors.runs.triggered_by")}</th>
                <th className="p-2">{t("connectors.status")}</th>
                <th className="p-2">+ / ~ / -</th>
                <th className="p-2">{t("connectors.runs.duration")}</th>
                <th className="p-2">{t("connectors.runs.trace")}</th>
                <th className="p-2">{t("connectors.runs.details")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((run) => (
                <Fragment key={run.id}>
                  <tr className="border-t align-top">
                    <td className="p-2 whitespace-nowrap">{formatTime(run.started_at ?? run.created_at)}</td>
                    <td className="p-2">{t(`connectors.runs.trigger_${run.trigger ?? "manual"}`)}</td>
                    <td className="p-2 break-all font-mono text-xs">{run.triggered_by || "—"}</td>
                    <td className="p-2">
                      <Badge variant={runBadgeVariant(run.status)}>
                        {t(`connectors.runs.status_${run.status ?? "pending"}`)}
                      </Badge>
                      {run.error_summary ? (
                        <p className="mt-1 max-w-xs break-words text-xs text-destructive">{run.error_summary}</p>
                      ) : null}
                    </td>
                    <td className="p-2 whitespace-nowrap">
                      {run.added ?? 0} / {run.changed ?? 0} / {run.deleted ?? 0}
                      {run.failed ? (
                        <span className="ml-1 text-destructive">({t("connectors.runs.failed", { count: run.failed })})</span>
                      ) : null}
                    </td>
                    <td className="p-2 whitespace-nowrap">
                      {run.duration_ms ? `${Math.round(run.duration_ms / 1000)}s` : "—"}
                    </td>
                    <td className="p-2">
                      {run.trace_id ? (
                        <code className="break-all text-xs">{run.trace_id}</code>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </td>
                    <td className="p-2">
                      <Button size="sm" variant="ghost" onClick={() => void toggleDetail(run.id)}>
                        {expandedID === run.id ? t("connectors.runs.hide_details") : t("connectors.runs.show_details")}
                      </Button>
                    </td>
                  </tr>
                  {expandedID === run.id ? (
                    <tr className="border-t bg-muted/30">
                      <td colSpan={8} className="p-3 text-xs">
                        {detail === null ? (
                          <span className="text-muted-foreground">{t("connectors.runs.loading_details")}</span>
                        ) : (
                          <div className="space-y-2">
                            <p>
                              {t("connectors.runs.skipped_unchanged")}: {detail.skipped_unchanged ?? 0}
                            </p>
                            {detail.skipped?.length ? (
                              <div>
                                <p className="font-medium">{t("connectors.runs.skipped_items")}</p>
                                <ul className="list-disc pl-5">
                                  {detail.skipped.map((item) => (
                                    <li key={`${item.key}:${item.reason}`} className="break-all">
                                      <code>{item.key}</code> — {item.reason}
                                      {item.size ? ` (${Math.round(item.size / (1024 * 1024))} MiB)` : ""}
                                    </li>
                                  ))}
                                </ul>
                              </div>
                            ) : null}
                          </div>
                        )}
                      </td>
                    </tr>
                  ) : null}
                </Fragment>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {rows.length < total ? (
        <Button size="sm" variant="outline" onClick={() => void load(page + 1)} disabled={loading}>
          {t("connectors.runs.load_more")}
        </Button>
      ) : null}
    </section>
  );
}
