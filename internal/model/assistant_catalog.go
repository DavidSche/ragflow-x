package model

import "time"

const (
	AssistantKindChat  = "chat"
	AssistantKindAgent = "agent"

	AssistantGovernanceEnabled  = "enabled"
	AssistantGovernanceDisabled = "disabled"
	AssistantGovernanceArchived = "archived"

	AssistantEffectiveActive   = "active"
	AssistantEffectiveInactive = "inactive"

	AssistantStatusReasonActive              = "ACTIVE"
	AssistantStatusReasonGovernanceDisabled  = "GOVERNANCE_DISABLED"
	AssistantStatusReasonGovernanceArchived  = "GOVERNANCE_ARCHIVED"
	AssistantStatusReasonUpstreamArchived    = "UPSTREAM_ARCHIVED"
	AssistantStatusReasonUpstreamDisabled    = "UPSTREAM_DISABLED"
	AssistantStatusReasonUpstreamInvalid     = "UPSTREAM_INVALID"
	AssistantStatusReasonUpstreamDeleted     = "UPSTREAM_DELETED"
	AssistantStatusReasonUpstreamUnavailable = "UPSTREAM_UNAVAILABLE"

	AssistantRiskLow    = "low"
	AssistantRiskMedium = "medium"
	AssistantRiskHigh   = "high"

	RoutingReadinessTypeMetadataCompleteness = "METADATA_COMPLETENESS"
	AgentFlowReadinessThreshold              = 0.80
)

// AssistantCatalog is RAGFlow-X's discovery read model for chat assistants and
// workflow agents. RAGFlow and the ownership shadows remain execution facts.
type AssistantCatalog struct {
	ID                     string  `gorm:"column:id;primaryKey;size:160" json:"id"`
	TenantID               string  `gorm:"column:tenant_id;size:32;not null;index:idx_assistant_catalog_tenant_discoverable" json:"tenant_id"`
	Kind                   string  `gorm:"column:kind;size:16;not null" json:"kind"`
	TargetID               string  `gorm:"column:target_id;size:64;not null" json:"target_id"`
	Name                   string  `gorm:"column:name;size:192;not null" json:"name"`
	Description            string  `gorm:"column:description;type:text" json:"description"`
	UpstreamStatus         string  `gorm:"column:upstream_status;size:32;not null;default:active" json:"upstream_status"`
	GovernanceStatus       string  `gorm:"column:governance_status;size:32;not null;default:enabled" json:"governance_status"`
	EffectiveStatus        string  `gorm:"column:effective_status;size:16;not null;default:active;index:idx_assistant_catalog_tenant_discoverable" json:"effective_status"`
	EffectiveStatusReason  string  `gorm:"column:effective_status_reason;size:48;not null;default:ACTIVE" json:"effective_status_reason"`
	OwnerID                string  `gorm:"column:owner_id;size:32;index" json:"owner_id"`
	CategoriesJSON         string  `gorm:"column:categories_json;type:text" json:"categories_json"`
	CapabilitiesJSON       string  `gorm:"column:capabilities_json;type:text" json:"capabilities_json"`
	IntentsJSON            string  `gorm:"column:intents_json;type:text" json:"intents_json"`
	KeywordsJSON           string  `gorm:"column:keywords_json;type:text" json:"keywords_json"`
	ExamplesJSON           string  `gorm:"column:examples_json;type:text" json:"examples_json"`
	RoutingWeight          float64 `gorm:"column:routing_weight;not null;default:1" json:"routing_weight"`
	AssistantRiskLevel     string  `gorm:"column:assistant_risk_level;size:16;not null;default:low" json:"assistant_risk_level"`
	CapabilityRiskJSON     string  `gorm:"column:capability_risk_json;type:text" json:"capability_risk_json"`
	WorkflowRiskJSON       string  `gorm:"column:workflow_risk_json;type:text" json:"workflow_risk_json"`
	WorkflowRiskUpperBound string  `gorm:"column:workflow_risk_upper_bound;size:16;not null;default:low" json:"workflow_risk_upper_bound"`
	Discoverable           bool    `gorm:"column:discoverable;not null;default:true;index:idx_assistant_catalog_tenant_discoverable" json:"discoverable"`
	AutoSelectEnabled      bool    `gorm:"column:auto_select_enabled;not null;default:false" json:"auto_select_enabled"`
	CatalogVersion         int64   `gorm:"column:catalog_version;not null;default:1" json:"catalog_version"`
	RoutingReadiness       float64 `gorm:"column:routing_readiness;not null;default:0.25" json:"routing_readiness"`
	// AgentFlowReadiness records whether the published workflow passed import,
	// smoke, blocking-node and security-review checks. Chat targets use 1.
	AgentFlowReadiness float64   `gorm:"column:agent_flow_readiness;not null;default:0" json:"agent_flow_readiness"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt          time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (AssistantCatalog) TableName() string { return "rgx_assistant_catalog" }
