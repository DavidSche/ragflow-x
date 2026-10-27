import { Create, SimpleForm, TextInput } from "@/components/admin";
import { required, useTranslate } from "ra-core";

export const TenantCreate = () => {
  const t = useTranslate();
  return (
  <Create redirect="list">
    <SimpleForm>
      <TextInput source="name" label={t("tenants.name")} validate={required()} />
    </SimpleForm>
  </Create>
  );
};
