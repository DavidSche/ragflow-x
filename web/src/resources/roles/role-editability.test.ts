import { describe, expect, it } from "vitest";
import { isRoleWritable } from "./role-editability";

// The three write affordances in RoleList (edit, delete, save permissions) all
// read from this one decision, so a single wrong answer would put a button on
// screen that the server refuses (doc/315 finding 1).
describe("isRoleWritable", () => {
  it("allows a role the caller owns", () => {
    expect(isRoleWritable({ editable: true })).toBe(true);
  });

  it("refuses a shared role the caller may only read", () => {
    expect(isRoleWritable({ editable: false, builtin: false })).toBe(false);
  });

  it("refuses a built-in even if the server said editable", () => {
    expect(isRoleWritable({ editable: true, builtin: true })).toBe(false);
  });

  it("fails closed when the server does not send the flag", () => {
    expect(isRoleWritable({})).toBe(false);
    expect(isRoleWritable(undefined)).toBe(false);
    expect(isRoleWritable(null)).toBe(false);
  });
});
