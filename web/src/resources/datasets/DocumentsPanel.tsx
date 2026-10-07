/**
 * DocumentsPanel – document list, upload, parse, chunk viewer for a dataset.
 */
import { useCallback, useEffect, useState } from "react";
import type { ChangeEvent } from "react";
import {
  useCanAccess,
  useGetIdentity,
  useNotify,
  useRecordContext,
  useTranslate,
} from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import { ResizableDialogContent } from "@/components/ui/resizable-dialog";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { OriginalDocumentViewer } from "@/components/documents/original-document-viewer";
import { isTextPreview } from "@/components/documents/document-preview";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Loader2,
  Info,
  ListChecks,
  Play,
  Power,
  RefreshCw,
  ShieldCheck,
  Square,
  Tags,
  Trash2,
  Upload,
} from "lucide-react";
import { api } from "../../lib/api";
import {
  type DocumentRecord,
  type ChunkRecord,
  type Envelope,
  type ParseQualityReportRecord,
  RUN_TEXT,
  errText,
  formatBytes,
  formatTime,
  documentExt,
  loadDocuments,
} from "./dataset-types";
import { ParseQualityDialog } from "./ParseQualityDialog";

export const DocumentsPanel = () => {
  const record = useRecordContext<{ id?: string; tenant_id?: string }>();
  const datasetId = record?.id;
  const { data: identity } = useGetIdentity();
  const identityTenantID = (identity as { tenant_id?: string } | undefined)?.tenant_id;
  const recordTenantID = record?.tenant_id;
  const scopeQuery =
    identityTenantID && recordTenantID && identityTenantID !== recordTenantID
      ? `?scope=specific&tenant_id=${encodeURIComponent(recordTenantID)}`
      : "";
  const scopePath = useCallback((path: string) => {
    return scopeQuery
      ? `${path}${path.includes("?") ? `&${scopeQuery.slice(1)}` : scopeQuery}`
      : path;
  }, [scopeQuery]);
  const notify = useNotify();
  const [docs, setDocs] = useState<DocumentRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [metaDoc, setMetaDoc] = useState<{
    id: string;
    name: string;
    text: string;
  } | null>(null);
  const [chunksDoc, setChunksDoc] = useState<{
    id: string;
    name: string;
  } | null>(null);
  const [chunks, setChunks] = useState<ChunkRecord[] | null>(null);
  const [chunksLoading, setChunksLoading] = useState(false);
  const [chunksPage, setChunksPage] = useState(1);
  const [chunksTotal, setChunksTotal] = useState(0);
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [previewCt, setPreviewCt] = useState("");
  const [previewText, setPreviewText] = useState("");
  const [previewLoading, setPreviewLoading] = useState(false);
  const [chunkSel, setChunkSel] = useState<Set<string>>(new Set());
  const [qualityReports, setQualityReports] = useState<Record<string, ParseQualityReportRecord>>({});
  const [qualityDoc, setQualityDoc] = useState<{ id: string; name: string } | null>(null);
  const t = useTranslate();
  const { canAccess: canAppendDocument } = useCanAccess({
    resource: "document",
    action: "append",
  });
  const { canAccess: canExecuteDocument } = useCanAccess({
    resource: "document",
    action: "execute",
  });
  const { canAccess: canReadDocument } = useCanAccess({
    resource: "document",
    action: "read",
  });
  const { canAccess: canDeleteOwnDocument } = useCanAccess({
    resource: "document",
    action: "delete:own",
  });

  const load = async () => {
    if (!datasetId) return;
    setLoading(true);
    try {
      setDocs(await loadDocuments(datasetId, scopeQuery));
    } catch (err) {
      notify(errText(err, t("datasets.load_doc_error")), { type: "error" });
    } finally {
      setLoading(false);
    }
  };

  const loadQualityReports = useCallback(async () => {
    if (!datasetId || !canReadDocument) return;
    const reports: Record<string, ParseQualityReportRecord> = {};
    try {
      const first = await api.get<Envelope<{ items: ParseQualityReportRecord[]; total: number }>>(
        scopePath(`/datasets/${datasetId}/parse-quality-reports?page=1&page_size=200`),
      );
      const total = first.data.data?.total ?? 0;
      for (const report of first.data.data?.items ?? []) {
        reports[report.document_id] = report;
      }
      for (let page = 2; page * 200 < total; page += 1) {
        const response = await api.get<Envelope<{ items: ParseQualityReportRecord[] }>>(
          scopePath(`/datasets/${datasetId}/parse-quality-reports?page=${page}&page_size=200`),
        );
        for (const report of response.data.data?.items ?? []) {
          reports[report.document_id] = report;
        }
      }
      setQualityReports(reports);
    } catch {
      setQualityReports({});
    }
  }, [canReadDocument, datasetId, scopePath]);

  useEffect(() => {
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [datasetId]);

  useEffect(() => {
    void loadQualityReports();
  }, [datasetId, loadQualityReports]);

  const run = async (fn: () => Promise<unknown>, success: string) => {
    setBusy(true);
    try {
      await fn();
      notify(success, { type: "success" });
      await load();
      await loadQualityReports();
    } catch (err) {
      notify(errText(err, t("datasets.chunk_op_error")), { type: "error" });
    } finally {
      setBusy(false);
    }
  };

  const handleUpload = (file: File) => {
    if (!datasetId) return;
    run(async () => {
      const form = new FormData();
      form.append("file", file);
      await api.post(scopePath(`/datasets/${datasetId}/documents`), form);
    }, t("datasets.doc_upload_ok", { name: file.name }));
  };

  const handleBatch = (act: "parse" | "stop" | "delete") => {
    if (!datasetId || selected.size === 0) return;
    run(async () => {
      if (act === "parse") {
        await api.post(scopePath(`/datasets/${datasetId}/parse`), {
          document_ids: Array.from(selected),
        });
      } else if (act === "stop") {
        await api.post(scopePath(`/datasets/${datasetId}/documents/stop`), {
          document_ids: Array.from(selected),
        });
      } else {
        await api.delete(scopePath(`/datasets/${datasetId}/documents`), {
          data: { document_ids: Array.from(selected) },
        });
      }
      setSelected(new Set());
    }, act === "parse" ? t("datasets.doc_parsed") : act === "stop" ? t("datasets.doc_stopped") : t("datasets.doc_deleted_selected"));
  };

  const handleOne = (
    act: "parse" | "stop" | "delete" | "toggle",
    doc: DocumentRecord,
  ) => {
    if (!datasetId) return;
    if (act === "toggle") {
      run(
        async () => {
          await api.post(scopePath(`/datasets/${datasetId}/documents/status`), {
            document_ids: [doc.id],
            enabled: !doc.enabled,
          });
        },
        doc.enabled ? t("datasets.doc_disabled_ok") : t("datasets.doc_enabled_ok"),
      );
      return;
    }
    run(async () => {
      if (act === "parse") {
        await api.post(scopePath(`/datasets/${datasetId}/parse`), {
          document_ids: [doc.id],
        });
      } else if (act === "stop") {
        await api.post(scopePath(`/datasets/${datasetId}/documents/stop`), {
          document_ids: [doc.id],
        });
      } else {
        await api.delete(scopePath(`/datasets/${datasetId}/documents`), {
          data: { document_ids: [doc.id] },
        });
      }
    }, act === "parse" ? t("datasets.doc_parsed") : act === "stop" ? t("datasets.doc_stopped") : t("datasets.doc_deleted"));
  };

  const saveMetadata = async () => {
    if (!datasetId || !metaDoc) return;
    let parsed: Record<string, unknown>;
    try {
      parsed = metaDoc.text.trim() ? JSON.parse(metaDoc.text) : {};
    } catch {
      notify(t("datasets.meta_invalid"), { type: "error" });
      return;
    }
    setBusy(true);
    try {
      await api.put(
        scopePath(`/datasets/${datasetId}/documents/${metaDoc.id}/metadata`),
        { metadata: parsed },
      );
      notify(t("datasets.meta_saved"), { type: "success" });
      setMetaDoc(null);
      await load();
    } catch (err) {
      notify(errText(err, t("datasets.meta_save_fail")), { type: "error" });
    } finally {
      setBusy(false);
    }
  };

  const loadChunksPage = async (docId: string, page: number) => {
    if (!datasetId) return;
    setChunksLoading(true);
    try {
      const res = await api.get<
        Envelope<{
          items: ChunkRecord[];
          total: number;
          page: number;
          page_size: number;
        }>
      >(
        scopePath(`/datasets/${datasetId}/documents/${docId}/chunks?page=${page}&page_size=100`),
      );
      setChunks(res.data.data?.items ?? []);
      setChunksTotal(res.data.data?.total ?? 0);
      setChunksPage(res.data.data?.page ?? 1);
    } catch (err) {
      notify(errText(err, t("datasets.load_chunk_error")), { type: "error" });
      if (page === 1) setChunksDoc(null);
    } finally {
      setChunksLoading(false);
    }
  };

  const openChunks = (doc: DocumentRecord) => {
    const loadPreview = async (doc: DocumentRecord) => {
      if (!datasetId) return;
      setPreviewLoading(true);
      try {
        const res = await api.get(
          scopePath(`/datasets/${datasetId}/documents/${doc.id}/preview`),
          { responseType: "blob" },
        );
        const blob = res.data as Blob;
        const url = URL.createObjectURL(blob);
        if (previewUrl) URL.revokeObjectURL(previewUrl);
        const ct = (res.headers["content-type"] as string) || "";
        setPreviewUrl(url);
        setPreviewCt(ct);
        setPreviewText(isTextPreview(ct, doc.name) ? await blob.text() : "");
      } catch {
        notify(t("datasets.load_preview_error"), { type: "error" });
      } finally {
        setPreviewLoading(false);
      }
    };
    setChunksDoc({ id: doc.id, name: doc.name });
    setChunks(null);
    setChunksTotal(0);
    setChunksPage(1);
    void loadChunksPage(doc.id, 1);
    void loadPreview(doc);
  };

  const nextChunks = () => {
    if (!chunksDoc || chunksPage * 100 >= chunksTotal) return;
    void loadChunksPage(chunksDoc.id, chunksPage + 1);
  };

  const prevChunks = () => {
    if (!chunksDoc || chunksPage <= 1) return;
    void loadChunksPage(chunksDoc.id, chunksPage - 1);
  };

  const closeChunks = () => {
    setChunksDoc(null);
    setChunks(null);
    if (previewUrl) {
      URL.revokeObjectURL(previewUrl);
      setPreviewUrl(null);
    }
    setPreviewText("");
    setPreviewCt("");
  };

  const toggleChunkSel = (id: string, checked: boolean) => {
    setChunkSel((prev) => {
      const next = new Set(prev);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });
  };

  const toggleChunkAll = (checked: boolean) => {
    const pageIds = (chunks ?? [])
      .map((c) => c.id)
      .filter((x): x is string => !!x);
    setChunkSel(checked ? new Set(pageIds) : new Set());
  };

  const chunkOp = async (act: "enable" | "disable" | "delete") => {
    if (!chunksDoc) return;
    const ids = Array.from(chunkSel);
    if (ids.length === 0) return;
    setBusy(true);
    try {
      if (act === "delete") {
        await api.delete(
          scopePath(`/datasets/${datasetId}/documents/${chunksDoc.id}/chunks`),
          { data: { chunk_ids: ids } },
        );
      } else {
        await api.patch(
          scopePath(`/datasets/${datasetId}/documents/${chunksDoc.id}/chunks`),
          { chunk_ids: ids, enabled: act === "enable" },
        );
      }
      setChunkSel(new Set());
      notify(
        act === "delete"
          ? t("datasets.chunk_deleted")
          : act === "enable"
            ? t("datasets.chunk_enabled")
            : t("datasets.chunk_disabled"),
        { type: "success" },
      );
      await loadChunksPage(chunksDoc.id, chunksPage);
    } catch (err) {
      notify(
        errText(
          err,
          act === "delete" ? t("datasets.chunk_op_delete") : t("datasets.chunk_op_update"),
        ),
        { type: "error" },
      );
    } finally {
      setBusy(false);
    }
  };

  const toggle = (id: string, checked: boolean) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (checked) next.add(id);
      else next.delete(id);
      return next;
    });
  };

  const status = (raw: string) => {
    const text = RUN_TEXT[raw] ?? (raw || t("datasets.doc_unknown"));
    const variant =
      raw === "3" || raw === "DONE"
        ? "default"
        : raw === "1" || raw === "RUNNING"
          ? "secondary"
          : raw === "4" || raw === "FAIL"
            ? "destructive"
            : "outline";
    return { text, variant };
  };

  return (
    <>
      <div className="mt-6 space-y-4" aria-label={t("datasets.aria_doc_panel")}>
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center">
          {canAppendDocument ? (
            <label className="inline-flex cursor-pointer items-center gap-2 text-sm">
              <Upload className="size-4" />
              {t("datasets.upload")}
              <Input
                type="file"
                accept=".txt,.md,.pdf,.docx"
                className="hidden"
                disabled={busy}
                onChange={(e: ChangeEvent<HTMLInputElement>) => {
                  const file = e.target.files?.[0];
                  if (file) handleUpload(file);
                  e.target.value = "";
                }}
              />
            </label>
          ) : null}
          {canExecuteDocument ? (
            <>
              <Button
                variant="outline"
                disabled={selected.size === 0 || busy}
                onClick={() => handleBatch("parse")}
              >
                <Play /> {t("datasets.parse_selected")}
              </Button>
              <Button
                variant="outline"
                disabled={selected.size === 0 || busy}
                onClick={() => handleBatch("stop")}
              >
                <Square /> {t("datasets.stop_selected")}
              </Button>
              <Button
                variant="destructive"
                disabled={selected.size === 0 || busy}
                onClick={() => handleBatch("delete")}
              >
                <Trash2 /> {t("datasets.delete_selected")}
              </Button>
            </>
          ) : null}
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              void load();
              void loadQualityReports();
            }}
          >
            {loading ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <RefreshCw className="size-4" />
            )}
            {t("datasets.refresh")}
          </Button>
        </div>
        <div className="overflow-x-auto rounded-md border">
          <table className="w-full text-sm" aria-label={t("datasets.aria_doc_list")}>
            <thead>
              <tr className="border-b text-left text-muted-foreground">
                <th className="p-2 w-8" />
                <th className="p-2">{t("datasets.doc_name")}</th>
                <th className="p-2 hidden lg:table-cell">{t("datasets.doc_created")}</th>
                <th className="p-2 hidden md:table-cell">{t("datasets.doc_size")}</th>
                <th className="p-2 hidden md:table-cell">{t("datasets.doc_chunks")}</th>
                <th className="p-2 hidden md:table-cell">{t("datasets.doc_tokens")}</th>
                <th className="p-2 hidden md:table-cell">{t("datasets.doc_enabled")}</th>
                <th className="p-2">{t("datasets.doc_status")}</th>
                <th className="p-2">{t("datasets.doc_actions")}</th>
              </tr>
            </thead>
            <tbody>
              {loading ? (
                <tr>
                  <td colSpan={9} className="p-2 text-muted-foreground">
                    {t("datasets.chunk_loading")}
                  </td>
                </tr>
              ) : docs.length === 0 ? (
                <tr>
                  <td colSpan={9} className="p-2">
                    {t("datasets.doc_empty")}
                  </td>
                </tr>
              ) : (
                docs.map((doc) => {
                  const s = status(doc.status);
                  const qualityReport = qualityReports[doc.id];
                  const done =
                    doc.status === "3" || doc.status === "DONE";
                  const running =
                    doc.status === "1" || doc.status === "RUNNING";
                  return (
                    <tr key={doc.id} className="border-b align-middle">
                      <td className="p-2">
                        {canExecuteDocument ? (
                          <input
                            type="checkbox"
                            checked={selected.has(doc.id)}
                            onChange={(e: ChangeEvent<HTMLInputElement>) =>
                              toggle(doc.id, e.target.checked)
                            }
                          />
                        ) : null}
                      </td>
                      <td className="p-2">
                        <div className="flex items-center gap-1">
                          <span className="font-medium">{doc.name}</span>
                          {doc.progress_msg && (
                            <Popover>
                              <PopoverTrigger asChild>
                                <button
                                  type="button"
                                  className="inline-flex items-center text-muted-foreground"
                                >
                                  <Info className="size-3.5" />
                                </button>
                              </PopoverTrigger>
                              <PopoverContent className="w-80 max-h-64 overflow-auto text-xs">
                                <div className="whitespace-pre-wrap break-words text-muted-foreground">
                                  {doc.progress_msg}
                                </div>
                              </PopoverContent>
                            </Popover>
                          )}
                        </div>
                      </td>
                      <td className="p-2 hidden lg:table-cell text-muted-foreground">
                        {formatTime(doc.created_at)}
                      </td>
                      <td className="p-2 hidden md:table-cell">
                        {formatBytes(doc.size)}
                      </td>
                      <td className="p-2 hidden md:table-cell">
                        {doc.chunk_count ?? 0}
                      </td>
                      <td className="p-2 hidden md:table-cell">
                        {doc.token_count ?? 0}
                      </td>
                      <td className="p-2 hidden md:table-cell">
                        <Badge
                          variant={
                            doc.enabled === false
                              ? "destructive"
                              : "default"
                          }
                        >
                          {doc.enabled === false ? t("datasets.doc_disable") : t("datasets.doc_enable")}
                        </Badge>
                      </td>
                      <td className="p-2">
                        <Badge
                          variant={
                            s.variant as
                              | "default"
                              | "secondary"
                              | "destructive"
                              | "outline"
                          }
                        >
                          {s.text}
                        </Badge>
                        {running ? (
                          <div className="mt-1 text-xs text-muted-foreground">
                            {Math.round((doc.progress ?? 0) * 100)}%
                          </div>
                        ) : null}
                        {qualityReport ? (
                          <div className="mt-1">
                            <Badge
                              variant={
                                qualityReport.quality_status === "FAIL"
                                  ? "destructive"
                                  : qualityReport.quality_status === "WARN"
                                    ? "secondary"
                                    : "default"
                              }
                            >
                              {qualityReport.quality_status} · {qualityReport.gate_action}
                            </Badge>
                          </div>
                        ) : null}
                      </td>
                      <td className="p-2">
                        <div className="flex items-center gap-0.5">
                          {canExecuteDocument ? (
                            <>
                              <IconButtonWithTooltip
                                label={t("datasets.doc_parse")}
                                onClick={() => handleOne("parse", doc)}
                                disabled={busy || done || running}
                              >
                                <Play />
                              </IconButtonWithTooltip>
                              <IconButtonWithTooltip
                                label={t("datasets.doc_stop")}
                                onClick={() => handleOne("stop", doc)}
                                disabled={busy || !running}
                              >
                                <Square />
                              </IconButtonWithTooltip>
                              <IconButtonWithTooltip
                                label={
                                  doc.enabled === false ? t("datasets.doc_enable") : t("datasets.doc_disable")
                                }
                                onClick={() => handleOne("toggle", doc)}
                                disabled={busy}
                              >
                                <Power />
                              </IconButtonWithTooltip>
                              <IconButtonWithTooltip
                                label={t("datasets.doc_metadata")}
                                onClick={() =>
                                  setMetaDoc({
                                    id: doc.id,
                                    name: doc.name,
                                    text: JSON.stringify(doc.metadata ?? {}, null, 2),
                                  })
                                }
                                disabled={busy}
                              >
                                <Tags />
                              </IconButtonWithTooltip>
                            </>
                          ) : null}
                          {canReadDocument ? (
                            <IconButtonWithTooltip
                              label={t("datasets.parse_quality_button")}
                              onClick={() =>
                                setQualityDoc({ id: doc.id, name: doc.name })
                              }
                              disabled={busy}
                            >
                              <ShieldCheck />
                            </IconButtonWithTooltip>
                          ) : null}
                          <IconButtonWithTooltip
                            label={t("datasets.doc_chunks_btn")}
                            onClick={() => void openChunks(doc)}
                            disabled={busy}
                          >
                            <ListChecks />
                          </IconButtonWithTooltip>
                          {canExecuteDocument || (canDeleteOwnDocument && doc.owned_by_me) ? (
                            <IconButtonWithTooltip
                              label={t("confirm.delete_label")}
                              onClick={() => handleOne("delete", doc)}
                              disabled={busy}
                              className="text-destructive!"
                            >
                              <Trash2 />
                            </IconButtonWithTooltip>
                          ) : null}
                        </div>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>

        <Dialog
          open={!!metaDoc}
          onOpenChange={(o) => !o && setMetaDoc(null)}
        >
          <DialogContent className="sm:max-w-lg">
            <DialogHeader>
              <DialogTitle>
                {t("datasets.meta_title")} - {metaDoc?.name ?? ""}
              </DialogTitle>
            </DialogHeader>
            <div className="space-y-3">
              <p className="text-xs text-muted-foreground">
                {t("datasets.meta_hint")}
              </p>
              <textarea
                className="h-48 w-full rounded border bg-background p-2 text-sm font-mono"
                value={metaDoc?.text ?? ""}
                onChange={(e: ChangeEvent<HTMLTextAreaElement>) =>
                  setMetaDoc((m) =>
                    m ? { ...m, text: e.target.value } : m,
                  )
                }
              />
              <div className="flex justify-end gap-2">
                <Button
                  variant="outline"
                  onClick={() => setMetaDoc(null)}
                >
                  {t("confirm.cancel")}
                </Button>
                <Button onClick={saveMetadata} disabled={busy}>
                  {t("datasets.meta_save")}
                </Button>
              </div>
            </div>
          </DialogContent>
        </Dialog>
      </div>

      <ParseQualityDialog
        open={!!qualityDoc}
        datasetId={datasetId}
        doc={qualityDoc}
        report={qualityDoc ? qualityReports[qualityDoc.id] : undefined}
        scopePath={scopePath}
        onClose={() => setQualityDoc(null)}
      />

      <Dialog
        open={!!chunksDoc}
        onOpenChange={(o) => !o && closeChunks()}
      >
        <ResizableDialogContent defaultWidth={1180} defaultHeight={680}>
          <DialogHeader>              <DialogTitle>
              {t("datasets.chunk_title")} {chunksDoc?.name ?? ""}
            </DialogTitle>
          </DialogHeader>
          <div className="grid min-h-0 flex-1 gap-4 overflow-hidden md:grid-cols-2">
            <div className="flex min-h-0 flex-col">
              <div className="mb-1 text-xs text-muted-foreground">
                {t("datasets.chunk_original")}
              </div>
              <div className="flex-1 overflow-auto rounded-md border bg-muted/20 p-2">
                <OriginalDocumentViewer
                    blobUrl={previewUrl ?? undefined}
                    contentType={previewCt}
                    text={previewText}
                    filename={chunksDoc?.name}
                    loading={previewLoading}
                  />
              </div>
            </div>
            <div className="flex min-h-0 flex-col">
              <div className="mb-1 text-xs text-muted-foreground">
                {t("datasets.chunk_count", { total: String(chunksTotal) })}
              </div>
              <div className="mb-2 flex flex-wrap items-center gap-2">
                <label className="flex items-center gap-1 text-xs">
                  <input
                    type="checkbox"
                    checked={
                      !!chunks &&
                      chunks.length > 0 &&
                      chunks.every(
                        (c) => !!c.id && chunkSel.has(c.id),
                      )
                    }
                    onChange={(e: ChangeEvent<HTMLInputElement>) =>
                      toggleChunkAll(e.target.checked)
                    }
                  />
                  {t("datasets.chunk_select_all")}
                </label>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy || chunkSel.size === 0}
                  onClick={() => void chunkOp("enable")}
                >
                  {t("datasets.chunk_enable")}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={busy || chunkSel.size === 0}
                  onClick={() => void chunkOp("disable")}
                >
                  {t("datasets.chunk_disable")}
                </Button>
                <Button
                  size="sm"
                  variant="destructive"
                  disabled={busy || chunkSel.size === 0}
                  onClick={() => void chunkOp("delete")}
                >
                  {t("datasets.chunk_delete")}
                </Button>
              </div>
              <div className="flex-1 space-y-2 overflow-auto text-sm">
                {chunksLoading ? (
                  <div className="text-muted-foreground">
                    {t("datasets.chunk_loading")}
                  </div>
                ) : !chunks || chunks.length === 0 ? (
                  <div className="text-muted-foreground">
                    {t("datasets.chunk_empty")}
                  </div>
                ) : (
                  chunks.map((c, i) => (
                    <div
                      key={c.id ?? i}
                      className="rounded-md border p-2"
                    >
                      <div className="mb-1 text-xs text-muted-foreground">
                        <label className="flex items-center gap-2">
                          <input
                            type="checkbox"
                            checked={
                              !!c.id && chunkSel.has(c.id)
                            }
                            onChange={(
                              e: ChangeEvent<HTMLInputElement>,
                            ) =>
                              c.id &&
                              toggleChunkSel(c.id, e.target.checked)
                            }
                          />
                          {t("datasets.doc_chunks")} {(chunksPage - 1) * 100 + i + 1}
                        </label>
                  {c.keywords && c.keywords.length > 0
                    ? ` · ${t("datasets.chunk_keywords", { keywords: c.keywords.join(", ") })}`
                    : ""}
                      </div>
                      <div className="whitespace-pre-wrap break-words">
                        {c.content}
                      </div>
                    </div>
                  ))
                )}
              </div>
              <div className="mt-2 flex items-center justify-between text-sm">
                <span className="text-muted-foreground">
                  {t("datasets.chunk_page", { page: String(chunksPage) })}
                </span>
                <div className="flex items-center gap-2">
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={busy || chunksPage <= 1}
                    onClick={prevChunks}
                  >
                    {t("datasets.chunk_prev")}
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={
                      busy || chunksPage * 100 >= chunksTotal
                    }
                    onClick={nextChunks}
                  >
                    {t("datasets.chunk_next")}
                  </Button>
                </div>
              </div>
            </div>
          </div>
        </ResizableDialogContent>
      </Dialog>
    </>
  );
};
