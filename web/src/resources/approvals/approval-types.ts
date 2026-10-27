export type ApprovalRecord = {
  id: string;
  request_no: string;
  object_type: string;
  object_id: string;
  action: string;
  title: string;
  reason: string;
  status: string;
  payload_json: string;
  snapshot_json: string;
  result_json: string;
  last_error?: string;
  current_step?: number;
  requester_id?: string;
  expires_at?: string;
  submitted_at?: string;
  decided_at?: string;
  executed_at?: string;
  created_at?: string;
  updated_at?: string;
  steps?: ApprovalStepRecord[];
};

export type ApprovalStepRecord = {
  id: string;
  step_no: number;
  name: string;
  approver_type: string;
  approver_value: string;
  status: string;
  acted_by?: string | null;
  decision?: string;
  comment?: string;
  due_at?: string | null;
  acted_at?: string | null;
  approval_mode?: "any" | "all";
  approvers_json?: string;
  required_approvals?: number;
  actors?: ApprovalStepActorRecord[];
};

export type ApprovalStepActorRecord = {
  id: string;
  actor_id: string;
  delegated_by?: string;
  decision: "approve" | "reject";
  comment?: string;
  acted_at?: string;
};

export type ApprovalPolicyRecord = {
  id?: string;
  tenant_id?: string;
  object_type: string;
  action: string;
  enabled: boolean;
  priority: number;
  conditions_json: string;
  steps_json: string;
  expire_hours: number;
  version?: number;
  created_at?: string;
  updated_at?: string;
};

export type ApprovalPolicyStep = {
  step_no: number;
  name: string;
  approver_type: string;
  approver_value: string;
  expire_hours: number;
  approval_mode?: "any" | "all";
  approvers?: ApprovalApproverSpec[];
  required_approvals?: number;
};

export type ApprovalApproverSpec = {
  type: string;
  value: string;
};

export type ApprovalDelegationRecord = {
  id: string;
  principal_id: string;
  delegate_id: string;
  object_type: string;
  action: string;
  starts_at: string;
  ends_at: string;
};

export type ApprovalPolicyCondition = {
  field: string;
  op: string;
  value: string;
};
