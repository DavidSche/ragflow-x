import { describe, expect, it } from "vitest";
import {
  APPROVAL_ACTIONS,
  APPROVAL_APPROVER_TYPES,
  APPROVAL_CONDITION_OPS,
  APPROVAL_OBJECTS,
  APPROVAL_STATUSES,
  approvalStatusVariant,
  approvalStepStatusVariant,
  parseJSONValue,
} from "./approval-ui";

describe("APPROVAL_STATUSES", () => {
  it("contains all 8 status values", () => {
    expect(APPROVAL_STATUSES).toHaveLength(8);
    expect(APPROVAL_STATUSES).toContain("pending_approval");
    expect(APPROVAL_STATUSES).toContain("approved");
    expect(APPROVAL_STATUSES).toContain("rejected");
    expect(APPROVAL_STATUSES).toContain("canceled");
    expect(APPROVAL_STATUSES).toContain("expired");
    expect(APPROVAL_STATUSES).toContain("executing");
    expect(APPROVAL_STATUSES).toContain("completed");
    expect(APPROVAL_STATUSES).toContain("execution_failed");
  });
});

describe("APPROVAL_OBJECTS", () => {
  it("contains all 6 object types", () => {
    expect(APPROVAL_OBJECTS).toHaveLength(9);
    expect(APPROVAL_OBJECTS).toContain("dataset");
    expect(APPROVAL_OBJECTS).toContain("document");
    expect(APPROVAL_OBJECTS).toContain("document-chunk");
    expect(APPROVAL_OBJECTS).toContain("api-key");
    expect(APPROVAL_OBJECTS).toContain("chat");
    expect(APPROVAL_OBJECTS).toContain("agent");
    expect(APPROVAL_OBJECTS).toContain("model-provider");
    expect(APPROVAL_OBJECTS).toContain("model-instance");
    expect(APPROVAL_OBJECTS).toContain("model-model");
  });
});

describe("APPROVAL_ACTIONS", () => {
  it("contains all 5 actions", () => {
    expect(APPROVAL_ACTIONS).toHaveLength(9);
    expect(APPROVAL_ACTIONS).toContain("create");
    expect(APPROVAL_ACTIONS).toContain("update");
    expect(APPROVAL_ACTIONS).toContain("delete");
    expect(APPROVAL_ACTIONS).toContain("revoke");
    expect(APPROVAL_ACTIONS).toContain("test");
    expect(APPROVAL_ACTIONS).toContain("parse");
    expect(APPROVAL_ACTIONS).toContain("stop");
    expect(APPROVAL_ACTIONS).toContain("enable");
    expect(APPROVAL_ACTIONS).toContain("disable");
  });
});

describe("APPROVAL_APPROVER_TYPES", () => {
  it("contains all 3 approver types", () => {
    expect(APPROVAL_APPROVER_TYPES).toHaveLength(3);
    expect(APPROVAL_APPROVER_TYPES).toContain("user");
    expect(APPROVAL_APPROVER_TYPES).toContain("role");
    expect(APPROVAL_APPROVER_TYPES).toContain("team");
  });
});

describe("APPROVAL_CONDITION_OPS", () => {
  it("contains all 5 condition operators", () => {
    expect(APPROVAL_CONDITION_OPS).toHaveLength(5);
    expect(APPROVAL_CONDITION_OPS).toContain("eq");
    expect(APPROVAL_CONDITION_OPS).toContain("neq");
    expect(APPROVAL_CONDITION_OPS).toContain("prefix");
    expect(APPROVAL_CONDITION_OPS).toContain("in");
    expect(APPROVAL_CONDITION_OPS).toContain("exists");
  });
});

describe("approvalStatusVariant", () => {
  it("returns secondary for pending_approval", () => {
    expect(approvalStatusVariant("pending_approval")).toBe("secondary");
  });

  it("returns default for completed", () => {
    expect(approvalStatusVariant("completed")).toBe("default");
  });

  it("returns destructive for rejected", () => {
    expect(approvalStatusVariant("rejected")).toBe("destructive");
  });

  it("returns destructive for canceled", () => {
    expect(approvalStatusVariant("canceled")).toBe("destructive");
  });

  it("returns destructive for expired", () => {
    expect(approvalStatusVariant("expired")).toBe("destructive");
  });

  it("returns destructive for execution_failed", () => {
    expect(approvalStatusVariant("execution_failed")).toBe("destructive");
  });

  it("returns outline for unknown status", () => {
    expect(approvalStatusVariant("unknown")).toBe("outline");
  });

  it("returns outline for approved", () => {
    expect(approvalStatusVariant("approved")).toBe("outline");
  });

  it("returns outline for executing", () => {
    expect(approvalStatusVariant("executing")).toBe("outline");
  });
});

describe("approvalStepStatusVariant", () => {
  it("returns secondary for current", () => {
    expect(approvalStepStatusVariant("current")).toBe("secondary");
  });

  it("returns default for approved", () => {
    expect(approvalStepStatusVariant("approved")).toBe("default");
  });

  it("returns destructive for rejected", () => {
    expect(approvalStepStatusVariant("rejected")).toBe("destructive");
  });

  it("returns destructive for expired", () => {
    expect(approvalStepStatusVariant("expired")).toBe("destructive");
  });

  it("returns outline for pending", () => {
    expect(approvalStepStatusVariant("pending")).toBe("outline");
  });

  it("returns outline for skipped", () => {
    expect(approvalStepStatusVariant("skipped")).toBe("outline");
  });

  it("returns outline for unknown status", () => {
    expect(approvalStepStatusVariant("unknown")).toBe("outline");
  });
});

describe("parseJSONValue", () => {
  it("returns fallback for undefined input", () => {
    expect(parseJSONValue(undefined, { default: true })).toEqual({ default: true });
  });

  it("returns fallback for empty string input", () => {
    expect(parseJSONValue("", [])).toEqual([]);
  });

  it("returns fallback for invalid JSON", () => {
    expect(parseJSONValue("not json", "fallback")).toBe("fallback");
  });

  it("parses valid JSON object", () => {
    const input = '{"key": "value", "count": 42}';
    expect(parseJSONValue(input, {})).toEqual({ key: "value", count: 42 });
  });

  it("parses valid JSON array", () => {
    const input = '[{"step_no": 1, "name": "step1"}]';
    expect(parseJSONValue(input, [])).toEqual([{ step_no: 1, name: "step1" }]);
  });

  it("parses valid JSON string", () => {
    expect(parseJSONValue('"hello"', "")).toBe("hello");
  });

  it("parses valid JSON number", () => {
    expect(parseJSONValue("42", 0)).toBe(42);
  });

  it("parses valid JSON boolean", () => {
    expect(parseJSONValue("true", false)).toBe(true);
  });

  it("parses valid JSON null", () => {
    expect(parseJSONValue("null", "fallback")).toBeNull();
  });
});
