package service

// Fine-grained platform capabilities. doc/77 §4.6/§4.7 enumerates them as
// distinct platform abilities instead of one "platform admin" bool, and doc/248
// (R-26) wires them: until then they were declared in the permission matrix but
// had no enforcement point, so granting them changed nothing.
//
// Scope note: this file only makes the permissions executable. It does not
// narrow anything. Each gate keeps the broader governance.* permission as an
// accepted alternative, so a role that was working yesterday keeps working, and
// platform_admin's effective authority and audit attribution stay unchanged.
const (
	PermTenantAuditRead      = "tenant.audit.read"
	PermTenantUsageRead      = "tenant.usage.read"
	PermTenantResourceManage = "tenant.resource.manage"
	PermTenantResourceDelete = "tenant.resource.delete"
)

// governanceResource is the permission resource these platform capabilities are
// declared on (one platform-scoped pseudo-resource, doc/77 §4.7).
const governanceResource = "tenant"

// GovernanceReadRefs returns the acceptable permissions for a cross-workspace
// read of the given governance capability. The order matters for audit
// attribution: the broad permission is tried first so existing platform_admin
// traffic keeps recording "governance.read:tenant", while a delegated
// auditor-only role records the fine-grained permission it actually used.
func GovernanceReadRefs(capability string) []PermissionRef {
	refs := []PermissionRef{{Action: "governance.read", Resource: governanceResource}}
	switch capability {
	case "audit", "audit-anchor", "audit-export":
		// Every audit surface is one evidence capability: an auditor who may read
		// the log may read its anchors and export it, otherwise the delegated role
		// would be usable for one route out of three.
		refs = append(refs, PermissionRef{Action: PermTenantAuditRead, Resource: governanceResource})
	case "usage", "usage-export":
		refs = append(refs, PermissionRef{Action: PermTenantUsageRead, Resource: governanceResource})
	}
	return refs
}

// TenantResourceRefs returns the acceptable permissions for writing a workspace
// record. Deleting is deliberately a separate capability (doc/77 §4.6): a
// platform operator may edit workspace settings without being able to delete a
// workspace.
func TenantResourceRefs(deleting bool) []PermissionRef {
	refs := []PermissionRef{{Action: "governance.manage", Resource: governanceResource}}
	if deleting {
		return append(refs, PermissionRef{Action: PermTenantResourceDelete, Resource: governanceResource})
	}
	return append(refs, PermissionRef{Action: PermTenantResourceManage, Resource: governanceResource})
}
