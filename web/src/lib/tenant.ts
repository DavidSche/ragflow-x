/**
 * The platform (control-plane) workspace. It is a singleton in the database
 * (`model.PlatformTenantID`) and `GET /tenants` never returns it — the list is
 * filtered to real workspaces on purpose. Any UI that renders a tenant id
 * therefore has to recognise this one itself.
 */
export const PLATFORM_TENANT_ID = "00000000000000000000000000000000";
