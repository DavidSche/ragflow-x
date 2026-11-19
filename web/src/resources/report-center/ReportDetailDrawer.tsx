import { useCallback, useEffect, useState } from "react";
import { useCanAccess, useNotify, useTranslate } from "ra-core";
import {
  Archive,
  ArchiveRestore,
  BookOpen,
  Download,
  FileDown,
  History,
  Loader2,
  TriangleAlert,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { unwrap, ApiError, api } from "../../lib/api";
import { ReportBudgetExtensionCard } from "./ReportBudgetExtensionCard";
import { ReportScheduleCard } from "./ReportScheduleCard";
import { ReportSharePanel } from "./ReportSharePanel";
import { REPORT_EXPORT_FORMATS } from "../conversation-center/report/report-contract";
import {
  artifactDownloadPath,
  formatBytes,
  formatRatio,
  parseConsistencyIssues,
  parseGaps,
  reportCompletenessLabel,
  reportGapImpactLabel,
  reportGapReasonLabel,
  reportRevisionStatusLabel,
  parseMetrics,
  type ReportDetailRecord,
  type ReportRevisionSummaryRecord,
  type ReportRow,
} from "./report-center-api";

interface ReportDetailDrawerProps {
  report: ReportRow | null;
  onOpenChange: (open: boolean) => void;
  /**
   * Hands the chosen revision to the full reading view. The drawer keeps the
   * metadata and the files; the reader owns the document, so a report is never
   * rendered by two components that could disagree.
   */
  onRead?: (report: ReportRow, revision: number) => void;
}

/**
 * Detail drawer for one report.
 *
 * It exists because the centre's "open" action needs an originating conversation
 * (or the browser sends you back to a chat that no longer exists). A report whose
 * conversation was archived must still be readable and downloadable from the read
 * aggregate alone — `GET /reports/:id` already carries the revision, the disclosed
 * gaps, the stored metrics and the delivered files.
 *
 * Rendering rules that are deliberate:
 *  - `downloadable` comes from the server, never recomputed from status/size;
 *  - a `null` ratio prints 暂无判定, never 0%;
 *  - gaps are shown, not collapsed away: they are the honest part of a `partial`.
 */
export function ReportDetailDrawer({ report, onOpenChange, onRead }: ReportDetailDrawerProps) {
  const t = useTranslate();
  // A plan is a timed rerun, so the card is offered under the same permission the
  // rerun route carries (`execute:report`). The server checks it again.
  const { canAccess: canExecuteReports } = useCanAccess({ resource: "report", action: "execute" });
  const canExecute = canExecuteReports === true;
  // Archiving carries `delete:own` — the same authority that closes a report, since
  // "stop being a working document" is that decision, not a read (doc/297 §2).
  const { canAccess: canArchiveReports } = useCanAccess({ resource: "report", action: "delete:own" });
  const canArchiveReportsFlag = canArchiveReports === true;
  const notify = useNotify();
  const [detail, setDetail] = useState<ReportDetailRecord | null>(null);
  const [loading, setLoading] = useState(false);
  // The failure is kept as the value that was thrown, not as already-translated
  // text: `load` must not depend on `t`. A translate function that is not
  // referentially stable (a test double, a locale switch mid-flight) would
  // otherwise rebuild `load` on every render, re-run the effect, and turn the
  // drawer into a request loop — found while testing this very file.
  const [error, setError] = useState<unknown>(null);
  const [exporting, setExporting] = useState<string | null>(null);
  const [history, setHistory] = useState<ReportRevisionSummaryRecord[]>([]);
  // The revision number being *viewed*, or null for the live one. The list and the
  // body are separate reads because a history picker must not ship every body.
  const [viewingRevision, setViewingRevision] = useState<number | null>(null);
  // Archiving is confirmed, not a one-click toggle: it is reversible, but it is the
  // kind of reversible that a user must mean (a stray click on a delivered report
  // otherwise makes it vanish from the list they were reading).
  const [confirmArchive, setConfirmArchive] = useState(false);
  const [archiving, setArchiving] = useState(false);

  const load = useCallback(async (id: string) => {
    setLoading(true);
    setError(null);
    try {
      const path = `/reports/${encodeURIComponent(id)}`;
      const [record, revisions] = await Promise.all([
        unwrap<ReportDetailRecord>(api.get(path)),
        // A history that cannot be listed must not blank a report that can be read:
        // the detail is the primary surface. It degrades to "no history section"
        // rather than an error over content the user is already looking at.
        unwrap<{ revisions: ReportRevisionSummaryRecord[] }>(api.get(`${path}/revisions`)).catch(
          () => ({ revisions: [] as ReportRevisionSummaryRecord[] }),
        ),
      ]);
      setDetail(record);
      setHistory(revisions.revisions ?? []);
      setViewingRevision(null);
    } catch (err) {
      setError(err);
      setDetail(null);
      setHistory([]);
    } finally {
      setLoading(false);
    }
  }, []);

  /**
   * Switches the drawer to a historical revision.
   *
   * This is the entry point A41 was missing: the API could always serve a superseded
   * revision by number, but nothing told a user which numbers existed, so "historical
   * revisions stay readable" was true of the server and false of the product. The
   * revision's own artifacts come with the body, so a superseded delivery is
   * downloadable from here exactly as it was when it was current.
   */
  const openRevision = useCallback(async (id: string, revision: number) => {
    setLoading(true);
    setError(null);
    try {
      const record = await unwrap<ReportDetailRecord>(
        api.get(`/reports/${encodeURIComponent(id)}/revisions/${revision}`),
      );
      setDetail(record);
      setViewingRevision(revision);
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }, []);

  /**
   * Exports one format straight from the centre.
   *
   * Without this the drawer is read-only for a report that was never exported: the
   * user would have to find the originating conversation to get a PDF out of a
   * report they are already looking at. The export endpoint is synchronous and
   * idempotent, so a second click returns the same artifact instead of a duplicate.
   * The response's `downloadable` is what the button state follows — the same flag
   * the history read uses.
   */
  const exportFormat = useCallback(async (format: string) => {
    if (!detail) return;
    setExporting(format);
    setError(null);
    try {
      await api.post(
        `/reports/${encodeURIComponent(detail.report.id)}/revisions/${detail.revision.revision}/artifacts`,
        { format },
      );
      // Re-read instead of splicing the response in: the history is ordered and
      // versioned by the server, and a local append would have to re-guess both.
      // While viewing a historical revision, re-read *that* revision — jumping to
      // current would take the file away from the user who just asked for it.
      if (viewingRevision !== null) {
        await openRevision(detail.report.id, viewingRevision);
      } else {
        await load(detail.report.id);
      }
    } catch (err) {
      setError(err);
    } finally {
      setExporting(null);
    }
  }, [detail, load, openRevision, viewingRevision]);

  useEffect(() => {
    if (report) void load(report.id);
    else setDetail(null);
  }, [report, load]);

  /**
   * Archive / unarchive. The answer is the detail view, so it replaces what the
   * drawer is showing instead of triggering a re-read: a re-read would flash the
   * previous lifecycle for a frame and, if it failed, leave the drawer arguing with
   * the list it just changed.
   */
  const setArchived = async (archived: boolean) => {
    if (!detail) return;
    const id = detail.report.id;
    setArchiving(true);
    try {
      // Neither verb needs a body except for the optional archive reason, and the
      // drawer has none to add: `reason` exists for callers that do (doc/297 §2).
      const path = `/reports/${encodeURIComponent(id)}/${archived ? "archive" : "unarchive"}`;
      const next = await unwrap<ReportDetailRecord>(api.post(path));
      setDetail(next);
      notify(t(archived ? "reportCenter.archive_done" : "reportCenter.unarchive_done"));
      setConfirmArchive(false);
    } catch (err) {
      notify(
        err instanceof ApiError
          ? err.displayMessage
          : t(archived ? "reportCenter.archive_fail" : "reportCenter.unarchive_fail"),
        { type: "error" },
      );
    } finally {
      setArchiving(false);
    }
  };

  const metrics = detail ? parseMetrics(detail.revision.metrics_json) : {};
  const gaps = detail ? parseGaps(detail.revision.gap_json) : [];
  // R16's disclosure travels inside the frozen metrics, so it cannot disagree with
  // the numbers above it. `keys_checked === null` means the check never ran (a
  // revision written before P1) — printing "0 项" would claim a check that did not
  // happen.
  const consistency = detail ? parseConsistencyIssues(detail.revision.metrics_json) : null;

  return (
    <Dialog open={report !== null} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{report?.title ?? t("reportCenter.detail_title")}</DialogTitle>
        </DialogHeader>

        {loading && (
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" /> {t("reportCenter.loading")}
          </div>
        )}

        {error !== null && (
          <div role="alert" className="rounded-md border border-destructive/40 bg-destructive/5 px-3 py-2 text-sm text-destructive">
            {error instanceof ApiError
              ? error.displayMessage
              : t("reportCenter.detail_fail")}
          </div>
        )}

        {detail && !loading && (
          <div className="flex max-h-[70vh] flex-col gap-4 overflow-y-auto text-sm">
            {/* Lifecycle (doc/297 §2). It sits above the metrics because it explains
                what the reader may still expect: an archived report keeps everything
                it delivered and produces nothing new. The button is offered to the
                owner only — the server answers 44109 to everyone else — and it is
                hidden once the report is in its recovery window, where "archive"
                would be a question about a row nobody can see. */}
            <div
              className={
                detail.report.status === "archived"
                  ? "flex flex-wrap items-center justify-between gap-2 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-amber-800 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200"
                  : "flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2 text-muted-foreground"
              }
              data-testid="report-detail-lifecycle"
            >
              <span className="text-xs">
                <span className="mr-1 font-medium" data-testid="report-detail-lifecycle-status">
                  {detail.report.status === "archived"
                    ? t("reportCenter.archived_badge")
                    : t("reportCenter.view_active")}
                </span>
                {detail.report.status === "archived"
                  ? t("reportCenter.archived_hint")
                  : t("reportCenter.archive_hint")}
              </span>
              {canArchiveReportsFlag && detail.access?.is_owner !== false && (
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={archiving}
                    onClick={() => {
                      if (detail.report.status === "archived") void setArchived(false);
                      else setConfirmArchive(true);
                    }}
                    data-testid={
                      detail.report.status === "archived"
                        ? "report-detail-unarchive"
                        : "report-detail-archive"
                    }
                  >
                    {archiving ? (
                      <Loader2 className="size-3.5 animate-spin" />
                    ) : detail.report.status === "archived" ? (
                      <ArchiveRestore className="size-3.5" />
                    ) : (
                      <Archive className="size-3.5" />
                    )}
                    {t(
                      detail.report.status === "archived"
                        ? "reportCenter.unarchive"
                        : "reportCenter.archive",
                    )}
                  </Button>
                )}
            </div>

            <dl className="grid grid-cols-2 gap-3">
              <div>
                <dt className="text-xs text-muted-foreground">{t("reportCenter.detail_revision")}</dt>
                <dd className="font-medium">
                  v{detail.revision.revision} · {reportRevisionStatusLabel(t, detail.revision.status)}
                  {detail.revision.completeness
                    ? ` · ${reportCompletenessLabel(t, detail.revision.completeness)}`
                    : ""}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">{t("reportCenter.detail_quality")}</dt>
                <dd className="font-medium tabular-nums">
                  {formatRatio(t, metrics.quality_gate_pass_rate)}
                </dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">{t("reportCenter.metric_citation")}</dt>
                <dd className="font-medium tabular-nums">{formatRatio(t, metrics.citation_coverage)}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">{t("reportCenter.detail_structure")}</dt>
                <dd className="font-medium tabular-nums">{formatRatio(t, metrics.structure_completeness)}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">{t("reportCenter.detail_claim_support")}</dt>
                <dd className="font-medium tabular-nums" data-testid="report-detail-claim-support">
                  {formatRatio(t, metrics.claim_support_rate)}
                </dd>
              </div>
            </dl>

            {consistency && consistency.keys_checked !== null && (
              <section data-testid="report-detail-consistency">
                <h3 className="mb-1 text-xs font-semibold text-muted-foreground">
                  {t("reportCenter.detail_consistency_checked", { count: consistency.keys_checked })}
                </h3>
                {consistency.issues.length === 0 ? null : (
                  <ul className="space-y-1">
                    {consistency.issues.map((issue, index) => (
                      <li
                        key={`${issue.label}:${index}`}
                        className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/5 px-2 py-1 text-xs text-destructive"
                        data-testid={`report-detail-consistency-${index}`}
                      >
                        <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
                        <span>
                          {t("reportCenter.detail_consistency_issue", {
                            label: issue.label,
                            ords: issue.ords
                              .map((ord) => t("reportCenter.detail_section_ord", { ord }))
                              .join(t("reportCenter.detail_list_separator")),
                            values: issue.values.join(t("reportCenter.detail_list_separator")),
                          })}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            )}

            {!detail.evidence_verifiable && (
              <div className="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 px-3 py-2 text-amber-800 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-200">
                <TriangleAlert className="mt-0.5 size-4 shrink-0" />
                <span>
                  {t("reportCenter.detail_evidence_expired")}
                  {detail.evidence_expired_at ? ` (${formatTime(detail.evidence_expired_at)})` : ""}
                </span>
              </div>
            )}

            {viewingRevision !== null && (
              <div
                className="flex flex-wrap items-center justify-between gap-2 rounded-md border bg-muted/40 px-3 py-2"
                data-testid="report-detail-history-banner"
              >
                <span>
                  {t("reportCenter.detail_viewing_history", { revision: viewingRevision })}
                </span>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => void load(detail.report.id)}
                  data-testid="report-detail-back-to-current"
                >
                  {t("reportCenter.detail_back_to_current")}
                </Button>
              </div>
            )}

            {history.length > 0 && (
              <section data-testid="report-detail-history">
                <h3 className="mb-1 flex items-center gap-1 text-xs font-semibold text-muted-foreground">
                  <History className="size-3.5" />
                  {t("reportCenter.detail_history")}
                </h3>
                <ul className="space-y-1">
                  {history.map((entry) => (
                    <li
                      key={entry.id}
                      className="flex flex-wrap items-center justify-between gap-2 rounded border px-2 py-1"
                      data-testid={`report-detail-history-${entry.revision}`}
                    >
                      <span>
                        <span className="font-medium">v{entry.revision}</span>
                        <span className="ml-2 text-muted-foreground">
                          {reportRevisionStatusLabel(t, entry.status)}
                          {entry.completeness
                            ? ` · ${reportCompletenessLabel(t, entry.completeness)}`
                            : ""}
                          {entry.published_at ? ` · ${formatTime(entry.published_at)}` : ""}
                        </span>
                        {entry.is_current && (
                          <span className="ml-2 rounded bg-emerald-100 px-1 text-[10px] text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300">
                            {t("reportCenter.detail_history_current")}
                          </span>
                        )}
                      </span>
                      {!entry.is_current && (
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => void openRevision(detail.report.id, entry.revision)}
                          data-testid={`report-detail-open-revision-${entry.revision}`}
                        >
                          {t("reportCenter.detail_history_open")}
                        </Button>
                      )}
                    </li>
                  ))}
                </ul>
              </section>
            )}

            {gaps.length > 0 && (
              <section>
                <h3 className="mb-1 text-xs font-semibold text-muted-foreground">
                  {t("reportCenter.detail_gaps")}
                </h3>
                <ul className="space-y-1">
                  {gaps.map((gap) => (
                    <li key={`${gap.ord}-${gap.section_key}`} className="rounded border px-2 py-1">
                      <span className="font-medium">
                        {gap.ord ? `${gap.ord}. ` : ""}
                        {gap.title || gap.section_key}
                      </span>
                      {/* 原因和影响是两套闭集，界面必须各说各的：原来直接把
                          `failed — section_missing` 印在中文抽屉里（2026-11-19
                          实测）。未知值仍原样输出，见 enumLabel。 */}
                      <span className="ml-2 text-muted-foreground">
                        {t("reportCenter.detail_gap_reason", {
                          reason: reportGapReasonLabel(t, gap.reason),
                        })}
                        {gap.impact
                          ? ` · ${t("reportCenter.detail_gap_impact", {
                              impact: reportGapImpactLabel(t, gap.impact),
                            })}`
                          : ""}
                        {gap.error_code ? ` · ${gap.error_code}` : ""}
                      </span>
                    </li>
                  ))}
                </ul>
              </section>
            )}

            <section>
              <div className="mb-1 flex flex-wrap items-center justify-between gap-2">
                <h3 className="text-xs font-semibold text-muted-foreground">
                  {t("reportCenter.detail_artifacts")}
                </h3>
                {detail.revision.status === "published" ? (
                  <div className="flex gap-1">
                    {/* One source for the buttons: the contract's report delivery
                        subset. The previous inline `["docx","markdown","pdf"]` was a
                        second copy of the server's gate, so adding a format meant
                        editing two places and the day one was missed the UI would
                        quietly offer less than the server delivers. */}
                    {REPORT_EXPORT_FORMATS.map((format) => (
                      <Button
                        key={format}
                        variant="secondary"
                        size="sm"
                        disabled={exporting !== null}
                        onClick={() => void exportFormat(format)}
                        data-testid={`report-detail-export-${format}`}
                      >
                        {exporting === format ? (
                          <Loader2 className="size-3.5 animate-spin" />
                        ) : (
                          <FileDown className="size-3.5" />
                        )}
                        {t(`reportCenter.label_export_format.${format}`, {
                          _: format,
                        })}
                      </Button>
                    ))}
                  </div>
                ) : (
                  // The export gate is published-only (doc/288 §7.10): offering the
                  // buttons on a superseded/failed revision would mint a request the
                  // server answers 409/44106.
                  <span className="text-xs text-muted-foreground">
                    {t("reportCenter.detail_export_only_published")}
                  </span>
                )}
              </div>
              {onRead && (
                <div className="mb-2">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => onRead(detail.report, detail.revision.revision)}
                    data-testid="report-detail-read"
                  >
                    <BookOpen className="size-3.5" />
                    {t("reportCenter.read")}
                  </Button>
                </div>
              )}
              {/* R20: the recurring plan lives with the report's own metadata, so a
                  reader who is about to rely on tomorrow's copy can see when it is
                  due and what the last run did. */}
              {report && (
                <ReportScheduleCard
                  reportId={report.id}
                  canExecute={canExecute === true}
                  formatTime={formatTime}
                />
              )}
              {/* Sharing (doc/296 §2.1) and the budget extension (§2.2) live next to
                  the plan because all three are about *this* delivery: who may read
                  it, when it rebuilds, and whether its run may keep spending. */}
              {/* Both are rendered unconditionally: a panel that disappears when the
                  payload lacks its field would hide the capability instead of saying
                  it is unavailable, and "nothing shown" reads as "nothing to do". */}
              <ReportSharePanel
                reportId={detail.report.id}
                access={detail.access}
                onSaved={(view) => setDetail((current) => (current ? { ...current, access: view } : current))}
              />
              <ReportBudgetExtensionCard
                reportId={detail.report.id}
                budget={detail.budget?.budget_tokens ?? null}
                budgetPolicy={detail.budget?.budget_policy}
                canExecute={canExecute === true}
              />

              {detail.artifacts.length === 0 ? (
                <p className="text-muted-foreground">{t("reportCenter.detail_no_artifacts")}</p>
              ) : (
                <ul className="space-y-1">
                  {detail.artifacts.map(({ artifact, downloadable }) => (
                    <li
                      key={artifact.id}
                      className="flex flex-wrap items-center justify-between gap-2 rounded border px-2 py-1"
                    >
                      <span>
                        <span className="font-medium uppercase">{artifact.format}</span>
                        <span className="ml-2 text-muted-foreground">
                          #{artifact.artifact_version} · {formatBytes(artifact.byte_size)} ·{" "}
                          {formatTime(artifact.created_at)}
                        </span>
                      </span>
                      {downloadable ? (
                        <Button variant="outline" size="sm" asChild>
                          <a
                            href={artifactDownloadPath(
                              detail.report.id,
                              detail.revision.revision,
                              artifact.id,
                            )}
                            download
                          >
                            <Download className="size-3.5" />
                            {t("reportCenter.detail_download")}
                          </a>
                        </Button>
                      ) : (
                        <span className="text-xs text-muted-foreground">
                          {t("reportCenter.detail_not_downloadable")}
                        </span>
                      )}
                    </li>
                  ))}
                </ul>
              )}
            </section>
          </div>
        )}
        <ConfirmDialog
          open={confirmArchive}
          onOpenChange={setConfirmArchive}
          title={t("reportCenter.archive_confirm_title")}
          message={t("reportCenter.archive_confirm_body", {
            title: detail?.report.title ?? "",
          })}
          confirmLabel={t("reportCenter.archive")}
          loading={archiving}
          onConfirm={() => void setArchived(true)}
        />
      </DialogContent>
    </Dialog>
  );
}

function formatTime(value: string | null | undefined): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}
