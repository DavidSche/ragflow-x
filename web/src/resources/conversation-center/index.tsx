import type { ResourceProps } from "ra-core";
import { MessagesSquare } from "lucide-react";
import { ConversationCenter } from "./ConversationCenter";

export const conversationCenter: ResourceProps = {
  name: "conversation-center",
  list: ConversationCenter,
  recordRepresentation: () => "对话中心",
  options: { label: "对话中心" },
  icon: MessagesSquare,
};
