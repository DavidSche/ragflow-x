package db

import "github.com/ragflow-x/ragflow-x/internal/model"

// This file declares the *intended* shape of the built-in role lattice and the
// action implications the matrix is expected to honour (doc/243).
//
// Why it exists: the grant matrix in migrate.go is a flat literal list. Nothing
// stops a hand-written edit from producing a role that is "more privileged" by
// name but strictly weaker in grants — that is exactly how business_user ended
// up unable to read resources a read-only viewer could read (doc/242), and how
// team_admin ended up unable to use the product at all (doc/243). The matrix
// alone cannot express intent, so intent lives here and the tests enforce it.
//
// Contract:
//   - every ordered pair of built-in roles is either a declared superset
//     relation or a declared incomparability (§1, §2);
//   - declared superset relations must hold on every resource and every action;
//   - declared incomparabilities must actually diverge (no stale declarations);
//   - any (role, resource) that carries manage/execute/append/test/session:create
//     must also carry read, unless the resource is action-only (§3).

// BuiltinRoles lists the built-in roles the lattice covers. Custom roles are
// out of scope: they inherit through rgx_role.parent_id and are resolved by the
// authorizer, not by this matrix.
var BuiltinRoles = []string{
	model.RolePlatformAdmin,
	model.RoleTenantAdmin,
	model.RoleOperator,
	model.RoleBusinessUser,
	model.RoleViewer,
	model.RoleTeamAdmin,
}

// RoleSuperset declares that Superset holds every grant Subset holds. This is
// the monotonicity guarantee: a role can only lose grants by moving *down* the
// lattice.
type RoleSuperset struct {
	Superset  string
	Subset    string
	Rationale string
}

// roleSupersetRelations is the intended partial order. It is deliberately not
// a total order: operator (content operations) and team_admin (team-scoped
// management) are peers below tenant_admin with disjoint privileges.
var roleSupersetRelations = []RoleSuperset{
	{
		Superset:  model.RolePlatformAdmin,
		Subset:    model.RoleTenantAdmin,
		Rationale: "平台管理员是工作区管理员的严格超集：工作区能做的平台都能做，跨租户读取另行由 scopeAll 收敛。",
	},
	{
		Superset:  model.RoleTenantAdmin,
		Subset:    model.RoleOperator,
		Rationale: "工作区管理员必须覆盖内容运营员：运营是最常见的工作区专职角色，管理员不能比它少任何权限。",
	},
	{
		Superset:  model.RoleTenantAdmin,
		Subset:    model.RoleTeamAdmin,
		Rationale: "工作区管理员必须覆盖团队管理员：团队管理员的能力是工作区管理员能力的子集，仅作用域收窄到 owned team。",
	},
	{
		Superset:  model.RoleTenantAdmin,
		Subset:    model.RoleBusinessUser,
		Rationale: "工作区管理员必须覆盖业务使用者：管理员排障时需要能进入业务使用者能进入的每一个界面。",
	},
	{
		Superset:  model.RoleTenantAdmin,
		Subset:    model.RoleViewer,
		Rationale: "工作区管理员必须覆盖只读访客：访客是业务使用者的只读切片（doc/245），管理员覆盖业务使用者即已覆盖它。",
	},
	{
		Superset:  model.RoleOperator,
		Subset:    model.RoleViewer,
		Rationale: "内容运营员必须覆盖只读访客：运营持有业务使用者的执行面，访客只持有其中可读的部分。",
	},
	{
		Superset: model.RoleBusinessUser,
		Subset:   model.RoleViewer,
		Rationale: "只读访客是业务使用者的只读切片（doc/245）。此前 viewer 额外持有 19 个租户级运营/治理/配置读面，而真正干活的 business_user 反而读不到——" +
			"这正是 doc/242 判为缺陷的读权倒挂，只是方向相反。收敛后 viewer 只保留业务读面且不得持有任何写动作（`TestViewerIsTheReadOnlySliceOfBusinessUser`），" +
			"因此它必须声明为 business_user 的子集，而不是「不可比」。",
	},
	{
		Superset:  model.RoleTeamAdmin,
		Subset:    model.RoleBusinessUser,
		Rationale: "团队管理员 = 业务使用者 + 团队范围管理（doc/243）。团队管理员首先是团队成员，不能出现「管理员进不去成员能进的界面」。",
	},
}

// RoleIncomparable declares two roles whose grant sets deliberately diverge in
// both directions. Every pair that is not a declared superset must appear here
// with a rationale a reviewer can argue with — silence is not an option.
type RoleIncomparable struct {
	Left      string
	Right     string
	Rationale string
}

var roleIncomparableRelations = []RoleIncomparable{
	{
		Left:  model.RoleBusinessUser,
		Right: model.RoleOperator,
		Rationale: "运营是业务使用者的超集，**但**有一条刻意留出的破口：`execute@memory-forget` 只给成员本人与管理员，运营员不得代替用户遗忘记忆" +
			"（doc/137 §10.2/§10.3：普通撤回限 owner/project 成员，治理删除必须 `manage` + 审批）。" +
			"这条隐私边界比「角色层级好看」更重要，因此该对声明为不可比，而不是把破口补平。" +
			"由 `TestP0_DBMIG_131_MemoryOperationOutboxIndexes` 与 `TestRoleIncomparableRelationsAreReal` 双向守护。",
	},
	{
		Left:  model.RoleOperator,
		Right: model.RoleTeamAdmin,
		Rationale: "运营与团队管理是两个正交职责：运营持有模型/API Key/审计/告警等租户级运维面，团队管理员持有 team/project/dataset 的 owned 管理面。" +
			"互不覆盖，避免任何一方被迫获得对方职责内的写权限。",
	},
}

// viewerConvergenceRemovedReads is the audit trail of doc/245: the 19 reads
// `viewer` used to hold that `business_user` never had. They were the mirror
// image of the inversion doc/242 fixed — a read-only role reading tenant-wide
// task queues, cost/dashboard aggregates, operations and governance ledgers and
// policy configuration that the working role could not see. Kept as data (not a
// comment) so a re-add is caught by name.
var viewerConvergenceRemovedReads = []string{
	"approval",            // 审批单：含申请人、理由、金额，租户级人事/治理可见性
	"compilation",         // 知识编译台账：运营面
	"dashboard",           // 经营聚合看板：成本与产出
	"document-governance", // 归属/移交治理台账
	"eval-set",            // 评测题集与评分：质量治理面
	"evidence-snapshot",   // 证据快照：发布证据链
	"knowledge-lifecycle", // 资产生命周期台账
	"knowledge-ops",       // 知识运营看板与处置
	"knowledge-profile",   // 知识画像策略配置
	"knowledge-strategy",  // 检索/切分策略配置
	"parser-policy",       // 解析策略配置
	"project",             // 项目组织面（business_user 亦无读）
	"prompt-policy",       // 提示词策略配置
	"quality-profile",     // 质量门阈值配置
	"release-governance",  // 发布治理台账
	"source-routing-rule", // 路由规则配置
	"task",                // 租户级任务队列投影
	"team",                // 团队组织面（business_user 亦无读）
	"usage",               // 成本与用量：财务敏感
}

// actionOnlyResources maps resources whose only meaningful action is a
// non-read verb. They intentionally have no read grant for anyone; the
// manage/execute ⊃ read rule in §3 must not fire on them.
var actionOnlyResources = map[string]string{
	"memory-write":  "写记忆是 append-only 动作，产品上没有「列出所有 memory-write」的读面；记忆内容通过 memory / memory-content 读取。",
	"memory-forget": "遗忘记忆是一次性动作（含审批留痕），没有可枚举的读面；删除结果通过 memory-operation 审计读取。",
}

// actionImplications are the action verbs that presuppose the ability to read
// the resource. manage/execute/append/test all need a list/detail view in the
// UI, so a role holding one of them without read is a hole, not a policy.
//
// delete:own belongs here for the same reason: "delete my own upload" is only
// reachable from the document list, so a contributor without document read
// could never exercise it.
var actionImplications = []string{"manage", "execute", "append", "test", "session:create", "delete:own"}

// ReadImpliedByAction reports whether holding action on a resource necessarily
// implies read on the same resource. The declaration above is enforced on the
// builtin matrix by TestActionImplicationRequiresRead; this accessor lets the
// authorization path apply the very same rule to custom roles, which are edited
// through PUT /roles/:id/permissions and can otherwise be saved with manage (or
// execute/append/test/session:create/delete:own) but no read (doc/247).
func ReadImpliedByAction(action string) bool {
	for _, implied := range actionImplications {
		if implied == action {
			return true
		}
	}
	return false
}

func roleSupersetSet() map[[2]string]struct{} {
	out := make(map[[2]string]struct{}, len(roleSupersetRelations))
	for _, rel := range roleSupersetRelations {
		out[[2]string{rel.Superset, rel.Subset}] = struct{}{}
	}
	return out
}

// roleSupersetClosure returns the transitive closure of the declared superset
// relations, so callers do not have to re-declare platform_admin ⊇ operator
// after declaring platform_admin ⊇ tenant_admin ⊇ operator.
func roleSupersetClosure() map[[2]string]bool {
	direct := roleSupersetSet()
	reach := func() map[string][]string {
		adj := map[string][]string{}
		for pair := range direct {
			adj[pair[0]] = append(adj[pair[0]], pair[1])
		}
		return adj
	}()
	closure := map[[2]string]bool{}
	for _, start := range BuiltinRoles {
		seen := map[string]bool{}
		stack := append([]string{}, reach[start]...)
		for len(stack) > 0 {
			next := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[next] || next == start {
				continue
			}
			seen[next] = true
			closure[[2]string{start, next}] = true
			stack = append(stack, reach[next]...)
		}
	}
	return closure
}

func roleIncomparableSet() map[[2]string]RoleIncomparable {
	out := make(map[[2]string]RoleIncomparable, len(roleIncomparableRelations))
	for _, rel := range roleIncomparableRelations {
		out[[2]string{rel.Left, rel.Right}] = rel
		out[[2]string{rel.Right, rel.Left}] = rel
	}
	return out
}

// roleGrantIndex materialises the built-in matrix as resource → role → action set.
func roleGrantIndex() map[string]map[string]map[string]struct{} {
	index := map[string]map[string]map[string]struct{}{}
	for _, grant := range BuiltinPermissionMatrix() {
		if index[grant.Resource] == nil {
			index[grant.Resource] = map[string]map[string]struct{}{}
		}
		if index[grant.Resource][grant.Role] == nil {
			index[grant.Resource][grant.Role] = map[string]struct{}{}
		}
		index[grant.Resource][grant.Role][grant.Action] = struct{}{}
	}
	return index
}
