import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { useRecordContext, useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { api, ApiError } from "@/lib/api";

type ConnectorRow = {
  id: string;
  name?: string;
  provider?: string;
  status?: string;
  schedule_kind?: string;
  schedule_value?: number;
  last_run_at?: string;
};

type Envelope<T> = { code: number; message?: string; data: T };

/**
 * DatasetConnectorsPanel – read-only "data sources" view for a dataset
 * (doc/225 §11.3: business users only see sources inside the dataset). The
 * server-side `dataset_id` filter keeps the page scoped to this dataset.
 */
export function DatasetConnectorsPanel() {
  const t = useTranslate();
  const record = useRecordContext<{ id?: string }>();
  const [rows, setRows] = useState<ConnectorRow[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [reloadTick, setReloadTick] = useState(0);

  useEffect(() => {
    if (!record?.id) return;
    let cancelled = false;
    setError(null);
    void (async () => {
      try {
        const res = await api.get<Envelope<{ items: ConnectorRow[]; total: number }>>(
          `/connectors?dataset_id=${encodeURIComponent(record.id!)}&page=1&page_size=20`,
        );
        if (!cancelled) setRows(res.data.data?.items ?? []);
      } catch (err) {
        if (cancelled) return;
        // 403 means the caller cannot read connectors at all: keep the panel
        // invisible instead of exposing a permission error inside the dataset.
        if (err instanceof ApiError && err.status === 403) {
          setError(null);
          setRows([]);
          return;
        }
        setError(
          err instanceof ApiError
            ? err.displayMessage
            : t("connectors.dataset_load_failed"),
        );
      }
    })();
    return () => {
      cancelled = true;
    };
    // `t` is intentionally excluded: translation identity changes must not
    // trigger a refetch (the error string is already materialized in state).
  }, [record?.id, reloadTick]);

  if (error) {
    return (
      <section className="space-y-2" aria-label={t("datasets.connectors_title")}>
        <h3 className="text-sm font-semibold">{t("datasets.connectors_title")}</h3>
        <div
          role="alert"
          className="flex flex-wrap items-center justify-between gap-2 rounded border border-destructive/40 bg-destructive/5 p-3 text-sm"
        >
          <span>{error}</span>
          <Button
            size="sm"
            variant="outline"
            onClick={() => setReloadTick((tick) => tick + 1)}
          >
            {t("connectors.retry")}
          </Button>
        </div>
      </section>
    );
  }
  if (rows.length === 0) return null;

  return (
    <section className="space-y-2" aria-label={t("datasets.connectors_title")}>
      <h3 className="text-sm font-semibold">{t("datasets.connectors_title")}</h3>
      <ul className="divide-y rounded border">
        {rows.map((row) => (
          <li key={row.id} className="flex flex-wrap items-center justify-between gap-2 p-3 text-sm">
            <Link className="font-medium underline-offset-2 hover:underline" to={`/connectors/${row.id}/show`}>
              {row.name || row.id}
            </Link>
            <div className="flex items-center gap-2 text-xs text-muted-foreground">
              <span>{row.provider}</span>
              <Badge variant={row.status === "degraded" ? "destructive" : "secondary"}>
                {t(`connectors.status_${row.status ?? "idle"}`)}
              </Badge>
              <span>
                {row.last_run_at ? new Date(row.last_run_at).toLocaleString() : t("connectors.runs.empty")}
              </span>
            </div>
          </li>
        ))}
      </ul>
    </section>
  );
}
