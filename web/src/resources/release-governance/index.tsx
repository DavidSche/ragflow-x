import { useCallback, useEffect, useState } from "react";
import type { ResourceProps } from "ra-core";
import { useNotify, useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { GitBranch, Plus, RefreshCw, ShieldCheck } from "lucide-react";
import { api, unwrap, ApiError, type ApiEnvelope } from "../../lib/api";
import { NumericRangeField } from "@/components/NumericRangeField";
import { clampInteger } from "@/lib/numeric";

interface PageEnvelope<T> { items: T[]; total: number; }

interface RuntimePinEvidence {
  model_route_version?: string; model_route_pin_id?: string; model_route_pin_version?: number;
  enterprise_connection_id?: string; enterprise_connection_version?: number;
  enterprise_connection_config_hash?: string; enterprise_binding_id?: string;
  enterprise_binding_version?: number; enterprise_model_ref?: string; credential_version?: string;
}

interface ReleaseCandidate {
  candidate_id: string; candidate_version: number; target_type: string; target_id: string;
  target_version: string; base_version: string; change_summary: string;
  candidate_hash: string; status: string; created_at: string;
}
interface ExecutionSnapshot extends RuntimePinEvidence {
  id: string; release_candidate_id: string; candidate_version: number;
  snapshot_schema_version: string; snapshot_hash: string; execution_config: string; created_at: string;
}
interface EvaluationRun {
  id: string; release_candidate_id: string; candidate_version: number; status: string;
  eval_set_id: string; eval_set_version: number; eval_set_hash: string; pass?: boolean; created_at: string;
}
interface EvidenceBundle extends RuntimePinEvidence {
  id: string; release_candidate_id: string; candidate_version: number;
  evaluation_run_id: string; snapshot_id: string; snapshot_hash: string; created_at: string;
}
interface ReleaseGate {
  id: string; release_candidate_id: string; candidate_version: number; evidence_bundle_id: string;
  environment: string; decision: string; reason: string; active_gate: boolean; created_at: string;
}
interface Release extends RuntimePinEvidence {
  id: string; release_candidate_id: string; candidate_version: number; snapshot_id: string;
  gate_decision_id: string; environment: string; status: string; rollback_baseline: string; created_at: string;
}
interface QualityIssue {
  id: string; source: string; source_id: string; title: string; owner: string; status: string;
  resolution: string; eval_case_id: string; evaluation_run_id: string; updated_at: string;
}

type DialogKind = "candidate" | "snapshot" | "run" | "evidence" | "gate" | "release" | "rollback" | "issue" | "issue-state" | null;

const emptyManifest = JSON.stringify(
  Object.fromEntries([
    "target", "prompt", "knowledge", "modelRoute", "policy", "catalog",
    "router", "tools", "retrievalConfig", "executionConfig",
  ].map((section) => [section, { version: "v1", hash: "sha256:" + section }])),
  null,
  2,
);

const parseJSON = (value: string): unknown => {
  if (!value.trim()) return undefined;
  return JSON.parse(value);
};

const badgeVariant = (value: string) => {
  if (["PASS", "RELEASED", "VERIFIED", "CLOSED"].includes(value)) return "default" as const;
  if (["BLOCK", "FAILED", "REOPENED"].includes(value)) return "destructive" as const;
  return "outline" as const;
};

const runtimePinEvidenceFields = (item: RuntimePinEvidence) => [
  { key: "model_route_version", value: item.model_route_version },
  { key: "model_route_pin_id", value: item.model_route_pin_id },
  { key: "model_route_pin_version", value: item.model_route_pin_version },
  { key: "enterprise_connection_id", value: item.enterprise_connection_id },
  { key: "enterprise_connection_version", value: item.enterprise_connection_version },
  { key: "enterprise_connection_config_hash", value: item.enterprise_connection_config_hash },
  { key: "enterprise_binding_id", value: item.enterprise_binding_id },
  { key: "enterprise_binding_version", value: item.enterprise_binding_version },
  { key: "enterprise_model_ref", value: item.enterprise_model_ref },
  { key: "credential_version", value: item.credential_version },
].filter((field): field is { key: string; value: string } => field.value !== undefined && field.value !== "");

export const ReleaseGovernanceBoard = () => {
  const t = useTranslate();
  const notify = useNotify();
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [tab, setTab] = useState("candidates");
  const [dialog, setDialog] = useState<DialogKind>(null);
  const [candidates, setCandidates] = useState<ReleaseCandidate[]>([]);
  const [selectedCandidate, setSelectedCandidate] = useState("");
  const [selectedVersion, setSelectedVersion] = useState(0);
  const [snapshots, setSnapshots] = useState<ExecutionSnapshot[]>([]);
  const [selectedSnapshot, setSelectedSnapshot] = useState("");
  const [runs, setRuns] = useState<EvaluationRun[]>([]);
  const [selectedRun, setSelectedRun] = useState("");
  const [evidence, setEvidence] = useState<EvidenceBundle[]>([]);
  const [selectedEvidence, setSelectedEvidence] = useState("");
  const [gates, setGates] = useState<ReleaseGate[]>([]);
  const [selectedGate, setSelectedGate] = useState("");
  const [releases, setReleases] = useState<Release[]>([]);
  const [selectedRelease, setSelectedRelease] = useState("");
  const [issues, setIssues] = useState<QualityIssue[]>([]);
  const [candidateForm, setCandidateForm] = useState({
    target_type: "assistant", target_id: "", target_version: "", base_version: "",
    change_summary: "", candidate_id: "", candidate_version: "1", manifest: emptyManifest,
  });
  const [snapshotForm, setSnapshotForm] = useState({ schema_version: "v1", execution_config: "{}" });
  const [runForm, setRunForm] = useState({
    eval_set_id: "", eval_set_version: "1", eval_set_hash: "",
    evaluation_policy_version: "v1", evaluation_policy_hash: "",
    aggregation_policy_version: "v1", aggregation_policy_hash: "",
  });
  const [evidenceForm, setEvidenceForm] = useState({
    evaluation_run_id: "", security_evidence: "{}", policy_evidence: "{}", risk_evidence: "{}",
    permission_evidence: "{}", configuration_evidence: "{}", approval_evidence: "{}", evidence_items: "{}",
  });
  const [gateForm, setGateForm] = useState({
    evidence_bundle_id: "", environment: "pilot", environment_policy_version: "v1",
    environment_policy_hash: "", sub_gate_states: "{}", reason: "", waiver: "", approval_id: "", activate: true,
  });
  const [releaseForm, setReleaseForm] = useState({ environment: "pilot", rollback_baseline: "" });
  const [rollbackForm, setRollbackForm] = useState({ reason: "" });
  const [issueForm, setIssueForm] = useState({
    source: "evaluation", source_id: "", title: "", evidence: "{}", owner: "",
    resolution_target_type: "", resolution_target_id: "", resolution_target_version: "", eval_case_id: "",
  });
  const [issueState, setIssueState] = useState<QualityIssue | null>(null);

  const candidate = candidates.find(
    (item) => item.candidate_id === selectedCandidate && item.candidate_version === selectedVersion,
  );

  const fail = (error: unknown) => {
    notify(error instanceof ApiError ? error.displayMessage : error instanceof Error ? error.message : t("release_governance.action_failed"), { type: "error" });
  };

  const load = useCallback(async () => {
    setLoading(true);
    const candidateID = selectedCandidate || "";
    const candidateVersion = selectedVersion;
    const filter = `page=1&page_size=100&candidate_id=${encodeURIComponent(candidateID)}`;
    try {
      const [candidateRes, snapshotRes, runRes, evidenceRes, gateRes, releaseRes, issueRes] = await Promise.all([
        unwrap(api.get<ApiEnvelope<PageEnvelope<ReleaseCandidate>>>("/release-candidates?page=1&page_size=100")),
        unwrap(api.get<ApiEnvelope<PageEnvelope<ExecutionSnapshot>>>(`/execution-snapshots?${filter}`)),
        unwrap(api.get<ApiEnvelope<PageEnvelope<EvaluationRun>>>(`/evaluation-runs?${filter}`)),
        unwrap(api.get<ApiEnvelope<PageEnvelope<EvidenceBundle>>>(`/evidence-bundles?${filter}`)),
        candidateVersion > 0
          ? unwrap(api.get<ApiEnvelope<PageEnvelope<ReleaseGate>>>(`/release-gates?${filter}&candidate_version=${candidateVersion}`))
          : Promise.resolve({ items: [], total: 0 }),
        unwrap(api.get<ApiEnvelope<PageEnvelope<Release>>>(`/releases?${filter}`)),
        unwrap(api.get<ApiEnvelope<PageEnvelope<QualityIssue>>>("/quality-issues?page=1&page_size=100")),
      ]);
      setCandidates(candidateRes.items ?? []);
      setSnapshots(snapshotRes.items ?? []);
      setRuns(runRes.items ?? []);
      setEvidence(evidenceRes.items ?? []);
      setGates(gateRes.items ?? []);
      setReleases(releaseRes.items ?? []);
      setIssues(issueRes.items ?? []);
      if ((!selectedCandidate || !selectedVersion) && candidateRes.items?.length) {
        setSelectedCandidate(candidateRes.items[0].candidate_id);
        setSelectedVersion(candidateRes.items[0].candidate_version);
      }
    } catch (error) {
      fail(error);
    } finally {
      setLoading(false);
    }
  }, [notify, selectedCandidate, selectedVersion]);

  useEffect(() => { void load(); }, [load]);

  const submit = async (path: string, payload: unknown, message: string) => {
    setBusy(true);
    try {
      await unwrap(api.post<ApiEnvelope<unknown>>(path, payload));
      notify(message, { type: "success" });
      setDialog(null);
      await load();
    } catch (error) {
      fail(error);
    } finally {
      setBusy(false);
    }
  };

  const action = async (run: () => Promise<unknown>, message: string) => {
    setBusy(true);
    try {
      await run();
      notify(message, { type: "success" });
      await load();
    } catch (error) {
      fail(error);
    } finally {
      setBusy(false);
    }
  };

  const submitCandidate = () => submit("/release-candidates", {
    ...candidateForm,
    candidate_version: clampInteger(Number(candidateForm.candidate_version || 1), 1, 1_000_000, 1),
    candidate_manifest: parseJSON(candidateForm.manifest),
  }, t("release_governance.candidate_created"));

  const submitSnapshot = () => submit("/execution-snapshots", {
    release_candidate_id: selectedCandidate,
    candidate_version: candidate?.candidate_version ?? 0,
    snapshot_schema_version: snapshotForm.schema_version,
    execution_config: parseJSON(snapshotForm.execution_config),
    prompt_version: "v1", model_route_version: "v1", knowledge_version: "v1",
    retrieval_config_version: "v1", catalog_version: "v1", policy_version: "v1",
    router_version: "v1", tool_registry_version: "v1", tool_set_hash: "sha256:tools",
  }, t("release_governance.snapshot_created"));

  const submitRun = () => submit("/evaluation-runs", {
    release_candidate_id: selectedCandidate,
    candidate_version: candidate?.candidate_version ?? 0,
    eval_set_id: runForm.eval_set_id,
    eval_set_version: clampInteger(Number(runForm.eval_set_version || 1), 1, 1_000_000, 1),
    eval_set_hash: runForm.eval_set_hash,
    evaluation_policy_version: runForm.evaluation_policy_version,
    evaluation_policy_hash: runForm.evaluation_policy_hash,
    aggregation_policy_version: runForm.aggregation_policy_version,
    aggregation_policy_hash: runForm.aggregation_policy_hash,
    execution_snapshot_id: selectedSnapshot,
  }, t("release_governance.run_created"));

  const submitEvidence = () => submit("/evidence-bundles", {
    release_candidate_id: selectedCandidate,
    candidate_version: candidate?.candidate_version ?? 0,
    evaluation_run_id: selectedRun || evidenceForm.evaluation_run_id,
    security_evidence: parseJSON(evidenceForm.security_evidence),
    policy_evidence: parseJSON(evidenceForm.policy_evidence),
    risk_evidence: parseJSON(evidenceForm.risk_evidence),
    permission_evidence: parseJSON(evidenceForm.permission_evidence),
    configuration_evidence: parseJSON(evidenceForm.configuration_evidence),
    approval_evidence: parseJSON(evidenceForm.approval_evidence),
    evidence_items: parseJSON(evidenceForm.evidence_items),
  }, t("release_governance.evidence_created"));

  const submitGate = () => submit("/release-gates", {
    release_candidate_id: selectedCandidate,
    candidate_version: candidate?.candidate_version ?? 0,
    evidence_bundle_id: selectedEvidence || gateForm.evidence_bundle_id,
    environment: gateForm.environment,
    environment_policy_version: gateForm.environment_policy_version,
    environment_policy_hash: gateForm.environment_policy_hash,
    sub_gate_states: parseJSON(gateForm.sub_gate_states),
    reason: gateForm.reason,
    waiver: parseJSON(gateForm.waiver),
    approval_id: gateForm.approval_id,
    activate: gateForm.activate,
  }, t("release_governance.gate_created"));

  const submitRelease = () => submit("/releases", {
    release_candidate_id: selectedCandidate,
    candidate_version: candidate?.candidate_version ?? 0,
    snapshot_id: selectedSnapshot,
    gate_decision_id: selectedGate,
    environment: releaseForm.environment,
    rollback_baseline: releaseForm.rollback_baseline,
  }, t("release_governance.release_created"));

  const submitRollback = () => selectedRelease && submit(`/releases/${selectedRelease}/rollback`, rollbackForm, t("release_governance.release_rolled_back"));

  const submitIssue = () => submit("/quality-issues", {
    ...issueForm,
    evidence: parseJSON(issueForm.evidence),
  }, t("release_governance.issue_created"));

  const submitIssueState = () => issueState && submit(`/quality-issues/${issueState.id}/state`, {
    status: issueState.status,
    owner: issueState.owner,
    resolution: issueState.resolution,
    resolution_target_id: issueState.source_id,
    eval_case_id: issueState.eval_case_id,
    reopen_reason: issueState.resolution,
  }, t("release_governance.issue_updated"));

  const openDialog = (kind: Exclude<DialogKind, null>) => {
    setDialog(kind);
    if (kind === "snapshot" && candidate) {
      setSnapshotForm({ schema_version: "v1", execution_config: JSON.stringify({ target: candidate.target_id, version: candidate.target_version }, null, 2) });
    }
    if (kind === "run" && snapshots.length) setSelectedSnapshot(snapshots[0].id);
    if (kind === "evidence" && runs.length) setSelectedRun(runs[0].id);
    if (kind === "gate" && evidence.length) setSelectedEvidence(evidence[0].id);
    if (kind === "release" && gates.length) setSelectedGate(gates[0].id);
    if (kind === "issue-state") setDialog(kind);
  };

  return (
    <div className="space-y-4" aria-label={t("release_governance.title")}>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <ShieldCheck className="size-5 text-primary" aria-hidden="true" />
          <div>
            <h1 className="text-lg font-semibold">{t("release_governance.title")}</h1>
            <p className="text-sm text-muted-foreground">{t("release_governance.subtitle")}</p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Select value={selectedCandidate} onValueChange={(value) => {
            const next = candidates.find((item) => item.candidate_id === value);
            setSelectedCandidate(value);
            setSelectedVersion(next?.candidate_version ?? 0);
          }}>
            <SelectTrigger className="w-64" aria-label={t("release_governance.select_candidate")}>
              <SelectValue placeholder={t("release_governance.select_candidate")} />
            </SelectTrigger>
            <SelectContent>
              {candidates.map((item) => (
                <SelectItem key={item.candidate_id + ":" + item.candidate_version} value={item.candidate_id}>
                  {item.candidate_id} v{item.candidate_version}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button variant="outline" size="sm" onClick={() => void load()} disabled={loading}>
            <RefreshCw className="size-4" /> {t("ra.action.refresh")}
          </Button>
          <Button size="sm" onClick={() => openDialog("candidate")}>
            <Plus className="size-4" /> {t("release_governance.create_candidate")}
          </Button>
        </div>
      </div>

      {loading ? (
        <div
          className="relative h-0.5 w-full overflow-hidden rounded-full bg-primary/10"
          data-testid="release-governance-loading"
        >
          <div className="absolute inset-y-0 left-0 w-full animate-loading-bar bg-primary" />
        </div>
      ) : null}

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="candidates">{t("release_governance.tab_candidates")}</TabsTrigger>
          <TabsTrigger value="runs">{t("release_governance.tab_runs")}</TabsTrigger>
          <TabsTrigger value="evidence">{t("release_governance.tab_evidence")}</TabsTrigger>
          <TabsTrigger value="releases">{t("release_governance.tab_releases")}</TabsTrigger>
          <TabsTrigger value="issues">{t("release_governance.tab_issues")}</TabsTrigger>
        </TabsList>

        <TabsContent value="candidates" className="space-y-3">
          <div className="flex gap-2">
            <Button variant="outline" size="sm" disabled={!candidate || candidate.status !== "DRAFT" || busy}
              onClick={() => void action(() => api.post(`/release-candidates/${selectedCandidate}/ready`, {}), t("release_governance.candidate_ready"))}>
              {t("release_governance.mark_ready")}
            </Button>
            <Button variant="outline" size="sm" disabled={!candidate || busy} onClick={() => openDialog("snapshot")}>
              {t("release_governance.create_snapshot")}
            </Button>
          </div>
          {candidates.map((item) => (
            <Card key={item.candidate_id + ":" + item.candidate_version} className={item.candidate_id === selectedCandidate ? "border-primary" : ""}>
              <CardHeader><CardTitle className="flex items-center justify-between gap-2 text-base">
                <span>{item.candidate_id} v{item.candidate_version}</span>
                <Badge variant={badgeVariant(item.status)}>{item.status}</Badge>
              </CardTitle></CardHeader>
              <CardContent className="space-y-1 text-sm">
                <div>{item.target_type} / {item.target_id} / {item.target_version}</div>
                <div className="text-muted-foreground">{item.change_summary}</div>
                <div className="font-mono text-xs">{item.candidate_hash}</div>
                <Button variant="ghost" size="sm" onClick={() => { setSelectedCandidate(item.candidate_id); setSelectedVersion(item.candidate_version); }}>{t("release_governance.select")}</Button>
              </CardContent>
            </Card>
          ))}
        </TabsContent>

        <TabsContent value="runs" className="space-y-3">
          <div className="flex gap-2">
            <Button size="sm" disabled={!candidate || busy} onClick={() => openDialog("run")}>{t("release_governance.create_run")}</Button>
            <Button variant="outline" size="sm" disabled={!selectedRun || runs.find((item) => item.id === selectedRun)?.status !== "RUNNING" || busy}
              onClick={() => void action(() => api.post(`/evaluation-runs/${selectedRun}/complete`, { failed: false }), t("release_governance.run_completed"))}>
              {t("release_governance.complete_run")}
            </Button>
          </div>
          {runs.map((item) => (
            <Card key={item.id} className={item.id === selectedRun ? "border-primary" : ""}>
              <CardHeader><CardTitle className="flex items-center justify-between gap-2 text-base">
                <span>{item.id}</span><Badge variant={badgeVariant(item.status)}>{item.status}</Badge>
              </CardTitle></CardHeader>
              <CardContent className="space-y-2 text-sm">
                <div>{item.eval_set_id} v{item.eval_set_version} · {item.eval_set_hash}</div>
                <Button variant="ghost" size="sm" onClick={() => setSelectedRun(item.id)}>{t("release_governance.select")}</Button>
              </CardContent>
            </Card>
          ))}
        </TabsContent>

        <TabsContent value="evidence" className="space-y-3">
          <div className="flex gap-2">
            <Button size="sm" disabled={!candidate || !selectedRun || busy} onClick={() => openDialog("evidence")}>{t("release_governance.create_evidence")}</Button>
            <Button variant="outline" size="sm" disabled={!candidate || !selectedEvidence || busy} onClick={() => openDialog("gate")}>{t("release_governance.create_gate")}</Button>
          </div>
          {[...evidence.map((item) => ({ id: item.id, detail: item.snapshot_id, kind: "EVIDENCE", pin: item, selected: item.id === selectedEvidence, select: () => setSelectedEvidence(item.id) })),
            ...gates.map((item) => ({ id: item.id, detail: `${item.environment} / ${item.decision}`, kind: item.decision, pin: undefined, selected: item.id === selectedGate, select: () => setSelectedGate(item.id) }))].map((item) => (
            <Card key={item.id} className={item.selected ? "border-primary" : ""}>
              <CardHeader><CardTitle className="flex items-center justify-between gap-2 text-base">
                <span>{item.id}</span><Badge variant={badgeVariant(item.kind)}>{item.kind}</Badge>
              </CardTitle></CardHeader>
              <CardContent className="text-sm">
                <div>{item.detail}</div>
                {item.pin ? <RuntimePinEvidenceDetails item={item.pin} /> : null}
                <Button variant="ghost" size="sm" onClick={item.select}>{t("release_governance.select")}</Button>
              </CardContent>
            </Card>
          ))}
        </TabsContent>

        <TabsContent value="releases" className="space-y-3">
          <Button size="sm" disabled={!candidate || !selectedSnapshot || !selectedGate || busy} onClick={() => openDialog("release")}>{t("release_governance.create_release")}</Button>
          {releases.map((item) => (
            <Card key={item.id}>
              <CardHeader><CardTitle className="flex items-center justify-between gap-2 text-base">
                <span>{item.id}</span><Badge variant={badgeVariant(item.status)}>{item.status}</Badge>
              </CardTitle></CardHeader>
              <CardContent className="space-y-2 text-sm">
                <div>{item.environment} · {item.snapshot_id} · {item.gate_decision_id}</div>
                <RuntimePinEvidenceDetails item={item} />
                {item.status === "CREATED" ? (
                  <Button size="sm" disabled={busy} onClick={() => void action(() => api.post(`/releases/${item.id}/complete`, { failed: false }), t("release_governance.release_completed"))}>
                    {t("release_governance.complete_release")}
                  </Button>
                ) : null}
                {item.status === "RELEASED" ? (
                  <Button size="sm" variant="outline" disabled={busy} onClick={() => { setSelectedRelease(item.id); setRollbackForm({ reason: "" }); openDialog("rollback"); }}>
                    {t("release_governance.rollback_release")}
                  </Button>
                ) : null}
              </CardContent>
            </Card>
          ))}
        </TabsContent>

        <TabsContent value="issues" className="space-y-3">
          <Button size="sm" disabled={busy} onClick={() => openDialog("issue")}>{t("release_governance.create_issue")}</Button>
          {issues.map((item) => (
            <Card key={item.id}>
              <CardHeader><CardTitle className="flex items-center justify-between gap-2 text-base">
                <span>{item.title}</span><Badge variant={badgeVariant(item.status)}>{item.status}</Badge>
              </CardTitle></CardHeader>
              <CardContent className="space-y-2 text-sm">
                <div>{item.source}/{item.source_id || "-"} · {item.owner || t("release_governance.unassigned")}</div>
                <Button variant="outline" size="sm" disabled={busy} onClick={() => { setIssueState(item); setDialog("issue-state"); }}>
                  {t("release_governance.update_issue")}
                </Button>
                {item.status !== "VERIFIED" && item.status !== "CLOSED" ? (
                  <Button size="sm" disabled={busy || !selectedRun || item.status !== "REGRESSION_PENDING"}
                    onClick={() => void action(() => api.post(`/quality-issues/${item.id}/verify`, { run_id: selectedRun }), t("release_governance.issue_verified"))}>
                    {t("release_governance.verify_issue")}
                  </Button>
                ) : null}
              </CardContent>
            </Card>
          ))}
        </TabsContent>
      </Tabs>

      <Dialog open={dialog !== null} onOpenChange={(open) => { if (!open) { setDialog(null); setIssueState(null); } }}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-3xl">
          <DialogHeader><DialogTitle>{t("release_governance.dialog_" + dialog)}</DialogTitle></DialogHeader>
          <div className="space-y-3">
            {dialog === "candidate" ? (
              <>
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label={t("release_governance.target_type")}>
                    <select className="rounded border bg-background px-2 py-1 text-sm" value={candidateForm.target_type} onChange={(e) => setCandidateForm({ ...candidateForm, target_type: e.target.value })}>
                      <option value="assistant">assistant</option><option value="prompt_version">prompt_version</option>
                      <option value="template">template</option><option value="model_route_package">model_route_package</option>
                    </select>
                  </Field>
                  <Field label={t("release_governance.candidate_id")}><Input value={candidateForm.candidate_id} onChange={(e) => setCandidateForm({ ...candidateForm, candidate_id: e.target.value })} /></Field>
                  <Field label={t("release_governance.target_id")}><Input value={candidateForm.target_id} onChange={(e) => setCandidateForm({ ...candidateForm, target_id: e.target.value })} /></Field>
                  <Field label={t("release_governance.target_version")}><Input value={candidateForm.target_version} onChange={(e) => setCandidateForm({ ...candidateForm, target_version: e.target.value })} /></Field>
                  <Field label={t("release_governance.base_version")}><Input value={candidateForm.base_version} onChange={(e) => setCandidateForm({ ...candidateForm, base_version: e.target.value })} /></Field>
                  <NumericRangeField
                    label={t("release_governance.candidate_version")}
                    value={candidateForm.candidate_version}
                    min={1}
                    max={1_000_000}
                    onChange={(next) => setCandidateForm({ ...candidateForm, candidate_version: String(next ?? 1) })}
                  />
                </div>
                <Field label={t("release_governance.change_summary")}><Input value={candidateForm.change_summary} onChange={(e) => setCandidateForm({ ...candidateForm, change_summary: e.target.value })} /></Field>
                <Field label={t("release_governance.manifest")}><Textarea className="min-h-56 font-mono text-xs" value={candidateForm.manifest} onChange={(e) => setCandidateForm({ ...candidateForm, manifest: e.target.value })} /></Field>
              </>
            ) : null}
            {dialog === "snapshot" ? (
              <>
                <Field label={t("release_governance.schema_version")}><Input value={snapshotForm.schema_version} onChange={(e) => setSnapshotForm({ ...snapshotForm, schema_version: e.target.value })} /></Field>
                <Field label={t("release_governance.execution_config")}><Textarea className="min-h-40 font-mono text-xs" value={snapshotForm.execution_config} onChange={(e) => setSnapshotForm({ ...snapshotForm, execution_config: e.target.value })} /></Field>
              </>
            ) : null}
            {dialog === "run" ? (
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label={t("release_governance.eval_set_id")}><Input value={runForm.eval_set_id} onChange={(e) => setRunForm({ ...runForm, eval_set_id: e.target.value })} /></Field>
                <NumericRangeField
                  label={t("release_governance.eval_set_version")}
                  value={runForm.eval_set_version}
                  min={1}
                  max={1_000_000}
                  onChange={(next) => setRunForm({ ...runForm, eval_set_version: String(next ?? 1) })}
                />
                <Field label={t("release_governance.eval_set_hash")}><Input value={runForm.eval_set_hash} onChange={(e) => setRunForm({ ...runForm, eval_set_hash: e.target.value })} /></Field>
                <Field label={t("release_governance.snapshot_id")}><Input value={selectedSnapshot} onChange={(e) => setSelectedSnapshot(e.target.value)} readOnly /></Field>
                <Field label={t("release_governance.evaluation_policy_version")}><Input value={runForm.evaluation_policy_version} onChange={(e) => setRunForm({ ...runForm, evaluation_policy_version: e.target.value })} /></Field>
                <Field label={t("release_governance.evaluation_policy_hash")}><Input value={runForm.evaluation_policy_hash} onChange={(e) => setRunForm({ ...runForm, evaluation_policy_hash: e.target.value })} /></Field>
                <Field label={t("release_governance.aggregation_policy_version")}><Input value={runForm.aggregation_policy_version} onChange={(e) => setRunForm({ ...runForm, aggregation_policy_version: e.target.value })} /></Field>
                <Field label={t("release_governance.aggregation_policy_hash")}><Input value={runForm.aggregation_policy_hash} onChange={(e) => setRunForm({ ...runForm, aggregation_policy_hash: e.target.value })} /></Field>
              </div>
            ) : null}
            {dialog === "evidence" ? (
              <>
                <Field label={t("release_governance.run_id")}><Input value={selectedRun || evidenceForm.evaluation_run_id} onChange={(e) => { setSelectedRun(e.target.value); setEvidenceForm({ ...evidenceForm, evaluation_run_id: e.target.value }); }} /></Field>
                {["security_evidence", "policy_evidence", "risk_evidence", "permission_evidence", "configuration_evidence", "approval_evidence", "evidence_items"].map((key) => (
                  <Field key={key} label={t("release_governance." + key)}>
                    <Textarea className="min-h-24 font-mono text-xs" value={evidenceForm[key as keyof typeof evidenceForm]} onChange={(e) => setEvidenceForm({ ...evidenceForm, [key]: e.target.value })} />
                  </Field>
                ))}
              </>
            ) : null}
            {dialog === "gate" ? (
              <>
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label={t("release_governance.evidence_id")}><Input value={selectedEvidence || gateForm.evidence_bundle_id} onChange={(e) => { setSelectedEvidence(e.target.value); setGateForm({ ...gateForm, evidence_bundle_id: e.target.value }); }} /></Field>
                  <Field label={t("release_governance.environment")}>
                    <select className="rounded border bg-background px-2 py-1 text-sm" value={gateForm.environment} onChange={(e) => setGateForm({ ...gateForm, environment: e.target.value })}>
                      <option value="development">development</option><option value="pilot">pilot</option><option value="production">production</option>
                    </select>
                  </Field>
                  <Field label={t("release_governance.environment_policy_version")}><Input value={gateForm.environment_policy_version} onChange={(e) => setGateForm({ ...gateForm, environment_policy_version: e.target.value })} /></Field>
                  <Field label={t("release_governance.environment_policy_hash")}><Input value={gateForm.environment_policy_hash} onChange={(e) => setGateForm({ ...gateForm, environment_policy_hash: e.target.value })} /></Field>
                  <Field label={t("release_governance.approval_id")}><Input value={gateForm.approval_id} onChange={(e) => setGateForm({ ...gateForm, approval_id: e.target.value })} /></Field>
                  <label className="flex items-center gap-2 pt-6 text-sm"><Checkbox checked={gateForm.activate} onCheckedChange={(checked) => setGateForm({ ...gateForm, activate: checked === true })} />{t("release_governance.activate")}</label>
                </div>
                <Field label={t("release_governance.sub_gate_states")}><Textarea className="min-h-24 font-mono text-xs" value={gateForm.sub_gate_states} onChange={(e) => setGateForm({ ...gateForm, sub_gate_states: e.target.value })} /></Field>
                <Field label={t("release_governance.reason")}><Textarea className="min-h-20" value={gateForm.reason} onChange={(e) => setGateForm({ ...gateForm, reason: e.target.value })} /></Field>
                <Field label={t("release_governance.waiver")}><Textarea className="min-h-24 font-mono text-xs" value={gateForm.waiver} onChange={(e) => setGateForm({ ...gateForm, waiver: e.target.value })} /></Field>
              </>
            ) : null}
            {dialog === "release" ? (
              <div className="grid gap-3 sm:grid-cols-2">
                <Field label={t("release_governance.snapshot_id")}><Input value={selectedSnapshot} readOnly /></Field>
                <Field label={t("release_governance.gate_id")}><Input value={selectedGate} readOnly /></Field>
                <Field label={t("release_governance.environment")}>
                  <select className="rounded border bg-background px-2 py-1 text-sm" value={releaseForm.environment} onChange={(e) => setReleaseForm({ ...releaseForm, environment: e.target.value })}>
                    <option value="development">development</option><option value="pilot">pilot</option><option value="production">production</option>
                  </select>
                </Field>
                <Field label={t("release_governance.rollback_baseline")}><Input value={releaseForm.rollback_baseline} onChange={(e) => setReleaseForm({ ...releaseForm, rollback_baseline: e.target.value })} /></Field>
              </div>
            ) : null}
            {dialog === "rollback" ? (
              <Field label={t("release_governance.rollback_reason")}>
                <Textarea className="min-h-24" value={rollbackForm.reason} onChange={(e) => setRollbackForm({ reason: e.target.value })} />
              </Field>
            ) : null}
            {dialog === "issue" ? (
              <>
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label={t("release_governance.issue_source")}><Input value={issueForm.source} onChange={(e) => setIssueForm({ ...issueForm, source: e.target.value })} /></Field>
                  <Field label={t("release_governance.issue_source_id")}><Input value={issueForm.source_id} onChange={(e) => setIssueForm({ ...issueForm, source_id: e.target.value })} /></Field>
                  <Field label={t("release_governance.issue_title")}><Input value={issueForm.title} onChange={(e) => setIssueForm({ ...issueForm, title: e.target.value })} /></Field>
                  <Field label={t("release_governance.issue_owner")}><Input value={issueForm.owner} onChange={(e) => setIssueForm({ ...issueForm, owner: e.target.value })} /></Field>
                  <Field label={t("release_governance.eval_case_id")}><Input value={issueForm.eval_case_id} onChange={(e) => setIssueForm({ ...issueForm, eval_case_id: e.target.value })} /></Field>
                  <Field label={t("release_governance.resolution_target_id")}><Input value={issueForm.resolution_target_id} onChange={(e) => setIssueForm({ ...issueForm, resolution_target_id: e.target.value })} /></Field>
                </div>
                <Field label={t("release_governance.issue_evidence")}><Textarea className="min-h-24 font-mono text-xs" value={issueForm.evidence} onChange={(e) => setIssueForm({ ...issueForm, evidence: e.target.value })} /></Field>
              </>
            ) : null}
            {dialog === "issue-state" && issueState ? (
              <>
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label={t("release_governance.issue_status")}>
                    <select className="rounded border bg-background px-2 py-1 text-sm" value={issueState.status} onChange={(e) => setIssueState({ ...issueState, status: e.target.value })}>
                      <option value="OPEN">OPEN</option><option value="TRIAGED">TRIAGED</option><option value="IN_PROGRESS">IN_PROGRESS</option>
                      <option value="FIXED">FIXED</option><option value="REGRESSION_PENDING">REGRESSION_PENDING</option>
                      <option value="CLOSED">CLOSED</option><option value="REOPENED">REOPENED</option>
                    </select>
                  </Field>
                  <Field label={t("release_governance.issue_owner")}><Input value={issueState.owner} onChange={(e) => setIssueState({ ...issueState, owner: e.target.value })} /></Field>
                </div>
                <Field label={t("release_governance.issue_resolution")}><Textarea className="min-h-24" value={issueState.resolution} onChange={(e) => setIssueState({ ...issueState, resolution: e.target.value })} /></Field>
              </>
            ) : null}
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setDialog(null)}>{t("ra.action.cancel")}</Button>
              <Button
                disabled={busy}
                onClick={() => {
                  if (dialog === "candidate") void submitCandidate();
                  if (dialog === "snapshot") void submitSnapshot();
                  if (dialog === "run") void submitRun();
                  if (dialog === "evidence") void submitEvidence();
                  if (dialog === "gate") void submitGate();
                  if (dialog === "release") void submitRelease();
                  if (dialog === "rollback") void submitRollback();
                  if (dialog === "issue") void submitIssue();
                  if (dialog === "issue-state") void submitIssueState();
                }}
              >
                {t("ra.action.save")}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <Label className="text-xs text-muted-foreground">{label}</Label>
      {children}
    </div>
  );
}

function RuntimePinEvidenceDetails({ item }: { item: RuntimePinEvidence }) {
  const t = useTranslate();
  const fields = runtimePinEvidenceFields(item);
  if (!fields.length) return null;
  return (
    <div className="mt-2 rounded-md border bg-muted/20 p-2">
      <div className="text-xs font-medium">{t("release_governance.runtime_pin_evidence")}</div>
      <dl className="mt-1 grid gap-1 text-xs sm:grid-cols-2">
        {fields.map((field) => (
          <div key={field.key} className="min-w-0">
            <dt className="text-muted-foreground">{t("release_governance." + field.key)}</dt>
            <dd className="truncate font-mono" title={String(field.value)}>{field.value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

export const releaseGovernance: ResourceProps = {
  name: "release-governance",
  list: ReleaseGovernanceBoard,
  recordRepresentation: () => "Release Governance",
  options: { label: "Release Governance" },
  icon: GitBranch,
};
