import { describe, expect, it } from "vitest";
import { assignableRoles, type AssignableRole } from "./user-roles";

const ROLES: AssignableRole[] = [
  { id: "tenant_admin", name: "租户管理员", status: "active" },
  { id: "legacy-orphan", name: "历史角色", status: "quarantined" },
  { id: "a-owned", name: "只读审计" },
];

describe("assignableRoles", () => {
  it("drops a quarantined role from the assignment picker", () => {
    expect(assignableRoles(ROLES).map((r) => r.id)).toEqual([
      "tenant_admin",
      "a-owned",
    ]);
  });

  it("keeps the role the user already holds, quarantined or not", () => {
    // Dropping it would make the <select> display the first option instead of
    // the truth: the account still holds that role (revocation is not what
    // quarantine does).
    expect(assignableRoles(ROLES, "legacy-orphan").map((r) => r.id)).toContain(
      "legacy-orphan",
    );
  });

  it("treats a missing status as active, because an old payload has no field", () => {
    expect(assignableRoles([{ id: "x", name: "x" }])).toHaveLength(1);
  });
});
