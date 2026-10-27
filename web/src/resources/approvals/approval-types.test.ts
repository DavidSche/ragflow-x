import { describe, expect, it } from "vitest";
import type {
  ApprovalRecord,
  ApprovalStepRecord,
  ApprovalPolicyRecord,
  ApprovalPolicyStep,
  ApprovalPolicyCondition,
} from "./approval-types";

describe("ApprovalRecord type", () => {
  it("can be created with required fields", () => {
    const record: ApprovalRecord = {
      id: "approval-1",
      request_no: "APR-20260831-001",
      object_type: "dataset",
      object_id: "ds-123",
      action: "delete",
      title: "Delete dataset",
      reason: "Testing",
      status: "pending_approval",
      payload_json: "{}",
      snapshot_json: "{}",
      result_json: "{}",
    };
    expect(record.id).toBe("approval-1");
    expect(record.request_no).toBe("APR-20260831-001");
    expect(record.object_type).toBe("dataset");
    expect(record.action).toBe("delete");
    expect(record.status).toBe("pending_approval");
  });

  it("can be created with optional fields", () => {
    const record: ApprovalRecord = {
      id: "approval-1",
      request_no: "APR-20260831-001",
      object_type: "dataset",
      object_id: "ds-123",
      action: "delete",
      title: "Delete dataset",
      reason: "Testing",
      status: "pending_approval",
      payload_json: "{}",
      snapshot_json: "{}",
      result_json: "{}",
      last_error: "Error message",
      current_step: 1,
      requester_id: "user-1",
      expires_at: "2026-09-02T10:00:00Z",
      submitted_at: "2026-08-31T10:00:00Z",
      decided_at: "2026-08-31T11:00:00Z",
      executed_at: "2026-08-31T12:00:00Z",
      created_at: "2026-08-31T10:00:00Z",
      updated_at: "2026-08-31T12:00:00Z",
      steps: [],
    };
    expect(record.last_error).toBe("Error message");
    expect(record.current_step).toBe(1);
    expect(record.steps).toEqual([]);
  });
});

describe("ApprovalStepRecord type", () => {
  it("can be created with required fields", () => {
    const step: ApprovalStepRecord = {
      id: "step-1",
      step_no: 1,
      name: "Admin approval",
      approver_type: "role",
      approver_value: "tenant_admin",
      status: "current",
    };
    expect(step.id).toBe("step-1");
    expect(step.step_no).toBe(1);
    expect(step.name).toBe("Admin approval");
    expect(step.approver_type).toBe("role");
    expect(step.status).toBe("current");
  });

  it("can be created with optional fields", () => {
    const step: ApprovalStepRecord = {
      id: "step-1",
      step_no: 1,
      name: "Admin approval",
      approver_type: "user",
      approver_value: "user-123",
      status: "approved",
      acted_by: "user-123",
      decision: "approve",
      comment: "Looks good",
      due_at: "2026-09-02T10:00:00Z",
      acted_at: "2026-08-31T11:00:00Z",
    };
    expect(step.acted_by).toBe("user-123");
    expect(step.decision).toBe("approve");
    expect(step.comment).toBe("Looks good");
  });
});

describe("ApprovalPolicyRecord type", () => {
  it("can be created with required fields", () => {
    const policy: ApprovalPolicyRecord = {
      object_type: "dataset",
      action: "delete",
      enabled: true,
      priority: 100,
      conditions_json: "{}",
      steps_json: "[]",
      expire_hours: 72,
    };
    expect(policy.object_type).toBe("dataset");
    expect(policy.action).toBe("delete");
    expect(policy.enabled).toBe(true);
    expect(policy.priority).toBe(100);
    expect(policy.expire_hours).toBe(72);
  });

  it("can be created with optional fields", () => {
    const policy: ApprovalPolicyRecord = {
      id: "policy-1",
      tenant_id: "tenant-1",
      object_type: "dataset",
      action: "delete",
      enabled: true,
      priority: 100,
      conditions_json: '{"all": [{"field": "name", "op": "prefix", "value": "prod-"}]}',
      steps_json: '[{"step_no": 1, "name": "Admin", "approver_type": "role", "approver_value": "admin", "expire_hours": 48}]',
      expire_hours: 72,
      version: 2,
      created_at: "2026-08-31T10:00:00Z",
      updated_at: "2026-08-31T11:00:00Z",
    };
    expect(policy.id).toBe("policy-1");
    expect(policy.tenant_id).toBe("tenant-1");
    expect(policy.version).toBe(2);
  });
});

describe("ApprovalPolicyStep type", () => {
  it("can be created with all fields", () => {
    const step: ApprovalPolicyStep = {
      step_no: 1,
      name: "Department approval",
      approver_type: "role",
      approver_value: "department_manager",
      expire_hours: 48,
    };
    expect(step.step_no).toBe(1);
    expect(step.name).toBe("Department approval");
    expect(step.approver_type).toBe("role");
    expect(step.approver_value).toBe("department_manager");
    expect(step.expire_hours).toBe(48);
  });
});

describe("ApprovalPolicyCondition type", () => {
  it("can be created with all fields", () => {
    const condition: ApprovalPolicyCondition = {
      field: "dataset_name",
      op: "prefix",
      value: "finance-",
    };
    expect(condition.field).toBe("dataset_name");
    expect(condition.op).toBe("prefix");
    expect(condition.value).toBe("finance-");
  });

  it("supports different operators", () => {
    const eqCondition: ApprovalPolicyCondition = {
      field: "status",
      op: "eq",
      value: "active",
    };
    expect(eqCondition.op).toBe("eq");

    const inCondition: ApprovalPolicyCondition = {
      field: "region",
      op: "in",
      value: "us-east,us-west",
    };
    expect(inCondition.op).toBe("in");

    const existsCondition: ApprovalPolicyCondition = {
      field: "project_id",
      op: "exists",
      value: "",
    };
    expect(existsCondition.op).toBe("exists");
  });
});
