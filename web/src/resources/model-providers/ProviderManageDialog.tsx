/**
 * ProviderManageDialog – orchestrates instance/model management for a provider.
 *
 * Sub-components are split into:
 * - provider-types.ts  (types, utils)
 * - provider-ui.tsx    (LabelField, ToggleChip)
 * - InstanceCard.tsx   (instance card with model table)
 */
import { useCallback, useEffect, useState } from "react";
import { useTranslate } from "ra-core";
import { Cable } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { ApprovalHoldDialog } from "../approvals/ApprovalHoldDialog";
import { ApprovalHoldError, approvalHoldFromResponse, type ApprovalHold } from "@/lib/approval-hold";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  type InstanceRow,
  type ModelRow,
  type ProviderForm,
  EMPTY_FORM,
  MODEL_TYPES,
  parseExtra,
  typeLabels,
} from "./provider-types";
import { LabelField, ToggleChip } from "./provider-ui";
import { InstanceCardList } from "./InstanceCard";
import { Send } from "lucide-react";
import { NumericRangeField } from "@/components/NumericRangeField";
import { clampInteger } from "@/lib/numeric";

async function req<T>(
  method: string,
  url: string,
  body?: unknown,
  headers?: Record<string, string>,
): Promise<T> {
  const res = await api.request<{
    code: number;
    message: string;
    data: T;
    trace_id?: string;
  }>({
    method,
    url,
    data: body as never,
    headers,
  });
  if (res.data.code !== 0) {
    throw new ApiError(
      res.status ?? 200,
      res.data.code,
      res.data.message || "request failed",
      res.data.trace_id,
    );
  }
  const hold = approvalHoldFromResponse(res);
  if (hold) throw new ApprovalHoldError(hold);
  return res.data.data;
}

function ProviderManageDialog({
  providerId,
  providerName,
  targetTenantID,
  open,
  onOpenChange,
}: {
  providerId: string;
  providerName: string;
  targetTenantID?: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const t = useTranslate();
  const [instances, setInstances] = useState<InstanceRow[]>([]);
  const [modelsByInstance, setModelsByInstance] = useState<
    Record<string, ModelRow[]>
  >({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [newInstanceOpen, setNewInstanceOpen] = useState(false);
  const [newModelFor, setNewModelFor] = useState<string | null>(null);
  const [testFor, setTestFor] = useState<ModelRow | null>(null);
  const [testOut, setTestOut] = useState("");
  const [form, setForm] = useState<ProviderForm>(EMPTY_FORM);
  const [editingInstance, setEditingInstance] = useState<InstanceRow | null>(
    null,
  );
  const [editingModel, setEditingModel] = useState<{
    instanceId: string;
    model: ModelRow;
  } | null>(null);
  const [editForm, setEditForm] = useState<ProviderForm>(EMPTY_FORM);
  const [approvalHold, setApprovalHold] = useState<ApprovalHold | null>(null);
  const workspaceScopeQuery = targetTenantID
    ? `?scope=specific&tenant_id=${encodeURIComponent(targetTenantID)}`
    : "";

  const reload = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const list = await req<InstanceRow[]>(
        "get",
        `/model-providers/${providerId}/instances${workspaceScopeQuery}`,
      );
      setInstances(list);
      const byId: Record<string, ModelRow[]> = {};
      await Promise.all(
        list.map(async (inst) => {
          try {
            byId[inst.id] = await req<ModelRow[]>(
              "get",
              `/model-providers/${providerId}/instances/${inst.id}/models${workspaceScopeQuery}`,
            );
          } catch {
            byId[inst.id] = [];
          }
        }),
      );
      setModelsByInstance(byId);
    } catch (e) {
      setError(e instanceof ApiError ? e.displayMessage : t("providers.instance_load_fail"));
    } finally {
      setLoading(false);
    }
  }, [providerId, workspaceScopeQuery]);

  useEffect(() => {
    if (open) {
      setForm(EMPTY_FORM);
      setNewModelFor(null);
      setTestFor(null);
      setTestOut("");
      void reload();
    }
  }, [open, reload]);

  const toggleType = (t: string) => {
    setForm((f) => ({
      ...f,
      model_type: f.model_type.includes(t)
        ? f.model_type.filter((x) => x !== t)
        : [...f.model_type, t],
    }));
  };

  const toggleEditType = (t: string) => {
    setEditForm((f) => ({
      ...f,
      model_type: f.model_type.includes(t)
        ? f.model_type.filter((x) => x !== t)
        : [...f.model_type, t],
    }));
  };

  const createInstance = async () => {
    setError("");
    try {
      await req(
        "post",
        `/model-providers/${providerId}/instances${workspaceScopeQuery}`,
        {
          instance_name: form.instance_name,
          api_key: form.api_key,
          base_url: form.base_url,
          region: form.region,
          model_info:
            form.model_name.trim() === ""
              ? []
              : [
                  {
                    model_name: form.model_name,
                    model_type:
                      form.model_type.length > 0
                        ? form.model_type
                        : ["chat"],
                    max_tokens: clampInteger(Number(form.max_tokens), 1, 1048576, 8192),
                    is_tools: form.is_tools,
                    thinking: form.thinking,
                  },
                ],
        },
      );
      setNewInstanceOpen(false);
      void reload();
    } catch (e) {
      if (e instanceof ApprovalHoldError) {
        setApprovalHold(e.hold);
        setNewInstanceOpen(false);
        return;
      }
      setError(e instanceof ApiError ? e.displayMessage : t("providers.instance_create_fail"));
    }
  };

  const createModel = async (instanceId: string) => {
    setError("");
    try {
      await req(
        "post",
        `/model-providers/${providerId}/instances/${instanceId}/models${workspaceScopeQuery}`,
        {
          model_name: form.model_name,
          model_type:
            form.model_type.length > 0 ? form.model_type : ["chat"],
          max_tokens: clampInteger(Number(form.max_tokens), 1, 1048576, 8192),
          is_tools: form.is_tools,
          thinking: form.thinking,
        },
      );
      setNewModelFor(null);
      void reload();
    } catch (e) {
      setError(e instanceof ApiError ? e.displayMessage : t("providers.model_add_fail"));
    }
  };

  const deleteInstance = async (id: string) => {
    setError("");
    try {
      await req("delete", `/model-providers/${providerId}/instances${workspaceScopeQuery}`, {
        instances: [id],
      });
      void reload();
    } catch (e) {
      setError(e instanceof ApiError ? e.displayMessage : t("providers.instance_delete_fail"));
    }
  };

  const deleteModel = async (instanceId: string, modelId: string) => {
    setError("");
    try {
      await req(
        "delete",
        `/model-providers/${providerId}/instances/${instanceId}/models${workspaceScopeQuery}`,
        { model_ids: [modelId] },
      );
      void reload();
    } catch (e) {
      setError(e instanceof ApiError ? e.displayMessage : t("providers.model_delete_fail"));
    }
  };

  const openEditInstance = (inst: InstanceRow) => {
    setEditForm({
      ...EMPTY_FORM,
      instance_name: inst.instance_name ?? "",
      base_url: inst.base_url ?? "",
      region: inst.region ?? "default",
    });
    setEditingInstance(inst);
  };

  const saveEditInstance = async () => {
    if (!editingInstance) return;
    setError("");
    try {
      await req(
        "put",
        `/model-providers/${providerId}/instances/${editingInstance.id}${workspaceScopeQuery}`,
        {
          instance_name: editForm.instance_name,
          api_key: editForm.api_key,
          base_url: editForm.base_url,
          region: editForm.region,
          model_info: (
            modelsByInstance[editingInstance.id] ?? []
          ).map((md) => ({
            model_name: md.model_name,
            model_type: typeLabels(md.model_type),
            max_tokens: md.max_tokens ?? 8192,
            is_tools: md.is_tools ?? false,
            thinking: md.thinking ?? false,
          })),
        },
        undefined,
      );
      setEditingInstance(null);
      void reload();
    } catch (e) {
      if (e instanceof ApprovalHoldError) {
        setApprovalHold(e.hold);
        setEditingInstance(null);
        return;
      }
      setError(e instanceof ApiError ? e.displayMessage : t("providers.instance_update_fail"));
    }
  };

  const openEditModel = (instanceId: string, m: ModelRow) => {
    const ex = parseExtra(m.extra_json);
    setEditForm({
      ...EMPTY_FORM,
      model_name: m.model_name ?? "",
      model_type: typeLabels(m.model_type),
      max_tokens: String(ex.max_tokens ?? m.max_tokens ?? 8192),
      is_tools: ex.is_tools ?? m.is_tools ?? false,
      thinking: ex.thinking ?? m.thinking ?? false,
    });
    setEditingModel({ instanceId, model: m });
  };

  const saveEditModel = async () => {
    if (!editingModel) return;
    setError("");
    try {
      await req(
        "patch",
        `/model-providers/${providerId}/instances/${editingModel.instanceId}/models/${editingModel.model.id}${workspaceScopeQuery}`,
        {
          model_type:
            editForm.model_type.length > 0 ? editForm.model_type : ["chat"],
          max_tokens: clampInteger(Number(editForm.max_tokens), 1, 1048576, 8192),
          extra: { is_tools: editForm.is_tools, thinking: editForm.thinking },
        },
      );
      setEditingModel(null);
      void reload();
    } catch (e) {
      setError(e instanceof ApiError ? e.displayMessage : t("providers.model_update_fail"));
    }
  };

  const runTest = async () => {
    if (!testFor) return;
    setError("");
    setTestOut("");
    try {
      const inst = instances.find((i) =>
        (modelsByInstance[i.id] ?? []).some((m) => m.id === testFor.id),
      );
      if (!inst) return;
      const out = await req<string>(
        "post",
        `/model-providers/${providerId}/instances/${inst.id}/models/${testFor.id}/test${workspaceScopeQuery}`,
        { message: "你好，请回复 OK" },
      );
      setTestOut(out);
    } catch (e) {
      setError(e instanceof ApiError ? e.displayMessage : t("providers.test_fail"));
    }
  };

  return (
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="flex max-h-[88vh] flex-col sm:max-w-3xl">
          {approvalHold && <ApprovalHoldDialog hold={approvalHold} onClose={() => setApprovalHold(null)} />}
          <DialogHeader className="shrink-0">
          <DialogTitle className="flex items-center gap-2">
            <Cable className="size-4" />
            {providerName}
          </DialogTitle>
          <DialogDescription>
            {t("providers.manage_desc", { name: providerName })}
          </DialogDescription>
        </DialogHeader>

        <InstanceCardList
          providerId={providerId}
          instances={instances}
          modelsByInstance={modelsByInstance}
          loading={loading}
          error={error}
          onReload={() => void reload()}
          onNewModel={(id) => setNewModelFor(id)}
          onEditInstance={openEditInstance}
          onDeleteInstance={(id) => void deleteInstance(id)}
          onTestModel={(m) => {
            setTestFor(m);
            setTestOut("");
          }}
          onEditModel={openEditModel}
          onDeleteModel={(instId, modelId) =>
            void deleteModel(instId, modelId)
          }
          onNewInstance={() => setNewInstanceOpen(true)}
        />
      </DialogContent>

      {/* New Instance Dialog */}
      <Dialog open={newInstanceOpen} onOpenChange={setNewInstanceOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t("providers.new_instance_title")}</DialogTitle>
            <DialogDescription>{t("providers.new_instance_desc")}</DialogDescription>
          </DialogHeader>
          <div className="grid gap-3">
            <LabelField label={t("providers.instance_name")}>
              <Input
                value={form.instance_name}
                onChange={(e) =>
                  setForm((f) => ({ ...f, instance_name: e.target.value }))
                }
                placeholder={t("providers.instance_name_placeholder")}
              />
            </LabelField>
            <LabelField label={t("providers.api_key")}>
              <Input
                type="password"
                value={form.api_key}
                onChange={(e) =>
                  setForm((f) => ({ ...f, api_key: e.target.value }))
                }
              />
            </LabelField>
            <LabelField label={t("providers.base_url_field")}>
              <Input
                value={form.base_url}
                onChange={(e) =>
                  setForm((f) => ({ ...f, base_url: e.target.value }))
                }
                placeholder={t("providers.base_url_placeholder")}
              />
            </LabelField>
            <LabelField label={t("providers.region")}>
              <Input
                value={form.region}
                onChange={(e) =>
                  setForm((f) => ({ ...f, region: e.target.value }))
                }
                placeholder="default"
              />
            </LabelField>
            <LabelField label={t("providers.model_name")}>
              <Input
                value={form.model_name}
                onChange={(e) =>
                  setForm((f) => ({ ...f, model_name: e.target.value }))
                }
                placeholder="gpt-4o"
              />
            </LabelField>
            {form.model_name.trim() !== "" ? (
              <>
                <LabelField label={t("providers.model_capability")}>
                  <div className="flex flex-wrap gap-2">
                    {MODEL_TYPES.map((mt) => (
                      <ToggleChip
                        key={mt}
                        active={form.model_type.includes(mt)}
                        onClick={() => toggleType(mt)}
                      >
                        {mt}
                      </ToggleChip>
                    ))}
                  </div>
                </LabelField>
                <NumericRangeField
                  label={t("providers.max_token")}
                  value={form.max_tokens}
                  min={1}
                  max={1048576}
                  unit="tokens"
                  onChange={(next) => setForm((f) => ({ ...f, max_tokens: String(next ?? 8192) }))}
                />
                <div className="flex items-center justify-between">
                  <Label>{t("providers.tool_calling_label")}</Label>
                  <Switch
                    checked={form.is_tools}
                    onCheckedChange={(v) =>
                      setForm((f) => ({ ...f, is_tools: v }))
                    }
                  />
                </div>
              </>
            ) : null}
          </div>
          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              onClick={() => setNewInstanceOpen(false)}
            >
              {t("providers.cancel")}
            </Button>
            <Button onClick={() => void createInstance()}>{t("providers.create")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* New Model Dialog */}
      <Dialog
        open={newModelFor !== null}
        onOpenChange={(o) => !o && setNewModelFor(null)}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("providers.add_model")}</DialogTitle>
          </DialogHeader>
          <div className="grid gap-3">
            <LabelField label={t("providers.model_name_only")}>
              <Input
                value={form.model_name}
                onChange={(e) =>
                  setForm((f) => ({ ...f, model_name: e.target.value }))
                }
                placeholder={t("providers.model_name_placeholder")}
              />
            </LabelField>
            <LabelField label={t("providers.model_capability")}>
              <div className="flex flex-wrap gap-2">
                {MODEL_TYPES.map((mt) => (
                  <ToggleChip
                    key={mt}
                    active={form.model_type.includes(mt)}
                    onClick={() => toggleType(mt)}
                  >
                    {mt}
                  </ToggleChip>
                ))}
              </div>
            </LabelField>
            <NumericRangeField
              label={t("providers.max_token")}
              value={form.max_tokens}
              min={1}
              max={1048576}
              unit="tokens"
              onChange={(next) => setForm((f) => ({ ...f, max_tokens: String(next ?? 8192) }))}
            />
            <div className="flex items-center justify-between">
              <Label>{t("providers.tool_calling_label")}</Label>
              <Switch
                checked={form.is_tools}
                onCheckedChange={(v) =>
                  setForm((f) => ({ ...f, is_tools: v }))
                }
              />
            </div>
          </div>
          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={() => setNewModelFor(null)}>
              {t("providers.cancel")}
            </Button>
            <Button
              onClick={() => newModelFor && void createModel(newModelFor)}
            >
              {t("providers.add")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Test Chat Dialog */}
      <Dialog
        open={testFor !== null}
        onOpenChange={(o) => !o && setTestFor(null)}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("providers.test_chat")} {testFor?.model_name ?? ""}</DialogTitle>
          </DialogHeader>
          <div className="min-h-16 whitespace-pre-wrap rounded-md border p-3 text-sm">
            {testOut || t("providers.test_placeholder")}
          </div>
          <DialogFooter>
            <Button onClick={() => void runTest()}>
              <Send className="size-4" /> {t("providers.test_send")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit Instance Dialog */}
      <Dialog
        open={editingInstance !== null}
        onOpenChange={(o) => !o && setEditingInstance(null)}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("providers.edit_instance_title")}</DialogTitle>
          </DialogHeader>
          <div className="grid gap-3">
            <LabelField label={t("providers.instance_name")}>
              <Input
                value={editForm.instance_name}
                onChange={(e) =>
                  setEditForm((f) => ({
                    ...f,
                    instance_name: e.target.value,
                  }))
                }
              />
            </LabelField>
            <LabelField label={t("providers.base_url_field")}>
              <Input
                value={editForm.base_url}
                onChange={(e) =>
                  setEditForm((f) => ({ ...f, base_url: e.target.value }))
                }
              />
            </LabelField>
            <LabelField label={t("providers.region")}>
              <Input
                value={editForm.region}
                onChange={(e) =>
                  setEditForm((f) => ({ ...f, region: e.target.value }))
                }
              />
            </LabelField>
            <LabelField label={t("providers.api_key_optional")}>
              <Input
                type="password"
                value={editForm.api_key}
                onChange={(e) =>
                  setEditForm((f) => ({ ...f, api_key: e.target.value }))
                }
              />
            </LabelField>
          </div>
          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              onClick={() => setEditingInstance(null)}
            >
              {t("providers.cancel")}
            </Button>
            <Button onClick={() => void saveEditInstance()}>{t("providers.save")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Edit Model Dialog */}
      <Dialog
        open={editingModel !== null}
        onOpenChange={(o) => !o && setEditingModel(null)}
      >
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              {t("providers.edit_model_title")} {editingModel?.model.model_name ?? ""}
            </DialogTitle>
          </DialogHeader>
          <div className="grid gap-3">
            <LabelField label={t("providers.model_capability")}>
              <div className="flex flex-wrap gap-2">
                {MODEL_TYPES.map((mt) => (
                  <ToggleChip
                    key={mt}
                    active={editForm.model_type.includes(mt)}
                    onClick={() => toggleEditType(mt)}
                  >
                    {mt}
                  </ToggleChip>
                ))}
              </div>
            </LabelField>
            <NumericRangeField
              label={t("providers.max_token")}
              value={editForm.max_tokens}
              min={1}
              max={1048576}
              unit="tokens"
              onChange={(next) => setEditForm((f) => ({ ...f, max_tokens: String(next ?? 8192) }))}
            />
            <div className="flex items-center justify-between">
              <Label>{t("providers.tool_calling_label")}</Label>
              <Switch
                checked={editForm.is_tools}
                onCheckedChange={(v) =>
                  setEditForm((f) => ({ ...f, is_tools: v }))
                }
              />
            </div>
            <div className="flex items-center justify-between">
              <Label>{t("providers.thinking_label")}</Label>
              <Switch
                checked={editForm.thinking}
                onCheckedChange={(v) =>
                  setEditForm((f) => ({ ...f, thinking: v }))
                }
              />
            </div>
          </div>
          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              onClick={() => setEditingModel(null)}
            >
              {t("providers.cancel")}
            </Button>
            <Button onClick={() => void saveEditModel()}>{t("providers.save")}</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Dialog>
  );
}

export { ProviderManageDialog };
