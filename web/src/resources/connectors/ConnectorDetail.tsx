import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useNotify, useRecordContext, useTranslate, useCanAccess } from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import { Show } from "@/components/admin";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { api, ApiError } from "@/lib/api";
import { approvalHoldFromResponse } from "@/lib/approval-hold";
import { ConnectorRunsPanel } from "./ConnectorRunsPanel";
import type { ConnectorRow } from "./ConnectorList";

const toHHMM = (minutes?: number) => {
  const m = Math.max(0, Math.min(1439, minutes ?? 0));
  return `${String(Math.floor(m / 60)).padStart(2, "0")}:${String(m % 60).padStart(2, "0")}`;
};

const fromHHMM = (value: string) => {
  const [h, m] = value.split(":").map(Number);
  return (h || 0) * 60 + (m || 0);
};

function DetailBody() {
  const t = useTranslate();
  const notify = useNotify();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const row = useRecordContext<ConnectorRow>();
  const [kind, setKind] = useState<string | null>(null);
  const [value, setValue] = useState(0);
  const [busy, setBusy] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const { canAccess: canManage } = useCanAccess({ resource: "connector", action: "manage" });

  if (!row) return null;
  const scheduleOffset = row.schedule_tz_offset_min ?? 0;
  const connectorDisabled = row.enabled === false || row.status === "disabled";
  const effectiveKind = kind ?? row.schedule_kind ?? "manual";
  const storedValue = row.schedule_value ?? 30;
  const effectiveValue = kind
    ? value
    : effectiveKind === "daily"
      ? (storedValue + scheduleOffset + 1440) % 1440
      : storedValue;

  const refresh = async () => {
    await qc.invalidateQueries({ queryKey: ["connectors"] });
  };

  const call = async (path: string, body: Record<string, unknown> = {}) => {
    setBusy(true);
    try {
      const res = await api.post(`/connectors/${row.id}/${path}`, body);
      await refresh();
      if (approvalHoldFromResponse(res)) {
        notify(t("connectors.approval_pending"), { type: "info" });
      } else {
        notify(t(`connectors.${path}_ok`), { type: "success" });
      }
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("connectors.action_failed"), { type: "error" });
    } finally {
      setBusy(false);
    }
  };

  const saveSchedule = async () => {
    setBusy(true);
    try {
      const schedule =
        effectiveKind === "manual"
          ? { kind: "manual" }
          : effectiveKind === "interval"
            ? { kind: "interval", minutes: effectiveValue }
            : { kind: "daily", hhmm: toHHMM(effectiveValue) };
      const res = await api.put(`/connectors/${row.id}/schedule`, {
        schedule,
        schedule_tz_offset_min: scheduleOffset,
      });
      await refresh();
      setKind(null);
      if (approvalHoldFromResponse(res)) {
        notify(t("connectors.approval_pending"), { type: "info" });
      } else {
        notify(t("connectors.schedule_saved"), { type: "success" });
      }
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("connectors.action_failed"), { type: "error" });
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    try {
      const res = await api.delete(`/connectors/${row.id}`);
      if (approvalHoldFromResponse(res)) {
        notify(t("connectors.approval_pending"), { type: "info" });
      } else {
        notify(t("connectors.deleted"), { type: "success" });
      }
      await refresh();
      navigate("/connectors");
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("connectors.delete_failed"), { type: "error" });
    } finally {
      setBusy(false);
      setConfirmDelete(false);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-2xl font-semibold">{row.name || row.id}</h1>
            <Badge variant={row.status === "degraded" ? "destructive" : "secondary"}>
              {t(`connectors.status_${row.status ?? "idle"}`)}
            </Badge>
          </div>
          <p className="mt-1 text-sm text-muted-foreground">
            {row.provider} · {t("connectors.target_dataset")}:{" "}
            <Link className="underline-offset-2 hover:underline" to={`/datasets/${row.target_dataset_id}/show`}>
              {row.target_dataset_id}
            </Link>
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          {canManage ? (
            <>
              <Button size="sm" variant="outline" disabled={busy} onClick={() => void call("preview")}>
                {t("connectors.preview")}
              </Button>
              <Button size="sm" disabled={busy} onClick={() => void call("sync")}>
                {t("connectors.sync_now")}
              </Button>
              {connectorDisabled ? (
                <Button size="sm" variant="outline" disabled={busy} onClick={() => void call("enable")}>
                  {t("connectors.enable")}
                </Button>
              ) : (
                <Button size="sm" variant="outline" disabled={busy} onClick={() => void call("disable")}>
                  {t("connectors.disable")}
                </Button>
              )}
              <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(true)}>
                {t("connectors.delete")}
              </Button>
            </>
          ) : null}
        </div>
      </div>

      <dl className="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-4">
        <Fact label={t("connectors.last_run")} value={row.last_run_at ? new Date(row.last_run_at).toLocaleString() : "—"} />
        <Fact label={t("connectors.last_run_status")} value={row.last_run_status || "—"} />
        <Fact label={t("connectors.has_credential")} value={row.has_credential ? t("connectors.wizard.yes") : t("connectors.wizard.no")} />
        <Fact
          label={t("connectors.credential_version")}
          value={row.has_credential ? String(row.credential_version ?? 0) : "—"}
        />
        <Fact
          label={t("connectors.adapter_kind")}
          value={t(`connectors.adapter_${row.adapter_kind || "native"}`)}
        />
        <Fact label={t("connectors.created_by")} value={row.created_by || "—"} />
        <Fact
          label={t("connectors.schedule")}
          value={
            row.schedule_kind === "interval"
              ? t("connectors.schedule_every_minutes", { count: row.schedule_value ?? 0 })
              : row.schedule_kind === "daily"
                ? t("connectors.schedule_at_local", { time: toHHMM(effectiveValue) })
                : t("connectors.schedule_manual")
          }
        />
      </dl>

      <Separator />

      <section className="space-y-3" aria-label={t("connectors.schedule_edit")}>
        <h3 className="text-sm font-semibold">{t("connectors.schedule_edit")}</h3>
        <div className="flex flex-wrap items-end gap-2" hidden={!canManage}>
          <label className="space-y-1 text-sm">
            <span className="text-muted-foreground">{t("connectors.schedule_mode")}</span>
            <select
              className="block h-9 rounded-md border bg-background px-3 text-sm"
              value={effectiveKind}
              onChange={(e) => {
                setKind(e.target.value);
                setValue(e.target.value === "daily" ? 540 : 30);
              }}
            >
              <option value="manual">{t("connectors.schedule_manual")}</option>
              <option value="interval">{t("connectors.schedule_interval")}</option>
              <option value="daily">{t("connectors.schedule_daily")}</option>
            </select>
          </label>
          {effectiveKind === "interval" ? (
            <label className="space-y-1 text-sm">
              <span className="text-muted-foreground">{t("connectors.schedule_minutes")}</span>
              <Input
                className="w-32"
                type="number"
                min={5}
                value={effectiveValue}
                onChange={(e) => setValue(Number(e.target.value))}
              />
            </label>
          ) : null}
          {effectiveKind === "daily" ? (
            <label className="space-y-1 text-sm">
              <span className="text-muted-foreground">{t("connectors.schedule_time_local")}</span>
              <Input
                className="w-32"
                type="time"
                value={toHHMM(effectiveValue)}
                onChange={(e) => setValue(fromHHMM(e.target.value))}
              />
            </label>
          ) : null}
          <Button size="sm" disabled={busy || kind === null} onClick={() => void saveSchedule()}>
            {t("connectors.save")}
          </Button>
        </div>
      </section>

      <Separator />
      <ConnectorRunsPanel connectorId={row.id} />

      <ConfirmDialog
        open={confirmDelete}
        onOpenChange={setConfirmDelete}
        title={t("connectors.delete_title")}
        message={t("connectors.delete_message", { name: row.name ?? row.id })}
        confirmLabel={t("connectors.delete")}
        cancelLabel={t("connectors.wizard.cancel")}
        onConfirm={() => void remove()}
      />
    </div>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded border p-3">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1 break-words font-medium">{value}</dd>
    </div>
  );
}

/** ConnectorDetail – binding detail, schedule editor and run history (doc/225 §11.1). */
export function ConnectorDetail() {
  return (
    <Show>
      <DetailBody />
    </Show>
  );
}
