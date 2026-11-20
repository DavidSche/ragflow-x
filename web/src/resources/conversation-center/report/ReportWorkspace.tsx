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
import { useCanAccess, useNotify, useTranslate } from "ra-core";
import {
  ArrowLeft,
  Database,
  FileText,
  Loader2,
  Radar,
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
import { ApiError } from "../../../lib/api";
import {
  reportJobStatusLabel,
  reportStageLabel,
} from "../../report-center/report-center-api";
import type { ReportAdapter } from "../adapters";
import { ArtifactPanel } from "./ArtifactPanel";
import { AssuranceReviewPanel } from "./AssuranceReviewPanel";
import { OutlineEditor } from "./OutlineEditor";
import { SectionDrawer } from "./SectionDrawer";
import {
  type ReportRevisionSummary,
  isReportJobTerminal,
  openReportEventPoller,
  rememberReportJob,
  rememberedReportJob,
  type CreateReportResult,
  type ReportArtifactEntry,
  type ReportEvidencePreview,
  type ReportFollowUpSuggestion,
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
  /**
   * Where this run was started from. The report row's 「打开」 navigates back with
   * `origin_chat_id`/`origin_session_id`; a run that does not record them leaves the
   * centre's jump-back button permanently disabled (live 2026-11-18: every report
   * said "该报告没有关联对话记录，无法从这里打开").
   */
  originChatId?: string;
  originSessionId?: string;
  /**
   * A14: what a follow-up chip click should do. Only supplied when this workspace was
   * opened *inside* a conversation that can take the turn — a chip that cannot be
   * sent is not rendered at all, rather than offering a button that dead-ends.
   */
  onFollowUp?: (request: ReportFollowUpRequest) => void;
}

/** One follow-up chip click, in the three keys the chat turn needs. */
export interface ReportFollowUpRequest {
  text: string;
  reportId: string;
  revision: number;
  sectionKey?: string;
}

interface DatasetOption {
  id: string;
  name: string;
}

/**
 * The workspace label the footer prints. `tenant_name` comes from `/me`; when a
 * deployment predates that field (or the name is blank) fall back to the id, so
 * the line degrades to the previous behaviour instead of printing "undefined".
 */
export function workspaceName(identity: Record<string, unknown> | null | undefined): string {
  if (!identity) return "";
  for (const key of ["tenant_name", "tenant_id", "user_id"]) {
    const value = identity[key];
    if (typeof value === "string" && value.trim() !== "") return value;
  }
  return "";
}

export function ReportWorkspace({
  adapter,
  identity,
  onClose,
  originChatId,
  originSessionId,
  onFollowUp,
}: ReportWorkspaceProps) {
  const t = useTranslate();
  const notify = useNotify();
  const access = useCanAccess({ resource: "report", action: "execute" });
  const canAccess = access?.canAccess === true;

  const [templates, setTemplates] = useState<ReportTemplateView[]>([]);
  const [selectedTemplate, setSelectedTemplate] = useState<ReportTemplateView | null>(null);
  const [selectedDatasetIds, setSelectedDatasetIds] = useState<string[]>([]);
  // R18's depth, defaulted to the cheaper standard run: deep research costs an extra
  // planning call per section, so it is opted into rather than assumed.
  const [researchDepth, setResearchDepth] = useState<"standard" | "deep">("standard");
  const [titleEdit, setTitleEdit] = useState("");
  const [creating, setCreating] = useState(false);
  const [report, setReport] = useState<CreateReportResult | null>(null);
  const [snapshot, setSnapshot] = useState<ReportJobSnapshot | null>(null);

  /**
   * The snapshot only ever moves forward.
   *
   * The follow loop and every manual refresh read the snapshot over HTTP while
   * the stream keeps pushing frames, so two reads can land out of order: a read
   * issued before the job finished used to overwrite the terminal snapshot that
   * arrived meanwhile, and the badge flipped back from 已生成 to 汇总成文 under a
   * finished report (live, 2026-11-18). Two rules keep that from happening —
   * a terminal snapshot is never replaced by a non-terminal one, and a read with
   * an older `event_seq` is dropped.
   */
  const applySnapshot = useCallback((next: ReportJobSnapshot | null) => {
    if (!next) return;
    setSnapshot((current) => {
      if (!current) return next;
      if (isReportJobTerminal(current.status) && !isReportJobTerminal(next.status)) return current;
      if (next.event_seq < current.event_seq) return current;
      return next;
    });
  }, []);
  // Delivery history of the published revision (A11). Loaded from the read, never
  // held only in the panel's memory: that is what makes an export survive F5.
  // The revisions a `resolved` decision may point at, and the id→number map the
  // panel prints. Loaded once per report: a revision is immutable, so the only thing
  // that can change the list is a *new* revision, which remounts this workspace.
  const [revisions, setRevisions] = useState<ReportRevisionSummary[]>([]);
  const laterRevisions = useMemo(
    () =>
      revisions
        .filter((row) => report !== null && row.revision > report.revision.revision)
        .sort((a, b) => a.revision - b.revision)
        .map((row) => ({ id: row.id, revision: row.revision })),
    [report, revisions],
  );
  const revisionNumbers = useMemo(() => {
    const map: Record<string, number> = {};
    for (const row of revisions) map[row.id] = row.revision;
    return map;
  }, [revisions]);
  const [artifacts, setArtifacts] = useState<ReportArtifactEntry[]>([]);
  const [outlineConfirmed, setOutlineConfirmed] = useState(false);
  const [confirmingOutline, setConfirmingOutline] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [phase, setPhase] = useState<WorkspacePhase>("loading");

  const [datasets, setDatasets] = useState<DatasetOption[]>([]);

  const jobId = report?.job.id ?? rememberedReportJob(report?.report.id ?? "") ?? snapshot?.job_id;

  // A14's chips. Derived from the stored revision, so they are re-read rather than
  // remembered: a body that was rewritten since the last look must not offer the
  // sentence about the old one.
  const [followUps, setFollowUps] = useState<ReportFollowUpSuggestion[]>([]);
  const followUpReportId = report?.report.id ?? "";
  const followUpRevision = report?.revision.revision ?? 0;
  useEffect(() => {
    if (!onFollowUp || !followUpReportId || followUpRevision <= 0) {
      setFollowUps([]);
      return;
    }
    let cancelled = false;
    const controller = new AbortController();
    void adapter
      .loadFollowUps(followUpReportId, { revision: followUpRevision, signal: controller.signal })
      .then((view) => {
        if (!cancelled) setFollowUps(view.items ?? []);
      })
      .catch(() => {
        // A chip list is a convenience: if it cannot be read the document is still
        // readable, so this stays silent instead of taking over the workspace.
        if (!cancelled) setFollowUps([]);
      });
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [adapter, followUpReportId, followUpRevision, onFollowUp]);

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
    if (canAccess !== true) return;
    // The bindable datasets come from the adapter, scoped to this workspace: the
    // governance catalogue lists every tenant, and a dataset from another workspace
    // is rejected by the run itself (404 dataset not found, found live 2026-11-18).
    let cancelled = false;
    void adapter.listReportDatasets().then((rows) => {
      if (!cancelled) setDatasets(rows);
    });
    return () => {
      cancelled = true;
    };
  }, [adapter, canAccess]);

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

  // R18 (doc/295 §2.2): the depth is frozen on the revision at creation and a
  // regeneration inherits it, so it is chosen here — once — and not offered again.
  // `deep` widens this run's own retrieval through a planner; it never relaxes what a
  // sentence may claim, which is why it is not presented as a quality setting.
  const toggleResearchDepth = () => {
    setResearchDepth((current) => (current === "deep" ? "standard" : "deep"));
  };

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
        research_depth: researchDepth,
        datasets: selectedDatasetIds.map((id) => ({ dataset_id: id, role: "primary" })),
        // Omitted when the workspace was opened outside a conversation: an empty
        // string is not an origin, and the server would store one.
        ...(originChatId ? { origin_chat_id: originChatId } : {}),
        ...(originSessionId ? { origin_session_id: originSessionId } : {}),
      });
      rememberReportJob(result.report.id, result.job.id);
      setReport(result);
      setPhase("confirm_outline");
    } catch (error) {
      // A bare "启动失败" made a server answer ("dataset not found") invisible: the
      // user could not tell a bad dataset from an outage. Show the reason when the
      // server gave one.
      const reason = error instanceof ApiError ? error.displayMessage : "";
      notify(
        reason
          ? `${t("conversationCenter.report_create_error")} ${reason}`
          : t("conversationCenter.report_create_error"),
        { type: "error" },
      );
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
      applySnapshot(snap);
      return snap;
    },
    [adapter, report, jobId, applySnapshot],
  );

  const reloadArtifacts = useCallback(
    async (options: { signal?: AbortSignal } = {}) => {
      const id = report?.report.id;
      const revisionNo = report?.revision.revision;
      if (!id || !revisionNo) return;
      const detail = await adapter.loadDetail(id, options).catch(() => null);
      if (detail && detail.revision?.revision === revisionNo) {
        setArtifacts(detail.artifacts ?? []);
      }
    },
    [adapter, report],
  );

  const reloadRevisions = useCallback(
    async (options: { signal?: AbortSignal } = {}) => {
      const id = report?.report.id;
      if (!id) return;
      // A failure here is not fatal: the review panel degrades to "no later revision
      // to point at", which disables one button rather than breaking the surface.
      //
      // The whole call is wrapped, not just the promise: an injected adapter may not
      // implement `listRevisions` at all (it is an optional capability), and
      // `undefined(...)` throws *synchronously* — before `.catch` could exist. That
      // rejected the enclosing async effect with no handler and surfaced only as an
      // unhandled rejection, so the test suite reported "22 passed, 6 errors".
      try {
        const rows = await adapter.listRevisions?.(id, options);
        if (rows) setRevisions(rows);
      } catch {
        /* keep the previous list; the button stays disabled */
      }
    },
    [adapter, report],
  );

  // The published surface owns the artifact list: entering it (including after a
  // refresh) re-reads the history, and every workspace-level refresh re-reads it
  // too, so a file delivered in another tab appears without a reload.
  useEffect(() => {
    if (phase !== "published") return;
    const controller = new AbortController();
    void reloadArtifacts({ signal: controller.signal });
    void reloadRevisions({ signal: controller.signal });
    return () => controller.abort();
  }, [phase, reloadArtifacts, reloadRevisions]);

  const refresh = async () => {
    setRefreshing(true);
    try {
      // Both halves of the published surface, so a file delivered in another tab and
      // a section written by a retry appear together instead of one at a time.
      await Promise.all([reloadSnapshot(), reloadArtifacts(), reloadRevisions()]);
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
        applySnapshot(snap);
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
            applySnapshot(fresh);
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
  }, [adapter, report, phase, notify, t, applySnapshot]);

  const status = snapshot?.status ?? (report ? (phase === "confirm_outline" ? "awaiting_outline" : undefined) : undefined);

  // Terminal snapshot states drive the phase; also guard a snapshot that is
  // terminal before the poller callback had a chance to run.
  useEffect(() => {
    if (!snapshot) return;
    if (!isReportJobTerminal(snapshot.status)) return;
    setPhase(snapshot.status === "failed" ? "failed" : snapshot.status === "cancelled" ? "cancelled" : "published");
  }, [snapshot]);

  // The outline's evidence preview comes from the job snapshot's frozen section
  // rows (v175), not from the template: the template declares what a section
  // *requires*, the preview records what the knowledge base actually held when
  // this outline was produced. Keyed by `section_key` so a reordered outline still
  // pairs the right line with the right section.
  const outlinePreviews = useMemo(() => {
    const map: Record<string, ReportEvidencePreview | null> = {};
    for (const section of snapshot?.sections ?? []) {
      map[section.section_key] = section.evidence_preview ?? null;
    }
    return map;
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
          // The badge speaks about the revision the server reports. It used to
          // build `conversationCenter.report_job_<status>`, a key family that does
          // not exist, so the badge printed `awaiting_outline` verbatim.
          <Badge variant="secondary" data-testid="report-status">
            {reportJobStatusLabel(t, status)}
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
              <div>
                <h3 className="mb-2 flex items-center gap-2 text-sm font-medium">
                  <Radar className="size-4 text-muted-foreground" />
                  {t("conversationCenter.report_research_depth_title")}
                </h3>
                <button
                  type="button"
                  onClick={toggleResearchDepth}
                  aria-pressed={researchDepth === "deep"}
                  className={`rounded-full border px-3 py-1 text-xs ${researchDepth === "deep" ? "border-primary bg-primary text-primary-foreground" : "bg-card hover:border-primary"}`}
                  data-testid="research-depth-toggle"
                >
                  {t(`conversationCenter.report_research_depth_${researchDepth}`)}
                </button>
                <p className="mt-2 text-xs text-muted-foreground">
                  {t("conversationCenter.report_research_depth_hint")}
                </p>
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
              previews={outlinePreviews}
              confirmed={outlineConfirmed}
              busy={confirmingOutline}
              onConfirm={confirmOutline}
            />
          )}

          {(phase === "running" || phase === "published" || phase === "failed" || phase === "cancelled") && (
            <>
              {onFollowUp && report && followUps.length > 0 && (
                <section className="space-y-2" data-testid="report-follow-ups">
                  <h3 className="flex items-center gap-2 text-sm font-medium">
                    <Sparkles className="size-4 text-muted-foreground" />
                    {t("conversationCenter.report_followup_title")}
                  </h3>
                  <p className="text-xs text-muted-foreground">
                    {t("conversationCenter.report_followup_hint")}
                  </p>
                  <div className="flex flex-wrap gap-2">
                    {followUps.map((item, index) => (
                      <button
                        key={`${item.kind}-${item.section_key}-${index}`}
                        type="button"
                        onClick={() =>
                          onFollowUp({
                            text: item.text,
                            reportId: report.report.id,
                            revision: report.revision.revision,
                            ...(item.section_key ? { sectionKey: item.section_key } : {}),
                          })
                        }
                        className="rounded-full border px-3 py-1 text-left text-xs hover:border-primary"
                        data-testid={`report-followup-${item.kind}`}
                      >
                        {item.text}
                      </button>
                    ))}
                  </div>
                </section>
              )}
              {snapshot && (
                <section className="space-y-4" data-testid="progress-step">
                  {phase === "running" && snapshot.progress_total > 0 && (
                    <div role="progressbar" aria-valuemin={0} aria-valuemax={snapshot.progress_total} aria-valuenow={snapshot.progress_done}>
                      <div className="mb-1 flex justify-between text-xs text-muted-foreground">
                        <span>{reportStageLabel(t, snapshot.stage)}</span>
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
                  <SectionDrawer sections={snapshot.sections ?? []} />
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
                    history={artifacts}
                    onExported={() => void reloadArtifacts()}
                    // The ack dialog needs the verdict it is asking the user to accept;
                    // without this read the 44121 refusal could only be a dead end.
                    loadReview={(reportId, revisionNo) => adapter.loadReview(reportId, revisionNo)}
                  />
                  {/* 证据复核 (doc/301 T08/T09/T12). It sits *above* nothing and below
                      the delivery panel on purpose: what the report says and whether it
                      can be trusted are read together, and the review cannot be skipped
                      past into a download without the ack prompt having been shown. */}
                  <AssuranceReviewPanel
                    load={(options) =>
                      adapter.loadReview(report.report.id, report.revision.revision, options)
                    }
                    trigger={() =>
                      adapter.triggerAssurance(report.report.id, report.revision.revision)
                    }
                    poll={(runId) =>
                      adapter.loadAssuranceRun(report.report.id, report.revision.revision, runId)
                    }
                    decide={(findingId, body) =>
                      adapter.updateFinding(report.report.id, report.revision.revision, findingId, body)
                    }
                    laterRevisions={laterRevisions}
                    revisionIdToNumber={revisionNumbers}
                  />
                </>
              )}
              {phase === "failed" && (
                <div className="space-y-3">
                  <p className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm">
                    {t("conversationCenter.report_failed_banner")}
                  </p>
                  {snapshot && snapshot.gaps && snapshot.gaps.length > 0 && (
                    <SectionDrawer sections={snapshot.sections ?? []} />
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
              // The footer names the workspace. Printing `tenant_id` put a
              // 32-char hex string in front of the reader (live, 2026-11-18);
              // `/me` now carries `tenant_name` for exactly this line.
              workspace: workspaceName(identity),
            })
          : ""}
      </footer>
    </div>
  );
}
