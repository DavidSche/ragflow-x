package model

import "time"

const (
	DBDriverPostgres = "postgres"
	DBDriverMySQL    = "mysql"
	DBDriverSQLite   = "sqlite"

	DBHealthUnknown   = "unknown"
	DBHealthHealthy   = "healthy"
	DBHealthUnhealthy = "unhealthy"
)

// DBConnection is a tenant-owned read-only database target. DSNRef points to
// a secret-manager reference and must never contain or log connection secrets.
type DBConnection struct {
	ID                 string     `gorm:"column:id;primaryKey;size:32" json:"id"`
	TenantID           string     `gorm:"column:tenant_id;index;size:32;not null" json:"tenant_id"`
	Name               string     `gorm:"column:name;size:128;not null" json:"name"`
	Driver             string     `gorm:"column:driver;size:32;not null" json:"driver"`
	DSNRef             string     `gorm:"column:dsn_ref;size:256;not null" json:"-"`
	AuthorizationScope string     `gorm:"column:authorization_scope;type:text;not null" json:"authorization_scope"`
	ReadOnly           bool       `gorm:"column:read_only;not null" json:"read_only"`
	MaxOpenConns       int        `gorm:"column:max_open_conns;not null" json:"max_open_conns"`
	MaxIdleConns       int        `gorm:"column:max_idle_conns;not null" json:"max_idle_conns"`
	HealthStatus       string     `gorm:"column:health_status;size:32;not null" json:"health_status"`
	LastVerifiedAt     *time.Time `gorm:"column:last_verified_at" json:"last_verified_at"`
	CreatedBy          string     `gorm:"column:created_by;size:32;not null" json:"created_by"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null" json:"created_at"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null" json:"updated_at"`
}

func (DBConnection) TableName() string { return "rgx_db_connection" }
