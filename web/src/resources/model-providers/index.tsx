import type { ResourceProps } from "ra-core";
import { Cpu } from "lucide-react";
import { ModelProviderList } from "./ModelProviderList";
import { ModelProviderCreate } from "./ModelProviderCreate";

export const modelProviders: ResourceProps = {
  name: "model-providers",
  list: ModelProviderList,
  create: ModelProviderCreate,
  recordRepresentation: (record) => record.name,
  icon: Cpu,
};
