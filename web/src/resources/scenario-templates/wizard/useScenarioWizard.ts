import { useCallback, useEffect, useMemo, useState } from "react";
import { useCanAccess, useCreatePath, useGetIdentity, useGetList, useLocale, useNavigate, useNotify, useTranslate } from "ra-core";
import { ApiError } from "@/lib/api";
import { datasetOptions } from "@/resources/chats/chat-types";
import { tenantScopedDatasetOptions } from "./datasetScope";
import type { DatasetLike } from "@/resources/chats/chat-types";
import {
  checkScenarioTemplateCapabilities,
  fetchScenarioSkeletonCatalogSafe,
  fetchScenarioTemplateCatalog,
  instantiateScenarioTemplate,
  importScenarioSkeleton,
  type CapabilityCheckResponse,
  type ScenarioAppType,
  type ScenarioSkeleton,
  type ScenarioTemplateView,
} from "../scenario-templates";
import type { PushdownRequest, RepairRequest } from "./repairPlan";
import { useRepairActions } from "./useRepairActions";
import { projectChoices, type ProjectLike } from "./projectOptions";
import type { CapabilityCheckView, DatasetCapabilityView, WizardSelection } from "./types";

const SUGGESTED_ROLE = "primary";

export interface ScenarioWizardApi {
  selection: WizardSelection;
  templates: ScenarioTemplateView[];
  catalogState: "loading" | "ready" | "error";
  datasetOptions: DatasetLike[];
  datasetValues: DatasetLike[];
  /** Datasets belonging to other workspaces, hidden because instantiate cannot resolve them. */
  hiddenDatasetCount: number;
  /** Projects the caller may attach the new assistant to (id + display name). */
  projects: { id: string; name: string }[];
  mapDatasetId: (dataset: DatasetLike) => string;
  isChecking: boolean;
  capabilityCheck: CapabilityCheckView | null;
  /**
   * A pushdown repair only *starts* an asynchronous metadata backfill, so the
   * conclusion kept here predates it. The wizard asks for an explicit re-check
   * instead of re-probing immediately and contradicting its own success message
   * (`postRepairStep` in ./repairPlan).
   */
  checkStale: boolean;
  capabilityError: string;
  skeletons: ScenarioSkeleton[];
  skeletonState: "idle" | "loading" | "ready" | "degraded" | "error";
  saving: boolean;
  canManageAgent: boolean;
  canManageChat: boolean;
  canStartTrialRun: boolean;
  selectedDatasetNames: string[];
  setAppType: (value: ScenarioAppType) => void;
  setName: (value: string) => void;
  setProjectId: (value: string) => void;
  setModelId: (value: string) => void;
  selectTemplate: (item: ScenarioTemplateView) => void;
  toggleDataset: (datasetId: string) => void;
  reloadCatalog: () => void;
  runCapabilityCheck: () => Promise<void>;
  repairDataset: (datasetId: string, body: RepairRequest) => Promise<void>;
  repairDatasetPushdown: (datasetId: string, body: PushdownRequest) => Promise<void>;
  loadSkeletons: () => Promise<void>;
  importSkeleton: (skeletonId: string) => Promise<void>;
  createApplication: () => Promise<void>;
}

/**
 * All wizard state and side effects live here so the four step components stay
 * presentational (doc/276 §12 / specification/26 file-size rule). Every network
 * failure goes through `ApiError.displayMessage` — no bare catch renders a raw
 * axios message.
 */
export function useScenarioWizard(): ScenarioWizardApi {
  const t = useTranslate();
  const notify = useNotify();
  const navigate = useNavigate();
  const createPath = useCreatePath();
  const [locale] = useLocale();

  const [templates, setTemplates] = useState<ScenarioTemplateView[]>([]);
  const [catalogState, setCatalogState] = useState<"loading" | "ready" | "error">("loading");
  const [reloadKey, setReloadKey] = useState(0);
  const [selection, setSelection] = useState<WizardSelection>({
    template: null, appType: "chat", name: "", projectId: "", modelId: "", datasetIds: [],
  });
  const [isChecking, setChecking] = useState(false);
  const [capabilityCheck, setCapabilityCheck] = useState<CapabilityCheckView | null>(null);
  const [capabilityError, setCapabilityError] = useState("");
  const [skeletons, setSkeletons] = useState<ScenarioSkeleton[]>([]);
  const [skeletonState, setSkeletonState] = useState<ScenarioWizardApi["skeletonState"]>("idle");
  const [saving, setSaving] = useState(false);

  const { data: datasets } = useGetList("datasets", { pagination: { page: 1, perPage: 200 } });
  // ra 5 types the generic parameter as the error type, so the identity shape
  // is asserted locally instead of through useGetIdentity<…>.
  const { data: identity } = useGetIdentity();
  const tenantId = (identity as { tenant_id?: string } | undefined)?.tenant_id;
  const { data: projects } = useGetList("projects", { pagination: { page: 1, perPage: 100 } });
  // The picker shows names while the API takes ids (see ./projectOptions).
  const projectOptions = useMemo(
    () => projectChoices(projects as ProjectLike[] | undefined),
    [projects],
  );
  const mapId = datasetOptions(datasets as DatasetLike[] | undefined).mapId;
  // Only the caller's workspace is bindable: instantiate resolves every id with
  // the caller's tenant (see ./datasetScope for the drill that proved it).
  const { values: datasetValues, hidden: hiddenDatasetCount } = useMemo(
    () => tenantScopedDatasetOptions((datasets as DatasetLike[] | undefined) ?? [], tenantId),
    [datasets, tenantId],
  );

  const { canAccess: canManageChat } = useCanAccess({ resource: "chat", action: "manage" });
  const { canAccess: canManageAgent } = useCanAccess({ resource: "agent", action: "manage" });
  const { canAccess: canStartTrialRun } = useCanAccess({ resource: "trial-run", action: "execute" });

  useEffect(() => {
    let mounted = true;
    setCatalogState("loading");
    fetchScenarioTemplateCatalog(locale)
      .then((rows) => {
        if (!mounted) return;
        setTemplates(rows);
        setCatalogState("ready");
      })
      .catch(() => {
        if (!mounted) return;
        setTemplates([]);
        setCatalogState("error");
      });
    return () => {
      mounted = false;
    };
  }, [locale, reloadKey]);

  // Project is mandatory on the server (doc/276 §7.5), so preselect the single
  // plausible project instead of letting the user hit a 400 on submit.
  useEffect(() => {
    if (selection.projectId) return;
    const rows = projectOptions;
    if (rows.length === 1) {
      setSelection((prev) => ({ ...prev, projectId: rows[0].id }));
    }
  }, [projectOptions, selection.projectId]);

  const runCapabilityCheck = useCallback(async () => {
    const template = selection.template;
    if (!template) return;
    setChecking(true);
    setCapabilityError("");
    try {
      const response = await checkScenarioTemplateCapabilities(template.id, {
        app_type: selection.appType,
        datasets: selection.datasetIds.map((datasetId) => ({
          role: SUGGESTED_ROLE,
          dataset_id: datasetId,
        })),
      });
      setCapabilityCheck(decorateCapabilityCheck(response, datasetValues));
      clearStale();
    } catch (error) {
      setCapabilityCheck(null);
      setCapabilityError(
        error instanceof ApiError ? error.displayMessage : t("scenarioTemplates.wizard.capabilityCheckFailed"),
      );
    } finally {
      setChecking(false);
    }
  }, [datasetValues, selection.appType, selection.datasetIds, selection.template, t]);

  const { checkStale, clearStale, repairDataset, repairDatasetPushdown } = useRepairActions(runCapabilityCheck);

  const selectTemplate = useCallback((item: ScenarioTemplateView) => {
    setSelection((prev) => ({
      ...prev,
      template: item,
      // Preselect a form the server can build; falling back to a declared-but-
      // unrenderable form would fail at submit time (doc/276 §8.3).
      appType: item.renderableAppTypes[0] ?? item.appTypes[0] ?? "chat",
      name: item.label,
      modelId: "",
    }));
    setCapabilityCheck(null);
    setCapabilityError("");
  }, []);

  const toggleDataset = useCallback((datasetId: string) => {
    setSelection((prev) => ({
      ...prev,
      datasetIds: prev.datasetIds.includes(datasetId)
        ? prev.datasetIds.filter((id) => id !== datasetId)
        : [...prev.datasetIds, datasetId],
    }));
    // The verdict belongs to the binding it was produced for. Keeping it after
    // the binding changed shows a conclusion about datasets that are no longer
    // selected (and the create gate would read it).
    setCapabilityCheck(null);
    clearStale();
  }, [clearStale]);


  const loadSkeletons = useCallback(async () => {
    setSkeletonState("loading");
    try {
      const catalog = await fetchScenarioSkeletonCatalogSafe();
      setSkeletons(catalog.items);
      setSkeletonState(catalog.degraded ? "degraded" : "ready");
    } catch {
      setSkeletons([]);
      setSkeletonState("error");
    }
  }, []);

  const importSkeleton = useCallback(
    async (skeletonId: string) => {
      try {
        const created = await importScenarioSkeleton(skeletonId);
        notify(t("scenarioTemplates.wizard.skeletonImported"), { type: "success" });
        navigate(createPath({ resource: "scenario-template-assets", type: "edit", id: created.id }));
      } catch (error) {
        notify(
          error instanceof ApiError ? error.displayMessage : t("scenarioTemplates.wizard.skeletonImportFailed"),
          { type: "error" },
        );
      }
    },
    [createPath, navigate, notify, t],
  );

  const createApplication = useCallback(async () => {
    const template = selection.template;
    if (!template || !selection.name.trim() || !selection.projectId) return;
    setSaving(true);
    try {
      // `dataset_ids` here are workspace links, not engine ids: the server resolves
      // each one with GetDatasetLink(tenantID, id) and answers
      // 400/41120 "dataset not found in workspace" for an engine id. `mapId`
      // (ragflow_dataset_id || id) is only for the probe/检索 side.
      const datasetIds = selection.datasetIds;
      const idempotencyKey = `wizard-${template.id}-${Date.now()}`;
      const result = await instantiateScenarioTemplate(
        template.id,
        {
          app_type: selection.appType,
          name: selection.name.trim(),
          project_id: selection.projectId,
          dataset_ids: datasetIds,
          create_missing_datasets: false,
          ...(selection.appType === "chat" && selection.modelId
            ? { authoring: { llm_id: selection.modelId } }
            : {}),
        },
        idempotencyKey,
      );
      notify(t("scenarioTemplates.wizard.created"), { type: "success" });
      const createdId =
        result.chat?.id ?? result.search_app?.id ?? result.agent?.id ?? result.instance_id;
      const resource = selection.appType === "chat" ? "chats" : selection.appType === "search" ? "search-apps" : "agents";
      if (selection.appType === "chat" && canStartTrialRun !== false) {
        const started = await startTrialRun(template.id, createdId);
        if (started) return;
      }
      navigate(createPath({ resource, type: "show", id: createdId }));
    } catch (error) {
      notify(
        error instanceof ApiError ? error.displayMessage : t("scenarioTemplates.wizard.createFailed"),
        { type: "error" },
      );
    } finally {
      setSaving(false);
    }
  }, [
    canStartTrialRun, createPath, datasetValues, mapId, navigate, notify,
    selection, t,
  ]);

  const startTrialRun = useCallback(
    async (templateID: string, assistantID: string) => {
      try {
        const { api } = await import("@/lib/api");
        const res = await api.post<{ code: number; data: { id: string } }>("/trial-runs", {
          scenario_template_id: templateID,
          assistant_id: assistantID,
          idempotency_key: `wizard-${assistantID}-${Date.now()}`,
        });
        const runID = res.data?.data?.id;
        if (!runID) return false;
        navigate(createPath({ resource: "trial-runs", type: "show", id: runID }));
        return true;
      } catch (error) {
        notify(error instanceof ApiError ? error.displayMessage : t("scenarioTemplates.trial.startFailed"), {
          type: "warning",
        });
        return false;
      }
    },
    [createPath, navigate, notify, t],
  );

  const selectedDatasetNames = useMemo(
    () =>
      datasetValues
        .filter((item) => selection.datasetIds.includes(item.id))
        .map((item) => item.name),
    [datasetValues, selection.datasetIds],
  );

  return {
    selection,
    templates,
    catalogState,
    datasetOptions: datasetValues,
    datasetValues,
    hiddenDatasetCount,
    projects: projectOptions,
    mapDatasetId: mapId,
    isChecking,
    capabilityCheck,
    capabilityError,
    skeletons,
    skeletonState,
    saving,
    canManageAgent: canManageAgent !== false,
    canManageChat: canManageChat !== false,
    canStartTrialRun: canStartTrialRun !== false,
    selectedDatasetNames,
    // Requirements differ per app type, so a chat verdict must not survive a
    // switch to agent.
    setAppType: (value) => {
      setSelection((prev) => ({ ...prev, appType: value, modelId: "" }));
      setCapabilityCheck(null);
      clearStale();
    },
    setName: (value) => setSelection((prev) => ({ ...prev, name: value })),
    setProjectId: (value) => setSelection((prev) => ({ ...prev, projectId: value })),
    setModelId: (value) => setSelection((prev) => ({ ...prev, modelId: value })),
    selectTemplate,
    toggleDataset,
    checkStale,
    reloadCatalog: () => setReloadKey((value) => value + 1),
    runCapabilityCheck,
    repairDataset,
    repairDatasetPushdown,
    loadSkeletons,
    importSkeleton,
    createApplication,
  };
}

function decorateCapabilityCheck(
  response: CapabilityCheckResponse,
  datasets: DatasetLike[],
): CapabilityCheckView {
  const byId = new Map(datasets.map((item) => [item.id, item.name]));
  const rows: DatasetCapabilityView[] = response.datasets.map((item) => ({
    ...item,
    dataset_name: byId.get(item.dataset_id) ?? item.dataset_id,
  }));
  return { ...response, datasets: rows };
}
