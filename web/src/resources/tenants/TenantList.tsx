import { useState } from "react";
import {
  BulkActionsToolbar,
  DataTable,
  DateInput,
  FilterForm,
  FilterButton,
  List,
  ListLoadingBar,
  SelectAllButton,
  SearchInput,
  SelectInput,
  TextInput,
} from "@/components/admin";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import {
  required,
  useListContext,
  useNotify,
  useRecordContext,
  useTranslate,
  useCanAccess,
} from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Ban, CheckCircle2, Download, Landmark, Loader2, Pencil, Trash2 } from "lucide-react";
import { LogoUploadInput } from "./LogoUploadInput";
import { LogoPickerRow } from "../../components/LogoPickerRow";
import { CreateDialog } from "../../components/CreateDialog";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { FormField, useFieldValidation } from "../../components/FormField";
import { api, ApiError } from "../../lib/api";

const PLATFORM_TENANT_ID = "00000000000000000000000000000000";

interface TenantLike {
  id: string;
  name: string;
  status: string;
  brand_name?: string;
  brand_logo?: string;
  created_at: string;
}

const TenantActions = () => {
  const { canAccess } = useCanAccess({ resource: "tenant", action: "manage" });
  const t = useTranslate();
  if (!canAccess) return null;
  const statusChoices = [
    { id: "active", name: t("tenants.status_active") },
    { id: "disabled", name: t("tenants.status_disabled") },
  ];
  return (
    <CreateDialog resource="tenants" title={t("tenants.create_title")}>
      <TextInput source="name" label={t("tenants.name")} validate={required()} />
      <SelectInput source="status" label={t("tenants.status")} choices={statusChoices} />
      <TextInput source="brand_name" label={t("tenants.brand_name")} />
      <LogoUploadInput source="brand_logo" label="Logo" />
    </CreateDialog>
  );
};

const TenantBulkActions = () => {
  const { selectedIds, onUnselectItems } = useListContext();
  const notify = useNotify();
  const qc = useQueryClient();
  const t = useTranslate();
  const count = selectedIds?.length ?? 0;
  const { canAccess } = useCanAccess({ resource: "tenant", action: "manage" });
  if (!canAccess) return null;

  const setStatus = async (status: "active" | "disabled") => {
    if (!count) return;
    try {
      await api.put("/tenants/status", { ids: selectedIds, status });
      notify(status === "active" ? t("tenants.bulk_enable") : t("tenants.bulk_disable"), { type: "success" });
      onUnselectItems();
      qc.invalidateQueries({ queryKey: ["tenants"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("tenants.bulk_enable_fail"), { type: "error" });
    }
  };

  const doExport = async () => {
    try {
      const res = await api.get("/tenants/export", { responseType: "blob" });
      const url = URL.createObjectURL(res.data as Blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "tenants.csv";
      a.click();
      URL.revokeObjectURL(url);
    } catch {
      notify(t("tenants.export_fail"), { type: "error" });
    }
  };

  return (
    <div className="flex flex-wrap items-center gap-2">
      <SelectAllButton />
      <Button size="sm" variant="outline" onClick={() => setStatus("active")}>
        <CheckCircle2 /> {t("tenants.bulk_enable")}
      </Button>
      <Button size="sm" variant="outline" onClick={() => setStatus("disabled")}>
        <Ban /> {t("tenants.bulk_disable")}
      </Button>
      <Button size="sm" variant="outline" onClick={doExport}>
        <Download /> {t("ra.action.export")}
      </Button>
    </div>
  );
};

const EditTenantCell = () => {
  const record = useRecordContext<TenantLike>();
  const notify = useNotify();
  const qc = useQueryClient();
  const t = useTranslate();
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const nameField = useFieldValidation(record?.name ?? "", { required: true });
  const [status, setStatus] = useState(record?.status ?? "active");
  const brandNameField = useFieldValidation(record?.brand_name ?? "");
  const [brandLogo, setBrandLogo] = useState(record?.brand_logo ?? "");

  const { canAccess } = useCanAccess({ resource: "tenant", action: "manage" });
  if (!canAccess) return null;

  if (!record) return null;

  const statusChoices = [
    { id: "active", name: t("tenants.status_active") },
    { id: "disabled", name: t("tenants.status_disabled") },
  ];

  const save = async () => {
    if (!nameField.validate()) return;
    setSaving(true);
    try {
      await api.put(`/tenants/${record.id}`, {
        name: nameField.value,
        status,
        brand_name: brandNameField.value,
        brand_logo: brandLogo,
      });
      notify(t("tenants.updated"), { type: "success" });
      setOpen(false);
      qc.invalidateQueries({ queryKey: ["tenants"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("tenants.update_fail"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <IconButtonWithTooltip label={t("tenants.edit_label")} onClick={() => setOpen(true)}>
        <Pencil className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("tenants.edit_title")} {record.name}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <FormField label={t("tenants.name")} required error={nameField.error}>
              <Input {...nameField.inputProps} aria-required="true" />
            </FormField>
            <div className="space-y-2">
              <Label>{t("tenants.status")}</Label>
              <select
                className="w-full rounded border bg-background px-2 py-1 text-sm"
                value={status}
                onChange={(e) => setStatus(e.target.value)}
                aria-label={t("tenants.aria_select_status")}
              >
                {statusChoices.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </select>
            </div>
            <FormField label={t("tenants.brand_name")} error={brandNameField.error}>
              <Input {...brandNameField.inputProps} />
            </FormField>
            <div className="space-y-2">
              <Label>Logo</Label>
              <LogoPickerRow value={brandLogo} onChange={setBrandLogo} />
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>
                {t("confirm.cancel")}
              </Button>
              <Button onClick={() => void save()} disabled={saving}>
                {saving ? <Loader2 className="size-4 animate-spin" /> : t("ra.action.save")}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
};

const DeleteTenantCell = () => {
  const record = useRecordContext<TenantLike>();
  const notify = useNotify();
  const qc = useQueryClient();
  const { canAccess } = useCanAccess({ resource: "tenant", action: "manage" });
  const t = useTranslate();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  if (!canAccess) return null;
  if (!record || record.id === PLATFORM_TENANT_ID) return null;
  const doDelete = async () => {
    setDeleting(true);
    try {
      await api.delete(`/tenants/${record.id}`);
      notify(t("tenants.deleted"), { type: "success" });
      qc.invalidateQueries({ queryKey: ["tenants"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("tenants.delete_fail"), { type: "error" });
    } finally {
      setDeleting(false);
      setConfirmOpen(false);
    }
  };
  return (
    <>
      <IconButtonWithTooltip label={t("tenants.delete_label")} onClick={() => setConfirmOpen(true)} className="text-destructive!">
        <Trash2 className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t("confirm.delete_title")}
        message={`确定删除租户「${record.name || record.id}」吗？此操作不可撤销。`}
        confirmLabel={t("confirm.delete_label")}
        onConfirm={() => void doDelete()}
        loading={deleting}
      />
    </>
  );
};

const ForceDeleteCell = () => {
  const record = useRecordContext<TenantLike>();
  const notify = useNotify();
  const qc = useQueryClient();
  const { canAccess } = useCanAccess({ resource: "tenant", action: "manage" });
  const t = useTranslate();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  if (!canAccess) return null;
  if (!record || record.id === PLATFORM_TENANT_ID) return null;

  const forceDelete = async () => {
    setDeleting(true);
    try {
      await api.delete(`/tenants/${record.id}?force=true`);
      notify(t("tenants.force_deleted"), { type: "success" });
      qc.invalidateQueries({ queryKey: ["tenants"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("tenants.delete_fail"), { type: "error" });
    } finally {
      setDeleting(false);
      setConfirmOpen(false);
    }
  };

  return (
    <>
      <IconButtonWithTooltip
        label={t("tenants.force_delete_label")}
        onClick={() => setConfirmOpen(true)}
        className="text-destructive!"
      >
        <Trash2 className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t("tenants.force_delete_title")}
        message={t("tenants.force_delete_msg", { name: record.name || "" })}
        confirmLabel={t("tenants.force_delete_confirm")}
        onConfirm={() => void forceDelete()}
        loading={deleting}
      />
    </>
  );
};

const EmptyTenantGuidance = () => {
  const t = useTranslate();
  return (
  <div className="flex flex-col items-center gap-3 rounded border border-dashed p-8 text-center">
    <Landmark className="size-10 text-muted-foreground/50" />
    <div>
      <p className="text-sm font-medium">{t("tenants.empty")}</p>
      <p className="mt-1 text-xs text-muted-foreground">
        {t("tenants.empty_hint")}
      </p>
    </div>
  </div>
  );
};

export const TenantList = () => {
  const t = useTranslate();
  const statusChoices = [
    { id: "active", name: t("tenants.status_active") },
    { id: "disabled", name: t("tenants.status_disabled") },
  ];
  const tenantFilters = [
    <SearchInput source="name" key="name" alwaysOn />,
    <SelectInput source="status" label={t("tenants.status")} choices={statusChoices} key="status" />,
    <DateInput source="created_from" label={t("tenants.created_at")} key="created_from" />,
    <DateInput source="created_to" label={t("tenants.created_at")} key="created_to" />,
  ];
  return (
    <List
      perPage={20}
      actions={<TenantActions />}
      filters={tenantFilters}
      filterDefaultValues={{ name: "" }}
      aria-label={t("tenants.aria_tenant_list")}
    >
      <ListLoadingBar />
      <DataTable
        bulkActionsToolbar={
          <BulkActionsToolbar>
            <TenantBulkActions />
          </BulkActionsToolbar>
        }
        aria-label={t("tenants.aria_tenant_table")}
        empty={<EmptyTenantGuidance />}
      >
        <DataTable.Col source="name" label={t("tenants.name")} />
        <DataTable.Col
          source="status"
          render={(r) =>
            r.status === "active" ? t("tenants.status_active") : t("tenants.status_disabled")
          }
        />
        <DataTable.Col
          source="brand_name"
          className="hidden md:table-cell"
          render={(r) => r.brand_name ?? "-"}
        />
        <DataTable.Col
          source="created_at"
          render={(record) => new Date(record.created_at).toLocaleString()}
        />
        <DataTable.Col label="">
          <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
            <EditTenantCell />
            <DeleteTenantCell />
            <ForceDeleteCell />
          </div>
        </DataTable.Col>
      </DataTable>
    </List>
  );
};
