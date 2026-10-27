import type { ResourceProps } from "ra-core";
import { Users } from "lucide-react";
import { UserList } from "./UserList";
import { UserCreate } from "./UserCreate";

export const users: ResourceProps = {
  name: "users",
  list: UserList,
  create: UserCreate,
  recordRepresentation: (record) => record.username,
  icon: Users,
};
