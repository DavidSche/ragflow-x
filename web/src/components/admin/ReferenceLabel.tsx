import { useGetList, useTranslate } from "ra-core";

interface ReferenceLike {
  id: string;
  name?: string;
  username?: string;
  title?: string;
}

function displayName(records: ReferenceLike[] | undefined, id: string) {
  const record = (records ?? []).find((item) => item.id === id);
  return record?.name || record?.username || record?.title;
}

export function UserNameLabel({ id }: { id?: string | null }) {
  const t = useTranslate();
  const { data, isLoading } = useGetList("users", {
    pagination: { page: 1, perPage: 200 },
    sort: { field: "username", order: "ASC" },
  });
  if (!id) return <>{t("common.unspecified")}</>;
  const name = displayName(data as ReferenceLike[] | undefined, id);
  if (isLoading && !name) return <>{t("common.loading")}</>;
  return <>{name ?? t("common.deleted_reference")}</>;
}

export function WorkspaceLabel({ id }: { id?: string | null }) {
  const t = useTranslate();
  const { data, isLoading } = useGetList("tenants", {
    pagination: { page: 1, perPage: 200 },
    sort: { field: "name", order: "ASC" },
  });
  if (!id) return <>{t("common.unspecified")}</>;
  const name = displayName(data as ReferenceLike[] | undefined, id);
  if (isLoading && !name) return <>{t("common.loading")}</>;
  return <>{name ?? t("common.deleted_reference")}</>;
}

export function RoleLabel({ id }: { id?: string | null }) {
  const t = useTranslate();
  const { data, isLoading } = useGetList("roles", {
    pagination: { page: 1, perPage: 200 },
    sort: { field: "name", order: "ASC" },
  });
  if (!id) return <>{t("common.unspecified")}</>;
  const name = displayName(data as ReferenceLike[] | undefined, id);
  if (isLoading && !name) return <>{t("common.loading")}</>;
  return <>{name ?? t("common.deleted_reference")}</>;
}
