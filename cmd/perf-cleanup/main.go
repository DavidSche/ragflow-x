package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/config"
	"github.com/ragflow-x/ragflow-x/internal/db"
	"github.com/ragflow-x/ragflow-x/internal/model"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "application YAML config path")
	prefix := flag.String("tenant-prefix", "perf-baseline-", "tenant name prefix to delete")
	apply := flag.Bool("yes", false, "confirm deletion of matched tenants")
	flag.Parse()
	if !*apply {
		fatal(fmt.Errorf("refusing to delete without --yes"))
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fatal(err)
	}
	gdb, err := db.Open(cfg.Database)
	if err != nil {
		fatal(err)
	}
	defer func() {
		if sqlDB, dbErr := gdb.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	}()

	var tenants []model.Tenant
	if err := gdb.WithContext(context.Background()).Where("name LIKE ?", *prefix+"%").Find(&tenants).Error; err != nil {
		fatal(err)
	}
	if len(tenants) == 0 {
		fmt.Println("no performance tenants matched")
		return
	}
	ids := make([]string, 0, len(tenants))
	for _, tenant := range tenants {
		ids = append(ids, tenant.ID)
	}
	fmt.Printf("matched %d performance tenants; deleting\n", len(tenants))
	if err := gdb.WithContext(context.Background()).Transaction(func(tx *gorm.DB) error {
		var userIDs []string
		if err := tx.Model(&model.User{}).Where("tenant_id IN ?", ids).Pluck("id", &userIDs).Error; err != nil {
			return err
		}
		if len(userIDs) > 0 {
			if err := tx.Where("user_id IN ?", userIDs).Delete(&model.UserRole{}).Error; err != nil {
				return err
			}
		}
		for _, table := range []string{
			"rgx_gateway_idempotency", "rgx_quota_reservation", "rgx_quota", "rgx_cost_metric",
			"rgx_quota_usage", "rgx_knowledge_ops_event", "rgx_job", "rgx_audit_log", "rgx_api_key",
			"rgx_model_route", "rgx_model_provider_model", "rgx_model_provider_instance",
			"rgx_model_provider", "rgx_chat", "rgx_user",
		} {
			if !tx.Migrator().HasTable(table) {
				continue
			}
			result := tx.Exec("DELETE FROM "+table+" WHERE tenant_id IN ?", ids)
			if result.Error != nil {
				return fmt.Errorf("delete %s: %w", table, result.Error)
			}
			fmt.Printf("%s: %d rows\n", table, result.RowsAffected)
		}
		result := tx.Where("id IN ?", ids).Delete(&model.Tenant{})
		if result.Error != nil {
			return fmt.Errorf("delete tenants: %w", result.Error)
		}
		fmt.Printf("rgx_tenant: %d rows\n", result.RowsAffected)
		return nil
	}); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "perf-cleanup:", err)
	os.Exit(1)
}
