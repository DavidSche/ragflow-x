import { useState } from "react";
import {
  DataTable,
  List,
  ListLoadingBar,
  SearchInput,
} from "@/components/admin";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import { useCreatePath, useGetList, useNavigate, useNotify, useRecordContext, useTranslate } from "ra-core";
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
import { Eye, Loader2, Pencil, Plus, Search, Trash2 } from "lucide-react";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { api, ApiError } from "../../lib/api";
import { datasetOptions } from "../chats/chat-types";
import type { DatasetLike } from "../chats/chat-types";

interface SearchAppLike {
  id: string;
  name: string;
  status: string;
  dataset_ids?: string;
  owner_id?: string;
  created_at?: string;
}

const CreateSearchAppDialog = () => {
  const t = useTranslate();
  const notify = useNotify();
  const qc = useQueryClient();
  const { data: datasets } = useGetList("datasets", { pagination: { page: 1, perPage: 200 } });
  const { values, mapId } = datasetOptions(datasets as DatasetLike[] | undefined);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const toggle = (id: string) =>
    setSelected((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));

  const create = async () => {
    if (!name.trim()) return;
    setSaving(true);
    try {
      await api.post("/search-apps", { name: name.trim(), dataset_ids: selected });
      notify(t("searchApps.created"), { type: "success" });
      setOpen(false);
      setName("");
      setSelected([]);
      qc.invalidateQueries({ queryKey: ["search-apps"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("searchApps.create_fail"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <Button onClick={() => setOpen(true)}>
        <Plus className="size-4" aria-hidden="true" />
        {t("searchApps.create_title")}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("searchApps.create_title")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label>{t("searchApps.name")}</Label>
              <Input value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label>{t("searchApps.datasets")}</Label>
              <div className="max-h-48 space-y-1 overflow-y-auto rounded border p-2">
                {values.map((d: DatasetLike) => {
                  const dsId = mapId(d);
                  return (
                    <label key={d.id} className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={selected.includes(dsId)} onChange={() => toggle(dsId)} />
                      {d.name}
                    </label>
                  );
                })}
                {values.length === 0 && (
                  <div className="text-sm text-muted-foreground">{t("searchApps.no_datasets")}</div>
                )}
              </div>
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>
                {t("ra.action.cancel")}
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

const SliderField = ({
  label,
  value,
  min,
  max,
  step,
  onChange,
}: {
  label: string;
  value: number;
  min: number;
  max: number;
  step: number;
  onChange: (v: number) => void;
}) => (
  <div className="space-y-1.5">
    <div className="flex items-center justify-between">
      <Label>{label}</Label>
      <span className="text-xs text-muted-foreground">{value.toFixed(step < 1 ? 2 : 0)}</span>
    </div>
    <input type="range" className="w-full accent-foreground" min={min} max={max} step={step} value={value} onChange={(e) => onChange(Number(e.target.value))} />
  </div>
);

const ViewDetailCell = () => {
  const t = useTranslate();
  const record = useRecordContext<SearchAppLike>();
  const createPath = useCreatePath();
  const navigate = useNavigate();
  if (!record) return null;
  return (
    <IconButtonWithTooltip
      label={t("searchApps.view_detail")}
      onClick={(e) => { e.stopPropagation(); navigate(createPath({ resource: "search-apps", type: "show", id: record.id })); }}
    >
      <Eye className="size-4" aria-hidden="true" />
    </IconButtonWithTooltip>
  );
};


const EditSearchAppCell = () => {
  const t = useTranslate();
  const record = useRecordContext<SearchAppLike>();
  const notify = useNotify();
  const qc = useQueryClient();
  const { data: datasets } = useGetList("datasets", { pagination: { page: 1, perPage: 200 } });
  const { values: editValues, mapId: editMapId } = datasetOptions(datasets as DatasetLike[] | undefined);
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [loadingCfg, setLoadingCfg] = useState(false);
  const [name, setName] = useState("");
  const [selected, setSelected] = useState<string[]>([]);
  const [similarity, setSimilarity] = useState(0.2);
  const [weight, setWeight] = useState(0.3);
  const [topK, setTopK] = useState(1024);
  if (!record) return null;

  const loadConfig = async () => {
    setOpen(true);
    setLoadingCfg(true);
    try {
      const res = await api.get<{ code: number; data: any }>(`/search-apps/${record.id}/config`);
      const data = res.data?.data ?? {};
      const c = data.search_config ?? {};
      setName(data.name ?? record.name ?? "");
      setSelected(Array.isArray(c.kb_ids) ? c.kb_ids : []);
      setSimilarity(Number(c.similarity_threshold) > 0 ? Number(c.similarity_threshold) : 0.2);
      setWeight(Number(c.vector_similarity_weight) > 0 ? Number(c.vector_similarity_weight) : 0.3);
      setTopK(Number(c.top_k) > 0 ? Number(c.top_k) : 1024);
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("searchApps.config_load_fail"), { type: "error" });
      setOpen(false);
    } finally {
      setLoadingCfg(false);
    }
  };

  const toggle = (id: string) =>
    setSelected((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));

  const save = async () => {
    if (!name.trim()) return;
    setSaving(true);
    try {
      await api.put(`/search-apps/${record.id}`, {
        name: name.trim(),
        search_config: {
          kb_ids: selected,
          similarity_threshold: similarity,
          vector_similarity_weight: weight,
          top_k: topK,
        },
      });
      notify(t("searchApps.updated"), { type: "success" });
      setOpen(false);
      qc.invalidateQueries({ queryKey: ["search-apps"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("searchApps.update_fail"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <IconButtonWithTooltip label={t("searchApps.edit_label")} onClick={(e) => { e.stopPropagation(); void loadConfig(); }}>
        <Pencil className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("searchApps.edit_title")}</DialogTitle>
          </DialogHeader>
          {loadingCfg ? (
            <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              <span>{t("searchApps.loading")}</span>
            </div>
          ) : (
            <div className="space-y-4">
              <div className="space-y-1.5">
                <Label>{t("searchApps.name")}</Label>
                <Input value={name} onChange={(e) => setName(e.target.value)} />
              </div>
              <div className="space-y-1.5">
                <Label>{t("searchApps.datasets")}</Label>
                <div className="max-h-40 space-y-1 overflow-y-auto rounded border p-2">
                  {editValues.map((d: DatasetLike) => {
                    const dsId = editMapId(d);
                    return (
                      <label key={d.id} className="flex items-center gap-2 text-sm">
                        <input type="checkbox" checked={selected.includes(dsId)} onChange={() => toggle(dsId)} />
                        {d.name}
                      </label>
                    );
                  })}
                  {editValues.length === 0 && (
                    <div className="text-sm text-muted-foreground">{t("searchApps.no_datasets")}</div>
                  )}
                </div>
              </div>
              <SliderField label={t("searchApps.similarity_threshold")} value={similarity} min={0.1} max={1} step={0.01} onChange={setSimilarity} />
              <SliderField label={t("searchApps.vector_weight")} value={weight} min={0.1} max={1} step={0.01} onChange={setWeight} />
              <SliderField label={t("searchApps.top_k")} value={topK} min={1} max={1024} step={1} onChange={setTopK} />
              <div className="flex justify-end gap-2">
                <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>
                  {t("ra.action.cancel")}
                </Button>
                <Button onClick={() => void save()} disabled={saving}>
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
const DeleteSearchAppCell = () => {
  const t = useTranslate();
  const record = useRecordContext<SearchAppLike>();
  const notify = useNotify();
  const qc = useQueryClient();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  if (!record) return null;
  const doDelete = async () => {
    setDeleting(true);
    try {
      await api.delete(`/search-apps/${record.id}`);
      notify(t("searchApps.deleted"), { type: "success" });
      qc.invalidateQueries({ queryKey: ["search-apps"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("searchApps.delete_fail"), {
        type: "error",
      });
    } finally {
      setDeleting(false);
      setConfirmOpen(false);
    }
  };
  return (
    <>
      <IconButtonWithTooltip label={t("searchApps.delete_label")} onClick={(e) => { e.stopPropagation(); setConfirmOpen(true); }} className="text-destructive!">
        <Trash2 className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t("confirm.delete_title")}
        message={t("searchApps.delete_msg", { name: record.name || record.id })}
        confirmLabel={t("confirm.delete_label")}
        onConfirm={() => void doDelete()}
        loading={deleting}
      />
    </>
  );
};

const EmptySearchAppGuidance = () => {
  const t = useTranslate();
  return (
    <div className="flex flex-col items-center gap-3 rounded border border-dashed p-8 text-center">
      <Search className="size-10 text-muted-foreground/50" />
      <div>
        <p className="text-sm font-medium">{t("searchApps.empty")}</p>
        <p className="mt-1 text-xs text-muted-foreground">{t("searchApps.empty_hint")}</p>
      </div>
    </div>
  );
};

const DatasetNamesCell = ({ value }: { value?: string }) => {
  const { data } = useGetList("datasets", {
    pagination: { page: 1, perPage: 200 },
    sort: { field: "name", order: "ASC" },
  });
  const names = (value || "")
    .split(",")
    .map((id) => id.trim())
    .filter(Boolean)
    .map((id) => (data ?? []).find((dataset: DatasetLike) => dataset.id === id)?.name || id);
  return <>{names.length ? names.join("、") : "-"}</>;
};

export const SearchAppList = () => {
  const t = useTranslate();
  const filters = [<SearchInput source="name" key="name" alwaysOn />];
  return (
    <List perPage={20} actions={<CreateSearchAppDialog />} filters={filters} aria-label={t("searchApps.list_title")}>
      <ListLoadingBar />
      <DataTable aria-label={t("searchApps.data_table")} empty={<EmptySearchAppGuidance />}>
        <DataTable.Col source="name" label={t("searchApps.name")} />
        <DataTable.Col source="status" label={t("searchApps.status")} className="hidden md:table-cell" />
        <DataTable.Col
          source="dataset_ids"
          label={t("searchApps.datasets")}
          className="hidden md:table-cell"
          render={(record) => <DatasetNamesCell value={(record as { dataset_ids?: string }).dataset_ids} />}
        />
        <DataTable.Col label="">
          <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
            <EditSearchAppCell />
            <ViewDetailCell />
            <DeleteSearchAppCell />
          </div>
        </DataTable.Col>
      </DataTable>
    </List>
  );
};
