/**
 * Report artifact panel (doc/288 §7.7 + §12, T12/T14).
 *
 * P0-A exports are synchronous: `POST .../artifacts` renders the frozen
 * revision inline and returns an already-`ready` (or `failed`) artifact. This
 * panel therefore only needs two verbs — export and download — plus the
 * idempotency discipline that a second click re-exports the same content to the
 * same artifact (server-deduped by canonical hash). Downloads go through a
 * plain same-origin link so the browser handles cookies and content-disposition.
 *
 * Two rules shape the list:
 *
 *   1. the history comes from the **read** (`history`), not from this component's
 *      memory, so a page refresh still finds the file delivered last week (A11);
 *   2. `downloadable` is rendered from the server's flag. Deriving it here from
 *      `artifact_status === "ready"` would ignore the 30-day clock and would be a
 *      second source of truth for the one thing the download endpoint enforces.
 *
 *   3. the five buttons come from `REPORT_EXPORT_FORMATS`, the same closed set the
 *      report-center drawer maps over: two entries to one delivery must not offer
 *      two different format lists (2026-11-19: this panel still had three).
 */
import { useState } from "react";
import { useNotify, useTranslate } from "ra-core";
import { Download, FileDown, FileText, Loader2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { reportArtifactStatusLabel } from "../../report-center/report-center-api";
import { REPORT_EXPORT_FORMATS, type ReportExportFormat } from "./report-contract";
import type { ReportAdapter, ReportExportSuccess } from "../adapters";
import type { ExportArtifactRecord, ReportArtifactEntry } from "./report-run";

export interface ArtifactPanelProps {
  adapter: ReportAdapter;
  reportId: string;
  revision: number;
  /** Delivery history of this revision from the report read, newest first. */
  history?: ReportArtifactEntry[];
  /** Lets the workspace re-read the history after an export (one source of truth). */
  onExported?: () => void;
}

/** Human size for the delivery line; a failed render has 0 bytes and prints "—". */
export function formatArtifactSize(bytes: number): string {
  if (bytes <= 0) return "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

export function artifactStatusLabel(
  t: (key: string, options?: Record<string, unknown>) => string,
  artifact: ExportArtifactRecord,
): string {
  // One vocabulary for artifact status across the workspace and the reader.
  return reportArtifactStatusLabel(t, artifact.artifact_status);
}

/**
 * Merges this session's fresh exports over the server history, newest first.
 *
 * A just-exported row may already be in `history` (if the workspace re-read), so
 * the merge is by artifact id: the session copy wins only because it is the same
 * row, and duplicates would double-render one file.
 */
export function mergeArtifactEntries(
  created: ReportExportSuccess[],
  history: ReportArtifactEntry[],
): ReportArtifactEntry[] {
  const seen = new Set(created.map((item) => item.export_artifact.id));
  return [
    ...created.map((item) => ({ artifact: item.export_artifact, downloadable: item.downloadable })),
    ...history.filter((item) => !seen.has(item.artifact.id)),
  ];
}

export function ArtifactPanel({
  adapter,
  reportId,
  revision,
  history = [],
  onExported,
}: ArtifactPanelProps) {
  const t = useTranslate();
  const notify = useNotify();
  const [exporting, setExporting] = useState<ReportExportFormat | null>(null);
  const [created, setCreated] = useState<ReportExportSuccess[]>([]);
  const entries = mergeArtifactEntries(created, history);

  const runExport = async (format: ReportExportFormat) => {
    if (exporting) return;
    setExporting(format);
    try {
      const result = await adapter.createExport(reportId, revision, format);
      setCreated((previous) => [
        result,
        ...previous.filter((item) => item.export_artifact.id !== result.export_artifact.id),
      ]);
      onExported?.();
      if (result.export_artifact.artifact_status === "failed") {
        // A failed render is recorded, not retried blindly (§7.7 / A40): the
        // artifact consumes a version and the message tells the operator what to
        // look at. PDF has one predictable cause, so it gets its own line.
        notify(
          t(
            format === "pdf"
              ? "conversationCenter.report_export_pdf_unavailable"
              : "conversationCenter.report_export_failed",
          ),
          { type: "error" },
        );
      }
    } catch (error) {
      notify(t("conversationCenter.report_export_error"), { type: "error" });
    } finally {
      setExporting(null);
    }
  };

  return (
    <section className="space-y-3" data-testid="artifact-panel">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="flex items-center gap-2 text-sm font-medium">
          <FileDown className="size-4 text-muted-foreground" />
          {t("conversationCenter.report_artifacts_title")}
        </h3>
        <div className="flex flex-wrap gap-2">
          {REPORT_EXPORT_FORMATS.map((format) => (
            <Button
              key={format}
              size="sm"
              variant={format === "docx" ? "default" : "outline"}
              onClick={() => void runExport(format)}
              disabled={exporting !== null}
              data-testid={`export-${format}`}
            >
              {exporting === format ? (
                <Loader2 className="size-4 animate-spin" />
              ) : format === "docx" || format === "xlsx" ? (
                <FileDown className="size-4" />
              ) : (
                <FileText className="size-4" />
              )}
              {t(`reportCenter.label_export_format.${format}`, { _: format })}
            </Button>
          ))}
        </div>
      </div>

      {entries.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          {t("conversationCenter.report_artifacts_empty")}
        </p>
      ) : (
        <ul className="space-y-2" data-testid="artifact-list">
          {entries.map(({ artifact, downloadable }) => (
            <li
              key={artifact.id}
              className="flex items-center gap-3 rounded-md border bg-card px-3 py-2"
              data-testid={`artifact-${artifact.id}`}
            >
              <FileDown className="size-4 shrink-0 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm font-medium">
                  {artifact.filename}
                </p>
                <p className="text-xs text-muted-foreground">
                  {artifact.format.toUpperCase()} ·{" "}
                  {formatArtifactSize(artifact.byte_size)} ·{" "}
                  {artifactStatusLabel(t, artifact)}
                </p>
              </div>
              <Badge variant="secondary" className="hidden sm:inline-flex">
                {artifact.sha256.slice(0, 12)}
              </Badge>
              {downloadable ? (
                <a
                  href={adapter.artifactDownloadUrl(
                    reportId,
                    revision,
                    artifact.id,
                  )}
                  download
                  data-testid={`artifact-download-${artifact.id}`}
                  className="inline-flex h-8 items-center gap-1 rounded-md px-2 text-xs font-medium text-primary hover:bg-accent"
                >
                  <Download className="size-3.5" />
                  {t("conversationCenter.report_artifact_download")}
                </a>
              ) : (
                <span className="text-xs text-muted-foreground">
                  {t("conversationCenter.report_artifact_pending")}
                </span>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
