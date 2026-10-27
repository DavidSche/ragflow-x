import { useCallback, useEffect, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  CheckCircle2,
  Clock,
  Download,
  Loader2,
  Plus,
  RefreshCw,
  RotateCcw,
  Trash2,
  Upload,
  XCircle,
} from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { AgentCanvas } from "./AgentCanvas";
import { AgentChat } from "./AgentChat";
import { UserNameLabel } from "@/components/admin/ReferenceLabel";
import { computeDslDiff } from "./dslDiff";
import { DiffView } from "./DiffView";
import {
  AGENT_CANVAS_CATEGORY,
  DATAFLOW_CANVAS_CATEGORY,
  importDsl,
  inferIsAgentFromImport,
} from "./dslUtils";
import { VersionPreview } from "./VersionPreview";

interface SessionLike {
  id: string;
  name?: string;
  user_id?: string;
  source?: string;
}
interface VersionLike {
  id: string;
  title: string;
  release: boolean;
  dsl?: Record<string, any>;
  update_time?: number;
}

type EditorTab = "canvas" | "json";
type PageTab = "config" | "versions" | "sessions" | "run";

export const AgentShow = () => {
  const t = useTranslate();
  const notify = useNotify();
  const { id } = useParams<{ id: string }>();
  const [title, setTitle] = useState("");
  const [release, setRelease] = useState(false);
  const [canvasCategory, setCanvasCategory] = useState<string>(AGENT_CANVAS_CATEGORY);
  const [dslObj, setDslObj] = useState<Record<string, any>>({});
  const [dslText, setDslText] = useState("{}");
  const [editorTab, setEditorTab] = useState<EditorTab>("canvas");
  const [pageTab, setPageTab] = useState<PageTab>("config");
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [versions, setVersions] = useState<VersionLike[]>([]);
  const [sessions, setSessions] = useState<SessionLike[]>([]);
  const [sessionName, setSessionName] = useState("");
  const [metaLoading, setMetaLoading] = useState(false);
  const loadSeqRef = useRef(0);
  const metaLoadedRef = useRef(false);
  const importInputRef = useRef<HTMLInputElement>(null);

  const loadDetail = async () => {
    if (!id) return;
    const seq = ++loadSeqRef.current;
    try {
      const detailRes = await api.get<{ code: number; data: any }>(`/agents/${id}/detail`);
      if (seq !== loadSeqRef.current) return;
      const d = detailRes.data?.data ?? {};
      const dsl = d.dsl ?? {};
      setTitle(d.title ?? "");
      setRelease(!!d.release);
      setCanvasCategory(
        d.canvas_category === DATAFLOW_CANVAS_CATEGORY ? DATAFLOW_CANVAS_CATEGORY : AGENT_CANVAS_CATEGORY,
      );
      setDslObj(dsl);
      setDslText(JSON.stringify(dsl, null, 2));
    } catch (err) {
      if (seq !== loadSeqRef.current) return;
      notify(err instanceof ApiError ? err.displayMessage : t("agents.load_fail"), { type: "error" });
    } finally {
      if (seq === loadSeqRef.current) setLoaded(true);
    }
  };

  const [metaError, setMetaError] = useState<string | null>(null);

  const loadMeta = async () => {
    if (!id) return;
    setMetaLoading(true);
    setMetaError(null);
    // Fetch versions and sessions independently so one failure doesn't
    // hide the other.
    const fetchVersions = api
      .get<{ code: number; data: VersionLike[] }>(`/agents/${id}/versions`)
      .then((res) => {
        const data = res.data?.data;
        setVersions(Array.isArray(data) ? data : []);
      })
      .catch((err) => {
        console.warn("[AgentShow] failed to load versions:", err);
        setVersions([]);
        return err;
      });

    const fetchSessions = api
      .get<{ code: number; data: { items: SessionLike[]; total: number } }>(`/agents/${id}/sessions`)
      .then((res) => {
        const items = res.data?.data?.items ?? res.data?.data;
        setSessions(Array.isArray(items) ? items : []);
      })
      .catch((err) => {
        console.warn("[AgentShow] failed to load sessions:", err);
        setSessions([]);
        return err;
      });

    const [vErr, sErr] = await Promise.all([fetchVersions, fetchSessions]);
    if (vErr || sErr) {
      const msgs: string[] = [];
      if (vErr) msgs.push(t("agents.versions_load_fail"));
      if (sErr) msgs.push(t("agents.sessions_load_fail"));
      setMetaError(msgs.join("; "));
    }
    metaLoadedRef.current = true;
    setMetaLoading(false);
  };

  useEffect(() => {
    if (pageTab !== "config" && !metaLoadedRef.current) void loadMeta();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pageTab]);

  const load = async () => {
    await loadDetail();
    if (metaLoadedRef.current || pageTab !== "config") void loadMeta();
  };

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  const syncDsl = (next: Record<string, any>) => {
    setDslObj(next);
    setDslText(JSON.stringify(next, null, 2));
  };

  const handleExportJson = () => {
    const filename = `${(title || id || "agent").trim()}.json`;
    const blob = new Blob([JSON.stringify(dslObj, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    a.click();
    URL.revokeObjectURL(url);
  };

  const handleImportFile = async (file: File | undefined) => {
    if (!file) return;
    try {
      const text = await file.text();
      const raw = JSON.parse(text);
      if (!raw || typeof raw !== "object" || !Array.isArray(raw?.graph?.nodes)) {
        notify(t("agents.import_fail"), { type: "error" });
        return;
      }
      const isAgent = inferIsAgentFromImport(raw);
      const next = importDsl(raw, isAgent);
      setDslObj(next);
      setDslText(JSON.stringify(next, null, 2));
      setCanvasCategory(isAgent ? AGENT_CANVAS_CATEGORY : DATAFLOW_CANVAS_CATEGORY);
      notify(t("agents.imported"), { type: "success" });
    } catch {
      notify(t("agents.import_fail"), { type: "error" });
    }
  };

  const save = async () => {
    if (!id) return;
    let payload = dslObj;
    if (editorTab === "json") {
      try {
        payload = JSON.parse(dslText);
      } catch {
        notify(t("agents.dsl_invalid"), { type: "error" });
        return;
      }
    }
    setSaving(true);
    try {
      await api.put(`/agents/${id}`, { title, dsl: payload, release });
      notify(t("agents.updated"), { type: "success" });
      await loadDetail();
      metaLoadedRef.current = false;
      void loadMeta();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("agents.update_fail"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  const togglePublish = async () => {
    if (!id) return;
    try {
      await api.post(`/agents/${id}/publish`, { release: !release });
      notify(release ? t("agents.unpublished") : t("agents.published"), { type: "success" });
      await loadDetail();
      metaLoadedRef.current = false;
      void loadMeta();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("agents.publish_fail"), { type: "error" });
    }
  };

  const addSession = async () => {
    if (!id) return;
    try {
      await api.post(`/agents/${id}/sessions`, { name: sessionName });
      notify(t("agents.session_created"), { type: "success" });
      setSessionName("");
      await loadMeta();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("agents.session_fail"), { type: "error" });
    }
  };

  const deleteSession = async (sid: string) => {
    if (!id) return;
    try {
      await api.delete(`/agents/${id}/sessions/${sid}`);
      notify(t("agents.session_deleted"), { type: "success" });
      await loadMeta();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("agents.session_fail"), { type: "error" });
    }
  };

  const chatNewSession = async (name: string) => {
    if (!id) return;
    try {
      await api.post(`/agents/${id}/sessions`, { name });
      await loadMeta();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("agents.session_fail"), { type: "error" });
    }
  };

  const rollbackVersion = async (versionId: string) => {
    if (!id) return;
    try {
      await api.post(`/agents/${id}/versions/${versionId}/rollback`);
      notify(t("agents.rollback_success"), { type: "success" });
      await load();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("agents.rollback_fail"), { type: "error" });
    }
  };

  /* ── Version detail fetching ─────────────────────────────── */
  const [expandedVersionId, setExpandedVersionId] = useState<string | null>(null);
  const [versionDetail, setVersionDetail] = useState<Record<string, any> | null>(null);
  const [versionDetailLoading, setVersionDetailLoading] = useState(false);
  const [versionViewMode, setVersionViewMode] = useState<"preview" | "diff" | "json">("preview");
  const versionDetailSeqRef = useRef(0);

  const fetchVersionDetail = useCallback(
    async (versionId: string) => {
      if (!id) return;
      const seq = ++versionDetailSeqRef.current;
      setVersionDetailLoading(true);
      setVersionDetail(null);
      try {
        const res = await api.get<{ code: number; data: any }>(`/agents/${id}/versions/${versionId}`);
        if (seq !== versionDetailSeqRef.current) return;
        const detail = res.data?.data ?? {};
        setVersionDetail(detail.dsl ? detail : { ...detail, dsl: detail });
      } catch (err) {
        if (seq !== versionDetailSeqRef.current) return;
        console.warn("[AgentShow] failed to load version detail:", err);
        setVersionDetail(null);
      } finally {
        if (seq === versionDetailSeqRef.current) setVersionDetailLoading(false);
      }
    },
    [id],
  );

  const handleExpandVersion = useCallback(
    (versionId: string) => {
      if (expandedVersionId === versionId) {
        setExpandedVersionId(null);
        setVersionDetail(null);
        return;
      }
      setExpandedVersionId(versionId);
      setVersionViewMode("preview");
      void fetchVersionDetail(versionId);
    },
    [expandedVersionId, fetchVersionDetail],
  );

  const handleDownloadVersion = useCallback(() => {
    if (!versionDetail?.dsl) return;
    const dslStr = JSON.stringify(versionDetail.dsl, null, 2);
    const blob = new Blob([dslStr], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${title || "agent"}_${expandedVersionId || "version"}.json`;
    a.click();
    URL.revokeObjectURL(url);
  }, [versionDetail, title, expandedVersionId]);

  /* ── Loading skeleton ─────────────────────────────────────── */
  if (!loaded) {
    return (
      <div className="flex h-full items-center justify-center gap-2 text-muted-foreground">
        <Loader2 className="size-4 animate-spin" aria-hidden="true" />
        <span>{t("agents.loading")}</span>
      </div>
    );
  }

  /* ── Main layout ──────────────────────────────────────────── */
  return (
    <Tabs value={pageTab} onValueChange={(v) => setPageTab(v as PageTab)} className="flex h-full flex-col overflow-hidden">
      {/* ── Toolbar (fixed, never shifts on tab switch) ──────── */}
      <div className="shrink-0 border-b bg-background px-4 py-3">
        {/* Row 1: title + action buttons */}
        <div className="flex items-center gap-3">
          <h1 className="min-w-0 truncate text-lg font-semibold">{title || id}</h1>
          <div className="ml-auto flex shrink-0 items-center gap-1.5">
            <Button variant="ghost" size="sm" onClick={handleExportJson} disabled={saving}>
              <Download className="size-4" aria-hidden="true" />
              <span className="ml-1 hidden sm:inline">{t("agents.export_json")}</span>
            </Button>
            <Button variant="ghost" size="sm" onClick={() => importInputRef.current?.click()} disabled={saving}>
              <Upload className="size-4" aria-hidden="true" />
              <span className="ml-1 hidden sm:inline">{t("agents.import_json")}</span>
            </Button>
            <input
              ref={importInputRef}
              type="file"
              accept=".json,application/json"
              className="hidden"
              onChange={(e) => {
                const f = e.target.files?.[0];
                void handleImportFile(f);
                e.target.value = "";
              }}
            />
            <Button variant="ghost" size="sm" onClick={() => void load()} disabled={saving}>
              <RefreshCw className="size-4" aria-hidden="true" />
              <span className="ml-1 hidden sm:inline">{t("agents.refresh")}</span>
            </Button>
            <div className="mx-1 h-5 w-px bg-border" />
            <Button variant="outline" size="sm" onClick={() => void togglePublish()}>
              {release ? (
                <CheckCircle2 className="size-4 text-green-600" aria-hidden="true" />
              ) : (
                <XCircle className="size-4 text-muted-foreground" aria-hidden="true" />
              )}
              <span className="ml-1">{release ? t("agents.unpublish") : t("agents.publish")}</span>
            </Button>
            <Button size="sm" onClick={() => void save()} disabled={saving}>
              {saving ? <Loader2 className="size-4 animate-spin" /> : t("ra.action.save")}
            </Button>
          </div>
        </div>

        {/* Row 2: tabs + loading indicator (reserved space) */}
        <div className="mt-2 flex items-center gap-3">
          <TabsList className="h-8">
            <TabsTrigger value="config" className="text-xs">{t("agents.tab_config")}</TabsTrigger>
            <TabsTrigger value="versions" className="text-xs">{t("agents.tab_versions")}</TabsTrigger>
            <TabsTrigger value="sessions" className="text-xs">{t("agents.tab_sessions")}</TabsTrigger>
            <TabsTrigger value="run" className="text-xs">{t("agents.tab_run")}</TabsTrigger>
          </TabsList>
          <span
            className={`flex min-w-[72px] items-center gap-1.5 text-xs text-muted-foreground transition-opacity ${
              metaLoading ? "opacity-100" : "opacity-0"
            }`}
          >
            <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
            {t("agents.loading")}
          </span>
          {metaError && (
            <span className="min-w-0 truncate text-xs text-destructive" title={metaError}>
              {metaError}
            </span>
          )}
        </div>
      </div>

      {/* ── Content area (fills remaining height, scrollable) ── */}
      <div className="min-h-0 flex-1 overflow-auto">
        <TabsContent value="config" className="h-full m-0 mt-0 p-4 data-[state=inactive]:hidden">
          <div className="flex h-full flex-col rounded border">
            <div className="flex shrink-0 items-center justify-between border-b px-4 py-2">
              <span className="text-sm font-medium">{t("agents.config_title")}</span>
              <div className="flex items-center gap-1 rounded border p-0.5">
                <button
                  type="button"
                  onClick={() => setEditorTab("canvas")}
                  className={`rounded px-3 py-1 text-xs transition-colors ${
                    editorTab === "canvas" ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground"
                  }`}
                >
                  画布
                </button>
                <button
                  type="button"
                  onClick={() => setEditorTab("json")}
                  className={`rounded px-3 py-1 text-xs transition-colors ${
                    editorTab === "json" ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground"
                  }`}
                >
                  JSON
                </button>
              </div>
            </div>
            <div className="min-h-0 flex-1">
              {editorTab === "canvas" ? (
                <AgentCanvas
                  dsl={dslObj}
                  disabled={saving}
                  onChange={syncDsl}
                  category={canvasCategory}
                  agentTitle={title}
                  onAgentTitleChange={setTitle}
                  viewportClassName="h-full min-h-[400px]"
                />
              ) : (
                <textarea
                  className="h-full min-h-[400px] w-full resize-none bg-background p-4 font-mono text-xs outline-none"
                  value={dslText}
                  onChange={(e) => setDslText(e.target.value)}
                  spellCheck={false}
                />
              )}
            </div>
          </div>
        </TabsContent>

        <TabsContent value="versions" className="m-0 mt-0 p-4 data-[state=inactive]:hidden">
          <section className="rounded border">
            <div className="border-b px-4 py-2.5">
              <h3 className="text-sm font-medium">{t("agents.versions_title")}</h3>
            </div>
            <div className="divide-y">
              {versions.length === 0 ? (
                <p className="px-4 py-8 text-center text-sm text-muted-foreground">{t("agents.versions_empty")}</p>
              ) : (
                versions.map((v) => (
                  <div key={v.id}>
                    {/* Version row header */}
                    <div className="flex items-center justify-between px-4 py-2.5">
                      <div className="flex min-w-0 items-center gap-2">
                        <button
                          type="button"
                          className="shrink-0 cursor-pointer text-muted-foreground hover:text-foreground"
                          onClick={() => handleExpandVersion(v.id)}
                        >
                          {expandedVersionId === v.id ? "▼" : "▶"}
                        </button>
                        {v.update_time ? (
                          <span className="flex items-center gap-1 text-xs text-muted-foreground">
                            <Clock className="size-3" aria-hidden="true" />
                            {new Date(v.update_time * 1000).toLocaleString()}
                          </span>
                        ) : (
                          <span className="truncate text-xs text-muted-foreground">{v.id}</span>
                        )}
                        {v.release && (
                          <span className="shrink-0 rounded bg-green-100 px-1.5 py-0.5 text-[10px] font-medium text-green-700">
                            {t("agents.released")}
                          </span>
                        )}
                      </div>
                      <div className="flex shrink-0 items-center gap-1">
                        {expandedVersionId === v.id && (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-xs"
                            onClick={handleDownloadVersion}
                            disabled={!versionDetail?.dsl}
                          >
                            <Download className="mr-1 size-3" aria-hidden="true" />
                            {t("agents.download")}
                          </Button>
                        )}
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-xs"
                          onClick={() => void rollbackVersion(v.id)}
                        >
                          <RotateCcw className="mr-1 size-3" aria-hidden="true" />
                          {t("agents.rollback")}
                        </Button>
                      </div>
                    </div>

                    {/* Expanded detail area */}
                    {expandedVersionId === v.id && (
                      <div className="border-t bg-muted/30 px-4 py-3">
                        {versionDetailLoading ? (
                          <div className="flex items-center justify-center gap-2 py-8 text-muted-foreground">
                            <Loader2 className="size-4 animate-spin" aria-hidden="true" />
                            <span className="text-xs">{t("agents.loading")}</span>
                          </div>
                        ) : versionDetail?.dsl ? (
                          <>
                            {/* View mode toggle */}
                            <div className="mb-2 flex gap-1">
                              <button
                                type="button"
                                className={`rounded px-2 py-0.5 text-[10px] ${
                                  versionViewMode === "preview"
                                    ? "bg-slate-200 text-slate-800"
                                    : "text-muted-foreground hover:bg-slate-100"
                                }`}
                                onClick={() => setVersionViewMode("preview")}
                              >
                                {t("agents.version_preview")}
                              </button>
                              <button
                                type="button"
                                className={`rounded px-2 py-0.5 text-[10px] ${
                                  versionViewMode === "diff"
                                    ? "bg-slate-200 text-slate-800"
                                    : "text-muted-foreground hover:bg-slate-100"
                                }`}
                                onClick={() => setVersionViewMode("diff")}
                              >
                                {t("agents.version_diff")}
                              </button>
                              <button
                                type="button"
                                className={`rounded px-2 py-0.5 text-[10px] ${
                                  versionViewMode === "json"
                                    ? "bg-slate-200 text-slate-800"
                                    : "text-muted-foreground hover:bg-slate-100"
                                }`}
                                onClick={() => setVersionViewMode("json")}
                              >
                                {t("agents.version_json")}
                              </button>
                            </div>
                            {versionViewMode === "preview" && (
                              <VersionPreview dsl={versionDetail.dsl} className="w-full" />
                            )}
                            {versionViewMode === "diff" && (
                              <div className="max-h-[500px] overflow-auto">
                                <DiffView diff={computeDslDiff(versionDetail.dsl, dslObj)} />
                              </div>
                            )}
                            {versionViewMode === "json" && (
                              <pre className="max-h-[500px] overflow-auto whitespace-pre-wrap rounded bg-white p-3 text-[11px] text-muted-foreground">
                                {JSON.stringify(versionDetail.dsl, null, 2)}
                              </pre>
                            )}
                          </>
                        ) : (
                          <p className="py-4 text-center text-xs text-muted-foreground">
                            {t("agents.version_no_dsl")}
                          </p>
                        )}
                      </div>
                    )}
                  </div>
                ))
              )}
            </div>
          </section>
        </TabsContent>

        <TabsContent value="sessions" className="m-0 mt-0 p-4 data-[state=inactive]:hidden">
          <section className="rounded border">
            <div className="border-b px-4 py-2.5">
              <h3 className="text-sm font-medium">{t("agents.sessions_title")}</h3>
            </div>
            <div className="divide-y">
              {sessions.length === 0 ? (
                <p className="px-4 py-8 text-center text-sm text-muted-foreground">{t("agents.sessions_empty")}</p>
              ) : (
                sessions.map((s) => (
                  <div key={s.id} className="flex items-center justify-between px-4 py-2.5">
                    <div className="min-w-0">
                      <div className="truncate text-sm font-medium">{s.name || s.id}</div>
                      <div className="text-xs text-muted-foreground">
                        <UserNameLabel id={s.user_id} />
                      </div>
                    </div>
                    <Button
                      variant="ghost"
                      size="sm"
                      className="shrink-0 text-destructive"
                      onClick={() => void deleteSession(s.id)}
                    >
                      <Trash2 className="size-3.5" aria-hidden="true" />
                    </Button>
                  </div>
                ))
              )}
            </div>
            <div className="flex gap-2 border-t p-3">
              <Input
                placeholder={t("agents.session_name")}
                value={sessionName}
                onChange={(e) => setSessionName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && sessionName.trim()) void addSession();
                }}
              />
              <Button variant="outline" onClick={() => void addSession()}>
                <Plus className="size-4" aria-hidden="true" />
                <span className="ml-1">{t("agents.new_session")}</span>
              </Button>
            </div>
          </section>
        </TabsContent>

        <TabsContent value="run" className="h-full m-0 mt-0 p-4 data-[state=inactive]:hidden">
          <div className="flex h-full flex-col rounded border">
            <div className="shrink-0 border-b px-4 py-2.5">
              <h3 className="text-sm font-medium">{t("agents.tab_run")}</h3>
            </div>
            <div className="min-h-0 flex-1 p-4">
              <AgentChat
                agentId={id!}
                sessions={sessions}
                createSession={chatNewSession}
                messageListClassName="min-h-0 flex-1"
              />
            </div>
          </div>
        </TabsContent>
      </div>
    </Tabs>
  );
};
