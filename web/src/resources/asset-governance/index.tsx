import { useCallback, useEffect, useState } from "react";
import type { ResourceProps } from "ra-core";
import { useCanAccess, useGetIdentity, useGetList, useNotify, useRefresh, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { Archive, Boxes, Copy, Download, Loader2, Plus, RefreshCw, RotateCcw, ShieldCheck, Sparkles, Upload } from "lucide-react";
import { api, unwrap, ApiError, type ApiEnvelope } from "../../lib/api";
import { PARAMETER_PROFILES, PROMPT_PRESETS } from "../workbench/prompt-presets";
import { NumericRangeField } from "@/components/NumericRangeField";
import { KnowledgeHealthCards, type KnowledgeHealthSummary } from "./KnowledgeHealthCards";

interface PageEnvelope<T> { items: T[]; total: number; }
interface TemplateAsset {
  id: string; key: string; name: string; app_types: string; description: string;
  latest_version: number; source: string; status: string; payload_json: string;
}
interface PromptPolicy {
  id: string; scope: string; object_id: string; version: number; active: boolean;
  preset_id: string; profile_id: string; payload: string; created_at: string;
}
interface LifecycleDataset {
  id: string; name: string; owner_id: string; owner_team_id: string; source_type: string;
  business_domain: string; sensitivity: string; expires_at?: string; review_status: string;
  quality_score: number; lifecycle_status: string;
}
interface EvalSet { id: string; name: string; app_type: string; item_count: number; status: string; }
interface EvalCase { id: string; question: string; expected_answer: string; source: string; }
interface PolicyObject { id: string; name: string; }
interface KnowledgeAssetEvidence {
  answer_snapshot_id: string; request_id: string; citation_id: string; chunk_id: string;
  document_id: string; document_version: string; citation_locator: string; citation_hash: string;
  created_at: string;
}
interface KnowledgeAssetTrace {
  trace_id: string; request_id: string; assistant_id: string; app_type: string;
  channel: string; status: string;
}
interface KnowledgeAssetTask {
  id: string; title: string; category: string; status: string; priority: string;
  owner_id: string; source_chunk_id: string; source_document_version: string;
  regression_status: string; created_at: string;
}
interface KnowledgeAssetMapItem extends LifecycleDataset {
  lifecycle_status: string; assistant_ids: string[]; assistant_release_ids: string[];
  trace_runs: KnowledgeAssetTrace[]; chunk_evidence: KnowledgeAssetEvidence[];
  knowledge_tasks: KnowledgeAssetTask[];
}

const appTypes = (value: string) => value.split(",").filter(Boolean).join(" / ");
const dateValue = (value?: string) => value ? value.slice(0, 10) : "";

const emptyTemplate = { key: "", name: "", description: "", app_types: "chat", status: "draft", payload_json: "{}" };

export const AssetGovernanceBoard = () => {
  const t = useTranslate();
  const notify = useNotify();
  const fail = (error: unknown, notifyFn: ReturnType<typeof useNotify>) => {
    notifyFn(error instanceof ApiError ? error.displayMessage : error instanceof Error ? error.message : t("assetGovernance.operation_failed"), { type: "error" });
  };
  const { data: identityData } = useGetIdentity();
  const identity = identityData as { role?: string } | undefined;
  const { data: workspaceRecords = [] } = useGetList("tenants", { pagination: { page: 1, perPage: 200 } });
  const { data: userRecords = [] } = useGetList("users", { pagination: { page: 1, perPage: 200 } });
  const { data: teamRecords = [] } = useGetList("teams", { pagination: { page: 1, perPage: 200 } });
  const { data: chatRecords = [] } = useGetList("chats", { pagination: { page: 1, perPage: 200 } });
  const { data: searchAppRecords = [] } = useGetList("search-apps", { pagination: { page: 1, perPage: 200 } });
  const { data: agentRecords = [] } = useGetList("agents", { pagination: { page: 1, perPage: 200 } });
  const [tab, setTab] = useState("templates");
  const [loading, setLoading] = useState(true);
  const [templates, setTemplates] = useState<TemplateAsset[]>([]);
  const [policies, setPolicies] = useState<PromptPolicy[]>([]);
  const [datasets, setDatasets] = useState<LifecycleDataset[]>([]);
  const [health, setHealth] = useState<KnowledgeHealthSummary | null>(null);
  const [assetMap, setAssetMap] = useState<KnowledgeAssetMapItem[]>([]);
  const [evalSets, setEvalSets] = useState<EvalSet[]>([]);
  const [evalCases, setEvalCases] = useState<EvalCase[]>([]);
  const [selectedEvalSet, setSelectedEvalSet] = useState<string>("");
  const [templateForm, setTemplateForm] = useState(emptyTemplate);
  const [editingTemplate, setEditingTemplate] = useState<TemplateAsset | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [importText, setImportText] = useState("");
  const [copyTarget, setCopyTarget] = useState("");
  const { canAccess: canCopyAcrossWorkspaces } = useCanAccess({ resource: "tenant", action: "governance.manage" });
  const { canAccess: canInstantiateChat } = useCanAccess({ resource: "chat", action: "manage" });
  const refresh = useRefresh();
  const [busy, setBusy] = useState(false);
  const [policyForm, setPolicyForm] = useState({ scope: "tenant", object_id: "", preset_id: "", profile_id: "", payload: "{}" });
  const [lifecycleForm, setLifecycleForm] = useState<LifecycleDataset | null>(null);
  const [evalForm, setEvalForm] = useState({ name: "", app_type: "chat", cases: "报销流程是什么？|按制度审批\n年假如何申请？|提交申请" });
  const [assetSearch, setAssetSearch] = useState("");
  const [assetLifecycle, setAssetLifecycle] = useState("");
  const policyObjects: PolicyObject[] = policyForm.scope === "chat"
    ? (chatRecords as unknown as PolicyObject[])
    : policyForm.scope === "search"
      ? (searchAppRecords as unknown as PolicyObject[])
      : policyForm.scope === "agent"
        ? (agentRecords as unknown as Array<PolicyObject & { title?: string }>).map((item) => ({ ...item, name: item.title ?? "" }))
        : [];

  const governanceObjects: Array<{ id?: string; name?: string; title?: string }> = [
    ...(chatRecords as unknown as Array<{ id?: string; name?: string }>),
    ...(searchAppRecords as unknown as Array<{ id?: string; name?: string }>),
    ...(agentRecords as unknown as Array<{ id?: string; title?: string }>),
  ];
  const governanceObjectName = (objectId: string) => {
    if (objectId.startsWith("new:")) return objectId.slice(4);
    const object = governanceObjects.find((item) => item.id === objectId);
    return object?.name
      ?? object?.title
      ?? objectId;
  };
  const governanceUserName = (userId: string) =>
    (userRecords as unknown as Array<{ id?: string; username?: string }>).find((user) => user.id === userId)?.username ?? userId;

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const [templatesRes, policiesRes, datasetsRes, evalSetsRes, healthRes, assetMapRes] = await Promise.all([
        unwrap(api.get<ApiEnvelope<PageEnvelope<TemplateAsset>>>("/scenario-template-assets?page=1&page_size=100")),
        unwrap(api.get<ApiEnvelope<PageEnvelope<PromptPolicy>>>("/prompt-policies?page=1&page_size=100")),
        unwrap(api.get<ApiEnvelope<LifecycleDataset[]>>("/knowledge-lifecycle")),
        unwrap(api.get<ApiEnvelope<PageEnvelope<EvalSet>>>("/eval-sets?page=1&page_size=100")),
        unwrap(api.get<ApiEnvelope<KnowledgeHealthSummary>>("/knowledge-lifecycle/health")),
        unwrap(api.get<ApiEnvelope<KnowledgeAssetMapItem[]>>("/knowledge-lifecycle/asset-map")),
      ]);
      setTemplates(templatesRes.items);
      setPolicies(policiesRes.items);
      setDatasets(datasetsRes);
      setEvalSets(evalSetsRes.items);
      setHealth(healthRes);
      setAssetMap(assetMapRes);
    } catch (error) {
      fail(error, notify);
    } finally {
      setLoading(false);
    }
  }, [notify]);

  useEffect(() => { void load(); }, [load]);

  const loadCases = useCallback(async (id: string) => {
    setSelectedEvalSet(id);
    if (!id) { setEvalCases([]); return; }
    try {
      const data = await unwrap(api.get<ApiEnvelope<{ cases: EvalCase[] }>>(`/eval-sets/${id}`));
      setEvalCases(data.cases ?? []);
    } catch (error) { fail(error, notify); }
  }, [notify]);

  const saveTemplate = async () => {
    setBusy(true);
    try {
      const payload = {
        key: templateForm.key,
        name: templateForm.name,
        description: templateForm.description,
        app_types: templateForm.app_types.split(",").map((item) => item.trim()).filter(Boolean),
        status: templateForm.status,
        payload: JSON.parse(templateForm.payload_json || "{}"),
        change_note: t("assetGovernance.change_note_ui"),
      };
      if (editingTemplate) {
        await api.put(`/scenario-template-assets/${editingTemplate.id}`, payload);
      } else {
        await api.post("/scenario-template-assets", payload);
      }
      setDialogOpen(false); setEditingTemplate(null); setTemplateForm(emptyTemplate);
      notify(t("assetGovernance.template_save_success"), { type: "success" }); await load();
    } catch (error) { fail(error, notify); } finally { setBusy(false); }
  };

  const run = async (action: () => Promise<unknown>, message: string) => {
    setBusy(true);
    try { await action(); notify(message, { type: "success" }); await load(); }
    catch (error) { fail(error, notify); } finally { setBusy(false); }
  };

  const exportTemplate = async (item: TemplateAsset) => {
    try {
      const data = await unwrap(api.get<ApiEnvelope<unknown>>(`/scenario-template-assets/${item.id}/export`));
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
      const link = document.createElement("a");
      link.href = URL.createObjectURL(blob);
      link.download = `${item.key}.json`;
      link.click(); URL.revokeObjectURL(link.href);
    } catch (error) { fail(error, notify); }
  };

  const savePolicy = () => run(async () => {
    await api.post("/prompt-policies", { ...policyForm, payload: JSON.parse(policyForm.payload || "{}") });
  }, t("assetGovernance.policy_created"));

  const saveLifecycle = () => run(async () => {
    if (!lifecycleForm) return;
    await api.put(`/datasets/${lifecycleForm.id}/lifecycle`, {
      owner_id: lifecycleForm.owner_id, owner_team_id: lifecycleForm.owner_team_id,
      source_type: lifecycleForm.source_type, business_domain: lifecycleForm.business_domain,
      sensitivity: lifecycleForm.sensitivity, expires_at: lifecycleForm.expires_at || null,
      review_status: lifecycleForm.review_status, quality_score: lifecycleForm.quality_score,
    });
    setLifecycleForm(null);
  }, t("assetGovernance.lifecycle_updated"));

  const createEvalSet = () => run(async () => {
    const cases = evalForm.cases.split("\n").filter(Boolean).map((line) => {
      const [question, expected = "", keywords = ""] = line.split("|");
      return { question: question.trim(), expected_answer: expected.trim(), expected_keywords: keywords.trim() };
    });
    await api.post("/eval-sets", { name: evalForm.name, app_type: evalForm.app_type, cases });
  }, t("assetGovernance.eval_set_created"));

  const createTemplateReleaseCandidate = (item: TemplateAsset) => run(async () => {
    await api.post(`/scenario-template-assets/${item.id}/release-candidate`, {});
  }, t("release_governance.candidate_from_template_created"));

  const instantiateChatFromTemplate = async (item: TemplateAsset) => {
    try {
      const approval = await unwrap<Record<string, unknown>>(api.post(`/scenario-template-assets/${item.id}/instantiate-chat`, {
        create_missing_datasets: true,
      }));
      if (approval.status === "pending_approval") {
        notify(t("release_governance.chat_template_approval_submitted"), { type: "info" });
      } else {
        notify(t("release_governance.chat_from_template_created"), { type: "success" });
      }
      await load();
      refresh();
    } catch (error) { fail(error, notify); }
  };

  return (
    <div className="space-y-4">
      <header className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 className="text-xl font-semibold">{t("assetGovernance.page_title")}</h1>
          <p className="text-sm text-muted-foreground">{t("assetGovernance.page_subtitle")}</p>
        </div>
        <Button variant="outline" onClick={() => void load()} disabled={loading} aria-label={t("assetGovernance.refresh_aria")}><RefreshCw className="size-4" /> {t("assetGovernance.refresh")}</Button>
      </header>

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList className="h-auto flex-wrap">
          <TabsTrigger value="templates">{t("assetGovernance.tab_templates")}</TabsTrigger>
          <TabsTrigger value="prompts">{t("assetGovernance.tab_prompts")}</TabsTrigger>
          <TabsTrigger value="knowledge">{t("assetGovernance.tab_knowledge")}</TabsTrigger>
          <TabsTrigger value="asset-map">{t("assetGovernance.asset_map_title")}</TabsTrigger>
          <TabsTrigger value="knowledge-health">{t("assetGovernance.knowledge_health_title")}</TabsTrigger>
          <TabsTrigger value="evals">{t("assetGovernance.tab_evals")}</TabsTrigger>
        </TabsList>

        <TabsContent value="templates" className="space-y-4">
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => { setEditingTemplate(null); setTemplateForm(emptyTemplate); setDialogOpen(true); }}><Plus className="size-4" /> {t("assetGovernance.create_template")}</Button>
            <Button variant="outline" onClick={() => void run(async () => {
              const result = await unwrap(api.post<ApiEnvelope<{ created: number }>>("/eval-sets/generate-from-templates"));
              if (!result.created) throw new Error(t("assetGovernance.eval_sets_all_generated"));
            }, t("assetGovernance.eval_sets_generated"))} disabled={busy}><Sparkles className="size-4" /> {t("assetGovernance.auto_fill_eval_sets")}</Button>
            <Button variant="outline" onClick={() => { const input = document.createElement("input"); input.type = "file"; input.accept = ".json"; input.onchange = async () => { const file = input.files?.[0]; if (!file) return; setImportText(await file.text()); await run(async () => { await api.post("/scenario-template-assets/import", JSON.parse(importText || await file.text())); }, t("assetGovernance.import_success")); }; input.click(); }}><Upload className="size-4" /> {t("assetGovernance.import")}</Button>
          </div>
          {canCopyAcrossWorkspaces ? (
            <Card><CardContent className="grid gap-2 md:grid-cols-[1fr_auto]">
              <select
                className="w-full rounded border bg-background px-2 py-1 text-sm"
                aria-label={t("assetGovernance.target_workspace")}
                value={copyTarget}
                onChange={(event) => setCopyTarget(event.target.value)}
              >
                <option value="">{t("assetGovernance.select_target_workspace")}</option>
                {workspaceRecords.map((workspace) => (
                  <option key={workspace.id} value={workspace.id}>{workspace.name}</option>
                ))}
              </select>
              <Button variant="outline" disabled={!copyTarget || busy}>{t("assetGovernance.copy_mode_enabled")}</Button>
            </CardContent></Card>
          ) : null}
          {loading ? <Skeleton className="h-40" /> : templates.map((item) => (
            <Card key={item.id}><CardContent className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
              <div>
                <div className="flex flex-wrap items-center gap-2">
                  <span className="font-medium">{item.name}</span><span className="rounded bg-muted px-1.5 text-xs">{item.key}</span>
                  <span className="rounded bg-muted px-1.5 text-xs">v{item.latest_version}</span>
                  <span className="rounded bg-muted px-1.5 text-xs">{item.status}</span>
                </div>
                <p className="mt-1 text-sm text-muted-foreground">{item.description || t("assetGovernance.no_description")}</p>
                <p className="mt-1 text-xs text-muted-foreground">{appTypes(item.app_types)}</p>
              </div>
              <div className="flex flex-wrap gap-2">
                <Button size="sm" variant="outline" onClick={() => void exportTemplate(item)}><Download className="size-4" /> {t("assetGovernance.export")}</Button>
                <Button size="sm" variant="outline" onClick={() => { setEditingTemplate(item); setTemplateForm({ ...emptyTemplate, name: item.name, description: item.description, app_types: item.app_types, status: item.status, payload_json: JSON.stringify(JSON.parse(item.payload_json || "{}"), null, 2) }); setDialogOpen(true); }}>{t("assetGovernance.new_version")}</Button>
                {canCopyAcrossWorkspaces ? <Button size="sm" variant="outline" onClick={() => void run(() => api.post(`/scenario-template-assets/${item.id}/copy`, { target_tenant_id: copyTarget }), t("assetGovernance.copied"))} disabled={!copyTarget}><Copy className="size-4" /> {t("assetGovernance.copy")}</Button> : null}
                <Button size="sm" variant="outline" onClick={() => void run(() => api.post(`/scenario-template-assets/${item.id}/eval-set`, { name: t("assetGovernance.eval_set_default_name", { name: item.name }) }), t("assetGovernance.eval_set_generated"))}>{t("assetGovernance.generate_eval")}</Button>
                {canInstantiateChat && item.status === "published" && item.app_types.split(",").includes("chat") ? (
                  <Button size="sm" variant="outline" disabled={busy} onClick={() => void instantiateChatFromTemplate(item)}>
                    <Sparkles className="size-4" /> {t("release_governance.create_chat_from_template")}
                  </Button>
                ) : null}
                <Button size="sm" variant="outline" onClick={() => void createTemplateReleaseCandidate(item)} disabled={busy}><ShieldCheck className="size-4" /> {t("release_governance.create_candidate_from_template")}</Button>
                <Button size="sm" variant="ghost" onClick={() => void run(() => api.delete(`/scenario-template-assets/${item.id}`), t("assetGovernance.archived"))}><Archive className="size-4" /> {t("assetGovernance.archive")}</Button>
              </div>
            </CardContent></Card>
          ))}
        </TabsContent>

        <TabsContent value="knowledge-health" className="space-y-4">
          <div>
            <h2 className="text-lg font-semibold">{t("assetGovernance.knowledge_health_title")}</h2>
            <p className="text-sm text-muted-foreground">{t("assetGovernance.knowledge_health_subtitle")}</p>
          </div>
          {loading ? <Skeleton className="h-40" /> : <KnowledgeHealthCards summary={health} />}
        </TabsContent>

        <TabsContent value="prompts" className="grid gap-4 lg:grid-cols-[360px_1fr]">
          <Card><CardHeader><CardTitle className="text-sm">{t("assetGovernance.new_default_policy")}</CardTitle></CardHeader><CardContent className="space-y-3">
            <div className="space-y-1"><Label>{t("assetGovernance.scope")}</Label><select className="w-full rounded border bg-background px-2 py-1 text-sm" value={policyForm.scope} onChange={(event) => setPolicyForm({ ...policyForm, scope: event.target.value, object_id: "" })}><option value="tenant">{t("assetGovernance.scope_tenant_default")}</option><option value="chat">Chat</option><option value="search">Search</option><option value="agent">Agent</option></select></div>
            {policyForm.scope === "tenant" ? (
              <div className="space-y-1"><Label>{t("assetGovernance.target_object")}</Label><p className="text-xs text-muted-foreground">{t("assetGovernance.scope_tenant_hint")}</p></div>
            ) : (
              <div className="space-y-1"><Label>{t("assetGovernance.target_object")}</Label>
                <select className="w-full rounded border bg-background px-2 py-1 text-sm" value={policyForm.object_id} onChange={(event) => setPolicyForm({ ...policyForm, object_id: event.target.value })}>
                  <option value="">{t("assetGovernance.select_object", { type: policyForm.scope === "chat" ? t("assetGovernance.object_chat") : policyForm.scope === "search" ? t("assetGovernance.object_search") : t("assetGovernance.object_agent") })}</option>
                  {policyObjects.map((object) => (
                    <option key={object.id} value={object.id}>{object.name}</option>
                  ))}
                </select>
                {policyObjects.length === 0 ? <p className="text-xs text-muted-foreground">{t("assetGovernance.no_objects_hint")}</p> : null}
              </div>
            )}
            <div className="space-y-1"><Label>{t("assetGovernance.prompt_preset")}</Label>
              <select className="w-full rounded border bg-background px-2 py-1 text-sm" value={policyForm.preset_id} onChange={(event) => setPolicyForm({ ...policyForm, preset_id: event.target.value })}>
                <option value="">{t("assetGovernance.no_preset")}</option>
                {PROMPT_PRESETS.map((preset) => (<option key={preset.id} value={preset.id}>{preset.label}</option>))}
              </select>
            </div>
            <div className="space-y-1"><Label>{t("assetGovernance.parameter_profile")}</Label>
              <select className="w-full rounded border bg-background px-2 py-1 text-sm" value={policyForm.profile_id} onChange={(event) => setPolicyForm({ ...policyForm, profile_id: event.target.value })}>
                <option value="">{t("assetGovernance.no_profile")}</option>
                {PARAMETER_PROFILES.map((profile) => (<option key={profile.id} value={profile.id}>{profile.label}</option>))}
              </select>
            </div>
            <Textarea rows={5} value={policyForm.payload} onChange={(event) => setPolicyForm({ ...policyForm, payload: event.target.value })} />
            <Button className="w-full" onClick={() => void savePolicy()} disabled={busy}>{t("assetGovernance.save_new_version")}</Button>
          </CardContent></Card>
          <div className="space-y-3">
            {policies.map((item) => (
              <Card key={item.id}><CardContent className="flex items-center justify-between gap-3">
                <div><div className="flex items-center gap-2"><span className="font-medium">{item.scope}</span><span className="rounded bg-muted px-1.5 text-xs">v{item.version}</span>{item.active ? <span className="rounded bg-primary/10 px-1.5 text-xs text-primary">active</span> : null}</div><p className="text-xs text-muted-foreground">{item.object_id ? governanceObjectName(item.object_id) : t("assetGovernance.global_default")}</p></div>
                <div className="flex gap-2"><Button size="sm" variant="outline" disabled={item.active || busy} onClick={() => void run(() => api.post(`/prompt-policies/${item.id}/rollback`), t("assetGovernance.rolled_back"))}><RotateCcw className="size-4" /> {t("assetGovernance.rollback")}</Button><Button size="sm" variant="ghost" disabled={item.active || busy} onClick={() => void run(() => api.delete(`/prompt-policies/${item.id}`), t("assetGovernance.deleted"))}>{t("assetGovernance.delete")}</Button></div>
              </CardContent></Card>
            ))}
          </div>
        </TabsContent>

        <TabsContent value="knowledge" className="space-y-3">
          {datasets.map((item) => (
            <Card key={item.id}><CardContent className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
              <div><div className="font-medium">{item.name}</div><div className="text-xs text-muted-foreground">{item.owner_id ? governanceUserName(item.owner_id) : t("assetGovernance.no_owner")} · {item.source_type || t("assetGovernance.no_source")} · {item.sensitivity || t("assetGovernance.no_sensitivity")}</div></div>
              <div className="flex items-center gap-2"><span className={`rounded px-2 py-1 text-xs ${item.lifecycle_status === "expired" ? "bg-destructive/10 text-destructive" : item.lifecycle_status === "due" ? "bg-amber-500/10 text-amber-600" : "bg-muted"}`}>{item.lifecycle_status}</span><Button size="sm" variant="outline" onClick={() => setLifecycleForm(item)}>{t("assetGovernance.edit")}</Button></div>
            </CardContent></Card>
          ))}
        </TabsContent>

        <TabsContent value="asset-map" className="space-y-3">
          <div className="grid gap-2 sm:grid-cols-[1fr_180px]">
            <Input
              value={assetSearch}
              onChange={(event) => setAssetSearch(event.target.value)}
              placeholder={t("assetGovernance.asset_map_search")}
              aria-label={t("assetGovernance.asset_map_search")}
            />
            <select
              className="h-9 rounded border bg-background px-2 text-sm"
              value={assetLifecycle}
              onChange={(event) => setAssetLifecycle(event.target.value)}
              aria-label={t("assetGovernance.asset_map_lifecycle_filter")}
            >
              <option value="">{t("assetGovernance.asset_map_all_lifecycle")}</option>
              <option value="none">{t("assetGovernance.lifecycle_none")}</option>
              <option value="current">{t("assetGovernance.lifecycle_current")}</option>
              <option value="due">{t("assetGovernance.lifecycle_due")}</option>
              <option value="expired">{t("assetGovernance.lifecycle_expired")}</option>
            </select>
          </div>
          {assetMap
            .filter((item) => item.name.toLowerCase().includes(assetSearch.toLowerCase()) ||
              item.business_domain.toLowerCase().includes(assetSearch.toLowerCase()))
            .filter((item) => !assetLifecycle || item.lifecycle_status === assetLifecycle)
            .map((item) => (
            <Card key={item.id}>
              <CardHeader className="pb-2">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <CardTitle className="text-base">{item.name}</CardTitle>
                  <span className={`rounded px-2 py-1 text-xs ${item.lifecycle_status === "expired" ? "bg-destructive/10 text-destructive" : item.lifecycle_status === "due" ? "bg-amber-500/10 text-amber-600" : "bg-muted"}`}>
                    {item.lifecycle_status}
                  </span>
                </div>
              </CardHeader>
              <CardContent className="space-y-3">
                <div className="flex flex-wrap gap-2 text-xs text-muted-foreground">
                  <span>{item.business_domain || t("assetGovernance.asset_map_unset")}</span>
                  <span>{item.owner_id ? governanceUserName(item.owner_id) : t("assetGovernance.asset_map_unset")}</span>
                  <span>{item.sensitivity || t("assetGovernance.asset_map_unset")}</span>
                  <span>{t("assetGovernance.asset_map_assistants")}: {item.assistant_ids.length}</span>
                  <span>{t("assetGovernance.asset_map_chunk_evidence")}: {item.chunk_evidence.length}</span>
                  <span>{t("assetGovernance.asset_map_tasks")}: {item.knowledge_tasks.length}</span>
                </div>
                {item.chunk_evidence.length > 0 ? (
                  <div className="space-y-1" aria-label={t("assetGovernance.asset_map_chunk_evidence")}>
                    {item.chunk_evidence.slice(0, 5).map((evidence) => (
                      <div key={`${evidence.answer_snapshot_id}-${evidence.citation_id}`} className="rounded border bg-muted/40 px-2 py-1 text-xs">
                        <div className="font-medium">{evidence.chunk_id || evidence.citation_id}</div>
                        <div className="text-muted-foreground">{evidence.document_version || "-"} · {evidence.citation_locator || "-"} · {evidence.citation_hash}</div>
                      </div>
                    ))}
                  </div>
                ) : null}
                <div className="flex flex-wrap gap-2">
                  {item.trace_runs.slice(0, 3).map((trace) => (
                    <Button key={trace.trace_id} size="sm" variant="outline" asChild>
                      <a href="/#/knowledge-ops">{trace.trace_id}</a>
                    </Button>
                  ))}
                  {item.knowledge_tasks.slice(0, 3).map((task) => (
                    <Button key={task.id} size="sm" variant="ghost" asChild>
                      <a href="/#/knowledge-ops">{task.title}</a>
                    </Button>
                  ))}
                </div>
              </CardContent>
            </Card>
          ))}
          {assetMap.length === 0 ? <p className="text-sm text-muted-foreground">{t("assetGovernance.asset_map_empty")}</p> : null}
        </TabsContent>

        <TabsContent value="evals" className="grid gap-4 lg:grid-cols-[360px_1fr]">
          <Card><CardHeader><CardTitle className="text-sm">{t("assetGovernance.create_eval_set")}</CardTitle></CardHeader><CardContent className="space-y-3">
            <Input placeholder={t("assetGovernance.eval_name_placeholder")} value={evalForm.name} onChange={(event) => setEvalForm({ ...evalForm, name: event.target.value })} />
            <Input placeholder={t("assetGovernance.eval_app_type_placeholder")} value={evalForm.app_type} onChange={(event) => setEvalForm({ ...evalForm, app_type: event.target.value })} />
            <Textarea rows={6} value={evalForm.cases} onChange={(event) => setEvalForm({ ...evalForm, cases: event.target.value })} />
            <Button className="w-full" onClick={() => void createEvalSet()} disabled={busy}>{t("assetGovernance.create")}</Button>
          </CardContent></Card>
          <div className="space-y-3">
            {evalSets.map((item) => (<Card key={item.id}><CardContent className="flex items-center justify-between"><div><div className="font-medium">{item.name}</div><div className="text-xs text-muted-foreground">{item.app_type} · {item.item_count} {t("assetGovernance.eval_items_suffix")} · {item.status}</div></div><Button size="sm" variant="outline" onClick={() => void loadCases(item.id)}>{t("assetGovernance.view")}</Button></CardContent></Card>))}
            {selectedEvalSet ? <Card><CardHeader><CardTitle className="text-sm">{t("assetGovernance.eval_cases")}</CardTitle></CardHeader><CardContent className="space-y-2">{evalCases.map((item) => (<div key={item.id} className="rounded border p-2"><div className="text-sm">{item.question}</div><div className="text-xs text-muted-foreground">{item.source} · {item.expected_answer || t("assetGovernance.no_expected_answer")}</div></div>))}</CardContent></Card> : null}
          </div>
        </TabsContent>
      </Tabs>

      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}><DialogContent className="max-w-2xl"><DialogHeader><DialogTitle>{editingTemplate ? t("assetGovernance.save_template_version") : t("assetGovernance.new_template")}</DialogTitle></DialogHeader>
        <div className="grid gap-3">
          {!editingTemplate ? <Input placeholder={t("assetGovernance.template_key_placeholder")} value={templateForm.key} onChange={(event) => setTemplateForm({ ...templateForm, key: event.target.value })} /> : null}
          <Input placeholder={t("assetGovernance.template_name_placeholder")} value={templateForm.name} onChange={(event) => setTemplateForm({ ...templateForm, name: event.target.value })} />
          <Input placeholder={t("assetGovernance.template_app_types_placeholder")} value={templateForm.app_types} onChange={(event) => setTemplateForm({ ...templateForm, app_types: event.target.value })} />
          <Input placeholder={t("assetGovernance.template_desc_placeholder")} value={templateForm.description} onChange={(event) => setTemplateForm({ ...templateForm, description: event.target.value })} />
          <select className="rounded border bg-background px-2 py-1 text-sm" value={templateForm.status} onChange={(event) => setTemplateForm({ ...templateForm, status: event.target.value })}><option value="draft">{t("assetGovernance.status_draft")}</option><option value="published">{t("assetGovernance.status_published")}</option></select>
          <Textarea rows={8} value={templateForm.payload_json} onChange={(event) => setTemplateForm({ ...templateForm, payload_json: event.target.value })} />
          <Button onClick={() => void saveTemplate()} disabled={busy}>{busy ? <Loader2 className="size-4 animate-spin" /> : <ShieldCheck className="size-4" />} {t("assetGovernance.save")}</Button>
        </div>
      </DialogContent></Dialog>

      <Dialog open={!!lifecycleForm} onOpenChange={(open) => !open && setLifecycleForm(null)}><DialogContent><DialogHeader><DialogTitle>{t("assetGovernance.knowledge_lifecycle")}</DialogTitle></DialogHeader>
        {lifecycleForm ? <div className="grid gap-3">
          <label className="space-y-1 text-sm">
            <span>{t("assetGovernance.owner")}</span>
            <select
              className="w-full rounded border bg-background px-2 py-1 text-sm"
              aria-label={t("assetGovernance.owner")}
              value={lifecycleForm.owner_id}
              onChange={(event) => setLifecycleForm({ ...lifecycleForm, owner_id: event.target.value })}
            >
              <option value="">{t("assetGovernance.select_owner")}</option>
              {[...userRecords].filter((user, index, records) => records.findIndex((candidate) => candidate.id === user.id) === index).map((user) => (
                <option key={user.id} value={user.id}>{user.username}</option>
              ))}
              {lifecycleForm.owner_id && !userRecords.some((user) => user.id === lifecycleForm.owner_id) ? (
                <option value={lifecycleForm.owner_id}>{lifecycleForm.owner_id}{t("assetGovernance.current_value_suffix")}</option>
              ) : null}
            </select>
          </label>
          <label className="space-y-1 text-sm">
            <span>{t("assetGovernance.owner_team")}</span>
            <select
              className="w-full rounded border bg-background px-2 py-1 text-sm"
              aria-label={t("assetGovernance.owner_team")}
              value={lifecycleForm.owner_team_id}
              onChange={(event) => setLifecycleForm({ ...lifecycleForm, owner_team_id: event.target.value })}
            >
              <option value="">{t("assetGovernance.select_owner_team")}</option>
              {teamRecords.map((team) => (
                <option key={team.id} value={team.id}>{team.name}</option>
              ))}
              {lifecycleForm.owner_team_id && !teamRecords.some((team) => team.id === lifecycleForm.owner_team_id) ? (
                <option value={lifecycleForm.owner_team_id}>{lifecycleForm.owner_team_id}{t("assetGovernance.current_value_suffix")}</option>
              ) : null}
            </select>
          </label>
          <Input placeholder={t("assetGovernance.source_type_placeholder")} value={lifecycleForm.source_type} onChange={(event) => setLifecycleForm({ ...lifecycleForm, source_type: event.target.value })} />
          <Input placeholder={t("assetGovernance.business_domain_placeholder")} value={lifecycleForm.business_domain} onChange={(event) => setLifecycleForm({ ...lifecycleForm, business_domain: event.target.value })} />
          <Input placeholder={t("assetGovernance.sensitivity_placeholder")} value={lifecycleForm.sensitivity} onChange={(event) => setLifecycleForm({ ...lifecycleForm, sensitivity: event.target.value })} />
          <Input type="date" value={dateValue(lifecycleForm.expires_at)} onChange={(event) => setLifecycleForm({ ...lifecycleForm, expires_at: event.target.value ? new Date(event.target.value).toISOString() : undefined })} />
          <select className="rounded border bg-background px-2 py-1 text-sm" value={lifecycleForm.review_status} onChange={(event) => setLifecycleForm({ ...lifecycleForm, review_status: event.target.value })}><option value="none">{t("assetGovernance.lifecycle_none")}</option><option value="current">{t("assetGovernance.lifecycle_current")}</option><option value="due">{t("assetGovernance.lifecycle_due")}</option><option value="expired">{t("assetGovernance.lifecycle_expired")}</option></select>
          <NumericRangeField
            label={t("assetGovernance.quality_score")}
            value={lifecycleForm.quality_score}
            min={0}
            max={100}
            ariaLabel={t("assetGovernance.quality_score")}
            onChange={(next) => setLifecycleForm({ ...lifecycleForm, quality_score: next ?? 0 })}
          />
          <Button onClick={() => void saveLifecycle()} disabled={busy}>{t("assetGovernance.save")}</Button>
        </div> : null}
      </DialogContent></Dialog>
    </div>
  );
};

export const assetGovernance: ResourceProps = {
  name: "asset-governance",
  list: AssetGovernanceBoard,
  recordRepresentation: () => "资产治理",
  options: { label: "资产治理" },
  icon: Boxes,
};
