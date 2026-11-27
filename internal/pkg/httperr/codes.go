package httperr

// Business error codes that were split out of overloaded buckets.
//
// `40085` was declared as "角色不能是自身的父角色" (Role block). Later features
// reused it as a catch-all for "the request is well-formed but cannot be
// honoured": dataset capability, approval separation of duties, scenario
// template authoring, skeleton import and engine rejections. By 2026-11-17 it
// carried eighteen unrelated messages, so no client-side code → sentence table
// could be correct for it, and the live 4.151 drill showed the consequence: a
// wizard instantiation failure ("dataset not found in workspace") rendered as
// "角色不能是自身的父角色" (doc/294).
//
// Fix: one code per semantic. `40085` goes back to meaning exactly one thing
// (Role, above), and the request-precondition family moves to block
// 41100-41199, which no other module uses. The grouping inside the block
// mirrors the concern, so a reader can tell from the number where the refusal
// came from:
//
//	41100-41109  scenario template instantiation
//	41110-41119  scenario template content and skeleton import
//	41120-41129  dataset reference and model capability
//	41130-41139  approval workflow
//	41140-41149  upstream (RAGFlow) business rejection
//
// Every code in the block is declared once below and registered in Declared();
// codes_test.go fails if a code is added, duplicated or placed outside the
// block. When you add a code, also register it in the internal registry table
// doc/specification/12_前后端接口交互规范.md §2.2 (that document is not shipped
// with the repo, so the shipped contract note is the CHANGELOG entry) and add
// the Chinese sentence to web/src/lib/errors.ts.
const (
	// BlockMin/BlockMax bound the request-precondition block.
	BlockMin = 41100
	BlockMax = 41199

	// Scenario template instantiation (41100-41109).
	CodeInvalidAppType             = 41101 // app_type is not chat/search/agent
	CodeInstantiationProjectNeeded = 41102 // project_id missing
	CodeTemplateNotPublished       = 41103 // template status is not published
	CodeTemplateAppTypeUnsupported = 41104 // template does not support the requested form
	CodeTemplateAgentUnavailable   = 41105 // agent requested, template ships no canvas
	CodeScenarioPackMismatch       = 41106 // pack declares a different app type than requested

	// Scenario template content and skeleton import (41110-41119).
	CodeTemplatePayloadRequired     = 41110 // content update without content
	CodeTemplateSuggestionsMissing  = 41111 // suggested datasets do not resolve
	CodeSkeletonIDMalformed         = 41112 // skeleton id is not <source>:<upstream_id>
	CodeSkeletonSourceUnsupported   = 41113 // unknown skeleton source
	CodeScenarioModelDefaultMissing = 41114 // pack needs a model capability the tenant has no default for

	// Dataset reference and model capability (41120-41129).
	CodeDatasetOutsideWorkspace = 41120 // dataset id does not resolve inside the caller's workspace
	CodeEmbeddingModelMissing   = 41121 // engine has no embedding model registered
	CodeDatasetPatchEmpty       = 41122 // update carries no updatable field

	// Approval workflow (41130-41139).
	CodeApprovalAdjacentApproverConflict = 41130 // same user on adjacent steps

	// Upstream business rejection (41140-41149).
	CodeUpstreamRejected = 41140 // engine answered 200 with a non-zero envelope code
	// A duplicate Search App name is the one upstream rejection an operator can
	// fix without knowing the engine: rename it. It gets its own code because the
	// engine reports it as HTTP 200 + code 102 ("Duplicated search name."), which
	// used to be flattened into 50261 "configure search app failed" — a sentence
	// that sends the reader to the network instead of to the name field
	// (doc/304 §4 E169).
	CodeSearchAppNameTaken = 41141 // engine refuses a Search App rename onto a taken name

	// Credential lifecycle (41150-41159). Rotation is a request that is
	// well-formed but cannot be honoured, which is exactly this block's contract;
	// it does not get a 40044 (already "template payload is invalid" elsewhere) and
	// it must not borrow a 409 without a code, because the client has to be able to
	// tell "already rotated" from "revoked" without parsing English.
	CodeAPIKeyRotationConflict = 41150 // key is revoked or already rotated

	// Role inheritance (41160-41169). A cycle is a request that is
	// well-formed but cannot be honoured: every row exists, and the result
	// would still be a role that inherits from itself. It cannot borrow 40085
	// ("role cannot be its own parent") because a three-role loop has no
	// self-parent, and it cannot borrow 40086 ("parent role not found") because
	// the parent is right there.
	CodeRoleParentCycle = 41160 // chosen parent is (or becomes) a descendant of the role
	// The rest of the role-inheritance preconditions land here for the same
	// reason as 41160: a well-formed request that cannot be honoured. They used
	// to be loose `4008x` literals, which meant a role edit could answer with
	// "智能体名称不能为空" (40087) or "智能体 DSL 不能为空" (40088) — the codes were
	// already spoken for by agents, chat, tasks and document versions.
	CodeRoleParentScopeMismatch      = 41161 // parent sits in another scope (tenant vs platform)
	CodeRoleParentForeignTenant      = 41162 // parent belongs to another tenant
	CodeRoleScopeChangeWithChildren  = 41163 // scope change would orphan the role's children
	CodeRolePermissionOutsideCatalog = 41164 // submitted rule is not in the permission catalog

	// Tenant lifecycle (41165-41169). 40003 and 40004 were overloaded: setup.go already
	// answers 40003 for "base_url and api_key are required" and 40004 for "account does
	// not exist", while tenant.go reused both for "the platform tenant is immutable" and
	// "this tenant still owns resources". One code, one meaning is the rule the web
	// client maps sentences by, so the tenant pair moves into this block (doc/315 review).
	CodeTenantPlatformImmutable = 41165 // the platform tenant cannot be renamed, disabled or deleted
	CodeTenantNotEmpty          = 41166 // the tenant still owns users/teams/datasets and force was not given
	// 41167 is the approval gate. It used to be 40086, which role inheritance had already
	// claimed for "parent role not found": the console offered "父角色不存在" for a tenant
	// whose approval workflow was simply switched off. The two meanings are different
	// parties (a role edit / an operation that needs a second pair of eyes), so they get
	// different codes rather than a shared sentence.
	CodeApprovalWorkflowDisabled = 41167 // the operation needs the approval workflow, which is disabled
)

// Declaration documents one code: what it means and who may emit it.
type Declaration struct {
	Code    int
	Name    string
	Meaning string
}

// Declared is the registry for the block above. One row per code, one meaning
// per code — that invariant is what lets the web client map codes to sentences
// without guessing.
var Declared = []Declaration{
	{CodeInvalidAppType, "invalid_app_type", "应用类型不是 chat、search 或 agent"},
	{CodeInstantiationProjectNeeded, "instantiation_project_required", "实例化缺少 project_id"},
	{CodeTemplateNotPublished, "template_not_published", "场景模板未发布，不可实例化"},
	{CodeTemplateAppTypeUnsupported, "template_app_type_unsupported", "场景模板不支持所请求的应用形态"},
	{CodeTemplateAgentUnavailable, "template_agent_unavailable", "请求智能体形态，但模板没有智能体画布"},
	{CodeScenarioPackMismatch, "scenario_pack_app_type_mismatch", "模板 Pack 声明的形态与请求不一致"},

	{CodeTemplatePayloadRequired, "template_payload_required", "更新场景模板但没有提交内容"},
	{CodeTemplateSuggestionsMissing, "template_suggestions_missing", "模板建议的数据集解析不到"},
	{CodeSkeletonIDMalformed, "skeleton_id_malformed", "骨架 id 不是 <source>:<upstream_id>"},
	{CodeSkeletonSourceUnsupported, "skeleton_source_unsupported", "不支持的骨架来源"},
	{CodeScenarioModelDefaultMissing, "scenario_model_default_missing", "模板所需模型能力在本工作区没有默认模型"},

	{CodeDatasetOutsideWorkspace, "dataset_outside_workspace", "数据集不在调用方工作区内"},
	{CodeEmbeddingModelMissing, "embedding_model_missing", "引擎未注册嵌入模型"},
	{CodeDatasetPatchEmpty, "dataset_patch_empty", "更新请求没有可更新字段"},

	{CodeApprovalAdjacentApproverConflict, "approval_adjacent_approver_conflict", "相邻审批步骤由同一人审批"},

	{CodeUpstreamRejected, "upstream_rejected", "引擎以 HTTP 200 + 非零 envelope code 拒绝业务请求"},
	{CodeSearchAppNameTaken, "search_app_name_taken", "已存在同名搜索应用，引擎不允许重名"},
	{CodeAPIKeyRotationConflict, "api_key_rotation_conflict", "API Key 已吊销或已轮换，不能再次轮换"},
	{CodeRoleParentCycle, "role_parent_cycle", "父角色选择会把角色自己变成祖先（继承成环）"},
	{CodeRoleParentScopeMismatch, "role_parent_scope_mismatch", "父角色与角色的作用域不一致（tenant / platform 不能互相继承）"},
	{CodeRoleParentForeignTenant, "role_parent_foreign_tenant", "父角色属于其他租户"},
	{CodeRoleScopeChangeWithChildren, "role_scope_change_with_children", "角色仍有子角色，改作用域前要先把子角色挂到其他父级"},
	{CodeRolePermissionOutsideCatalog, "role_permission_outside_catalog", "提交的权限项不在权限目录内"},
	{CodeTenantPlatformImmutable, "tenant_platform_immutable", "平台工作区不可改名、停用或删除"},
	{CodeTenantNotEmpty, "tenant_not_empty", "工作区仍有用户/团队/数据集，未指定 force 时不可删除"},
	{CodeApprovalWorkflowDisabled, "approval_workflow_disabled", "该操作需要审批流，但审批流未启用"},
}
