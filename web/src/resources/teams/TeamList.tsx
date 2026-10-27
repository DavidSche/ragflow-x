import { useState } from "react";
import {
  AutocompleteInput,
  DataTable,
  DateInput,
  FilterForm,
  FilterButton,
  List,
  ListLoadingBar,
  ReferenceInput,
  SearchInput,
  TextInput,
} from "@/components/admin";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import {
  useCreatePath,
  useGetIdentity,
  useListContext,
  useNavigate,
  useNotify,
  useRecordContext,
  useTranslate,
} from "ra-core";
import { useQueryClient } from "@tanstack/react-query";
import { Eye, FolderKanban, Trash2, Users, UsersRound } from "lucide-react";
import { api, ApiError } from "../../lib/api";
import { CreateDialog } from "../../components/CreateDialog";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { Button } from "@/components/ui/button";
import { UserNameLabel } from "@/components/admin/ReferenceLabel";

interface TeamRow {
  id: string;
  name?: string;
  owner_id?: string;
  owner_name?: string;
  tenant_name?: string;
  projects_count?: number;
  created_at?: string;
}

const TeamActions = () => {
  const t = useTranslate();
  return (
  <CreateDialog resource="teams" title={t("teams.create_title")}>
    <TextInput source="name" label={t("teams.name")} />
    <ReferenceInput source="owner_id" reference="users">
      <AutocompleteInput label={t("teams.owner")} optionText="username" />
    </ReferenceInput>
  </CreateDialog>
  );
};

const ViewMembersCell = () => {
  const record = useRecordContext<{ id: string }>();
  const createPath = useCreatePath();
  const navigate = useNavigate();
  const t = useTranslate();
  if (!record) return null;
  const to = createPath({ resource: "teams", type: "show", id: record.id });
  return (
    <IconButtonWithTooltip label={t("teams.view_members")} onClick={() => navigate(to)}>
      <Eye className="size-4" aria-hidden="true" />
    </IconButtonWithTooltip>
  );
};

const DeleteTeamCell = () => {
  const record = useRecordContext<{ id: string; name?: string }>();
  const notify = useNotify();
  const qc = useQueryClient();
  const t = useTranslate();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  if (!record) return null;
  const doDelete = async () => {
    setDeleting(true);
    try {
      await api.delete(`/teams/${record.id}`);
      notify(t("teams.deleted"), { type: "success" });
      qc.invalidateQueries({ queryKey: ["teams"] });
    } catch (err) {
      notify(err instanceof ApiError ? err.displayMessage : t("teams.delete_fail"), { type: "error" });
    } finally {
      setDeleting(false);
      setConfirmOpen(false);
    }
  };
  return (
    <>
      <IconButtonWithTooltip label={t("teams.delete_label")} onClick={() => setConfirmOpen(true)} className="text-destructive!">
        <Trash2 className="size-4" aria-hidden="true" />
      </IconButtonWithTooltip>
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={setConfirmOpen}
        title={t("confirm.delete_title")}
        message={`确定删除团队「${record.name || record.id}」吗？此操作不可撤销。`}
        confirmLabel={t("confirm.delete_label")}
        onConfirm={() => void doDelete()}
        loading={deleting}
      />
    </>
  );
};

const EmptyTeamGuidance = () => {
  const t = useTranslate();
  return (
  <div className="flex flex-col items-center gap-3 rounded border border-dashed p-8 text-center">
    <UsersRound className="size-10 text-muted-foreground/50" />
    <div>
      <p className="text-sm font-medium">{t("teams.empty")}</p>
      <p className="mt-1 text-xs text-muted-foreground">
        {t("teams.empty_hint")}
      </p>
    </div>
  </div>
  );
};

const TeamTableView = () => {
  const { data } = useListContext<TeamRow>();
  const { data: user } = useGetIdentity();
  const t = useTranslate();
  const [onlyMine, setOnlyMine] = useState(false);
  const rows = (data ?? []) as TeamRow[];
  const visible = onlyMine && user ? rows.filter((r) => r.owner_id === user.id) : rows;

  return (
    <>
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          variant={onlyMine ? "default" : "outline"}
          onClick={() => setOnlyMine((v) => !v)}
        >
          <Users className="size-4" /> {t("teams.only_mine")}
        </Button>
      </div>
      <DataTable data={visible} empty={<EmptyTeamGuidance />}>
        <DataTable.Col source="name" label={t("teams.name")} />
        <DataTable.Col
          source="owner_id"
          label={t("teams.owner")}
          className="hidden md:table-cell"
          render={(r: TeamRow) => r.owner_name ?? <UserNameLabel id={r.owner_id} />}
        />
        <DataTable.Col
          source="projects_count"
          label={t("teams.projects_count")}
          render={(r: TeamRow) => (
            <span className="inline-flex items-center gap-1">
              <FolderKanban className="size-3.5" /> {r.projects_count ?? 0}
            </span>
          )}
        />
        <DataTable.Col
          source="tenant_name"
          label={t("teams.tenant")}
          className="hidden md:table-cell"
          render={(r: TeamRow) => r.tenant_name ?? "-"}
        />
        <DataTable.Col
          source="created_at"
          label={t("teams.created_at")}
          render={(r: TeamRow) => new Date(r.created_at ?? "").toLocaleString()}
        />
        <DataTable.Col label="">
          <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
            <ViewMembersCell />
            <DeleteTeamCell />
          </div>
        </DataTable.Col>
      </DataTable>
    </>
  );
};

export const TeamList = () => {
  const t = useTranslate();
  const teamFilters = [
    <SearchInput source="name" key="name" alwaysOn />,
    <DateInput source="created_from" label={t("teams.created_from")} key="created_from" />,
    <DateInput source="created_to" label={t("teams.created_to")} key="created_to" />,
  ];
  return (
  <List perPage={20} actions={<TeamActions />} filters={teamFilters} aria-label={t("teams.aria_team_list")}>
    <ListLoadingBar />
    <TeamTableView />
  </List>
  );
};
