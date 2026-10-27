import { useEffect, useState, type ReactNode } from "react";
import { useNotify, useTranslate } from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Loader2, PlayCircle, RefreshCw } from "lucide-react";
import { api, ApiError, unwrap } from "../../lib/api";
import { NumericRangeField } from "@/components/NumericRangeField";

interface SyncSetting {
  enabled: boolean;
  scheduled_reconcile_enabled: boolean;
  resource_types: string[];
  interval_seconds: number;
  batch_size: number;
  max_resources: number;
  deletion_confirmations: number;
  default_target_tenant_id: string;
  default_owner_id: string;
}

interface TenantMapping {
  external_tenant_id: string;
  target_tenant_id: string;
}

interface SyncRun {
  id: string;
  trigger_type: string;
  status: string;
  scan_consistency: string;
  deletion_safe: boolean;
  plan_summary: string;
  result_summary: string;
  progress_total?: number;
  progress_done?: number;
  progress_failed?: number;
  error: string;
  created_at: string;
}

interface SyncRunItem {
  id: string;
  resource_type: string;
  external_tenant_id: string;
  external_id: string;
  local_id: string;
  tenant_id: string;
  owner_id: string;
  action: string;
  status: string;
  conflict_type: string;
  error: string;
  sync_state?: string;
  upstream_current_hash: string;
  upstream_last_synced_hash: string;
  local_current_hash: string;
  local_last_synced_hash: string;
  payload_diff: string;
}

interface ResourceBinding {
  id: string;
  resource_type: string;
  external_id: string;
  external_tenant_id: string;
  local_id: string;
  tenant_id: string;
  binding_lifecycle: string;
  governance_state: string;
  conflict_type: string;
  sync_state?: string;
}

interface BindingVersion {
  id: string;
  version: number;
  upstream_hash: string;
  source_credential_version: string;
  sync_run_id: string;
  created_at: string;
}

interface RelinkTenantOption {
  id: string;
  name: string;
  type: string;
  status: string;
}

interface RelinkResourceOption {
  tenant_id: string;
  local_id: string;
  name: string;
}

interface SyncUserOption {
  id: string;
  username: string;
  tenant_id: string;
  status: string;
}

const RESOURCE_TYPES = ["dataset", "chat", "agent", "search_app", "memory"];
type SyncTab = "overview" | "onboarding" | "runs" | "resources" | "settings";
const inputCls = "w-full rounded border bg-background px-2 py-1 text-sm";

const safeDiffKeys = new Set(["schema_version", "action", "conflict_type", "hashes"]);

function safeResourceDiff(raw: string): string {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw || "{}");
  } catch {
    return "{}";
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return "{}";
  const output: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(parsed)) {
    if (safeDiffKeys.has(key)) output[key] = value;
  }
  return JSON.stringify(output, null, 2);
}

type ResourceSyncSummary = Record<string, Record<string, number>>;

function parseResourceSyncSummary(raw: string): ResourceSyncSummary {
  try {
    const parsed = JSON.parse(raw || "{}");
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return {};
    return parsed as ResourceSyncSummary;
  } catch {
    return {};
  }
}

function summaryCount(summary: ResourceSyncSummary): number {
  return Object.values(summary).reduce((total, typeCounts) => (
    total + Object.entries(typeCounts).reduce((typeTotal, [key, count]) => (
      key === "mapping" || key === "create" || key === "update" || key === "conflict" || key === "relink"
        ? typeTotal + Number(count || 0)
        : typeTotal
    ), 0)
  ), 0);
}

function StatusChip({ tone, children }: { tone: "ok" | "info" | "warn" | "danger"; children: ReactNode }) {
  const styles = {
    ok: "bg-emerald-50 text-emerald-700",
    info: "bg-primary/10 text-primary",
    warn: "bg-amber-50 text-amber-700",
    danger: "bg-red-50 text-red-700",
  }[tone];
  return <span className={`inline-flex rounded-full px-2 py-0.5 text-xs font-medium ${styles}`}>{children}</span>;
}

function SummaryCard({ label, value, hint, children }: {
  label: string;
  value: ReactNode;
  hint?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <div className="rounded border bg-background p-3">
      <p className="text-xs text-muted-foreground">{label}</p>
      <div className="mt-1 text-sm font-medium">{value}</div>
      {hint && <p className="mt-1 text-xs text-muted-foreground">{hint}</p>}
      {children && <div className="mt-2">{children}</div>}
    </div>
  );
}

function EmptyHint({ children }: { children: ReactNode }) {
  return (
    <div className="rounded border border-dashed bg-muted/20 p-4 text-sm text-muted-foreground">
      {children}
    </div>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="space-y-1 text-sm">
      <span className="block">{label}</span>
      {children}
    </label>
  );
}

export const RagflowSyncPanel = () => {
  const t = useTranslate();
  const notify = useNotify();
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [reconciling, setReconciling] = useState(false);
  const [setting, setSetting] = useState<SyncSetting | null>(null);
  const [persistedSetting, setPersistedSetting] = useState<SyncSetting | null>(null);
  const [mappings, setMappings] = useState<TenantMapping[]>([]);
  const [runs, setRuns] = useState<SyncRun[]>([]);
  const [resources, setResources] = useState<ResourceBinding[]>([]);
  const [targetTenant, setTargetTenant] = useState("");
  const [selectedRunId, setSelectedRunId] = useState("");
  const [runItems, setRunItems] = useState<SyncRunItem[]>([]);
  const [runItemsLoading, setRunItemsLoading] = useState(false);
  const [runItemsPage, setRunItemsPage] = useState(1);
  const [runItemsTotal, setRunItemsTotal] = useState(0);
  const [versions, setVersions] = useState<BindingVersion[]>([]);
  const [selectedBindingId, setSelectedBindingId] = useState("");
  const [versionsLoading, setVersionsLoading] = useState(false);
  const [selectedRelinkResource, setSelectedRelinkResource] = useState<ResourceBinding | null>(null);
  const [relinkTenantScope, setRelinkTenantScope] = useState("current");
  const [relinkTenantQuery, setRelinkTenantQuery] = useState("");
  const [relinkTenantOptions, setRelinkTenantOptions] = useState<RelinkTenantOption[]>([]);
  const [workspaceOptions, setWorkspaceOptions] = useState<RelinkTenantOption[]>([]);
  const [relinkTenantID, setRelinkTenantID] = useState("");
  const [relinkResourceQuery, setRelinkResourceQuery] = useState("");
  const [relinkResourceOptions, setRelinkResourceOptions] = useState<RelinkResourceOption[]>([]);
  const [relinkLocalID, setRelinkLocalID] = useState("");
  const [selectedItemIDs, setSelectedItemIDs] = useState<string[]>([]);
  const [assigningItems, setAssigningItems] = useState(false);
  const [assignTenantID, setAssignTenantID] = useState("");
  const [assignOwnerID, setAssignOwnerID] = useState("");
  const [assignOwnerQuery, setAssignOwnerQuery] = useState("");
  const [assignOwnerOptions, setAssignOwnerOptions] = useState<SyncUserOption[]>([]);
  const [importingRun, setImportingRun] = useState(false);
  const [governingItemID, setGoverningItemID] = useState("");
  const [activeTab, setActiveTab] = useState<SyncTab>("overview");

  const activeRun = runs.find((run) => ["pending", "scanning", "planned", "running"].includes(run.status));
  const processingRun = runs.find((run) => ["scanning", "running"].includes(run.status));
  const selectedRun = runs.find((run) => run.id === selectedRunId);
  const selectedRunPlan = selectedRun ? parseResourceSyncSummary(selectedRun.plan_summary) : {};
  const selectedRunDetailMismatch = selectedRun?.status === "planned" && summaryCount(selectedRunPlan) > 0 && runItemsTotal === 0;
  const workspaceName = (workspaceID: string) =>
    relinkTenantOptions.find((workspace) => workspace.id === workspaceID)?.name ?? workspaceID;

  const runProgress = runItems.length > 0 ? {
    total: runItemsTotal,
    pending: runItems.filter((item) => item.status === "pending").length,
    succeeded: runItems.filter((item) => item.status === "succeeded").length,
    failed: runItems.filter((item) => item.status === "failed").length,
  } : null;

  const renderSummaryChips = (summary: ResourceSyncSummary) => {
    const entries = Object.entries(summary);
    if (entries.length === 0) return null;
    return (
      <div className="mt-1 flex flex-wrap gap-1">
        {entries.map(([resourceType, typeCounts]) => Object.entries(typeCounts).map(([action, count]) => (
          <span key={`${resourceType}-${action}`} className="rounded bg-muted px-1.5 py-0.5 text-xs">
            {t(`system.ragflow_sync_type_${resourceType}`)} · {action} · {count}
          </span>
        )))}
      </div>
    );
  };

  const errorMessage = (err: unknown, fallback: string) =>
    err instanceof ApiError ? err.displayMessage : fallback;

  const load = async (showLoading = false) => {
    if (showLoading) setLoading(true);
    try {
      const [settingRes, mappingRes, runRes, resourceRes] = await Promise.all([
        api.get<{ code: number; data: SyncSetting }>("/system/ragflow/sync/settings"),
        api.get<{ code: number; data: TenantMapping[] }>("/system/ragflow/sync/mappings"),
        api.get<{ code: number; data: { items: SyncRun[] } }>("/system/ragflow/sync/runs?page=1&page_size=10"),
        api.get<{ code: number; data: { items: ResourceBinding[] } }>("/system/ragflow/sync/resources?page=1&page_size=20"),
      ]);
      setSetting(settingRes.data?.data ?? null);
      setPersistedSetting(settingRes.data?.data ?? null);
      setMappings(mappingRes.data?.data ?? []);
      setRuns(runRes.data?.data?.items ?? []);
      setResources(resourceRes.data?.data?.items ?? []);
    } catch (err) {
      notify(errorMessage(err, t("system.ragflow_sync_load_fail")), { type: "error" });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => { void load(true); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, []);

  useEffect(() => {
    if (loading) return;
    const timer = window.setTimeout(() => { void loadRelinkTenantOptions(relinkTenantScope, relinkTenantQuery); }, 250);
    void loadWorkspaceOptions();
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loading, relinkTenantScope, relinkTenantQuery]);

  useEffect(() => {
    if (loading || !selectedRelinkResource || !relinkTenantID) {
      setRelinkResourceOptions([]);
      return;
    }
    const timer = window.setTimeout(async () => {
      try {
        const query = new URLSearchParams({
          type: selectedRelinkResource.resource_type,
          scope: relinkTenantScope,
          query: relinkResourceQuery,
          page: "1",
          page_size: "100",
        });
        if (relinkTenantScope !== "current") {
          query.set("tenant_id", relinkTenantID);
        }
        const response = await api.get<{ code: number; data: { items: RelinkResourceOption[] } }>(
          `/system/ragflow/sync/relink/resource-options?${query.toString()}`,
        );
        setRelinkResourceOptions(response.data?.data?.items ?? []);
      } catch (err) {
        notify(errorMessage(err, t("system.ragflow_sync_action_fail")), { type: "error" });
      }
    }, 250);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loading, selectedRelinkResource, relinkTenantScope, relinkTenantID, relinkResourceQuery]);

  useEffect(() => {
    if (loading) return;
    const preferredRun = activeRun ?? runs[0];
    if (preferredRun?.id && preferredRun.id !== selectedRunId) {
      void loadRunItems(preferredRun.id);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loading, runs, selectedRunId]);

  // Poll while a run is active so run-level progress and item states advance
  // in near-real time without a manual refresh.
  useEffect(() => {
    if (loading || !processingRun) return;
    const timer = window.setInterval(async () => {
      await load();
      if (processingRun.id === selectedRunId) {
        await loadRunItems(processingRun.id, true, runItemsPage);
      }
    }, 5000);
    return () => window.clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loading, processingRun?.id]);

  const save = async () => {
    if (!setting) return false;
    setSaving(true);
    try {
      await unwrap(api.patch("/system/ragflow/sync/settings", setting));
      setPersistedSetting(setting);
      notify(t("system.ragflow_sync_saved"), { type: "success" });
      await load();
      return true;
    } catch (err) {
      notify(errorMessage(err, t("system.ragflow_sync_save_fail")), { type: "error" });
      return false;
    } finally {
      setSaving(false);
    }
  };

  const ensureSettingsSaved = async () => {
    if (!setting) return false;
    if (!persistedSetting || JSON.stringify(setting) !== JSON.stringify(persistedSetting)) {
      return save();
    }
    return true;
  };

  const scan = async () => {
    setScanning(true);
    try {
      if (!(await ensureSettingsSaved())) return;
      const response = await unwrap(api.post("/system/ragflow/sync/scan", { resource_types: setting?.resource_types ?? [] }));
      const run = response as SyncRun | undefined;
      if (run?.id) setSelectedRunId(run.id);
      notify(t("system.ragflow_sync_scan_started"), { type: "success" });
      await load();
      if (run?.id) await loadRunItems(run.id);
    } catch (err) {
      notify(errorMessage(err, t("system.ragflow_sync_action_fail")), { type: "error" });
    } finally {
      setScanning(false);
    }
  };

  const reconcile = async () => {
    setReconciling(true);
    try {
      if (!(await ensureSettingsSaved())) return;
      const run = await unwrap(api.post("/system/ragflow/sync/reconcile", { resource_types: setting?.resource_types ?? [] })) as SyncRun | undefined;
      if (run?.id) setSelectedRunId(run.id);
      notify(t("system.ragflow_sync_reconcile_started"), { type: "success" });
      await load();
      if (run?.id) await loadRunItems(run.id);
    } catch (err) {
      notify(errorMessage(err, t("system.ragflow_sync_action_fail")), { type: "error" });
    } finally {
      setReconciling(false);
    }
  };

  const loadRunItems = async (runID: string, preserveSelection = false, page = 1) => {
    setSelectedRunId(runID);
    setRunItemsPage(page);
    if (!preserveSelection) {
      setSelectedItemIDs([]);
    }
    setRunItemsLoading(true);
    try {
      const response = await api.get<{ code: number; data: { items: SyncRunItem[]; total: number } }>(
        `/system/ragflow/sync/runs/${runID}/items?page=${page}&page_size=100`,
      );
      setRunItems(response.data?.data?.items ?? []);
      setRunItemsTotal(response.data?.data?.total ?? 0);
    } catch (err) {
      notify(errorMessage(err, t("system.ragflow_sync_action_fail")), { type: "error" });
    } finally {
      setRunItemsLoading(false);
    }
  };

  const importRun = async () => {
    if (!selectedRunId) return;
    setImportingRun(true);
    try {
      await unwrap(api.post(`/system/ragflow/sync/runs/${selectedRunId}/import`, { async: true }));
      notify(t("system.ragflow_sync_import_started"), { type: "success" });
      await Promise.all([load(), loadRunItems(selectedRunId, true)]);
    } catch (err) {
      notify(errorMessage(err, t("system.ragflow_sync_action_fail")), { type: "error" });
    } finally {
      setImportingRun(false);
    }
  };

  const toggleSelectedItem = (itemID: string) => {
    setSelectedItemIDs((current) => current.includes(itemID)
      ? current.filter((value) => value !== itemID)
      : [...current, itemID]);
  };

  const assignSelectedItems = async () => {
    if (!selectedRunId || selectedItemIDs.length === 0 || !assignTenantID) return;
    setAssigningItems(true);
    try {
      await unwrap(api.post(`/system/ragflow/sync/runs/${selectedRunId}/assign`, {
        item_ids: selectedItemIDs, tenant_id: assignTenantID, owner_id: assignOwnerID || undefined,
      }));
      notify(t("system.ragflow_sync_items_assigned"), { type: "success" });
      setSelectedItemIDs([]);
      setAssignOwnerID("");
      await Promise.all([load(), loadRunItems(selectedRunId)]);
    } catch (err) {
      notify(errorMessage(err, t("system.ragflow_sync_action_fail")), { type: "error" });
    } finally {
      setAssigningItems(false);
    }
  };

  const resolveConflict = async (item: SyncRunItem, resolution: "use_upstream" | "use_local" | "manual_merge") => {
    setGoverningItemID(item.id);
    try {
      await unwrap(api.post(
        `/system/ragflow/sync/resources/${item.resource_type}/${item.external_tenant_id}/${item.external_id}/conflicts/resolve`,
        { item_id: item.id, resolution },
      ));
      notify(t("system.ragflow_sync_conflict_resolved"), { type: "success" });
      await Promise.all([load(), loadRunItems(selectedRunId)]);
    } catch (err) {
      notify(errorMessage(err, t("system.ragflow_sync_action_fail")), { type: "error" });
    } finally {
      setGoverningItemID("");
    }
  };

  const selectRelinkResource = (resource: ResourceBinding) => {
    setSelectedRelinkResource(resource);
    setRelinkTenantID(relinkTenantOptions[0]?.id ?? "");
    setRelinkLocalID("");
    setRelinkResourceQuery("");
    setRelinkResourceOptions([]);
  };

  const loadRelinkTenantOptions = async (scope: string, query: string) => {
    try {
      const response = await api.get<{ code: number; data: { items: RelinkTenantOption[] } }>(
        `/system/ragflow/sync/relink/tenant-options?scope=${encodeURIComponent(scope)}&query=${encodeURIComponent(query)}&page=1&page_size=100`,
      );
      const options = response.data?.data?.items ?? [];
      setRelinkTenantOptions(options);
      if (options.length > 0 && !options.some((option) => option.id === relinkTenantID)) {
        setRelinkTenantID(options[0].id);
      } else if (options.length === 0) {
        setRelinkTenantID("");
      }
    } catch (err) {
      notify(errorMessage(err, t("system.ragflow_sync_action_fail")), { type: "error" });
    }
  };

  const loadWorkspaceOptions = async () => {
    try {
      const response = await api.get<{ code: number; data: { items: RelinkTenantOption[] } }>(
        "/system/ragflow/sync/relink/tenant-options?scope=all&query=&page=1&page_size=100",
      );
      const options = response.data?.data?.items ?? [];
      setWorkspaceOptions(options);
      setTargetTenant((current) => (options.some((option) => option.id === current) ? current : ""));
      setAssignTenantID((current) => (options.some((option) => option.id === current) ? current : options[0]?.id ?? ""));
    } catch (err) {
      notify(errorMessage(err, t("system.ragflow_sync_action_fail")), { type: "error" });
    }
  };

  const changeRelinkScope = (scope: string) => {
    setRelinkTenantScope(scope);
    setRelinkTenantID("");
    setRelinkLocalID("");
    setRelinkResourceOptions([]);
  };

  useEffect(() => {
    if (!assignTenantID) {
      setAssignOwnerID("");
      setAssignOwnerOptions([]);
      return;
    }
    let cancelled = false;
    // Server-side username search keeps the candidate list bounded on large
    // tenants instead of relying on a fixed page_size=200 snapshot.
    const params = new URLSearchParams({
      scope: "all",
      page: "1",
      page_size: "50",
      status: "active",
    });
    if (assignOwnerQuery.trim()) {
      params.set("username", assignOwnerQuery.trim());
    }
    api.get<{ code: number; data: { items: SyncUserOption[] } }>(`/users?${params.toString()}`)
      .then((response) => {
        if (cancelled) return;
        const users = (response.data?.data?.items ?? []).filter(
          (user) => user.tenant_id === assignTenantID && user.status === "active",
        );
        setAssignOwnerOptions(users);
        if (!users.some((user) => user.id === assignOwnerID)) {
          setAssignOwnerID("");
        }
      })
      .catch(() => {
        if (!cancelled) setAssignOwnerOptions([]);
      });
    return () => {
      cancelled = true;
    };
  }, [assignTenantID, assignOwnerQuery, assignOwnerID]);

  const relinkResource = async () => {
    if (!selectedRelinkResource || !relinkTenantID || !relinkLocalID) return;
    try {
      await unwrap(api.post(
        `/system/ragflow/sync/resources/${selectedRelinkResource.resource_type}/${selectedRelinkResource.external_tenant_id}/${selectedRelinkResource.external_id}/relink`,
        { scope: relinkTenantScope, tenant_id: relinkTenantID, local_id: relinkLocalID },
      ));
      setSelectedRelinkResource(null);
      setRelinkTenantID("");
      setRelinkLocalID("");
      setRelinkResourceOptions([]);
      notify(t("system.ragflow_sync_relinked"), { type: "success" });
      await load();
    } catch (err) {
      notify(errorMessage(err, t("system.ragflow_sync_action_fail")), { type: "error" });
    }
  };

  const loadVersions = async (resource: ResourceBinding) => {
    setSelectedBindingId(resource.id);
    setVersionsLoading(true);
    try {
      const response = await api.get<{ code: number; data: { items: BindingVersion[] } }>(
        `/system/ragflow/sync/resources/${resource.resource_type}/${resource.external_tenant_id}/${resource.external_id}/versions?page=1&page_size=50`,
      );
      setVersions(response.data?.data?.items ?? []);
    } catch (err) {
      notify(errorMessage(err, t("system.ragflow_sync_action_fail")), { type: "error" });
    } finally {
      setVersionsLoading(false);
    }
  };

  const upsertMapping = async () => {
    if (!targetTenant) return;
    try {
      await unwrap(api.post("/system/ragflow/sync/mappings", {
        external_tenant_id: "__GLOBAL__",
        target_tenant_id: targetTenant,
      }));
      setTargetTenant("");
      await load();
    } catch (err) {
      notify(errorMessage(err, t("system.ragflow_sync_action_fail")), { type: "error" });
    }
  };

  const toggleType = (type: string) => {
    if (!setting) return;
    const exists = setting.resource_types.includes(type);
    const resourceTypes = exists
      ? setting.resource_types.filter((value) => value !== type)
      : [...setting.resource_types, type];
    setSetting({ ...setting, resource_types: resourceTypes });
  };

  if (loading || !setting) {
    return (
      <div className="flex items-center gap-2 p-4 text-sm text-muted-foreground">
        <Loader2 className="size-4 animate-spin" aria-hidden="true" />
        {t("ra.page.loading")}
      </div>
    );
  }

  return (
    <section className="mt-4 rounded border p-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div>
          <h3 className="text-sm font-medium">{t("system.ragflow_sync_title")}</h3>
          <p className="mt-1 text-xs text-muted-foreground">
            {processingRun
              ? t("system.ragflow_sync_processing")
              : activeRun
                ? t("system.ragflow_sync_active_run")
                : t("system.ragflow_sync_subtitle")}
          </p>
        </div>
        <div className="flex gap-2">
          <Button size="sm" variant="outline" onClick={() => void load()}>
            <RefreshCw className="size-4" aria-hidden="true" />
            {t("system.refresh")}
          </Button>
          <Button size="sm" variant="outline" onClick={() => void scan()} disabled={scanning || Boolean(activeRun)}>
            {scanning ? <Loader2 className="size-4 animate-spin" /> : <PlayCircle className="size-4" />}
            {t("system.ragflow_sync_scan")}
          </Button>
          <Button size="sm" variant="outline" onClick={() => void reconcile()} disabled={reconciling || Boolean(activeRun)}>
            {reconciling ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
            {t("system.ragflow_sync_reconcile")}
          </Button>
          <Button size="sm" onClick={() => void importRun()} disabled={!selectedRunId || importingRun || activeRun?.status === "running" || selectedRunDetailMismatch}>
            {importingRun ? <Loader2 className="size-4 animate-spin" /> : <PlayCircle className="size-4" />}
            {t("system.ragflow_sync_import")}
          </Button>
        </div>
      </div>

      <div className="mb-4 flex flex-wrap gap-1 rounded border bg-muted/20 p-1" role="tablist" aria-label={t("system.ragflow_sync_title")}>
        {([
          ["overview", t("system.ragflow_sync_tab_overview")],
          ["onboarding", t("system.ragflow_sync_tab_onboarding")],
          ["runs", t("system.ragflow_sync_tab_runs")],
          ["resources", t("system.ragflow_sync_tab_resources")],
          ["settings", t("system.ragflow_sync_tab_settings")],
        ] as [SyncTab, string][]).map(([tab, label]) => (
          <button
            key={tab}
            type="button"
            role="tab"
            aria-selected={activeTab === tab}
            className={`rounded px-3 py-1.5 text-sm ${activeTab === tab ? "bg-background font-medium shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
            onClick={() => setActiveTab(tab)}
          >
            {label}
          </button>
        ))}
      </div>

      {activeTab === "overview" && (
        <div className="space-y-4">
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
            <SummaryCard
              label={t("system.ragflow_sync_sync_status")}
              value={<StatusChip tone={setting.enabled ? "ok" : "warn"}>{setting.enabled ? t("system.ragflow_sync_sync_enabled") : t("system.ragflow_sync_sync_disabled")}</StatusChip>}
              hint={setting.scheduled_reconcile_enabled ? t("system.ragflow_sync_scheduled_reconcile_enabled") : t("system.ragflow_sync_reconcile_off")}
            />
            <SummaryCard
              label={t("system.ragflow_sync_current_run")}
              value={processingRun ? `${processingRun.trigger_type} · ${processingRun.status}` : activeRun ? `${activeRun.trigger_type} · ${activeRun.status}` : t("system.ragflow_sync_no_run")}
            >
              {selectedRun && renderSummaryChips(selectedRunPlan)}
            </SummaryCard>
            <SummaryCard
              label={t("system.ragflow_sync_default_mapping")}
              value={mappings[0] ? workspaceName(mappings[0].target_tenant_id) : t("system.ragflow_sync_mapping_unset")}
              hint={mappings[0] ? undefined : t("system.ragflow_sync_mapping_required")}
            />
            <SummaryCard
              label={t("system.ragflow_sync_bound_resources")}
              value={resources.length}
              hint={`${runs.length} ${t("system.ragflow_sync_runs")}`}
            />
          </div>
          <EmptyHint>
            <div className="font-medium text-foreground">{t("system.ragflow_sync_quick_start")}</div>
            <p className="mt-1">{t("system.ragflow_sync_quick_start_detail")}</p>
            <div className="mt-3 flex flex-wrap gap-2">
              <Button size="sm" onClick={() => setActiveTab("onboarding")}>{t("system.ragflow_sync_tab_onboarding")}</Button>
              <Button size="sm" variant="outline" onClick={() => void scan()} disabled={scanning || Boolean(activeRun)}>
                {scanning ? <Loader2 className="size-4 animate-spin" /> : <PlayCircle className="size-4" />}
                {t("system.ragflow_sync_scan")}
              </Button>
              <Button size="sm" variant="outline" onClick={() => setActiveTab("runs")}>{t("system.ragflow_sync_tab_runs")}</Button>
            </div>
          </EmptyHint>
        </div>
      )}

      {activeTab === "onboarding" && (
        <section aria-label={t("system.ragflow_sync_wizard_title")} className="mb-4 rounded border bg-muted/30 p-3">
          <h4 className="text-sm font-medium">{t("system.ragflow_sync_wizard_title")}</h4>
          <ol className="mt-2 list-decimal space-y-1 pl-5 text-xs text-muted-foreground">
            <li>{t("system.ragflow_sync_wizard_step1")}</li>
            <li>{t("system.ragflow_sync_wizard_step2")}</li>
            <li>{t("system.ragflow_sync_wizard_step3")}</li>
            <li>{t("system.ragflow_sync_wizard_step4")}</li>
            <li>{t("system.ragflow_sync_wizard_step5")}</li>
          </ol>
        </section>
      )}

      {activeTab === "settings" && (
      <>
      <div className="grid gap-3 md:grid-cols-4">
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" className="size-4" checked={setting.enabled} onChange={(event) => setSetting({ ...setting, enabled: event.target.checked })} />
          {t("system.ragflow_sync_enabled")}
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" className="size-4" checked={setting.scheduled_reconcile_enabled} onChange={(event) => setSetting({ ...setting, scheduled_reconcile_enabled: event.target.checked })} />
          {t("system.ragflow_sync_scheduled_reconcile_enabled")}
        </label>
        <NumericRangeField
          label={t("system.ragflow_sync_interval")}
          value={setting.interval_seconds}
          min={60}
          max={86400}
          unit="s"
          onChange={(next) => setSetting({ ...setting, interval_seconds: next ?? 900 })}
        />
        <NumericRangeField
          label={t("system.ragflow_sync_batch")}
          value={setting.batch_size}
          min={10}
          max={500}
          onChange={(next) => setSetting({ ...setting, batch_size: next ?? 100 })}
        />
        <NumericRangeField
          label={t("system.ragflow_sync_deletions")}
          value={setting.deletion_confirmations}
          min={2}
          max={10}
          onChange={(next) => setSetting({ ...setting, deletion_confirmations: next ?? 3 })}
        />
      </div>

      <div className="mt-3 flex flex-wrap gap-3 text-sm">
        {RESOURCE_TYPES.map((type) => (
          <label key={type} className="flex items-center gap-2">
            <input type="checkbox" className="size-4" checked={setting.resource_types.includes(type)} onChange={() => toggleType(type)} />
            {t(`system.ragflow_sync_type_${type}`)}
          </label>
        ))}
      </div>

      </>
      )}

      {activeTab === "onboarding" && (
      <div className="mt-4">
        <div>
          <h4 className="mb-2 text-sm font-medium">{t("system.ragflow_sync_default_mapping")}</h4>
          <div className="flex gap-2">
            <Select value={targetTenant} onValueChange={setTargetTenant}>
              <SelectTrigger aria-label={t("system.ragflow_sync_target_workspace")}>
                <SelectValue placeholder={t("system.ragflow_sync_select_workspace")} />
              </SelectTrigger>
              <SelectContent>
                {workspaceOptions.map((tenant) => (
                  <SelectItem key={tenant.id} value={tenant.id}>{tenant.name}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button variant="outline" aria-label={t("system.ragflow_sync_save_mapping")} onClick={() => void upsertMapping()} disabled={!targetTenant}>{t("system.save")}</Button>
          </div>
          {workspaceOptions.length === 0 && <p className="mt-2 text-xs text-muted-foreground">{t("system.ragflow_sync_create_workspace_hint")}</p>}
          <ul className="mt-2 space-y-1 text-sm">
            {mappings.map((mapping) => (
              <li key={mapping.external_tenant_id} className="rounded border px-2 py-1">
                {t("system.ragflow_sync_global_resources")} → {workspaceName(mapping.target_tenant_id)}
              </li>
            ))}
          </ul>
          {runs.length === 0 && <EmptyHint>{t("system.ragflow_sync_no_run")}</EmptyHint>}
        </div>

      </div>
      )}

      {activeTab === "runs" && (
      <>
        <div>
          <h4 className="mb-2 text-sm font-medium">{t("system.ragflow_sync_runs")}</h4>
          <ul className="space-y-1 text-sm">
            {runs.map((run) => (
              <li key={run.id} className={`flex items-center justify-between gap-2 rounded border px-2 py-1 ${run.id === selectedRunId ? "border-primary bg-muted/50" : ""}`}>
                <span className="min-w-0 flex-1">
                  <span className="font-medium">{run.trigger_type}</span> · {run.status} · {run.scan_consistency}
                  {typeof run.progress_total === "number" && run.progress_total > 0 && run.status === "running" && (
                    <span className="ml-2 rounded bg-muted px-1.5 py-0.5 text-xs">
                      {t("system.ragflow_sync_run_progress_bar", {
                        done: run.progress_done ?? 0,
                        total: run.progress_total,
                        failed: run.progress_failed ?? 0,
                      })}
                    </span>
                  )}
                  {run.id === selectedRunId && <span className="ml-2 rounded bg-primary/10 px-1.5 py-0.5 text-xs">{t("system.ragflow_sync_selected_run")}</span>}
                  {renderSummaryChips(parseResourceSyncSummary(run.plan_summary))}
                  {renderSummaryChips(parseResourceSyncSummary(run.result_summary))}
                  {run.error && (
                    <div className="mt-1 text-xs text-red-600">
                      {t("system.ragflow_sync_failed_reason")}: {run.error}
                    </div>
                  )}
                </span>
                <Button size="sm" variant="outline" onClick={() => void loadRunItems(run.id)}>
                  {t("system.ragflow_sync_view_items")}
                </Button>
              </li>
            ))}
          </ul>
        </div>

      {selectedRunId && (
        <div className="mt-4">
          <div className="mb-2 grid gap-2 rounded border p-3 md:grid-cols-4">
            <Field label={t("system.ragflow_sync_target_tenant")}>
              <Select value={assignTenantID} onValueChange={setAssignTenantID}>
                <SelectTrigger aria-label={t("system.ragflow_sync_target_tenant")}>
                  <SelectValue placeholder={t("common.empty")} />
                </SelectTrigger>
                <SelectContent>
                  {workspaceOptions.map((tenant) => (
                    <SelectItem key={tenant.id} value={tenant.id}>{tenant.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label={t("system.ragflow_sync_owner")}>
              <Input
                aria-label={t("system.ragflow_sync_owner_search")}
                placeholder={t("system.ragflow_sync_owner_search")}
                value={assignOwnerQuery}
                onChange={(event) => setAssignOwnerQuery(event.target.value)}
              />
              <select
                className={`${inputCls} mt-1`}
                aria-label={t("system.ragflow_sync_owner")}
                value={assignOwnerID}
                onChange={(event) => setAssignOwnerID(event.target.value)}
              >
                <option value="">{t("common.empty")}</option>
                {assignOwnerOptions.map((user) => (
                  <option key={user.id} value={user.id}>{user.username}</option>
                ))}
              </select>
            </Field>
            <div className="flex items-end">
              <Button size="sm" disabled={!assignTenantID || selectedItemIDs.length === 0 || assigningItems} onClick={() => void assignSelectedItems()}>
                {t("system.ragflow_sync_assign")}
              </Button>
            </div>
            <div className="flex items-end text-xs text-muted-foreground">
              {selectedItemIDs.length > 0 ? t("system.ragflow_sync_selected", { count: selectedItemIDs.length }) : t("system.ragflow_sync_select_create_items")}
            </div>
          </div>
          <h4 className="mb-2 text-sm font-medium">{t("system.ragflow_sync_run_items")}</h4>
          {runProgress && (
            <p className="mb-2 text-xs text-muted-foreground">
              {t("system.ragflow_sync_run_progress", {
                total: runProgress.total,
                pending: runProgress.pending,
                succeeded: runProgress.succeeded,
                failed: runProgress.failed,
              })}
            </p>
          )}
          {selectedRun?.status === "planned" && summaryCount(selectedRunPlan) > 0 && runItemsTotal === 0 && (
            <div className="mb-2 rounded border border-amber-200 bg-amber-50 p-3 text-sm text-amber-700">
              {t("system.ragflow_sync_plan_item_mismatch")}
            </div>
          )}
          {runItemsLoading ? (
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" aria-hidden="true" />
              {t("ra.page.loading")}
            </div>
          ) : runItems.length === 0 ? (
            <div className="text-sm text-muted-foreground">
              {selectedRun?.status === "failed" && selectedRun.error
                ? `${t("system.ragflow_sync_failed_reason")}: ${selectedRun.error}`
                : selectedRun?.status === "planned" && summaryCount(selectedRunPlan) === 0
                  ? t("system.ragflow_sync_no_plan_items")
                  : t("common.empty")}
            </div>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead className="text-xs text-muted-foreground">
                  <tr>
                    <th className="py-2">{t("system.ragflow_sync_action")}</th>
                    <th className="py-2">{t("system.ragflow_sync_status")}</th>
                    <th className="py-2">{t("system.ragflow_sync_conflict")}</th>
                    <th className="py-2">{t("system.ragflow_sync_state")}</th>
                    <th className="py-2">{t("system.ragflow_sync_hashes")}</th>
                    <th className="py-2">{t("system.ragflow_sync_diff")}</th>
                    <th className="py-2">{t("system.actions")}</th>
                    <th className="py-2">{t("system.ragflow_sync_select")}</th>
                  </tr>
                </thead>
                <tbody>
                  {runItems.map((item) => (
                    <tr key={item.id} className="border-t align-top">
                      <td className="py-2">{item.resource_type} · {item.action}</td>
                      <td className="py-2">{item.status}{item.error ? `: ${item.error}` : ""}</td>
                      <td className="py-2">{item.conflict_type}</td>
                      <td className="py-2">{item.sync_state}</td>
                      <td className="py-2 font-mono text-xs text-muted-foreground">
                        <div>{t("system.ragflow_sync_upstream")}: {(item.upstream_last_synced_hash || "").slice(0, 8)} → {(item.upstream_current_hash || "").slice(0, 8)}</div>
                        <div>{t("system.ragflow_sync_local")}: {(item.local_last_synced_hash || "").slice(0, 8)} → {(item.local_current_hash || "").slice(0, 8)}</div>
                      </td>
                      <td className="max-w-96 py-2">
                        <pre className="max-h-32 overflow-auto rounded bg-muted p-2 text-xs">
                          {safeResourceDiff(item.payload_diff || "{}")}
                        </pre>
                      </td>
                      <td className="py-2">
                        {item.conflict_type !== "none" && (
                          <div className="flex flex-wrap gap-1">
                            <Button size="sm" variant="outline" disabled={governingItemID === item.id || item.conflict_type !== "content"} onClick={() => void resolveConflict(item, "use_upstream")}>
                              {t("system.ragflow_sync_use_upstream")}
                            </Button>
                            <Button size="sm" variant="outline" disabled={governingItemID === item.id || item.conflict_type !== "content"} onClick={() => void resolveConflict(item, "use_local")}>
                              {t("system.ragflow_sync_use_local")}
                            </Button>
                            <Button size="sm" variant="outline" disabled={governingItemID === item.id || item.conflict_type !== "content"} onClick={() => void resolveConflict(item, "manual_merge")}>
                              {t("system.ragflow_sync_manual_merge")}
                            </Button>
                          </div>
                        )}
                      </td>
                      <td className="py-2">
                        <input
                          type="checkbox"
                          className="size-4"
                          aria-label={`${t("system.ragflow_sync_select")} ${item.resource_type}/${item.external_id}`}
                          checked={selectedItemIDs.includes(item.id)}
                          disabled={item.action !== "create" || item.status !== "pending"}
                          onChange={() => toggleSelectedItem(item.id)}
                        />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {runItemsTotal > 100 && (
            <div className="mt-2 flex items-center justify-end gap-2 text-sm">
              <Button
                size="sm"
                variant="outline"
                disabled={runItemsPage <= 1 || runItemsLoading}
                onClick={() => void loadRunItems(selectedRunId, true, runItemsPage - 1)}
              >
                {t("ra.navigation.previous")}
              </Button>
              <span className="text-xs text-muted-foreground">
                {t("system.page", { page: runItemsPage, total_pages: Math.ceil(runItemsTotal / 100) })}
              </span>
              <Button
                size="sm"
                variant="outline"
                disabled={runItemsPage * 100 >= runItemsTotal || runItemsLoading}
                onClick={() => void loadRunItems(selectedRunId, true, runItemsPage + 1)}
              >
                {t("ra.navigation.next")}
              </Button>
            </div>
          )}
        </div>
      )}
        </>
      )}

      {activeTab === "resources" && (
      <div className="mt-4 overflow-x-auto">
        <div className="mb-3 rounded border p-3">
          <div className="mb-2 text-sm font-medium">
            {selectedRelinkResource
              ? `${t("system.ragflow_sync_relink_wizard")}: ${selectedRelinkResource.resource_type}/${selectedRelinkResource.external_id}`
              : t("system.ragflow_sync_relink_wizard")}
          </div>
          <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-4">
            <Field label={t("system.ragflow_sync_relink_scope")}>
              <Select value={relinkTenantScope} onValueChange={changeRelinkScope}>
                <SelectTrigger aria-label={t("system.ragflow_sync_relink_scope")}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="current">{t("system.ragflow_sync_relink_scope_current")}</SelectItem>
                  <SelectItem value="specific">{t("system.ragflow_sync_relink_scope_specific")}</SelectItem>
                  <SelectItem value="all">{t("system.ragflow_sync_relink_scope_all")}</SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field label={t("system.ragflow_sync_relink_tenant")}>
              <Select value={relinkTenantID} onValueChange={setRelinkTenantID} disabled={relinkTenantOptions.length === 0}>
                <SelectTrigger aria-label={t("system.ragflow_sync_relink_tenant")}>
                  <SelectValue placeholder={t("common.empty")} />
                </SelectTrigger>
                <SelectContent>
                  {relinkTenantOptions.map((tenant) => (
                    <SelectItem key={tenant.id} value={tenant.id}>{tenant.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label={t("system.ragflow_sync_relink_tenant_search")}>
              <Input
                className={inputCls}
                aria-label={t("system.ragflow_sync_relink_tenant_search")}
                value={relinkTenantQuery}
                onChange={(event) => setRelinkTenantQuery(event.target.value)}
              />
            </Field>
            <Field label={t("system.ragflow_sync_relink_resource_search")}>
              <Input
                className={inputCls}
                aria-label={t("system.ragflow_sync_relink_resource_search")}
                value={relinkResourceQuery}
                disabled={!selectedRelinkResource || !relinkTenantID}
                onChange={(event) => setRelinkResourceQuery(event.target.value)}
              />
            </Field>
          </div>
          <div className="mt-2 grid gap-2 md:grid-cols-2">
            <Field label={t("system.ragflow_sync_relink_local")}>
              <Select value={relinkLocalID} onValueChange={setRelinkLocalID} disabled={!selectedRelinkResource || !relinkTenantID || relinkResourceOptions.length === 0}>
                <SelectTrigger aria-label={t("system.ragflow_sync_relink_local")}>
                  <SelectValue placeholder={t("common.empty")} />
                </SelectTrigger>
                <SelectContent>
                  {relinkResourceOptions.map((option) => (
                    <SelectItem key={`${option.tenant_id}:${option.local_id}`} value={option.local_id}>
                      {option.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <div className="flex items-end">
              <Button size="sm" disabled={!selectedRelinkResource || !relinkTenantID || !relinkLocalID} onClick={() => void relinkResource()}>
                {t("system.ragflow_sync_relink")}
              </Button>
            </div>
          </div>
        </div>
        <table className="w-full text-left text-sm">
          <thead className="text-xs text-muted-foreground">
            <tr>
              <th className="py-2">{t("system.ragflow_sync_resource_type")}</th>
                    <th className="py-2">{t("system.ragflow_sync_external_id")}</th>
                    <th className="py-2">{t("system.ragflow_sync_local_id")}</th>
              <th className="py-2">{t("system.ragflow_sync_lifecycle")}</th>
              <th className="py-2">{t("system.ragflow_sync_governance")}</th>
              <th className="py-2">{t("system.ragflow_sync_conflict")}</th>
              <th className="py-2">{t("system.ragflow_sync_state")}</th>
              <th className="py-2">{t("system.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {resources.map((resource) => (
              <tr key={`${resource.resource_type}:${resource.external_tenant_id}:${resource.external_id}`} className="border-t">
                <td className="py-2">{resource.resource_type}</td>
                <td className="py-2 text-muted-foreground">{resource.external_id}</td>
                <td className="py-2 text-muted-foreground">{resource.local_id}</td>
                <td className="py-2">{resource.binding_lifecycle}</td>
                <td className="py-2">{resource.governance_state}</td>
                <td className="py-2">{resource.conflict_type}</td>
                <td className="py-2">{resource.sync_state}</td>
                <td className="py-2">
                  <div className="flex gap-1">
                    <Button size="sm" variant="outline" onClick={() => void loadVersions(resource)}>
                      {t("system.ragflow_sync_versions")}
                    </Button>
                    <Button size="sm" variant="outline" onClick={() => selectRelinkResource(resource)}>
                      {t("system.ragflow_sync_select_relink")}
                    </Button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {resources.length === 0 && <EmptyHint>{t("system.ragflow_sync_no_bindings")}</EmptyHint>}
      </div>
      )}

      {selectedBindingId && (
        <div className="mt-4">
          <h4 className="mb-2 text-sm font-medium">{t("system.ragflow_sync_versions")}</h4>
          {versionsLoading ? (
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" aria-hidden="true" />
              {t("ra.page.loading")}
            </div>
          ) : versions.length === 0 ? (
            <div className="text-sm text-muted-foreground">{t("common.empty")}</div>
          ) : (
            <table className="w-full text-left text-sm">
              <thead className="text-xs text-muted-foreground">
                <tr>
                  <th className="py-2">{t("system.ragflow_sync_version")}</th>
                  <th className="py-2">{t("system.ragflow_sync_upstream_hash")}</th>
                  <th className="py-2">{t("system.ragflow_sync_credential")}</th>
                  <th className="py-2">{t("system.ragflow_sync_run")}</th>
                </tr>
              </thead>
              <tbody>
                {versions.map((version) => (
                  <tr key={version.id} className="border-t">
                    <td className="py-2">{version.version}</td>
                    <td className="py-2 font-mono text-xs text-muted-foreground">{version.upstream_hash}</td>
                    <td className="py-2">{version.source_credential_version}</td>
                    <td className="py-2 text-muted-foreground">{version.sync_run_id}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

      <div className="mt-4 flex justify-end">
        <Button onClick={() => void save()} disabled={saving}>
          {saving ? <Loader2 className="size-4 animate-spin" /> : null}
          {t("system.ragflow_sync_save")}
        </Button>
      </div>
    </section>
  );
};
