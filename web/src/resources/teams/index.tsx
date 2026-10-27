import type { ResourceProps } from "ra-core";
import { UsersRound } from "lucide-react";
import { TeamList } from "./TeamList";
import { TeamShow } from "./TeamShow";

export const teams: ResourceProps = {
  name: "teams",
  list: TeamList,
  show: TeamShow,
  icon: UsersRound,
};
