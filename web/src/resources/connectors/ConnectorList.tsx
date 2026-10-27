import { useState } from "react";
import { Link } from "react-router-dom";
import { useCanAccess, useNotify, useTranslate } from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import { DataTable, List, ListLoadingBar, SelectInput } from "@/components/admin";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { api, ApiError } from "@/lib/api";
import { ConnectorWizardDialog } from "./ConnectorWizardDialog";
import { approvalHoldFromResponse } from "@/lib/approval-hold";

export type ConnectorRow = {
  id: string;
  name?: string;
  provider?: string;
  adapter_kind?: string;
  credential_version?: number;
  target_dataset_id?: string;
  schedule_kind?: string;
  schedule_value?: number;
  schedule_tz_offset_min?: number;
  enabled?: boolean;
  status?: string;
  last_run_at?: string;
  last_run_status?: string;
  has_credential?: boolean;
  created_by?: string;
};

type StatusVariant = "default" | "secondary" | "destructive" | "outline";

const statusVariant = (status?: string): StatusVariant => {
  if (status === "idle") return "default";
  if (status === "degraded") return "destructive";
  return "outline";
};

const scheduleLabel = (
  row: ConnectorRow,
  t: (key: string, options?: Record<string, unknown>) => string,
): string => {
  if (row.schedule_kind === "interval") {
    return t("connectors.schedule_every_minutes", { count: row.schedule_value ?? 0 });
  }
  if (row.schedule_kind === "daily") {
    const minutes = ((row.schedule_value ?? 0) + (row.schedule_tz_offset_min ?? 0) + 1440) % 1440;
    const hh = String(Math.floor(minutes / 60)).padStart(2, "0");
    const mm = String(minutes % 60).padStart(2, "0");
    return t("connectors.schedule_at_local", { time: `${hh}:${mm}` });
  }
  return t("connectors.schedule_manual");
};

function ConnectorRowActions({ row }: { row: ConnectorRow }) {
  const t = useTranslate();
  const notify = useNotify();
  const qc = useQueryClient();
  const { canAccess: canManage } = useCanAccess({ resource: "connector", action: "manage" });
  const [pending, setPending] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<ConnectorRow | null>(null);

  const refresh = () => qc.invalidateQueries({ queryKey: ["connectors"] });

  const run = async (row: ConnectorRow, action: "sync" | "enable" | "disable" | "preview") => {
    setPending(`${row.id}:${action}`);
    try {
      if (action === "preview") {
        const res = await api.post(`/connectors/${row.id}/preview`, { limit: 20 });
        const data = res.data?.data as
          | { added?: number; changed?: number; deleted?: number; skipped?: number }
          | undefined;
        notify(
          t("connectors.preview_result", {
            added: data?.added ?? 0,
            changed: data?.changed ?? 0,
            deleted: data?.deleted ?? 0,
            skipped: data?.skipped ?? 0,
          }),
          { type: "info" },
        );
      } else {
        const res = await api.post(`/connectors/${row.id}/${action}`, {});
        if (approvalHoldFromResponse(res)) {
          notify(t("connectors.approval_pending"), { type: "info" });
        } else {
          notify(t(`connectors.${action}_queued`), { type: "success" });
        }
        await refresh();
      }
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("connectors.action_failed"), { type: "error" });
    } finally {
      setPending(null);
    }
  };

  const remove = async () => {
    if (!confirmDelete) return;
    try {
      const res = await api.delete(`/connectors/${confirmDelete.id}`);
      if (approvalHoldFromResponse(res)) {
        notify(t("connectors.approval_pending"), { type: "info" });
      } else {
        notify(t("connectors.deleted"), { type: "success" });
      }
      await refresh();
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("connectors.delete_failed"), { type: "error" });
    } finally {
      setConfirmDelete(null);
    }
  };

  return (
    <div className="flex flex-wrap gap-1">
      <Button size="sm" variant="ghost" asChild>
        <Link to={`/connectors/${row.id}/show`}>{t("connectors.detail")}</Link>
      </Button>
      {canManage ? (
        <>
          <Button
            size="sm"
            variant="outline"
            disabled={pending !== null}
            onClick={() => void run(row, "preview")}
          >
            {t("connectors.preview")}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={pending !== null}
            onClick={() => void run(row, "sync")}
          >
            {t("connectors.sync_now")}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={pending !== null}
            onClick={() => void run(row, row.enabled === false || row.status === "disabled" ? "enable" : "disable")}
          >
            {row.enabled === false || row.status === "disabled"
              ? t("connectors.enable")
              : t("connectors.disable")}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(row)}>
            {t("connectors.delete")}
          </Button>
        </>
      ) : null}
      <ConfirmDialog
        open={confirmDelete !== null}
        onOpenChange={(next) => setConfirmDelete(next ? confirmDelete : null)}
        title={t("connectors.delete_title")}
        message={t("connectors.delete_message", { name: confirmDelete?.name ?? "" })}
        confirmLabel={t("connectors.delete")}
        cancelLabel={t("connectors.wizard.cancel")}
        onConfirm={() => void remove()}
      />
    </div>
  );
}

/**
 * ConnectorList – connector bindings with status, schedule, last run and the
 * scoped operations (preview/sync/disable/delete). Runs on the shared
 * ra-core List so pagination/filtering match the rest of the console (doc/225
 * §11.2). The per-row actions receive the row through the DataTable render prop.
 */
export function ConnectorList() {
  const t = useTranslate();
  const filters = [
    <SelectInput
      source="status"
      key="status"
      label={t("connectors.status")}
      choices={[
        { id: "idle", name: t("connectors.status_idle") },
        { id: "degraded", name: t("connectors.status_degraded") },
        { id: "disabled", name: t("connectors.status_disabled") },
      ]}
    />,
  ];

  return (
    <List
      perPage={20}
      filters={filters}
      actions={
        <div className="flex gap-2">
          <ConnectorWizardDialog />
        </div>
      }
      aria-label={t("connectors.list_title")}
    >
      <ListLoadingBar />
      <DataTable
        aria-label={t("connectors.data_table")}
        empty={
          <div className="p-6 text-center text-sm text-muted-foreground">{t("connectors.empty")}</div>
        }
      >
        <DataTable.Col
          source="name"
          label={t("connectors.name")}
          render={(row: ConnectorRow) => (
            <Link className="font-medium underline-offset-2 hover:underline" to={`/connectors/${row.id}/show`}>
              {row.name || row.id}
            </Link>
          )}
        />
        <DataTable.Col source="provider" label={t("connectors.provider")} />
        <DataTable.Col
          source="status"
          label={t("connectors.status")}
          render={(row: ConnectorRow) => (
            <Badge variant={statusVariant(row.status)}>{t(`connectors.status_${row.status ?? "idle"}`)}</Badge>
          )}
        />
        <DataTable.Col
          source="schedule_kind"
          label={t("connectors.schedule")}
          className="hidden md:table-cell"
          render={(row: ConnectorRow) => scheduleLabel(row, t)}
        />
        <DataTable.Col
          source="last_run_at"
          label={t("connectors.last_run")}
          className="hidden lg:table-cell"
          render={(row: ConnectorRow) =>
            row.last_run_at ? new Date(row.last_run_at).toLocaleString() : "—"
          }
        />
        <DataTable.Col source="target_dataset_id" label={t("connectors.target_dataset")} className="hidden xl:table-cell" />
        <DataTable.Col
          source="id"
          label={t("connectors.actions")}
          render={(row: ConnectorRow) => <ConnectorRowActions row={row} />}
        />
      </DataTable>
    </List>
  );
}
