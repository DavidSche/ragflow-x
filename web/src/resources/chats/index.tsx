import type { ResourceProps } from "ra-core";
import { MessageSquare } from "lucide-react";
import { ChatList } from "./ChatList";
import { ChatShow } from "./ChatShow";

export const chats: ResourceProps = {
  name: "chats",
  list: ChatList,
  show: ChatShow,
  recordRepresentation: (record: { name?: string }): string => record.name ?? "",
  options: { label: "Chat 管理" },
  icon: MessageSquare,
};
