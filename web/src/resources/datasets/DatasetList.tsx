import { useState } from "react";
import {
  BulkDeleteButton,
  DataTable,
  DateInput,
  FilterForm,
  FilterButton,
  List,
  ListLoadingBar,
  SearchInput,
} from "@/components/admin";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import {
  useCreatePath,
  useGetIdentity,
  useNavigate,
  useNotify,
  useRecordContext,
  useTranslate,
} from "ra-core";
import { useCanAccess } from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Database, Download, Eye, Loader2, Pencil, Plus, Trash2 } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { ApprovalHoldDialog } from "../approvals/ApprovalHoldDialog";
import { approvalHoldFromResponse, type ApprovalHold } from "@/lib/approval-hold";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { DatasetConfigFields, type DatasetConfigFieldsValue } from "./DatasetConfigFields";
import { DEFAULT_CHUNK_TOKEN_NUM } from "./embeddingModel";

const useDatasetFilters = () => {
  const { canAccess: canFilterTenant } = useCanAccess({ resource: "dataset", action: "governance.read" });
  const t = useTranslate();
  const filters = [
    <SearchInput source="name" key="name" alwaysOn />,
    <DateInput source="created_from" label={t("datasets.filter_created_from")} key="created_from" />,
    <DateInput source="created_to" label={t("datasets.filter_created_to")} key="created_to" />,
  ];
  if (canFilterTenant) {
    filters.push(
      <SearchInput
        source="tenant_id"
        label={t("datasets.filter_tenant_id")}
        key="tenant_id"
        placeholder={t("datasets.filter_tenant_hint")}
      />,
    );
  }
  return filters;
};

const emptyDatasetConfig = (): DatasetConfigFieldsValue => ({
  chunk_method: "naive",
  embedding_model: "",
  permission: "team",
  chunk_token_num: String(DEFAULT_CHUNK_TOKEN_NUM),
});

const buildDatasetConfigPayload = (cfg: DatasetConfigFieldsValue) => {
  const payload: Record<string, unknown> = {
    chunk_method: cfg.chunk_method,
    permission: cfg.permission,
  };
  if (cfg.embedding_model) {
    payload.embedding_model = cfg.embedding_model;
  }
  const token = Number(cfg.chunk_token_num);
  if (cfg.chunk_token_num !== "" && Number.isFinite(token) && token > 0) {
    payload.parser_config = { chunk_token_num: token };
  }
  return payload;
};

const CreateDatasetDialog = () => {
  const t = useTranslate();
  const notify = useNotify();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [hold, setHold] = useState<ApprovalHold | null>(null);
  const [name, setName] = useState("");
  const [cfg, setCfg] = useState<DatasetConfigFieldsValue>(emptyDatasetConfig());

  const create = async () => {
    if (!name.trim()) return;
    setSaving(true);
    try {
      const res = await api.post<{ code: number; data: { id: string } }>("/datasets", { name: name.trim() });
      const responseHold = approvalHoldFromResponse(res);
      if (responseHold) {
        setHold(responseHold);
        return;
      }
      const newId = res.data?.data?.id;
      if (newId) {
        const cfgPayload = buildDatasetConfigPayload(cfg);
        if (Object.keys(cfgPayload).length > 0) {
          const configRes = await api.put(`/datasets/${newId}/config`, cfgPayload);
          const configHold = approvalHoldFromResponse(configRes);
          if (configHold) {
            setHold(configHold);
            return;
          }
        }
      }
      notify(t("datasets.created"), { type: "success" });
      setOpen(false);
      setName("");
      setCfg(emptyDatasetConfig());
      qc.invalidateQueries({ queryKey: ["datasets"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("datasets.create_fail"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      {hold && <ApprovalHoldDialog hold={hold} onClose={() => setHold(null)} />}
      <Button onClick={() => setOpen(true)}>
        <Plus className="size-4" aria-hidden="true" />
        {t("datasets.create")}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t("datasets.create_title")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label>{t("datasets.name")}</Label>
              <Input value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <DatasetConfigFields value={cfg} onChange={setCfg} />
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>
                {t("confirm.cancel")}
              </Button>
              <Button onClick={() => void create()} disabled={saving || !name.trim()}>
                {saving ? <Loader2 className="size-4 animate-spin" /> : t("ra.action.save")}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
};

const DatasetActions = () => {
  const notify = useNotify();
  const t = useTranslate();
  const doExport = async () => {
    try {
      const res = await api.get("/datasets/export", { responseType: "blob" });
      const url = URL.createObjectURL(res.data as Blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = "datasets.csv";
      a.click();
      URL.revokeObjectURL(url);
    } catch {
      notify(t("datasets.export_fail"), { type: "error" });
    }
  };
  return (
    <div className="flex items-center gap-2">
      <CreateDatasetDialog />
      <Button variant="outline" onClick={doExport}>
        <Download /> {t("ra.action.export")}
      </Button>
    </div>
  );
};

const ViewDocumentsCell = () => {
  const record = useRecordContext<{ id: string }>();
  const createPath = useCreatePath();
  const navigate = useNavigate();
  const t = useTranslate();
  if (!record) return null;
  const to = createPath({ resource: "datasets", type: "show", id: record.id });
  return (
    <IconButtonWithTooltip label={t("datasets.view_docs")} onClick={(e) => { e.stopPropagation(); navigate(to); }}>
      <Eye className="size-4" aria-hidden="true" />
    </IconButtonWithTooltip>
  );
};

const EditDatasetCell = () => {
  const record = useRecordContext<{ id: string; name?: string; tenant_id?: string }>();
  const { data: identity } = useGetIdentity();
  const identityTenantID = (identity as { tenant_id?: string } | undefined)?.tenant_id;
  const scopeQuery =
    identityTenantID && record?.tenant_id && identityTenantID !== record.tenant_id
      ? `?scope=specific&tenant_id=${encodeURIComponent(record.tenant_id)}`
      : "";
  const notify = useNotify();
  const qc = useQueryClient();
  const t = useTranslate();
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [hold, setHold] = useState<ApprovalHold | null>(null);
  const [loadingCfg, setLoadingCfg] = useState(false);
  const [name, setName] = useState(record?.name ?? "");
  const [cfg, setCfg] = useState<DatasetConfigFieldsValue>(emptyDatasetConfig());

  if (!record) return null;

  const loadConfig = async () => {
    setOpen(true);
    setLoadingCfg(true);
    try {
      const res = await api.get<{ code: number; data: any }>(`/datasets/${record.id}/config${scopeQuery}`);
      const d = res.data?.data ?? {};
      setName(d.name ?? record.name ?? "");
      setCfg({
        chunk_method: d.chunk_method ?? "naive",
        embedding_model: d.embedding_model ?? "",
        permission: d.permission ?? "team",
        chunk_token_num: String((d.parser_config?.chunk_token_num as number) ?? ""),
      });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("datasets.config_load_fail"), { type: "error" });
      setOpen(false);
    } finally {
      setLoadingCfg(false);
    }
  };

  const save = async () => {
    if (!name.trim()) return;
    setSaving(true);
    try {
      const nameRes = await api.put(`/datasets/${record.id}${scopeQuery}`, { name: name.trim() });
      const nameHold = approvalHoldFromResponse(nameRes);
      if (nameHold) {
        setHold(nameHold);
        setOpen(false);
        return;
      }
      const configRes = await api.put(`/datasets/${record.id}/config${scopeQuery}`, buildDatasetConfigPayload(cfg));
      const configHold = approvalHoldFromResponse(configRes);
      if (configHold) {
        setHold(configHold);
        setOpen(false);
        return;
      }
      notify(t("datasets.updated"), { type: "success" });
      setOpen(false);
      qc.invalidateQueries({ queryKey: ["datasets"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("datasets.update_fail"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      {hold && <ApprovalHoldDialog hold={hold} onClose={() => setHold(null)} />}
      <IconButtonWithTooltip label={t("datasets.edit_label")} onClick={(e) => { e.stopPropagation(); void loadConfig(); }}>
        <Pencil className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t("datasets.edit_title")} {name || record.name || record.id}</DialogTitle>
          </DialogHeader>
          {loadingCfg ? (
            <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              <span>{t("datasets.config_loading")}</span>
            </div>
          ) : (
            <div className="space-y-4">
              <div className="space-y-1.5">
                <Label>{t("datasets.name")}</Label>
                <Input value={name} onChange={(e) => setName(e.target.value)} />
              </div>
              <DatasetConfigFields value={cfg} onChange={setCfg} />
              <div className="flex justify-end gap-2">
                <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>
                  {t("confirm.cancel")}
                </Button>
                <Button onClick={() => void save()} disabled={saving || !name.trim()}>
                  {saving ? <Loader2 className="size-4 animate-spin" /> : t("ra.action.save")}
                </Button>
              </div>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
};

const DeleteDatasetCell = () => {
  const record = useRecordContext<{ id: string; name?: string }>();
  const notify = useNotify();
  const qc = useQueryClient();
  const t = useTranslate();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [hold, setHold] = useState<ApprovalHold | null>(null);
  if (!record) return null;
  const doDelete = async () => {
    setDeleting(true);
    try {
      const res = await api.delete(`/datasets/${record.id}`);
      const responseHold = approvalHoldFromResponse(res);
      if (responseHold) {
        setHold(responseHold);
        return;
      }
      notify(t("datasets.deleted"), { type: "success" });
      qc.invalidateQueries({ queryKey: ["datasets"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("datasets.delete_fail"), { type: "error" });
    } finally {
      setDeleting(false);
      setConfirmOpen(false);
    }
  };
  return (
    <>
      {hold && <ApprovalHoldDialog hold={hold} onClose={() => setHold(null)} />}
      <IconButtonWithTooltip
        label={t("datasets.delete_label")}
        onClick={(e) => { e.stopPropagation(); setConfirmOpen(true); }}
        className="text-destructive!"
      >
        <Trash2 className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t("confirm.delete_title")}
        message={`确定删除数据集「${record.name || record.id}」吗？此操作不可撤销。`}
        confirmLabel={t("confirm.delete_label")}
        onConfirm={() => void doDelete()}
        loading={deleting}
      />
    </>
  );
};

const EmptyDatasetGuidance = () => {
  const t = useTranslate();
  return (
  <div className="flex flex-col items-center gap-3 rounded border border-dashed p-8 text-center">
    <Database className="size-10 text-muted-foreground/50" />
    <div>
      <p className="text-sm font-medium">{t("datasets.empty")}</p>
      <p className="mt-1 text-xs text-muted-foreground">
        {t("datasets.empty_hint")}
      </p>
    </div>
  </div>
  );
};

export const DatasetList = () => {
  const t = useTranslate();
  const filters = useDatasetFilters();
  return (
    <List perPage={20} actions={<DatasetActions />} filters={filters} aria-label={t("datasets.aria_dataset_list")}>
      <ListLoadingBar />
      <DataTable bulkActionButtons={<BulkDeleteButton />} aria-label={t("datasets.aria_dataset_table")} empty={<EmptyDatasetGuidance />}>
        <DataTable.Col source="name" label={t("datasets.name")} />
        <DataTable.Col
          source="tenant_name"
          label={t("datasets.tenant_name")}
          className="hidden md:table-cell"
          render={(r) => r.tenant_name ?? "-"}
        />
        <DataTable.NumberCol source="document_count" label={t("datasets.doc_count")} />
        <DataTable.Col
          source="ragflow_dataset_id"
          label="RAGFlow ID"
          className="hidden md:table-cell"
        />
        <DataTable.Col label="">
          <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
            <EditDatasetCell />
            <ViewDocumentsCell />
            <DeleteDatasetCell />
          </div>
        </DataTable.Col>
      </DataTable>
    </List>
  );
};
