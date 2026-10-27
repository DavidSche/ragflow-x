import { Create, SimpleForm, TextInput } from "@/components/admin";
import { required, useTranslate } from "ra-core";

export const DatasetCreate = () => {
  const t = useTranslate();
  return (
  <Create redirect="list">
    <SimpleForm>
      <TextInput source="name" label={t("datasets.name")} validate={required()} />
    </SimpleForm>
  </Create>
  );
};
