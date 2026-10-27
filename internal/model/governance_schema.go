package model

import "time"

// GovernanceSchema 是治理词汇在数据集/文档上的抽取声明（T1，doc/224 §2.2）。
// 自动投影层用它对文档内容/元数据做正则或关键词匹配，命中后按字典 target
// 写入 document_governance（source=auto），并受抽取质量门槛约束（有效抽取
// 质量 < QualityMin 的文档不进入投影）。
const (
	// SchemaMatcher 匹配器类型。
	SchemaMatcherRegex   = "regex"   // 正则匹配文档内容（粗文本匹配 MVP）
	SchemaMatcherKeyword = "keyword" // 关键词包含匹配

	// SchemaBindScope 绑定粒度。
	SchemaBindDataset  = "dataset"  // 绑定整个 dataset（作用于该 dataset 全部文档）
	SchemaBindDocument = "document" // 绑定单个 document

	// GovernanceSchemaDefaultQualityMin 默认抽取质量门槛。
	GovernanceSchemaDefaultQualityMin = 60
)

// GovernanceSchema declares how a controlled-vocabulary target is projected
// from document content. A schema only takes effect when its target lands on
// an Active dictionary term of the same category.
type GovernanceSchema struct {
	ID          string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID    string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Name        string    `gorm:"column:name;size:128;not null" json:"name"`
	Category    string    `gorm:"column:category;size:32;not null" json:"category"`
	MatcherType string    `gorm:"column:matcher_type;size:16;not null;default:regex" json:"matcher_type"`
	Pattern     string    `gorm:"column:pattern;size:1024;not null" json:"pattern"`
	TargetKey   string    `gorm:"column:target_key;size:64;not null" json:"target_key"`
	QualityMin  int       `gorm:"column:quality_min;not null;default:60" json:"quality_min"`
	BindScope   string    `gorm:"column:bind_scope;size:16;not null;default:dataset" json:"bind_scope"`
	DatasetID   string    `gorm:"column:dataset_id;size:32;not null;default:''" json:"dataset_id"`
	DocumentID  string    `gorm:"column:document_id;size:64;not null;default:''" json:"document_id"`
	Active      bool      `gorm:"column:active;not null;default:true" json:"active"`
	CreatedBy   string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (GovernanceSchema) TableName() string { return "rgx_governance_schema" }

// AppliesTo reports whether the schema's bind covers the given document inside
// a dataset. document bind is exact-document; dataset bind covers all.
func (s *GovernanceSchema) AppliesTo(datasetID, documentID string) bool {
	switch s.BindScope {
	case SchemaBindDocument:
		return s.DatasetID == datasetID && s.DocumentID == documentID
	default:
		return s.DatasetID == datasetID
	}
}
