import type { ApiRole } from "@/types/api";

// Whether the console may offer a role's write actions (edit, delete, save
// permissions). The API derives `editable` per caller (doc/315 finding 1): a
// shared role is readable everywhere but writable only by the platform, and a
// built-in is never writable.
//
// Undefined is "no". An older server that does not send the flag must make the
// console read-only rather than render a button whose request would 403 — the
// rule is fail-closed, so the two sides cannot disagree in the permissive
// direction.
//
// The shape is taken from the generated contract instead of being restated
// here: `editable`/`builtin` were added to the API in doc/315, and a hand-kept
// interface would have kept compiling after a rename (doc/310 T310-03).
export type RoleEditability = Pick<ApiRole, "builtin" | "editable">;

export function isRoleWritable(role: RoleEditability | undefined | null): boolean {
  if (!role) return false;
  if (role.builtin === true) return false;
  return role.editable === true;
}
