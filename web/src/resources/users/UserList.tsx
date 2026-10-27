import { useState } from "react";
import { DataTable, DateInput, FilterForm, FilterButton, List, ListLoadingBar, SearchInput, SelectInput, TextInput } from "@/components/admin";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import {
  minLength,
  required,
  useCanAccess,
  useGetList,
  useNotify,
  useRecordContext,
  useTranslate,
} from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import type { ChangeEvent } from "react";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Loader2, Pencil, Trash2, Users } from "lucide-react";
import { CreateDialog } from "../../components/CreateDialog";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { FormField, useFieldValidation } from "../../components/FormField";
import { api, ApiError } from "../../lib/api";
import { validateEmailFormat, validateUniqueUsername } from "./user-validators";

interface RoleLike {
  id: string;
  name: string;
}

interface TenantLike {
  id: string;
  name: string;
}

const RoleSelectField = () => {
  const { data } = useGetList("roles", {
    pagination: { page: 1, perPage: 200 },
  });
  const choices = (data ?? []).map((r: RoleLike) => ({ id: r.id, name: r.name }));
  const t = useTranslate();
  return <SelectInput source="role" label={t("users.role")} choices={choices} validate={required()} />;
};

const TenantSelectField = () => {
  const { data } = useGetList("tenants", {
    pagination: { page: 1, perPage: 200 },
  });
  const choices = (data ?? []).map((t: TenantLike) => ({ id: t.id, name: t.name }));
  const tr = useTranslate();
  return <SelectInput source="tenant_id" label={tr("users.tenant")} choices={choices} />;
};

const UserActions = () => {
  const t = useTranslate();
  return (
  <CreateDialog resource="users" title={t("users.create_title")}>
    <TenantSelectField />
    <TextInput
      source="password"
      label={t("users.password")}
      type="password"
      validate={[required(), minLength(8)]}
    />
    <TextInput
      source="username"
      label={t("users.username")}
      validate={[required(), (value?: string) => validateUniqueUsername(value, t("users.username_taken"))]}
    />
    <TextInput
      source="email"
      label={t("users.email")}
      validate={(value?: string) => validateEmailFormat(value) ? t("users.email_invalid") : undefined}
    />
    <RoleSelectField />
  </CreateDialog>
  );
};

const invalidateUsers = (qc: ReturnType<typeof useQueryClient>) =>
  qc.invalidateQueries({ queryKey: ["users"] });

const RoleCell = () => {
  const record = useRecordContext<{ id: string; role: string }>();
  const notify = useNotify();
  const qc = useQueryClient();
  const t = useTranslate();
  const { data } = useGetList("roles", {
    pagination: { page: 1, perPage: 200 },
  });
  const roles = (data ?? []) as RoleLike[];
  if (!record) return null;
  const onChange = async (e: ChangeEvent<HTMLSelectElement>) => {
    try {
      await api.put(`/users/${record.id}/role`, { role: e.target.value });
      notify(t("users.updated"), { type: "success" });
      invalidateUsers(qc);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("users.update_fail"), { type: "error" });
    }
  };
  return (
    <select
      value={record.role}
      onChange={onChange}
      className="rounded border bg-background px-2 py-1 text-sm"
      aria-label={t("users.aria_select_role")}
    >
      {roles.map((r) => (
        <option key={r.id} value={r.id}>
          {r.name}
        </option>
      ))}
    </select>
  );
};

const StatusCell = () => {
  const record = useRecordContext<{ id: string; status: string }>();
  const notify = useNotify();
  const qc = useQueryClient();
  const t = useTranslate();
  if (!record) return null;
  const onChange = async (checked: boolean) => {
    const status = checked ? "active" : "disabled";
    try {
      await api.put(`/users/${record.id}/status`, { status });
      notify(t("users.status_updated"), { type: "success" });
      invalidateUsers(qc);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("users.update_fail"), { type: "error" });
    }
  };
  return (
    <Switch
      checked={record.status === "active"}
      onCheckedChange={onChange}
      aria-label={record.status === "active" ? t("users.aria_toggle_status_off") : t("users.aria_toggle_status_on")}
    />
  );
};

const EditUserCell = () => {
  const record = useRecordContext<{
    id: string;
    username: string;
    email: string;
    role: string;
    tenant_name?: string;
  }>();
  const notify = useNotify();
  const qc = useQueryClient();
  const t = useTranslate();
  const { data } = useGetList("roles", {
    pagination: { page: 1, perPage: 200 },
  });
  const roles = (data ?? []) as RoleLike[];
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const usernameField = useFieldValidation(record?.username ?? "", { required: true });
  const emailField = useFieldValidation(record?.email ?? "");
  const [role, setRole] = useState(record?.role ?? "");
  const passwordField = useFieldValidation("");

  if (!record) return null;
  const save = async () => {
    if (!usernameField.validate()) return;
    setSaving(true);
    try {
      await api.put(`/users/${record.id}`, {
        username: usernameField.value,
        email: emailField.value,
        role,
        password: passwordField.value || undefined,
      });
      notify(t("users.updated"), { type: "success" });
      setOpen(false);
      invalidateUsers(qc);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("users.update_fail"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <IconButtonWithTooltip label={t("users.edit_label")} onClick={() => setOpen(true)}>
        <Pencil className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("users.edit_title")} {record.username || record.id}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label>{t("users.tenant")}</Label>
              <div className="rounded border bg-muted px-3 py-2 text-sm">
                {record.tenant_name || t("users.unspecified_workspace")}
              </div>
            </div>
            <FormField label={t("users.username")} required error={usernameField.error}>
              <Input {...usernameField.inputProps} aria-required="true" />
            </FormField>
            <FormField label={t("users.email")} error={emailField.error}>
              <Input {...emailField.inputProps} type="email" />
            </FormField>
            <div className="space-y-2">
              <Label>{t("users.role")}</Label>
              <select
                className="w-full rounded border bg-background px-2 py-1 text-sm"
                value={role}
                onChange={(e) => setRole(e.target.value)}
                aria-label={t("users.select_role")}
              >
                {roles.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.name}
                  </option>
                ))}
              </select>
            </div>
            <FormField label={t("users.reset_password")} hint={t("users.reset_password_hint")}>
              <Input {...passwordField.inputProps} type="password" />
            </FormField>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>
                {t("ra.action.cancel")}
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

const DeleteUserCell = () => {
  const record = useRecordContext<{ id: string; role: string; username: string }>();
  const notify = useNotify();
  const qc = useQueryClient();
  const t = useTranslate();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  if (!record) return null;
  if (record.role === "platform_admin") return null;
  const doDelete = async () => {
    setDeleting(true);
    try {
      await api.delete(`/users/${record.id}`);
      notify(t("users.deleted"), { type: "success" });
      invalidateUsers(qc);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("users.delete_fail"), { type: "error" });
    } finally {
      setDeleting(false);
      setConfirmOpen(false);
    }
  };
  return (
    <>
      <IconButtonWithTooltip label={t("users.delete_label")} onClick={() => setConfirmOpen(true)} className="text-destructive!">
        <Trash2 className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t("confirm.delete_title")}
        message={`确定删除用户「${record.username || record.id}」吗？此操作不可撤销。`}
        confirmLabel={t("confirm.delete_label")}
        onConfirm={() => void doDelete()}
        loading={deleting}
      />
    </>
  );
};

const EmptyUserGuidance = () => {
  const t = useTranslate();
  return (
  <div className="flex flex-col items-center gap-3 rounded border border-dashed p-8 text-center">
    <Users className="size-10 text-muted-foreground/50" />
    <div>
      <p className="text-sm font-medium">{t("users.empty")}</p>
      <p className="mt-1 text-xs text-muted-foreground">
        {t("users.empty_hint")}
      </p>
    </div>
  </div>
  );
};

export const UserList = () => {
  const t = useTranslate();
  const { canAccess: canFilterTenant } = useCanAccess({ resource: "user", action: "governance.read" });
  const statusChoices = [
    { id: "active", name: t("users.status_active") },
    { id: "disabled", name: t("users.status_disabled") },
  ];
  const roleFilterChoices = [
    { id: "platform_admin", name: t("users.role_platform_admin") },
    { id: "tenant_admin", name: t("users.role_tenant_admin") },
    { id: "operator", name: t("users.role_operator") },
    { id: "business_user", name: t("users.role_business_user") },
    { id: "viewer", name: t("users.role_viewer") },
    { id: "team_admin", name: t("users.role_team_admin") },
  ];
  const userFilters = [
    <SearchInput source="username" key="username" alwaysOn />,
    <SelectInput source="status" label={t("users.filter_status")} choices={statusChoices} key="status" />,
    <SelectInput source="role" label={t("users.filter_role")} choices={roleFilterChoices} key="role" />,
    <DateInput source="created_from" label={t("users.filter_created_from")} key="created_from" />,
    <DateInput source="created_to" label={t("users.filter_created_to")} key="created_to" />,
  ];
  if (canFilterTenant) {
    userFilters.push(<TenantSelectField key="tenant_id" />);
  }
  return (
    <List perPage={20} actions={<UserActions />} filters={userFilters} aria-label={t("users.aria_user_list")}>
      <ListLoadingBar />
      <DataTable aria-label={t("users.aria_user_table")} empty={<EmptyUserGuidance />}>
        <DataTable.Col source="username" label={t("users.username")} />
        <DataTable.Col source="email" label={t("users.email")} className="hidden md:table-cell" />
        <DataTable.Col
          source="tenant_name"
          label={t("users.tenant")}
          className="hidden md:table-cell"
          render={(r) => r.tenant_name ?? "-"}
        />
        <DataTable.Col source="role" label={t("users.role")} disableSort>
          <RoleCell />
        </DataTable.Col>
        <DataTable.Col source="status" label={t("users.status")} disableSort>
          <StatusCell />
        </DataTable.Col>
        <DataTable.Col label="">
          <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
            <EditUserCell />
            <DeleteUserCell />
          </div>
        </DataTable.Col>
        <DataTable.Col
          source="created_at"
          label={t("users.created_at")}
          render={(record) => new Date(record.created_at).toLocaleString()}
        />
      </DataTable>
    </List>
  );
};
