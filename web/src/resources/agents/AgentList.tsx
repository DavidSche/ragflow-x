import { useState } from "react";
import { List, ListLoadingBar, SearchInput, DataTable } from "@/components/admin";
import { AutocompleteInput, ReferenceInput } from "@/components/admin";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import { useCanAccess, useCreatePath, useNavigate, useNotify, useRecordContext, useTranslate } from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { CheckCircle2, Eye, Loader2, Network, Plus, Route, Shapes, Trash2, XCircle } from "lucide-react";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { api, ApiError } from "../../lib/api";
import { AssistantRouteGovernanceButton } from "../conversation-center/AssistantRouteGovernance";
import { WorkspaceLabel } from "@/components/admin/ReferenceLabel";
import {
  AGENT_CANVAS_CATEGORY,
  DATAFLOW_CANVAS_CATEGORY,
  createDataflowEmptyDsl,
  createEmptyAgentDsl,
} from "./dslUtils";

interface AgentLike {
  id: string;
  title: string;
  status: string;
  release: boolean;
  tenant_id?: string;
  tenant_name?: string;
}

type AgentType = "workflow" | "dataflow" | "compiler";

const AGENT_TYPE_OPTIONS: {
  value: AgentType;
  labelKey: string;
  descKey: string;
  icon: typeof Network;
  disabled?: boolean;
}[] = [
  { value: "workflow", labelKey: "agents.type_workflow", descKey: "agents.type_workflow_desc", icon: Network },
  { value: "dataflow", labelKey: "agents.type_dataflow", descKey: "agents.type_dataflow_desc", icon: Route },
  { value: "compiler", labelKey: "agents.type_compiler", descKey: "agents.type_compiler_desc", icon: Shapes, disabled: true },
];

const CreateAgentDialog = () => {
  const t = useTranslate();
  const notify = useNotify();
  const qc = useQueryClient();
  const createPath = useCreatePath();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState("");
  const [agentType, setAgentType] = useState<AgentType>("workflow");
  const [release, setRelease] = useState(false);
  const [saving, setSaving] = useState(false);

  const create = async () => {
    setSaving(true);
    try {
      const isDataflow = agentType === "dataflow";
      const res = await api.post<{ code: number; data: { id: string } }>("/agents", {
        title,
        dsl: isDataflow ? createDataflowEmptyDsl() : createEmptyAgentDsl(),
        release,
        canvas_category: isDataflow ? DATAFLOW_CANVAS_CATEGORY : AGENT_CANVAS_CATEGORY,
      });
      notify(t("agents.created"), { type: "success" });
      setOpen(false);
      setTitle("");
      setAgentType("workflow");
      setRelease(false);
      qc.invalidateQueries({ queryKey: ["agents"] });
      const id = res.data?.data?.id;
      if (id) {
        navigate(createPath({ resource: "agents", type: "show", id }));
      }
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("agents.create_fail"), { type: "error" });
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <Button onClick={() => setOpen(true)}>
        <Plus className="size-4" aria-hidden="true" />
        {t("agents.create")}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t("agents.create_title")}</DialogTitle>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1.5">
              <Label>{t("agents.type")}</Label>
              <div className="grid grid-cols-3 gap-2">
                {AGENT_TYPE_OPTIONS.map((opt) => {
                  const Icon = opt.icon;
                  const active = agentType === opt.value;
                  return (
                    <button
                      key={opt.value}
                      type="button"
                      disabled={saving}
                      onClick={() => !opt.disabled && setAgentType(opt.value)}
                      className={`flex flex-col items-start gap-1 rounded border p-2.5 text-left transition-colors ${active ? "border-primary bg-primary/5" : "border-border hover:bg-muted"} ${opt.disabled && !active ? "opacity-60" : "cursor-pointer"}`}
                    >
                      <span className="flex items-center gap-1.5 text-sm font-medium">
                        <Icon className="size-4" aria-hidden="true" />
                        {t(opt.labelKey)}
                        {opt.disabled && (
                          <span className="rounded bg-amber-100 px-1 py-0.5 text-[10px] font-normal text-amber-700">
                            开发中
                          </span>
                        )}
                      </span>
                      <span className="text-xs text-muted-foreground">{t(opt.descKey)}</span>
                    </button>
                  );
                })}
              </div>
              {agentType === "compiler" && (
                <p className="text-xs text-amber-600">{t("agents.type_compiler_soon")}</p>
              )}
            </div>
            <div className="space-y-1.5">
              <Label>{t("agents.title")}</Label>
              <Input value={title} onChange={(e) => setTitle(e.target.value)} />
            </div>
            <div className="flex items-center gap-2">
              <input type="checkbox" checked={release} onChange={(e) => setRelease(e.target.checked)} />
              <Label>{t("agents.release")}</Label>
            </div>
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setOpen(false)} disabled={saving}>
                {t("ra.action.cancel")}
              </Button>
              <Button onClick={() => void create()} disabled={saving || !title.trim() || agentType === "compiler"}>
                {saving ? <Loader2 className="size-4 animate-spin" /> : t("agents.create")}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
};

const ViewCell = () => {
  const t = useTranslate();
  const record = useRecordContext<AgentLike>();
  const createPath = useCreatePath();
  const navigate = useNavigate();
  if (!record) return null;
  return (
    <IconButtonWithTooltip
      label={t("agents.view_detail")}
      onClick={() => navigate(createPath({ resource: "agents", type: "show", id: record.id }))}
    >
      <Eye className="size-4" aria-hidden="true" />
    </IconButtonWithTooltip>
  );
};

const RouteGovernanceCell = () => {
  const record = useRecordContext<AgentLike>();
  if (!record) return null;
  return <AssistantRouteGovernanceButton kind="agent" targetId={record.id} name={record.title} />;
};

const PublishCell = () => {
  const t = useTranslate();
  const record = useRecordContext<AgentLike>();
  const notify = useNotify();
  const qc = useQueryClient();
  if (!record) return null;
  const toggle = async () => {
    try {
      await api.post(`/agents/${record.id}/publish`, { release: !record.release });
      notify(record.release ? t("agents.unpublished") : t("agents.published"), { type: "success" });
      qc.invalidateQueries({ queryKey: ["agents"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("agents.publish_fail"), { type: "error" });
    }
  };
  return (
    <IconButtonWithTooltip
      label={record.release ? t("agents.unpublish") : t("agents.publish")}
      onClick={() => void toggle()}
      className={record.release ? "text-green-600!" : "text-muted-foreground"}
    >
      {record.release ? <CheckCircle2 className="size-4" aria-hidden="true" /> : <XCircle className="size-4" aria-hidden="true" />}
    </IconButtonWithTooltip>
  );
};

const DeleteCell = () => {
  const t = useTranslate();
  const record = useRecordContext<AgentLike>();
  const notify = useNotify();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  if (!record) return null;
  const del = async () => {
    setDeleting(true);
    try {
      await api.delete(`/agents/${record.id}`);
      notify(t("agents.deleted"), { type: "success" });
      qc.invalidateQueries({ queryKey: ["agents"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("agents.delete_fail"), { type: "error" });
    } finally {
      setDeleting(false);
      setOpen(false);
    }
  };
  return (
    <>
      <IconButtonWithTooltip label={t("agents.delete_label")} onClick={() => setOpen(true)} className="text-destructive!">
        <Trash2 className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={t("confirm.delete_title")}
        message={t("agents.delete_msg", { name: record.title || record.id })}
        confirmLabel={t("confirm.delete_label")}
        onConfirm={() => void del()}
        loading={deleting}
      />
    </>
  );
};

export const AgentList = () => {
  const t = useTranslate();
  const { canAccess: canFilterTenant } = useCanAccess({ resource: "agent", action: "governance.read" });
  const filters = [<SearchInput source="title" key="title" alwaysOn />];
  if (canFilterTenant) {
    filters.push(
      <ReferenceInput source="tenant_id" reference="tenants" key="tenant_id">
        <AutocompleteInput label={t("tenants.tenant_name")} optionText="name" />
      </ReferenceInput>,
    );
  }
  return (
    <List perPage={20} actions={<CreateAgentDialog />} filters={filters} aria-label={t("agents.list_title")}>
      <ListLoadingBar />
      <DataTable aria-label={t("agents.data_table")} empty={<div className="p-6 text-center text-sm text-muted-foreground">{t("agents.empty")}</div>}>
        <DataTable.Col source="title" label={t("agents.title")} />
        <DataTable.Col source="tenant_name" label={t("tenants.tenant_name")} className="hidden lg:table-cell" render={(r: AgentLike) => r.tenant_name || <WorkspaceLabel id={r.tenant_id} />} />
        <DataTable.Col source="status" label={t("agents.status")} className="hidden md:table-cell" />
        <DataTable.Col source="release" label={t("agents.release")} className="hidden md:table-cell" render={(r: AgentLike) => (r.release ? t("agents.released") : t("agents.not_released"))} />
        <DataTable.Col label="">
          <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
            <ViewCell />
            <RouteGovernanceCell />
            <PublishCell />
            <DeleteCell />
          </div>
        </DataTable.Col>
      </DataTable>
    </List>
  );
};
