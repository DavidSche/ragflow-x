import { DataTable, List, SearchInput, SelectInput, TextInput, BulkActionsToolbar } from "@/components/admin";
import { AutocompleteInput, ReferenceInput } from "@/components/admin";
import { useCreatePath, useNavigate, useRecordContext, useTranslate } from "ra-core";
import { Eye } from "lucide-react";
import { IconButtonWithTooltip } from "@/components/admin/icon-button-with-tooltip";
import type { ChatLike } from "./chat-types";
import { ChatActions, EditChatButton } from "./EditChatDialog";
import { BulkActions } from "./ChatBulkActions";
import { AssistantRouteGovernanceButton } from "../conversation-center/AssistantRouteGovernance";
import { UserNameLabel } from "@/components/admin/ReferenceLabel";

/* ── Row actions ─────────────────────────────────────────────────── */

const ViewCell = () => {
  const record = useRecordContext<ChatLike>();
  const createPath = useCreatePath();
  const navigate = useNavigate();
  const t = useTranslate();
  if (!record) return null;
  return (
    <IconButtonWithTooltip
      label={t("chats.view_detail")}
      onClick={() =>
        navigate(
          createPath({ resource: "chats", type: "show", id: record.id }),
        )
      }
    >
      <Eye className="size-4" aria-hidden="true" />
    </IconButtonWithTooltip>
  );
};

const EditCell = () => {
  const record = useRecordContext<ChatLike>();
  if (!record) return null;
  return <EditChatButton chat={{ id: record.id, name: record.name }} />;
};

const RouteGovernanceCell = () => {
  const record = useRecordContext<ChatLike>();
  if (!record) return null;
  return <AssistantRouteGovernanceButton kind="chat" targetId={record.id} name={record.name} />;
};

/* ── Main export ─────────────────────────────────────────────────── */

export const ChatList = () => {
  const t = useTranslate();
  const statusChoices = [
    { id: "active", name: t("chats.status_active") },
    { id: "disabled", name: t("chats.status_disabled") },
  ];
  const chatFilters = [
    <SearchInput source="name" key="name" alwaysOn />,
    <SelectInput source="status" label={t("chats.status_active")} choices={statusChoices} key="status" />,
    <ReferenceInput source="owner_id" reference="users" key="owner_id">
      <AutocompleteInput label={t("chats.filter_owner")} optionText="username" />
    </ReferenceInput>,
    <TextInput source="min_messages" label={t("chats.filter_min_messages")} key="min_messages" />,
  ];
  return (
    <List
      perPage={20}
      actions={<ChatActions />}
      filters={chatFilters}
      filterDefaultValues={{ name: "", min_messages: "" }}
      aria-label={t("chats.aria_chat_list")}
    >
      <DataTable
        bulkActionsToolbar={
          <BulkActionsToolbar>
            <BulkActions />
          </BulkActionsToolbar>
        }
        rowClick={false}
        aria-label={t("chats.aria_chat_table")}
      >
        <DataTable.Col source="name" label={t("chats.name")} />
        <DataTable.Col
          source="status"
          label={t("chats.status_active")}
          render={(r: ChatLike) => (r.status === "active" ? t("chats.status_active") : t("chats.status_disabled"))}
        />
        <DataTable.Col
          source="dataset_ids"
          label={t("chats.dataset")}
          className="hidden md:table-cell"
          render={(r: ChatLike) => r.dataset_ids || "-"}
        />
        <DataTable.Col
          source="message_count"
          label={t("chats.messages")}
          render={(r: ChatLike) => r.message_count ?? 0}
        />
        <DataTable.Col
          source="owner_id"
          label={t("chats.owner")}
          className="hidden lg:table-cell"
          render={(r: ChatLike) => <UserNameLabel id={r.owner_id} />}
        />
        <DataTable.Col
          source="created_at"
          label={t("chats.created_at")}
          render={(r: ChatLike) =>
            r.created_at ? new Date(r.created_at).toLocaleString() : "-"
          }
        />
        <DataTable.Col label="">
          <div className="flex items-center gap-1">
            <EditCell />
            <RouteGovernanceCell />
            <ViewCell />
          </div>
        </DataTable.Col>
      </DataTable>
    </List>
  );
};
