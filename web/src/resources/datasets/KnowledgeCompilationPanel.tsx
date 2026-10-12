import { useCallback, useEffect, useState } from "react";
import { useCanAccess, useNotify, useRecordContext, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { api, ApiError, unwrap, type ApiEnvelope } from "@/lib/api";

interface KnowledgeProfile {
  id: string;
  name: string;
  profile_type: string;
  status: string;
  updated_at: string;
}

interface CompilationRun {
  id: string;
  state: string;
  profile_id: string;
  kind: string;
  manifest_hash: string;
  template_hash: string;
  pipeline_hash: string;
  document_ids: string;
  manifest_complete: boolean;
  downstream_source_snapshot_id: string;
  post_run_source_manifest_hash: string;
  source_consistency: string;
  detail: string;
  created_at: string;
}

interface ArtifactProjection {
  id: string;
  artifact_id: string;
  kind: string;
  state: string;
  artifact_hash: string;
  run_id: string;
  artifact_json?: string;
}

interface DatasetDocument {
  id: string;
  name: string;
  enabled: boolean;
  pipeline_id?: string;
  status?: string;
}

interface PageEnvelope<T> {
  items: T[];
  total: number;
}

interface CompilationRunDetail {
  reason?: string;
  kind?: string;
  progress?: number;
  upstream_state?: string;
  current_phase?: string;
  upstream_age_seconds?: number;
}

interface ArtifactBindingEvidence {
  run_binding?: boolean;
  content_hash_binding?: boolean;
}

const compilationDetail = (value: string): CompilationRunDetail => {
  try {
    return JSON.parse(value) as CompilationRunDetail;
  } catch {
    return {};
  }
};

const artifactBindingEvidence = (value?: string): ArtifactBindingEvidence => {
  try {
    return JSON.parse(value ?? "") as ArtifactBindingEvidence;
  } catch {
    return {};
  }
};

export const KnowledgeCompilationPanel = () => {
  const record = useRecordContext<{ id?: string }>();
  const datasetId = record?.id;
  const t = useTranslate();
  const notify = useNotify();
  const { canAccess: canRead } = useCanAccess({ resource: "compilation", action: "read" });
  const { canAccess: canReadProfiles } = useCanAccess({ resource: "knowledge-profile", action: "read" });
  const { canAccess: canManage } = useCanAccess({ resource: "compilation", action: "manage" });
  const { canAccess: canReconcile } = useCanAccess({ resource: "compilation", action: "execute" });
  const [loading, setLoading] = useState(false);
  const [profiles, setProfiles] = useState<KnowledgeProfile[]>([]);
  const [runs, setRuns] = useState<CompilationRun[]>([]);
  const [artifacts, setArtifacts] = useState<ArtifactProjection[]>([]);
  const [documents, setDocuments] = useState<DatasetDocument[]>([]);
  const [selectedDocumentIDs, setSelectedDocumentIDs] = useState<string[]>([]);
  const [profileID, setProfileID] = useState("");
  const [kind, setKind] = useState("wiki");
  const [triggering, setTriggering] = useState(false);
  const [reconciling, setReconciling] = useState(false);

  const load = useCallback(async () => {
    if (!datasetId || !canRead) return;
    setLoading(true);
    try {
      const profileData = canReadProfiles
        ? unwrap(api.get<ApiEnvelope<PageEnvelope<KnowledgeProfile>>>(`/knowledge-profiles?dataset_id=${encodeURIComponent(datasetId)}`))
        : Promise.resolve<PageEnvelope<KnowledgeProfile>>({ items: [], total: 0 });
      const [profilesData, runsData, artifactsData, documentsData] = await Promise.all([
        profileData,
        unwrap(api.get<ApiEnvelope<PageEnvelope<CompilationRun>>>(`/datasets/${encodeURIComponent(datasetId)}/compilation/runs`)),
        unwrap(api.get<ApiEnvelope<PageEnvelope<ArtifactProjection>>>(`/datasets/${encodeURIComponent(datasetId)}/artifacts`)),
        unwrap(api.get<ApiEnvelope<DatasetDocument[]>>(`/datasets/${encodeURIComponent(datasetId)}/documents`)),
      ]);
      setProfiles(profilesData.items ?? []);
      setRuns(runsData.items ?? []);
      setArtifacts(artifactsData.items ?? []);
      setDocuments(documentsData ?? []);
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("knowledgeCompilation.load_failed"), { type: "error" });
    } finally {
      setLoading(false);
    }
  }, [datasetId, canRead, canReadProfiles, notify, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const triggerRun = async () => {
    if (!datasetId || !canManage) return;
    if (selectedDocumentIDs.length === 0) {
      notify(t("knowledgeCompilation.documents_required"), { type: "error" });
      return;
    }
    setTriggering(true);
    try {
      await api.post(`/datasets/${encodeURIComponent(datasetId)}/compilation/runs`, {
        idempotency_key: `ui-${datasetId}-${Date.now()}`,
        profile_id: profileID || undefined,
        kind: kind || undefined,
        document_ids: selectedDocumentIDs,
      });
      notify(t("knowledgeCompilation.run_accepted"), { type: "success" });
      setSelectedDocumentIDs([]);
      setProfileID("");
      setKind("wiki");
      await load();
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("knowledgeCompilation.trigger_failed"), { type: "error" });
    } finally {
      setTriggering(false);
    }
  };

  const reconcileRuns = async () => {
    if (!datasetId || !canReconcile) return;
    setReconciling(true);
    try {
      await api.post(`/datasets/${encodeURIComponent(datasetId)}/compilation/reconcile`, {});
      notify(t("knowledgeCompilation.reconcile_started"), { type: "success" });
      await load();
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("knowledgeCompilation.reconcile_failed"), { type: "error" });
    } finally {
      setReconciling(false);
    }
  };

  const toggleDocument = (documentID: string) => {
    setSelectedDocumentIDs((current) =>
      current.includes(documentID)
        ? current.filter((id) => id !== documentID)
        : [...current, documentID],
    );
  };

  if (!datasetId || !canRead) return null;

  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle>{t("knowledgeCompilation.title")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="rounded border p-3 text-sm">
          <div className="font-medium">{t("knowledgeCompilation.profiles")}</div>
          {loading ? <div className="text-muted-foreground">{t("common.loading")}</div> : profiles.length === 0 ? (
            <div className="text-muted-foreground">{t("knowledgeCompilation.no_profiles")}</div>
          ) : profiles.map((profile) => (
            <div key={profile.id} className="mt-2 flex flex-wrap gap-2">
              <span className="font-medium">{profile.name}</span>
              <span>{profile.profile_type}</span>
              <span>{profile.status}</span>
            </div>
          ))}
        </div>

        <div className="rounded border p-3 text-sm">
          <div className="font-medium">{t("knowledgeCompilation.runs")}</div>
          {runs.length === 0 ? (
            <div className="text-muted-foreground">{t("knowledgeCompilation.no_runs")}</div>
          ) : runs.map((run) => {
            const detail = compilationDetail(run.detail);
            return (
              <div key={run.id} className="mt-2 space-y-1">
                <div className="flex flex-wrap gap-2">
                  <span className="font-medium">{run.state}</span>
                  {run.kind && <span>{run.kind}</span>}
                  {detail.upstream_state && (
                    <span className="text-muted-foreground">
                      {t("knowledgeCompilation.upstream_state")}: {detail.upstream_state}
                    </span>
                  )}
                  {detail.current_phase && (
                    <span className="text-muted-foreground">
                      {t("knowledgeCompilation.current_phase")}: {detail.current_phase}
                    </span>
                  )}
                  {run.source_consistency && (
                    <span className={run.source_consistency === "changed" ? "text-amber-700 dark:text-amber-300" : "text-muted-foreground"}>
                      {t("knowledgeCompilation.source_consistency")}: {run.source_consistency}
                    </span>
                  )}
                  <span>{new Date(run.created_at).toLocaleString()}</span>
                </div>
                {detail.reason && (
                  <div className="text-muted-foreground">
                    {t("knowledgeCompilation.reason")}: {detail.reason}
                  </div>
                )}
                {run.source_consistency === "changed" && (
                  <div className="text-amber-700 dark:text-amber-300">
                    {t("knowledgeCompilation.source_changed")}
                  </div>
                )}
                {run.state === "unknown" && detail.reason === "upstream_stalled" && typeof detail.upstream_age_seconds === "number" && (
                  <div className="text-amber-700 dark:text-amber-300">
                    {t("knowledgeCompilation.upstream_stalled")}: {Math.max(0, detail.upstream_age_seconds / 3600).toFixed(1)}h
                  </div>
                )}
                {run.state === "blocked" && detail.reason === "phase0_live_contract_unverified" && (
                  <div className="text-amber-700 dark:text-amber-300">{t("knowledgeCompilation.blocked_phase0")}</div>
                )}
              </div>
            );
          })}
        </div>

        <div className="rounded border p-3 text-sm">
          <div className="font-medium">{t("knowledgeCompilation.artifacts")}</div>
          {artifacts.length === 0 ? (
            <div className="text-muted-foreground">{t("knowledgeCompilation.no_artifacts")}</div>
          ) : artifacts.map((artifact) => (
            <div key={artifact.id} className="mt-2 flex flex-wrap gap-2">
              <span className="font-medium">{artifact.kind}</span>
              <span>{artifact.state}</span>
              <span className="font-mono text-xs">{artifact.artifact_hash}</span>
              {artifactBindingEvidence(artifact.artifact_json).run_binding &&
                artifactBindingEvidence(artifact.artifact_json).content_hash_binding && (
                <span className="text-emerald-700 dark:text-emerald-300">
                  {t("knowledgeCompilation.artifacts_bound")}
                </span>
              )}
            </div>
          ))}
          {artifacts.some((artifact) => artifact.state === "unverified") && (
            <div className="mt-2 text-amber-700 dark:text-amber-300">{t("knowledgeCompilation.artifacts_unverified")}</div>
          )}
        </div>

        {canReconcile && (
          <Button type="button" variant="outline" onClick={reconcileRuns} disabled={reconciling || loading}>
            {t("knowledgeCompilation.reconcile")}
          </Button>
        )}

        {canManage && (
          <div className="space-y-3 rounded border p-3">
            <div className="grid gap-2 md:grid-cols-2">
              <select
                className="h-9 rounded border bg-background px-3 text-sm"
                value={profileID}
                onChange={(event) => setProfileID(event.target.value)}
                aria-label={t("knowledgeCompilation.profile")}
              >
                <option value="">{t("knowledgeCompilation.profile_optional")}</option>
                {profiles.filter((profile) => profile.status === "active").map((profile) => (
                  <option key={profile.id} value={profile.id}>{profile.name}</option>
                ))}
              </select>
              <select
                className="h-9 rounded border bg-background px-3 text-sm"
                value={kind}
                onChange={(event) => setKind(event.target.value)}
                aria-label={t("knowledgeCompilation.kind")}
              >
                {["wiki", "page_index", "tree", "graph", "mindmap", "timeline"].map((value) => (
                  <option key={value} value={value}>{value}</option>
                ))}
              </select>
            </div>
            <div className="max-h-48 space-y-1 overflow-y-auto rounded border p-2">
              {documents.length === 0 ? (
                <div className="text-muted-foreground">{t("knowledgeCompilation.no_documents")}</div>
              ) : documents.map((document) => (
                <label key={document.id} className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={selectedDocumentIDs.includes(document.id)}
                    onChange={() => toggleDocument(document.id)}
                    disabled={!document.pipeline_id || !document.enabled}
                  />
                  <span className="flex-1">{document.name}</span>
                  {document.pipeline_id ? (
                    <span className="font-mono text-xs">{document.pipeline_id}</span>
                  ) : (
                    <span className="text-muted-foreground">{t("knowledgeCompilation.pipeline_missing")}</span>
                  )}
                  {!document.enabled && (
                    <span className="text-muted-foreground">{t("knowledgeCompilation.document_disabled")}</span>
                  )}
                </label>
              ))}
            </div>
            <Button type="button" onClick={triggerRun} disabled={triggering}>
              {t("knowledgeCompilation.trigger")}
            </Button>
          </div>
        )}
      </CardContent>
    </Card>
  );
};
