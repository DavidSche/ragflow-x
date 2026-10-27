import { useState } from "react";
import { useCanAccess, useListContext, useNotify, useRecordContext, useTranslate } from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { List, DataTable, SearchInput, AutocompleteInput, ReferenceInput } from "@/components/admin";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import { Plus, Settings, Server, Trash2 } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { ProviderSelectDialog } from "./ProviderSelectDialog";
import { ProviderManageDialog } from "./ProviderManageDialog";
import { getProviderLogo } from "./provider-logos";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { WorkspaceLabel } from "@/components/admin/ReferenceLabel";

function ModelProviderActions() {
  const { refetch } = useListContext();
  const t = useTranslate();
  const [open, setOpen] = useState(false);

  return (
    <>
      <Button onClick={() => setOpen(true)}>
        <Plus /> {t("providers.add_btn")}
      </Button>
      <ProviderSelectDialog
        open={open}
        onOpenChange={setOpen}
        onAdded={() => void refetch()}
      />
    </>
  );
}
function ManageCell({
  onManage,
}: {
  onManage: (id: string, name: string, tenantID: string) => void;
}) {
  const record = useRecordContext();
  const notify = useNotify();
  const t = useTranslate();
  const qc = useQueryClient();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const doDelete = async () => {
    const id = String(record?.id ?? "");
    if (!id) return;
    setDeleting(true);
    try {
      const tenantID = String(record?.tenant_id ?? "");
      const scopeQuery = tenantID
        ? `?scope=specific&tenant_id=${encodeURIComponent(tenantID)}`
        : "";
      await api.delete(`/model-providers/${id}${scopeQuery}`);
      notify(t("providers.deleted"), { type: "success" });
      qc.invalidateQueries({ queryKey: ["model-providers"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("providers.delete_fail"), {
        type: "error",
      });
    } finally {
      setDeleting(false);
      setConfirmOpen(false);
    }
  };
  return (
    <>
      <div className="flex items-center justify-end gap-1">
        <IconButtonWithTooltip
          label={t("providers.manage_label")}
          onClick={() =>
            onManage(
              String(record?.id ?? ""),
              String(record?.name ?? ""),
              String(record?.tenant_id ?? ""),
            )
          }
        >
          <Settings className="size-4" />
        </IconButtonWithTooltip>
        <IconButtonWithTooltip
          label={t("providers.delete_label")}
          onClick={() => setConfirmOpen(true)}
          className="text-destructive!"
        >
          <Trash2 className="size-4" aria-hidden="true" />
        </IconButtonWithTooltip>
      </div>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t("confirm.delete_title")}
        message={t("providers.delete_msg", { name: record?.name ?? "" })}
        confirmLabel={t("confirm.delete_label")}
        onConfirm={() => void doDelete()}
        loading={deleting}
      />
    </>
  );
}

function ProviderTenantCell() {
  const record = useRecordContext();
  if (!record?.tenant_name && !record?.tenant_id) return null;
  return (
    <span className="text-muted-foreground">
      {record.tenant_name ?? <WorkspaceLabel id={record.tenant_id} />}
    </span>
  );
}

function ProviderNameCell() {
  const record = useRecordContext();
  const { tag, Icon, color } = getProviderLogo(String(record?.name ?? ""));

  return (
    <span className="flex items-center gap-2 font-medium">
      <Icon aria-hidden="true" className={`size-4 shrink-0 ${color}`} />
      {record?.name}
    </span>
  );
}

const EmptyProviderGuidance = () => {
  const t = useTranslate();
  return (
  <div className="flex flex-col items-center gap-3 rounded border border-dashed p-8 text-center">
    <Server className="size-10 text-muted-foreground/50" />
    <div>
      <p className="text-sm font-medium">{t("providers.empty")}</p>
      <p className="mt-1 text-xs text-muted-foreground">
        {t("providers.empty_hint")}
      </p>
    </div>
  </div>
  );
};

export const ModelProviderList = () => {
  const t = useTranslate();
  const { canAccess: canFilterTenant } = useCanAccess({ resource: "model-provider", action: "governance.read" });
  const filters = canFilterTenant
    ? [<ReferenceInput source="tenant_id" reference="tenants" key="tenant_id">
        <AutocompleteInput label={t("tenants.tenant_name")} optionText="name" />
      </ReferenceInput>]
    : [];
  const [manage, setManage] = useState<{ id: string; name: string; tenantID: string } | null>(null);

  return (
    <List perPage={20} actions={<ModelProviderActions />} filters={filters} aria-label={t("providers.list_title")}>
      <DataTable aria-label={t("providers.data_table")} empty={<EmptyProviderGuidance />}>
        <DataTable.Col source="name" label={t("providers.name")} render={() => <ProviderNameCell />} />
        <DataTable.Col
          source="provider_type"
          label={t("providers.provider_type")}
          className="hidden md:table-cell"
        />
        <DataTable.Col
          source="base_url"
          label={t("providers.base_url")}
          className="hidden lg:table-cell"
        />
        <DataTable.Col
          label={t("providers.target_workspace")}
          className="hidden lg:table-cell"
          render={() => <ProviderTenantCell />}
        />
        <DataTable.Col
          label={t("providers.instance_model")}
          render={(r) => (
            <span className="whitespace-nowrap">
              {r.instance_count ?? 0} / {r.model_count ?? 0}
            </span>
          )}
        />
        <DataTable.Col
          label={t("providers.capability")}
          render={(r) =>
            Array.isArray(r.model_types) && r.model_types.length > 0 ? (
              <div className="flex max-w-56 flex-wrap gap-1">
                {r.model_types.map((mt: string) => (
                  <Badge key={mt} variant="secondary">
                    {mt}
                  </Badge>
                ))}
              </div>
            ) : (
              <span className="text-muted-foreground">-</span>
            )
          }
        />
        <DataTable.Col
          label={t("providers.status")}
          render={(r) => (
            <Badge variant={r.status === "active" ? "default" : "outline"}>
              {r.status === "active" ? t("providers.status_active") : t("providers.status_disabled")}
            </Badge>
          )}
        />
        <DataTable.Col label="">
          <ManageCell onManage={(id, name, tenantID) => setManage({ id, name, tenantID })} />
        </DataTable.Col>
      </DataTable>
      {manage ? (
        <ProviderManageDialog
          providerId={manage.id}
          providerName={manage.name}
          targetTenantID={manage.tenantID}
          open={!!manage}
          onOpenChange={(open) => {
            if (!open) setManage(null);
          }}
        />
      ) : null}
    </List>
  );
};

