package model

import "time"

// Lifecycle status values shared by providers, instances and models.
const (
	ProviderStatusActive   = "active"
	ProviderStatusDisabled = "disabled"
	ModelStatusActive      = "active"
	ModelStatusInactive    = "inactive"
	ModelVerifyUnknown     = "unknown"
	ModelVerifySuccess     = "success"
	ModelVerifyFail        = "fail"
)

// Model type bit flags, mirroring RAGFlow's ModelTypeBinary exactly so that a
// single integer can encode a model's full capability set (LSB -> MSB).
const (
	ModelTypeChat      = 1 << iota // 1
	ModelTypeEmbedding             // 2
	ModelTypeASR                   // 4
	ModelTypeVision                // 8
	ModelTypeRerank                // 16
	ModelTypeTTS                   // 32
	ModelTypeOCR                   // 64
)

// ModelTypeNames labels each bit value in ascending order.
var ModelTypeNames = map[int]string{
	ModelTypeChat:      "chat",
	ModelTypeEmbedding: "embedding",
	ModelTypeASR:       "asr",
	ModelTypeVision:    "vision",
	ModelTypeRerank:    "rerank",
	ModelTypeTTS:       "tts",
	ModelTypeOCR:       "ocr",
}

// ModelTypeValues lists bits from LSB to MSB.
var modelTypeValues = []int{
	ModelTypeChat,
	ModelTypeEmbedding,
	ModelTypeASR,
	ModelTypeVision,
	ModelTypeRerank,
	ModelTypeTTS,
	ModelTypeOCR,
}

// ModelTypeMask ORs the named model types into a bitmask. Unknown names are
// ignored so forward-compatible callers never corrupt stored data.
func ModelTypeMask(names []string) int {
	mask := 0
	for _, n := range names {
		if v, ok := modelTypeNameValue(n); ok {
			mask |= v
		}
	}
	return mask
}

func modelTypeNameValue(name string) (int, bool) {
	for _, v := range modelTypeValues {
		if ModelTypeNames[v] == name {
			return v, true
		}
	}
	return 0, false
}

// ModelTypeLabels expands a bitmask into its human model-type names.
func ModelTypeLabels(mask int) []string {
	var out []string
	for _, v := range modelTypeValues {
		if mask&v != 0 {
			out = append(out, ModelTypeNames[v])
		}
	}
	return out
}

// ModelProviderInstance is a named connection configuration (api_key, base_url,
// region) under a provider. This is the real unit that talks to the upstream.
type ModelProviderInstance struct {
	ID           string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID     string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProviderID   string    `gorm:"column:provider_id;size:32;not null;index" json:"provider_id"`
	InstanceName string    `gorm:"column:instance_name;size:128;not null" json:"instance_name"`
	APIKeyEnc    string    `gorm:"column:api_key_enc;size:1024" json:"-"`
	BaseURL      string    `gorm:"column:base_url;size:512" json:"base_url"`
	Region       string    `gorm:"column:region;size:64" json:"region"`
	Status       string    `gorm:"column:status;size:32;not null;default:active" json:"status"`
	ExtraJSON    string    `gorm:"column:extra_json;size:1024" json:"extra_json"`
	CreatedAt    time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (ModelProviderInstance) TableName() string { return "rgx_model_provider_instance" }

// ModelTypeName is a convenience alias used by API payloads.
type ModelTypeName = string

// ModelProviderModel is a model configured on a provider instance. model_type
// is a bitmask of ModelType* values; extra_json carries max_tokens, is_tools,
// thinking and verify metadata that RAGFlow stores per model.
type ModelProviderModel struct {
	ID         string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID   string    `gorm:"column:tenant_id;size:32;not null;index" json:"tenant_id"`
	ProviderID string    `gorm:"column:provider_id;size:32;not null;index" json:"provider_id"`
	InstanceID string    `gorm:"column:instance_id;size:32;not null;index" json:"instance_id"`
	ModelName  string    `gorm:"column:model_name;size:128;not null" json:"model_name"`
	ModelType  int       `gorm:"column:model_type;not null;default:1" json:"model_type"`
	Status     string    `gorm:"column:status;size:32;not null;default:active" json:"status"`
	MaxTokens  int       `gorm:"column:max_tokens;not null;default:8192" json:"max_tokens"`
	IsTools    bool      `gorm:"column:is_tools;not null;default:false" json:"is_tools"`
	Thinking   bool      `gorm:"column:thinking;not null;default:false" json:"thinking"`
	Verify     string    `gorm:"column:verify;size:32" json:"verify"`
	ExtraJSON  string    `gorm:"column:extra_json;size:1024" json:"extra_json"`
	CreatedAt  time.Time `gorm:"column:created_at" json:"created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at" json:"updated_at"`
}

// TableName is the physical table name.
func (ModelProviderModel) TableName() string { return "rgx_model_provider_model" }
