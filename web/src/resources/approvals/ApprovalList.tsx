import { useEffect, useState } from "react";
import {
  DataTable,
  DateInput,
  FilterButton,
  FilterForm,
  List,
  ListLoadingBar,
  SearchInput,
  SelectInput,
} from "@/components/admin";
import { TextInput } from "@/components/admin";
import {
  useCreatePath,
  useCanAccess,
  useGetIdentity,
  useNavigate,
  useListContext,
  useNotify,
  useRecordContext,
  useRefresh,
  useTranslate,
} from "ra-core";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { useQueryClient } from "@tanstack/react-query";
import { Check, Download, Plus, X } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { APPROVAL_ACTIONS, APPROVAL_OBJECTS, APPROVAL_STATUSES, StatusBadge } from "./approval-ui";
import { ApprovalDelegationButton } from "./ApprovalDelegations";
import {
  type ApprovalRecord,
} from "./approval-types";

type ApprovalScope = "pending_for_me" | "created_by_me" | "all";
type ApprovalBulkDecision = "approve" | "reject";

const formatDateTime = (value?: string) =>
  value ? new Date(value).toLocaleString() : "-";

const buildExportQuery = (filterValues: Record<string, unknown>) => {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(filterValues)) {
    if (key !== "scope" && value != null && value !== "") {
      params.append(key, String(value));
    }
  }
  const query = params.toString();
  return query ? `?${query}` : "";
};

const ExportApprovalButton = () => {
  const { filterValues } = useListContext<ApprovalRecord>();
  const { canAccess } = useCanAccess({ resource: "approval", action: "read" });
  const notify = useNotify();
  const t = useTranslate();

  if (!canAccess) return null;

  const doExport = async () => {
    try {
      const res = await api.get(`/approvals/export${buildExportQuery(filterValues ?? {})}`, {
        responseType: "blob",
      });
      const url = URL.createObjectURL(res.data as Blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = `approvals-${new Date().toISOString().slice(0, 10)}.csv`;
      anchor.click();
      URL.revokeObjectURL(url);
      notify(t("approvals.exported"), { type: "success" });
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("approvals.export_fail"), {
        type: "error",
      });
    }
  };

  return (
    <Button variant="outline" onClick={doExport} aria-label={t("approvals.export_label")}>
      <Download className="size-4" /> {t("ra.action.export")}
    </Button>
  );
};

const ApprovalActions = () => {
  const { canAccess: canCreate } = useCanAccess({ resource: "approval", action: "execute" });
  const t = useTranslate();
  const navigate = useNavigate();
  const createPath = useCreatePath();
  return (
    <div className="flex flex-wrap items-center gap-2">
      <FilterButton />
      {canCreate && (
        <Button onClick={() => navigate(createPath({ resource: "approvals", type: "create" }))}>
          <Plus className="size-4" /> {t("ra.action.create")}
        </Button>
      )}
      <ExportApprovalButton />
      <ApprovalDelegationButton />
    </div>
  );
};

const ApprovalBulkActions = () => {
  const { selectedIds = [], onUnselectItems, refetch } = useListContext<ApprovalRecord>();
  const { canAccess: canManageApprovals } = useCanAccess({ resource: "approval", action: "manage" });
  const notify = useNotify();
  const t = useTranslate();
  const qc = useQueryClient();
  const [decision, setDecision] = useState<ApprovalBulkDecision | null>(null);
  const [comment, setComment] = useState("");
  const [saving, setSaving] = useState(false);

  if (!canManageApprovals || selectedIds.length === 0) return null;
  const tooMany = selectedIds.length > 100;

  const submit = async () => {
    if (!decision || !comment.trim()) return;
    setSaving(true);
    try {
      const res = await api.post<{ code: number; data?: Array<{ id: string; ok?: boolean }> }>(
        `/approvals/batch-${decision}`,
        { ids: selectedIds, comment: comment.trim() },
      );
      const results = res.data.data ?? [];
      const successCount = results.filter((result) => result.ok).length;
      const failedCount = selectedIds.length - successCount;
      notify(failedCount
        ? t("approvals.bulk.partial")
        : t(decision === "approve" ? "approvals.bulk.approved" : "approvals.bulk.rejected"),
        failedCount ? {
          type: "warning",
          messageArgs: { success: String(successCount), failed: String(failedCount) },
        } : { type: "success" });
      onUnselectItems();
      qc.invalidateQueries({ queryKey: ["approvals"] });
      void refetch();
      setDecision(null);
      setComment("");
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("approvals.bulk.failed"), {
        type: "error",
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button size="sm" onClick={() => setDecision("approve")} disabled={tooMany}>
        <Check className="size-4" /> {t("approvals.bulk.approve")}
      </Button>
      <Button size="sm" variant="outline" onClick={() => setDecision("reject")} disabled={tooMany}>
        <X className="size-4" /> {t("approvals.bulk.reject")}
      </Button>
      {tooMany && <span className="text-xs text-destructive">{t("approvals.bulk.too_many")}</span>}
      <Dialog open={!!decision} onOpenChange={(open) => !open && setDecision(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>
              {decision === "approve" ? t("approvals.bulk.approve_title") : t("approvals.bulk.reject_title")}
            </DialogTitle>
          </DialogHeader>
          <p className="text-sm text-muted-foreground">
            {t("approvals.bulk.confirm", { count: String(selectedIds.length) })}
          </p>
          <Textarea
            value={comment}
            onChange={(event) => setComment(event.target.value)}
            placeholder={t("approvals.comment_placeholder")}
            rows={4}
            aria-label={t("approvals.comment")}
          />
          <DialogFooter>
            <Button variant="outline" onClick={() => setDecision(null)} disabled={saving}>
              {t("ra.action.cancel")}
            </Button>
            <Button onClick={() => void submit()} disabled={saving || !comment.trim()}>
              {t("ra.action.confirm")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
};

const ApprovalScopeTabs = () => {
  const { filterValues, displayedFilters, setFilters } = useListContext<ApprovalRecord>();
  const { data: identity } = useGetIdentity();
  const { canAccess: canManageApprovals } = useCanAccess({
    resource: "approval",
    action: "manage",
  });
  const t = useTranslate();
  const scope = (filterValues?.scope as ApprovalScope) ?? "pending_for_me";

  useEffect(() => {
    if (scope === "pending_for_me" && identity?.id && filterValues?.approver_id !== identity.id) {
      setFilters({ ...filterValues, approver_id: identity.id }, displayedFilters);
    }
  }, [displayedFilters, filterValues, identity?.id, scope, setFilters]);

  const changeScope = (nextScope: string) => {
    const nextFilters: Record<string, unknown> = { ...filterValues, scope: nextScope };
    delete nextFilters.approver_id;
    delete nextFilters.requester_id;
    if (identity?.id) {
      if (nextScope === "pending_for_me") nextFilters.approver_id = identity.id;
      if (nextScope === "created_by_me") nextFilters.requester_id = identity.id;
    }
    setFilters(nextFilters, displayedFilters);
  };

  return (
    <Tabs value={scope} onValueChange={changeScope} className="mb-3">
      <TabsList>
        <TabsTrigger value="pending_for_me">{t("approvals.tabs.pending_for_me")}</TabsTrigger>
        <TabsTrigger value="created_by_me">{t("approvals.tabs.created_by_me")}</TabsTrigger>
        {canManageApprovals && (
          <TabsTrigger value="all">{t("approvals.tabs.all")}</TabsTrigger>
        )}
      </TabsList>
    </Tabs>
  );
};

const DecisionDialog = ({
  record,
  decision,
  onClose,
  onLoadingChange,
}: {
  record: ApprovalRecord;
  decision: "approve" | "reject";
  onClose: () => void;
  onLoadingChange?: (loading: boolean) => void;
}) => {
  const notify = useNotify();
  const refresh = useRefresh();
  const t = useTranslate();
  const [comment, setComment] = useState("");
  const [saving, setSaving] = useState(false);

  const submit = async () => {
    if (!comment.trim()) return;
    setSaving(true);
    onLoadingChange?.(true);
    try {
      await api.post(`/approvals/${record.id}/${decision}`, { comment: comment.trim() });
      notify(decision === "approve" ? t("approvals.approved") : t("approvals.rejected"), {
        type: "success",
      });
      refresh();
      onClose();
    } catch (error) {
      notify(error instanceof ApiError ? error.displayMessage : t("approvals.action_fail"), {
        type: "error",
      });
    } finally {
      setSaving(false);
      onLoadingChange?.(false);
    }
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {decision === "approve" ? t("approvals.approve_title") : t("approvals.reject_title")}
          </DialogTitle>
        </DialogHeader>
        <p className="text-sm text-muted-foreground">{record.title}</p>
        <Textarea
          value={comment}
          onChange={(event) => setComment(event.target.value)}
          placeholder={t("approvals.comment_placeholder")}
          aria-label={t("approvals.comment")}
          rows={4}
        />
        <DialogFooter>
          <Button variant="outline" onClick={onClose} disabled={saving}>
            {t("ra.action.cancel")}
          </Button>
          <Button onClick={submit} disabled={saving || !comment.trim()}>
            {t("ra.action.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
};

const DecisionCell = ({ decision }: { decision: "approve" | "reject" }) => {
  const record = useRecordContext<ApprovalRecord>();
  const { canAccess: canManageApprovals } = useCanAccess({
    resource: "approval",
    action: "manage",
  });
  const t = useTranslate();
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(false);

  if (!record || record.status !== "pending_approval" || !canManageApprovals) return null;

  return (
    <>
      <Button
        size="sm"
        variant={decision === "approve" ? "default" : "outline"}
        onClick={() => setOpen(true)}
        disabled={loading}
        aria-label={decision === "approve" ? t("approvals.approve_label") : t("approvals.reject_label")}
      >
        {decision === "approve" ? <Check className="h-4 w-4" /> : <X className="h-4 w-4" />}
      </Button>
      {open && (
        <DecisionDialog
          record={record}
          decision={decision}
          onClose={() => setOpen(false)}
          onLoadingChange={setLoading}
        />
      )}
    </>
  );
};

const statusChoices = (translate: ReturnType<typeof useTranslate>) =>
  APPROVAL_STATUSES.map((status) => ({
    id: status,
    name: translate(`approvals.status.${status}`),
  }));

export const ApprovalList = () => {
  const t = useTranslate();
  const statusLabels = Object.fromEntries(
    APPROVAL_STATUSES.map((status) => [status, t(`approvals.status.${status}`)]),
  );
  return (
    <List
      perPage={20}
      actions={<ApprovalActions />}
      filterDefaultValues={{ scope: "pending_for_me", status: "pending_approval" }}
      filters={[
        <SearchInput key="q" source="q" placeholder={t("approvals.search_placeholder")} />,
        <SelectInput key="status" source="status" choices={statusChoices(t)} />,
        <SelectInput
          key="object_type"
          source="object_type"
          choices={APPROVAL_OBJECTS.map((value) => ({ id: value, name: value }))}
        />,
        <SelectInput
          key="action"
          source="action"
          choices={APPROVAL_ACTIONS.map((value) => ({ id: value, name: value }))}
        />,
        <TextInput key="policy_id" source="policy_id" label={t("approvals.policy_id")} />,
        <DateInput key="from" source="from" label={t("approvals.from")} />,
        <DateInput key="to" source="to" label={t("approvals.to")} />,
      ]}
    >
      <ApprovalScopeTabs />
      <ListLoadingBar />
      <DataTable rowClick="show" empty={t("approvals.empty")} bulkActionButtons={<ApprovalBulkActions />}>
        <DataTable.Col source="request_no" label={t("approvals.request_no")} />
        <DataTable.Col source="object_type" label={t("approvals.object_type")} />
        <DataTable.Col source="action" label={t("approvals.action")} />
        <DataTable.Col source="title" label={t("approvals.title")} />
        <DataTable.Col source="current_step" label={t("approvals.current_step")} />
        <DataTable.Col
          source="status"
          label={t("approvals.status_title")}
          render={(record) => <StatusBadge status={record.status} labels={statusLabels} />}
        />
        <DataTable.Col
          source="created_at"
          label={t("approvals.created_at")}
          render={(record) => formatDateTime(record.created_at)}
        />
        <DataTable.Col
          source="expires_at"
          label={t("approvals.expires_at")}
          render={(record) => formatDateTime(record.expires_at)}
        />
        <DataTable.Col label={t("approvals.decisions")}>
          <DecisionCell decision="approve" />
          <DecisionCell decision="reject" />
        </DataTable.Col>
      </DataTable>
    </List>
  );
};
