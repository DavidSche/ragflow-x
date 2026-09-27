import { useCallback, useEffect, useState } from "react";
import type { ResourceProps } from "ra-core";
import { useCanAccess, useGetList, useNotify, useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { GitBranch, Plus, RefreshCw, ShieldCheck } from "lucide-react";
import { api, unwrap, ApiError, type ApiEnvelope } from "../../lib/api";

interface PageEnvelope<T> { items: T[]; total: number; }

interface AssistantRelease {
  id: string; assistant_id: string; assistant_version_id: string; release_version: number;
  release_state: string; reconcile_status: string; desired_state_hash: string; actual_state_hash: string;
  snapshot_manifest_id: string; previous_release_id: string; fencing_token: number;
  optimistic_version: number; evidence_bundle_id: string; gate_decision_id: string;
  gate_result: string; approval_id: string;
  created_by: string; created_at: string; updated_at: string;
}

interface Assistant {
  id: string; project_id: string; name: string; lifecycle_status: string;
  current_assistant_release_id: string; owner_id: string; risk_level: string;
}

interface SnapshotManifest {
  id: string; assistant_release_id: string; schema_version: string; snapshot_hash: string;
  complete: boolean; manifest_json: string; created_at: string;
}

interface ReleaseOperation {
  id: string; release_id: string; source_release_id: string; target_release_id: string;
  operation_type: string; operation_state: string; current_step: string;
  error_code: string; created_at: string;
}

interface RuntimeHealth {
  assistant_release_id: string; health: string; reason: string; checked_at: string;
}

interface DatasetOption {
  id: string;
  name: string;
}

type DialogKind = "create" | "rollback" | "activate" | null;

const idempotencyKey = () =>
  (globalThis.crypto?.randomUUID?.() ?? "release-" + Date.now().toString(36) + Math.random().toString(36).slice(2));

export const releaseStateTone = (state: string) => {
  if (state === "ACTIVE") return "default" as const;
  if (state.endsWith("FAILED") || state === "UNAVAILABLE") return "destructive" as const;
  if (state === "CANARY_ACTIVE" || state === "DEGRADED") return "secondary" as const;
  return "outline" as const;
};

export const canApplyRelease = (state: string) => state === "APPROVED";
export const canActivateRelease = (state: string) => state === "VERIFIED";
export const canPromoteCanary = (state: string) => state === "CANARY_ACTIVE";
export const canRollbackRelease = (state: string) => state === "ACTIVE";
export const canRollbackToTarget = (state: string) => state === "RETIRED" || state === "VERIFIED";
export const canCompensateRelease = (state: string) =>
  state === "APPLY_FAILED" || state === "COMPENSATING";

const parseJSONField = (label: string, value: string) => {
  try {
    return JSON.parse(value) as unknown;
  } catch {
    throw new Error(`${label} must be valid JSON`);
  }
};

function Field({ label, value }: { label: string; value: unknown }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="truncate font-mono text-xs" title={String(value ?? "")}>{String(value ?? "-")}</dd>
    </div>
  );
}

export function AssistantReleaseBoard() {
  const t = useTranslate();
  const notify = useNotify();
  const { canAccess: canManage } = useCanAccess({ resource: "release-governance", action: "manage" });
  const [assistants, setAssistants] = useState<Assistant[]>([]);
  const [assistantId, setAssistantId] = useState("");
  const [releases, setReleases] = useState<AssistantRelease[]>([]);
  const [selectedId, setSelectedId] = useState("");
  const [manifest, setManifest] = useState<SnapshotManifest | null>(null);
  const [operations, setOperations] = useState<ReleaseOperation[]>([]);
  const [health, setHealth] = useState<RuntimeHealth | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [dialog, setDialog] = useState<DialogKind>(null);
  const [rollbackTarget, setRollbackTarget] = useState("");
  const [form, setForm] = useState({
    project_id: "", scenario_template_id: "", template_version_id: "", name: "", owner_id: "",
    policy_version: "policy-v1", scenario_pack_payload: "{}", execution_contract: "{}",
    policy_json: "{}", runtime_profile: "{}", desired_state: "{}", target_type: "chat", target_id: "",
    evidence_bundle_id: "", gate_decision_id: "", gate_result: "PASS", approval_id: "",
  });
  const [selectedDatasetIds, setSelectedDatasetIds] = useState<string[]>([]);
  const { data: datasets } = useGetList<DatasetOption>("datasets", { pagination: { page: 1, perPage: 200 } });

  const selected = releases.find((release) => release.id === selectedId) ?? null;

  const toggleDataset = (datasetId: string) => {
    setSelectedDatasetIds((previous) => previous.includes(datasetId)
      ? previous.filter((id) => id !== datasetId)
      : [...previous, datasetId]);
  };

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const data = await unwrap(api.get<ApiEnvelope<PageEnvelope<Assistant>>>("/assistants?page=1&page_size=100"));
      setAssistants(data.items);
      const nextAssistant = assistantId || data.items[0]?.id || "";
      setAssistantId(nextAssistant);
      if (!nextAssistant) {
        setReleases([]);
        setSelectedId("");
        return;
      }
      const releaseData = await unwrap(api.get<ApiEnvelope<PageEnvelope<AssistantRelease>>>(
        `/assistant-releases?assistant_id=${encodeURIComponent(nextAssistant)}&page=1&page_size=100`,
      ));
      setReleases(releaseData.items);
      const next = selectedId && releaseData.items.some((item) => item.id === selectedId)
        ? releaseData.items.find((item) => item.id === selectedId)
        : releaseData.items[0];
      const nextId = next?.id ?? "";
      setSelectedId(nextId);
      if (!nextId) {
        setManifest(null);
        setOperations([]);
        setHealth(null);
        return;
      }
      const [manifestData, operationData, healthData] = await Promise.all([
        unwrap(api.get<ApiEnvelope<SnapshotManifest>>(`/assistant-releases/${nextId}/manifest`)),
        unwrap(api.get<ApiEnvelope<PageEnvelope<ReleaseOperation>>>(
          `/assistant-releases/${nextId}/operations?page=1&page_size=50`,
        )),
        unwrap(api.get<ApiEnvelope<RuntimeHealth | null>>(`/assistant-releases/${nextId}/health`)),
      ]);
      setManifest(manifestData);
      setOperations(operationData.items);
      setHealth(healthData);
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("assistant_release.load_failed"), { type: "error" });
    } finally {
      setLoading(false);
    }
  }, [assistantId, notify, selectedId, t]);

  useEffect(() => {
    void load();
  }, [assistantId, load]);

  const mutate = async (action: () => Promise<unknown>, successKey: string) => {
    setBusy(true);
    try {
      await action();
      notify(t(successKey), { type: "success" });
      await load();
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("assistant_release.action_failed"), { type: "error" });
    } finally {
      setBusy(false);
    }
  };

  const createRelease = () => {
    if (!assistantId || !form.project_id || !form.template_version_id || !form.name || !form.owner_id ||
      !form.target_id || selectedDatasetIds.length === 0 || !form.evidence_bundle_id ||
      !form.gate_decision_id || !form.approval_id) {
      notify(t("assistant_release.required_fields_missing"), { type: "error" });
      return;
    }
    try {
      parseJSONField("scenario_pack", form.scenario_pack_payload);
      parseJSONField("execution_contract", form.execution_contract);
      parseJSONField("policy", form.policy_json);
      parseJSONField("runtime_profile", form.runtime_profile);
      parseJSONField("desired_state", form.desired_state);
    } catch (error) {
      notify(error instanceof Error ? error.message : t("assistant_release.invalid_json"), { type: "error" });
      return;
    }
    return mutate(async () => {
    const target = form.target_id;
    await unwrap(api.post<ApiEnvelope<AssistantRelease>>("/assistant-releases", {
      project_id: form.project_id,
      scenario_template_id: form.scenario_template_id,
      template_version_id: form.template_version_id,
      assistant_id: assistantId || undefined,
      name: form.name,
      owner_id: form.owner_id,
      scenario_pack_schema: "1",
      scenario_pack_payload: form.scenario_pack_payload,
      execution_contract: form.execution_contract,
      policy_version: form.policy_version,
      policy_json: form.policy_json,
      runtime_profile: form.runtime_profile,
      desired_state: form.desired_state,
      evidence_bundle_id: form.evidence_bundle_id,
      gate_decision_id: form.gate_decision_id,
      gate_result: form.gate_result,
      approval_id: form.approval_id,
      capability_bindings: [{
        binding_id: "capability-main",
        capability: form.target_type === "agent" ? "agentic_task" : form.target_type === "search_app" ? "explicit_search" : "knowledge_chat",
        adapter: form.target_type === "agent" ? "ragflow_agent" : form.target_type === "search_app" ? "ragflow_search_app" : "ragflow_chat",
        target_type: form.target_type, target_id: target, rgx_resource_id: target,
        target_version: "1", ownership_verified: true,
      }],
      dataset_bindings: selectedDatasetIds.map((datasetId, index) => ({
        binding_id: `dataset-${index + 1}`,
        dataset_id: datasetId,
        dataset_version: "1",
      })),
    }, { headers: { "Idempotency-Key": idempotencyKey() } }));
    setDialog(null);
    }, "assistant_release.create_success");
  };

  const apply = () => selected && mutate(
    () => api.post(`/assistant-releases/${selected.id}/apply`, { fencing_token: selected.fencing_token }, { headers: { "Idempotency-Key": idempotencyKey() } }),
    "assistant_release.apply_success",
  );

  const reconcile = () => selected && mutate(
    () => api.post(`/assistant-releases/${selected.id}/reconcile`, { fencing_token: selected.fencing_token }, { headers: { "Idempotency-Key": idempotencyKey() } }),
    "assistant_release.reconcile_success",
  );

  const activate = (canary: boolean) => selected && mutate(async () => {
    const currentAssistant = assistants.find((assistant) => assistant.id === assistantId);
    await api.post(`/assistant-releases/${selected.id}/activate`, {
      canary,
      rollout_percentage: canary ? 5 : 100,
      expected_current_release_id: currentAssistant?.current_assistant_release_id,
      fencing_token: selected.fencing_token,
    }, { headers: { "Idempotency-Key": idempotencyKey() } });
    setDialog(null);
  }, "assistant_release.activate_success");

  const promoteCanary = () => selected && mutate(
    () => api.post(`/assistant-releases/${selected.id}/promote-canary`, {
      fencing_token: selected.fencing_token,
      expected_current_release_id: assistants.find((assistant) => assistant.id === assistantId)?.current_assistant_release_id,
    }, { headers: { "Idempotency-Key": idempotencyKey() } }),
    "assistant_release.promote_success",
  );

  const rollback = () => selected && mutate(async () => {
    await api.post(
      `/assistant-releases/${selected.id}/rollback/${encodeURIComponent(rollbackTarget)}`,
      { fencing_token: selected.fencing_token }, { headers: { "Idempotency-Key": idempotencyKey() } },
    );
    setDialog(null);
  }, "assistant_release.rollback_success");

  const compensate = () => selected && mutate(
    () => api.post(`/assistant-releases/${selected.id}/compensate`, {
      fencing_token: selected.fencing_token,
    }, { headers: { "Idempotency-Key": idempotencyKey() } }),
    "assistant_release.compensate_success",
  );

  return (
    <div className="space-y-4" data-testid="assistant-release-board">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h1 className="text-lg font-semibold">{t("assistant_release.title")}</h1>
          <p className="text-sm text-muted-foreground">{t("assistant_release.subtitle")}</p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" onClick={() => void load()} disabled={loading}>
            <RefreshCw className="mr-2 size-4" />{t("assistant_release.refresh")}
          </Button>
          {canManage && (
            <Button onClick={() => setDialog("create")}>
              <Plus className="mr-2 size-4" />{t("assistant_release.create")}
            </Button>
          )}
        </div>
      </div>

      <Card>
        <CardHeader><CardTitle>{t("assistant_release.assistants")}</CardTitle></CardHeader>
        <CardContent className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {assistants.map((assistant) => (
            <button
              key={assistant.id}
              type="button"
              onClick={() => setAssistantId(assistant.id)}
              className={`rounded-lg border p-3 text-left transition ${assistant.id === assistantId ? "border-primary" : ""}`}
            >
              <div className="flex items-center justify-between gap-2">
                <span className="font-medium">{assistant.name}</span>
                <Badge variant={releaseStateTone(assistant.lifecycle_status)}>{assistant.lifecycle_status}</Badge>
              </div>
              <div className="mt-1 truncate text-xs text-muted-foreground">{assistant.current_assistant_release_id}</div>
            </button>
          ))}
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>{t("assistant_release.releases")}</CardTitle></CardHeader>
        <CardContent className="space-y-2">
          {loading && <div data-testid="assistant-release-loading">{t("assistant_release.loading")}</div>}
          {!loading && releases.map((release) => (
            <button
              key={release.id}
              type="button"
              onClick={() => setSelectedId(release.id)}
              className={`w-full rounded-lg border p-3 text-left ${selected?.id === release.id ? "border-primary" : ""}`}
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">v{release.release_version}</span>
                <Badge variant={releaseStateTone(release.release_state)}>{release.release_state}</Badge>
                <Badge variant={releaseStateTone(release.reconcile_status)}>{release.reconcile_status}</Badge>
              </div>
              <div className="mt-1 text-xs text-muted-foreground">{release.id}</div>
            </button>
          ))}
        </CardContent>
      </Card>

      {selected && (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <ShieldCheck className="size-4" />{t("assistant_release.release_detail")}
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <dl className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
              <Field label={t("assistant_release.id")} value={selected.id} />
              <Field label={t("assistant_release.version_id")} value={selected.assistant_version_id} />
              <Field label={t("assistant_release.desired_hash")} value={selected.desired_state_hash} />
              <Field label={t("assistant_release.actual_hash")} value={selected.actual_state_hash} />
            </dl>
            <div className="flex flex-wrap gap-2">
              {canManage && canApplyRelease(selected.release_state) && (
                <Button onClick={apply} disabled={busy}>{t("assistant_release.apply")}</Button>
              )}
              {canManage && canActivateRelease(selected.release_state) && (
                <Button onClick={() => setDialog("activate")} disabled={busy}>{t("assistant_release.activate")}</Button>
              )}
              {canManage && canPromoteCanary(selected.release_state) && (
                <Button onClick={promoteCanary} disabled={busy}>{t("assistant_release.promote")}</Button>
              )}
              {canManage && canRollbackRelease(selected.release_state) && (
                <Button variant="outline" onClick={() => { setRollbackTarget(""); setDialog("rollback"); }} disabled={busy}>{t("assistant_release.rollback")}</Button>
              )}
              {canManage && (
                <Button variant="outline" onClick={reconcile} disabled={busy}>
                <GitBranch className="mr-2 size-4" />{t("assistant_release.reconcile")}
                </Button>
              )}
              {canManage && canCompensateRelease(selected.release_state) && (
                <Button variant="destructive" onClick={compensate} disabled={busy}>{t("assistant_release.compensate")}</Button>
              )}
            </div>
            <div className="grid gap-3 md:grid-cols-2">
              <div className="rounded-lg border p-3">
                <h3 className="text-sm font-medium">{t("assistant_release.manifest")}</h3>
                <dl className="mt-2 grid grid-cols-2 gap-2">
                  <Field label={t("assistant_release.complete")} value={String(manifest?.complete)} />
                  <Field label={t("assistant_release.snapshot_hash")} value={manifest?.snapshot_hash} />
                </dl>
              </div>
              <div className="rounded-lg border p-3">
                <h3 className="text-sm font-medium">{t("assistant_release.health")}</h3>
                <div className="mt-2 flex items-center gap-2">
                  <Badge variant={releaseStateTone(health?.health ?? "")}>{health?.health ?? "UNKNOWN"}</Badge>
                  <span className="truncate text-xs text-muted-foreground">{health?.reason}</span>
                </div>
              </div>
            </div>
            <div className="rounded-lg border">
              <div className="border-b p-3 text-sm font-medium">{t("assistant_release.operations")}</div>
              <div className="divide-y">
                {operations.map((operation) => (
                  <div key={operation.id} className="flex flex-wrap items-center gap-2 p-3 text-xs">
                    <Badge variant={releaseStateTone(operation.operation_state)}>{operation.operation_type}</Badge>
                    <span>{operation.operation_state}</span>
                    <span className="text-muted-foreground">{operation.current_step}</span>
                    {operation.error_code && <span className="text-destructive">{operation.error_code}</span>}
                  </div>
                ))}
              </div>
            </div>
          </CardContent>
        </Card>
      )}

      <Dialog open={dialog === "create"} onOpenChange={(open) => !open && setDialog(null)}>
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-3xl">
          <DialogHeader><DialogTitle>{t("assistant_release.create")}</DialogTitle></DialogHeader>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="space-y-1"><Label>{t("assistant_release.project_id")}</Label><Input value={form.project_id} onChange={(event) => setForm({ ...form, project_id: event.target.value })} /></div>
            <div className="space-y-1"><Label>{t("assistant_release.template_id")}</Label><Input value={form.scenario_template_id} onChange={(event) => setForm({ ...form, scenario_template_id: event.target.value })} /></div>
            <div className="space-y-1"><Label>{t("assistant_release.template_version_id")}</Label><Input value={form.template_version_id} onChange={(event) => setForm({ ...form, template_version_id: event.target.value })} /></div>
            <div className="space-y-1"><Label>{t("assistant_release.name")}</Label><Input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} /></div>
            <div className="space-y-1"><Label>{t("assistant_release.owner_id")}</Label><Input value={form.owner_id} onChange={(event) => setForm({ ...form, owner_id: event.target.value })} /></div>
            <div className="space-y-1">
              <Label htmlFor="assistant-release-target-type">{t("assistant_release.target_type")}</Label>
              <select
                id="assistant-release-target-type"
                className="w-full rounded-md border bg-background px-2 py-1 text-sm"
                value={form.target_type}
                onChange={(event) => setForm({ ...form, target_type: event.target.value })}
              >
                <option value="chat">Chat</option>
                <option value="agent">Agent</option>
                <option value="search_app">Search App</option>
              </select>
            </div>
            <div className="space-y-1 sm:col-span-2"><Label>{t("assistant_release.target_id")}</Label><Input value={form.target_id} onChange={(event) => setForm({ ...form, target_id: event.target.value })} /></div>
            <div className="space-y-1 sm:col-span-2">
              <Label>{t("assistant_release.dataset_bindings")}</Label>
              <div className="max-h-32 space-y-1 overflow-y-auto rounded-md border p-2">
                {(datasets ?? []).map((dataset) => (
                  <label key={dataset.id} className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={selectedDatasetIds.includes(dataset.id)}
                      onChange={() => toggleDataset(dataset.id)}
                    />
                    {dataset.name}
                  </label>
                ))}
                {(datasets ?? []).length === 0 && (
                  <p className="text-sm text-muted-foreground">{t("assistant_release.no_datasets")}</p>
                )}
              </div>
            </div>
            <div className="space-y-1 sm:col-span-2"><Label>{t("assistant_release.scenario_pack")}</Label><Textarea value={form.scenario_pack_payload} onChange={(event) => setForm({ ...form, scenario_pack_payload: event.target.value })} /></div>
            <div className="space-y-1 sm:col-span-2"><Label>{t("assistant_release.execution_contract")}</Label><Textarea value={form.execution_contract} onChange={(event) => setForm({ ...form, execution_contract: event.target.value })} /></div>
            <div className="space-y-1 sm:col-span-2"><Label>{t("assistant_release.policy")}</Label><Textarea value={form.policy_json} onChange={(event) => setForm({ ...form, policy_json: event.target.value })} /></div>
            <div className="space-y-1 sm:col-span-2"><Label>{t("assistant_release.runtime_profile")}</Label><Textarea value={form.runtime_profile} onChange={(event) => setForm({ ...form, runtime_profile: event.target.value })} /></div>
            <div className="space-y-1 sm:col-span-2"><Label>{t("assistant_release.desired_state")}</Label><Textarea value={form.desired_state} onChange={(event) => setForm({ ...form, desired_state: event.target.value })} /></div>
            <div className="space-y-1"><Label>{t("assistant_release.evidence_bundle_id")}</Label><Input value={form.evidence_bundle_id} onChange={(event) => setForm({ ...form, evidence_bundle_id: event.target.value })} /></div>
            <div className="space-y-1"><Label>{t("assistant_release.gate_decision_id")}</Label><Input value={form.gate_decision_id} onChange={(event) => setForm({ ...form, gate_decision_id: event.target.value })} /></div>
            <div className="space-y-1">
              <Label htmlFor="assistant-release-gate-result">{t("assistant_release.gate_result")}</Label>
              <select id="assistant-release-gate-result" className="w-full rounded-md border bg-background px-2 py-1 text-sm" value={form.gate_result} onChange={(event) => setForm({ ...form, gate_result: event.target.value })}>
                <option value="PASS">PASS</option>
                <option value="PASS_WITH_WARNING">PASS_WITH_WARNING</option>
              </select>
            </div>
            <div className="space-y-1"><Label>{t("assistant_release.approval_id")}</Label><Input value={form.approval_id} onChange={(event) => setForm({ ...form, approval_id: event.target.value })} /></div>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setDialog(null)}>{t("assistant_release.cancel")}</Button>
            <Button onClick={createRelease} disabled={busy}>{t("assistant_release.submit")}</Button>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={dialog === "activate"} onOpenChange={(open) => !open && setDialog(null)}>
        <DialogContent>
          <DialogHeader><DialogTitle>{t("assistant_release.activate")}</DialogTitle></DialogHeader>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setDialog(null)}>{t("assistant_release.cancel")}</Button>
            <Button variant="outline" onClick={() => activate(true)} disabled={busy}>{t("assistant_release.activate_canary")}</Button>
            <Button onClick={() => activate(false)} disabled={busy}>{t("assistant_release.activate_stable")}</Button>
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={dialog === "rollback"} onOpenChange={(open) => !open && setDialog(null)}>
        <DialogContent>
          <DialogHeader><DialogTitle>{t("assistant_release.rollback")}</DialogTitle></DialogHeader>
          <div className="space-y-1">
            <Label>{t("assistant_release.target_release")}</Label>
            <select
              className="w-full rounded-md border bg-background px-2 py-1 text-sm"
              value={rollbackTarget}
              onChange={(event) => setRollbackTarget(event.target.value)}
            >
              <option value="">{t("assistant_release.select_target")}</option>
              {releases
                .filter((release) => release.id !== selected?.id && canRollbackToTarget(release.release_state))
                .map((release) => (
                  <option key={release.id} value={release.id}>
                    v{release.release_version} · {release.release_state}
                  </option>
                ))}
            </select>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setDialog(null)}>{t("assistant_release.cancel")}</Button>
            <Button onClick={rollback} disabled={busy || !rollbackTarget}>{t("assistant_release.submit")}</Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

export const assistantReleases: ResourceProps = {
  name: "assistant-releases",
  list: AssistantReleaseBoard,
  recordRepresentation: () => "Assistant Releases",
  options: { label: "Assistant Releases" },
  icon: GitBranch,
};
