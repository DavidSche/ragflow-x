import { useState } from "react";
import { DataTable, List, ListLoadingBar, SearchInput, SelectInput, TextInput } from "@/components/admin";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import { required, useCanAccess, useGetList, useNotify, useRecordContext, useTranslate } from "ra-core";
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
import { Badge } from "@/components/ui/badge";
import { Loader2, Pencil, ShieldCheck, Trash2 } from "lucide-react";
import { CreateDialog } from "../../components/CreateDialog";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { FormField, useFieldValidation } from "../../components/FormField";
import { api, ApiError } from "../../lib/api";
import { PermissionMatrix, type CatEntry } from "./PermissionMatrix";
import { RoleLabel } from "@/components/admin/ReferenceLabel";

interface RoleLike {
  id: string;
  name: string;
  scope: string;
  description?: string;
  parent_id?: string;
  builtin?: boolean;
}

interface Perm {
  action: string;
  resource: string;
  effect: string;
}

function useCanManageRole() {
  const { canAccess } = useCanAccess({ resource: "roles", action: "manage" });
  return canAccess === true;
}

function useCanManagePlatformScope() {
  const { canAccess } = useCanAccess({ resource: "tenants", action: "governance.manage" });
  return canAccess === true;
}

function scopeText(t: ReturnType<typeof useTranslate>, s: string) {
  const map: Record<string, string> = {
    platform: t("roles.scope_platform"),
    tenant: t("roles.scope_tenant"),
    project: t("roles.scope_project"),
  };
  return map[s] ?? s;
}

const ParentRoleSelect = ({ currentId }: { currentId?: string }) => {
  const t = useTranslate();
  const { data } = useGetList("roles", { pagination: { page: 1, perPage: 200 } });
  const choices = (data ?? [])
    .filter((r: RoleLike) => r.id !== currentId)
    .map((r: RoleLike) => ({ id: r.id, name: r.name }));
  return <SelectInput source="parent_id" label={t("roles.parent_id")} choices={choices} />;
};

const RoleActions = () => {
  const t = useTranslate();
  const canManage = useCanManageRole();
  const isPlatform = useCanManagePlatformScope();
  if (!canManage) return null;
  const scopeChoices = [
    { id: "platform", name: t("roles.scope_platform_full") },
    { id: "tenant", name: t("roles.scope_tenant_full") },
    { id: "project", name: t("roles.scope_project_full") },
  ];
  const choices = scopeChoices.filter((c) => isPlatform || c.id !== "platform");
  return (
    <CreateDialog resource="roles" title={t("roles.create_title")}>
      <TextInput source="name" label={t("roles.name")} validate={required()} />
      <SelectInput source="scope" label={t("roles.scope")} choices={choices} validate={required()} />
      <TextInput source="description" label={t("roles.description")} />
      <ParentRoleSelect />
    </CreateDialog>
  );
};

const PermissionCell = () => {
  const t = useTranslate();
  const notify = useNotify();
  const record = useRecordContext<RoleLike & { builtin?: boolean }>();
  const canManage = useCanManageRole();
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [catalog, setCatalog] = useState<CatEntry[]>([]);
  const [mapState, setMapState] = useState<Record<string, string>>({});
  if (!record || !canManage) return null;

  const builtin = !!record.builtin;

  const load = async () => {
    setOpen(true);
    setLoading(true);
    try {
      const [catRes, permsRes] = await Promise.all([
        api.get<{ code: number; data: CatEntry[] }>("/roles/permission-catalog"),
        api.get<{ code: number; data: Perm[] }>(`/roles/${record.id}/permissions`),
      ]);
      setCatalog(catRes.data?.data ?? []);
      const m: Record<string, string> = {};
      (permsRes.data?.data ?? []).forEach((p) => {
        m[`${p.resource}|${p.action}`] = p.effect;
      });
      setMapState(m);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("roles.permission_fail"), { type: "error" });
      setOpen(false);
    } finally {
      setLoading(false);
    }
  };

  const save = async () => {
    const perms = Object.entries(mapState)
      .filter(([, v]) => v === "allow" || v === "deny")
      .map(([k, v]) => {
        const sep = k.indexOf("|");
        return { resource: k.slice(0, sep), action: k.slice(sep + 1), effect: v };
      });
    setSaving(true);
    try {
      await api.put(`/roles/${record.id}/permissions`, { permissions: perms });
      notify(t("roles.permission_saved"), { type: "success" });
      setOpen(false);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("roles.permission_fail"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <IconButtonWithTooltip label={t("roles.permission_button")} onClick={(e) => { e.stopPropagation(); void load(); }}>
        <ShieldCheck className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          className="flex max-h-[90vh] flex-col overflow-hidden sm:max-w-4xl"
          style={{ resize: "both", minWidth: "26rem", minHeight: "20rem" }}
        >
          <DialogHeader className="shrink-0">
            <DialogTitle>{t("roles.permission_title", { name: record.name })}</DialogTitle>
          </DialogHeader>
          <div className="min-h-0 flex-1 overflow-y-auto pr-1">
            {loading ? (
              <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                <span>{t("ra.page.loading")}</span>
              </div>
            ) : (
              <div className="space-y-3">
                {builtin ? (
                  <div className="rounded border bg-muted p-3 text-sm text-muted-foreground">
                    {t("roles.builtin_note")}
                  </div>
                ) : (
                  <p className="text-sm text-muted-foreground">{t("roles.no_permissions")}</p>
                )}
                <PermissionMatrix
                  catalog={catalog}
                  mapState={mapState}
                  builtin={builtin}
                  disabled={saving}
                  onChange={builtin ? undefined : (resource, action, effect) =>
                    setMapState((prev) => ({ ...prev, [`${resource}|${action}`]: effect }))
                  }
                />
              </div>
            )}
          </div>
          {!builtin && (
            <div className="flex justify-end gap-2 pt-2 shrink-0">
              <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>
                {t("ra.action.cancel")}
              </Button>
              <Button onClick={() => void save()} disabled={saving}>
                {saving ? <Loader2 className="size-4 animate-spin" /> : t("roles.permission_save")}
              </Button>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
};

const EditRoleCell = () => {
  const t = useTranslate();
  const record = useRecordContext<RoleLike>();
  const notify = useNotify();
  const qc = useQueryClient();
  const canManage = useCanManageRole();
  const { data } = useGetList("roles", { pagination: { page: 1, perPage: 200 } });
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const nameField = useFieldValidation(record?.name ?? "", { required: true });
  const [scope, setScope] = useState(record?.scope ?? "tenant");
  const descriptionField = useFieldValidation(record?.description ?? "");
  const [parentId, setParentId] = useState(record?.parent_id ?? "");

  const isPlatform = useCanManagePlatformScope();
  const scopeChoices = [
    { id: "platform", name: t("roles.scope_platform_full") },
    { id: "tenant", name: t("roles.scope_tenant_full") },
    { id: "project", name: t("roles.scope_project_full") },
  ];
  const scopeOptions = scopeChoices.filter((c) => isPlatform || c.id !== "platform");
  if (!record || !canManage) return null;

  const parentChoices = (data ?? [])
    .filter((r: RoleLike) => r.id !== record.id)
    .map((r: RoleLike) => ({ id: r.id, name: r.name }));

  const save = async () => {
    if (!nameField.validate()) return;
    setSaving(true);
    try {
      await api.put(`/roles/${record.id}`, {
        name: nameField.value,
        scope,
        description: descriptionField.value,
        parent_id: parentId || undefined,
      });
      notify(t("roles.updated"), { type: "success" });
      setOpen(false);
      qc.invalidateQueries({ queryKey: ["roles"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("roles.update_fail"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <IconButtonWithTooltip label={t("roles.edit_label")} onClick={() => setOpen(true)}>
        <Pencil className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("roles.edit_title")} {record.name}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <FormField label={t("roles.name")} required error={nameField.error}>
              <Input {...nameField.inputProps} aria-required="true" />
            </FormField>
            <div className="space-y-2">
              <Label>{t("roles.scope")}</Label>
              <select className="w-full rounded border bg-background px-2 py-1 text-sm" value={scope} onChange={(e) => setScope(e.target.value)}>
                {scopeOptions.map((s) => (
                  <option key={s.id} value={s.id}>{s.name}</option>
                ))}
              </select>
            </div>
            <FormField label={t("roles.description")} error={descriptionField.error}>
              <Input {...descriptionField.inputProps} />
            </FormField>
            <div className="space-y-2">
              <Label>{t("roles.parent_id")}</Label>
              <select className="w-full rounded border bg-background px-2 py-1 text-sm" value={parentId} onChange={(e) => setParentId(e.target.value)}>
                <option value="">{t("roles.parent_none")}</option>
                {parentChoices.map((p) => (
                  <option key={p.id} value={p.id}>{p.name}</option>
                ))}
              </select>
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>{t("ra.action.cancel")}</Button>
              <Button onClick={() => void save()} disabled={saving}>{saving ? <Loader2 className="size-4 animate-spin" /> : t("ra.action.save")}</Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
};

const DeleteRoleCell = () => {
  const t = useTranslate();
  const record = useRecordContext<{ id: string; name?: string }>();
  const notify = useNotify();
  const qc = useQueryClient();
  const canManage = useCanManageRole();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  if (!record || !canManage) return null;
  const doDelete = async () => {
    setDeleting(true);
    try {
      await api.delete(`/roles/${record.id}`);
      notify(t("roles.deleted"), { type: "success" });
      qc.invalidateQueries({ queryKey: ["roles"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("roles.delete_fail"), { type: "error" });
    } finally {
      setDeleting(false);
      setConfirmOpen(false);
    }
  };
  return (
    <>
      <IconButtonWithTooltip label={t("roles.delete_label")} onClick={() => setConfirmOpen(true)} className="text-destructive!">
        <Trash2 className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t("confirm.delete_title")}
        message={t("roles.delete_msg", { name: record.name || record.id })}
        confirmLabel={t("confirm.delete_label")}
        onConfirm={() => void doDelete()}
        loading={deleting}
      />
    </>
  );
};

const EmptyRoleGuidance = () => {
  const t = useTranslate();
  return (
    <div className="flex flex-col items-center gap-3 rounded border border-dashed p-8 text-center">
      <ShieldCheck className="size-10 text-muted-foreground/50" />
      <div>
        <p className="text-sm font-medium">{t("roles.empty")}</p>
        <p className="mt-1 text-xs text-muted-foreground">{t("roles.empty_hint")}</p>
      </div>
    </div>
  );
};

export const RoleList = () => {
  const t = useTranslate();
  const scopeFilterChoices = [
    { id: "platform", name: t("roles.scope_platform_short") },
    { id: "tenant", name: t("roles.scope_tenant_short") },
    { id: "project", name: t("roles.scope_project_short") },
  ];
  const roleFilters = [
    <SearchInput source="name" key="name" alwaysOn />,
    <SelectInput source="scope" label={t("roles.scope")} choices={scopeFilterChoices} key="scope" />,
  ];
  return (
    <List perPage={20} actions={<RoleActions />} filters={roleFilters} aria-label={t("roles.list_title")}>
      <ListLoadingBar />
      <DataTable aria-label={t("roles.data_table")} empty={<EmptyRoleGuidance />}>
        <DataTable.Col source="name" label={t("roles.name")} render={(r) => (
          <span className="flex items-center gap-2">
            {r.name}
            {r.builtin ? <Badge variant="secondary" className="text-xs">{t("roles.builtin")}</Badge> : null}
          </span>
        )} />
        <DataTable.Col source="scope" label={t("roles.scope")} render={(r) => scopeText(t, r.scope)} />
        <DataTable.Col source="description" label={t("roles.description")} className="hidden md:table-cell" />
        <DataTable.Col
          source="parent_id"
          label={t("roles.parent_id")}
          className="hidden md:table-cell"
          render={(r) => <RoleLabel id={r.parent_id} />}
        />
        <DataTable.Col label={t("roles.permissions")}>
          <PermissionCell />
        </DataTable.Col>
        <DataTable.Col label="">
          <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
            <EditRoleCell />
            <DeleteRoleCell />
          </div>
        </DataTable.Col>
      </DataTable>
    </List>
  );
};
