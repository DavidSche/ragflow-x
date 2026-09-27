import {
  DataTable,
  List,
  ListLoadingBar,
  SearchInput,
  SelectInput,
} from "@/components/admin";
import { useNotify, useRecordContext, useRefresh, useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { BellRing, Check, UserCheck } from "lucide-react";
import { api, unwrap, ApiError } from "../../lib/api";

interface AlertRow {
  id: string;
  title: string;
  severity: string;
  type: string;
  resource: string;
  resource_id: string;
  detail: string;
  status: string;
  acked_by?: string;
  acked_at?: string;
  occurred_at: string;
}

const severityColor = (severity: string) =>
  severity === "critical" || severity === "error"
    ? "destructive"
    : severity === "warn"
      ? "secondary"
      : "outline";

const AlertActions = () => {
  const record = useRecordContext<AlertRow>();
  const notify = useNotify();
  const refresh = useRefresh();
  const t = useTranslate();
  if (!record || record.status !== "open") return null;

  const update = async (status: "read" | "claimed") => {
    try {
      await unwrap(api.post(`/alerts/${record.id}/${status}`));
      notify(t(status === "read" ? "system.alert_marked_read" : "system.alert_claimed"), { type: "success" });
      refresh();
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : error instanceof Error ? error.message : t("system.alert_update_failed"), { type: "error" });
    }
  };

  return (
    <div className="flex gap-1">
      <Button size="sm" variant="outline" onClick={() => update("read")} aria-label={t("system.alert_mark_read")}>
        <Check className="size-4" />
      </Button>
      <Button size="sm" variant="outline" onClick={() => update("claimed")} aria-label={t("system.alert_claim")}>
        <UserCheck className="size-4" />
      </Button>
    </div>
  );
};

const EmptyAlerts = () => {
  const t = useTranslate();
  return (
    <div className="flex flex-col items-center gap-3 rounded border border-dashed p-8 text-center">
      <BellRing className="size-10 text-muted-foreground/50" />
      <p className="text-sm">{t("system.alert_empty")}</p>
    </div>
  );
};

export const AlertList = () => {
  const t = useTranslate();
  const filters = [
    <SearchInput source="search" key="search" alwaysOn placeholder={t("system.alert_filter_search")} />,
    <SelectInput
      key="status"
      source="status"
      alwaysOn
      choices={[
        { id: "open", name: t("system.alert_status_open") },
        { id: "read", name: t("system.alert_status_read") },
        { id: "claimed", name: t("system.alert_status_claimed") },
      ]}
    />,
  ];

  return (
    <List perPage={20} filters={filters} aria-label={t("system.alert_title")}>
      <ListLoadingBar />
      <DataTable aria-label={t("system.alert_data_table")} empty={<EmptyAlerts />}>
        <DataTable.Col source="occurred_at" label={t("system.alert_occurred_at")} render={(row) => new Date(row.occurred_at).toLocaleString()} />
        <DataTable.Col source="severity" label={t("system.alert_severity")} render={(row) => <Badge variant={severityColor(row.severity)}>{row.severity}</Badge>} />
        <DataTable.Col source="type" label={t("system.alert_type")} />
        <DataTable.Col source="title" label={t("system.alert_title")} />
        <DataTable.Col source="resource" label={t("system.alert_resource")} className="hidden md:table-cell" />
        <DataTable.Col source="status" label={t("system.alert_status")} />
        <DataTable.Col source="acked_by" label={t("system.alert_acked_by")} className="hidden lg:table-cell" render={(row) => row.acked_by || "-"} />
        <DataTable.Col label="">
          <AlertActions />
        </DataTable.Col>
      </DataTable>
    </List>
  );
};
