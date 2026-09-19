package model

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	ApprovalObjectDataset              = "dataset"
	ApprovalObjectDocument             = "document"
	ApprovalObjectDocumentChunk        = "document-chunk"
	ApprovalObjectAPIKey               = "api-key"
	ApprovalObjectChat                 = "chat"
	ApprovalObjectAgent                = "agent"
	ApprovalObjectModelProvider        = "model-provider"
	ApprovalObjectModelInstance        = "model-instance"
	ApprovalObjectModelModel           = "model-model"
	ApprovalObjectEnterpriseConnection = "enterprise-connection"
	ApprovalObjectEnterpriseBinding    = "enterprise-binding"

	ApprovalActionCreate           = "create"
	ApprovalActionUpdate           = "update"
	ApprovalActionDelete           = "delete"
	ApprovalActionRevoke           = "revoke"
	ApprovalActionBind             = "bind"
	ApprovalActionRotateCredential = "rotate-credential"
	ApprovalActionDeprecate        = "deprecate"
	ApprovalActionRetire           = "retire"
	ApprovalActionTest             = "test"
	ApprovalActionParse            = "parse"
	ApprovalActionStop             = "stop"
	ApprovalActionEnable           = "enable"
	ApprovalActionDisable          = "disable"

	ApprovalStatusPendingApproval = "pending_approval"
	ApprovalStatusApproved        = "approved"
	ApprovalStatusRejected        = "rejected"
	ApprovalStatusCanceled        = "canceled"
	ApprovalStatusExpired         = "expired"
	ApprovalStatusExecuting       = "executing"
	ApprovalStatusCompleted       = "completed"
	ApprovalStatusExecutionFailed = "execution_failed"

	ApprovalStepPending  = "pending"
	ApprovalStepCurrent  = "current"
	ApprovalStepApproved = "approved"
	ApprovalStepRejected = "rejected"
	ApprovalStepSkipped  = "skipped"
	ApprovalStepExpired  = "expired"

	ApprovalApproverRole = "role"
	ApprovalApproverUser = "user"
	ApprovalApproverTeam = "team"

	ApprovalModeAny         = "any"
	ApprovalModeAll         = "all"
	ApprovalDecisionApprove = "approve"
	ApprovalDecisionReject  = "reject"
)

const ApprovalExecutionJobKeyPrefix = "approval_execute:"

type ApprovalStepSpec struct {
	StepNo            int                    `json:"step_no"`
	Name              string                 `json:"name"`
	ApproverType      string                 `json:"approver_type"`
	ApproverValue     string                 `json:"approver_value"`
	ApprovalMode      string                 `json:"approval_mode,omitempty"`
	Approvers         []ApprovalApproverSpec `json:"approvers,omitempty"`
	RequiredApprovals int                    `json:"required_approvals,omitempty"`
	ExpireHours       int                    `json:"expire_hours"`
}

type ApprovalApproverSpec struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type ApprovalCondition struct {
	Field string          `json:"field"`
	Op    string          `json:"op"`
	Value json.RawMessage `json:"value,omitempty"`
}

type ApprovalConditions struct {
	All []ApprovalCondition `json:"all"`
}

type ApprovalPolicy struct {
	ID             string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID       string    `gorm:"column:tenant_id;size:32;not null;default:''" json:"tenant_id"`
	ObjectType     string    `gorm:"column:object_type;size:64;not null" json:"object_type"`
	Action         string    `gorm:"column:action;size:64;not null" json:"action"`
	Enabled        bool      `gorm:"column:enabled;not null;default:false" json:"enabled"`
	Priority       int       `gorm:"column:priority;not null;default:100" json:"priority"`
	ConditionsJSON string    `gorm:"column:conditions_json;type:text;not null;default:'{}'" json:"conditions_json"`
	StepsJSON      string    `gorm:"column:steps_json;type:text;not null" json:"steps_json"`
	ExpireHours    int       `gorm:"column:expire_hours;not null;default:72" json:"expire_hours"`
	Version        int64     `gorm:"column:version;not null;default:1" json:"version"`
	CreatedBy      string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt      time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (ApprovalPolicy) TableName() string { return "rgx_approval_policy" }

type Approval struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	RequestNo          string    `gorm:"column:request_no;size:64;not null" json:"request_no"`
	ObjectType         string    `gorm:"column:object_type;size:64;not null" json:"object_type"`
	ObjectID           string    `gorm:"column:object_id;size:64;not null" json:"object_id"`
	Action             string    `gorm:"column:action;size:64;not null" json:"action"`
	Title              string    `gorm:"column:title;size:255;not null" json:"title"`
	Reason             string    `gorm:"column:reason;type:text" json:"reason"`
	PayloadJSON        string    `gorm:"column:payload_json;type:text;not null;default:'{}'" json:"payload_json"`
	SnapshotJSON       string    `gorm:"column:snapshot_json;type:text;not null;default:'{}'" json:"snapshot_json"`
	TargetTenantID     string    `gorm:"column:target_tenant_id;size:32;not null;default:'';index" json:"target_tenant_id"`
	ResourceVersion    string    `gorm:"column:resource_version;size:128;not null;default:''" json:"resource_version"`
	ApprovalActionHash string    `gorm:"column:approval_action_hash;size:80;not null;default:'';index" json:"approval_action_hash"`
	Status             string    `gorm:"column:status;size:32;not null;default:pending_approval;index" json:"status"`
	PolicyID           string    `gorm:"column:policy_id;size:32;not null" json:"policy_id"`
	PolicyVersion      int64     `gorm:"column:policy_version;not null;default:1" json:"policy_version"`
	CurrentStep        int       `gorm:"column:current_step;not null;default:1" json:"current_step"`
	RequesterID        string    `gorm:"column:requester_id;size:32;not null;index" json:"requester_id"`
	IdempotencyKey     string    `gorm:"column:idempotency_key;size:128;not null" json:"idempotency_key"`
	ExpiresAt          time.Time `gorm:"column:expires_at;not null" json:"expires_at"`
	SubmittedAt        time.Time `gorm:"column:submitted_at" json:"submitted_at"`
	DecidedAt          time.Time `gorm:"column:decided_at" json:"decided_at"`
	ExecutedAt         time.Time `gorm:"column:executed_at" json:"executed_at"`
	ResultJSON         string    `gorm:"column:result_json;type:text;not null;default:'{}'" json:"result_json"`
	LastError          string    `gorm:"column:last_error;type:text" json:"last_error"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt          time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (Approval) TableName() string { return "rgx_approval" }

type ApprovalStep struct {
	ID                string              `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID          string              `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ApprovalID        string              `gorm:"column:approval_id;size:32;not null;index" json:"approval_id"`
	StepNo            int                 `gorm:"column:step_no;not null" json:"step_no"`
	Name              string              `gorm:"column:name;size:128;not null" json:"name"`
	ApproverType      string              `gorm:"column:approver_type;size:32;not null" json:"approver_type"`
	ApproverValue     string              `gorm:"column:approver_value;size:128;not null" json:"approver_value"`
	ApprovalMode      string              `gorm:"column:approval_mode;size:16;not null;default:any" json:"approval_mode"`
	ApproversJSON     string              `gorm:"column:approvers_json;type:text;not null;default:'[]'" json:"approvers_json"`
	RequiredApprovals int                 `gorm:"column:required_approvals;not null;default:1" json:"required_approvals"`
	Status            string              `gorm:"column:status;size:32;not null;default:pending" json:"status"`
	ActedBy           *string             `gorm:"column:acted_by;size:32" json:"acted_by"`
	Decision          string              `gorm:"column:decision;size:16" json:"decision"`
	Comment           string              `gorm:"column:comment;type:text" json:"comment"`
	DueAt             *time.Time          `gorm:"column:due_at" json:"due_at"`
	ActedAt           *time.Time          `gorm:"column:acted_at" json:"acted_at"`
	CreatedAt         time.Time           `gorm:"column:created_at" json:"created_at"`
	UpdatedAt         time.Time           `gorm:"column:updated_at" json:"updated_at"`
	Actors            []ApprovalStepActor `gorm:"-" json:"actors,omitempty"`
}

func (ApprovalStep) TableName() string { return "rgx_approval_step" }

type ApprovalStepActor struct {
	ID          string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID    string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ApprovalID  string    `gorm:"column:approval_id;size:32;not null;index" json:"approval_id"`
	StepID      string    `gorm:"column:step_id;size:32;not null;uniqueIndex:uk_approval_step_actor" json:"step_id"`
	ActorID     string    `gorm:"column:actor_id;size:32;not null;uniqueIndex:uk_approval_step_actor" json:"actor_id"`
	DelegatedBy string    `gorm:"column:delegated_by;size:32" json:"delegated_by"`
	Decision    string    `gorm:"column:decision;size:16;not null" json:"decision"`
	Comment     string    `gorm:"column:comment;type:text" json:"comment"`
	ActedAt     time.Time `gorm:"column:acted_at" json:"acted_at"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
}

func (ApprovalStepActor) TableName() string { return "rgx_approval_step_actor" }

type ApprovalDelegation struct {
	ID          string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID    string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	PrincipalID string    `gorm:"column:principal_id;size:32;not null;index" json:"principal_id"`
	DelegateID  string    `gorm:"column:delegate_id;size:32;not null;index" json:"delegate_id"`
	ObjectType  string    `gorm:"column:object_type;size:64" json:"object_type"`
	Action      string    `gorm:"column:action;size:64" json:"action"`
	StartsAt    time.Time `gorm:"column:starts_at" json:"starts_at"`
	EndsAt      time.Time `gorm:"column:ends_at" json:"ends_at"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (ApprovalDelegation) TableName() string { return "rgx_approval_delegation" }

type ApprovalCredential struct {
	ID            string              `gorm:"column:id;primaryKey;size:32" json:"id"`
	ApprovalID    string              `gorm:"column:approval_id;size:32;not null;index" json:"approval_id"`
	TenantID      string              `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Name          string              `gorm:"column:name;size:128;not null" json:"name"`
	Ciphertext    string              `gorm:"column:ciphertext;type:text;not null" json:"-"`
	ConsumedAt    *time.Time          `gorm:"column:consumed_at" json:"-"`
	RetainUntilAt *time.Time          `gorm:"column:retain_until_at" json:"-"`
	CreatedAt     time.Time           `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     time.Time           `gorm:"column:updated_at" json:"updated_at"`
	Actors        []ApprovalStepActor `gorm:"-" json:"actors,omitempty"`
}

func (ApprovalCredential) TableName() string { return "rgx_approval_credential" }

const (
	ActingContextActive   = "ACTIVE"
	ActingContextClaimed  = "CLAIMED"
	ActingContextConsumed = "CONSUMED"
	ActingContextFailed   = "FAILED"
	ActingContextExpired  = "EXPIRED"
	ActingContextRevoked  = "REVOKED"
)

// ActingContext is a short-lived server-issued execution authorization. The
// database row, not any queue payload, is the authoritative security object.
type ActingContext struct {
	ID                 string     `gorm:"column:id;primaryKey;size:64" json:"id"`
	ActorID            string     `gorm:"column:actor_id;size:32;not null;index" json:"actor_id"`
	ActorTenantID      string     `gorm:"column:actor_tenant_id;size:32;not null;index" json:"actor_tenant_id"`
	ActorRole          string     `gorm:"column:actor_role;size:64;not null" json:"actor_role"`
	ActingMode         string     `gorm:"column:acting_mode;size:32;not null" json:"acting_mode"`
	TargetTenantID     string     `gorm:"column:target_tenant_id;size:32;not null;index" json:"target_tenant_id"`
	ResourceType       string     `gorm:"column:resource_type;size:64;not null;index" json:"resource_type"`
	ResourceID         string     `gorm:"column:resource_id;size:128;not null" json:"resource_id"`
	ResourceVersion    string     `gorm:"column:resource_version;size:128;not null" json:"resource_version"`
	Action             string     `gorm:"column:action;size:64;not null;index" json:"action"`
	ApprovalID         string     `gorm:"column:approval_id;size:32;not null;uniqueIndex:uk_acting_context_approval_attempt" json:"approval_id"`
	AttemptNo          int64      `gorm:"column:attempt_no;not null;default:1;uniqueIndex:uk_acting_context_approval_attempt" json:"attempt_no"`
	ApprovalActionHash string     `gorm:"column:approval_action_hash;size:80;not null" json:"approval_action_hash"`
	IdempotencyKey     string     `gorm:"column:idempotency_key;size:128;not null" json:"idempotency_key"`
	RequestID          string     `gorm:"column:request_id;size:128;not null" json:"request_id"`
	RiskLevel          string     `gorm:"column:risk_level;size:16;not null;default:high" json:"risk_level"`
	Reason             string     `gorm:"column:reason;size:255;not null;default:''" json:"reason"`
	IssuedAt           time.Time  `gorm:"column:issued_at;not null" json:"issued_at"`
	ClaimedAt          *time.Time `gorm:"column:claimed_at" json:"claimed_at,omitempty"`
	ClaimedBy          string     `gorm:"column:claimed_by;size:128;not null;default:''" json:"claimed_by"`
	ClaimExpiresAt     *time.Time `gorm:"column:claim_expires_at" json:"claim_expires_at,omitempty"`
	ExpiresAt          time.Time  `gorm:"column:expires_at;not null;index" json:"expires_at"`
	Status             string     `gorm:"column:status;size:16;not null;default:ACTIVE;index" json:"status"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (ActingContext) TableName() string { return "rgx_acting_context" }

// ApprovalStepApprovers returns the approver list for a step, parsing ApproversJSON
// or falling back to the legacy single-approver fields.
func ApprovalStepApprovers(step *ApprovalStep) ([]ApprovalApproverSpec, error) {
	var approvers []ApprovalApproverSpec
	if strings.TrimSpace(step.ApproversJSON) != "" {
		if err := json.Unmarshal([]byte(step.ApproversJSON), &approvers); err != nil {
			return nil, err
		}
	}
	if len(approvers) == 0 {
		approvers = []ApprovalApproverSpec{{Type: step.ApproverType, Value: step.ApproverValue}}
	}
	return approvers, nil
}

// ApprovalStepRequiredApprovals returns the number of approvals needed to complete
// the given step. For ApprovalModeAll it uses RequiredApprovals or the approver count;
// for ApprovalModeAny it always returns 1.
func ApprovalStepRequiredApprovals(step *ApprovalStep) int {
	if step.ApprovalMode == ApprovalModeAll {
		approvers, err := ApprovalStepApprovers(step)
		if err != nil || len(approvers) == 0 {
			return 1
		}
		if step.RequiredApprovals > 0 && step.RequiredApprovals <= len(approvers) {
			return step.RequiredApprovals
		}
		return len(approvers)
	}
	return 1
}
