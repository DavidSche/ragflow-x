import {
  DataTable,
  List,
  ListLoadingBar,
  SelectInput,
} from "@/components/admin";
import { useCanAccess, useNotify, useRefresh, useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Send } from "lucide-react";
import { useState } from "react";
import { api, ApiError, unwrap } from "../../lib/api";

interface AlertDeliveryRow {
  alert_event_id: string;
  channel: string;
  status: string;
  attempts: number;
  last_error: string;
  last_attempt_at: string;
  next_retry_at?: string;
  delivered_at?: string;
  lease_owner: string;
  lease_expires_at?: string;
}

export const canManuallyRetryDelivery = (status: string) =>
  status === "pending" || status === "failed" || status === "abandoned";

const RetryDeliveryButton = ({ delivery }: { delivery: AlertDeliveryRow }) => {
  const t = useTranslate();
  const notify = useNotify();
  const refresh = useRefresh();
  const { canAccess } = useCanAccess({ resource: "alert", action: "manage" });
  const [busy, setBusy] = useState(false);

  if (!canAccess || !canManuallyRetryDelivery(delivery.status)) return null;

  const retry = async () => {
    setBusy(true);
    try {
      await unwrap(api.post(
        `/alerts/${delivery.alert_event_id}/deliveries/${encodeURIComponent(delivery.channel)}/retry`,
      ));
      notify(t("system.alert_delivery_retry_requested"), { type: "success" });
      refresh();
    } catch (error) {
      notify(
        error instanceof ApiError
          ? error.displayMessage
          : error instanceof Error
            ? error.message
            : t("system.alert_delivery_retry_failed"),
        { type: "error" },
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <Button size="sm" variant="outline" disabled={busy} onClick={() => void retry()}>
      {t("system.alert_delivery_retry")}
    </Button>
  );
};

const deliveryStatusVariant = (status: string) =>
  status === "failed"
    ? "destructive"
    : status === "succeeded"
      ? "default"
      : status === "pending"
        ? "secondary"
        : "outline";

const formatDateTime = (value?: string) => (value ? new Date(value).toLocaleString() : "-");

const EmptyDeliveries = () => {
  const t = useTranslate();
  return (
    <div className="flex flex-col items-center gap-3 rounded border border-dashed p-8 text-center">
      <Send className="size-10 text-muted-foreground/50" />
      <p className="text-sm">{t("system.alert_delivery_empty")}</p>
    </div>
  );
};

export const AlertDeliveryList = () => {
  const t = useTranslate();
  const filters = [
    <SelectInput
      key="status"
      source="status"
      alwaysOn
      choices={[
        { id: "pending", name: t("system.alert_delivery_status_pending") },
        { id: "succeeded", name: t("system.alert_delivery_status_succeeded") },
        { id: "failed", name: t("system.alert_delivery_status_failed") },
        { id: "abandoned", name: t("system.alert_delivery_status_abandoned") },
      ]}
    />,
    <SelectInput
      key="channel"
      source="channel"
      alwaysOn
      choices={[
        { id: "webhook", name: "webhook" },
        { id: "email", name: "email" },
        { id: "wecom", name: "wecom" },
        { id: "dingtalk", name: "dingtalk" },
      ]}
    />,
  ];

  return (
    <List
      perPage={20}
      sort={{ field: "last_attempt_at", order: "DESC" }}
      filters={filters}
      aria-label={t("system.alert_delivery_title")}
    >
      <ListLoadingBar />
      <DataTable aria-label={t("system.alert_delivery_data_table")} empty={<EmptyDeliveries />}>
        <DataTable.Col source="alert_event_id" label={t("system.alert_delivery_alert_event")} />
        <DataTable.Col source="channel" label={t("system.alert_delivery_channel")} />
        <DataTable.Col
          source="status"
          label={t("system.alert_delivery_status")}
          render={(row) => {
            const delivery = row as unknown as AlertDeliveryRow;
            return <Badge variant={deliveryStatusVariant(delivery.status)}>{delivery.status}</Badge>;
          }}
        />
        <DataTable.Col source="attempts" label={t("system.alert_delivery_attempts")} />
        <DataTable.Col
          source="last_error"
          label={t("system.alert_delivery_last_error")}
          className="hidden md:table-cell"
          render={(row) => (row as unknown as AlertDeliveryRow).last_error || "-"}
        />
        <DataTable.Col
          source="last_attempt_at"
          label={t("system.alert_delivery_last_attempt")}
          render={(row) => formatDateTime((row as unknown as AlertDeliveryRow).last_attempt_at)}
        />
        <DataTable.Col
          source="next_retry_at"
          label={t("system.alert_delivery_next_retry")}
          className="hidden lg:table-cell"
          render={(row) => formatDateTime((row as unknown as AlertDeliveryRow).next_retry_at)}
        />
        <DataTable.Col
          source="delivered_at"
          label={t("system.alert_delivery_delivered")}
          className="hidden lg:table-cell"
          render={(row) => formatDateTime((row as unknown as AlertDeliveryRow).delivered_at)}
        />
        <DataTable.Col
          source="lease_owner"
          label={t("system.alert_delivery_lease_owner")}
          className="hidden xl:table-cell"
          render={(row) => (row as unknown as AlertDeliveryRow).lease_owner || "-"}
        />
        <DataTable.Col
          source="lease_expires_at"
          label={t("system.alert_delivery_lease_expires")}
          className="hidden xl:table-cell"
          render={(row) => formatDateTime((row as unknown as AlertDeliveryRow).lease_expires_at)}
        />
        <DataTable.Col
          label=""
          render={(row) => <RetryDeliveryButton delivery={row as unknown as AlertDeliveryRow} />}
        />
      </DataTable>
    </List>
  );
};
