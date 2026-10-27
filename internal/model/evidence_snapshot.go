package model

import "time"

const (
	EvidenceBindingRetrieval = "retrieval_binding"
	EvidenceBindingManual    = "manual_binding"

	EvidenceStatusFresh       = "fresh"
	EvidenceStatusStale       = "stale"
	EvidenceStatusRevalidated = "revalidated"
)

// EvidenceSnapshot is an immutable dependency and policy contract used to
// replay and invalidate evaluation cases.
type EvidenceSnapshot struct {
	ID                         string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                   string    `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	SnapshotHash               string    `gorm:"column:snapshot_hash;size:64;index;not null" json:"snapshot_hash"`
	DatasetIDs                 string    `gorm:"column:dataset_ids;type:text;not null" json:"dataset_ids"`
	DocumentVersions           string    `gorm:"column:document_versions;type:text;not null" json:"document_versions"`
	RetrievalPolicyVersion     string    `gorm:"column:retrieval_policy_version;size:64;not null" json:"retrieval_policy_version"`
	PromptVersion              string    `gorm:"column:prompt_version;size:64;not null" json:"prompt_version"`
	ModelVersion               string    `gorm:"column:model_version;size:128;not null" json:"model_version"`
	ParserPolicyVersion        string    `gorm:"column:parser_policy_version;size:64;not null" json:"parser_policy_version"`
	ToolPolicyVersion          string    `gorm:"column:tool_policy_version;size:64;not null" json:"tool_policy_version"`
	AuthorizationPolicyVersion string    `gorm:"column:authorization_policy_version;size:64;not null" json:"authorization_policy_version"`
	CreatedAt                  time.Time `gorm:"column:created_at;not null" json:"created_at"`
}

func (EvidenceSnapshot) TableName() string { return "rgx_evidence_snapshot" }

// EvidenceSnapshotDataset is the normalized dataset projection of an immutable
// evidence snapshot. It keeps authorization filtering relational while the
// snapshot itself remains immutable.
type EvidenceSnapshotDataset struct {
	ID         string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID   string `gorm:"column:tenant_id;uniqueIndex:idx_evidence_snapshot_dataset_unique,priority:1;index:idx_evidence_snapshot_dataset_lookup,priority:1;size:32;not null" json:"tenant_id"`
	SnapshotID string `gorm:"column:snapshot_id;uniqueIndex:idx_evidence_snapshot_dataset_unique,priority:2;size:32;not null" json:"snapshot_id"`
	DatasetID  string `gorm:"column:dataset_id;uniqueIndex:idx_evidence_snapshot_dataset_unique,priority:3;index:idx_evidence_snapshot_dataset_lookup,priority:2;size:32;not null" json:"dataset_id"`
}

func (EvidenceSnapshotDataset) TableName() string {
	return "rgx_evidence_snapshot_dataset"
}

// EvalCaseEvidence links one replayable evaluation case to an immutable
// snapshot and tracks its event-driven lifecycle state.
type EvalCaseEvidence struct {
	ID                 string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	EvalCaseID         string     `gorm:"column:eval_case_id;index;size:32;not null" json:"eval_case_id"`
	TenantID           string     `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	EvidenceSnapshotID string     `gorm:"column:evidence_snapshot_id;index;size:32;not null" json:"evidence_snapshot_id"`
	DependencySet      string     `gorm:"column:dependency_set;type:text;not null" json:"dependency_set"`
	StaleStatus        string     `gorm:"column:stale_status;size:32;not null;default:fresh" json:"stale_status"`
	StaleDetectedAt    *time.Time `gorm:"column:stale_detected_at" json:"stale_detected_at"`
	RevalidatedAt      *time.Time `gorm:"column:revalidated_at" json:"revalidated_at"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null" json:"created_at"`
}

func (EvalCaseEvidence) TableName() string { return "rgx_eval_case_evidence" }

// EvalCaseDependency is the normalized lookup projection of a dependency set.
type EvalCaseDependency struct {
	ID                 string `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	EvalCaseEvidenceID string `gorm:"column:eval_case_evidence_id;uniqueIndex:idx_eval_case_dependency_unique;size:32;not null" json:"eval_case_evidence_id"`
	LogicalDocumentID  string `gorm:"column:logical_document_id;uniqueIndex:idx_eval_case_dependency_unique;size:32;not null" json:"logical_document_id"`
	BindingType        string `gorm:"column:binding_type;uniqueIndex:idx_eval_case_dependency_unique;size:32;not null" json:"binding_type"`
}

func (EvalCaseDependency) TableName() string { return "rgx_eval_case_dependency" }
