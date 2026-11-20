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
 *
 * A fourth rule arrived with the assurance gate (doc/301 §7.2 / T09): the server refuses
 * an export whose revision still has unresolved conflicts (44121) or has not been
 * reviewed at all (44122). Those two refusals are **not** interchangeable errors:
 *
 *   - 44121 is an acknowledgement, so the panel opens a dialog that states the
 *     eligibility verdict, the open-conflict count, and what the ack will record, then
 *     re-exports with `acknowledge_assurance=true`. Accepting on the user's behalf
 *     (a silent retry) would write an ack into the audit log that nobody gave.
 *   - 44122 is a wait, so the panel says "review has not finished" and offers no
 *     button. Offering an ack there would be offering a lie: there is nothing to
 *     acknowledge yet.
 */
import { useState } from "react";
import { useNotify, useTranslate } from "ra-core";
import { Download, FileDown, FileText, Loader2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { ApiError } from "../../../lib/api";
import { reportArtifactStatusLabel } from "../../report-center/report-center-api";
import { REPORT_EXPORT_FORMATS, type ReportExportFormat } from "./report-contract";
import type { ReportAdapter, ReportExportSuccess } from "../adapters";
import type { ExportArtifactRecord, ReportArtifactEntry } from "./report-run";
import { describeEligibility, type ReportReview } from "./report-assurance";

export interface ArtifactPanelProps {
  adapter: ReportAdapter;
  reportId: string;
  revision: number;
  /** Delivery history of this revision from the report read, newest first. */
  history?: ReportArtifactEntry[];
  /** Lets the workspace re-read the history after an export (one source of truth). */
  onExported?: () => void;
  /**
   * Reads the review projection when the server refuses with 44121, so the dialog can
   * state *what* is unresolved. Absent ⇒ the refusal degrades to a message: a dialog
   * that cannot name the conflicts would be a dialog that asks for consent to nothing.
   */
  loadReview?: (reportId: string, revision: number) => Promise<ReportReview>;
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
  loadReview,
}: ArtifactPanelProps) {
  const t = useTranslate();
  const notify = useNotify();
  const [exporting, setExporting] = useState<ReportExportFormat | null>(null);
  const [created, setCreated] = useState<ReportExportSuccess[]>([]);
  const [pendingAck, setPendingAck] = useState<{ format: ReportExportFormat; review: ReportReview } | null>(null);
  const [ackBusy, setAckBusy] = useState(false);
  const entries = mergeArtifactEntries(created, history);

  const runExport = async (format: ReportExportFormat, acknowledgeAssurance = false) => {
    if (exporting) return;
    setExporting(format);
    try {
      // Called with three arguments unless the user just acknowledged: the extra
      // `{acknowledgeAssurance:true}` is a *user act* and must not be passed as
      // `undefined` on every ordinary export, where a later default flip would silently
      // reinterpret the call.
      const result = acknowledgeAssurance
        ? await adapter.createExport(reportId, revision, format, { acknowledgeAssurance: true })
        : await adapter.createExport(reportId, revision, format);
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
      const code = error instanceof ApiError ? error.code : 0;
      if (code === 44122) {
        // Not reviewed yet: nothing to acknowledge, nothing to retry.
        notify(t("conversationCenter.report_export_assurance_not_ready"), { type: "error" });
      } else if (code === 44121 && loadReview) {
        try {
          const review = await loadReview(reportId, revision);
          setPendingAck({ format, review });
        } catch {
          // The review read failed too; the generic message is still true.
          notify(t("conversationCenter.report_export_error"), { type: "error" });
        }
      } else {
        notify(t("conversationCenter.report_export_error"), { type: "error" });
      }
    } finally {
      setExporting(null);
    }
  };

  const confirmAck = async () => {
    if (!pendingAck || ackBusy) return;
    setAckBusy(true);
    const { format } = pendingAck;
    setPendingAck(null);
    try {
      await runExport(format, true);
    } finally {
      setAckBusy(false);
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

      <AlertDialog
        open={pendingAck !== null}
        onOpenChange={(open) => {
          if (!open) setPendingAck(null);
        }}
      >
        <AlertDialogContent data-testid="ack-dialog">
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("conversationCenter.report_export_ack_title")}
            </AlertDialogTitle>
            <AlertDialogDescription asChild>
              <div className="space-y-2 text-sm">
                <p data-testid="ack-eligibility">
                  {t("conversationCenter.report_export_ack_eligibility", {
                    reason: pendingAck
                      ? describeEligibility(
                          t,
                          pendingAck.review.release_eligibility,
                          pendingAck.review.release_eligibility_basis,
                        )
                      : "",
                  })}
                </p>
                <p data-testid="ack-counts">
                  {t("conversationCenter.report_export_ack_counts", {
                    open: pendingAck?.review.counters.open_findings ?? 0,
                    high: pendingAck?.review.counters.high_findings ?? 0,
                  })}
                </p>
                <p className="text-muted-foreground">
                  {t("conversationCenter.report_export_ack_body")}
                </p>
              </div>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={ackBusy} data-testid="ack-cancel">
              {t("conversationCenter.report_export_ack_cancel")}
            </AlertDialogCancel>
            <AlertDialogAction
              disabled={ackBusy}
              data-testid="ack-confirm"
              className="inline-flex h-9 items-center justify-center rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground hover:bg-primary/90"
              onClick={() => void confirmAck()}
            >
              {t("conversationCenter.report_export_ack_confirm")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  );
}
