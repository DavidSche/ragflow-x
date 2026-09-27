import { Create, SelectInput, SimpleForm, TextInput } from "@/components/admin";
import { minLength, required, useGetList, useTranslate } from "ra-core";
import { validateEmailFormat, validateUniqueUsername } from "./user-validators";

const RoleField = () => {
  const { data } = useGetList("roles", {
    pagination: { page: 1, perPage: 200 },
  });
  const choices = (data ?? []).map((r) => ({ id: r.id, name: r.name }));
  const t = useTranslate();
  return <SelectInput source="role" label={t("users.role")} choices={choices} validate={required()} />;
};

const TenantField = () => {
  const { data } = useGetList("tenants", {
    pagination: { page: 1, perPage: 200 },
  });
  const choices = (data ?? []).map((t) => ({ id: t.id, name: t.name }));
  const tr = useTranslate();
  return <SelectInput source="tenant_id" label={tr("users.tenant")} choices={choices} />;
};

export const UserCreate = () => {
  const t = useTranslate();
  return (
  <Create redirect="list">
    <SimpleForm>
      <TenantField />
      <TextInput
        source="username"
        label={t("users.username")}
        validate={[required(), (value?: string) => validateUniqueUsername(value, t("users.username_taken"))]}
      />
      <TextInput
        source="password"
        label={t("users.password")}
        type="password"
        validate={[required(), minLength(8)]}
      />
      <TextInput
        source="email"
        label={t("users.email")}
        validate={(value?: string) => validateEmailFormat(value) ? t("users.email_invalid") : undefined}
      />
      <RoleField />
    </SimpleForm>
  </Create>
  );
};
