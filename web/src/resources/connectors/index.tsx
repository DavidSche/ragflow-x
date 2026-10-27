import type { ResourceProps } from "ra-core";
import { Cable } from "lucide-react";
import { ConnectorList } from "./ConnectorList";
import { ConnectorDetail } from "./ConnectorDetail";

/**
 * Data-source connectors resource (doc/225 §11.1). The create flow is a
 * four-step wizard opened from the list toolbar; the detail route hosts the
 * schedule editor and paged run history.
 */
export const connectors: ResourceProps = {
  name: "connectors",
  list: ConnectorList,
  show: ConnectorDetail,
  recordRepresentation: (record) => record.name,
  icon: Cable,
};
