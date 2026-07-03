package models

import "time"

// UsageLimitConfigModel is an operator-declared usage limit for a tenant, one
// row per (tenant_id, scope). Scope "tenant" limits the whole tenant; scope
// "per_user" limits each end user independently. Unit is turns or steps; the
// limit resets on a rolling window of interval_seconds from first use.
type UsageLimitConfigModel struct {
	ID              string    `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	TenantID        string    `gorm:"type:uuid;not null;default:'00000000-0000-0000-0000-000000000001';uniqueIndex:uq_usage_limit_configs_tenant_scope,priority:1" json:"tenant_id"`
	Scope           string    `gorm:"type:varchar(20);not null;default:tenant;uniqueIndex:uq_usage_limit_configs_tenant_scope,priority:2" json:"scope"`
	Unit            string    `gorm:"type:varchar(20);not null" json:"unit"`
	LimitValue      int64     `gorm:"not null" json:"limit_value"`
	IntervalSeconds int64     `gorm:"not null;default:2592000" json:"interval_seconds"`
	Enabled         bool      `gorm:"not null;default:true" json:"enabled"`
	CreatedAt       time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// TableName maps the model to the usage_limit_configs table.
func (UsageLimitConfigModel) TableName() string { return "usage_limit_configs" }

// UsageCounterModel is a rolling usage counter, one row per
// (tenant_id, user_sub, window_kind). user_sub "" is the tenant-wide counter;
// a real sub is a per-end-user counter. Both turns_count and steps_count are
// incremented every turn so the enforced unit can switch mid-period without a
// recount. period_start is reset (with the counts) when the rolling window
// elapses, inside the atomic upsert.
type UsageCounterModel struct {
	ID          string    `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	TenantID    string    `gorm:"type:uuid;not null;default:'00000000-0000-0000-0000-000000000001';uniqueIndex:uq_usage_counters_scope_window,priority:1" json:"tenant_id"`
	UserSub     string    `gorm:"column:user_sub;type:varchar(255);not null;default:'';uniqueIndex:uq_usage_counters_scope_window,priority:2" json:"user_sub"`
	WindowKind  string    `gorm:"type:varchar(20);not null;default:rolling;uniqueIndex:uq_usage_counters_scope_window,priority:3" json:"window_kind"`
	PeriodStart time.Time `gorm:"not null;default:now()" json:"period_start"`
	TurnsCount  int64     `gorm:"not null;default:0" json:"turns_count"`
	StepsCount  int64     `gorm:"not null;default:0" json:"steps_count"`
	CreatedAt   time.Time `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime" json:"updated_at"`
}

// TableName maps the model to the usage_counters table.
func (UsageCounterModel) TableName() string { return "usage_counters" }
