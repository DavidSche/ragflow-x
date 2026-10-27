package model

import "time"

// 受控治理字典（T1，doc/224 §2.2 / 223 §3 T1）。平台内置三档起步（密级
// level + 发布状态 status），租户扩展走审批审计（198 模式）。字典是治理
// 语义的唯一事实源：自动投影与 Schema 标注的 target 必须落在字典条目上，
// 保证跨租户可比与审计一致性（223 问题 1 结论：平台内置 + 租户扩展混合制）。

const (
	// DictCategory 字典轴。
	DictCategoryLevel  = "level"  // 密级轴 internal/confidential/top_secret...
	DictCategoryStatus = "status" // 发布状态轴 draft/published...

	// DictOrigin 条目来源。
	DictOriginPlatform = "platform" // X 发版维护的内置标准条目
	DictOriginTenant   = "tenant"   // 租户扩展条目（须审批）

	// DictApprovalState 租户扩展条目的审批状态（MVP 内联，与 T2 治理记录一致）。
	DictApprovalPending  = "pending"
	DictApprovalApproved = "approved"
	DictApprovalRejected = "rejected"
)

// GovernanceDictionaryEntry is one controlled-vocabulary term on a governance
// axis. Tenant "" marks a platform-maintained builtin term (immutable,
// shipped by X release; see migration seed). Tenant entries are created in
// approval pending state and only become Active once approved.
type GovernanceDictionaryEntry struct {
	ID            string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID      string     `gorm:"column:tenant_id;size:32;not null;default:'';uniqueIndex:uk_rgx_governance_dictionary_identity" json:"tenant_id"`
	Category      string     `gorm:"column:category;size:32;not null;uniqueIndex:uk_rgx_governance_dictionary_identity" json:"category"`
	Key           string     `gorm:"column:key;size:64;not null;uniqueIndex:uk_rgx_governance_dictionary_identity" json:"key"`
	Label         string     `gorm:"column:label;size:128;not null" json:"label"`
	Rank          int        `gorm:"column:rank;not null;default:1" json:"rank"`
	Origin        string     `gorm:"column:origin;size:16;not null;default:tenant" json:"origin"`
	ApprovalState string     `gorm:"column:approval_state;size:16;not null;default:pending" json:"approval_state"`
	Active        bool       `gorm:"column:active;not null;default:false" json:"active"`
	CreatedBy     string     `gorm:"column:created_by;size:32;not null" json:"created_by"`
	ApprovedBy    *string    `gorm:"column:approved_by;size:32" json:"approved_by"`
	ApprovedAt    *time.Time `gorm:"column:approved_at" json:"approved_at"`
	CreatedAt     time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (GovernanceDictionaryEntry) TableName() string { return "rgx_governance_dictionary" }

// BuiltinGovernanceDictionaryTerms is the platform-maintained controlled
// vocabulary shipped by X (三档起步), seeded by migration 150. Tenant extends
// on top under the same (category, key) identity space.
func BuiltinGovernanceDictionaryTerms() []GovernanceDictionaryEntry {
	return []GovernanceDictionaryEntry{
		{TenantID: "", Category: DictCategoryLevel, Key: "internal", Label: "内部", Rank: 1},
		{TenantID: "", Category: DictCategoryLevel, Key: "confidential", Label: "机密", Rank: 2},
		{TenantID: "", Category: DictCategoryLevel, Key: "top_secret", Label: "绝密", Rank: 3},
		{TenantID: "", Category: DictCategoryStatus, Key: "draft", Label: "草稿", Rank: 1},
		{TenantID: "", Category: DictCategoryStatus, Key: "published", Label: "已发布", Rank: 2},
	}
}

// EffectiveRank resolves the ordering rank for a (category,key): builtin terms
// carry their fixed rank; tenant extensions rank above the highest builtin of
// the same category by default.
func (e *GovernanceDictionaryEntry) EffectiveRank() int {
	if e.Rank > 0 {
		return e.Rank
	}
	return 100
}
