import { useState, useEffect } from "react";
import {
  DataTable,
  DateInput,
  FilterForm,
  FilterButton,
  List,
  ListLoadingBar,
  SearchInput,
  TextInput,
  AutocompleteInput,
  ReferenceInput,
} from "@/components/admin";
import { useCanAccess, useNotify, useRecordContext } from "ra-core";
import { useTranslate } from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Download, ShieldCheck, ShieldAlert, History, Eye } from "lucide-react";
import { api } from "../../lib/api";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import { UserNameLabel } from "@/components/admin/ReferenceLabel";

function actionColor(action: string): "default" | "secondary" | "destructive" | "outline" {
  if (action.includes("delete")) return "destructive";
  if (action.includes("create")) return "default";
  if (action.includes("update") || action.includes("put")) return "secondary";
  return "outline";
}

const AuditActions = () => {
  const t = useTranslate();
  const notify = useNotify();
  const [verifying, setVerifying] = useState(false);
  const [verifyResult, setVerifyResult] = useState<{ open: boolean; valid: boolean }>({
    open: false,
    valid: false,
  });

  const doExport = async () => {
    try {
      const res = await api.get("/audit/export", { responseType: "blob" });
      const url = URL.createObjectURL(res.data as Blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `audit-${new Date().toISOString().slice(0, 10)}.csv`;
      a.click();
      URL.revokeObjectURL(url);
      notify(t("audit.exported"), { type: "success" });
    } catch {
      notify(t("audit.export_fail"), { type: "error" });
    }
  };

  const doVerify = async () => {
    setVerifying(true);
    try {
      const res = await api.get<{ code: number; data: { valid: boolean } }>( "/audit/verify");
      setVerifyResult({ open: true, valid: res.data.data.valid });
    } catch {
      notify(t("audit.verify_error"), { type: "error" });
    } finally {
      setVerifying(false);
    }
  };

  return (
    <>
      <div className="flex items-center gap-2">
        <Button variant="outline" onClick={doExport} aria-label={t("audit.export_label")}>
          <Download className="size-4" /> {t("audit.export_btn")}
        </Button>
        <Button
          variant="outline"
          onClick={doVerify}
          disabled={verifying}
          aria-label={t("audit.verify_label")}
        >
          <ShieldCheck className="size-4" /> {verifying ? t("audit.verifying") : t("audit.verify_btn")}
        </Button>
      </div>
      <Dialog open={verifyResult.open} onOpenChange={(o) => setVerifyResult((v) => ({ ...v, open: o }))}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              {verifyResult.valid ? (
                <ShieldCheck className="size-5 text-green-600" />
              ) : (
                <ShieldAlert className="size-5 text-destructive" />
              )}
              {t("audit.verify_title")}
            </DialogTitle>
          </DialogHeader>
          <div className="text-sm">
            {verifyResult.valid ? (
              <p className="text-green-600">{t("audit.verify_ok")}</p>
            ) : (
              <p className="text-destructive">{t("audit.verify_fail")}</p>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
};

interface AuditDetailProps {
  record: Record<string, unknown> | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

const AuditDetailDialog = ({ record, open, onOpenChange }: AuditDetailProps) => {
  const t = useTranslate();
  if (!record) return null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("audit.detail_title")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3 text-sm">
          <div className="grid grid-cols-2 gap-2">
            <div>
              <span className="text-muted-foreground">{t("audit.time")}:</span>
              <p>{record.at ? new Date(String(record.at)).toLocaleString() : "-"}</p>
            </div>
            <div>
              <span className="text-muted-foreground">{t("audit.user")}:</span>
              <p><UserNameLabel id={record.user_id ? String(record.user_id) : undefined} /></p>
            </div>
            <div>
              <span className="text-muted-foreground">{t("audit.action")}:</span>
              <p><Badge variant={actionColor(String(record.action))}>{String(record.action)}</Badge></p>
            </div>
            <div>
              <span className="text-muted-foreground">{t("audit.resource")}:</span>
              <p>{String(record.resource ?? "-")}</p>
            </div>
            <div>
              <span className="text-muted-foreground">{t("audit.ip")}:</span>
              <p>{String(record.ip ?? "-")}</p>
            </div>
            <div>
              <span className="text-muted-foreground">{t("audit.hash")}:</span>
              <p className="break-all font-mono text-xs">{String(record.hash ?? "-")}</p>
            </div>
          </div>
          {!!record.detail && (
            <div>
              <span className="text-muted-foreground">{t("audit.detail")}:</span>
              <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap rounded bg-muted p-2 text-xs">
                {typeof record.detail === "string" ? record.detail : JSON.stringify(record.detail, null, 2)}
              </pre>
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
};

const ViewDetailCell = () => {
  const record = useRecordContext();
  const [open, setOpen] = useState(false);
  if (!record) return null;
  return (
    <>
      <IconButtonWithTooltip label="audit.detail_title" onClick={() => setOpen(true)}>
        <Eye className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <AuditDetailDialog record={record as Record<string, unknown>} open={open} onOpenChange={setOpen} />
    </>
  );
};

const EmptyAuditGuidance = () => {
  const t = useTranslate();
  return (
  <div className="flex flex-col items-center gap-3 rounded border border-dashed p-8 text-center">
    <History className="size-10 text-muted-foreground/50" />
    <div>
      <p className="text-sm font-medium">{t("audit.empty")}</p>
      <p className="mt-1 text-xs text-muted-foreground">
        {t("audit.empty_hint")}
      </p>
    </div>
  </div>
  );
};

export const AuditList = () => {
  const t = useTranslate();
  const { canAccess: canFilterTenant } = useCanAccess({ resource: "audit", action: "governance.read" });
  const auditFilters = [
    <ReferenceInput source="user_id" reference="users" key="user_id">
      <AutocompleteInput label={t("audit.filter_user")} optionText="username" />
    </ReferenceInput>,
    <TextInput source="action" label={t("audit.filter_action")} placeholder={t("audit.filter_action_placeholder")} />,
    <TextInput source="resource" label={t("audit.filter_resource")} placeholder={t("audit.filter_resource_placeholder")} />,
    <DateInput source="created_from" label={t("audit.filter_from")} key="created_from" />,
    <DateInput source="created_to" label={t("audit.filter_to")} key="created_to" />,
  ];
  if (canFilterTenant) {
    auditFilters.push(
      <ReferenceInput source="target_tenant_id" reference="tenants" key="target_tenant_id">
        <AutocompleteInput label={t("tenants.tenant_name")} optionText="name" />
      </ReferenceInput>,
    );
  }
  return (
    <List perPage={20} actions={<AuditActions />} filters={auditFilters} aria-label={t("audit.list_title")}>
      <ListLoadingBar />
      <DataTable aria-label={t("audit.data_table")} empty={<EmptyAuditGuidance />}>
        <DataTable.Col
          source="at"
          label={t("audit.time")}
          render={(r) => new Date(r.at).toLocaleString()}
        />
        <DataTable.Col
          source="user_id"
          label={t("audit.user")}
          className="hidden md:table-cell"
          render={(r) => <UserNameLabel id={r.user_id} />}
        />
        <DataTable.Col
          source="action"
          label={t("audit.action")}
          render={(r) => (
            <Badge variant={actionColor(r.action)}>{r.action}</Badge>
          )}
        />
        <DataTable.Col source="resource" label={t("audit.resource")} />
        <DataTable.Col source="ip" label={t("audit.ip")} className="hidden md:table-cell" />
        <DataTable.Col
          source="hash"
          label={t("audit.hash")}
          className="hidden lg:table-cell"
          render={(r) => (
            <code className="max-w-[8rem] truncate text-xs text-muted-foreground" title={r.hash}>
              {r.hash ? r.hash.slice(0, 12) + "…" : "-"}
            </code>
          )}
        />
        <DataTable.Col label="">
          <ViewDetailCell />
        </DataTable.Col>
      </DataTable>
    </List>
  );
};
