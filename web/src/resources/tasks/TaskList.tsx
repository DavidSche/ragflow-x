import { useEffect, useState } from "react";
import { DataTable, List, ListLoadingBar, SearchInput, SelectInput } from "@/components/admin";
import { useListContext } from "ra-core";
import { useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { RefreshCw, ListTodo } from "lucide-react";

function statusText(t: ReturnType<typeof useTranslate>, status: string) {
  const labels: Record<string, string> = {
    queued: t("tasks.status_queued"),
    running: t("tasks.status_running"),
    done: t("tasks.status_done"),
    failed: t("tasks.status_failed"),
    stopped: t("tasks.status_stopped"),
  };
  return labels[status] ?? status;
}

function statusVariant(status: string): "default" | "secondary" | "destructive" | "outline" {
  switch (status) {
    case "done":
      return "default";
    case "running":
      return "secondary";
    case "failed":
      return "destructive";
    case "queued":
      return "outline";
    default:
      return "outline";
  }
}

function taskTypeText(t: ReturnType<typeof useTranslate>, type: string) {
  const labels: Record<string, string> = {
    upload: t("tasks.type_upload"),
    parse: t("tasks.type_parse"),
    stop: t("tasks.type_stop"),
    delete: t("tasks.type_delete"),
  };
  return labels[type] ?? type;
}

const AutoRefreshToggle = () => {
  const t = useTranslate();
  const [autoRefresh, setAutoRefresh] = useState(false);
  const { refetch } = useListContext();

  useEffect(() => {
    if (!autoRefresh) return;
    const interval = setInterval(() => {
      void refetch();
    }, 5000);
    return () => clearInterval(interval);
  }, [autoRefresh, refetch]);

  return (
    <Button
      size="sm"
      variant={autoRefresh ? "default" : "outline"}
      onClick={() => setAutoRefresh((v) => !v)}
      aria-label={autoRefresh ? t("tasks.auto_refresh_label_on") : t("tasks.auto_refresh_label_off")}
    >
      <RefreshCw className={`size-4 ${autoRefresh ? "animate-spin" : ""}`} />
      {autoRefresh ? t("tasks.auto_refreshing") : t("tasks.auto_refresh")}
    </Button>
  );
};

function ProgressBar({ progress }: { progress: number }) {
  return (
    <div className="flex items-center gap-2">
      <div className="h-2 w-20 overflow-hidden rounded-full bg-muted">
        <div
          className="h-full rounded-full bg-primary transition-all duration-300"
          style={{ width: `${Math.min(100, Math.max(0, progress))}%` }}
        />
      </div>
      <span className="text-xs tabular-nums text-muted-foreground">{progress}%</span>
    </div>
  );
}

const EmptyTaskGuidance = () => {
  const t = useTranslate();
  return (
  <div className="flex flex-col items-center gap-3 rounded border border-dashed p-8 text-center">
    <ListTodo className="size-10 text-muted-foreground/50" />
    <div>
      <p className="text-sm font-medium">{t("tasks.empty")}</p>
      <p className="mt-1 text-xs text-muted-foreground">
        {t("tasks.empty_hint")}
      </p>
    </div>
  </div>
  );
};

export const TaskList = () => {
  const t = useTranslate();
  const statusChoices = [
    { id: "queued", name: t("tasks.status_queued") },
    { id: "running", name: t("tasks.status_running") },
    { id: "done", name: t("tasks.status_done") },
    { id: "failed", name: t("tasks.status_failed") },
    { id: "stopped", name: t("tasks.status_stopped") },
  ];
  const typeChoices = [
    { id: "upload", name: t("tasks.type_upload") },
    { id: "parse", name: t("tasks.type_parse") },
    { id: "stop", name: t("tasks.type_stop") },
    { id: "delete", name: t("tasks.type_delete") },
  ];
  const taskFilters = [
    <SearchInput source="doc_name" key="doc_name" alwaysOn placeholder={t("tasks.filter_doc")} />,
    <SelectInput source="status" label={t("tasks.filter_status")} choices={statusChoices} key="status" />,
    <SelectInput source="task_type" label={t("tasks.filter_type")} choices={typeChoices} key="task_type" />,
  ];
  return (
    <List perPage={20} filters={taskFilters} actions={<AutoRefreshToggle />} aria-label={t("tasks.list_title")}>
    <ListLoadingBar />
    <DataTable
      aria-label={t("tasks.data_table")}
      empty={<EmptyTaskGuidance />}
    >
      <DataTable.Col
        source="task_type"
        label={t("tasks.type")}
        render={(r) => (
          <Badge variant="outline">{taskTypeText(t, r.task_type)}</Badge>
        )}
      />
      <DataTable.Col
        source="status"
        label={t("tasks.status")}
        render={(r) => (
          <Badge variant={statusVariant(r.status)}>{statusText(t, r.status)}</Badge>
        )}
      />
      <DataTable.Col source="doc_name" label={t("tasks.doc")} className="hidden md:table-cell" />
      <DataTable.Col
        source="progress"
        label={t("tasks.progress")}
        render={(r) => <ProgressBar progress={r.progress ?? 0} />}
      />
      <DataTable.Col
        source="detail"
        label={t("tasks.detail")}
        className="hidden lg:table-cell"
        render={(r) => (
          <div className="max-h-20 overflow-auto whitespace-pre-wrap break-words text-xs text-muted-foreground">
            {r.detail || "-"}
          </div>
        )}
      />
      <DataTable.Col
        source="created_at"
        label={t("tasks.time")}
        render={(record) => new Date(record.created_at).toLocaleString()}
      />
    </DataTable>
  </List>
  );
};
