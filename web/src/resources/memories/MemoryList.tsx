import { useState } from "react";
import { DataTable, List, ListLoadingBar, SearchInput } from "@/components/admin";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import { useCreatePath, useNavigate, useNotify, useRecordContext, useTranslate } from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Eye, Loader2, Pencil, Plus, Trash2 } from "lucide-react";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { api, ApiError } from "../../lib/api";
import { MemoryConfigFields, emptyMemoryConfig, type MemoryConfigValues } from "./MemoryConfigFields";
import { clampInteger, clampNumber } from "@/lib/numeric";

interface MemoryLike {
  id: string;
  name: string;
  memory_type?: string;
  owner_id?: string;
}

const CreateMemoryDialog = () => {
  const t = useTranslate();
  const notify = useNotify();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState("");
  const [cfg, setCfg] = useState<MemoryConfigValues>(emptyMemoryConfig());

  const create = async () => {
    if (!name.trim() || !cfg.embd_id || !cfg.llm_id) return;
    setSaving(true);
    try {
      await api.post("/memories", {
        name: name.trim(),
        memory_type: cfg.memory_type,
        embd_id: cfg.embd_id,
        llm_id: cfg.llm_id,
      });
      notify(t("memories.created"), { type: "success" });
      setOpen(false);
      setName("");
      setCfg(emptyMemoryConfig());
      qc.invalidateQueries({ queryKey: ["memories"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("memories.create_fail"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <Button onClick={() => setOpen(true)}>
        <Plus className="size-4" aria-hidden="true" />
        {t("memories.create")}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t("memories.create_title")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-1.5">
              <Label>{t("memories.name")}</Label>
              <Input value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <MemoryConfigFields value={cfg} onChange={setCfg} mode="create" />
            {(!cfg.embd_id || !cfg.llm_id) ? (
              <p className="text-sm text-destructive">{t("memories.create_models_required")}</p>
            ) : null}
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>
                {t("ra.action.cancel")}
              </Button>
              <Button onClick={() => void create()} disabled={saving || !name.trim() || !cfg.embd_id || !cfg.llm_id}>
                {saving ? <Loader2 className="size-4 animate-spin" /> : t("ra.action.save")}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
};

const ViewDetailCell = () => {
  const t = useTranslate();
  const record = useRecordContext<MemoryLike>();
  const createPath = useCreatePath();
  const navigate = useNavigate();
  if (!record) return null;
  return (
    <IconButtonWithTooltip
      label={t("memories.view_detail")}
      onClick={(e) => { e.stopPropagation(); navigate(createPath({ resource: "memories", type: "show", id: record.id })); }}
    >
      <Eye className="size-4" aria-hidden="true" />
    </IconButtonWithTooltip>
  );
};

const EditMemoryCell = () => {
  const t = useTranslate();
  const record = useRecordContext<MemoryLike>();
  const notify = useNotify();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [loadingCfg, setLoadingCfg] = useState(false);
  const [name, setName] = useState(record?.name ?? "");
  const [cfg, setCfg] = useState<MemoryConfigValues>(emptyMemoryConfig());
  if (!record) return null;

  const loadConfig = async () => {
    setOpen(true);
    setLoadingCfg(true);
    try {
      const res = await api.get<{ code: number; data: any }>(`/memories/${record.id}/config`);
      const d = res.data?.data ?? {};
      setName(d.name ?? record.name ?? "");
      setCfg({
        memory_type: Array.isArray(d.memory_type) ? d.memory_type : [],
        embd_id: d.embd_id ?? "",
        llm_id: d.llm_id ?? "",
        memory_size: d.memory_size ?? "",
        permissions: d.permissions ?? "team",
        forgetting_policy: d.forgetting_policy ?? "FIFO",
        temperature: d.temperature ?? "0.5",
        description: d.description ?? "",
        system_prompt: d.system_prompt ?? "",
        user_prompt: d.user_prompt ?? "",
      });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("memories.config_load_fail"), { type: "error" });
      setOpen(false);
    } finally {
      setLoadingCfg(false);
    }
  };

  const save = async () => {
    if (!name.trim() || !cfg.embd_id || !cfg.llm_id) return;
    setSaving(true);
    try {
      const memorySize = Number(cfg.memory_size);
      const temperature = Number(cfg.temperature);
      await api.put(`/memories/${record.id}`, {
        name: name.trim(),
        memory_type: cfg.memory_type,
        embd_id: cfg.embd_id,
        llm_id: cfg.llm_id,
        memory_size: Number.isFinite(memorySize) ? clampInteger(memorySize, 1, 10485760, 5242880) : undefined,
        permissions: cfg.permissions,
        forgetting_policy: cfg.forgetting_policy,
        temperature: Number.isFinite(temperature) ? clampNumber(temperature, 0, 2, 0.5) : undefined,
        description: cfg.description,
        system_prompt: cfg.system_prompt,
        user_prompt: cfg.user_prompt,
      });
      notify(t("memories.updated"), { type: "success" });
      setOpen(false);
      qc.invalidateQueries({ queryKey: ["memories"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("memories.update_fail"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <IconButtonWithTooltip label={t("memories.edit_label")} onClick={(e) => { e.stopPropagation(); void loadConfig(); }}>
        <Pencil className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t("memories.edit_title")} {name || record.name || record.id}</DialogTitle>
          </DialogHeader>
          {loadingCfg ? (
            <div className="flex items-center gap-2 py-6 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" />
              <span>{t("memories.config_loading")}</span>
            </div>
          ) : (
            <div className="space-y-4">
              <div className="space-y-1.5">
                <Label>{t("memories.name")}</Label>
                <Input value={name} onChange={(e) => setName(e.target.value)} />
              </div>
              <MemoryConfigFields value={cfg} onChange={setCfg} mode="edit" />
              <div className="flex justify-end gap-2">
                <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>
                  {t("ra.action.cancel")}
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

const DeleteMemoryCell = () => {
  const t = useTranslate();
  const record = useRecordContext<MemoryLike>();
  const notify = useNotify();
  const qc = useQueryClient();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  if (!record) return null;
  const doDelete = async () => {
    setDeleting(true);
    try {
      await api.delete(`/memories/${record.id}`);
      notify(t("memories.deleted"), { type: "success" });
      qc.invalidateQueries({ queryKey: ["memories"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("memories.delete_fail"), { type: "error" });
    } finally {
      setDeleting(false);
      setConfirmOpen(false);
    }
  };
  return (
    <>
      <IconButtonWithTooltip label={t("memories.delete_label")} onClick={(e) => { e.stopPropagation(); setConfirmOpen(true); }} className="text-destructive!">
        <Trash2 className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t("confirm.delete_title")}
        message={t("memories.delete_msg", { name: record.name || record.id })}
        confirmLabel={t("confirm.delete_label")}
        onConfirm={() => void doDelete()}
        loading={deleting}
      />
    </>
  );
};

export const MemoryList = () => {
  const t = useTranslate();
  const filters = [<SearchInput source="name" key="name" alwaysOn />];
  return (
    <List perPage={20} actions={<CreateMemoryDialog />} filters={filters} aria-label={t("memories.list_title")}>
      <ListLoadingBar />
      <DataTable aria-label={t("memories.data_table")} empty={<div className="p-6 text-center text-sm text-muted-foreground">{t("memories.empty")}</div>}>
        <DataTable.Col source="name" label={t("memories.name")} />
        <DataTable.Col source="memory_type" label={t("memories.memory_type")} className="hidden md:table-cell" />
        <DataTable.Col label="">
          <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
            <EditMemoryCell />
            <ViewDetailCell />
            <DeleteMemoryCell />
          </div>
        </DataTable.Col>
      </DataTable>
    </List>
  );
};
