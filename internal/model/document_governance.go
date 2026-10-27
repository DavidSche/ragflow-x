package model

import "time"

// 文档级治理标注（T2，doc/224 §2.1“结构性新建顶点”——文档级 ACL）。
//
// T2 在 doc/123 数据集级 sensitivity 下推之上增加文档级治理维度：
//   - 密级（内部/机密/绝密）→ 检索投影按用户 clearance 过滤；
//   - 权威度（等级分）→ 结果端说明性/二次过滤依据（MVP 不改语义排位）；
//   - 发布状态（草稿/已发布）→ 草稿文档不进已发布检索；
//   - 过期时间 → 过期视为已下线（不参与投影白名单）；
//   - 标注来源三通道（manual / approval / auto）→ 审批与审计闭环。
//
// 未标注文档保持可见（不因缺失治理记录而被误过滤），这是 123 §2.4“存量
// 文档无元数据”教训的镜像：治理标注只收紧候选集，绝不放大。

const (
	// 密级（三级）。rank 越大越敏感（见 GovernanceLevelRank）。
	GovernanceLevelInternal     = "internal"     // 内部
	GovernanceLevelConfidential = "confidential" // 机密
	GovernanceLevelTopSecret    = "top_secret"   // 绝密

	// 发布状态。
	GovernanceStatusDraft     = "draft"     // 草稿（不进入已发布检索）
	GovernanceStatusPublished = "published" // 已发布

	// 标注来源通道。
	GovernanceSourceManual   = "manual"   // X 侧人工标注（经权限门槛）
	GovernanceSourceApproval = "approval" // 审批工单通道（高敏标注必经）
	GovernanceSourceAuto     = "auto"     // Auto Metadata 委托同步

	// 审批状态。
	GovernanceApprovalNone     = "none"
	GovernanceApprovalPending  = "pending"
	GovernanceApprovalApproved = "approved"
	GovernanceApprovalRejected = "rejected"

	// 权威度默认等级分（0-100）。
	GovernanceDefaultAuthority = 50
	GovernanceMinAuthority     = 0
	GovernanceMaxAuthority     = 100

	// 触发审批的密级下限：标注为绝密时默认进入审批通道。
	GovernanceApprovalRequiredLevel = GovernanceLevelTopSecret
)

// GovernanceLevelRank maps a 密级 to its ordering (higher = more sensitive).
// Unknown or empty levels are ranked 0 (least sensitive), matching the 123
// fail-open reading for documents that carry no explicit level value.
func GovernanceLevelRank(level string) int {
	switch level {
	case GovernanceLevelTopSecret:
		return 3
	case GovernanceLevelConfidential:
		return 2
	case GovernanceLevelInternal:
		return 1
	default:
		return 0
	}
}

// ValidGovernanceLevel reports whether level is one of the built-in 密级 values.
func ValidGovernanceLevel(level string) bool {
	switch level {
	case GovernanceLevelInternal, GovernanceLevelConfidential, GovernanceLevelTopSecret:
		return true
	default:
		return false
	}
}

// ValidGovernanceStatus reports whether status is a built-in 发布状态 value.
func ValidGovernanceStatus(status string) bool {
	switch status {
	case GovernanceStatusDraft, GovernanceStatusPublished:
		return true
	default:
		return false
	}
}

// ValidGovernanceSource reports whether source is a built-in 标注来源 value.
func ValidGovernanceSource(source string) bool {
	switch source {
	case GovernanceSourceManual, GovernanceSourceApproval, GovernanceSourceAuto:
		return true
	default:
		return false
	}
}

// ValidAuthoritativeRank reports whether a 权威度 level score is within bounds.
func ValidAuthoritativeRank(authority int) bool {
	return authority >= GovernanceMinAuthority && authority <= GovernanceMaxAuthority
}

// DocumentGovernance is a single document's governance annotation.
type DocumentGovernance struct {
	ID       string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID string `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	// DatasetID is the X-side dataset link id; DocumentID is the RAGFlow
	// document id (source of truth for downstream retrieval filtering).
	DatasetID  string `gorm:"column:dataset_id;index;size:32;not null;uniqueIndex:uk_rgx_doc_governance_doc" json:"dataset_id"`
	DocumentID string `gorm:"column:document_id;index;size:64;not null;uniqueIndex:uk_rgx_doc_governance_doc" json:"document_id"`
	Level      string `gorm:"column:level;size:32;not null" json:"level"`
	// Authority is the 权威度 等级分 (0-100).
	Authority int `gorm:"column:authority;not null;default:50" json:"authority"`
	Status    string `gorm:"column:status;size:32;not null;default:published" json:"status"`
	Source    string `gorm:"column:source;size:32;not null;default:manual" json:"source"`
	Reason    string `gorm:"column:reason;size:1024" json:"reason"`
	ExpiresAt *time.Time `gorm:"column:expires_at;index" json:"expires_at"`

	ApprovalState string     `gorm:"column:approval_state;size:32;not null;default:none" json:"approval_state"`
	CreatedBy     string     `gorm:"column:created_by;size:32;not null" json:"created_by"`
	ApprovedBy    *string    `gorm:"column:approved_by;size:32" json:"approved_by"`
	ApprovedAt    *time.Time `gorm:"column:approved_at" json:"approved_at"`
	CreatedAt     time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (DocumentGovernance) TableName() string { return "rgx_document_governance" }

// Active reports whether the annotation currently participates in retrieval
// projection: approved (or not requiring approval) and not expired.
func (g *DocumentGovernance) Active(now time.Time) bool {
	if g == nil {
		return false
	}
	if g.ApprovalState == GovernanceApprovalRejected || g.ApprovalState == GovernanceApprovalPending {
		return false
	}
	if g.ExpiresAt != nil && !g.ExpiresAt.After(now) {
		return false
	}
	return true
}

// UserClearance records a user's 密级 clearance (RBAC 联动：用户密级越权时
// 其检索投影剔除超密级文档）。缺失记录按租户默认（internal, rank=1）。
type UserClearance struct {
	ID        string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID  string    `gorm:"column:tenant_id;index;size:32;not null;uniqueIndex:uk_rgx_user_clearance_user" json:"tenant_id"`
	UserID    string    `gorm:"column:user_id;index;size:32;not null;uniqueIndex:uk_rgx_user_clearance_user" json:"user_id"`
	Level     string    `gorm:"column:level;size:32;not null;default:internal" json:"level"`
	UpdatedBy string    `gorm:"column:updated_by;size:32;not null" json:"updated_by"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (UserClearance) TableName() string { return "rgx_user_clearance" }
