import { useEffect, useState } from "react";
import { DataTable, List, ListLoadingBar } from "@/components/admin";
import { useCanAccess, useNotify, useRecordContext, useRefresh, useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Plus, Trash2 } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { NumericRangeField } from "@/components/NumericRangeField";
import { clampInteger } from "@/lib/numeric";
import {
  APPROVAL_ACTIONS,
  APPROVAL_APPROVER_TYPES,
  APPROVAL_CONDITION_OPS,
  APPROVAL_OBJECTS,
  parseJSONValue,
} from "./approval-ui";
import {
  type ApprovalPolicyCondition,
  type ApprovalPolicyRecord,
  type ApprovalPolicyStep,
} from "./approval-types";

const defaultPolicy: ApprovalPolicyRecord = {
  object_type: "dataset",
  action: "delete",
  enabled: true,
  priority: 100,
  conditions_json: "{}",
  steps_json: "[]",
  expire_hours: 72,
};

type ApproverOption = { id: string; label: string };

const approverOptionsFor = (options: {
  users: ApproverOption[];
  roles: ApproverOption[];
  teams: ApproverOption[];
}, type: string) =>
  type === "role" ? options.roles
    : type === "team" ? options.teams
      : options.users;

const parseConditionRows = (raw: string): ApprovalPolicyCondition[] => {
  const conditions = parseJSONValue<{ all?: Array<{ field: string; op: string; value?: unknown }> }>(raw, {});
  return (conditions.all ?? []).map((condition) => {
    const value = condition.value;
    const text = Array.isArray(value)
      ? value.map(String).join(",")
      : value === undefined || value === null ? "" : String(value);
    return { field: condition.field, op: condition.op, value: text };
  });
};

const parseStepRows = (raw: string): ApprovalPolicyStep[] =>
  parseJSONValue<ApprovalPolicyStep[]>(raw, []).map((step, index) => ({
    ...step,
    step_no: step.step_no || index + 1,
    expire_hours: step.expire_hours || 72,
    approval_mode: step.approval_mode || "any",
    approvers: step.approvers?.length
      ? step.approvers.map((approver) => ({ type: approver.type, value: approver.value }))
      : [{ type: step.approver_type, value: step.approver_value }],
    required_approvals: step.approval_mode === "all"
      ? step.required_approvals || step.approvers?.length || 1
      : 1,
  }));

const parseConditionValue = (value: string) => {
  if (value === "true") return true;
  if (value === "false") return false;
  if (/^-?\d+(?:\.\d+)?$/.test(value)) return Number(value);
  return value;
};

const buildConditionsJSON = (rows: ApprovalPolicyCondition[]) => {
  const all = rows
    .filter((row) => row.field.trim())
    .map((row) => {
      if (row.op === "exists") return { field: row.field.trim(), op: row.op };
      const value = row.op === "in"
        ? row.value.split(",").map((item) => parseConditionValue(item.trim()))
        : parseConditionValue(row.value);
      return { field: row.field.trim(), op: row.op, value };
    });
  return JSON.stringify({ all });
};

const updateStep = (
  steps: ApprovalPolicyStep[],
  index: number,
  changes: Partial<ApprovalPolicyStep>,
): ApprovalPolicyStep[] =>
  steps.map((step, stepIndex) => {
    if (stepIndex !== index) return step;
    const next = { ...step, ...changes };
    if (next.approval_mode === "any") next.required_approvals = 1;
    if (next.approval_mode === "all" && !next.required_approvals) {
      next.required_approvals = next.approvers?.length || 1;
    }
    return next;
  });

const PolicyDialog = ({
  open,
  initial,
  onClose,
}: {
  open: boolean;
  initial?: ApprovalPolicyRecord;
  onClose: () => void;
}) => {
  const notify = useNotify();
  const refresh = useRefresh();
  const t = useTranslate();
  const [form, setForm] = useState<ApprovalPolicyRecord>(initial ?? defaultPolicy);
  const [conditions, setConditions] = useState<ApprovalPolicyCondition[]>(
    parseConditionRows((initial ?? defaultPolicy).conditions_json),
  );
  const [steps, setSteps] = useState<ApprovalPolicyStep[]>(
    parseStepRows((initial ?? defaultPolicy).steps_json),
  );
  const [saving, setSaving] = useState(false);
  const [approverOptions, setApproverOptions] = useState({
    users: [] as ApproverOption[],
    roles: [] as ApproverOption[],
    teams: [] as ApproverOption[],
  });

  useEffect(() => {
    if (!open) return;
    let cancelled = false;

    const loadOptions = async () => {
        try {
        const [users, roles, teams] = await Promise.all([
          api.get<unknown>("/users", { params: { page: 1, page_size: 200 } }),
          api.get<unknown>("/roles"),
          api.get<unknown>("/teams"),
        ]);
        const items = (response: unknown): Array<Record<string, unknown>> => {
          const value = (response as { data?: { data?: unknown } } | undefined)?.data?.data;
          if (Array.isArray(value)) return value as Array<Record<string, unknown>>;
          if (value && typeof value === "object" && Array.isArray((value as { items?: unknown }).items)) {
            return (value as { items: Array<Record<string, unknown>> }).items;
          }
          return [];
        };
        const toOptions = (rows: Array<Record<string, unknown>>, labelKey: "username" | "name"): ApproverOption[] =>
          rows.map((row) => ({
            id: String(row.id ?? ""),
            label: String(row[labelKey] ?? row.id ?? ""),
          })).filter((option) => option.id);

        const userOptions = toOptions(items(users), "username");
        const roleOptions = toOptions(items(roles), "name");
        const teamOptions = toOptions(items(teams), "name");
        if (!cancelled) {
          setApproverOptions({
            users: userOptions,
            roles: roleOptions,
            teams: teamOptions,
          });
        }
      } catch (error) {
        if (!cancelled) {
          notify(error instanceof ApiError ? error.displayMessage : t("approvals.policies.load_failed"), { type: "error" });
        }
      }
    };

    void loadOptions();
    return () => {
      cancelled = true;
    };
  }, [open]);
  const update = (key: keyof ApprovalPolicyRecord, value: ApprovalPolicyRecord[keyof ApprovalPolicyRecord]) =>
    setForm((current) => ({ ...current, [key]: value }));

  const validate = () => {
    if (!form.priority || form.priority < 1 || form.priority > 1000) return t("approvals.policies.priority_invalid");
    if (!form.expire_hours || form.expire_hours < 1 || form.expire_hours > 8760) return t("approvals.policies.expire_invalid");
    if (!steps.length) return t("approvals.policies.steps_required");
    for (let index = 0; index < steps.length; index += 1) {
      const step = steps[index];
      if (!step.name.trim() || !step.approvers?.length) {
        return t("approvals.policies.steps_required");
      }
      const approverKeys = new Set<string>();
      for (const approver of step.approvers) {
        if (!approver.type || !approver.value.trim()) {
          return t("approvals.policies.step_required");
        }
        const key = `${approver.type}:${approver.value}`;
        if (approverKeys.has(key)) {
          return t("approvals.policies.approver_duplicate");
        }
        approverKeys.add(key);
      }
      if (step.approval_mode === "all" && step.required_approvals && step.required_approvals > step.approvers.length) {
        return t("approvals.policies.required_invalid");
      }
      if (
        index > 0 &&
        step.approvers.some((current) => current.type === "user") &&
        steps[index - 1].approvers?.some((previous) => previous.type === "user") &&
        step.approvers.some((current) =>
          current.type === "user" &&
          steps[index - 1].approvers?.some((previous) => previous.type === "user" && previous.value === current.value),
        )
      ) {
        return t("approvals.policies.sod_invalid");
      }
    }
    for (const condition of conditions) {
      if (condition.field.trim() && condition.op !== "exists" && !condition.value.trim()) {
        return t("approvals.policies.condition_value_required");
      }
    }
    return "";
  };

  const save = async () => {
    const validationError = validate();
    if (validationError) {
      notify(validationError, { type: "error" });
      return;
    }
    setSaving(true);
    try {
      const payload = {
        id: initial?.id,
        tenant_id: initial?.tenant_id,
        object_type: form.object_type,
        action: form.action,
        enabled: form.enabled,
        priority: clampInteger(form.priority, 1, 1000, 100),
        conditions: parseJSONValue<Record<string, unknown>>(buildConditionsJSON(conditions), { all: [] }),
        expire_hours: clampInteger(form.expire_hours, 1, 8760, 72),
        steps: steps.map((step, index) => {
          const normalized = { ...step, step_no: index + 1 };
          return {
            ...normalized,
            approver_type: normalized.approvers?.[0]?.type || "user",
            approver_value: normalized.approvers?.[0]?.value || "",
          };
        }),
      };
      if (initial?.id) {
        await api.put(`/approval-policies/${initial.id}`, payload);
        notify(t("approvals.policies.updated"), { type: "success" });
      } else {
        await api.post("/approval-policies", payload);
        notify(t("approvals.policies.created"), { type: "success" });
      }
      refresh();
      onClose();
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("approvals.policies.save_fail"), {
        type: "error",
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(value) => !value && onClose()}>
      <DialogContent className="max-h-[90vh] max-w-3xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>
            {initial?.id ? t("approvals.policies.edit_title") : t("approvals.policies.create_title")}
          </DialogTitle>
        </DialogHeader>

        <div className="grid gap-4 md:grid-cols-2">
          <div className="space-y-2">
            <Label>{t("approvals.object_type")} <span className="text-destructive">*</span></Label>
            <Select value={form.object_type} onValueChange={(value) => update("object_type", value)}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                {APPROVAL_OBJECTS.map((value) => (
                  <SelectItem key={value} value={value}>{value}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label>{t("approvals.action")} <span className="text-destructive">*</span></Label>
            <Select value={form.action} onValueChange={(value) => update("action", value)}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                {APPROVAL_ACTIONS.map((value) => (
                  <SelectItem key={value} value={value}>{value}</SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-2">
            <Label htmlFor="policy-priority">{t("approvals.policies.priority")} <span className="text-destructive">*</span></Label>
            <NumericRangeField
              id="policy-priority"
              label={`${t("approvals.policies.priority")} *`}
              value={form.priority}
              min={1}
              max={1000}
              onChange={(next) => update("priority", next ?? 100)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="policy-expire">{t("approvals.policies.expire_hours")} <span className="text-destructive">*</span></Label>
            <NumericRangeField
              id="policy-expire"
              label={`${t("approvals.policies.expire_hours")} *`}
              value={form.expire_hours}
              min={1}
              max={8760}
              unit="h"
              onChange={(next) => update("expire_hours", next ?? 72)}
            />
          </div>
          <div className="flex items-center justify-between rounded-md border p-3 md:col-span-2">
            <div>
              <Label htmlFor="policy-enabled">{t("approvals.policies.enabled")}</Label>
              <p className="text-xs text-muted-foreground">{t("approvals.policies.enabled_hint")}</p>
            </div>
            <Switch id="policy-enabled" checked={form.enabled} onCheckedChange={(value) => update("enabled", value)} />
          </div>
        </div>

        <section className="space-y-3">
          <div className="flex items-center justify-between">
            <h4 className="font-medium">{t("approvals.policies.steps")}</h4>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() =>
                setSteps((current) => [...current, {
                  step_no: current.length + 1,
                  name: "",
                  approver_type: "user",
                  approver_value: "",
                  approval_mode: "any",
                  approvers: [{ type: "user", value: "" }],
                  required_approvals: 1,
                  expire_hours: form.expire_hours,
                }])
              }
            >
              <Plus className="size-4" /> {t("approvals.policies.add_step")}
            </Button>
          </div>
          {steps.map((step, index) => (
            <div key={index} className="space-y-3 rounded-md border p-3">
              <div className="grid gap-2 md:grid-cols-12">
                <Input
                  className="md:col-span-4"
                  value={step.name}
                  onChange={(event) => setSteps((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, name: event.target.value } : item))}
                  placeholder={t("approvals.policies.step_name")}
                  aria-label={t("approvals.policies.step_name")}
                />
                <Select
                  value={step.approval_mode || "any"}
                  onValueChange={(value) => setSteps((current) => updateStep(current, index, {
                    approval_mode: value as ApprovalPolicyStep["approval_mode"],
                    required_approvals: value === "all" ? step.approvers?.length || 1 : 1,
                  }))}
                >
                  <SelectTrigger className="md:col-span-3"><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value="any">{t("approvals.policies.approval_mode.any")}</SelectItem>
                    <SelectItem value="all">{t("approvals.policies.approval_mode.all")}</SelectItem>
                  </SelectContent>
                </Select>
                {step.approval_mode === "all" && (
                  <NumericRangeField
                    className="md:col-span-2"
                    hideRangeHint
                    ariaLabel={t("approvals.policies.required_approvals")}
                    min={1}
                    max={step.approvers?.length || 1}
                    value={step.required_approvals || 1}
                    onChange={(next) => setSteps((current) => updateStep(current, index, { required_approvals: clampInteger(next ?? 1, 1, step.approvers?.length || 1, 1) }))}
                  />
                )}
                <NumericRangeField
                  className="md:col-span-2"
                  hideRangeHint
                  ariaLabel={t("approvals.policies.expire_hours")}
                  min={1}
                  max={8760}
                  value={step.expire_hours}
                  onChange={(next) => setSteps((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, expire_hours: clampInteger(next ?? 72, 1, 8760, 72) } : item))}
                />
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  className="md:col-span-1"
                  onClick={() => setSteps((current) => current.filter((_, itemIndex) => itemIndex !== index))}
                  disabled={steps.length <= 1}
                >
                  <Trash2 className="size-4" />
                </Button>
              </div>
              <div className="space-y-2">
                <div className="flex items-center justify-between">
                  <Label>{t("approvals.policies.approvers")}</Label>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={() => setSteps((current) => updateStep(current, index, {
                      approvers: [...(step.approvers || []), { type: "user", value: "" }],
                    }))}
                  >
                    <Plus className="size-4" /> {t("approvals.policies.add_approver")}
                  </Button>
                </div>
                {(step.approvers || []).map((approver, approverIndex) => (
                  <div key={approverIndex} className="grid gap-2 md:grid-cols-12">
            <Select
              aria-label={t("approvals.policies.approver_type")}
              value={approver.type}
                      onValueChange={(value) => setSteps((current) => updateStep(current, index, {
                        approvers: (step.approvers || []).map((item, itemIndex) =>
                          itemIndex === approverIndex ? { type: value, value: "" } : item,
                        ),
                      }))}
                    >
                      <SelectTrigger className="md:col-span-4"><SelectValue /></SelectTrigger>
                      <SelectContent>
                        {APPROVAL_APPROVER_TYPES.map((value) => (
                          <SelectItem key={value} value={value}>{t(`approvals.policies.approver_type.${value}`)}</SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                    <Select
                      aria-label={t("approvals.policies.approver_value")}
                      value={approver.value || ""}
                      onValueChange={(value) => setSteps((current) => updateStep(current, index, {
                        approvers: (step.approvers || []).map((item, itemIndex) =>
                          itemIndex === approverIndex ? { ...item, value } : item,
                        ),
                      }))}
                    >
                      <SelectTrigger className="md:col-span-7">
                        <SelectValue placeholder={t("approvals.policies.approver_value_placeholder")} />
                      </SelectTrigger>
                      <SelectContent>
                        {(() => {
                          const options = approverOptionsFor(approverOptions, approver.type);
                          const selected = options.find((option) => option.id === approver.value);
                          const selectable = selected ? options : approver.value.trim()
                            ? [{ id: approver.value, label: approver.value }, ...options]
                            : options;
                          return selectable.map((option) => (
                            <SelectItem key={option.id} value={option.id}>{option.label}</SelectItem>
                          ));
                        })()}
                      </SelectContent>
                    </Select>
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      className="md:col-span-1"
                      onClick={() => setSteps((current) => updateStep(current, index, {
                        approvers: (step.approvers || []).filter((_, itemIndex) => itemIndex !== approverIndex),
                      }))}
                      disabled={(step.approvers?.length || 0) <= 1}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                ))}
              </div>
            </div>
          ))}
          <p className="text-xs text-muted-foreground">{t("approvals.policies.approver_hint")}</p>
        </section>

        <section className="space-y-3">
          <div className="flex items-center justify-between">
            <h4 className="font-medium">{t("approvals.policies.conditions")}</h4>
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => setConditions((current) => [...current, { field: "", op: "eq", value: "" }])}
            >
              <Plus className="size-4" /> {t("approvals.policies.add_condition")}
            </Button>
          </div>
          {conditions.map((condition, index) => (
            <div key={index} className="grid gap-2 md:grid-cols-12">
              <Input
                className="md:col-span-4"
                value={condition.field}
                onChange={(event) => setConditions((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, field: event.target.value } : item))}
                placeholder={t("approvals.policies.condition_field")}
                aria-label={t("approvals.policies.condition_field")}
              />
              <Select
                value={condition.op}
                onValueChange={(value) => setConditions((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, op: value } : item))}
              >
                <SelectTrigger className="md:col-span-3"><SelectValue /></SelectTrigger>
                <SelectContent>
                  {APPROVAL_CONDITION_OPS.map((value) => (
                    <SelectItem key={value} value={value}>{value}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Input
                className="md:col-span-4"
                value={condition.value}
                disabled={condition.op === "exists"}
                onChange={(event) => setConditions((current) => current.map((item, itemIndex) => itemIndex === index ? { ...item, value: event.target.value } : item))}
                placeholder={t("approvals.policies.condition_value")}
                aria-label={t("approvals.policies.condition_value")}
              />
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="md:col-span-1"
                onClick={() => setConditions((current) => current.filter((_, itemIndex) => itemIndex !== index))}
              >
                <Trash2 className="size-4" />
              </Button>
            </div>
          ))}
        </section>

        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={saving}>{t("ra.action.cancel")}</Button>
          <Button onClick={save} disabled={saving}>{t("ra.action.save")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
};

const CreatePolicyButton = () => {
  const { canAccess } = useCanAccess({ resource: "approval-policy", action: "manage" });
  const t = useTranslate();
  const [open, setOpen] = useState(false);
  if (!canAccess) return null;
  return (
    <>
      <Button onClick={() => setOpen(true)}>
        <Plus className="size-4" /> {t("approvals.policies.create_title")}
      </Button>
      {open && <PolicyDialog open initial={defaultPolicy} onClose={() => setOpen(false)} />}
    </>
  );
};

const EditPolicyCell = () => {
  const record = useRecordContext<ApprovalPolicyRecord>();
  const { canAccess } = useCanAccess({ resource: "approval-policy", action: "manage" });
  const t = useTranslate();
  const [open, setOpen] = useState(false);
  if (!record || !canAccess) return null;
  return (
    <>
      <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
        {t("approvals.policies.edit_label")}
      </Button>
      {open && <PolicyDialog open initial={record} onClose={() => setOpen(false)} />}
    </>
  );
};

export const ApprovalPoliciesList = () => {
  const t = useTranslate();
  return (
    <List perPage={50} actions={<CreatePolicyButton />}>
      <ListLoadingBar />
      <DataTable empty={t("approvals.policies.empty")}>
        <DataTable.Col source="object_type" label={t("approvals.object_type")} />
        <DataTable.Col source="action" label={t("approvals.action")} />
        <DataTable.Col
          source="enabled"
          label={t("approvals.policies.enabled")}
          render={(record) => (
            <Badge variant={record.enabled ? "default" : "outline"}>
              {record.enabled ? t("approvals.policies.enabled") : t("approvals.policies.disabled")}
            </Badge>
          )}
        />
        <DataTable.Col source="priority" label={t("approvals.policies.priority")} />
        <DataTable.Col source="expire_hours" label={t("approvals.policies.expire_hours")} />
        <DataTable.Col
          source="steps_json"
          label={t("approvals.policies.approval_mode_title")}
          render={(record) => {
            const policySteps = parseStepRows(record.steps_json || "[]");
            return policySteps.some((step) => step.approval_mode === "all")
              ? t("approvals.policies.approval_mode.all")
              : t("approvals.policies.approval_mode.any");
          }}
        />
        <DataTable.Col source="version" label={t("approvals.policies.version")} />
        <DataTable.Col label={t("approvals.policies.edit_label")}>
          <EditPolicyCell />
        </DataTable.Col>
      </DataTable>
    </List>
  );
};
