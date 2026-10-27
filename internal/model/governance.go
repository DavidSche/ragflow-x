package model

import "time"

// ScenarioTemplateAsset is the tenant-owned reusable app template.
type ScenarioTemplateAsset struct {
	ID            string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID      string    `gorm:"column:tenant_id;size:32;not null;index:uk_scenario_template_key,unique" json:"tenant_id"`
	Key           string    `gorm:"column:key;size:96;not null;index:uk_scenario_template_key,unique" json:"key"`
	Name          string    `gorm:"column:name;size:128;not null" json:"name"`
	AppTypes      string    `gorm:"column:app_types;size:128" json:"app_types"`
	Description   string    `gorm:"column:description;size:512" json:"description"`
	LatestVersion int64     `gorm:"column:latest_version;not null;default:1" json:"latest_version"`
	Source        string    `gorm:"column:source;size:16;not null;default:custom" json:"source"`
	Status        string    `gorm:"column:status;size:16;not null;default:draft;index" json:"status"`
	PayloadJSON   string    `gorm:"column:payload_json;type:text" json:"payload_json"`
	CreatedBy     string    `gorm:"column:created_by;size:32" json:"created_by"`
	CreatedAt     time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt     time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (ScenarioTemplateAsset) TableName() string { return "rgx_scenario_template" }

// ScenarioTemplateVersion is immutable template history.
type ScenarioTemplateVersion struct {
	ID          string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID    string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	TemplateID  string    `gorm:"column:template_id;size:32;not null;uniqueIndex:uk_scenario_template_version" json:"template_id"`
	Version     int64     `gorm:"column:version;not null;uniqueIndex:uk_scenario_template_version" json:"version"`
	PayloadJSON string    `gorm:"column:payload_json;type:text" json:"payload_json"`
	ChangeNote  string    `gorm:"column:change_note;size:512" json:"change_note"`
	CreatedBy   string    `gorm:"column:created_by;size:32" json:"created_by"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"created_at"`
}

func (ScenarioTemplateVersion) TableName() string { return "rgx_scenario_template_version" }

// PromptPolicyVersion stores one immutable prompt/parameter policy version.
type PromptPolicyVersion struct {
	ID        string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID  string    `gorm:"column:tenant_id;size:32;not null;index:uk_prompt_policy_version,unique" json:"tenant_id"`
	Scope     string    `gorm:"column:scope;size:16;not null;index:uk_prompt_policy_version,unique" json:"scope"`
	ObjectID  string    `gorm:"column:object_id;size:64;not null;default:'';index:uk_prompt_policy_version,unique" json:"object_id"`
	Version   int64     `gorm:"column:version;not null;uniqueIndex:uk_prompt_policy_version" json:"version"`
	Active    bool      `gorm:"column:active;not null;default:false;index" json:"active"`
	PresetID  string    `gorm:"column:preset_id;size:64" json:"preset_id"`
	ProfileID string    `gorm:"column:profile_id;size:64" json:"profile_id"`
	Payload   string    `gorm:"column:payload;type:text" json:"payload"`
	CreatedBy string    `gorm:"column:created_by;size:32" json:"created_by"`
	CreatedAt time.Time `gorm:"column:created_at" json:"created_at"`
}

func (PromptPolicyVersion) TableName() string { return "rgx_prompt_policy_version" }

// EvalSet is a versionable regression suite for an app or scenario template.
type EvalSet struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	Name               string    `gorm:"column:name;size:128;not null" json:"name"`
	Description        string    `gorm:"column:description;size:512" json:"description"`
	Source             string    `gorm:"column:source;size:16;not null;default:manual" json:"source"`
	AppType            string    `gorm:"column:app_type;size:16" json:"app_type"`
	AppID              string    `gorm:"column:app_id;size:64" json:"app_id"`
	ScenarioTemplateID string    `gorm:"column:scenario_template_id;size:32;index" json:"scenario_template_id"`
	DatasetIDs         string    `gorm:"column:dataset_ids;size:512" json:"dataset_ids"`
	Version            int64     `gorm:"column:version;not null;default:1" json:"version"`
	ItemCount          int64     `gorm:"column:item_count;not null;default:0" json:"item_count"`
	Status             string    `gorm:"column:status;size:16;not null;default:draft" json:"status"`
	CreatedBy          string    `gorm:"column:created_by;size:32" json:"created_by"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt          time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (EvalSet) TableName() string { return "rgx_eval_set" }

// EvalCase is one replayable knowledge-quality question.
type EvalCase struct {
	ID                     string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID               string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	EvalSetID              string    `gorm:"column:eval_set_id;size:32;not null;index" json:"eval_set_id"`
	Question               string    `gorm:"column:question;type:text" json:"question"`
	ExpectedAnswer         string    `gorm:"column:expected_answer;type:text" json:"expected_answer"`
	ExpectedKeywords       string    `gorm:"column:expected_keywords;size:512" json:"expected_keywords"`
	ExpectedCitationDocIDs string    `gorm:"column:expected_citation_doc_ids;size:512" json:"expected_citation_doc_ids"`
	Source                 string    `gorm:"column:source;size:16;not null;default:manual" json:"source"`
	SourceID               string    `gorm:"column:source_id;size:64;index" json:"source_id"`
	SourceSessionID        string    `gorm:"column:source_session_id;size:64;index" json:"source_session_id"`
	SourceRequestID        string    `gorm:"column:source_request_id;size:64;index" json:"source_request_id"`
	SourceFeedbackComment  string    `gorm:"column:source_feedback_comment;size:1024" json:"source_feedback_comment"`
	Status                 string    `gorm:"column:status;size:16;not null;default:draft" json:"status"`
	CreatedBy              string    `gorm:"column:created_by;size:32" json:"created_by"`
	CreatedAt              time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt              time.Time `gorm:"column:updated_at" json:"updated_at"`
}

func (EvalCase) TableName() string { return "rgx_eval_case" }

// Governance status constants.
const (
	TemplateSourceBuiltin = "builtin"
	TemplateSourceImport  = "imported"
	TemplateSourceCopy    = "copy"
	TemplateSourceCustom  = "custom"

	AssetStatusDraft     = "draft"
	AssetStatusPublished = "published"
	AssetStatusArchived  = "archived"

	PromptScopeTenant = "tenant"
	PromptScopeChat   = "chat"
	PromptScopeSearch = "search"
	PromptScopeAgent  = "agent"

	EvalSourceManual    = "manual"
	EvalSourceTemplate  = "template"
	EvalSourceBadcase   = "badcase"
	EvalStatusDraft     = "draft"
	EvalStatusPublished = "published"

	KnowledgeReviewNone    = "none"
	KnowledgeReviewCurrent = "current"
	KnowledgeReviewDue     = "due"
	KnowledgeReviewExpired = "expired"
)
