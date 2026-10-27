package model

import "time"

// Evaluation protocol registration lifecycle values.
const (
	// EvaluationProtocolRegistrationActive marks a registration that gates
	// scoped evaluation execution.
	EvaluationProtocolRegistrationActive = "active"
	// EvaluationProtocolRegistrationRevoked marks a registration that no
	// longer gates execution. Revocations are permanent; a new registration
	// row must be created to gate the same protocol again.
	EvaluationProtocolRegistrationRevoked = "revoked"
)

// EvaluationProtocolRegistration is the pre-registered governance record for
// a scoped paired-A/B evaluation protocol (137 §11.3 / §14): the protocol
// snapshot, its canonical hash, and optionally the evaluation set identity it
// was preregistered against are frozen before any run executes. Scoped
// evaluation runs that carry a Gate-bearing protocol (contract v2/v3) must
// match an active registration, otherwise execution fails closed.
type EvaluationProtocolRegistration struct {
	ID               string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID         string     `gorm:"column:tenant_id;size:32;not null;index:idx_eval_protocol_registration_unique" json:"tenant_id"`
	Capability       string     `gorm:"column:capability;size:32;not null;index:idx_eval_protocol_registration_unique" json:"capability"`
	ContractVersion  string     `gorm:"column:contract_version;size:64;not null;index:idx_eval_protocol_registration_unique" json:"contract_version"`
	ProtocolHash     string     `gorm:"column:protocol_hash;size:64;not null;index:idx_eval_protocol_registration_unique" json:"protocol_hash"`
	ProtocolSnapshot string     `gorm:"column:protocol_snapshot;type:text;not null" json:"protocol_snapshot"`
	EvalSetID        string     `gorm:"column:eval_set_id;size:32;not null;default:''" json:"eval_set_id"`
	SampleSetHash    string     `gorm:"column:sample_set_hash;size:64;not null;default:''" json:"sample_set_hash"`
	Status           string     `gorm:"column:status;size:16;not null;default:active;index" json:"status"`
	RegisteredBy     string     `gorm:"column:registered_by;size:32;not null" json:"registered_by"`
	RevokedBy        string     `gorm:"column:revoked_by;size:32;not null;default:''" json:"revoked_by"`
	RevokeReason     string     `gorm:"column:revoke_reason;size:256;not null;default:''" json:"revoke_reason"`
	Note             string     `gorm:"column:note;size:512;not null;default:''" json:"note"`
	CreatedAt        time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt        time.Time  `gorm:"column:updated_at" json:"updated_at"`
	RevokedAt        *time.Time `gorm:"column:revoked_at" json:"revoked_at,omitempty"`
}

func (EvaluationProtocolRegistration) TableName() string {
	return "rgx_evaluation_protocol_registration"
}
