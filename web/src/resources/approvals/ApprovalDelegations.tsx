import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCanAccess, useGetIdentity, useNotify, useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { UserRoundCog } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { APPROVAL_ACTIONS, APPROVAL_OBJECTS } from "./approval-ui";
import { type ApprovalDelegationRecord } from "./approval-types";
import { UserNameLabel } from "@/components/admin/ReferenceLabel";

const formatDateTime = (value?: string) =>
  value ? new Date(value).toLocaleString() : "-";

const delegationState = (record: ApprovalDelegationRecord) => {
  const now = Date.now();
  if (new Date(record.ends_at).getTime() <= now) return "expired";
  if (new Date(record.starts_at).getTime() > now) return "scheduled";
  return "active";
};

const defaultDateTime = (offsetHours: number) => {
  const value = new Date(Date.now() + offsetHours * 60 * 60 * 1000);
  const pad = (input: number) => String(input).padStart(2, "0");
  return `${value.getFullYear()}-${pad(value.getMonth() + 1)}-${pad(value.getDate())}T${pad(value.getHours())}:${pad(value.getMinutes())}`;
};

const DelegationDialog = ({ onClose }: { onClose: () => void }) => {
  const t = useTranslate();
  const notify = useNotify();
  const qc = useQueryClient();
  const { data: identity } = useGetIdentity();
  const [form, setForm] = useState({
    delegate_id: "",
    object_type: "all",
    action: "all",
    starts_at: defaultDateTime(0),
    ends_at: defaultDateTime(168),
  });
  const [saving, setSaving] = useState(false);
  const query = useQuery({
    queryKey: ["approval-delegations", identity?.id],
    queryFn: async () => {
      const res = await api.get<{ code: number; data: ApprovalDelegationRecord[] }>("/approval-delegations");
      return res.data.data ?? [];
    },
  });

  const create = async () => {
    if (!form.delegate_id.trim()) return;
    setSaving(true);
    try {
      await api.post("/approval-delegations", {
        delegate_id: form.delegate_id.trim(),
        object_type: form.object_type === "all" ? "" : form.object_type,
        action: form.action === "all" ? "" : form.action,
        starts_at: new Date(form.starts_at).toISOString(),
        ends_at: new Date(form.ends_at).toISOString(),
      });
      notify(t("approvals.delegations.created"), { type: "success" });
      setForm((current) => ({ ...current, delegate_id: "" }));
      await qc.invalidateQueries({ queryKey: ["approval-delegations"] });
      await query.refetch();
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("approvals.delegations.create_fail"), {
        type: "error",
      });
    } finally {
      setSaving(false);
    }
  };

  const remove = async (id: string) => {
    try {
      await api.delete(`/approval-delegations/${id}`);
      notify(t("approvals.delegations.deleted"), { type: "success" });
      await qc.invalidateQueries({ queryKey: ["approval-delegations"] });
      await query.refetch();
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("approvals.delegations.delete_fail"), {
        type: "error",
      });
    }
  };

  const stateLabels: Record<string, string> = {
    active: t("approvals.delegations.active"),
    scheduled: t("approvals.delegations.scheduled"),
    expired: t("approvals.delegations.expired"),
  };

  return (
    <DialogContent className="max-h-[90vh] max-w-4xl overflow-y-auto">
      <DialogHeader>
        <DialogTitle>{t("approvals.delegations.title")}</DialogTitle>
      </DialogHeader>

      <div className="grid gap-4 md:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor="delegate-id">{t("approvals.delegations.delegate_id")}</Label>
          <Input
            id="delegate-id"
            value={form.delegate_id}
            onChange={(event) => setForm((current) => ({ ...current, delegate_id: event.target.value }))}
            placeholder={t("approvals.delegations.delegate_placeholder")}
          />
        </div>
        <div className="space-y-2">
          <Label>{t("approvals.object_type")}</Label>
          <Select value={form.object_type} onValueChange={(value) => setForm((current) => ({ ...current, object_type: value }))}>
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t("approvals.delegations.all")}</SelectItem>
              {APPROVAL_OBJECTS.map((value) => (
                <SelectItem key={value} value={value}>{value}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-2">
          <Label>{t("approvals.action")}</Label>
          <Select value={form.action} onValueChange={(value) => setForm((current) => ({ ...current, action: value }))}>
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t("approvals.delegations.all")}</SelectItem>
              {APPROVAL_ACTIONS.map((value) => (
                <SelectItem key={value} value={value}>{value}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="grid grid-cols-2 gap-2 md:col-span-2">
          <div className="space-y-2">
            <Label htmlFor="delegation-starts">{t("approvals.delegations.starts_at")}</Label>
            <Input
              id="delegation-starts"
              type="datetime-local"
              value={form.starts_at}
              onChange={(event) => setForm((current) => ({ ...current, starts_at: event.target.value }))}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="delegation-ends">{t("approvals.delegations.ends_at")}</Label>
            <Input
              id="delegation-ends"
              type="datetime-local"
              value={form.ends_at}
              onChange={(event) => setForm((current) => ({ ...current, ends_at: event.target.value }))}
            />
          </div>
        </div>
      </div>

      <div className="rounded-md border">
        <div className="border-b px-3 py-2 text-sm font-medium">{t("approvals.delegations.list")}</div>
        {query.isLoading ? (
          <p className="p-3 text-sm text-muted-foreground">{t("ra.loading")}</p>
        ) : query.error ? (
          <p className="p-3 text-sm text-destructive">{t("approvals.delegations.load_fail")}</p>
        ) : !query.data?.length ? (
          <p className="p-3 text-sm text-muted-foreground">{t("approvals.delegations.empty")}</p>
        ) : (
          <ul className="divide-y">
            {query.data.map((record) => {
              const state = delegationState(record);
              const canDelete = identity?.id === record.principal_id;
              return (
                <li key={record.id} className="grid gap-2 p-3 text-sm md:grid-cols-12">
                  <span className="md:col-span-3">
                    <span className="text-muted-foreground">{t("approvals.delegations.principal_id")}:</span>{" "}
                    <UserNameLabel id={record.principal_id} />
                  </span>
                  <span className="md:col-span-3">
                    <span className="text-muted-foreground">{t("approvals.delegations.delegate_id")}:</span>{" "}
                    <UserNameLabel id={record.delegate_id} />
                  </span>
                  <span className="md:col-span-2">{record.object_type || t("approvals.delegations.all")}</span>
                  <span className="md:col-span-1">{record.action || t("approvals.delegations.all")}</span>
                  <span className="md:col-span-2">
                    {formatDateTime(record.starts_at)} → {formatDateTime(record.ends_at)}
                  </span>
                  <div className="flex items-center justify-end gap-2 md:col-span-1">
                    <Badge variant={state === "active" ? "default" : "outline"}>{stateLabels[state]}</Badge>
                    {canDelete && (
                      <Button size="sm" variant="outline" onClick={() => void remove(record.id)}>
                        {t("ra.action.delete")}
                      </Button>
                    )}
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </div>

      <DialogFooter>
        <Button variant="outline" onClick={onClose}>{t("ra.action.cancel")}</Button>
        <Button
          onClick={() => void create()}
          disabled={saving || !form.delegate_id.trim() || !form.starts_at || !form.ends_at}
        >
          {t("approvals.delegations.create")}
        </Button>
      </DialogFooter>
    </DialogContent>
  );
};

export const ApprovalDelegationButton = () => {
  const { canAccess } = useCanAccess({ resource: "approval", action: "read" });
  const t = useTranslate();
  const [open, setOpen] = useState(false);
  if (!canAccess) return null;
  return (
    <>
      <Button variant="outline" onClick={() => setOpen(true)}>
        <UserRoundCog className="size-4" /> {t("approvals.delegations.manage")}
      </Button>
      {open && <DelegationDialog onClose={() => setOpen(false)} />}
    </>
  );
};
