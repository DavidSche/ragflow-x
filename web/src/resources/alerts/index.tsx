import type { ResourceProps } from "ra-core";
import { BellRing, Send } from "lucide-react";
import { AlertList } from "./AlertList";
import { AlertDeliveryList } from "./AlertDeliveryList";

export const alerts: ResourceProps = {
  name: "alerts",
  list: AlertList,
  icon: BellRing,
};

export const alertDeliveries: ResourceProps = {
  name: "alert-deliveries",
  list: AlertDeliveryList,
  icon: Send,
};
