import type { ResourceProps } from "ra-core";
import { KeyRound } from "lucide-react";
import { APIKeyList } from "./APIKeyList";
import { APIKeyCreate } from "./APIKeyCreate";

export const apiKeys: ResourceProps = {
  name: "api-keys",
  list: APIKeyList,
  create: APIKeyCreate,
  recordRepresentation: (record) => record.name,
  icon: KeyRound,
};
