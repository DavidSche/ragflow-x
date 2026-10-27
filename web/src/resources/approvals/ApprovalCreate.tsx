import { useEffect, useMemo, useState, type ReactNode } from "react";
import {
  useCanAccess,
  useGetIdentity,
  useGetList,
  useNavigate,
  useNotify,
  useTranslate,
} from "ra-core";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Skeleton } from "@/components/ui/skeleton";
import { dataProvider } from "../../dataProvider";
import { api, ApiError } from "../../lib/api";
import {
  ApprovalHoldError,
  approvalIdempotencyKey,
  type ApprovalHold,
} from "@/lib/approval-hold";
import { ApprovalPayloadField } from "./ApprovalPayloadField";
import { APPROVAL_ACTIONS, APPROVAL_OBJECTS, parseJSONValue } from "./approval-ui";
import { parseExtra, typeLabels } from "../model-providers/provider-types";

type ApprovalForm = {
  object_type: string;
  action: string;
  object_id: string;
  title: string;
  reason: string;
  payload_json: string;
};

type DatasetRow = { id: string; name: string; project_id?: string };
type APIKeyRow = { id: string; name: string; enabled?: boolean };
type AgentRow = { id: string; title: string };
type ChatRow = { id: string; name: string };
type ProviderRow = { id: string; name: string; provider_type: string };
type InstanceRow = {
  id: string;
  instance_name: string;
  base_url?: string;
  region?: string;
};
type InstanceModelRow = {
  id: string;
  model_name: string;
  model_type: number;
  max_tokens?: number;
  extra_json?: string;
};
type InstanceForm = {
  instance_name: string;
  api_key: string;
  base_url: string;
  region: string;
};

const EMPTY_FORM: ApprovalForm = {
  object_type: "dataset",
  action: "delete",
  object_id: "",
  title: "",
  reason: "",
  payload_json: "{}",
};

const EMPTY_INSTANCE_FORM: InstanceForm = {
  instance_name: "",
  api_key: "",
  base_url: "",
  region: "default",
};

const SUPPORTED_ACTIONS: Record<string, string[]> = {
  dataset: ["create", "update", "delete"],
  "document": ["update", "delete", "parse", "stop", "enable", "disable"],
  "document-chunk": ["delete", "enable", "disable"],
	"api-key": ["create", "revoke"],
	"chat": ["create", "update", "delete"],
	"agent": ["create", "update", "delete"],
	"model-provider": ["create", "delete"],
  "model-instance": ["create", "update", "delete"],
  "model-model": ["create", "update", "delete", "test"],
};

export const ApprovalCreate = () => {
  const t = useTranslate();
  const notify = useNotify();
  const navigate = useNavigate();
  const { data: identity } = useGetIdentity();
  const { canAccess } = useCanAccess({ resource: "approval", action: "execute" });
  const { data: datasetRecords } = useGetList("datasets", {
    pagination: { page: 1, perPage: 500 },
  });
  const { data: apiKeyRecords } = useGetList("api-keys", {
    pagination: { page: 1, perPage: 500 },
  });
  const { data: agentRecords } = useGetList("agents", {
    pagination: { page: 1, perPage: 500 },
  });
  const { data: chatRecords } = useGetList("chats", {
    pagination: { page: 1, perPage: 500 },
  });
  const { data: providerRecords } = useGetList("model-providers", {
    pagination: { page: 1, perPage: 500 },
  });
  const [form, setForm] = useState<ApprovalForm>(EMPTY_FORM);
  const [instanceForm, setInstanceForm] = useState(EMPTY_INSTANCE_FORM);
  const [instanceModels, setInstanceModels] = useState<InstanceModelRow[]>([]);
  const [instances, setInstances] = useState<InstanceRow[]>([]);
  const [instanceOptionsLoading, setInstanceOptionsLoading] = useState(false);
  const [instanceModelsLoading, setInstanceModelsLoading] = useState(false);
  const [approvalHold, setApprovalHold] = useState<ApprovalHold | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const actions = useMemo(
    () => SUPPORTED_ACTIONS[form.object_type] ?? [],
    [form.object_type],
  );
  const datasets = useMemo(
    () => Object.values(datasetRecords ?? {}) as DatasetRow[],
    [datasetRecords],
  );
  const apiKeys = useMemo(
    () => Object.values(apiKeyRecords ?? {}) as APIKeyRow[],
    [apiKeyRecords],
  );
  const agents = useMemo(
    () => Object.values(agentRecords ?? {}) as AgentRow[],
    [agentRecords],
  );
  const chats = useMemo(
    () => Object.values(chatRecords ?? {}) as ChatRow[],
    [chatRecords],
  );
  const providers = useMemo(
    () => Object.values(providerRecords ?? {}) as ProviderRow[],
    [providerRecords],
  );
  const isCreateAction = form.action === "create";
  const objectIDRequired = !(
    isCreateAction &&
    (form.object_type === "api-key" || form.object_type === "model-provider" || form.object_type === "model-instance" || form.object_type === "dataset" || form.object_type === "agent" || form.object_type === "chat")
  );
  const selectedProviderID = form.object_id.split(":", 1)[0];
  const objectIDParts = form.object_id.split(":");
  const selectedInstanceID = form.object_id.includes(":")
    ? objectIDParts[1] ?? ""
    : "";
  const selectedModelID = objectIDParts[2] ?? "";
  const selectedInstance = instances.find((instance) => instance.id === selectedInstanceID);

  useEffect(() => {
    if (form.object_type !== "model-instance" && form.object_type !== "model-model") {
      setInstances([]);
      return;
    }
    if (providers.length === 0) {
      setInstances([]);
      return;
    }
    let canceled = false;
    setInstanceOptionsLoading(true);
    Promise.all(
      providers.map(async (provider) => {
        const response = await api.get<{
          code: number;
          message: string;
          data: InstanceRow[];
          trace_id?: string;
        }>(`/model-providers/${provider.id}/instances`);
        if (response.data.code !== 0) {
          throw new ApiError(
            response.status,
            response.data.code,
            response.data.message,
            response.data.trace_id,
          );
        }
        return response.data.data;
      }),
    )
      .then((pages) => {
        if (!canceled) setInstances(pages.flat());
      })
      .catch(() => {
        if (!canceled) setInstances([]);
      })
      .finally(() => {
        if (!canceled) setInstanceOptionsLoading(false);
      });
    return () => {
      canceled = true;
    };
  }, [form.object_type, providers]);

  useEffect(() => {
    if (
      (form.object_type !== "model-instance" && form.object_type !== "model-model") ||
      !selectedProviderID ||
      !selectedInstanceID
    ) {
      setInstanceModels([]);
      return;
    }
    let canceled = false;
    setInstanceModelsLoading(true);
    api
      .get<{
        code: number;
        message: string;
        data: InstanceModelRow[];
        trace_id?: string;
      }>(`/model-providers/${selectedProviderID}/instances/${selectedInstanceID}/models`)
      .then((response) => {
        if (response.data.code !== 0) {
          throw new ApiError(
            response.status,
            response.data.code,
            response.data.message,
            response.data.trace_id,
          );
        }
        if (!canceled) setInstanceModels(response.data.data ?? []);
      })
      .catch(() => {
        if (!canceled) setInstanceModels([]);
      })
      .finally(() => {
        if (!canceled) setInstanceModelsLoading(false);
      });
    return () => {
      canceled = true;
    };
  }, [form.object_type, selectedProviderID, selectedInstanceID]);

  const changeObject = (value: string) => {
    const nextAction = SUPPORTED_ACTIONS[value]?.[0] ?? "update";
    setForm((current) => ({
      ...current,
      object_type: value,
      action: nextAction,
      object_id: "",
      payload_json: defaultPayload(value, nextAction),
    }));
    setInstanceForm(EMPTY_INSTANCE_FORM);
    setInstanceModels([]);
  };

  const changeAction = (value: string) => {
    setForm((current) => ({
      ...current,
      action: value,
      object_id: "",
      payload_json: defaultPayload(current.object_type, value),
    }));
    setInstanceForm(EMPTY_INSTANCE_FORM);
    setInstanceModels([]);
  };

  const update = <K extends keyof ApprovalForm>(key: K, value: ApprovalForm[K]) => {
    setForm((current) => ({ ...current, [key]: value }));
  };

  const updateInstanceForm = (value: InstanceForm) => setInstanceForm(value);

  const submit = async () => {
    let payload: Record<string, unknown>;
    if (form.object_type === "model-instance") {
      payload = {
        ...instanceForm,
        provider_id: selectedProviderID,
        model_info: instanceModels.map((model) => ({
          model_name: model.model_name,
          model_type: typeLabels(model.model_type),
          max_tokens: model.max_tokens ?? 8192,
          is_tools: parseExtra(model.extra_json).is_tools ?? false,
          thinking: parseExtra(model.extra_json).thinking ?? false,
        })),
      };
    } else if (form.object_type === "model-model") {
      payload = parseJSONValue<Record<string, unknown>>(form.payload_json, {});
      payload.provider_id = selectedProviderID;
      payload.instance_id = selectedInstanceID;
      if (form.action !== "create") {
        payload.model_id = selectedModelID;
      }
    } else {
      try {
        payload = JSON.parse(form.payload_json || "{}") as Record<string, unknown>;
      } catch {
        notify(t("approvals.create.invalid_payload"), { type: "error" });
        return;
      }
    }
    setSubmitting(true);
    try {
      const result = await dataProvider.create<{ id: string }>("approvals", {
        data: {
          object_type: form.object_type,
          action: form.action,
          object_id: form.object_id,
          title: form.title,
          reason: form.reason,
          payload,
          idempotency_key: approvalIdempotencyKey(String(identity?.id || "manual")),
        },
      });
      notify(t("approvals.created"), { type: "success" });
      navigate(`/approvals/${result.data.id}/show`);
    } catch (error) {
      if (error instanceof ApprovalHoldError) {
        setApprovalHold(error.hold);
        return;
      }
      notify(error instanceof ApiError ? error.displayMessage : t("approvals.create.failed"), {
        type: "error",
      });
    } finally {
      setSubmitting(false);
    }
  };

  if (canAccess === false) {
    return <p className="text-sm text-destructive">{t("approvals.create.forbidden")}</p>;
  }

  return (
    <div className="mx-auto max-w-2xl space-y-4">
      <h2 className="text-xl font-semibold">{t("approvals.create.title")}</h2>
      <p className="text-sm text-muted-foreground">{t("approvals.create.description")}</p>
      <div className="grid gap-4 rounded-lg border bg-card p-4">
        <div className="grid gap-4 md:grid-cols-2">
          <div className="space-y-2">
            <RequiredLabel htmlFor="approval-object">{t("approvals.object_type")}</RequiredLabel>
            <select
              id="approval-object"
              className="w-full rounded border bg-background px-2 py-1 text-sm"
              value={form.object_type}
              onChange={(event) => changeObject(event.target.value)}
            >
              {APPROVAL_OBJECTS.map((value) => (
                <option key={value} value={value}>{value}</option>
              ))}
            </select>
          </div>
          <div className="space-y-2">
            <RequiredLabel htmlFor="approval-action">{t("approvals.action")}</RequiredLabel>
            <select
              id="approval-action"
              className="w-full rounded border bg-background px-2 py-1 text-sm"
              value={form.action}
              onChange={(event) => changeAction(event.target.value)}
            >
              {actions.map((value) => (
                <option key={value} value={value}>{value}</option>
              ))}
            </select>
          </div>
        </div>
        <ObjectIDSelector
          datasets={datasets}
          apiKeys={apiKeys}
          agents={agents}
          chats={chats}
          providers={providers}
          instances={instances}
          form={form}
          instanceOptionsLoading={instanceOptionsLoading}
          instanceModels={instanceModels}
          instanceModelsLoading={instanceModelsLoading}
          selectedInstance={selectedInstance}
          update={update}
          updateInstanceForm={updateInstanceForm}
        />
        <div className="space-y-2">
          <Label htmlFor="approval-title">{t("approvals.title")}</Label>
          <Input id="approval-title" value={form.title} onChange={(event) => update("title", event.target.value)} />
        </div>
        <div className="space-y-2">
          <Label htmlFor="approval-reason">{t("approvals.reason")}</Label>
          <Textarea id="approval-reason" rows={3} value={form.reason} onChange={(event) => update("reason", event.target.value)} />
        </div>
        {form.object_type === "model-instance" ? (
          <div className="space-y-4 rounded-lg border bg-muted/20 p-3">
            <div className="grid gap-4 md:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="approval-instance-name">{t("providers.instance_name")}</Label>
                <Input
                  id="approval-instance-name"
                  value={instanceForm.instance_name}
                  onChange={(event) => setInstanceForm((current) => ({ ...current, instance_name: event.target.value }))}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="approval-region">{t("providers.region")}</Label>
                <Input
                  id="approval-region"
                  value={instanceForm.region}
                  onChange={(event) => setInstanceForm((current) => ({ ...current, region: event.target.value }))}
                />
              </div>
            </div>
            <div className="space-y-2">
              <RequiredLabel htmlFor="approval-base-url">{t("providers.base_url")}</RequiredLabel>
              <Input
                id="approval-base-url"
                value={instanceForm.base_url}
                onChange={(event) => setInstanceForm((current) => ({ ...current, base_url: event.target.value }))}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="approval-api-key">{t("providers.api_key_optional")}</Label>
              <Input
                id="approval-api-key"
                type="password"
                value={instanceForm.api_key}
                onChange={(event) => setInstanceForm((current) => ({ ...current, api_key: event.target.value }))}
              />
              <p className="text-xs text-muted-foreground">{t("approvals.create.api_key_hint")}</p>
            </div>
            {selectedInstance && instanceModelsLoading ? (
              <p className="text-xs text-muted-foreground">{t("approvals.create.loading_models")}</p>
            ) : (
              <p className="text-xs text-muted-foreground">
                {t("approvals.create.models_loaded", { count: instanceModels.length })}
              </p>
            )}
          </div>
        ) : (
          <div className="space-y-2">
            {form.action === "delete" ? (
              <Label htmlFor="approval-payload">{t("approvals.payload")}</Label>
            ) : (
              <RequiredLabel htmlFor="approval-payload">{t("approvals.payload")}</RequiredLabel>
            )}
            <ApprovalPayloadField
              id="approval-payload"
              label=""
              value={form.payload_json}
              invalidLabel={t("approvals.create.invalid_payload")}
              onChange={(value) => update("payload_json", value)}
            />
          </div>
        )}
        <div className="flex justify-end">
          <Button
            onClick={() => void submit()}
            disabled={
              submitting ||
              (objectIDRequired && !form.object_id) ||
              (form.object_type === "model-instance" && form.action === "create" && !instanceForm.base_url) ||
              (form.object_type === "model-instance" && form.action !== "create" && !form.object_id) ||
              (form.object_type === "model-model" && !selectedProviderID && !selectedInstanceID)
            }
          >
            {submitting ? t("approvals.create.submitting") : t("approvals.create.submit")}
          </Button>
        </div>
      </div>
    </div>
  );
};

const RequiredLabel = ({
  htmlFor,
  children,
}: {
  htmlFor: string;
  children: ReactNode;
}) => (
  <Label htmlFor={htmlFor}>
    <span className="text-destructive">*</span> {children}
  </Label>
);

function defaultPayload(objectType: string, action: string): string {
  const defaults: Record<string, Record<string, unknown>> = {
    "dataset:create": { name: "" },
    "dataset:update": { name: "", project_id: "", config: null },
	"api-key:create": { name: "", token_quota: 0, request_quota: 0, allowed_ips: [] },
	"agent:create": { title: "", dsl: {}, release: false, canvas_category: "agent_canvas" },
	"agent:update": { title: "", dsl: {}, release: null },
	"agent:delete": {},
	"chat:create": { name: "", dataset_ids: [], authoring: {} },
	"chat:update": { name: "", dataset_ids: [], authoring: {} },
	"chat:delete": {},
	"document:update": { dataset_id: "", document_id: "", metadata: {} },
	"document:delete": { dataset_id: "", document_id: "" },
	"document:parse": { dataset_id: "", document_id: "" },
	"document:stop": { dataset_id: "", document_id: "" },
	"document:enable": { dataset_id: "", document_id: "", enabled: true },
	"document:disable": { dataset_id: "", document_id: "", enabled: false },
	"document-chunk:delete": { dataset_id: "", document_id: "", chunk_id: "" },
	"document-chunk:enable": { dataset_id: "", document_id: "", chunk_id: "", enabled: true },
	"document-chunk:disable": { dataset_id: "", document_id: "", chunk_id: "", enabled: false },
    "model-provider:create": { provider_name: "" },
    "model-provider:delete": {},
    "model-instance:create": {
      instance_name: "",
      api_key: "",
      base_url: "",
      region: "default",
      model_info: [],
    },
    "model-model:create": { model_name: "", model_type: ["chat"], max_tokens: 8192 },
    "model-model:update": { update: { status: "", max_tokens: 8192, extra: {} } },
    "model-model:test": { message: "" },
  };
  if (objectType === "model-instance" && action === "update") {
    return JSON.stringify(
      {
        instance_name: "",
        api_key: "",
        base_url: "",
        region: "default",
        model_info: [],
      },
      null,
      2,
    );
  }
  return JSON.stringify(defaults[`${objectType}:${action}`] ?? {}, null, 2);
}

const ObjectIDSelector = ({
  datasets,
  apiKeys,
	agents,
	chats,
  providers,
  instances,
  form,
  instanceOptionsLoading,
  instanceModels,
  instanceModelsLoading,
  selectedInstance,
  update,
  updateInstanceForm,
}: {
  datasets: DatasetRow[];
  apiKeys: APIKeyRow[];
	agents: AgentRow[];
	chats: ChatRow[];
  providers: ProviderRow[];
  instances: InstanceRow[];
  form: ApprovalForm;
  instanceOptionsLoading: boolean;
  instanceModels: InstanceModelRow[];
  instanceModelsLoading: boolean;
  selectedInstance?: InstanceRow;
  update: <K extends keyof ApprovalForm>(key: K, value: ApprovalForm[K]) => void;
  updateInstanceForm: (value: InstanceForm) => void;
}) => {
  const t = useTranslate();

  if (form.object_type === "dataset" && form.action === "create") {
    return (
      <div className="space-y-2">
        <Label htmlFor="approval-object-id">{t("approvals.object_id")}</Label>
        <Input id="approval-object-id" value={form.object_id} disabled />
        <p className="text-xs text-muted-foreground">{t("approvals.create.auto_object_id")}</p>
      </div>
    );
  }

  if (form.object_type === "agent" && form.action === "create") {
    return (
      <div className="space-y-2">
        <Label htmlFor="approval-object-id">{t("approvals.object_id")}</Label>
        <Input id="approval-object-id" value={form.object_id} disabled />
        <p className="text-xs text-muted-foreground">{t("approvals.create.auto_object_id")}</p>
      </div>
    );
  }

  if (form.object_type === "chat" && form.action === "create") {
    return (
      <div className="space-y-2">
        <Label htmlFor="approval-object-id">{t("approvals.object_id")}</Label>
        <Input id="approval-object-id" value={form.object_id} disabled />
        <p className="text-xs text-muted-foreground">{t("approvals.create.auto_object_id")}</p>
      </div>
    );
  }

  if (form.object_type === "chat") {
    return (
      <div className="space-y-2">
        <RequiredLabel htmlFor="approval-chat">{t("approvals.object_id")}</RequiredLabel>
        <select
          id="approval-chat"
          className="w-full rounded border bg-background px-2 py-1 text-sm"
          value={form.object_id}
          onChange={(event) => update("object_id", event.target.value)}
        >
          <option value="">{t("approvals.create.select_object")}</option>
          {chats.map((chat) => (
            <option key={chat.id} value={chat.id}>{chat.name}</option>
          ))}
        </select>
        {chats.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t("approvals.create.no_objects")}</p>
        ) : null}
      </div>
    );
  }

  if (form.object_type === "agent") {
    return (
      <div className="space-y-2">
        <RequiredLabel htmlFor="approval-agent">{t("approvals.object_id")}</RequiredLabel>
        <select
          id="approval-agent"
          className="w-full rounded border bg-background px-2 py-1 text-sm"
          value={form.object_id}
          onChange={(event) => update("object_id", event.target.value)}
        >
          <option value="">{t("approvals.create.select_object")}</option>
          {agents.map((agent) => (
            <option key={agent.id} value={agent.id}>{agent.title}</option>
          ))}
        </select>
      </div>
    );
  }

  if (form.object_type === "dataset") {
    return (
      <div className="space-y-2">
        <RequiredLabel htmlFor="approval-object-id">{t("approvals.object_id")}</RequiredLabel>
        <select
          id="approval-object-id"
          className="w-full rounded border bg-background px-2 py-1 text-sm"
          value={form.object_id}
          onChange={(event) => update("object_id", event.target.value)}
        >
          <option value="">{t("approvals.create.select_object")}</option>
          {datasets.map((dataset) => (
            <option key={dataset.id} value={dataset.id}>
              {dataset.name} ({dataset.id})
            </option>
          ))}
        </select>
        {datasets.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t("approvals.create.no_objects")}</p>
        ) : null}
      </div>
    );
  }

  if (form.object_type === "api-key" && form.action === "revoke") {
    return (
      <div className="space-y-2">
        <RequiredLabel htmlFor="approval-object-id">{t("approvals.object_id")}</RequiredLabel>
        <select
          id="approval-object-id"
          className="w-full rounded border bg-background px-2 py-1 text-sm"
          value={form.object_id}
          onChange={(event) => update("object_id", event.target.value)}
        >
          <option value="">{t("approvals.create.select_object")}</option>
          {apiKeys.map((apiKey) => (
            <option key={apiKey.id} value={apiKey.id}>
              {apiKey.name} ({apiKey.id})
            </option>
          ))}
        </select>
        {apiKeys.length === 0 ? (
          <p className="text-xs text-muted-foreground">{t("approvals.create.no_objects")}</p>
        ) : null}
      </div>
    );
  }

  if (form.object_type === "model-instance") {
    const providerID = form.object_id.split(":", 1)[0];
    const instanceID = form.action === "create" ? "" : form.object_id.slice(providerID.length + 1);
    const needsInstance = form.action !== "create";
    return (
      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-2">
          <RequiredLabel htmlFor="approval-provider">{t("approvals.create.provider")}</RequiredLabel>
          <select
            id="approval-provider"
            className="w-full rounded border bg-background px-2 py-1 text-sm"
            value={providerID}
            onChange={(event) => {
              update("object_id", needsInstance ? event.target.value : `new:${event.target.value}:`);
              updateInstanceForm(EMPTY_INSTANCE_FORM);
            }}
          >
            <option value="">{t("approvals.create.select_object")}</option>
            {providers.map((provider) => (
              <option key={provider.id} value={provider.id}>
                {provider.name}
              </option>
            ))}
          </select>
        </div>
        <div className="space-y-2">
          {needsInstance ? (
            <>
              <RequiredLabel htmlFor="approval-instance">{t("approvals.create.instance")}</RequiredLabel>
              {instanceOptionsLoading ? (
                <Skeleton className="h-9 w-full" />
              ) : (
                <select
                  id="approval-instance"
                  className="w-full rounded border bg-background px-2 py-1 text-sm"
                  value={instanceID}
                  disabled={!providerID}
                  onChange={(event) => {
                    update("object_id", `${providerID}:${event.target.value}`);
                    const instance = instances.find((item) => item.id === event.target.value);
                    updateInstanceForm({
                      instance_name: instance?.instance_name ?? "",
                      api_key: "",
                      base_url: instance?.base_url ?? "",
                      region: instance?.region || "default",
                    });
                  }}
                >
                  <option value="">{t("approvals.create.select_object")}</option>
                  {instances.map((instance) => (
                    <option key={instance.id} value={instance.id}>
                      {instance.instance_name}
                    </option>
                  ))}
                </select>
              )}
            </>
          ) : null}
        </div>
        {selectedInstance && needsInstance ? (
          <p className="text-xs text-muted-foreground md:col-span-2">
            {t("approvals.create.generated_object_id", { value: form.object_id })}
          </p>
        ) : null}
      </div>
    );
  }

  if (form.object_type === "model-model") {
    const providerID = form.object_id.split(":", 1)[0];
    const instanceID = form.object_id.includes(":") ? form.object_id.split(":")[1] ?? "" : "";
    const modelID = form.object_id.split(":")[2] ?? "";
    return (
      <div className="grid gap-4 md:grid-cols-3">
        <div className="space-y-2">
          <RequiredLabel htmlFor="approval-model-provider">{t("approvals.create.provider")}</RequiredLabel>
          <select
            id="approval-model-provider"
            className="w-full rounded border bg-background px-2 py-1 text-sm"
            value={providerID}
            onChange={(event) => update("object_id", form.action === "create" ? `new:${event.target.value}:` : event.target.value)}
          >
            <option value="">{t("approvals.create.select_object")}</option>
            {providers.map((provider) => (
              <option key={provider.id} value={provider.id}>{provider.name}</option>
            ))}
          </select>
        </div>
        <div className="space-y-2">
          <RequiredLabel htmlFor="approval-model-instance">{t("approvals.create.instance")}</RequiredLabel>
          {instanceOptionsLoading ? (
            <Skeleton className="h-9 w-full" />
          ) : (
            <select
              id="approval-model-instance"
              className="w-full rounded border bg-background px-2 py-1 text-sm"
              value={instanceID}
              disabled={!providerID}
              onChange={(event) => update("object_id", form.action === "create" ? `new:${providerID}:${event.target.value}` : `${providerID}:${event.target.value}`)}
            >
              <option value="">{t("approvals.create.select_object")}</option>
              {instances.map((instance) => (
                <option key={instance.id} value={instance.id}>{instance.instance_name}</option>
              ))}
            </select>
          )}
        </div>
        <div className="space-y-2">
          {form.action === "create" ? (
            <Label htmlFor="approval-model-model">{t("approvals.create.model_name")}</Label>
          ) : (
            <RequiredLabel htmlFor="approval-model-model">{t("approvals.create.model_name")}</RequiredLabel>
          )}
          {instanceModelsLoading ? (
            <Skeleton className="h-9 w-full" />
          ) : (
            <select
              id="approval-model-model"
              className="w-full rounded border bg-background px-2 py-1 text-sm"
              value={modelID}
              disabled={!instanceID || form.action === "create"}
              onChange={(event) => update("object_id", `new:${providerID}:${instanceID}:${event.target.value}`)}
            >
              <option value="">{form.action === "create" ? t("approvals.create.auto_object_id") : t("approvals.create.select_object")}</option>
              {instanceModels.map((instanceModel) => (
                <option key={instanceModel.id} value={instanceModel.id}>{instanceModel.model_name}</option>
              ))}
            </select>
          )}
        </div>
      </div>
    );
  }

  if (form.object_type === "model-provider" && form.action === "delete") {
    return (
      <div className="space-y-2">
        <RequiredLabel htmlFor="approval-provider-delete">{t("approvals.create.provider")}</RequiredLabel>
        <select
          id="approval-provider-delete"
          className="w-full rounded border bg-background px-2 py-1 text-sm"
          value={form.object_id}
          onChange={(event) => update("object_id", event.target.value)}
        >
          <option value="">{t("approvals.create.select_object")}</option>
          {providers.map((provider) => (
            <option key={provider.id} value={provider.id}>{provider.name}</option>
          ))}
        </select>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <Label htmlFor="approval-object-id">{t("approvals.object_id")}</Label>
      <Input id="approval-object-id" value={form.object_id} disabled />
      <p className="text-xs text-muted-foreground">{t("approvals.create.auto_object_id")}</p>
    </div>
  );
};
