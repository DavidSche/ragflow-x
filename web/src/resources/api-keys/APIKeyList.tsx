import { useState } from "react";
import { DataTable, List, ListLoadingBar, SearchInput, SelectInput } from "@/components/admin";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import { useNotify, useRecordContext } from "ra-core";
import { useTranslate } from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Plus, Trash2, KeyRound } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { ApprovalHoldDialog } from "../approvals/ApprovalHoldDialog";
import { ApprovalHoldError, approvalHoldFromResponse, approvalIdempotencyKey, type ApprovalHold } from "@/lib/approval-hold";
import { CreateDialog } from "../../components/CreateDialog";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { FormField, useFieldValidation } from "../../components/FormField";
import { Input } from "@/components/ui/input";

const CreateKeyDialog = () => {
  const t = useTranslate();
  const notify = useNotify();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const nameField = useFieldValidation("", { required: true });
  const [allowedIPs, setAllowedIPs] = useState("");
  const [secret, setSecret] = useState<string | null>(null);
  const [hold, setHold] = useState<ApprovalHold | null>(null);
  const [creating, setCreating] = useState(false);

  const save = async () => {
    if (!nameField.validate()) return;
    setCreating(true);
    try {
      const res = await api.post<{ code: number; data: { id: string; name: string; secret?: string } }>(
        "/keys",
        {
          name: nameField.value,
          ...(allowedIPs.trim() !== ""
            ? {
                allowed_ips: allowedIPs
                  .split(/[,\n]/)
                  .map((value) => value.trim())
                  .filter(Boolean),
              }
            : {}),
        },
        { headers: { "Idempotency-Key": approvalIdempotencyKey("api-key-create") } },
      );
      const responseHold = approvalHoldFromResponse(res);
      if (responseHold) {
        setHold(responseHold);
        setOpen(false);
        nameField.setValue("");
        setAllowedIPs("");
        return;
      }
      const key = res.data.data;
      if (key.secret) {
        setSecret(key.secret);
      } else {
        notify(t("api_keys.created"), { type: "success" });
        setOpen(false);
        nameField.setValue("");
        qc.invalidateQueries({ queryKey: ["api-keys"] });
      }
    } catch (err) {
      if (err instanceof ApprovalHoldError) {
        setHold(err.hold);
        return;
      }
      notify(err instanceof ApiError ? err.displayMessage : t("api_keys.create_fail"), {
        type: "error",
      });
    } finally {
      setCreating(false);
    }
  };

  if (secret) {
    return (
      <>
        <Button size="sm" onClick={() => setOpen(true)}>
          <Plus /> {t("api_keys.create_btn")}
        </Button>
        <Dialog open={true} onOpenChange={() => setSecret(null)}>
          <DialogContent className="sm:max-w-md">
            <DialogHeader>
              <DialogTitle>{t("api_keys.secret_title")}</DialogTitle>
            </DialogHeader>
            <div className="space-y-3">
              <p className="text-sm text-muted-foreground">
                {t("api_keys.secret_hint")}
              </p>
              <code className="block break-all rounded bg-muted p-3 text-sm font-mono">
                {secret}
              </code>
              <div className="flex justify-end">
                <Button
                  onClick={() => {
                    setSecret(null);
                    setOpen(false);
                    nameField.setValue("");
                    qc.invalidateQueries({ queryKey: ["api-keys"] });
                  }}
                >
                  {t("api_keys.secret_done")}
                </Button>
              </div>
            </div>
          </DialogContent>
        </Dialog>
      </>
    );
  }

  if (hold) {
    return <ApprovalHoldDialog hold={hold} onClose={() => setHold(null)} />;
  }

  return (
    <>
      <Button size="sm" onClick={() => setOpen(true)}>
        <Plus /> {t("api_keys.create_btn")}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("api_keys.create_title")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <FormField label={t("api_keys.key_name")} required error={nameField.error}>
              <Input
                {...nameField.inputProps}
                aria-required="true"
                placeholder={t("api_keys.key_name_placeholder")}
              />
            </FormField>
            <FormField label={t("api_keys.allowed_ips")}>
              <Input
                value={allowedIPs}
                placeholder={t("api_keys.allowed_ips_placeholder")}
                onChange={(event) => setAllowedIPs(event.target.value)}
              />
              <p className="text-xs text-muted-foreground">{t("api_keys.allowed_ips_hint")}</p>
            </FormField>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setOpen(false)}>
                {t("ra.action.cancel")}
              </Button>
              <Button onClick={() => void save()} disabled={creating}>
                {creating ? t("api_keys.creating") : t("api_keys.create_action")}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
};

import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

const StatusToggle = () => {
  const t = useTranslate();
  const record = useRecordContext<{ id: string; enabled: boolean }>();
  const notify = useNotify();
  const qc = useQueryClient();
  if (!record) return null;
  const toggle = async (checked: boolean) => {
    try {
      await api.put(`/keys/${record.id}`, { enabled: checked });
      notify(checked ? t("api_keys.toggle_enable") : t("api_keys.toggle_disable"), { type: "success" });
      qc.invalidateQueries({ queryKey: ["api-keys"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("api_keys.toggle_fail"), { type: "error" });
    }
  };
  return (
    <Switch
      checked={record.enabled}
      onCheckedChange={toggle}
      aria-label={record.enabled ? t("api_keys.toggle_disable_label") : t("api_keys.toggle_enable_label")}
    />
  );
};

const DeleteKeyCell = () => {
  const t = useTranslate();
  const record = useRecordContext<{ id: string; name?: string }>();
  const notify = useNotify();
  const qc = useQueryClient();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [hold, setHold] = useState<ApprovalHold | null>(null);
  if (!record) return null;
  const doDelete = async () => {
    setDeleting(true);
    try {
      const res = await api.delete(`/keys/${record.id}`, {
        headers: { "Idempotency-Key": approvalIdempotencyKey("api-key-revoke") },
      });
      const responseHold = approvalHoldFromResponse(res);
      if (responseHold) {
        setHold(responseHold);
        return;
      }
      notify(t("api_keys.deleted"), { type: "success" });
      qc.invalidateQueries({ queryKey: ["api-keys"] });
    } catch (err) {
      if (err instanceof ApprovalHoldError) {
        setHold(err.hold);
        return;
      }
      notify(err instanceof ApiError ? err.displayMessage : t("api_keys.delete_fail"), {
        type: "error",
      });
    } finally {
      setDeleting(false);
      setConfirmOpen(false);
    }
  };
  return (
    <>
      {hold && <ApprovalHoldDialog hold={hold} onClose={() => setHold(null)} />}
      <IconButtonWithTooltip
        label={t("api_keys.delete_label")}
        onClick={() => setConfirmOpen(true)}
        className="text-destructive!"
      >
        <Trash2 className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t("confirm.delete_title")}
        message={t("api_keys.delete_msg", { name: record.name || record.id })}
        confirmLabel={t("confirm.delete_label")}
        onConfirm={() => void doDelete()}
        loading={deleting}
      />
    </>
  );
};

export const APIKeyList = () => {
  const t = useTranslate();
  const statusChoices = [
    { id: "true", name: t("api_keys.enabled") },
    { id: "false", name: t("api_keys.disabled") },
  ];
  const keyFilters = [
    <SearchInput source="name" key="name" alwaysOn />,
    <SelectInput source="enabled" label={t("api_keys.filter_status")} choices={statusChoices} key="enabled" />,
  ];
  return (
    <List perPage={20} actions={<CreateKeyDialog />} filters={keyFilters} aria-label={t("api_keys.list_title")}>
    <ListLoadingBar />
    <DataTable aria-label={t("api_keys.data_table")}>
      <DataTable.Col source="name" label={t("api_keys.name")} />
      <DataTable.Col
        source="prefix"
        label={t("api_keys.prefix")}
        render={(r) => (
          <code className="rounded bg-muted px-1.5 py-0.5 text-xs font-mono">{r.prefix}</code>
        )}
      />
      <DataTable.Col
        source="enabled"
        label={t("api_keys.status")}
        render={(r) => (
          <Badge variant={r.enabled ? "default" : "destructive"}>
            {r.enabled ? t("api_keys.enabled") : t("api_keys.disabled")}
          </Badge>
        )}
      />
      <DataTable.Col label={t("api_keys.status_toggle")}>
        <StatusToggle />
      </DataTable.Col>
      <DataTable.Col
        source="last_used_at"
        label={t("api_keys.last_used")}
        className="hidden md:table-cell"
        render={(r) => (r.last_used_at ? new Date(r.last_used_at).toLocaleString() : t("api_keys.never_used"))}
      />
      <DataTable.Col
        source="created_at"
        label={t("api_keys.created_at")}
        render={(record) => new Date(record.created_at).toLocaleString()}
      />
      <DataTable.Col label="">
        <DeleteKeyCell />
      </DataTable.Col>
    </DataTable>
  </List>
  );
};
