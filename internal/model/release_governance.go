package model

import "time"

const (
	CandidateDraft                = "DRAFT"
	CandidateReadyForEvaluation   = "READY_FOR_EVALUATION"
	CandidateEvaluating           = "EVALUATING"
	CandidateEvaluated            = "EVALUATED"
	CandidateGatePending          = "GATE_PENDING"
	CandidateApproved             = "APPROVED"
	CandidateReleased             = "RELEASED"
	CandidateFailed               = "FAILED"
	CandidateRejected             = "REJECTED"
	CandidateCancelled            = "CANCELLED"
	EvaluationRunQueued           = "QUEUED"
	EvaluationRunRunning          = "RUNNING"
	EvaluationRunCompleted        = "COMPLETED"
	EvaluationRunFailed           = "FAILED"
	EvaluationRunCancelled        = "CANCELLED"
	GateDecisionPending           = "PENDING"
	GateDecisionEvaluating        = "EVALUATING"
	GateDecisionPass              = "PASS"
	GateDecisionPassWithWarning   = "PASS_WITH_WARNING"
	GateDecisionBlock             = "BLOCK"
	ReleaseStatusCreated          = "CREATED"
	ReleaseStatusReleasing        = "RELEASING"
	ReleaseStatusReleased         = "RELEASED"
	ReleaseStatusFailed           = "FAILED"
	ReleaseStatusRolledBack       = "ROLLED_BACK"
	QualityIssueOpen              = "OPEN"
	QualityIssueTriaged           = "TRIAGED"
	QualityIssueInProgress        = "IN_PROGRESS"
	QualityIssueFixed             = "FIXED"
	QualityIssueRegressionPending = "REGRESSION_PENDING"
	QualityIssueVerified          = "VERIFIED"
	QualityIssueClosed            = "CLOSED"
	QualityIssueReopened          = "REOPENED"
	HashAlgorithmSHA256           = "SHA256"
	CandidateManifestTarget       = "target"
	CandidateManifestPrompt       = "prompt"
	CandidateManifestKnowledge    = "knowledge"
	CandidateManifestModelRoute   = "modelRoute"
	CandidateManifestPolicy       = "policy"
	CandidateManifestCatalog      = "catalog"
	CandidateManifestRouter       = "router"
	CandidateManifestTools        = "tools"
	CandidateManifestRetrieval    = "retrievalConfig"
	CandidateManifestExecution    = "executionConfig"
)

// ReleaseCandidate is an immutable release change package once it enters
// READY_FOR_EVALUATION. candidate_id identifies lineage and candidate_version
// identifies the immutable package.
type ReleaseCandidate struct {
	ID                string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID          string    `gorm:"column:tenant_id;size:32;not null;uniqueIndex:idx_release_candidate_version;index" json:"tenant_id"`
	CandidateID       string    `gorm:"column:candidate_id;size:32;not null;uniqueIndex:idx_release_candidate_version;index" json:"candidate_id"`
	CandidateVersion  int64     `gorm:"column:candidate_version;not null;uniqueIndex:idx_release_candidate_version" json:"candidate_version"`
	TargetType        string    `gorm:"column:target_type;size:32;not null;index" json:"target_type"`
	TargetID          string    `gorm:"column:target_id;size:64;not null;index" json:"target_id"`
	TargetVersion     string    `gorm:"column:target_version;size:64;not null" json:"target_version"`
	BaseVersion       string    `gorm:"column:base_version;size:64" json:"base_version"`
	ChangeSummary     string    `gorm:"column:change_summary;size:1024" json:"change_summary"`
	CandidateManifest string    `gorm:"column:candidate_manifest;type:text;not null" json:"candidate_manifest"`
	CandidateHash     string    `gorm:"column:candidate_hash;size:64;not null" json:"candidate_hash"`
	HashAlgorithm     string    `gorm:"column:hash_algorithm;size:16;not null" json:"hash_algorithm"`
	Status            string    `gorm:"column:status;size:32;not null;default:DRAFT;index" json:"status"`
	CreatedBy         string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt         time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt         time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (ReleaseCandidate) TableName() string { return "rgx_release_candidate" }

// ExecutionSnapshot captures how a candidate was executed and is immutable.
type ExecutionSnapshot struct {
	ID                             string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                       string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ReleaseCandidateID             string    `gorm:"column:release_candidate_id;size:32;not null;uniqueIndex:idx_execution_snapshot_candidate" json:"release_candidate_id"`
	CandidateVersion               int64     `gorm:"column:candidate_version;not null;uniqueIndex:idx_execution_snapshot_candidate" json:"candidate_version"`
	TargetType                     string    `gorm:"column:target_type;size:32;not null" json:"target_type"`
	TargetID                       string    `gorm:"column:target_id;size:64;not null;index" json:"target_id"`
	TargetVersion                  string    `gorm:"column:target_version;size:64;not null" json:"target_version"`
	SnapshotSchemaVersion          string    `gorm:"column:snapshot_schema_version;size:32;not null" json:"snapshot_schema_version"`
	SnapshotHashAlgorithm          string    `gorm:"column:snapshot_hash_algorithm;size:16;not null" json:"snapshot_hash_algorithm"`
	SnapshotHash                   string    `gorm:"column:snapshot_hash;size:64;not null" json:"snapshot_hash"`
	ExecutionConfig                string    `gorm:"column:execution_config;type:text;not null" json:"execution_config"`
	PromptVersion                  string    `gorm:"column:prompt_version;size:64" json:"prompt_version"`
	ModelRouteVersion              string    `gorm:"column:model_route_version;size:64" json:"model_route_version"`
	ModelRoutePinID                string    `gorm:"column:model_route_pin_id;size:64;index" json:"model_route_pin_id,omitempty"`
	ModelRoutePinVersion           int64     `gorm:"column:model_route_pin_version" json:"model_route_pin_version,omitempty"`
	EnterpriseConnectionID         string    `gorm:"column:enterprise_connection_id;size:64;index" json:"enterprise_connection_id,omitempty"`
	EnterpriseConnectionVersion    int64     `gorm:"column:enterprise_connection_version" json:"enterprise_connection_version,omitempty"`
	EnterpriseConnectionConfigHash string    `gorm:"column:enterprise_connection_config_hash;size:80" json:"enterprise_connection_config_hash,omitempty"`
	EnterpriseBindingID            string    `gorm:"column:enterprise_binding_id;size:64;index" json:"enterprise_binding_id,omitempty"`
	EnterpriseBindingVersion       int64     `gorm:"column:enterprise_binding_version" json:"enterprise_binding_version,omitempty"`
	EnterpriseModelRef             string    `gorm:"column:enterprise_model_ref;size:128" json:"enterprise_model_ref,omitempty"`
	CredentialVersion              string    `gorm:"column:credential_version;size:64" json:"credential_version,omitempty"`
	KnowledgeVersion               string    `gorm:"column:knowledge_version;size:64" json:"knowledge_version"`
	RetrievalConfigVersion         string    `gorm:"column:retrieval_config_version;size:64" json:"retrieval_config_version"`
	CatalogVersion                 string    `gorm:"column:catalog_version;size:64" json:"catalog_version"`
	PolicyVersion                  string    `gorm:"column:policy_version;size:64" json:"policy_version"`
	RouterVersion                  string    `gorm:"column:router_version;size:64" json:"router_version"`
	ToolRegistryVersion            string    `gorm:"column:tool_registry_version;size:64" json:"tool_registry_version"`
	ToolSetHash                    string    `gorm:"column:tool_set_hash;size:64" json:"tool_set_hash"`
	CreatedBy                      string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt                      time.Time `gorm:"column:created_at" json:"created_at"`
}

func (ExecutionSnapshot) TableName() string { return "rgx_execution_snapshot" }

// EvaluationSetVersion is an immutable eval suite identity.
type EvaluationSetVersion struct {
	ID                string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID          string    `gorm:"column:tenant_id;size:32;not null;uniqueIndex:idx_evaluation_set_version" json:"tenant_id"`
	EvalSetID         string    `gorm:"column:eval_set_id;size:32;not null;uniqueIndex:idx_evaluation_set_version;index" json:"eval_set_id"`
	Version           int64     `gorm:"column:version;not null;uniqueIndex:idx_evaluation_set_version" json:"version"`
	Hash              string    `gorm:"column:hash;size:64;not null" json:"hash"`
	CasesSnapshotHash string    `gorm:"column:cases_snapshot_hash;size:64;not null" json:"cases_snapshot_hash"`
	Status            string    `gorm:"column:status;size:16;not null;default:published" json:"status"`
	ChangeSummary     string    `gorm:"column:change_summary;size:1024" json:"change_summary"`
	CreatedBy         string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt         time.Time `gorm:"column:created_at" json:"created_at"`
}

func (EvaluationSetVersion) TableName() string { return "rgx_evaluation_set_version" }

// EvaluationCaseVersion is the immutable case snapshot used by one run.
type EvaluationCaseVersion struct {
	ID                     string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID               string    `gorm:"column:tenant_id;size:32;not null;uniqueIndex:idx_evaluation_case_version" json:"tenant_id"`
	EvalSetID              string    `gorm:"column:eval_set_id;size:32;not null;uniqueIndex:idx_evaluation_case_version;index" json:"eval_set_id"`
	EvalSetVersion         int64     `gorm:"column:eval_set_version;not null;uniqueIndex:idx_evaluation_case_version;index" json:"eval_set_version"`
	CaseID                 string    `gorm:"column:case_id;size:32;not null;uniqueIndex:idx_evaluation_case_version;index" json:"case_id"`
	CaseVersion            int64     `gorm:"column:case_version;not null;uniqueIndex:idx_evaluation_case_version" json:"case_version"`
	Hash                   string    `gorm:"column:hash;size:64;not null" json:"hash"`
	Question               string    `gorm:"column:question;type:text;not null" json:"question"`
	ExpectedAnswer         string    `gorm:"column:expected_answer;type:text" json:"expected_answer"`
	ExpectedKeywords       string    `gorm:"column:expected_keywords;size:512" json:"expected_keywords"`
	ExpectedCitationDocIDs string    `gorm:"column:expected_citation_doc_ids;size:512" json:"expected_citation_doc_ids"`
	Metadata               string    `gorm:"column:metadata;type:text" json:"metadata"`
	CreatedBy              string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt              time.Time `gorm:"column:created_at" json:"created_at"`
}

func (EvaluationCaseVersion) TableName() string { return "rgx_evaluation_case_version" }

// EvaluationRun is an immutable aggregate once terminal. FAILED means the run
// could not produce a trustworthy result, so Pass is null.
type EvaluationRun struct {
	ID                       string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                 string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ReleaseCandidateID       string     `gorm:"column:release_candidate_id;size:32;not null;index" json:"release_candidate_id"`
	CandidateVersion         int64      `gorm:"column:candidate_version;not null;index" json:"candidate_version"`
	EvalSetID                string     `gorm:"column:eval_set_id;size:32;not null" json:"eval_set_id"`
	EvalSetVersion           int64      `gorm:"column:eval_set_version;not null" json:"eval_set_version"`
	EvalSetHash              string     `gorm:"column:eval_set_hash;size:64;not null" json:"eval_set_hash"`
	EvaluationPolicyVersion  string     `gorm:"column:evaluation_policy_version;size:64;not null" json:"evaluation_policy_version"`
	EvaluationPolicyHash     string     `gorm:"column:evaluation_policy_hash;size:64;not null" json:"evaluation_policy_hash"`
	AggregationPolicyVersion string     `gorm:"column:aggregation_policy_version;size:64;not null" json:"aggregation_policy_version"`
	AggregationPolicyHash    string     `gorm:"column:aggregation_policy_hash;size:64;not null" json:"aggregation_policy_hash"`
	ExecutionSnapshotID      string     `gorm:"column:execution_snapshot_id;size:32;not null" json:"execution_snapshot_id"`
	Status                   string     `gorm:"column:status;size:16;not null;default:QUEUED;index" json:"status"`
	Metrics                  string     `gorm:"column:metrics;type:text" json:"metrics"`
	Pass                     *bool      `gorm:"column:pass" json:"pass"`
	Actor                    string     `gorm:"column:actor;size:32;not null" json:"actor"`
	TraceID                  string     `gorm:"column:trace_id;size:64" json:"trace_id"`
	StartedAt                *time.Time `gorm:"column:started_at" json:"started_at"`
	CompletedAt              *time.Time `gorm:"column:completed_at" json:"completed_at"`
	CreatedAt                time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt                time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (EvaluationRun) TableName() string { return "rgx_evaluation_run" }

// EvaluationCaseResult becomes immutable when its run reaches terminal state.
type EvaluationCaseResult struct {
	ID              string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID        string    `gorm:"column:tenant_id;size:32;not null;uniqueIndex:idx_evaluation_case_result_run_case;index" json:"tenant_id"`
	RunID           string    `gorm:"column:run_id;size:32;not null;uniqueIndex:idx_evaluation_case_result_run_case;index" json:"run_id"`
	CaseID          string    `gorm:"column:case_id;size:32;not null;uniqueIndex:idx_evaluation_case_result_run_case" json:"case_id"`
	CaseVersionID   string    `gorm:"column:case_version_id;size:32;not null;index" json:"case_version_id"`
	CaseVersionHash string    `gorm:"column:case_version_hash;size:64;not null" json:"case_version_hash"`
	ActualAnswer    string    `gorm:"column:actual_answer;type:text" json:"actual_answer"`
	References      string    `gorm:"column:references;type:text" json:"references"`
	Metrics         string    `gorm:"column:metrics;type:text" json:"metrics"`
	Pass            *bool     `gorm:"column:pass" json:"pass"`
	FailureReason   string    `gorm:"column:failure_reason;type:text" json:"failure_reason"`
	LatencyMs       int64     `gorm:"column:latency_ms;not null;default:0" json:"latency_ms"`
	Tokens          int64     `gorm:"column:tokens;not null;default:0" json:"tokens"`
	CreatedAt       time.Time `gorm:"column:created_at" json:"created_at"`
}

func (EvaluationCaseResult) TableName() string { return "rgx_evaluation_case_result" }

// EvidenceBundle is an immutable evidence card generated at Gate time.
type EvidenceBundle struct {
	ID                             string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                       string    `gorm:"column:tenant_id;size:32;not null;uniqueIndex:idx_evidence_bundle_candidate;index" json:"tenant_id"`
	ReleaseCandidateID             string    `gorm:"column:release_candidate_id;size:32;not null;uniqueIndex:idx_evidence_bundle_candidate" json:"release_candidate_id"`
	CandidateVersion               int64     `gorm:"column:candidate_version;not null;uniqueIndex:idx_evidence_bundle_candidate" json:"candidate_version"`
	SnapshotID                     string    `gorm:"column:snapshot_id;size:32;not null" json:"snapshot_id"`
	SnapshotHash                   string    `gorm:"column:snapshot_hash;size:64;not null" json:"snapshot_hash"`
	ModelRouteVersion              string    `gorm:"column:model_route_version;size:64" json:"model_route_version"`
	ModelRoutePinID                string    `gorm:"column:model_route_pin_id;size:64;index" json:"model_route_pin_id,omitempty"`
	ModelRoutePinVersion           int64     `gorm:"column:model_route_pin_version" json:"model_route_pin_version,omitempty"`
	EnterpriseConnectionID         string    `gorm:"column:enterprise_connection_id;size:64;index" json:"enterprise_connection_id,omitempty"`
	EnterpriseConnectionVersion    int64     `gorm:"column:enterprise_connection_version" json:"enterprise_connection_version,omitempty"`
	EnterpriseConnectionConfigHash string    `gorm:"column:enterprise_connection_config_hash;size:80" json:"enterprise_connection_config_hash,omitempty"`
	EnterpriseBindingID            string    `gorm:"column:enterprise_binding_id;size:64;index" json:"enterprise_binding_id,omitempty"`
	EnterpriseBindingVersion       int64     `gorm:"column:enterprise_binding_version" json:"enterprise_binding_version,omitempty"`
	EnterpriseModelRef             string    `gorm:"column:enterprise_model_ref;size:128" json:"enterprise_model_ref,omitempty"`
	CredentialVersion              string    `gorm:"column:credential_version;size:64" json:"credential_version,omitempty"`
	EvaluationRunID                string    `gorm:"column:evaluation_run_id;size:32;not null" json:"evaluation_run_id"`
	EvalSetID                      string    `gorm:"column:eval_set_id;size:32;not null" json:"eval_set_id"`
	EvalSetVersion                 int64     `gorm:"column:eval_set_version;not null" json:"eval_set_version"`
	EvalSetHash                    string    `gorm:"column:eval_set_hash;size:64;not null" json:"eval_set_hash"`
	EvaluationPolicyVersion        string    `gorm:"column:evaluation_policy_version;size:64;not null" json:"evaluation_policy_version"`
	EvaluationPolicyHash           string    `gorm:"column:evaluation_policy_hash;size:64;not null" json:"evaluation_policy_hash"`
	AggregationPolicyVersion       string    `gorm:"column:aggregation_policy_version;size:64;not null" json:"aggregation_policy_version"`
	AggregationPolicyHash          string    `gorm:"column:aggregation_policy_hash;size:64;not null" json:"aggregation_policy_hash"`
	SecurityEvidence               string    `gorm:"column:security_evidence;type:text" json:"security_evidence"`
	PolicyEvidence                 string    `gorm:"column:policy_evidence;type:text" json:"policy_evidence"`
	RiskEvidence                   string    `gorm:"column:risk_evidence;type:text" json:"risk_evidence"`
	PermissionEvidence             string    `gorm:"column:permission_evidence;type:text" json:"permission_evidence"`
	ConfigurationEvidence          string    `gorm:"column:configuration_evidence;type:text" json:"configuration_evidence"`
	ApprovalEvidence               string    `gorm:"column:approval_evidence;type:text" json:"approval_evidence"`
	EvidenceItems                  string    `gorm:"column:evidence_items;type:text" json:"evidence_items"`
	HashAlgorithm                  string    `gorm:"column:hash_algorithm;size:16;not null" json:"hash_algorithm"`
	BundleHash                     string    `gorm:"column:bundle_hash;size:64;not null" json:"bundle_hash"`
	CreatedBy                      string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt                      time.Time `gorm:"column:created_at" json:"created_at"`
}

func (EvidenceBundle) TableName() string { return "rgx_evidence_bundle" }

// ReleaseGateDecision is a historical decision. gate_decision_id (ID) is the PK;
// candidate binding is a target reference, not the PK.
type ReleaseGateDecision struct {
	ID                       string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                 string    `gorm:"column:tenant_id;size:32;not null;uniqueIndex:idx_gate_decision_version;index" json:"tenant_id"`
	ReleaseCandidateID       string    `gorm:"column:release_candidate_id;size:32;not null;uniqueIndex:idx_gate_decision_version;index" json:"release_candidate_id"`
	CandidateVersion         int64     `gorm:"column:candidate_version;not null;uniqueIndex:idx_gate_decision_version" json:"candidate_version"`
	DecisionVersion          int64     `gorm:"column:decision_version;not null;uniqueIndex:idx_gate_decision_version" json:"decision_version"`
	EvidenceBundleID         string    `gorm:"column:evidence_bundle_id;size:32;not null;index" json:"evidence_bundle_id"`
	Environment              string    `gorm:"column:environment;size:32;not null;index" json:"environment"`
	EnvironmentPolicyVersion string    `gorm:"column:environment_policy_version;size:64;not null" json:"environment_policy_version"`
	EnvironmentPolicyHash    string    `gorm:"column:environment_policy_hash;size:64;not null" json:"environment_policy_hash"`
	SubGateStates            string    `gorm:"column:sub_gate_states;type:text;not null" json:"sub_gate_states"`
	Decision                 string    `gorm:"column:decision;size:32;not null;index" json:"decision"`
	Reason                   string    `gorm:"column:reason;type:text" json:"reason"`
	Waiver                   string    `gorm:"column:waiver;type:text" json:"waiver"`
	ActiveGate               bool      `gorm:"column:active_gate;not null;default:false;index" json:"active_gate"`
	Actor                    string    `gorm:"column:actor;size:32;not null" json:"actor"`
	ApprovalID               string    `gorm:"column:approval_id;size:32" json:"approval_id"`
	CreatedAt                time.Time `gorm:"column:created_at" json:"created_at"`
}

func (ReleaseGateDecision) TableName() string { return "rgx_release_gate_decision" }

// Release binds a candidate to one immutable gate decision and snapshot.
type Release struct {
	ID                             string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                       string     `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ReleaseCandidateID             string     `gorm:"column:release_candidate_id;size:32;not null;index" json:"release_candidate_id"`
	CandidateVersion               int64      `gorm:"column:candidate_version;not null;index" json:"candidate_version"`
	SnapshotID                     string     `gorm:"column:snapshot_id;size:32;not null" json:"snapshot_id"`
	GateDecisionID                 string     `gorm:"column:gate_decision_id;size:32;not null;uniqueIndex" json:"gate_decision_id"`
	ModelRouteVersion              string     `gorm:"column:model_route_version;size:64" json:"model_route_version"`
	ModelRoutePinID                string     `gorm:"column:model_route_pin_id;size:64;index" json:"model_route_pin_id,omitempty"`
	ModelRoutePinVersion           int64      `gorm:"column:model_route_pin_version" json:"model_route_pin_version,omitempty"`
	EnterpriseConnectionID         string     `gorm:"column:enterprise_connection_id;size:64;index" json:"enterprise_connection_id,omitempty"`
	EnterpriseConnectionVersion    int64      `gorm:"column:enterprise_connection_version" json:"enterprise_connection_version,omitempty"`
	EnterpriseConnectionConfigHash string     `gorm:"column:enterprise_connection_config_hash;size:80" json:"enterprise_connection_config_hash,omitempty"`
	EnterpriseBindingID            string     `gorm:"column:enterprise_binding_id;size:64;index" json:"enterprise_binding_id,omitempty"`
	EnterpriseBindingVersion       int64      `gorm:"column:enterprise_binding_version" json:"enterprise_binding_version,omitempty"`
	EnterpriseModelRef             string     `gorm:"column:enterprise_model_ref;size:128" json:"enterprise_model_ref,omitempty"`
	CredentialVersion              string     `gorm:"column:credential_version;size:64" json:"credential_version,omitempty"`
	Environment                    string     `gorm:"column:environment;size:32;not null" json:"environment"`
	Status                         string     `gorm:"column:status;size:16;not null;default:CREATED;index" json:"status"`
	ReleasedBy                     string     `gorm:"column:released_by;size:32;not null" json:"released_by"`
	ReleasedAt                     *time.Time `gorm:"column:released_at" json:"released_at"`
	RollbackBaseline               string     `gorm:"column:rollback_baseline;size:64" json:"rollback_baseline"`
	CreatedAt                      time.Time  `gorm:"column:created_at" json:"created_at"`
	UpdatedAt                      time.Time  `gorm:"column:updated_at" json:"updated_at"`
}

func (Release) TableName() string { return "rgx_release" }

// QualityIssue is the closed-loop owner of bad cases and their resolution.
type QualityIssue struct {
	ID                      string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID                string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Source                  string    `gorm:"column:source;size:32;not null" json:"source"`
	SourceID                string    `gorm:"column:source_id;size:64" json:"source_id"`
	Title                   string    `gorm:"column:title;size:256;not null" json:"title"`
	Evidence                string    `gorm:"column:evidence;type:text" json:"evidence"`
	Owner                   string    `gorm:"column:owner;size:32" json:"owner"`
	Status                  string    `gorm:"column:status;size:32;not null;default:OPEN;index" json:"status"`
	Resolution              string    `gorm:"column:resolution;type:text" json:"resolution"`
	ResolutionTargetType    string    `gorm:"column:resolution_target_type;size:32" json:"resolution_target_type"`
	ResolutionTargetID      string    `gorm:"column:resolution_target_id;size:64" json:"resolution_target_id"`
	ResolutionTargetVersion string    `gorm:"column:resolution_target_version;size:64" json:"resolution_target_version"`
	EvalCaseID              string    `gorm:"column:eval_case_id;size:32" json:"eval_case_id"`
	EvaluationRunID         string    `gorm:"column:evaluation_run_id;size:32" json:"evaluation_run_id"`
	ReleaseID               string    `gorm:"column:release_id;size:32" json:"release_id"`
	ReopenCount             int64     `gorm:"column:reopen_count;not null;default:0" json:"reopen_count"`
	ReopenReason            string    `gorm:"column:reopen_reason;type:text" json:"reopen_reason"`
	PreviousResolution      string    `gorm:"column:previous_resolution;type:text" json:"previous_resolution"`
	PreviousRunID           string    `gorm:"column:previous_run_id;size:32" json:"previous_run_id"`
	CreatedBy               string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt               time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt               time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (QualityIssue) TableName() string { return "rgx_quality_issue" }
