import {
  DataTable,
  List,
  ListLoadingBar,
  SearchInput,
  SelectInput,
  AutocompleteInput,
  ReferenceInput,
} from "@/components/admin";
import { useTranslate } from "ra-core";

export type EnterpriseConnectionRow = {
  id: string;
  provider_name?: string;
  display_name?: string;
  tenant_name?: string;
  managed_by?: string;
  visibility?: string;
  lifecycle_status?: string;
  runtime_health?: string;
};

export const EnterpriseConnectionList = () => {
  const t = useTranslate();
  const filters = [
    <SearchInput source="provider" key="provider" alwaysOn />,
    <ReferenceInput source="tenant_id" reference="tenants" key="tenant_id">
      <AutocompleteInput label={t("enterprise_connections.workspace")} optionText="name" />
    </ReferenceInput>,
    <SelectInput
      source="lifecycle_status"
      key="lifecycle_status"
      label="Lifecycle"
      choices={[
        { id: "ACTIVE", name: "Active" },
        { id: "DEPRECATED", name: "Deprecated" },
        { id: "DISABLED", name: "Disabled" },
        { id: "RETIRED", name: "Retired" },
      ]}
    />,
  ];

  return (
    <List
      perPage={20}
      filters={filters}
      aria-label={t("enterprise_connections.list_title")}
    >
      <ListLoadingBar />
      <DataTable
        aria-label={t("enterprise_connections.data_table")}
        empty={
          <div className="p-6 text-center text-sm text-muted-foreground">
            {t("enterprise_connections.empty")}
          </div>
        }
      >
        <DataTable.Col
          source="display_name"
          label={t("enterprise_connections.display_name")}
          render={(row: EnterpriseConnectionRow) => row.display_name || row.provider_name || row.id}
        />
        <DataTable.Col source="provider_name" label={t("enterprise_connections.provider_name")} />
        <DataTable.Col
          source="tenant_name"
          label={t("enterprise_connections.workspace")}
          className="hidden lg:table-cell"
          render={(row: EnterpriseConnectionRow) => row.tenant_name || "Platform"}
        />
        <DataTable.Col source="managed_by" label={t("enterprise_connections.managed_by")} className="hidden md:table-cell" />
        <DataTable.Col source="visibility" label={t("enterprise_connections.visibility")} className="hidden md:table-cell" />
        <DataTable.Col source="lifecycle_status" label={t("enterprise_connections.lifecycle_status")} />
        <DataTable.Col source="runtime_health" label={t("enterprise_connections.runtime_health")} className="hidden sm:table-cell" />
      </DataTable>
    </List>
  );
};
