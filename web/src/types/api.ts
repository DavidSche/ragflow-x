// Readable names for the generated OpenAPI types (doc/310 T310-03).
//
// The generator keys schemas by their Go path, so every consumer would
// otherwise write `components["schemas"]["github_com_ragflow-x_ragflow-x_internal_model.Role"]`.
// This file is the one place allowed to alias those keys: it imports nothing
// but types, so a rename on the server shows up here as a compile error rather
// than as a silently-`any` field (doc/specification/14 §3).
import type { components, paths } from "./openapi";

export type ApiSchemas = components["schemas"];
export type ApiPaths = paths;

/** The role as the API returns it, including the caller-derived write flags. */
export type ApiRole = ApiSchemas["github_com_ragflow-x_ragflow-x_internal_model.Role"];

/** The user row the console renders (`GET /api/v1/users`). */
export type ApiUser = ApiSchemas["github_com_ragflow-x_ragflow-x_internal_model.User"];

/** A workspace/tenant row (`GET /api/v1/tenants`). */
export type ApiTenant = ApiSchemas["github_com_ragflow-x_ragflow-x_internal_model.Tenant"];
