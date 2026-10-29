package db

// BusinessUserReadIntent states what the built-in `business_user` role is meant
// to be able to *read* on each resource exposed by BuiltinPermissionMatrix().
//
// Why this file exists (doc/242): the built-in matrix is a flat list that grew
// resource-by-resource, and `business_user` was added later than `viewer`
// (migration v88 "add_business_user_role"). Every time a new governance
// resource landed, the author added `read` for viewer/operator/admin but often
// forgot the business user — which left 19 resources where a read-only role
// could read more than the role that actually does the work. (doc/245 later
// removed the viewer side of those 19 rows: viewer is now a strict read-only
// slice of business_user, so the inversion is gone in both directions and this
// file only has to decide what the working role may read.)
//
// Fixing those rows one by one would not fix the cause. Instead every resource
// must now carry an explicit intent, so adding a resource without deciding what
// business_user may read fails the build (permission_intent_test.go). `none` is
// a decision, not an omission: it must carry a rationale that a reviewer can
// argue with.
type BusinessUserReadIntent string

const (
	// BusinessUserReadAllowed grants `read` on the resource.
	BusinessUserReadAllowed BusinessUserReadIntent = "read"
	// BusinessUserReadDenied withholds `read`. Rationale is mandatory.
	BusinessUserReadDenied BusinessUserReadIntent = "none"
)

// PermissionJourney groups resources by the surface they belong to, so a
// reviewer can see at a glance which part of the product a grant serves.
type PermissionJourney string

const (
	JourneyWorkspace   PermissionJourney = "workspace"
	JourneyKnowledge   PermissionJourney = "knowledge"
	JourneyAssistant   PermissionJourney = "assistant"
	JourneyInteraction PermissionJourney = "interaction"
	JourneyOperations  PermissionJourney = "operations"
	JourneyGovernance  PermissionJourney = "governance"
	JourneyPlatform    PermissionJourney = "platform"
)

// BusinessUserReadRule is one resource's declared read intent.
type BusinessUserReadRule struct {
	Resource  string
	Intent    BusinessUserReadIntent
	Journey   PermissionJourney
	Rationale string
}

// businessUserReadRules is exhaustive over BuiltinPermissionMatrix() resources.
// Adding a resource to the matrix without adding a rule here fails
// TestBusinessUserReadIntentCoversMatrix.
var businessUserReadRules = []BusinessUserReadRule{
	// ---- workspace -------------------------------------------------------
	{Resource: "tenant", Intent: BusinessUserReadDenied, Journey: JourneyPlatform,
		Rationale: "Workspace metadata and cross-tenant governance views are admin surfaces."},
	{Resource: "user", Intent: BusinessUserReadAllowed, Journey: JourneyWorkspace,
		Rationale: "Member directory backs ownership labels and author names."},
	{Resource: "role", Intent: BusinessUserReadDenied, Journey: JourneyPlatform,
		Rationale: "Role definitions are administered by workspace admins."},
	{Resource: "branding", Intent: BusinessUserReadDenied, Journey: JourneyPlatform,
		Rationale: "Branding assets are a workspace-admin surface."},
	{Resource: "team", Intent: BusinessUserReadDenied, Journey: JourneyWorkspace,
		Rationale: "Teams are administered by workspace/team admins; dataset.project_id already carries the ownership label a consumer needs."},
	{Resource: "project", Intent: BusinessUserReadDenied, Journey: JourneyWorkspace,
		Rationale: "Projects are an administration surface; datasets expose their project label without requiring project:read."},
	{Resource: "user-clearance", Intent: BusinessUserReadDenied, Journey: JourneyGovernance,
		Rationale: "Sensitive-content clearance is issued by workspace admins."},

	// ---- knowledge -------------------------------------------------------
	{Resource: "dataset", Intent: BusinessUserReadAllowed, Journey: JourneyKnowledge,
		Rationale: "Users must see which datasets back the assistants they are allowed to use."},
	{Resource: "document", Intent: BusinessUserReadAllowed, Journey: JourneyKnowledge,
		Rationale: "Users add documents and need to see what they added."},
	{Resource: "logical-document", Intent: BusinessUserReadAllowed, Journey: JourneyKnowledge,
		Rationale: "Logical documents are the read model behind document listings."},
	{Resource: "dataset-export", Intent: BusinessUserReadDenied, Journey: JourneyKnowledge,
		Rationale: "Bulk dataset export is an operator/admin action with its own export audit trail."},
	{Resource: "task", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "The task projection is tenant-wide (ListTasks has no caller scoping), so read would expose other users' ingestion jobs."},
	{Resource: "knowledge-ops", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "Knowledge operations is the operator workbench."},
	{Resource: "knowledge-strategy", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "Retrieval strategy configuration belongs to knowledge operators."},
	{Resource: "knowledge-profile", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "Knowledge profiles are an operator tuning surface."},
	{Resource: "knowledge-lifecycle", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "Lifecycle states drive operator remediation, not consumption."},
	{Resource: "compilation", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "Compilation runs are an operator surface with execution rights."},
	{Resource: "parser-policy", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "Parser policies configure ingestion and are managed by operators."},
	{Resource: "quality-profile", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "Quality profiles are a tuning surface for knowledge operators."},
	{Resource: "source-routing-rule", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "Source routing rules are ingestion configuration."},
	{Resource: "prompt-policy", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "Prompt policies govern answer generation and are operator/admin owned."},
	{Resource: "document-governance", Intent: BusinessUserReadDenied, Journey: JourneyGovernance,
		Rationale: "Document attribution and recovery runs are a governance surface; a user who deleted an own document requests recovery through the approval workflow."},

	// ---- assistant -------------------------------------------------------
	{Resource: "assistant", Intent: BusinessUserReadAllowed, Journey: JourneyAssistant,
		Rationale: "Consuming assistants requires reading them."},
	{Resource: "search-app", Intent: BusinessUserReadAllowed, Journey: JourneyAssistant,
		Rationale: "Search apps are consumed by the same audience."},
	{Resource: "agent", Intent: BusinessUserReadAllowed, Journey: JourneyAssistant,
		Rationale: "Agents are consumed by the same audience."},
	{Resource: "scenario-template", Intent: BusinessUserReadAllowed, Journey: JourneyAssistant,
		Rationale: "The template catalog and onboarding readiness are how a consumer discovers what a workspace can offer, and the only non-403 path out of the empty conversation state (doc/238 §3)."},
	{Resource: "trial-run", Intent: BusinessUserReadAllowed, Journey: JourneyAssistant,
		Rationale: "Trial runs keep exactly the visibility they had through scenario-template:read (doc/244): a consumer may inspect the practice result of an assistant they can use, but starting one is authoring work that stays with team admins and operators."},

	// ---- interaction -----------------------------------------------------
	{Resource: "chat", Intent: BusinessUserReadAllowed, Journey: JourneyInteraction,
		Rationale: "Conversation history and answer export are keyed to chat:read."},
	{Resource: "memory", Intent: BusinessUserReadAllowed, Journey: JourneyInteraction,
		Rationale: "Users read the memory configuration that applies to them."},
	{Resource: "memory-content", Intent: BusinessUserReadAllowed, Journey: JourneyInteraction,
		Rationale: "Users read memory content scoped to themselves."},
	{Resource: "memory-policy", Intent: BusinessUserReadAllowed, Journey: JourneyInteraction,
		Rationale: "Users see the policy that governs their memory without managing it."},
	{Resource: "memory-operation", Intent: BusinessUserReadAllowed, Journey: JourneyInteraction,
		Rationale: "Read-only runtime status of the memory subsystem."},
	{Resource: "memory-write", Intent: BusinessUserReadDenied, Journey: JourneyInteraction,
		Rationale: "Write-only resource: it grants execute without a read action by design."},
	{Resource: "memory-forget", Intent: BusinessUserReadDenied, Journey: JourneyInteraction,
		Rationale: "Execute-only resource: forgetting is an action, not a readable surface."},
	{Resource: "context-decision", Intent: BusinessUserReadAllowed, Journey: JourneyInteraction,
		Rationale: "Context decisions explain why an answer used the context it did."},
	{Resource: "evidence-snapshot", Intent: BusinessUserReadDenied, Journey: JourneyInteraction,
		Rationale: "Answer snapshots are exported through chat:read; a second snapshot grant would only widen the surface without adding a capability."},

	// ---- operations ------------------------------------------------------
	{Resource: "usage", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "Usage is aggregated per tenant and day with estimated cost; financial visibility stays with admins and operators."},
	{Resource: "usage-export", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "Usage export is a billing/download surface owned by admins."},
	{Resource: "dashboard", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "The dashboard aggregates tenant-wide quality and usage metrics with no per-caller scoping."},
	{Resource: "alert", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "Alerts are operational incidents handled by operators."},
	{Resource: "outbox-event", Intent: BusinessUserReadDenied, Journey: JourneyGovernance,
		Rationale: "Outbox events exist for operator troubleshooting."},
	{Resource: "eval-set", Intent: BusinessUserReadDenied, Journey: JourneyOperations,
		Rationale: "eval-set governs evaluation configuration and release evidence; trial runs have their own resource since doc/244, so nothing consumer-facing needs this."},
	{Resource: "release-governance", Intent: BusinessUserReadDenied, Journey: JourneyGovernance,
		Rationale: "Release gates and evidence packs are a governance surface."},
	{Resource: "approval", Intent: BusinessUserReadDenied, Journey: JourneyGovernance,
		Rationale: "Approvals are a second-confirmation channel: submitting requires the caller to already hold manage on the target object, so a consumer has nothing to submit or read. Self-service access requests need a dedicated entitlement feature (doc/242)."},
	{Resource: "approval-policy", Intent: BusinessUserReadDenied, Journey: JourneyGovernance,
		Rationale: "Approval routing policies are configured by workspace admins."},

	// ---- governance / platform -------------------------------------------
	{Resource: "audit", Intent: BusinessUserReadDenied, Journey: JourneyGovernance,
		Rationale: "The audit chain is a compliance surface."},
	{Resource: "audit-anchor", Intent: BusinessUserReadDenied, Journey: JourneyGovernance,
		Rationale: "Anchor verification belongs with compliance auditors."},
	{Resource: "audit-export", Intent: BusinessUserReadDenied, Journey: JourneyGovernance,
		Rationale: "Audit export is denied by design; an existing regression test asserts the denial."},
	{Resource: "system", Intent: BusinessUserReadDenied, Journey: JourneyPlatform,
		Rationale: "Platform runtime settings and feature switches."},
	{Resource: "system-health", Intent: BusinessUserReadDenied, Journey: JourneyPlatform,
		Rationale: "Service health probes are an operator surface."},
	{Resource: "ragflow-sync", Intent: BusinessUserReadDenied, Journey: JourneyPlatform,
		Rationale: "Upstream RAGFlow synchronization is platform-only."},
	{Resource: "model-provider", Intent: BusinessUserReadDenied, Journey: JourneyPlatform,
		Rationale: "Model provider credentials and endpoints are admin owned."},
	{Resource: "model-route", Intent: BusinessUserReadDenied, Journey: JourneyPlatform,
		Rationale: "Routing rules between models are admin owned."},
	{Resource: "api-key", Intent: BusinessUserReadDenied, Journey: JourneyPlatform,
		Rationale: "API keys are credentials; listing them is an admin action."},
	{Resource: "enterprise-connection", Intent: BusinessUserReadDenied, Journey: JourneyPlatform,
		Rationale: "Enterprise identity connections are admin owned."},
	{Resource: "connector", Intent: BusinessUserReadDenied, Journey: JourneyPlatform,
		Rationale: "Connector bindings and credentials are admin/operator owned."},
	{Resource: "db-connection", Intent: BusinessUserReadDenied, Journey: JourneyPlatform,
		Rationale: "External database connections carry credentials."},
	{Resource: "tool-registry", Intent: BusinessUserReadDenied, Journey: JourneyPlatform,
		Rationale: "Tool registry entries execute server-side actions and are admin owned."},
	{Resource: "query-template", Intent: BusinessUserReadDenied, Journey: JourneyPlatform,
		Rationale: "Query templates are admin configuration."},
}

// businessUserReadRuleMap indexes businessUserReadRules by resource.
func businessUserReadRuleMap() map[string]BusinessUserReadRule {
	out := make(map[string]BusinessUserReadRule, len(businessUserReadRules))
	for _, rule := range businessUserReadRules {
		out[rule.Resource] = rule
	}
	return out
}
