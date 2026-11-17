/**
 * 报告工作台 (doc/288, T8/T9/T10/T11/T12) — `kind:"report"` run surface.
 *
 * A report is not a chat turn: it has a template, a dataset binding, an editable
 * outline, then a long-running job whose progress is followed through the §8.3
 * event stream. This component hosts that flow end to end:
 *
 *   1. 模板选型   — read the content-template catalog (`report-templates`)
 *   2. 数据集绑定 — pick datasets (capability re-check happens server-side)
 *   3. 提纲确认   — OutlineEditor; `required` sections cannot be removed
 *   4. 生成进度   — snapshot + SSE follow, `continue` after network resets
 *   5. 产物       — SectionDrawer disclosures + ArtifactPanel exports
 *
 * Access is gated with the same `useCanAccess` the center uses for its tabs:
 * `resource: "report", action: "execute"`. The permission model (RBAC) is P0-B;
 * this gate keeps the P0-A screen honest without claiming a policy it lacks.
 */
import { useCallback, useEffect, useMemo, useState } from "react";
import { useCanAccess, useGetList, useNotify, useTranslate } from "ra-core";
import {
  ArrowLeft,
  Database,
  FileText,
  Loader2,
  RefreshCw,
  Sparkles,
  XCircle,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import type { ReportAdapter } from "../adapters";
import { ArtifactPanel } from "./ArtifactPanel";
import { OutlineEditor } from "./OutlineEditor";
import { SectionDrawer } from "./SectionDrawer";
import {
  isReportJobTerminal,
  openReportEventPoller,
  rememberReportJob,
  rememberedReportJob,
  type CreateReportResult,
  type ReportJobSnapshot,
  type ReportTemplateView,
} from "./report-run";

type WorkspacePhase =
  | "loading"
  | "no_access"
  | "templates"
  | "configure"
  | "confirm_outline"
  | "running"
  | "published"
  | "failed"
  | "cancelled";

export interface ReportWorkspaceProps {
  adapter: ReportAdapter;
  identity?: Record<string, unknown> | null;
  onClose: () => void;
}

interface DatasetOption {
  id: string;
  name: string;
}

export function ReportWorkspace({
  adapter,
  identity,
  onClose,
}: ReportWorkspaceProps) {
  const t = useTranslate();
  const notify = useNotify();
  const access = useCanAccess({ resource: "report", action: "execute" });
  const canAccess = access?.canAccess === true;

  const [templates, setTemplates] = useState<ReportTemplateView[]>([]);
  const [selectedTemplate, setSelectedTemplate] = useState<ReportTemplateView | null>(null);
  const [selectedDatasetIds, setSelectedDatasetIds] = useState<string[]>([]);
  const [titleEdit, setTitleEdit] = useState("");
  const [creating, setCreating] = useState(false);
  const [report, setReport] = useState<CreateReportResult | null>(null);
  const [snapshot, setSnapshot] = useState<ReportJobSnapshot | null>(null);
  const [outlineConfirmed, setOutlineConfirmed] = useState(false);
  const [confirmingOutline, setConfirmingOutline] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [phase, setPhase] = useState<WorkspacePhase>("loading");

  const datasetQuery = useGetList<DatasetOption>("datasets", {
    pagination: { page: 1, perPage: 200 },
    sort: { field: "updated_at", order: "DESC" },
  });
  const datasets: DatasetOption[] = useMemo(
    () => (datasetQuery.data ?? []).map((d) => ({ id: String(d.id), name: String(d.name ?? d.id) })),
    [datasetQuery.data],
  );

  const jobId = report?.job.id ?? rememberedReportJob(report?.report.id ?? "") ?? snapshot?.job_id;

  const loadTemplates = useCallback(async () => {
    try {
      const list = await adapter.listTemplates();
      setTemplates(list);
      if (list.length === 0) {
        setPhase("templates");
        notify(t("conversationCenter.report_templates_empty"), { type: "warning" });
      }
    } catch (error) {
      notify(t("conversationCenter.report_load_error"), { type: "error" });
      setSelectedTemplate(null);
      setTemplates([]);
    }
  }, [adapter, notify, t]);

  useEffect(() => {
    if (canAccess === false) {
      setPhase("no_access");
      return;
    }
    if (canAccess === true) {
      void loadTemplates().then(() => setPhase((previous) => (previous === "loading" ? "templates" : previous)));
    }
    // Phase transitions from templates onwards are user-driven.
  }, [canAccess, loadTemplates]);

  useEffect(() => {
    // Selecting a template opens the configure step; going back returns to
    // the catalog. The initial mount (phase "loading") is left alone until
    // templates resolve through loadTemplates.
    setSelectedDatasetIds([]);
    setPhase((previous) =>
      previous === "loading" ? "loading" : selectedTemplate ? "configure" : "templates",
    );
  }, [selectedTemplate]);

  const selectTemplate = (template: ReportTemplateView) => {
    setSelectedTemplate(template);
  };

  const backToTemplates = () => setSelectedTemplate(null);

  const toggleDataset = (id: string) => {
    setSelectedDatasetIds((previous) =>
      previous.includes(id) ? previous.filter((item) => item !== id) : [...previous, id],
    );
  };

  const create = async () => {
    if (!selectedTemplate || creating) return;
    const title = titleEdit.trim();
    if (!title) {
      notify(t("conversationCenter.report_title_required"), { type: "warning" });
      return;
    }
    setCreating(true);
    try {
      const result = await adapter.createReport({
        title,
        question: title,
        template_key: selectedTemplate.template_key,
        datasets: selectedDatasetIds.map((id) => ({ dataset_id: id, role: "primary" })),
      });
      rememberReportJob(result.report.id, result.job.id);
      setReport(result);
      setPhase("confirm_outline");
    } catch (error) {
      notify(t("conversationCenter.report_create_error"), { type: "error" });
    } finally {
      setCreating(false);
    }
  };

  const confirmOutline = async (sections: Array<{ section_key: string; title: string }>) => {
    if (!report || confirmingOutline) return;
    setConfirmingOutline(true);
    try {
      await adapter.confirmOutline(report.report.id, report.revision.revision, sections);
      setOutlineConfirmed(true);
      setPhase("running");
    } catch (error) {
      notify(t("conversationCenter.report_outline_confirm_error"), { type: "error" });
    } finally {
      setConfirmingOutline(false);
    }
  };

  const cancel = async () => {
    if (!report || cancelling) return;
    setCancelling(true);
    try {
      await adapter.cancelReport(report.report.id);
      notify(t("conversationCenter.report_cancel_requested"), { type: "info" });
    } catch (error) {
      notify(t("conversationCenter.report_cancel_error"), { type: "error" });
    } finally {
      setCancelling(false);
    }
  };

  const reloadSnapshot = useCallback(
    async (options: { signal?: AbortSignal } = {}) => {
      const id = report?.report.id;
      const jid = jobId;
      if (!id || !jid) return null;
      const snap = await adapter.loadSnapshot(id, jid, options).catch(() => null);
      if (snap) setSnapshot(snap);
      return snap;
    },
    [adapter, report, jobId],
  );

  const refresh = async () => {
    setRefreshing(true);
    try {
      await reloadSnapshot();
    } finally {
      setRefreshing(false);
    }
  };

  // Follow the job: initial snapshot, then the §8.3 stream, continuing across
  // non-terminal closes and cursor resets (44115 → re-read the snapshot). This
  // is the "刷新仍在" guarantee – progress never restarts from memory.
  useEffect(() => {
    if (phase !== "running" && phase !== "confirm_outline") return;
    if (!report) return;
    const activeJobId = report.job?.id ?? rememberedReportJob(report.report.id);
    if (!activeJobId) return;
    const controller = new AbortController();
    let disposed = false;
    (async () => {
      const snap = await adapter.loadSnapshot(report.report.id, activeJobId, {
        signal: controller.signal,
      }).catch(() => null);
      if (disposed) return;
      if (snap) {
        setSnapshot(snap);
        if (isReportJobTerminal(snap.status)) return;
      }
      const poller = openReportEventPoller(activeJobId, {
        signal: controller.signal,
        after: snap?.event_seq ?? 0,
        onReset: async () => {
          const fresh = await adapter.loadSnapshot(report.report.id, activeJobId).catch(() => null);
          return fresh ? fresh.event_seq : 0;
        },
      });
      try {
        for await (const frame of poller.frames()) {
          if (disposed) return;
          const fresh = await adapter.loadSnapshot(report.report.id, activeJobId, {
            signal: controller.signal,
          }).catch(() => null);
          if (fresh) {
            setSnapshot(fresh);
            if (isReportJobTerminal(fresh.status)) {
              poller.abort();
              return;
            }
          }
        }
      } catch (error) {
        if (!controller.signal.aborted && !disposed) {
          notify(t("conversationCenter.report_follow_error"), { type: "warning" });
        }
      } finally {
        poller.abort();
      }
    })();
    return () => {
      disposed = true;
      controller.abort();
    };
  }, [adapter, report, phase, notify, t]);

  const status = snapshot?.status ?? (report ? (phase === "confirm_outline" ? "awaiting_outline" : undefined) : undefined);

  // Terminal snapshot states drive the phase; also guard a snapshot that is
  // terminal before the poller callback had a chance to run.
  useEffect(() => {
    if (!snapshot) return;
    if (!isReportJobTerminal(snapshot.status)) return;
    setPhase(snapshot.status === "failed" ? "failed" : snapshot.status === "cancelled" ? "cancelled" : "published");
  }, [snapshot]);

  const templateHeader = selectedTemplate ?? templates[0] ?? null;

  return (
    <div className="flex h-full min-h-0 flex-col gap-4 overflow-hidden" data-testid="report-workspace">
      <header className="flex flex-wrap items-center gap-3 border-b pb-3">
        <Button variant="ghost" size="icon" onClick={onClose} aria-label="back">
          <ArrowLeft className="size-4" />
        </Button>
        <div className="flex min-w-0 flex-1 items-center gap-2">
          <Sparkles className="size-4 shrink-0 text-muted-foreground" />
          <Input
            value={titleEdit}
            onChange={(event) => setTitleEdit(event.target.value)}
            placeholder={t("conversationCenter.report_title_placeholder")}
            disabled={!!report}
            className="h-8 max-w-xs font-medium disabled:opacity-100"
            data-testid="report-title-input"
          />
        </div>
        {status && (
          <Badge variant="secondary" data-testid="report-status">
            {t(`conversationCenter.report_job_${status}`, { _: status })}
          </Badge>
        )}
        <div className="flex items-center gap-2">
          {phase === "running" && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="outline" size="sm" onClick={cancel} disabled={cancelling} data-testid="report-cancel">
                  {cancelling ? <Loader2 className="size-4 animate-spin" /> : <XCircle className="size-4" />}
                  {t("conversationCenter.report_cancel")}
                </Button>
              </TooltipTrigger>
              <TooltipContent>{t("conversationCenter.report_cancel_hint")}</TooltipContent>
            </Tooltip>
          )}
          {report && (
            <Button variant="ghost" size="sm" onClick={refresh} disabled={refreshing} aria-label="refresh">
              <RefreshCw className={`size-4 ${refreshing ? "animate-spin" : ""}`} />
            </Button>
          )}
        </div>
      </header>

      <main className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-3xl flex-col gap-6 p-1">
          {phase === "loading" && (
            <div className="flex items-center justify-center py-20 text-muted-foreground">
              <Loader2 className="mr-2 size-4 animate-spin" />
              {t("conversationCenter.loading")}
            </div>
          )}

          {phase === "no_access" && (
            <div className="py-20 text-center text-muted-foreground">
              {t("conversationCenter.report_no_access")}
            </div>
          )}

          {phase === "templates" && (
            <section data-testid="template-catalog">
              <h2 className="mb-3 flex items-center gap-2 text-base font-medium">
                <FileText className="size-4 text-muted-foreground" />
                {t("conversationCenter.report_templates_title")}
              </h2>
              {templates.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t("conversationCenter.report_templates_empty")}</p>
              ) : (
                <ul className="grid gap-3 sm:grid-cols-2" data-testid="template-list">
                  {templates.map((template) => (
                    <li key={template.template_id}>
                      <button
                        type="button"
                        onClick={() => selectTemplate(template)}
                        className="flex h-full w-full flex-col gap-1 rounded-lg border bg-card p-4 text-left hover:border-primary"
                        data-testid={`template-${template.template_key}`}
                      >
                        <span className="text-sm font-medium">
                          {template.payload?.name?.zh ?? template.payload?.name?.en ?? template.template_key}
                        </span>
                        <span className="text-xs text-muted-foreground">
                          {t("conversationCenter.report_template_sections", {
                            count: template.payload?.sections?.length ?? 0,
                          })}{" "}
                          ·{" "}
                          {t("conversationCenter.report_template_evidence", {
                            count: (template.payload?.sections ?? []).filter((s) => s.requires_evidence).length,
                          })}
                        </span>
                        {template.payload?.outline_confirmation === "required" && (
                          <Badge variant="outline" className="mt-1 w-fit text-[10px]">
                            {t("conversationCenter.report_outline_confirm")}
                          </Badge>
                        )}
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          )}

          {phase === "configure" && templateHeader && (
            <section className="space-y-5" data-testid="configure-step">
              <div className="flex items-center gap-2">
                <span className="text-sm font-medium">
                  {templateHeader.payload?.name?.zh ?? templateHeader.payload?.name?.en ?? templateHeader.template_key}
                </span>
                <Badge variant="outline">{templateHeader.template_key}</Badge>
              </div>
              <div>
                <h3 className="mb-2 flex items-center gap-2 text-sm font-medium">
                  <Database className="size-4 text-muted-foreground" />
                  {t("conversationCenter.report_datasets_title")}
                </h3>
                {datasets.length === 0 ? (
                  <p className="text-sm text-muted-foreground">{t("conversationCenter.report_datasets_empty")}</p>
                ) : (
                  <ul className="flex flex-wrap gap-2" data-testid="dataset-options">
                    {datasets.map((dataset) => {
                      const checked = selectedDatasetIds.includes(dataset.id);
                      return (
                        <li key={dataset.id}>
                          <button
                            type="button"
                            onClick={() => toggleDataset(dataset.id)}
                            aria-pressed={checked}
                            className={`rounded-full border px-3 py-1 text-xs ${checked ? "border-primary bg-primary text-primary-foreground" : "bg-card hover:border-primary"}`}
                            data-testid={`dataset-${dataset.id}`}
                          >
                            {dataset.name}
                          </button>
                        </li>
                      );
                    })}
                  </ul>
                )}
              </div>
              <div className="flex items-center justify-end gap-2 border-t pt-3">
                <Button variant="ghost" size="sm" onClick={backToTemplates}>
                  {t("conversationCenter.report_back_templates")}
                </Button>
                <Button onClick={() => void create()} disabled={creating || selectedDatasetIds.length === 0} data-testid="report-create">
                  {creating ? <Loader2 className="size-4 animate-spin" /> : <Sparkles className="size-4" />}
                  {t("conversationCenter.report_create")}
                </Button>
              </div>
            </section>
          )}

          {phase === "confirm_outline" && report && templateHeader && (
            <OutlineEditor
              reportId={report.report.id}
              revision={report.revision.revision}
              mode={templateHeader.payload.outline_confirmation === "auto" ? "auto" : "required"}
              sections={templateHeader.payload.sections ?? []}
              confirmed={outlineConfirmed}
              busy={confirmingOutline}
              onConfirm={confirmOutline}
            />
          )}

          {(phase === "running" || phase === "published" || phase === "failed" || phase === "cancelled") && (
            <>
              {snapshot && (
                <section className="space-y-4" data-testid="progress-step">
                  {phase === "running" && snapshot.progress_total > 0 && (
                    <div role="progressbar" aria-valuemin={0} aria-valuemax={snapshot.progress_total} aria-valuenow={snapshot.progress_done}>
                      <div className="mb-1 flex justify-between text-xs text-muted-foreground">
                        <span>{t(`conversationCenter.report_stage_${snapshot.stage}`, { _: snapshot.stage })}</span>
                        <span>
                          {snapshot.progress_done}/{snapshot.progress_total}
                        </span>
                      </div>
                      <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
                        <div
                          className="h-full rounded-full bg-primary transition-all"
                          style={{ width: `${(snapshot.progress_done / snapshot.progress_total) * 100}%` }}
                        />
                      </div>
                    </div>
                  )}
                  <SectionDrawer sections={snapshot.sections ?? []} evidenceBySection={undefined} />
                  {phase === "running" && (
                    <p className="text-xs text-muted-foreground">{t("conversationCenter.report_running_hint")}</p>
                  )}
                </section>
              )}
              {phase === "published" && report && (
                <>
                  {snapshot && snapshot.status === "partial" && (
                    <div className="rounded-md border border-amber-300/60 bg-amber-50 px-3 py-2 text-xs dark:border-amber-500/40 dark:bg-amber-950/40">
                      {t("conversationCenter.report_partial_banner", {
                        gaps: snapshot.gaps?.length ?? 0,
                      })}
                    </div>
                  )}
                  <ArtifactPanel
                    adapter={adapter}
                    reportId={report.report.id}
                    revision={report.revision.revision}
                    initialExports={[]}
                  />
                </>
              )}
              {phase === "failed" && (
                <div className="space-y-3">
                  <p className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm">
                    {t("conversationCenter.report_failed_banner")}
                  </p>
                  {snapshot && snapshot.gaps && snapshot.gaps.length > 0 && (
                    <SectionDrawer sections={snapshot.sections ?? []} evidenceBySection={undefined} />
                  )}
                </div>
              )}
              {phase === "cancelled" && (
                <p className="text-sm text-muted-foreground">{t("conversationCenter.report_cancelled_banner")}</p>
              )}
            </>
          )}
        </div>
      </main>

      <footer className="border-t pt-2 text-xs text-muted-foreground">
        {identity
          ? t("conversationCenter.report_workspace_footer", {
              workspace: String(identity.tenant_id ?? identity.user_id ?? ""),
            })
          : ""}
      </footer>
    </div>
  );
}
