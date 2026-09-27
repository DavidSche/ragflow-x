import { useState } from "react";
import { DataTable, List, ListLoadingBar, SearchInput, TextInput } from "@/components/admin";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import { useCreatePath, useNavigate, useNotify, useRecordContext, required } from "ra-core";
import { useTranslate } from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Eye, FolderKanban, Loader2, Pencil, Trash2 } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { CreateDialog } from "../../components/CreateDialog";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { FormField, useFieldValidation } from "../../components/FormField";

const projectFilters = [
  <SearchInput source="name" key="name" alwaysOn />,
];

const CreateProjectDialog = () => {
  const t = useTranslate();
  return (
    <CreateDialog resource="projects" title={t("projects.create_title")}>
      <TextInput source="name" label={t("projects.name")} validate={required()} />
      <TextInput source="description" label={t("projects.description")} />
    </CreateDialog>
  );
};

const ViewProjectCell = () => {
  const t = useTranslate();
  const record = useRecordContext<{ id: string; name?: string }>();
  const createPath = useCreatePath();
  const navigate = useNavigate();
  if (!record) return null;
  return (
    <IconButtonWithTooltip
      label={t("projects.view_detail")}
      onClick={(e) => { e.stopPropagation(); navigate(createPath({ resource: "projects", type: "show", id: record.id })); }}
    >
      <Eye className="size-4" aria-hidden="true" />
    </IconButtonWithTooltip>
  );
};
const EditProjectCell = () => {
  const t = useTranslate();
  const record = useRecordContext<{ id: string; name?: string; description?: string }>();
  const notify = useNotify();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const nameField = useFieldValidation(record?.name ?? "", { required: true });
  const descField = useFieldValidation(record?.description ?? "");

  if (!record) return null;

  const save = async () => {
    if (!nameField.validate()) return;
    setSaving(true);
    try {
      await api.put(`/projects/${record.id}`, {
        name: nameField.value,
        description: descField.value,
      });
      notify(t("projects.updated"), { type: "success" });
      setOpen(false);
      qc.invalidateQueries({ queryKey: ["projects"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("projects.update_fail"), {
        type: "error",
      });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <IconButtonWithTooltip label={t("projects.edit_label")} onClick={() => setOpen(true)}>
        <Pencil className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t("projects.edit_title")} {record.name}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            <FormField label={t("projects.name")} required error={nameField.error}>
              <Input {...nameField.inputProps} aria-required="true" />
            </FormField>
            <FormField label={t("projects.description")} error={descField.error}>
              <Input {...descField.inputProps} />
            </FormField>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>
                {t("ra.action.cancel")}
              </Button>
              <Button onClick={() => void save()} disabled={saving}>
                {saving ? <Loader2 className="size-4 animate-spin" /> : t("ra.action.save")}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
};

const DeleteProjectCell = () => {
  const t = useTranslate();
  const record = useRecordContext<{ id: string; name?: string }>();
  const notify = useNotify();
  const qc = useQueryClient();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  if (!record) return null;
  const doDelete = async () => {
    setDeleting(true);
    try {
      await api.delete(`/projects/${record.id}`);
      notify(t("projects.deleted"), { type: "success" });
      qc.invalidateQueries({ queryKey: ["projects"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("projects.delete_fail"), {
        type: "error",
      });
    } finally {
      setDeleting(false);
      setConfirmOpen(false);
    }
  };
  return (
    <>
      <IconButtonWithTooltip label={t("projects.delete_label")} onClick={() => setConfirmOpen(true)} className="text-destructive!">
        <Trash2 className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t("confirm.delete_title")}
        message={t("projects.delete_msg", { name: record.name || record.id })}
        confirmLabel={t("confirm.delete_label")}
        onConfirm={() => void doDelete()}
        loading={deleting}
      />
    </>
  );
};

const EmptyProjectGuidance = () => {
  const t = useTranslate();
  return (
  <div className="flex flex-col items-center gap-3 rounded border border-dashed p-8 text-center">
    <FolderKanban className="size-10 text-muted-foreground/50" />
    <div>
      <p className="text-sm font-medium">{t("projects.empty")}</p>
      <p className="mt-1 text-xs text-muted-foreground">
        {t("projects.empty_hint")}
      </p>
    </div>
  </div>
  );
};

export const ProjectList = () => {
  const t = useTranslate();
  return (
    <List perPage={20} actions={<CreateProjectDialog />} filters={projectFilters} aria-label={t("projects.list_title")}>
    <ListLoadingBar />
    <DataTable aria-label={t("projects.data_table")} empty={<EmptyProjectGuidance />}>
      <DataTable.Col source="name" label={t("projects.name")} />
      <DataTable.Col
        source="description"
        label={t("projects.description")}
        className="hidden md:table-cell"
        render={(r) => (
          <span className="line-clamp-1 text-muted-foreground">{r.description || "-"}</span>
        )}
      />
      <DataTable.Col
        source="created_at"
        label={t("projects.created_at")}
        render={(record) => new Date(record.created_at).toLocaleString()}
      />
      <DataTable.Col label="">
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <EditProjectCell />
          <ViewProjectCell />
          <DeleteProjectCell />
        </div>
      </DataTable.Col>
    </DataTable>
  </List>
  );
};



