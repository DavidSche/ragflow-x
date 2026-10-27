package model

import "time"

type QueryTemplate struct {
	ID                 string    `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string    `gorm:"column:tenant_id;uniqueIndex:uq_rgx_query_template_tenant_name;size:32;not null" json:"tenant_id"`
	Name               string    `gorm:"column:name;uniqueIndex:uq_rgx_query_template_tenant_name;size:128;not null" json:"name"`
	SQLTemplate        string    `gorm:"column:sql_template;type:text;not null" json:"sql_template"`
	ParameterSchema    string    `gorm:"column:parameter_schema;type:text;not null" json:"parameter_schema"`
	ResultSchema       string    `gorm:"column:result_schema;type:text;not null" json:"result_schema"`
	ExecutionPolicy    string    `gorm:"column:execution_policy;type:text;not null" json:"execution_policy"`
	AuthorizationScope string    `gorm:"column:authorization_scope;type:text;not null" json:"authorization_scope"`
	ConnectionID       string    `gorm:"column:connection_id;index;size:32;not null" json:"connection_id"`
	Active             bool      `gorm:"column:active;not null" json:"active"`
	CreatedBy          string    `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt          time.Time `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt          time.Time `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (QueryTemplate) TableName() string { return "rgx_query_template" }
