import type { ResourceProps } from "ra-core";
import { TrendingUp } from "lucide-react";
import { KnowledgeOpsBoard } from "./KnowledgeOpsBoard";

export const knowledgeOps: ResourceProps = {
  name: "knowledge-ops",
  list: KnowledgeOpsBoard,
  recordRepresentation: () => "知识运营",
  options: { label: "知识运营" },
  icon: TrendingUp,
};
