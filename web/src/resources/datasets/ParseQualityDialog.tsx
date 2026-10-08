import { useEffect, useState } from "react";
import { useTranslate } from "ra-core";
import { Loader2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { api } from "../../lib/api";
import {
  type Envelope,
  type ParseAttemptRecord,
  type ParseQualityReportRecord,
} from "./dataset-types";

function parseMetricNames(metrics?: string) {
  if (!metrics) return [];
  try {
    const parsed = JSON.parse(metrics) as Record<string, number>;
    return Object.keys(parsed).sort();
  } catch {
    return [];
  }
}

function formatTime(value?: string) {
  if (!value) return "-";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? "-" : parsed.toLocaleString();
}

export function ParseQualityDialog({
  open,
  datasetId,
  doc,
  report,
  scopePath,
  onClose,
}: {
  open: boolean;
  datasetId?: string;
  doc?: { id: string; name: string } | null;
  report?: ParseQualityReportRecord | null;
  scopePath: (path: string) => string;
  onClose: () => void;
}) {
  const t = useTranslate();
  const [attempts, setAttempts] = useState<ParseAttemptRecord[] | null>(null);
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!open || !datasetId || !doc?.id) return;
    let active = true;
    setLoading(true);
    api
      .get<Envelope<{ items: ParseAttemptRecord[] }>>(
        scopePath(`/datasets/${datasetId}/documents/${doc.id}/parse-attempts?page=1&page_size=100`),
      )
      .then((response) => {
        if (active) setAttempts(response.data.data?.items ?? []);
      })
      .catch(() => {
        if (active) setAttempts([]);
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [datasetId, doc?.id, open, scopePath]);

  const chronologicalAttempts = [...(attempts ?? [])].reverse();

  return (
    <Dialog open={open} onOpenChange={(value) => !value && onClose()}>
      <DialogContent className="sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>
            {t("datasets.parse_quality_title")} - {doc?.name ?? ""}
          </DialogTitle>
        </DialogHeader>
        {loading ? (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            {t("datasets.chunk_loading")}
          </div>
        ) : !report ? (
          <p className="text-sm text-muted-foreground">
            {t("datasets.parse_quality_empty")}
          </p>
        ) : (
          <div className="space-y-4">
            <div className="flex flex-wrap items-center gap-2">
              <Badge
                variant={
                  report.quality_status === "FAIL"
                    ? "destructive"
                    : report.quality_status === "WARN"
                      ? "secondary"
                      : "default"
                }
              >
                {t("datasets.parse_quality_status")}: {report.quality_status}
              </Badge>
              <Badge variant="outline">
                {t("datasets.parse_quality_action")}: {report.gate_action}
              </Badge>
              <Badge variant="outline">
                {t("datasets.parse_quality_score")}: {report.effective_score.toFixed(3)}
              </Badge>
            </div>
            <div className="rounded-md border p-3">
              <div className="mb-2 text-sm font-medium">
                {t("datasets.parse_quality_metrics")}
              </div>
              <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
                {parseMetricNames(report.metrics).length === 0
                  ? t("datasets.parse_quality_metrics_empty")
                  : parseMetricNames(report.metrics).map((metric) => (
                      <Badge key={metric} variant="outline">
                        {metric}
                      </Badge>
                    ))}
              </div>
            </div>
            <div>
              <div className="mb-2 text-sm font-medium">
                {t("datasets.parse_attempt_timeline")}
              </div>
              {chronologicalAttempts.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  {t("datasets.parse_quality_empty")}
                </p>
              ) : (
                <ol className="space-y-3">
                  {chronologicalAttempts.map((attempt) => (
                    <li
                      key={attempt.id}
                      className="rounded-md border p-3 text-sm"
                    >
                      <div className="flex flex-wrap items-center gap-2">
                        <Badge
                          variant={
                            attempt.quality_status === "FAIL"
                              ? "destructive"
                              : attempt.quality_status === "WARN"
                                ? "secondary"
                                : "default"
                          }
                        >
                          #{attempt.attempt_no} {attempt.quality_status}
                        </Badge>
                        <span className="text-xs text-muted-foreground">
                          {attempt.parse_mode}
                        </span>
                      </div>
                      <div className="mt-2 text-xs text-muted-foreground">
                        {formatTime(attempt.started_at)} → {formatTime(attempt.finished_at)}
                      </div>
                      {attempt.failure_reason ? (
                        <div className="mt-2 text-xs text-destructive">
                          {attempt.failure_reason}
                        </div>
                      ) : null}
                    </li>
                  ))}
                </ol>
              )}
            </div>
          </div>
        )}
      </DialogContent>
    </Dialog>
  );
}
