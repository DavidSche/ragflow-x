import type { ResourceProps } from "ra-core";
import { ClipboardCheck, Settings2 } from "lucide-react";
import { ApprovalList } from "./ApprovalList";
import { ApprovalShow } from "./ApprovalShow";
import { ApprovalCreate } from "./ApprovalCreate";
import { ApprovalPoliciesList } from "./ApprovalPoliciesList";

export const approvals: ResourceProps = {
  name: "approvals",
  list: ApprovalList,
  show: ApprovalShow,
  create: ApprovalCreate,
  recordRepresentation: (record) => record.request_no,
  icon: ClipboardCheck,
};

export const approvalPolicies: ResourceProps = {
  name: "approval-policies",
  list: ApprovalPoliciesList,
  icon: Settings2,
};
