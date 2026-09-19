package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

// Migration is a versioned schema step. Each step either creates or alters
// tables in a dialect-agnostic way via GORM AutoMigrate, which keeps both
// PostgreSQL and SQLite targets in sync.
type Migration struct {
	Version int64
	Name    string
	Migrate func(tx *gorm.DB) error
}

// migrations is the ordered list of schema steps.
var migrations = []Migration{
	{
		Version: 1,
		Name:    "init_rgx_core",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(
				&model.Tenant{},
				&model.User{},
				&model.DatasetLink{},
			)
		},
	},
	{
		Version: 2,
		Name:    "m2_rbac_model_keys_audit",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(
				&model.Team{},
				&model.Role{},
				&model.Permission{},
				&model.UserRole{},
				&model.ModelProvider{},
				&model.ModelRoute{},
				&model.APIKey{},
				&model.AuditLog{},
			); err != nil {
				return err
			}
			return seedDefaultRBAC(tx)
		},
	},
	{
		Version: 3,
		Name:    "m2_tasks_usage",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(
				&model.Task{},
				&model.QuotaUsage{},
			)
		},
	},
	{
		Version: 4,
		Name:    "m2_user_team",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.UserTeam{})
		},
	},
	{
		Version: 5,
		Name:    "m2_role_hierarchy_abac",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.Role{})
		},
	},
	{
		Version: 6,
		Name:    "m2_project_and_audit_chain",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.Project{}, &model.ProjectMember{}, &model.DatasetLink{}, &model.AuditLog{})
		},
	},
	{
		Version: 7,
		Name:    "backfill_audit_chain",
		Migrate: func(tx *gorm.DB) error {
			var rows []model.AuditLog
			if err := tx.Order("tenant_id, at, id").Find(&rows).Error; err != nil {
				return err
			}
			prev := map[string]string{}
			for i := range rows {
				r := &rows[i]
				p := prev[r.TenantID]
				r.PrevHash = p
				r.Hash = model.AuditHash(p, r)
				prev[r.TenantID] = r.Hash
				if err := tx.Save(r).Error; err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 8,
		Name:    "tenant_branding",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.Tenant{})
		},
	},
	{
		Version: 9,
		Name:    "gateway_usage_idempotency",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.QuotaUsage{}, &model.MeterRequest{})
		},
	},
	{
		Version: 10,
		Name:    "refresh_tokens",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.RefreshToken{})
		},
	},
	{
		Version: 11,
		Name:    "usage_scope_unique",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.QuotaUsage{}); err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_scope ON rgx_quota_usage (tenant_id, user_id, key_id, date)`).Error
		},
	},
	{
		Version: 12,
		Name:    "model_provider_hierarchy",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(
				&model.ModelProvider{},
				&model.ModelProviderInstance{},
				&model.ModelProviderModel{},
			); err != nil {
				return err
			}
			return tx.Exec(`UPDATE rgx_model_provider SET status = ? WHERE status = '' OR status IS NULL`, model.ProviderStatusActive).Error
		},
	},
	{
		Version: 13,
		Name:    "reconcile_builtin_rbac_matrix",
		Migrate: func(tx *gorm.DB) error {
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		Version: 14,
		Name:    "m2_team_project_binding",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.TeamProject{})
		},
	},
	{
		Version: 15,
		Name:    "m2_team_admin_role",
		Migrate: func(tx *gorm.DB) error {
			if err := reconcileBuiltinPermissions(tx); err != nil {
				return err
			}
			return seedTeamAdminOwners(tx)
		},
	},
	{
		Version: 16,
		Name:    "gateway_usage_detail",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.CostMetric{})
		},
	},
	{
		// Restored v17 (doc/33 A2): the durable async job table (rgx_job) that
		// backed the in-process worker queue. Version 17 had been retired during
		// early development and never shipped, so restoring the number keeps the
		// version trajectory comparable across databases while un-skipping it for
		// the async worker foundation.
		Version: 17,
		Name:    "async_job_table",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.Job{})
		},
	},
	{
		Version: 18,
		Name:    "m2_chat_shadow",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.ChatShadow{})
		},
	},
	{
		Version: 19,
		Name:    "reconcile_chat_rbac",
		Migrate: func(tx *gorm.DB) error {
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		Version: 20,
		Name:    "m2_message_feedback",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.MessageFeedback{})
		},
	},
	{
		Version: 21,
		Name:    "chat_shadow_config_json",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.ChatShadow{})
		},
	},
	{
		Version: 22,
		Name:    "chat_shadow_owner_message_count",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.ChatShadow{})
		},
	},
	{
		Version: 23,
		Name:    "cost_metric_chat_id",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.CostMetric{})
		},
	},
	{
		// audit_chain_sequence hardens the tamper-evident hash chain against
		// concurrent writers: a per-tenant monotonic seq with a unique
		// (tenant_id, seq) index serializes chain appends at the database
		// level. The column is added and backfilled BEFORE the unique index is
		// created — legacy rows all carry seq=0, so indexing first would fail.
		Version: 24,
		Name:    "audit_chain_sequence",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.AuditLog{}); err != nil {
				return err
			}
			var tenants []string
			if err := tx.Model(&model.AuditLog{}).Distinct().Pluck("tenant_id", &tenants).Error; err != nil {
				return err
			}
			for _, tid := range tenants {
				var rows []model.AuditLog
				// Same canonical order the original v7 backfill used.
				if err := tx.Where("tenant_id = ?", tid).Order("at ASC, id ASC").Find(&rows).Error; err != nil {
					return err
				}
				for i := range rows {
					if err := tx.Model(&model.AuditLog{}).
						Where("id = ? AND tenant_id = ?", rows[i].ID, tid).
						Update("seq", i+1).Error; err != nil {
						return err
					}
				}
			}
			return tx.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_audit_tenant_seq ON rgx_audit_log (tenant_id, seq)").Error
		},
	},
	{
		Version: 25,
		Name:    "search_app_shadow",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.SearchAppShadow{})
		},
	},
	{
		// Reconcile built-in role grants so operator/viewer/tenant_admin roles
		// pick up the new "search-app" resource permissions on upgrade.
		Version: 26,
		Name:    "reconcile_search_app_rbac",
		Migrate: func(tx *gorm.DB) error {
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		Version: 27,
		Name:    "memory_shadow",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.MemoryShadow{})
		},
	},
	{
		// Reconcile built-in role grants so roles pick up the "memory" resource.
		Version: 28,
		Name:    "reconcile_memory_rbac",
		Migrate: func(tx *gorm.DB) error {
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		Version: 29,
		Name:    "agent_shadow",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.AgentShadow{})
		},
	},
	{
		// Reconcile built-in role grants so roles pick up the "agent" resource.
		Version: 30,
		Name:    "reconcile_agent_rbac",
		Migrate: func(tx *gorm.DB) error {
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		// Reconcile built-in grants after least-privilege tightening of
		// operator/viewer (remove audit, gateway config, branding, etc.).
		Version: 31,
		Name:    "reconcile_least_privilege_rbac",
		Migrate: func(tx *gorm.DB) error {
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		// Gateway quota preauthorization: per-key monthly token budget
		// (rgx_quota) plus the per-request reservation ledger. v17 now backs
		// the async job table (doc/33 A2); this is v32 to keep the version order
		// contiguous from v31.
		Version: 32,
		Name:    "gateway_quota_preauthorization",
		Migrate: func(tx *gorm.DB) error {
			// AutoMigrate(&model.APIKey{}) adds the new token_quota column in a
			// backward-compatible way (existing keys default to 0 = unlimited).
			return tx.AutoMigrate(&model.APIKey{}, &model.QuotaLimit{}, &model.QuotaReservation{})
		},
	},
	{
		Version: 33,
		Name:    "approval_workflow",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(
				&model.Approval{},
				&model.ApprovalStep{},
				&model.ApprovalPolicy{},
				&model.ApprovalCredential{},
			); err != nil {
				return err
			}
			indexes := []string{
				`CREATE UNIQUE INDEX IF NOT EXISTS uk_rgx_approval_request_no ON rgx_approval (tenant_id, request_no)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS uk_rgx_approval_idempotency ON rgx_approval (tenant_id, idempotency_key)`,
				`CREATE INDEX IF NOT EXISTS idx_rgx_approval_tenant_created ON rgx_approval (tenant_id, created_at DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_rgx_approval_tenant_status ON rgx_approval (tenant_id, status, created_at DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_rgx_approval_object ON rgx_approval (tenant_id, object_type, object_id, status)`,
				`CREATE INDEX IF NOT EXISTS idx_rgx_approval_tenant_requester ON rgx_approval (tenant_id, requester_id, status, created_at DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_rgx_approval_pending_due ON rgx_approval (tenant_id, expires_at) WHERE status = 'pending_approval'`,
				`CREATE INDEX IF NOT EXISTS idx_rgx_approval_policy_pending ON rgx_approval (tenant_id, policy_id) WHERE status = 'pending_approval'`,
				`CREATE UNIQUE INDEX IF NOT EXISTS uk_rgx_approval_step ON rgx_approval_step (approval_id, step_no)`,
				`CREATE INDEX IF NOT EXISTS idx_rgx_approval_step_tenant_status ON rgx_approval_step (tenant_id, status, due_at)`,
				`CREATE INDEX IF NOT EXISTS idx_rgx_approval_step_approval ON rgx_approval_step (approval_id, step_no)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS uk_rgx_approval_policy ON rgx_approval_policy (tenant_id, object_type, action)`,
				`CREATE INDEX IF NOT EXISTS idx_rgx_approval_policy_match ON rgx_approval_policy (object_type, action, priority)`,
				`CREATE INDEX IF NOT EXISTS idx_rgx_approval_credential ON rgx_approval_credential (tenant_id, approval_id, consumed_at)`,
				`CREATE INDEX IF NOT EXISTS idx_rgx_approval_credential_retain ON rgx_approval_credential (retain_until_at) WHERE consumed_at IS NOT NULL`,
			}
			for _, stmt := range indexes {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		Version: 34,
		Name:    "approval_organization_adaptability",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(
				&model.ApprovalStep{},
				&model.ApprovalStepActor{},
				&model.ApprovalDelegation{},
			); err != nil {
				return err
			}
			var steps []model.ApprovalStep
			if err := tx.Where("approvers_json IS NULL OR approvers_json = '' OR approvers_json = '[]'").
				Find(&steps).Error; err != nil {
				return err
			}
			for _, step := range steps {
				legacy, err := json.Marshal([]model.ApprovalApproverSpec{{
					Type: step.ApproverType, Value: step.ApproverValue,
				}})
				if err != nil {
					return err
				}
				mode := step.ApprovalMode
				if mode == "" {
					mode = model.ApprovalModeAny
				}
				if err := tx.Model(&model.ApprovalStep{}).
					Where("id = ?", step.ID).
					Updates(map[string]interface{}{
						"approvers_json": string(legacy), "approval_mode": mode,
						"required_approvals": 1,
					}).Error; err != nil {
					return err
				}
			}
			indexes := []string{
				`DROP INDEX IF EXISTS uk_rgx_approval_policy`,
				`CREATE INDEX IF NOT EXISTS idx_rgx_approval_policy_tenant_object ON rgx_approval_policy (tenant_id, object_type, action, priority)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS uk_rgx_approval_step_actor ON rgx_approval_step_actor (step_id, actor_id)`,
				`CREATE INDEX IF NOT EXISTS idx_rgx_approval_delegation_active ON rgx_approval_delegation (tenant_id, delegate_id, starts_at, ends_at)`,
			}
			for _, stmt := range indexes {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 35,
		Name:    "task_detail_text",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.Task{})
		},
	},
	{
		Version: 36,
		Name:    "knowledge_ops_event",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.KnowledgeOpsEvent{}); err != nil {
				return err
			}
			indexes := []string{
				`CREATE INDEX IF NOT EXISTS idx_knowledge_ops_tenant_created ON rgx_knowledge_ops_event (tenant_id, created_at)`,
				`CREATE INDEX IF NOT EXISTS idx_knowledge_ops_question_hash ON rgx_knowledge_ops_event (tenant_id, question_hash, created_at)`,
			}
			for _, stmt := range indexes {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 37,
		Name:    "knowledge_ops_latency",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.KnowledgeOpsEvent{})
		},
	},
	{
		Version: 38,
		Name:    "knowledge_ops_resolution",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.KnowledgeOpsEvent{})
		},
	},
	{
		Version: 39,
		Name:    "p1_5_g_governance",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(
				&model.ScenarioTemplateAsset{},
				&model.ScenarioTemplateVersion{},
				&model.PromptPolicyVersion{},
				&model.EvalSet{},
				&model.EvalCase{},
				&model.DatasetLink{},
				&model.KnowledgeOpsEvent{},
			); err != nil {
				return err
			}
			indexes := []string{
				`CREATE INDEX IF NOT EXISTS idx_scenario_template_tenant_status ON rgx_scenario_template (tenant_id, status)`,
				`CREATE INDEX IF NOT EXISTS idx_prompt_policy_active ON rgx_prompt_policy_version (tenant_id, scope, object_id, active)`,
				`CREATE INDEX IF NOT EXISTS idx_dataset_lifecycle_expiry ON rgx_dataset_link (tenant_id, expires_at, review_status)`,
				`CREATE INDEX IF NOT EXISTS idx_eval_set_tenant_status ON rgx_eval_set (tenant_id, status)`,
			}
			for _, stmt := range indexes {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		// Existing databases may predate the unified permission-matrix
		// reconciliation. Refresh immutable built-in role grants on upgrade.
		Version: 40,
		Name:    "reconcile_builtin_rbac",
		Migrate: func(tx *gorm.DB) error {
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		Version: 41,
		Name:    "assistant_catalog",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.AssistantCatalog{}); err != nil {
				return err
			}
			indexes := []string{
				`CREATE UNIQUE INDEX IF NOT EXISTS uk_assistant_catalog_target ON rgx_assistant_catalog (tenant_id, kind, target_id)`,
				`CREATE INDEX IF NOT EXISTS idx_assistant_catalog_target ON rgx_assistant_catalog (tenant_id, kind, target_id)`,
			}
			for _, stmt := range indexes {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			backfill := func(kind string) error {
				nameColumn := map[string]string{"chat": "name", "agent": "title"}[kind]
				riskLevel := "low"
				if kind == "agent" {
					riskLevel = "medium"
				}
				query := `
					INSERT INTO rgx_assistant_catalog (
						id, tenant_id, kind, target_id, name, description,
						upstream_status, governance_status, effective_status, effective_status_reason,
						owner_id, categories_json, capabilities_json, intents_json, keywords_json, examples_json,
						routing_weight, assistant_risk_level, capability_risk_json, workflow_risk_json,
						workflow_risk_upper_bound, agent_flow_readiness, discoverable, auto_select_enabled, catalog_version,
						routing_readiness, created_at, updated_at
					)
					SELECT
						tenant_id || ':` + kind + `:' || id, tenant_id, ?, id,
						` + nameColumn + `, '',
						CASE WHEN status = 'active' THEN 'active' ELSE 'archived' END,
						'enabled',
						CASE WHEN status = 'active' THEN 'active' ELSE 'inactive' END,
						CASE WHEN status = 'active' THEN 'ACTIVE' ELSE 'UPSTREAM_ARCHIVED' END,
						owner_id, '[]', '[]', '[]', '[]', '[]',
						1,
						'` + riskLevel + `',
						'{}', '{}',
						'` + riskLevel + `',
						CASE WHEN ? = 'chat' THEN 1 ELSE 0 END, TRUE, FALSE, 1,
						0.25, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
					FROM ` + map[string]string{"chat": "rgx_chat", "agent": "rgx_agent"}[kind] + `
					WHERE NOT EXISTS (
						SELECT 1 FROM rgx_assistant_catalog c
						WHERE c.tenant_id = ` + map[string]string{"chat": "rgx_chat", "agent": "rgx_agent"}[kind] + `.tenant_id
						  AND c.kind = ?
						  AND c.target_id = ` + map[string]string{"chat": "rgx_chat", "agent": "rgx_agent"}[kind] + `.id
					)
				`
				return tx.Exec(query, kind, kind, kind).Error
			}
			if err := backfill("chat"); err != nil {
				return err
			}
			return backfill("agent")
		},
	},
	{
		Version: 42,
		Name:    "assistant_catalog_rbac",
		Migrate: func(tx *gorm.DB) error {
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		Version: 43,
		Name:    "conversation_route_m2",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(
				&model.RouteDecision{},
				&model.RouteSelection{},
				&model.BootstrapOperation{},
			)
		},
	},
	{
		Version: 44,
		Name:    "route_evaluation_m25",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.RouteEvaluationRun{}); err != nil {
				return err
			}
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		Version: 45,
		Name:    "route_policy_m3",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.RouteEvaluationRun{})
		},
	},
	{
		Version: 46,
		Name:    "agent_flow_readiness_and_tenant_route_mode",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.Tenant{}, &model.AssistantCatalog{}); err != nil {
				return err
			}
			if err := tx.Exec(`UPDATE rgx_assistant_catalog SET agent_flow_readiness = 1 WHERE kind = 'chat'`).Error; err != nil {
				return err
			}
			return tx.Exec(`UPDATE rgx_tenant SET auto_route_mode = 'recommend_only' WHERE auto_route_mode IS NULL OR auto_route_mode = ''`).Error
		},
	},
	{
		Version: 47,
		Name:    "assistant_catalog_boolean_type",
		Migrate: func(tx *gorm.DB) error {
			if err := reconcileBuiltinPermissions(tx); err != nil {
				return err
			}
			if tx.Dialector.Name() != "postgres" {
				return nil
			}
			var dataType string
			if err := tx.Raw(`SELECT data_type FROM information_schema.columns WHERE table_name = 'rgx_assistant_catalog' AND column_name = 'discoverable'`).Scan(&dataType).Error; err != nil {
				return err
			}
			if dataType != "integer" {
				return nil
			}
			return tx.Exec(`ALTER TABLE rgx_assistant_catalog ALTER COLUMN discoverable TYPE boolean USING (discoverable <> 0)`).Error
		},
	},
	{
		Version: 48,
		Name:    "assistant_execute_rbac",
		Migrate: func(tx *gorm.DB) error {
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		Version: 49,
		Name:    "billing_to_usage_governance",
		Migrate: func(tx *gorm.DB) error {
			if err := reconcileBuiltinPermissions(tx); err != nil {
				return err
			}
			if err := tx.Where("resource IN ?", []string{"billing", "billing-export"}).Delete(&model.Permission{}).Error; err != nil {
				return err
			}
			if err := tx.AutoMigrate(&model.GatewayIdempotency{}); err != nil {
				return err
			}
			if err := tx.AutoMigrate(&model.QuotaUsage{}, &model.CostMetric{}); err != nil {
				return err
			}
			if err := tx.AutoMigrate(
				&model.ReleaseCandidate{},
				&model.ExecutionSnapshot{},
				&model.EvaluationSetVersion{},
				&model.EvaluationCaseVersion{},
				&model.EvaluationRun{},
				&model.EvaluationCaseResult{},
				&model.EvidenceBundle{},
				&model.ReleaseGateDecision{},
				&model.Release{},
				&model.QualityIssue{},
			); err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_active_gate_decision ON rgx_release_gate_decision (tenant_id, release_candidate_id, candidate_version) WHERE active_gate IS TRUE`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`DELETE FROM rgx_permission WHERE resource IN ('billing','billing-export')`).Error; err != nil {
				return err
			}
			return createReleaseGovernanceGuards(tx)
		},
	},
	{
		Version: 50,
		Name:    "gateway_request_count_quota",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.APIKey{}, &model.QuotaLimit{}, &model.QuotaReservation{}); err != nil {
				return err
			}
			// Existing pending reservations were already represented in the
			// quota row's pending balances. Mark them applied so a release or
			// stale reap cannot release the same balance a second time.
			return tx.Model(&model.QuotaReservation{}).
				Where("status = ?", model.QuotaReservationPending).
				Update("applied", true).Error
		},
	},
	{
		Version: 51,
		Name:    "terminal_evaluation_run_freeze",
		Migrate: func(tx *gorm.DB) error {
			if tx.Dialector.Name() == "postgres" {
				if err := tx.Exec(`CREATE OR REPLACE FUNCTION rgx_terminal_evaluation_run_freeze() RETURNS trigger AS $$
					BEGIN
						IF OLD.status IN ('COMPLETED','FAILED','CANCELLED') THEN
							RAISE EXCEPTION 'terminal evaluation run is immutable';
						END IF;
						RETURN OLD;
					END;
				$$ LANGUAGE plpgsql`).Error; err != nil {
					return err
				}
				if err := tx.Exec(`DROP TRIGGER IF EXISTS trg_evaluation_run_terminal_guard ON rgx_evaluation_run`).Error; err != nil {
					return err
				}
				return tx.Exec(`CREATE TRIGGER trg_evaluation_run_terminal_guard BEFORE UPDATE OR DELETE ON rgx_evaluation_run
					FOR EACH ROW EXECUTE FUNCTION rgx_terminal_evaluation_run_freeze()`).Error
			}

			statements := []string{
				`CREATE TRIGGER IF NOT EXISTS trg_evaluation_run_terminal_guard BEFORE UPDATE ON rgx_evaluation_run
					WHEN OLD.status IN ('COMPLETED','FAILED','CANCELLED')
					BEGIN SELECT RAISE(ABORT, 'terminal evaluation run is immutable'); END`,
				`CREATE TRIGGER IF NOT EXISTS trg_evaluation_run_terminal_delete_guard BEFORE DELETE ON rgx_evaluation_run
					WHEN OLD.status IN ('COMPLETED','FAILED','CANCELLED')
					BEGIN SELECT RAISE(ABORT, 'terminal evaluation run is immutable'); END`,
			}
			for _, statement := range statements {
				if err := tx.Exec(statement).Error; err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 52,
		Name:    "api_key_ip_allowlist",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.APIKey{})
		},
	},
	{
		Version: 53,
		Name:    "alert_event_worklist",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.AlertEvent{}); err != nil {
				return err
			}
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		Version: 54,
		Name:    "immutable_audit_anchor",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.AuditAnchor{}); err != nil {
				return err
			}
			if tx.Dialector.Name() == "postgres" {
				if err := tx.Exec(`CREATE OR REPLACE FUNCTION rgx_audit_anchor_freeze() RETURNS trigger AS $$
					BEGIN
						RAISE EXCEPTION 'audit anchors are immutable';
					END;
				$$ LANGUAGE plpgsql`).Error; err != nil {
					return err
				}
				if err := tx.Exec(`DROP TRIGGER IF EXISTS trg_audit_anchor_guard ON rgx_audit_anchor`).Error; err != nil {
					return err
				}
				return tx.Exec(`CREATE TRIGGER trg_audit_anchor_guard BEFORE UPDATE OR DELETE ON rgx_audit_anchor
					FOR EACH ROW EXECUTE FUNCTION rgx_audit_anchor_freeze()`).Error
			}

			statements := []string{
				`CREATE TRIGGER IF NOT EXISTS trg_audit_anchor_guard BEFORE UPDATE ON rgx_audit_anchor
					BEGIN SELECT RAISE(ABORT, 'audit anchors are immutable'); END`,
				`CREATE TRIGGER IF NOT EXISTS trg_audit_anchor_delete_guard BEFORE DELETE ON rgx_audit_anchor
					BEGIN SELECT RAISE(ABORT, 'audit anchors are immutable'); END`,
			}
			for _, statement := range statements {
				if err := tx.Exec(statement).Error; err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 55,
		Name:    "tenant_type_and_platform_guard",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.Tenant{}); err != nil {
				return err
			}
			platform := model.Tenant{
				ID: model.PlatformTenantID, Name: "Platform", Type: model.TenantTypePlatform,
				Status: model.TenantStatusActive,
			}
			if err := tx.Where("id = ?", model.PlatformTenantID).Attrs(platform).FirstOrCreate(&platform).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Tenant{}).Where("id = ?", model.PlatformTenantID).
				Updates(map[string]interface{}{
					"type":   model.TenantTypePlatform,
					"status": model.TenantStatusActive,
				}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Tenant{}).Where("type IS NULL OR type = ''").
				Update("type", model.TenantTypeWorkspace).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uk_one_platform_tenant ON rgx_tenant (type) WHERE type = 'platform'`).Error; err != nil {
				return err
			}
			return createTenantPlatformGuards(tx)
		},
	},
	{
		Version: 56,
		Name:    "enterprise_connection_catalog",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(
				&model.EnterpriseConnection{},
				&model.EnterpriseConnectionVersion{},
				&model.EnterpriseConnectionBinding{},
				&model.EnterpriseConnectionBindingVersion{},
			); err != nil {
				return err
			}
			if err := createEnterpriseConnectionGuards(tx); err != nil {
				return err
			}
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		Version: 57,
		Name:    "model_route_enterprise_pinning",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(
				&model.EnterpriseConnection{},
				&model.EnterpriseConnectionVersion{},
				&model.EnterpriseConnectionBinding{},
				&model.EnterpriseConnectionBindingVersion{},
				&model.ModelRoute{},
				&model.ModelRouteEnterprisePin{},
				&model.ExecutionSnapshot{},
			); err != nil {
				return err
			}
			if err := createEnterpriseConnectionGuards(tx); err != nil {
				return err
			}
			return createModelRoutePinGuards(tx)
		},
	},
	{
		Version: 58,
		Name:    "execution_snapshot_model_route_pin_version",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.ExecutionSnapshot{})
		},
	},
	{
		Version: 59,
		Name:    "release_evidence_connection_credential_identity",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(
				&model.ExecutionSnapshot{},
				&model.EvidenceBundle{},
				&model.Release{},
			)
		},
	},
	{
		Version: 60,
		Name:    "enterprise_connection_health_checks",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(
				&model.EnterpriseConnection{},
				&model.EnterpriseConnectionHealthCheck{},
			); err != nil {
				return err
			}
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		Version: 61,
		Name:    "enterprise_connection_health_check_guards",
		Migrate: func(tx *gorm.DB) error {
			return createEnterpriseConnectionHealthCheckGuards(tx)
		},
	},
	{
		Version: 62,
		Name:    "acting_context_and_approval_fingerprint",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(
				&model.Approval{},
				&model.ActingContext{},
			); err != nil {
				return err
			}
			return nil
		},
	},
	{
		Version: 63,
		Name:    "approval_operation_result_ledger",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.ApprovalOperation{})
		},
	},
	{
		Version: 64,
		Name:    "runtime_tenant_row_security",
		Migrate: func(tx *gorm.DB) error {
			return createTenantRuntimeRowSecurity(tx)
		},
	},
	{
		Version: 65,
		Name:    "ragflow_external_reference_tenant_isolation",
		Migrate: func(tx *gorm.DB) error {
			var duplicateCount int64
			if err := tx.Table("rgx_dataset_link").
				Select("COUNT(DISTINCT tenant_id)").
				Where("ragflow_dataset_id IN (?)",
					tx.Table("rgx_dataset_link").Select("ragflow_dataset_id").Group("ragflow_dataset_id").Having("COUNT(*) > 1"),
				).
				Count(&duplicateCount).Error; err != nil {
				return err
			}
			if duplicateCount > 0 {
				return fmt.Errorf("ragflow external dataset references must be unique per tenant")
			}
			if tx.Dialector.Name() == "postgres" {
				if err := tx.Exec(`ALTER TABLE rgx_dataset_link DROP CONSTRAINT IF EXISTS rgx_dataset_link_ragflow_dataset_id_key`).Error; err != nil {
					return err
				}
				if err := tx.Exec(`DROP INDEX IF EXISTS idx_rgx_dataset_link_ragflow_dataset_id`).Error; err != nil {
					return err
				}
				return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uk_dataset_link_tenant_ragflow_id ON rgx_dataset_link (tenant_id, ragflow_dataset_id)`).Error
			}
			if err := tx.Exec(`DROP INDEX IF EXISTS idx_rgx_dataset_link_ragflow_dataset_id`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uk_dataset_link_tenant_ragflow_id ON rgx_dataset_link (tenant_id, ragflow_dataset_id)`).Error
		},
	},
	{
		Version: 66,
		Name:    "audit_governance_fields",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.AuditLog{}); err != nil {
				return err
			}
			var logs []model.AuditLog
			if err := tx.Order("tenant_id ASC, seq ASC").Find(&logs).Error; err != nil {
				return err
			}
			for index := range logs {
				hash := model.AuditHash(logs[index].PrevHash, &logs[index])
				if hash != logs[index].Hash {
					if err := tx.Model(&model.AuditLog{}).Where("id = ?", logs[index].ID).
						Update("hash", hash).Error; err != nil {
						return err
					}
				}
			}
			return nil
		},
	},
	{
		Version: 67,
		Name:    "enterprise_connection_reference_contract",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(
				&model.EnterpriseConnection{},
				&model.EnterpriseConnectionVersion{},
				&model.EnterpriseConnectionBinding{},
				&model.EnterpriseConnectionBindingVersion{},
				&model.ModelRoute{},
				&model.ModelRouteEnterprisePin{},
				&model.ExecutionSnapshot{},
				&model.EvidenceBundle{},
			); err != nil {
				return err
			}
			return createEnterpriseConnectionReferenceContracts(tx)
		},
	},
	{
		Version: 68,
		Name:    "governance_list_and_audit_indexes",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.AuditLog{}); err != nil {
				return err
			}
			indexes := []string{
				`CREATE INDEX IF NOT EXISTS idx_enterprise_connection_keyset ON rgx_enterprise_connection (created_at DESC, id DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_enterprise_connection_tenant_keyset ON rgx_enterprise_connection (owner_tenant_id, created_at DESC, id DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_audit_tenant_at ON rgx_audit_log (tenant_id, at DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_audit_tenant_action ON rgx_audit_log (tenant_id, action)`,
				`CREATE INDEX IF NOT EXISTS idx_audit_tenant_resource_at ON rgx_audit_log (tenant_id, resource, at DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_audit_tenant_target_at ON rgx_audit_log (tenant_id, target_tenant_id, at DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_audit_tenant_approval ON rgx_audit_log (tenant_id, approval_id)`,
			}
			for _, statement := range indexes {
				if err := tx.Exec(statement).Error; err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 69,
		Name:    "audit_acting_context_identity",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.AuditLog{}); err != nil {
				return err
			}
			var logs []model.AuditLog
			if err := tx.Order("tenant_id ASC, seq ASC").Find(&logs).Error; err != nil {
				return err
			}
			prevHashes := map[string]string{}
			for index := range logs {
				row := &logs[index]
				row.PrevHash = prevHashes[row.TenantID]
				row.Hash = model.AuditHash(row.PrevHash, row)
				prevHashes[row.TenantID] = row.Hash
				if err := tx.Save(row).Error; err != nil {
					return err
				}
			}
			if tx.Dialector.Name() == "postgres" {
				return tx.Exec(`CREATE INDEX IF NOT EXISTS idx_audit_tenant_acting_context ON rgx_audit_log (tenant_id, acting_context_id)`).Error
			}
			return nil
		},
	},
	{
		Version: 70,
		Name:    "workspace_role_terms_and_permission_catalog",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Model(&model.Role{}).Where("id = ? AND name = ?", model.RoleTenantAdmin, "租户管理员").Updates(map[string]interface{}{
				"name":        "工作区管理员",
				"description": "工作区内管理",
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Role{}).Where("id = ? AND description = ?", model.RoleOperator, "租户内读写与执行").Update("description", "工作区内读写与执行").Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Role{}).Where("id = ? AND description = ?", model.RoleViewer, "租户内只读").Update("description", "工作区内只读").Error; err != nil {
				return err
			}
			return reconcileBuiltinPermissions(tx)
		},
	},
	{
		Version: 71,
		Name:    "ragflow_resource_sync_and_binding_versions",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(
				&model.ResourceSyncSetting{},
				&model.SyncRun{},
				&model.SyncItem{},
				&model.ResourceSyncTenantMapping{},
				&model.ResourceBinding{},
				&model.ResourceBindingVersion{},
			); err != nil {
				return err
			}
			if tx.Dialector.Name() == "postgres" {
				statements := []string{
					`ALTER TABLE rgx_resource_binding_version
					 ADD CONSTRAINT uk_resource_binding_version_pointer
					 UNIQUE (binding_id, id)`,
					`ALTER TABLE rgx_resource_binding ADD CONSTRAINT fk_resource_binding_current_version
					 FOREIGN KEY (id, current_binding_version_id)
					 REFERENCES rgx_resource_binding_version(binding_id, id)
					 ON UPDATE RESTRICT ON DELETE RESTRICT`,
					`CREATE OR REPLACE FUNCTION rgx_resource_binding_version_freeze() RETURNS trigger AS $$
					 BEGIN
						 RAISE EXCEPTION 'resource binding versions are immutable';
					 END;
					 $$ LANGUAGE plpgsql`,
					`DROP TRIGGER IF EXISTS trg_resource_binding_version_freeze ON rgx_resource_binding_version;
					 CREATE TRIGGER trg_resource_binding_version_freeze
					 BEFORE UPDATE OR DELETE ON rgx_resource_binding_version
					FOR EACH ROW EXECUTE FUNCTION rgx_resource_binding_version_freeze()`,
				}
				for _, statement := range statements {
					if err := tx.Exec(statement).Error; err != nil {
						return err
					}
				}
			}
			if tx.Dialector.Name() == "sqlite" {
				statements := []string{
					"CREATE TRIGGER IF NOT EXISTS trg_resource_binding_version_freeze_update BEFORE UPDATE ON rgx_resource_binding_version FOR EACH ROW BEGIN SELECT RAISE(ABORT, 'resource binding versions are immutable'); END",
					"CREATE TRIGGER IF NOT EXISTS trg_resource_binding_version_freeze_delete BEFORE DELETE ON rgx_resource_binding_version FOR EACH ROW BEGIN SELECT RAISE(ABORT, 'resource binding versions are immutable'); END",
					"CREATE TRIGGER IF NOT EXISTS trg_resource_binding_current_version_insert_guard BEFORE INSERT ON rgx_resource_binding FOR EACH ROW WHEN NEW.current_binding_version_id <> '' BEGIN SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM rgx_resource_binding_version WHERE binding_id = NEW.id AND id = NEW.current_binding_version_id) THEN RAISE(ABORT, 'current binding version must belong to binding') END; END",
					"CREATE TRIGGER IF NOT EXISTS trg_resource_binding_current_version_update_guard BEFORE UPDATE ON rgx_resource_binding FOR EACH ROW WHEN NEW.current_binding_version_id <> '' BEGIN SELECT CASE WHEN NOT EXISTS (SELECT 1 FROM rgx_resource_binding_version WHERE binding_id = NEW.id AND id = NEW.current_binding_version_id) THEN RAISE(ABORT, 'current binding version must belong to binding') END; END",
				}
				for _, statement := range statements {
					if err := tx.Exec(statement).Error; err != nil {
						return err
					}
				}
			}
			return nil
		},
	},
	{
		Version: 72,
		Name:    "immutable_setting_revisions",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(
				&model.SettingRevision{},
				&model.SettingCurrent{},
				&model.SettingSecretVersion{},
				&model.RuntimeSettingInstanceState{},
			); err != nil {
				return err
			}
			if tx.Dialector.Name() == "postgres" {
				statements := []string{
					`ALTER TABLE rgx_setting_current ADD CONSTRAINT fk_setting_current_revision
					 FOREIGN KEY (desired_revision_id) REFERENCES rgx_setting_revision(id)
					 ON UPDATE RESTRICT ON DELETE RESTRICT`,
					`CREATE OR REPLACE FUNCTION rgx_setting_revision_freeze() RETURNS trigger AS $$
					 BEGIN
						 RAISE EXCEPTION 'setting revisions are immutable';
					 END;
					 $$ LANGUAGE plpgsql`,
					`DROP TRIGGER IF EXISTS trg_setting_revision_freeze ON rgx_setting_revision;
					 CREATE TRIGGER trg_setting_revision_freeze
					 BEFORE UPDATE OF revision, schema_version, snapshot_json, created_by, created_at,
					 note, checksum, rollback_from_revision_id
					 ON rgx_setting_revision
					 FOR EACH ROW EXECUTE FUNCTION rgx_setting_revision_freeze()`,
					`DROP TRIGGER IF EXISTS trg_setting_revision_delete_freeze ON rgx_setting_revision;
					 CREATE TRIGGER trg_setting_revision_delete_freeze
					 BEFORE DELETE ON rgx_setting_revision
					 FOR EACH ROW EXECUTE FUNCTION rgx_setting_revision_freeze()`,
					`DROP TRIGGER IF EXISTS trg_setting_secret_version_freeze ON rgx_setting_secret_version;
					 CREATE TRIGGER trg_setting_secret_version_freeze
					 BEFORE UPDATE OR DELETE ON rgx_setting_secret_version
					 FOR EACH ROW EXECUTE FUNCTION rgx_setting_revision_freeze()`,
					`CREATE OR REPLACE FUNCTION rgx_setting_current_revision_guard() RETURNS trigger AS $$
					 BEGIN
						 IF NOT EXISTS (
							 SELECT 1 FROM rgx_setting_revision
							 WHERE id = NEW.desired_revision_id AND revision_status = 'current'
						 ) THEN
							 RAISE EXCEPTION 'current setting revision must be committed';
						 END IF;
						 RETURN NEW;
					 END;
					 $$ LANGUAGE plpgsql`,
					`DROP TRIGGER IF EXISTS trg_setting_current_revision_guard ON rgx_setting_current;
					 CREATE TRIGGER trg_setting_current_revision_guard
					 BEFORE INSERT OR UPDATE ON rgx_setting_current
					 FOR EACH ROW EXECUTE FUNCTION rgx_setting_current_revision_guard()`,
				}
				for _, statement := range statements {
					if err := tx.Exec(statement).Error; err != nil {
						return err
					}
				}
			}
			if tx.Dialector.Name() == "sqlite" {
				statements := []string{
					`CREATE TRIGGER IF NOT EXISTS trg_setting_revision_freeze_update
					 BEFORE UPDATE ON rgx_setting_revision
					 FOR EACH ROW WHEN OLD.revision <> NEW.revision
						 OR OLD.schema_version <> NEW.schema_version
						 OR OLD.snapshot_json <> NEW.snapshot_json
						 OR OLD.created_by <> NEW.created_by
						 OR OLD.created_at <> NEW.created_at
						 OR OLD.note <> NEW.note
						 OR OLD.checksum <> NEW.checksum
						 OR OLD.rollback_from_revision_id <> NEW.rollback_from_revision_id
					 BEGIN SELECT RAISE(ABORT, 'setting revisions are immutable'); END`,
					`CREATE TRIGGER IF NOT EXISTS trg_setting_revision_freeze_delete
					 BEFORE DELETE ON rgx_setting_revision
					 FOR EACH ROW BEGIN SELECT RAISE(ABORT, 'setting revisions are immutable'); END`,
					`CREATE TRIGGER IF NOT EXISTS trg_setting_secret_version_freeze_update
					 BEFORE UPDATE ON rgx_setting_secret_version
					 FOR EACH ROW BEGIN SELECT RAISE(ABORT, 'setting secret versions are immutable'); END`,
					`CREATE TRIGGER IF NOT EXISTS trg_setting_secret_version_freeze_delete
					 BEFORE DELETE ON rgx_setting_secret_version
					 FOR EACH ROW BEGIN SELECT RAISE(ABORT, 'setting secret versions are immutable'); END`,
					`CREATE TRIGGER IF NOT EXISTS trg_setting_current_revision_insert_guard
					 BEFORE INSERT ON rgx_setting_current
					 FOR EACH ROW WHEN NOT EXISTS (
						 SELECT 1 FROM rgx_setting_revision
						 WHERE id = NEW.desired_revision_id AND revision_status = 'current'
					 ) BEGIN SELECT RAISE(ABORT, 'current setting revision must be committed'); END`,
					`CREATE TRIGGER IF NOT EXISTS trg_setting_current_revision_update_guard
					 BEFORE UPDATE ON rgx_setting_current
					 FOR EACH ROW WHEN NOT EXISTS (
						 SELECT 1 FROM rgx_setting_revision
						 WHERE id = NEW.desired_revision_id AND revision_status = 'current'
					 ) BEGIN SELECT RAISE(ABORT, 'current setting revision must be committed'); END`,
				}
				for _, statement := range statements {
					if err := tx.Exec(statement).Error; err != nil {
						return err
					}
				}
			}
			return nil
		},
	},
	{
		Version: 73,
		Name:    "ragflow_resource_sync_artifacts",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.ResourceSyncArtifact{}); err != nil {
				return err
			}
			if tx.Dialector.Name() == "postgres" {
				statements := []string{
					`CREATE OR REPLACE FUNCTION rgx_resource_sync_artifact_freeze() RETURNS trigger AS $$
					 BEGIN
						 RAISE EXCEPTION 'resource sync artifacts are immutable';
					 END;
					 $$ LANGUAGE plpgsql`,
					`DROP TRIGGER IF EXISTS trg_resource_sync_artifact_freeze ON rgx_resource_sync_artifact;
					 CREATE TRIGGER trg_resource_sync_artifact_freeze
					 BEFORE UPDATE OR DELETE ON rgx_resource_sync_artifact
					 FOR EACH ROW EXECUTE FUNCTION rgx_resource_sync_artifact_freeze()`,
				}
				for _, statement := range statements {
					if err := tx.Exec(statement).Error; err != nil {
						return err
					}
				}
				return nil
			}
			if tx.Dialector.Name() == "sqlite" {
				statements := []string{
					"CREATE TRIGGER IF NOT EXISTS trg_resource_sync_artifact_freeze_update BEFORE UPDATE ON rgx_resource_sync_artifact FOR EACH ROW BEGIN SELECT RAISE(ABORT, 'resource sync artifacts are immutable'); END",
					"CREATE TRIGGER IF NOT EXISTS trg_resource_sync_artifact_freeze_delete BEFORE DELETE ON rgx_resource_sync_artifact FOR EACH ROW BEGIN SELECT RAISE(ABORT, 'resource sync artifacts are immutable'); END",
				}
				for _, statement := range statements {
					if err := tx.Exec(statement).Error; err != nil {
						return err
					}
				}
			}
			return nil
		},
	},
	{
		Version: 74,
		Name:    "resource_sync_artifact_lifecycle",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.ResourceSyncArtifactLifecycle{}); err != nil {
				return err
			}
			backfill := `INSERT INTO rgx_resource_sync_artifact_lifecycle
				(artifact_id, retain_until, created_at, updated_at)
				SELECT id, retain_until, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
				FROM rgx_resource_sync_artifact
				WHERE NOT EXISTS (
					SELECT 1 FROM rgx_resource_sync_artifact_lifecycle
					WHERE artifact_id = rgx_resource_sync_artifact.id
				)`
			return tx.Exec(backfill).Error
		},
	},
	{
		Version: 75,
		Name:    "resource_sync_scheduled_reconcile_enabled",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.ResourceSyncSetting{})
		},
	},
	{
		Version: 76,
		Name:    "case_insensitive_unique_display_names",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(
				&model.Tenant{}, &model.User{}, &model.DatasetLink{}, &model.AgentShadow{},
				&model.Project{}, &model.Team{}, &model.Role{},
			); err != nil {
				return err
			}
			if err := trimDisplayNames(tx); err != nil {
				return err
			}
			if err := dedupeUniqueNames(tx, "rgx_tenant", "id", "", "name"); err != nil {
				return err
			}
			if err := dedupeUniqueNames(tx, "rgx_user", "id", "", "username"); err != nil {
				return err
			}
			if err := dedupeUniqueNames(tx, "rgx_dataset_link", "id", "tenant_id", "name"); err != nil {
				return err
			}
			if err := dedupeUniqueNames(tx, "rgx_agent", "id", "tenant_id", "title"); err != nil {
				return err
			}
			if err := dedupeUniqueNames(tx, "rgx_project", "id", "tenant_id", "name"); err != nil {
				return err
			}
			if err := dedupeUniqueNames(tx, "rgx_team", "id", "tenant_id", "name"); err != nil {
				return err
			}
			if err := dedupeUniqueNames(tx, "rgx_role", "id", "tenant_id", "name"); err != nil {
				return err
			}
			indexes := []string{
				"CREATE UNIQUE INDEX IF NOT EXISTS uk_tenant_name_ci ON rgx_tenant (LOWER(name))",
				"CREATE UNIQUE INDEX IF NOT EXISTS uk_user_username_ci ON rgx_user (LOWER(username))",
				"CREATE UNIQUE INDEX IF NOT EXISTS uk_dataset_link_tenant_name_ci ON rgx_dataset_link (tenant_id, LOWER(name))",
				"CREATE UNIQUE INDEX IF NOT EXISTS uk_agent_tenant_title_ci ON rgx_agent (tenant_id, LOWER(title))",
				"CREATE UNIQUE INDEX IF NOT EXISTS uk_project_tenant_name_ci ON rgx_project (tenant_id, LOWER(name))",
				"CREATE UNIQUE INDEX IF NOT EXISTS uk_team_tenant_name_ci ON rgx_team (tenant_id, LOWER(name))",
				"CREATE UNIQUE INDEX IF NOT EXISTS uk_role_name_ci ON rgx_role (LOWER(name))",
			}
			for _, statement := range indexes {
				if err := tx.Exec(statement).Error; err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 77,
		Name:    "model_provider_instance_identity",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.ModelProviderInstance{}, &model.ModelProviderModel{}); err != nil {
				return err
			}
			if err := deduplicateModelProviderIdentities(tx); err != nil {
				return err
			}
			indexes := []string{
				"CREATE UNIQUE INDEX IF NOT EXISTS uk_model_provider_instance_name_ci ON rgx_model_provider_instance (tenant_id, provider_id, LOWER(TRIM(instance_name)))",
				"CREATE UNIQUE INDEX IF NOT EXISTS uk_model_provider_model_name_ci ON rgx_model_provider_model (tenant_id, instance_id, LOWER(TRIM(model_name)))",
			}
			for _, statement := range indexes {
				if err := tx.Exec(statement).Error; err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 78,
		Name:    "alert_delivery_lifecycle",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.AlertDelivery{})
		},
	},
	{
		Version: 79,
		Name:    "alert_delivery_lease",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.AlertDelivery{})
		},
	},
	{
		Version: 80,
		Name:    "alert_delivery_lease_fencing",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.AlertDelivery{})
		},
	},
	{
		Version: 81,
		Name:    "alert_postgres_storage_contracts",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.AlertEvent{}, &model.AlertDelivery{}); err != nil {
				return err
			}
			if tx.Dialector.Name() != "postgres" {
				return nil
			}
			if err := tx.Exec(`UPDATE rgx_alert_event SET fields_json = '{}' WHERE btrim(fields_json) = ''`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`ALTER TABLE rgx_alert_event ALTER COLUMN fields_json TYPE jsonb USING fields_json::jsonb`).Error; err != nil {
				return err
			}
			indexes := []string{
				`CREATE INDEX IF NOT EXISTS idx_alert_delivery_failed_retry
					ON rgx_alert_delivery (next_retry_at, alert_event_id, channel)
					WHERE status = 'failed' AND next_retry_at IS NOT NULL`,
				`CREATE INDEX IF NOT EXISTS idx_alert_delivery_pending_compensation
					ON rgx_alert_delivery (last_attempt_at, alert_event_id, channel)
					WHERE status = 'pending'`,
			}
			for _, statement := range indexes {
				if err := tx.Exec(statement).Error; err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 82,
		Name:    "governance_postgres_json_contracts",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(
				&model.SettingRevision{},
				&model.ResourceSyncSetting{},
				&model.SyncRun{},
			); err != nil {
				return err
			}
			if tx.Dialector.Name() != "postgres" {
				return nil
			}
			if err := tx.Exec(`DROP TRIGGER IF EXISTS trg_setting_revision_freeze ON rgx_setting_revision`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`DROP TRIGGER IF EXISTS trg_setting_revision_delete_freeze ON rgx_setting_revision`).Error; err != nil {
				return err
			}
			conversions := []struct {
				table       string
				column      string
				emptyJSON   string
				defaultJSON string
				emptyAsNull bool
			}{
				{table: "rgx_setting_revision", column: "snapshot_json", emptyJSON: "{}"},
				{table: "rgx_resource_sync_settings", column: "resource_types_json", emptyJSON: "[]", defaultJSON: "[]"},
				{table: "rgx_resource_sync_settings", column: "scope_json", emptyJSON: "{}", defaultJSON: "{}"},
				{table: "rgx_sync_run", column: "resource_types_json", emptyJSON: "[]"},
				{table: "rgx_sync_run", column: "scope_json", emptyJSON: "{}"},
				{table: "rgx_sync_run", column: "source_snapshot_json", emptyAsNull: true},
				{table: "rgx_sync_run", column: "plan_summary_json", emptyAsNull: true},
				{table: "rgx_sync_run", column: "result_summary_json", emptyAsNull: true},
			}
			for _, conversion := range conversions {
				if !conversion.emptyAsNull {
					if err := tx.Exec(
						"UPDATE "+conversion.table+" SET "+conversion.column+" = ? WHERE btrim("+conversion.column+") = ''",
						conversion.emptyJSON,
					).Error; err != nil {
						return err
					}
				}
				expression := conversion.column + "::jsonb"
				if conversion.emptyAsNull {
					expression = "NULLIF(btrim(" + conversion.column + "), '')::jsonb"
				}
				if conversion.defaultJSON != "" {
					if err := tx.Exec(
						"ALTER TABLE " + conversion.table + " ALTER COLUMN " + conversion.column + " DROP DEFAULT",
					).Error; err != nil {
						return err
					}
				}
				if err := tx.Exec(
					"ALTER TABLE " + conversion.table + " ALTER COLUMN " + conversion.column + " TYPE jsonb USING " + expression,
				).Error; err != nil {
					return err
				}
				if conversion.defaultJSON != "" {
					if err := tx.Exec(
						"ALTER TABLE " + conversion.table + " ALTER COLUMN " + conversion.column + " SET DEFAULT '" + conversion.defaultJSON + "'::jsonb",
					).Error; err != nil {
						return err
					}
				}
			}
			indexes := []string{
				`CREATE INDEX IF NOT EXISTS idx_sync_run_active_by_source
					ON rgx_sync_run (source_id, updated_at)
					WHERE status IN ('scanning', 'running')`,
				`CREATE INDEX IF NOT EXISTS idx_sync_item_skipped_conflict_identity
					ON rgx_sync_item (resource_type, external_scope_key, external_id, conflict_type)
					WHERE status = 'skipped' AND action IN ('conflict', 'relink')`,
			}
			for _, statement := range indexes {
				if err := tx.Exec(statement).Error; err != nil {
					return err
				}
			}
			settingRevisionTriggers := []string{
				`CREATE TRIGGER trg_setting_revision_freeze
					BEFORE UPDATE OF revision, schema_version, snapshot_json, created_by, created_at,
					note, checksum, rollback_from_revision_id
					ON rgx_setting_revision
					FOR EACH ROW EXECUTE FUNCTION rgx_setting_revision_freeze()`,
				`CREATE TRIGGER trg_setting_revision_delete_freeze
					BEFORE DELETE ON rgx_setting_revision
					FOR EACH ROW EXECUTE FUNCTION rgx_setting_revision_freeze()`,
			}
			for _, statement := range settingRevisionTriggers {
				if err := tx.Exec(statement).Error; err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 83,
		Name:    "sync_run_progress_columns",
		Migrate: func(tx *gorm.DB) error {
			for _, field := range []string{"ProgressTotal", "ProgressDone", "ProgressFailed"} {
				if tx.Migrator().HasColumn(&model.SyncRun{}, field) {
					continue
				}
				if err := tx.Migrator().AddColumn(&model.SyncRun{}, field); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 84,
		Name:    "preserve_governance_postgres_json_contracts",
		Migrate: func(tx *gorm.DB) error {
			if tx.Dialector.Name() != "postgres" {
				return nil
			}
			return tx.Exec(`
				DO $$
				DECLARE column_record record;
				BEGIN
					FOR column_record IN
						SELECT table_schema, table_name, column_name, data_type
						FROM information_schema.columns
						WHERE table_schema = current_schema()
							AND (
								(table_name = 'rgx_setting_revision' AND column_name = 'snapshot_json')
								OR (table_name = 'rgx_resource_sync_settings' AND column_name IN ('resource_types_json', 'scope_json'))
								OR (table_name = 'rgx_sync_run' AND column_name IN (
									'resource_types_json', 'scope_json', 'source_snapshot_json',
									'plan_summary_json', 'result_summary_json'
								))
							)
							AND data_type IN ('text', 'character varying')
					LOOP
						EXECUTE format(
							'ALTER TABLE %I.%I ALTER COLUMN %I TYPE jsonb USING NULLIF(btrim(%I), '''')::jsonb',
							column_record.table_schema, column_record.table_name,
							column_record.column_name, column_record.column_name
						);
					END LOOP;
				END $$;
			`).Error
		},
	},
	{
		Version: 85,
		Name:    "cost_metric_estimated_cost",
		Migrate: func(tx *gorm.DB) error {
			if tx.Migrator().HasColumn(&model.CostMetric{}, "EstimatedCost") {
				return nil
			}
			return tx.Migrator().AddColumn(&model.CostMetric{}, "EstimatedCost")
		},
	},
	{
		Version: 86,
		Name:    "feedback_to_knowledge_ops_badcase",
		Migrate: func(tx *gorm.DB) error {
			for _, field := range []string{"RequestID"} {
				if tx.Migrator().HasColumn(&model.MessageFeedback{}, field) {
					continue
				}
				if err := tx.Migrator().AddColumn(&model.MessageFeedback{}, field); err != nil {
					return err
				}
			}
			for _, field := range []string{"FeedbackID", "FeedbackRating", "FeedbackComment", "FeedbackAt"} {
				if tx.Migrator().HasColumn(&model.KnowledgeOpsEvent{}, field) {
					continue
				}
				if err := tx.Migrator().AddColumn(&model.KnowledgeOpsEvent{}, field); err != nil {
					return err
				}
			}
			for _, field := range []string{"SourceSessionID", "SourceRequestID", "SourceFeedbackComment"} {
				if tx.Migrator().HasColumn(&model.EvalCase{}, field) {
					continue
				}
				if err := tx.Migrator().AddColumn(&model.EvalCase{}, field); err != nil {
					return err
				}
			}
			return nil
		},
	},
	{
		Version: 87,
		Name:    "clarify_operator_role_terms",
		Migrate: func(tx *gorm.DB) error {
			return tx.Model(&model.Role{}).Where("id = ?", model.RoleOperator).Updates(map[string]interface{}{
				"name":        "内容运营员",
				"description": "工作区内内容与业务运营",
			}).Error
		},
	},
	{
		Version: 88,
		Name:    "add_business_user_role",
		Migrate: func(tx *gorm.DB) error {
			return seedDefaultRBAC(tx)
		},
	},
	{
		Version: 89,
		Name:    "document_ownership_and_auto_parse",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.DocumentOwnershipLink{})
		},
	},
	{
		Version: 90,
		Name:    "enterprise_oidc_identity",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.OidcIdentity{}); err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uk_oidc_identity_issuer_subject ON rgx_oidc_identity (issuer, subject)`).Error
		},
	},
	{
		Version: 91,
		Name:    "audit_approval_action_hash_identity",
		Migrate: func(tx *gorm.DB) error {
			return tx.AutoMigrate(&model.AuditLog{})
		},
	},
	{
		Version: 92,
		Name:    "dataset_incremental_ledger",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.IncrementalLedger{}); err != nil {
				return err
			}
			return tx.Exec(`CREATE INDEX IF NOT EXISTS idx_incremental_ledger_document ON rgx_incremental_ledger (tenant_id, ragflow_document_id)`).Error
		},
	},
	{
		Version: 93,
		Name:    "citation_reference_snapshot",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.AutoMigrate(&model.CitationReference{}); err != nil {
				return err
			}
			return tx.Exec(`CREATE INDEX IF NOT EXISTS idx_citation_reference_request ON rgx_citation_reference (tenant_id, request_id, created_at)`).Error
		},
	},
}

func deduplicateModelProviderIdentities(tx *gorm.DB) error {
	dedupes := []struct {
		table     string
		partition string
	}{
		{"rgx_model_provider_instance", "tenant_id, provider_id, LOWER(TRIM(instance_name))"},
		{"rgx_model_provider_model", "tenant_id, instance_id, LOWER(TRIM(model_name))"},
	}
	for _, dedupe := range dedupes {
		statement := `
			DELETE FROM ` + dedupe.table + `
			WHERE id IN (
				SELECT id FROM (
					SELECT id, ROW_NUMBER() OVER (
						PARTITION BY ` + dedupe.partition + `
						ORDER BY created_at ASC, id ASC
					) AS duplicate_rank
					FROM ` + dedupe.table + `
				) duplicates
				WHERE duplicates.duplicate_rank > 1
			)`
		if err := tx.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

type uniqueNameRow struct {
	ID       string
	TenantID string
	Name     string
}

func trimDisplayNames(tx *gorm.DB) error {
	updates := [][2]string{
		{"rgx_tenant", "name"},
		{"rgx_user", "username"},
		{"rgx_user", "email"},
		{"rgx_dataset_link", "name"},
		{"rgx_agent", "title"},
		{"rgx_project", "name"},
		{"rgx_team", "name"},
		{"rgx_role", "name"},
	}
	for _, item := range updates {
		if err := tx.Exec(
			"UPDATE " + item[0] + " SET " + item[1] + " = TRIM(" + item[1] + ") WHERE " + item[1] + " <> TRIM(" + item[1] + ")",
		).Error; err != nil {
			return err
		}
	}
	return nil
}

func dedupeUniqueNames(tx *gorm.DB, table, primaryKey, tenantColumn, nameColumn string) error {
	query := "SELECT " + primaryKey + " AS id, "
	if tenantColumn == "" {
		query += "'' AS tenant_id, "
	} else {
		query += tenantColumn + " AS tenant_id, "
	}
	query += nameColumn + " AS name FROM " + table
	var rows []uniqueNameRow
	if err := tx.Raw(query).Scan(&rows).Error; err != nil {
		return err
	}

	used := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		used[uniqueNameKey(row.TenantID, row.Name)] = struct{}{}
	}
	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		key := uniqueNameKey(row.TenantID, row.Name)
		counts[key]++
		if counts[key] == 1 {
			continue
		}
		next, err := nextUniqueName(row.TenantID, row.Name, used)
		if err != nil {
			return err
		}
		if err := tx.Exec(
			"UPDATE "+table+" SET "+nameColumn+" = ? WHERE "+primaryKey+" = ?",
			next, row.ID,
		).Error; err != nil {
			return err
		}
		used[uniqueNameKey(row.TenantID, next)] = struct{}{}
	}
	return nil
}

func uniqueNameKey(tenantID, name string) string {
	return tenantID + "\x00" + strings.ToLower(name)
}

func nextUniqueName(tenantID, base string, used map[string]struct{}) (string, error) {
	for sequence := 2; sequence < 1_000_000; sequence++ {
		suffix := fmt.Sprintf(" (%d)", sequence)
		limit := 128 - utf8.RuneCountInString(suffix)
		runes := []rune(base)
		if len(runes) > limit {
			base = string(runes[:limit])
		}
		candidate := strings.TrimSpace(string(runes)) + suffix
		key := uniqueNameKey(tenantID, candidate)
		if _, exists := used[key]; !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("unable to generate a unique display name")
}

func createModelRoutePinGuards(tx *gorm.DB) error {
	if tx.Dialector.Name() == "postgres" {
		if err := tx.Exec(`CREATE OR REPLACE FUNCTION rgx_model_route_pin_pointer_guard() RETURNS trigger AS $$
			BEGIN
				IF NEW.current_pin_id <> '' AND NOT EXISTS (
					SELECT 1 FROM rgx_model_route_enterprise_pin
					WHERE tenant_id = NEW.tenant_id AND route_id = NEW.id
						AND pin_id = NEW.current_pin_id AND version = NEW.current_pin_version
				) THEN
					RAISE EXCEPTION 'model route pin pointer must reference an immutable pin';
				END IF;
				RETURN NEW;
			END;
		$$ LANGUAGE plpgsql`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE OR REPLACE FUNCTION rgx_model_route_pin_freeze() RETURNS trigger AS $$
			BEGIN
				RAISE EXCEPTION 'model route enterprise pins are immutable';
			END;
		$$ LANGUAGE plpgsql`).Error; err != nil {
			return err
		}
		statements := []string{
			`DROP TRIGGER IF EXISTS trg_model_route_pin_pointer_guard ON rgx_model_route;
			 CREATE TRIGGER trg_model_route_pin_pointer_guard BEFORE UPDATE ON rgx_model_route
			 FOR EACH ROW EXECUTE FUNCTION rgx_model_route_pin_pointer_guard()`,
			`DROP TRIGGER IF EXISTS trg_model_route_pin_guard ON rgx_model_route_enterprise_pin;
			 CREATE TRIGGER trg_model_route_pin_guard BEFORE UPDATE OR DELETE ON rgx_model_route_enterprise_pin
			 FOR EACH ROW EXECUTE FUNCTION rgx_model_route_pin_freeze()`,
		}
		for _, statement := range statements {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		return nil

	}

	statements := []string{
		`CREATE TRIGGER IF NOT EXISTS trg_model_route_pin_pointer_guard BEFORE UPDATE ON rgx_model_route
			WHEN NEW.current_pin_id <> '' AND NOT EXISTS (
				SELECT 1 FROM rgx_model_route_enterprise_pin
				WHERE tenant_id = NEW.tenant_id AND route_id = NEW.id
					AND pin_id = NEW.current_pin_id AND version = NEW.current_pin_version
			)
			BEGIN SELECT RAISE(ABORT, 'model route pin pointer must reference an immutable pin'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_model_route_pin_guard BEFORE UPDATE ON rgx_model_route_enterprise_pin
			BEGIN SELECT RAISE(ABORT, 'model route enterprise pins are immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_model_route_pin_delete_guard BEFORE DELETE ON rgx_model_route_enterprise_pin
			BEGIN SELECT RAISE(ABORT, 'model route enterprise pins are immutable'); END`,
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func createEnterpriseConnectionGuards(tx *gorm.DB) error {
	if tx.Dialector.Name() == "postgres" {
		functions := []string{
			`CREATE OR REPLACE FUNCTION rgx_enterprise_connection_pointer_guard() RETURNS trigger AS $$
				BEGIN
					IF NOT EXISTS (
						SELECT 1 FROM rgx_enterprise_connection_version
						WHERE connection_id = NEW.id AND version = NEW.current_connection_version
					) THEN
						RAISE EXCEPTION 'enterprise connection pointer must reference an immutable version';
					END IF;
					RETURN NEW;
				END;
			$$ LANGUAGE plpgsql`,
			`CREATE OR REPLACE FUNCTION rgx_enterprise_connection_version_freeze() RETURNS trigger AS $$
				BEGIN
					RAISE EXCEPTION 'enterprise connection versions are immutable';
				END;
			$$ LANGUAGE plpgsql`,
			`CREATE OR REPLACE FUNCTION rgx_enterprise_binding_pointer_guard() RETURNS trigger AS $$
				BEGIN
					IF NOT EXISTS (
						SELECT 1 FROM rgx_enterprise_connection_binding_version
						WHERE binding_id = NEW.binding_id AND version = NEW.current_binding_version
					) THEN
						RAISE EXCEPTION 'enterprise binding pointer must reference an immutable version';
					END IF;
					RETURN NEW;
				END;
			$$ LANGUAGE plpgsql`,
			`CREATE OR REPLACE FUNCTION rgx_enterprise_binding_version_freeze() RETURNS trigger AS $$
				BEGIN
					RAISE EXCEPTION 'enterprise binding versions are immutable';
				END;
			$$ LANGUAGE plpgsql`,
		}
		for _, statement := range functions {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		triggers := []string{
			`DROP TRIGGER IF EXISTS trg_enterprise_connection_pointer_guard ON rgx_enterprise_connection;
			 CREATE TRIGGER trg_enterprise_connection_pointer_guard BEFORE UPDATE ON rgx_enterprise_connection
			 FOR EACH ROW EXECUTE FUNCTION rgx_enterprise_connection_pointer_guard()`,
			`DROP TRIGGER IF EXISTS trg_enterprise_connection_version_guard ON rgx_enterprise_connection_version;
			 CREATE TRIGGER trg_enterprise_connection_version_guard BEFORE UPDATE OR DELETE ON rgx_enterprise_connection_version
			 FOR EACH ROW EXECUTE FUNCTION rgx_enterprise_connection_version_freeze()`,
			`DROP TRIGGER IF EXISTS trg_enterprise_binding_pointer_guard ON rgx_enterprise_connection_binding;
			 CREATE TRIGGER trg_enterprise_binding_pointer_guard BEFORE UPDATE ON rgx_enterprise_connection_binding
			 FOR EACH ROW EXECUTE FUNCTION rgx_enterprise_binding_pointer_guard()`,
			`DROP TRIGGER IF EXISTS trg_enterprise_binding_version_guard ON rgx_enterprise_connection_binding_version;
			 CREATE TRIGGER trg_enterprise_binding_version_guard BEFORE UPDATE OR DELETE ON rgx_enterprise_connection_binding_version
			 FOR EACH ROW EXECUTE FUNCTION rgx_enterprise_binding_version_freeze()`,
		}
		for _, statement := range triggers {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		return nil
	}

	statements := []string{
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_connection_pointer_guard BEFORE UPDATE ON rgx_enterprise_connection
			WHEN NOT EXISTS (
				SELECT 1 FROM rgx_enterprise_connection_version
				WHERE connection_id = NEW.id AND version = NEW.current_connection_version
			)
			BEGIN SELECT RAISE(ABORT, 'enterprise connection pointer must reference an immutable version'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_connection_version_guard BEFORE UPDATE ON rgx_enterprise_connection_version
			BEGIN SELECT RAISE(ABORT, 'enterprise connection versions are immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_connection_version_delete_guard BEFORE DELETE ON rgx_enterprise_connection_version
			BEGIN SELECT RAISE(ABORT, 'enterprise connection versions are immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_binding_pointer_guard BEFORE UPDATE ON rgx_enterprise_connection_binding
			WHEN NOT EXISTS (
				SELECT 1 FROM rgx_enterprise_connection_binding_version
				WHERE binding_id = NEW.binding_id AND version = NEW.current_binding_version
			)
			BEGIN SELECT RAISE(ABORT, 'enterprise binding pointer must reference an immutable version'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_binding_version_guard BEFORE UPDATE ON rgx_enterprise_connection_binding_version
			BEGIN SELECT RAISE(ABORT, 'enterprise binding versions are immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_binding_version_delete_guard BEFORE DELETE ON rgx_enterprise_connection_binding_version
			BEGIN SELECT RAISE(ABORT, 'enterprise binding versions are immutable'); END`,
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func createEnterpriseConnectionHealthCheckGuards(tx *gorm.DB) error {
	if tx.Dialector.Name() == "postgres" {
		if err := tx.Exec(`CREATE OR REPLACE FUNCTION rgx_enterprise_connection_health_check_freeze() RETURNS trigger AS $$
			BEGIN
				RAISE EXCEPTION 'enterprise connection health checks are immutable';
			END;
		$$ LANGUAGE plpgsql`).Error; err != nil {
			return err
		}
		statements := []string{
			`DROP TRIGGER IF EXISTS trg_enterprise_connection_health_check_guard ON rgx_enterprise_connection_health_check;
			 CREATE TRIGGER trg_enterprise_connection_health_check_guard BEFORE UPDATE OR DELETE ON rgx_enterprise_connection_health_check
			 FOR EACH ROW EXECUTE FUNCTION rgx_enterprise_connection_health_check_freeze()`,
		}
		for _, statement := range statements {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		return nil
	}

	statements := []string{
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_connection_health_check_guard BEFORE UPDATE ON rgx_enterprise_connection_health_check
			BEGIN SELECT RAISE(ABORT, 'enterprise connection health checks are immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_connection_health_check_delete_guard BEFORE DELETE ON rgx_enterprise_connection_health_check
			BEGIN SELECT RAISE(ABORT, 'enterprise connection health checks are immutable'); END`,
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func createEnterpriseConnectionReferenceContracts(tx *gorm.DB) error {
	if tx.Dialector.Name() == "postgres" {
		contractQueries := []struct {
			name string
			sql  string
		}{
			{"connection version", `SELECT COUNT(*) FROM rgx_enterprise_connection_version v LEFT JOIN rgx_enterprise_connection c ON c.id = v.connection_id WHERE c.id IS NULL`},
			{"binding", `SELECT COUNT(*) FROM rgx_enterprise_connection_binding b LEFT JOIN rgx_enterprise_connection c ON c.id = b.connection_id WHERE c.id IS NULL`},
			{"binding version", `SELECT COUNT(*) FROM rgx_enterprise_connection_binding_version bv LEFT JOIN rgx_enterprise_connection_binding b ON b.binding_id = bv.binding_id WHERE b.binding_id IS NULL`},
			{"binding tenant mismatch", `SELECT COUNT(*) FROM rgx_enterprise_connection_binding_version bv JOIN rgx_enterprise_connection_binding b ON b.binding_id = bv.binding_id WHERE b.tenant_id <> bv.tenant_id`},
			{"model route pin", `SELECT COUNT(*) FROM rgx_model_route_enterprise_pin p LEFT JOIN rgx_model_route r ON r.id = p.route_id AND r.tenant_id = p.tenant_id WHERE r.id IS NULL`},
		}
		for _, item := range contractQueries {
			var count int64
			if err := tx.Raw(item.sql).Scan(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return fmt.Errorf("%s violates the enterprise connection reference contract", item.name)
			}
		}

		indexes := []string{
			`CREATE UNIQUE INDEX IF NOT EXISTS uk_enterprise_binding_identity ON rgx_enterprise_connection_binding (binding_id, tenant_id)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS uk_enterprise_binding_version_identity ON rgx_enterprise_connection_binding_version (binding_id, version, tenant_id)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS uk_model_route_identity ON rgx_model_route (tenant_id, id)`,
			`CREATE UNIQUE INDEX IF NOT EXISTS uk_model_route_enterprise_pin_identity ON rgx_model_route_enterprise_pin (pin_id, version)`,
		}
		for _, statement := range indexes {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		foreignKeys := []string{
			`fk_enterprise_connection_version_connection|ALTER TABLE rgx_enterprise_connection_version ADD CONSTRAINT fk_enterprise_connection_version_connection FOREIGN KEY (connection_id) REFERENCES rgx_enterprise_connection(id) ON DELETE RESTRICT`,
			`fk_enterprise_binding_connection|ALTER TABLE rgx_enterprise_connection_binding ADD CONSTRAINT fk_enterprise_binding_connection FOREIGN KEY (connection_id) REFERENCES rgx_enterprise_connection(id) ON DELETE RESTRICT`,
			`fk_enterprise_binding_tenant|ALTER TABLE rgx_enterprise_connection_binding ADD CONSTRAINT fk_enterprise_binding_tenant FOREIGN KEY (tenant_id) REFERENCES rgx_tenant(id) ON DELETE RESTRICT`,
			`fk_enterprise_binding_version_binding|ALTER TABLE rgx_enterprise_connection_binding_version ADD CONSTRAINT fk_enterprise_binding_version_binding FOREIGN KEY (binding_id, tenant_id) REFERENCES rgx_enterprise_connection_binding(binding_id, tenant_id) ON DELETE RESTRICT`,
			`fk_enterprise_model_route_pin_route|ALTER TABLE rgx_model_route_enterprise_pin ADD CONSTRAINT fk_enterprise_model_route_pin_route FOREIGN KEY (tenant_id, route_id) REFERENCES rgx_model_route(tenant_id, id) ON DELETE RESTRICT`,
			`fk_enterprise_model_route_pin_connection|ALTER TABLE rgx_model_route_enterprise_pin ADD CONSTRAINT fk_enterprise_model_route_pin_connection FOREIGN KEY (connection_id, connection_version) REFERENCES rgx_enterprise_connection_version(connection_id, version) ON DELETE RESTRICT`,
			`fk_enterprise_model_route_pin_binding|ALTER TABLE rgx_model_route_enterprise_pin ADD CONSTRAINT fk_enterprise_model_route_pin_binding FOREIGN KEY (binding_id, binding_version) REFERENCES rgx_enterprise_connection_binding_version(binding_id, version) ON DELETE RESTRICT`,
		}
		for _, item := range foreignKeys {
			parts := strings.SplitN(item, "|", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid foreign key contract definition")
			}
			var count int64
			if err := tx.Raw(`SELECT COUNT(*) FROM pg_constraint WHERE conname = ?`, parts[0]).Scan(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				if err := tx.Exec(parts[1]).Error; err != nil {
					return err
				}
			}
		}
		return createEnterpriseSnapshotReferenceGuard(tx)
	}

	statements := []string{
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_connection_version_parent_guard BEFORE INSERT ON rgx_enterprise_connection_version
			WHEN NOT EXISTS (SELECT 1 FROM rgx_enterprise_connection WHERE id = NEW.connection_id)
			BEGIN SELECT RAISE(ABORT, 'enterprise connection version parent is required'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_binding_parent_guard BEFORE INSERT ON rgx_enterprise_connection_binding
			WHEN NOT EXISTS (SELECT 1 FROM rgx_enterprise_connection WHERE id = NEW.connection_id)
			BEGIN SELECT RAISE(ABORT, 'enterprise binding connection is required'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_binding_tenant_guard BEFORE INSERT ON rgx_enterprise_connection_binding
			WHEN NOT EXISTS (SELECT 1 FROM rgx_tenant WHERE id = NEW.tenant_id)
			BEGIN SELECT RAISE(ABORT, 'enterprise binding tenant is required'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_binding_version_parent_guard BEFORE INSERT ON rgx_enterprise_connection_binding_version
			WHEN NOT EXISTS (SELECT 1 FROM rgx_enterprise_connection_binding WHERE binding_id = NEW.binding_id AND tenant_id = NEW.tenant_id)
			BEGIN SELECT RAISE(ABORT, 'enterprise binding version parent is required'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_model_route_pin_parent_guard BEFORE INSERT ON rgx_model_route_enterprise_pin
			WHEN NOT EXISTS (SELECT 1 FROM rgx_model_route WHERE id = NEW.route_id AND tenant_id = NEW.tenant_id)
			BEGIN SELECT RAISE(ABORT, 'enterprise model route pin parent is required'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_model_route_pin_connection_guard BEFORE INSERT ON rgx_model_route_enterprise_pin
			WHEN NOT EXISTS (SELECT 1 FROM rgx_enterprise_connection_version WHERE connection_id = NEW.connection_id AND version = NEW.connection_version)
			BEGIN SELECT RAISE(ABORT, 'enterprise model route pin connection version is required'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_model_route_pin_binding_guard BEFORE INSERT ON rgx_model_route_enterprise_pin
			WHEN NOT EXISTS (SELECT 1 FROM rgx_enterprise_connection_binding_version WHERE binding_id = NEW.binding_id AND version = NEW.binding_version)
			BEGIN SELECT RAISE(ABORT, 'enterprise model route pin binding version is required'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_connection_delete_guard BEFORE DELETE ON rgx_enterprise_connection
			WHEN EXISTS (SELECT 1 FROM rgx_enterprise_connection_version WHERE connection_id = OLD.id)
			BEGIN SELECT RAISE(ABORT, 'enterprise connection versions must be removed first'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_binding_delete_guard BEFORE DELETE ON rgx_enterprise_connection_binding
			WHEN EXISTS (SELECT 1 FROM rgx_enterprise_connection_binding_version WHERE binding_id = OLD.binding_id)
			BEGIN SELECT RAISE(ABORT, 'enterprise binding versions must be removed first'); END`,
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return err
		}
	}
	return createEnterpriseSnapshotReferenceGuard(tx)
}

func createEnterpriseSnapshotReferenceGuard(tx *gorm.DB) error {
	if tx.Dialector.Name() == "postgres" {
		function := `CREATE OR REPLACE FUNCTION rgx_enterprise_snapshot_reference_guard() RETURNS trigger AS $$
			BEGIN
				IF NEW.model_route_pin_id <> '' AND NOT EXISTS (
					SELECT 1 FROM rgx_model_route_enterprise_pin
					WHERE pin_id = NEW.model_route_pin_id AND version = NEW.model_route_pin_version
				) THEN RAISE EXCEPTION 'snapshot model route pin reference is invalid'; END IF;
				IF NEW.enterprise_connection_id <> '' AND NOT EXISTS (
					SELECT 1 FROM rgx_enterprise_connection_version
					WHERE connection_id = NEW.enterprise_connection_id AND version = NEW.enterprise_connection_version
				) THEN RAISE EXCEPTION 'snapshot enterprise connection reference is invalid'; END IF;
				IF NEW.enterprise_binding_id <> '' AND NOT EXISTS (
					SELECT 1 FROM rgx_enterprise_connection_binding_version
					WHERE binding_id = NEW.enterprise_binding_id AND version = NEW.enterprise_binding_version
				) THEN RAISE EXCEPTION 'snapshot enterprise binding reference is invalid'; END IF;
				RETURN NEW;
			END;
		$$ LANGUAGE plpgsql`
		if err := tx.Exec(function).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DROP TRIGGER IF EXISTS trg_enterprise_snapshot_reference_guard ON rgx_execution_snapshot`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE TRIGGER trg_enterprise_snapshot_reference_guard BEFORE INSERT OR UPDATE ON rgx_execution_snapshot
			FOR EACH ROW EXECUTE FUNCTION rgx_enterprise_snapshot_reference_guard()`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DROP TRIGGER IF EXISTS trg_enterprise_evidence_reference_guard ON rgx_evidence_bundle`).Error; err != nil {
			return err
		}
		return tx.Exec(`CREATE TRIGGER trg_enterprise_evidence_reference_guard BEFORE INSERT OR UPDATE ON rgx_evidence_bundle
			FOR EACH ROW EXECUTE FUNCTION rgx_enterprise_snapshot_reference_guard()`).Error
	}

	statements := []string{
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_snapshot_pin_reference_guard BEFORE INSERT ON rgx_execution_snapshot
			WHEN NEW.model_route_pin_id <> '' AND NOT EXISTS (
				SELECT 1 FROM rgx_model_route_enterprise_pin WHERE pin_id = NEW.model_route_pin_id AND version = NEW.model_route_pin_version
			) BEGIN SELECT RAISE(ABORT, 'snapshot model route pin reference is invalid'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_snapshot_connection_reference_guard BEFORE INSERT ON rgx_execution_snapshot
			WHEN NEW.enterprise_connection_id <> '' AND NOT EXISTS (
				SELECT 1 FROM rgx_enterprise_connection_version WHERE connection_id = NEW.enterprise_connection_id AND version = NEW.enterprise_connection_version
			) BEGIN SELECT RAISE(ABORT, 'snapshot enterprise connection reference is invalid'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_snapshot_binding_reference_guard BEFORE INSERT ON rgx_execution_snapshot
			WHEN NEW.enterprise_binding_id <> '' AND NOT EXISTS (
				SELECT 1 FROM rgx_enterprise_connection_binding_version WHERE binding_id = NEW.enterprise_binding_id AND version = NEW.enterprise_binding_version
			) BEGIN SELECT RAISE(ABORT, 'snapshot enterprise binding reference is invalid'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_evidence_pin_reference_guard BEFORE INSERT ON rgx_evidence_bundle
			WHEN NEW.model_route_pin_id <> '' AND NOT EXISTS (
				SELECT 1 FROM rgx_model_route_enterprise_pin WHERE pin_id = NEW.model_route_pin_id AND version = NEW.model_route_pin_version
			) BEGIN SELECT RAISE(ABORT, 'evidence model route pin reference is invalid'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_evidence_connection_reference_guard BEFORE INSERT ON rgx_evidence_bundle
			WHEN NEW.enterprise_connection_id <> '' AND NOT EXISTS (
				SELECT 1 FROM rgx_enterprise_connection_version WHERE connection_id = NEW.enterprise_connection_id AND version = NEW.enterprise_connection_version
			) BEGIN SELECT RAISE(ABORT, 'evidence enterprise connection reference is invalid'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_enterprise_evidence_binding_reference_guard BEFORE INSERT ON rgx_evidence_bundle
			WHEN NEW.enterprise_binding_id <> '' AND NOT EXISTS (
				SELECT 1 FROM rgx_enterprise_connection_binding_version WHERE binding_id = NEW.enterprise_binding_id AND version = NEW.enterprise_binding_version
			) BEGIN SELECT RAISE(ABORT, 'evidence enterprise binding reference is invalid'); END`,
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func createTenantPlatformGuards(tx *gorm.DB) error {
	if tx.Dialector.Name() == "postgres" {
		if err := tx.Exec(`CREATE OR REPLACE FUNCTION rgx_tenant_type_guard() RETURNS trigger AS $$
			BEGIN
				IF OLD.type <> NEW.type THEN
					RAISE EXCEPTION 'tenant type is immutable';
				END IF;
				IF OLD.type = 'platform' AND (
					OLD.status IS DISTINCT FROM NEW.status OR
					OLD.id IS DISTINCT FROM NEW.id
				) THEN
					RAISE EXCEPTION 'platform tenant identity/status is immutable';
				END IF;
				RETURN NEW;
			END;
		$$ LANGUAGE plpgsql`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE OR REPLACE FUNCTION rgx_platform_tenant_delete_guard() RETURNS trigger AS $$
			BEGIN
				RAISE EXCEPTION 'platform tenant cannot be deleted';
			END;
		$$ LANGUAGE plpgsql`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DROP TRIGGER IF EXISTS trg_tenant_type_guard ON rgx_tenant`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`CREATE TRIGGER trg_tenant_type_guard BEFORE UPDATE ON rgx_tenant
			FOR EACH ROW EXECUTE FUNCTION rgx_tenant_type_guard()`).Error; err != nil {
			return err
		}
		if err := tx.Exec(`DROP TRIGGER IF EXISTS trg_platform_tenant_delete_guard ON rgx_tenant`).Error; err != nil {
			return err
		}
		return tx.Exec(`CREATE TRIGGER trg_platform_tenant_delete_guard BEFORE DELETE ON rgx_tenant
			FOR EACH ROW WHEN (OLD.type = 'platform') EXECUTE FUNCTION rgx_platform_tenant_delete_guard()`).Error
	}

	statements := []string{
		`CREATE TRIGGER IF NOT EXISTS trg_tenant_type_guard BEFORE UPDATE ON rgx_tenant
			WHEN OLD.type <> NEW.type OR (OLD.type = 'platform' AND (OLD.status <> NEW.status OR OLD.id <> NEW.id))
			BEGIN SELECT RAISE(ABORT, 'tenant/platform lifecycle guard failed'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_platform_tenant_delete_guard BEFORE DELETE ON rgx_tenant
			WHEN OLD.type = 'platform'
			BEGIN SELECT RAISE(ABORT, 'platform tenant cannot be deleted'); END`,
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

// createTenantRuntimeRowSecurity installs a defensive Platform-row guard only.
// The SELECT/INSERT/UPDATE policies intentionally allow all rows because the
// application tenant boundary is enforced in the service/repository layer.
// This is not a general per-tenant database isolation boundary; a future
// implementation must bind a runtime role/session GUC and add per-table
// policies before it may be documented as RLS tenant isolation.
func createTenantRuntimeRowSecurity(tx *gorm.DB) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	statements := []string{
		`ALTER TABLE rgx_tenant ENABLE ROW LEVEL SECURITY`,
		`ALTER TABLE rgx_tenant FORCE ROW LEVEL SECURITY`,
		`DROP POLICY IF EXISTS rgx_tenant_runtime_read ON rgx_tenant`,
		`DROP POLICY IF EXISTS rgx_tenant_runtime_insert ON rgx_tenant`,
		`DROP POLICY IF EXISTS rgx_tenant_runtime_update ON rgx_tenant`,
		`DROP POLICY IF EXISTS rgx_tenant_runtime_delete ON rgx_tenant`,
		`CREATE POLICY rgx_tenant_runtime_read ON rgx_tenant FOR SELECT TO PUBLIC USING (true)`,
		`CREATE POLICY rgx_tenant_runtime_insert ON rgx_tenant FOR INSERT TO PUBLIC WITH CHECK (true)`,
		`CREATE POLICY rgx_tenant_runtime_update ON rgx_tenant FOR UPDATE TO PUBLIC USING (true) WITH CHECK (true)`,
		`CREATE POLICY rgx_tenant_runtime_delete ON rgx_tenant FOR DELETE TO PUBLIC USING (type <> 'platform')`,
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}

func createReleaseGovernanceGuards(tx *gorm.DB) error {
	if tx.Dialector.Name() == "postgres" {
		statements := []string{
			`CREATE OR REPLACE FUNCTION rgx_release_candidate_freeze() RETURNS trigger AS $$
				BEGIN
					IF OLD.status <> 'DRAFT' AND (
						OLD.target_type IS DISTINCT FROM NEW.target_type OR
						OLD.target_id IS DISTINCT FROM NEW.target_id OR
						OLD.target_version IS DISTINCT FROM NEW.target_version OR
						OLD.candidate_manifest IS DISTINCT FROM NEW.candidate_manifest OR
						OLD.candidate_hash IS DISTINCT FROM NEW.candidate_hash OR
						OLD.hash_algorithm IS DISTINCT FROM NEW.hash_algorithm
					) THEN
						RAISE EXCEPTION 'release candidate evidence is immutable';
					END IF;
					RETURN NEW;
				END;
			$$ LANGUAGE plpgsql`,
			`CREATE OR REPLACE FUNCTION rgx_simple_evidence_freeze() RETURNS trigger AS $$
				BEGIN
					RAISE EXCEPTION 'evaluation evidence is immutable';
				END;
			$$ LANGUAGE plpgsql`,
			`CREATE OR REPLACE FUNCTION rgx_case_result_freeze() RETURNS trigger AS $$
				BEGIN
					IF EXISTS (SELECT 1 FROM rgx_evaluation_run WHERE id = OLD.run_id AND status IN ('COMPLETED','FAILED','CANCELLED')) THEN
						RAISE EXCEPTION 'case results are immutable after terminal run';
					END IF;
					IF TG_OP = 'DELETE' THEN
						RETURN OLD;
					END IF;
					RETURN NEW;
				END;
			$$ LANGUAGE plpgsql`,
			`CREATE OR REPLACE FUNCTION rgx_release_core_freeze() RETURNS trigger AS $$
				BEGIN
					IF OLD.release_candidate_id IS DISTINCT FROM NEW.release_candidate_id OR
						OLD.candidate_version IS DISTINCT FROM NEW.candidate_version OR
						OLD.snapshot_id IS DISTINCT FROM NEW.snapshot_id OR
						OLD.gate_decision_id IS DISTINCT FROM NEW.gate_decision_id OR
						OLD.environment IS DISTINCT FROM NEW.environment OR
						OLD.released_by IS DISTINCT FROM NEW.released_by OR
						OLD.rollback_baseline IS DISTINCT FROM NEW.rollback_baseline THEN
						RAISE EXCEPTION 'release core evidence is immutable';
					END IF;
					RETURN NEW;
				END;
			$$ LANGUAGE plpgsql`,
		}
		for _, statement := range statements {
			if err := tx.Exec(statement).Error; err != nil {
				return err
			}
		}
		triggers := [][]string{
			{"rgx_release_candidate", "rgx_release_candidate_freeze", "BEFORE UPDATE"},
			{"rgx_execution_snapshot", "rgx_simple_evidence_freeze", "BEFORE UPDATE OR DELETE"},
			{"rgx_evaluation_set_version", "rgx_simple_evidence_freeze", "BEFORE UPDATE OR DELETE"},
			{"rgx_evaluation_case_version", "rgx_simple_evidence_freeze", "BEFORE UPDATE OR DELETE"},
			{"rgx_evidence_bundle", "rgx_simple_evidence_freeze", "BEFORE UPDATE OR DELETE"},
			{"rgx_evaluation_case_result", "rgx_case_result_freeze", "BEFORE UPDATE OR DELETE"},
			{"rgx_release", "rgx_release_core_freeze", "BEFORE UPDATE"},
		}
		for _, trigger := range triggers {
			sql := "DROP TRIGGER IF EXISTS trg_" + trigger[0] + "_guard ON " + trigger[0] +
				"; CREATE TRIGGER trg_" + trigger[0] + "_guard " + trigger[2] +
				" ON " + trigger[0] + " FOR EACH ROW EXECUTE FUNCTION " + trigger[1] + "()"
			if err := tx.Exec(sql).Error; err != nil {
				return err
			}
		}
		return nil
	}

	statements := []string{
		`CREATE TRIGGER IF NOT EXISTS trg_release_candidate_guard BEFORE UPDATE ON rgx_release_candidate
			WHEN OLD.status <> 'DRAFT' AND (
				OLD.target_type IS NOT NEW.target_type OR OLD.target_id IS NOT NEW.target_id OR
				OLD.target_version IS NOT NEW.target_version OR OLD.candidate_manifest IS NOT NEW.candidate_manifest OR
				OLD.candidate_hash IS NOT NEW.candidate_hash OR OLD.hash_algorithm IS NOT NEW.hash_algorithm
			)
			BEGIN SELECT RAISE(ABORT, 'release candidate evidence is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_execution_snapshot_guard BEFORE UPDATE ON rgx_execution_snapshot
			BEGIN SELECT RAISE(ABORT, 'execution snapshot is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_evaluation_set_version_guard BEFORE UPDATE ON rgx_evaluation_set_version
			BEGIN SELECT RAISE(ABORT, 'evaluation set version is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_evaluation_case_version_guard BEFORE UPDATE ON rgx_evaluation_case_version
			BEGIN SELECT RAISE(ABORT, 'evaluation case version is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_evidence_bundle_guard BEFORE UPDATE ON rgx_evidence_bundle
			BEGIN SELECT RAISE(ABORT, 'evidence bundle is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_evaluation_case_result_guard BEFORE UPDATE ON rgx_evaluation_case_result
			WHEN (SELECT status FROM rgx_evaluation_run WHERE id = OLD.run_id) IN ('COMPLETED','FAILED','CANCELLED')
			BEGIN SELECT RAISE(ABORT, 'case results are immutable after terminal run'); END`,
		`CREATE TRIGGER IF NOT EXISTS trg_release_guard BEFORE UPDATE ON rgx_release
			WHEN OLD.release_candidate_id IS NOT NEW.release_candidate_id OR OLD.candidate_version IS NOT NEW.candidate_version OR
				OLD.snapshot_id IS NOT NEW.snapshot_id OR OLD.gate_decision_id IS NOT NEW.gate_decision_id OR
				OLD.environment IS NOT NEW.environment OR OLD.released_by IS NOT NEW.released_by OR
				OLD.rollback_baseline IS NOT NEW.rollback_baseline
			BEGIN SELECT RAISE(ABORT, 'release core evidence is immutable'); END`,
	}
	for _, statement := range statements {
		if err := tx.Exec(statement).Error; err != nil {
			return err
		}
	}
	if err := tx.Exec(`DROP TRIGGER IF EXISTS trg_evaluation_case_result_guard`).Error; err != nil {
		return err
	}
	if err := tx.Exec(`CREATE TRIGGER trg_evaluation_case_result_guard BEFORE UPDATE ON rgx_evaluation_case_result
		WHEN (SELECT status FROM rgx_evaluation_run WHERE id = OLD.run_id) IN ('COMPLETED','FAILED','CANCELLED')
		BEGIN SELECT RAISE(ABORT, 'case results are immutable after terminal run'); END`).Error; err != nil {
		return err
	}
	return tx.Exec(`CREATE TRIGGER trg_evaluation_case_result_delete_guard BEFORE DELETE ON rgx_evaluation_case_result
		WHEN (SELECT status FROM rgx_evaluation_run WHERE id = OLD.run_id) IN ('COMPLETED','FAILED','CANCELLED')
		BEGIN SELECT RAISE(ABORT, 'case results are immutable after terminal run'); END`).Error
}

// Migrate applies all pending migrations within transactions and records the
// applied version in rgx_schema_version. It is idempotent. PostgreSQL uses an
// advisory lock so simultaneous replicas cannot run DDL concurrently.
func Migrate(db *gorm.DB) error {
	unlock, err := lockMigrations(db)
	if err != nil {
		return err
	}
	defer unlock()

	if err := db.AutoMigrate(&model.SchemaVersion{}); err != nil {
		return fmt.Errorf("create schema version table: %w", err)
	}

	for _, m := range migrations {
		var count int64
		if err := db.Model(&model.SchemaVersion{}).
			Where("version = ?", m.Version).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			if err := m.Migrate(tx); err != nil {
				return err
			}
			return tx.Create(&model.SchemaVersion{
				Version:   m.Version,
				AppliedAt: time.Now().UTC(),
			}).Error
		})
		if err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.Version, m.Name, err)
		}
	}
	var platformCount int64
	if err := db.Model(&model.Tenant{}).
		Where("type = ?", model.TenantTypePlatform).
		Count(&platformCount).Error; err != nil {
		return err
	}
	if platformCount != 1 {
		return fmt.Errorf("expected exactly one platform tenant, got %d", platformCount)
	}
	return nil
}

// LatestMigrationVersion returns the version encoded by the frozen migration list.
func LatestMigrationVersion() int64 {
	return int64(migrations[len(migrations)-1].Version)
}

func lockMigrations(db *gorm.DB) (func(), error) {
	if db.Dialector.Name() != "postgres" {
		return func() {}, nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		return nil, fmt.Errorf("acquire migration connection: %w", err)
	}
	if _, err := conn.ExecContext(context.Background(), "SELECT pg_advisory_lock(hashtext('ragflow_x_schema_migrations'))"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("acquire migration lock: %w", err)
	}
	return func() {
		defer conn.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(ctx, "SELECT pg_advisory_unlock(hashtext('ragflow_x_schema_migrations'))")
	}, nil
}

// PermissionGrant is one row of the canonical built-in RBAC matrix.
type PermissionGrant struct {
	Role     string
	Action   string
	Resource string
}

// BuiltinPermissionMatrix is the single source of truth for built-in role
// grants. Keep in sync with the permission matrix in doc/11_权限控制规范.md.
func BuiltinPermissionMatrix() []PermissionGrant {
	return []PermissionGrant{
		// Platform governance is explicitly enumerated. Platform Admin must
		// never become a wildcard superuser permission.
		{model.RolePlatformAdmin, "governance.read", "tenant"},
		{model.RolePlatformAdmin, "governance.manage", "tenant"},
		{model.RolePlatformAdmin, "governance.read_sensitive", "tenant"},
		{model.RolePlatformAdmin, "tenant.resource.manage", "tenant"},
		{model.RolePlatformAdmin, "tenant.resource.delete", "tenant"},
		{model.RolePlatformAdmin, "tenant.connection.manage", "tenant"},
		{model.RolePlatformAdmin, "tenant.connection.bind", "tenant"},
		{model.RolePlatformAdmin, "tenant.connection.unbind", "tenant"},
		{model.RolePlatformAdmin, "tenant.connection.rotate_secret", "tenant"},
		{model.RolePlatformAdmin, "tenant.audit.read", "tenant"},
		{model.RolePlatformAdmin, "tenant.usage.read", "tenant"},
		{model.RolePlatformAdmin, "read", "system"},
		{model.RolePlatformAdmin, "manage", "system"},
		{model.RolePlatformAdmin, "read", "tenant"},
		{model.RolePlatformAdmin, "manage", "tenant"},
		{model.RolePlatformAdmin, "read", "role"},
		{model.RolePlatformAdmin, "manage", "role"},
		{model.RolePlatformAdmin, "read", "branding"},
		{model.RolePlatformAdmin, "manage", "branding"},
		{model.RolePlatformAdmin, "read", "user"},
		{model.RolePlatformAdmin, "manage", "user"},
		{model.RolePlatformAdmin, "read", "team"},
		{model.RolePlatformAdmin, "manage", "team"},
		{model.RolePlatformAdmin, "read", "project"},
		{model.RolePlatformAdmin, "manage", "project"},
		{model.RolePlatformAdmin, "read", "dataset"},
		{model.RolePlatformAdmin, "manage", "dataset"},
		{model.RolePlatformAdmin, "read", "dataset-export"},
		{model.RolePlatformAdmin, "read", "document"},
		{model.RolePlatformAdmin, "append", "document"},
		{model.RolePlatformAdmin, "delete:own", "document"},
		{model.RolePlatformAdmin, "execute", "document"},
		{model.RolePlatformAdmin, "read", "task"},
		{model.RolePlatformAdmin, "manage", "task"},
		{model.RolePlatformAdmin, "execute", "task"},
		{model.RolePlatformAdmin, "read", "model-provider"},
		{model.RolePlatformAdmin, "manage", "model-provider"},
		{model.RolePlatformAdmin, "execute", "model-provider"},
		{model.RolePlatformAdmin, "read", "enterprise-connection"},
		{model.RolePlatformAdmin, "test", "enterprise-connection"},
		{model.RolePlatformAdmin, "manage", "enterprise-connection"},
		{model.RolePlatformAdmin, "read", "model-route"},
		{model.RolePlatformAdmin, "manage", "model-route"},
		{model.RolePlatformAdmin, "read", "api-key"},
		{model.RolePlatformAdmin, "manage", "api-key"},
		{model.RolePlatformAdmin, "read", "usage"},
		{model.RolePlatformAdmin, "read", "usage-export"},
		{model.RolePlatformAdmin, "read", "audit"},
		{model.RolePlatformAdmin, "manage", "audit"},
		{model.RolePlatformAdmin, "read", "audit-anchor"},
		{model.RolePlatformAdmin, "read", "dashboard"},
		{model.RolePlatformAdmin, "read", "knowledge-ops"},
		{model.RolePlatformAdmin, "manage", "knowledge-ops"},
		{model.RolePlatformAdmin, "read", "assistant"},
		{model.RolePlatformAdmin, "manage", "assistant"},
		{model.RolePlatformAdmin, "execute", "assistant"},
		{model.RolePlatformAdmin, "read", "system-health"},
		{model.RolePlatformAdmin, "read", "chat"},
		{model.RolePlatformAdmin, "manage", "chat"},
		{model.RolePlatformAdmin, "execute", "chat"},
		{model.RolePlatformAdmin, "read", "search-app"},
		{model.RolePlatformAdmin, "manage", "search-app"},
		{model.RolePlatformAdmin, "execute", "search-app"},
		{model.RolePlatformAdmin, "read", "scenario-template"},
		{model.RolePlatformAdmin, "manage", "scenario-template"},
		{model.RolePlatformAdmin, "read", "prompt-policy"},
		{model.RolePlatformAdmin, "manage", "prompt-policy"},
		{model.RolePlatformAdmin, "read", "knowledge-lifecycle"},
		{model.RolePlatformAdmin, "manage", "knowledge-lifecycle"},
		{model.RolePlatformAdmin, "read", "eval-set"},
		{model.RolePlatformAdmin, "manage", "eval-set"},
		{model.RolePlatformAdmin, "read", "release-governance"},
		{model.RolePlatformAdmin, "manage", "release-governance"},
		{model.RolePlatformAdmin, "read", "alert"},
		{model.RolePlatformAdmin, "manage", "alert"},
		{model.RolePlatformAdmin, "read", "memory"},
		{model.RolePlatformAdmin, "manage", "memory"},
		{model.RolePlatformAdmin, "execute", "memory"},
		{model.RolePlatformAdmin, "read", "agent"},
		{model.RolePlatformAdmin, "manage", "agent"},
		{model.RolePlatformAdmin, "execute", "agent"},
		{model.RolePlatformAdmin, "session:create", "agent"},
		{model.RolePlatformAdmin, "read", "approval"},
		{model.RolePlatformAdmin, "execute", "approval"},
		{model.RolePlatformAdmin, "manage", "approval"},
		{model.RolePlatformAdmin, "read", "approval-policy"},
		{model.RolePlatformAdmin, "manage", "approval-policy"},
		// system (platform admins only)
		{model.RolePlatformAdmin, "read", "system"},
		{model.RolePlatformAdmin, "manage", "system"},
		{model.RolePlatformAdmin, "read", "ragflow-sync"},
		{model.RolePlatformAdmin, "manage", "ragflow-sync"},
		// tenant_admin
		{model.RoleTenantAdmin, "read", "tenant"},
		{model.RoleTenantAdmin, "read", "branding"},
		{model.RoleTenantAdmin, "manage", "branding"},
		{model.RoleTenantAdmin, "read", "user"},
		{model.RoleTenantAdmin, "manage", "user"},
		{model.RoleTenantAdmin, "read", "role"},
		{model.RoleTenantAdmin, "manage", "role"},
		{model.RoleTenantAdmin, "read", "team"},
		{model.RoleTenantAdmin, "manage", "team"},
		{model.RoleTenantAdmin, "read", "project"},
		{model.RoleTenantAdmin, "manage", "project"},
		{model.RoleTenantAdmin, "read", "dataset"},
		{model.RoleTenantAdmin, "manage", "dataset"},
		{model.RoleTenantAdmin, "read", "dataset-export"},
		{model.RoleTenantAdmin, "read", "document"},
		{model.RoleTenantAdmin, "append", "document"},
		{model.RoleTenantAdmin, "delete:own", "document"},
		{model.RoleTenantAdmin, "execute", "document"},
		{model.RoleTenantAdmin, "read", "task"},
		{model.RoleTenantAdmin, "execute", "task"},
		{model.RoleTenantAdmin, "manage", "task"},
		{model.RoleTenantAdmin, "read", "model-provider"},
		{model.RoleTenantAdmin, "manage", "model-provider"},
		{model.RoleTenantAdmin, "execute", "model-provider"},
		{model.RoleTenantAdmin, "read", "enterprise-connection"},
		{model.RoleTenantAdmin, "test", "enterprise-connection"},
		{model.RoleTenantAdmin, "read", "model-route"},
		{model.RoleTenantAdmin, "manage", "model-route"},
		{model.RoleTenantAdmin, "read", "api-key"},
		{model.RoleTenantAdmin, "manage", "api-key"},
		{model.RoleTenantAdmin, "execute", "chat"},
		{model.RoleTenantAdmin, "read", "usage"},
		{model.RoleTenantAdmin, "read", "usage-export"},
		{model.RoleTenantAdmin, "read", "audit"},
		{model.RoleTenantAdmin, "read", "audit-anchor"},
		{model.RoleTenantAdmin, "read", "dashboard"},
		{model.RoleTenantAdmin, "read", "assistant"},
		{model.RoleTenantAdmin, "execute", "assistant"},
		{model.RoleTenantAdmin, "manage", "assistant"},
		{model.RoleTenantAdmin, "read", "system-health"},
		{model.RoleTenantAdmin, "read", "knowledge-ops"},
		{model.RoleTenantAdmin, "manage", "knowledge-ops"},
		{model.RoleTenantAdmin, "read", "chat"},
		{model.RoleTenantAdmin, "manage", "chat"},
		{model.RoleTenantAdmin, "execute", "search-app"},
		{model.RoleTenantAdmin, "read", "search-app"},
		{model.RoleTenantAdmin, "manage", "search-app"},
		{model.RoleTenantAdmin, "read", "scenario-template"},
		{model.RoleTenantAdmin, "manage", "scenario-template"},
		{model.RoleTenantAdmin, "read", "prompt-policy"},
		{model.RoleTenantAdmin, "manage", "prompt-policy"},
		{model.RoleTenantAdmin, "read", "knowledge-lifecycle"},
		{model.RoleTenantAdmin, "manage", "knowledge-lifecycle"},
		{model.RoleTenantAdmin, "read", "eval-set"},
		{model.RoleTenantAdmin, "manage", "eval-set"},
		{model.RoleTenantAdmin, "read", "release-governance"},
		{model.RoleTenantAdmin, "manage", "release-governance"},
		{model.RoleTenantAdmin, "read", "alert"},
		{model.RoleTenantAdmin, "manage", "alert"},
		// operator
		{model.RoleOperator, "read", "user"},
		{model.RoleOperator, "read", "team"},
		{model.RoleOperator, "read", "project"},
		{model.RoleOperator, "read", "dataset"},
		{model.RoleOperator, "read", "document"},
		{model.RoleOperator, "append", "document"},
		{model.RoleOperator, "delete:own", "document"},
		{model.RoleOperator, "execute", "document"},
		{model.RoleOperator, "read", "task"},
		{model.RoleOperator, "execute", "task"},
		{model.RoleOperator, "manage", "task"},
		{model.RoleOperator, "execute", "chat"},
		{model.RoleOperator, "read", "chat"},
		{model.RoleOperator, "execute", "search-app"},
		{model.RoleOperator, "read", "assistant"},
		{model.RoleOperator, "execute", "assistant"},
		{model.RoleOperator, "read", "search-app"},
		{model.RoleOperator, "read", "usage"},
		{model.RoleOperator, "read", "dashboard"},
		{model.RoleOperator, "read", "knowledge-ops"},
		{model.RoleOperator, "manage", "knowledge-ops"},
		{model.RoleOperator, "read", "scenario-template"},
		{model.RoleOperator, "read", "prompt-policy"},
		{model.RoleOperator, "manage", "prompt-policy"},
		{model.RoleOperator, "read", "knowledge-lifecycle"},
		{model.RoleOperator, "read", "eval-set"},
		{model.RoleOperator, "manage", "eval-set"},
		{model.RoleOperator, "read", "release-governance"},
		{model.RoleOperator, "read", "alert"},
		// business_user
		{model.RoleBusinessUser, "read", "user"},
		{model.RoleBusinessUser, "read", "dataset"},
		{model.RoleBusinessUser, "read", "document"},
		{model.RoleBusinessUser, "append", "document"},
		{model.RoleBusinessUser, "delete:own", "document"},
		{model.RoleBusinessUser, "read", "assistant"},
		{model.RoleBusinessUser, "execute", "assistant"},
		{model.RoleBusinessUser, "read", "chat"},
		{model.RoleBusinessUser, "execute", "chat"},
		{model.RoleBusinessUser, "read", "search-app"},
		{model.RoleBusinessUser, "execute", "search-app"},
		// viewer
		{model.RoleViewer, "read", "user"},
		{model.RoleViewer, "read", "team"},
		{model.RoleViewer, "read", "project"},
		{model.RoleViewer, "read", "dataset"},
		{model.RoleViewer, "read", "document"},
		{model.RoleViewer, "read", "task"},
		{model.RoleViewer, "read", "usage"},
		{model.RoleViewer, "read", "dashboard"},
		{model.RoleViewer, "read", "knowledge-ops"},
		{model.RoleViewer, "read", "chat"},
		{model.RoleViewer, "read", "search-app"},
		{model.RoleViewer, "read", "assistant"},
		{model.RoleViewer, "read", "scenario-template"},
		{model.RoleViewer, "read", "prompt-policy"},
		{model.RoleViewer, "read", "knowledge-lifecycle"},
		{model.RoleViewer, "read", "eval-set"},
		{model.RoleViewer, "read", "release-governance"},
		// memory
		{model.RoleTenantAdmin, "execute", "memory"},
		{model.RoleTenantAdmin, "read", "memory"},
		{model.RoleTenantAdmin, "manage", "memory"},
		{model.RoleOperator, "execute", "memory"},
		{model.RoleOperator, "read", "memory"},
		{model.RoleOperator, "manage", "memory"},
		{model.RoleViewer, "read", "memory"},
		{model.RoleBusinessUser, "read", "memory"},
		{model.RoleBusinessUser, "execute", "memory"},
		// agent
		{model.RoleTenantAdmin, "execute", "agent"},
		{model.RoleTenantAdmin, "read", "agent"},
		{model.RoleTenantAdmin, "manage", "agent"},
		{model.RoleTenantAdmin, "session:create", "agent"},
		{model.RoleOperator, "execute", "agent"},
		{model.RoleOperator, "read", "agent"},
		{model.RoleOperator, "manage", "agent"},
		{model.RoleOperator, "session:create", "agent"},
		{model.RoleViewer, "read", "agent"},
		{model.RoleBusinessUser, "execute", "agent"},
		{model.RoleBusinessUser, "read", "agent"},
		{model.RoleBusinessUser, "session:create", "agent"},
		// team_admin
		{model.RoleTeamAdmin, "read", "team"},
		{model.RoleTeamAdmin, "manage", "team"},
		{model.RoleTeamAdmin, "read", "project"},
		{model.RoleTeamAdmin, "manage", "project"},
		{model.RoleTeamAdmin, "read", "dataset"},
		{model.RoleTeamAdmin, "manage", "dataset"},
		{model.RoleTeamAdmin, "read", "document"},
		{model.RoleTeamAdmin, "append", "document"},
		{model.RoleTeamAdmin, "delete:own", "document"},
		{model.RoleTeamAdmin, "execute", "document"},
		{model.RoleTeamAdmin, "read", "user"},
		{model.RoleTeamAdmin, "read", "scenario-template"},
		{model.RoleTeamAdmin, "manage", "scenario-template"},
		{model.RoleTeamAdmin, "read", "prompt-policy"},
		{model.RoleTeamAdmin, "read", "knowledge-lifecycle"},
		{model.RoleTeamAdmin, "read", "eval-set"},
		{model.RoleTeamAdmin, "manage", "eval-set"},
		// approval
		{model.RoleTenantAdmin, "read", "approval"},
		{model.RoleTenantAdmin, "execute", "approval"},
		{model.RoleTenantAdmin, "manage", "approval"},
		{model.RoleTenantAdmin, "read", "approval-policy"},
		{model.RoleTenantAdmin, "manage", "approval-policy"},
		{model.RoleOperator, "read", "approval"},
		{model.RoleOperator, "execute", "approval"},
		{model.RoleViewer, "read", "approval"},
	}
}

// seedDefaultRBAC installs the built-in roles and reconciles the canonical
// permission matrix on every run. Built-in role grants are immutable at
// runtime, so rows are replaced wholesale to keep existing databases in sync.
func seedDefaultRBAC(tx *gorm.DB) error {
	roles := []model.Role{
		{ID: model.RolePlatformAdmin, Name: "平台管理员", Scope: model.RoleScopePlatform, Description: "平台级全量管理"},
		{ID: model.RoleTenantAdmin, Name: "工作区管理员", Scope: model.RoleScopeTenant, Description: "工作区内管理"},
		{ID: model.RoleOperator, Name: "内容运营员", Scope: model.RoleScopeTenant, Description: "工作区内内容与业务运营"},
		{ID: model.RoleViewer, Name: "只读", Scope: model.RoleScopeTenant, Description: "工作区内只读"},
		{ID: model.RoleTeamAdmin, Name: "团队管理员", Scope: model.RoleScopeTenant, Description: "负责团队的项目/数据集管理"},
		{ID: model.RoleBusinessUser, Name: "业务使用者", Scope: model.RoleScopeTenant, Description: "使用助手并维护数据集文档"},
	}
	for _, r := range roles {
		var cnt int64
		if err := tx.Model(&model.Role{}).Where("id = ?", r.ID).Count(&cnt).Error; err != nil {
			return err
		}
		if cnt == 0 {
			if err := tx.Create(&r).Error; err != nil {
				return err
			}
		}
	}
	return reconcileBuiltinPermissions(tx)
}

// reconcileBuiltinPermissions replaces the built-in roles' grants with the
// canonical matrix. Because built-in roles are immutable, any stale or
// coarse wildcard grants from earlier schema versions are dropped.
// PermissionCatalog aggregates the built-in permission matrix into the set of
// (resource, action) pairs a role may be granted. It drives the role permission
// management UI so it always stays in sync with the built-in grants.
type ResourceActions struct {
	Resource string   `json:"resource"`
	Actions  []string `json:"actions"`
}

func PermissionCatalog() []ResourceActions {
	actions := map[string]map[string]bool{}
	order := []string{}
	for _, g := range BuiltinPermissionMatrix() {
		if g.Resource == "*" {
			continue
		}
		if _, ok := actions[g.Resource]; !ok {
			actions[g.Resource] = map[string]bool{}
			order = append(order, g.Resource)
		}
		actions[g.Resource][g.Action] = true
	}
	out := make([]ResourceActions, 0, len(order))
	for _, r := range order {
		acts := []string{}
		for _, a := range []string{
			"read", "manage", "execute", "session:create", "governance.read",
			"append", "delete:own",
			"test", "governance.read_sensitive", "governance.manage", "tenant.resource.manage",
			"tenant.resource.delete", "tenant.connection.manage", "tenant.connection.bind",
			"tenant.connection.unbind", "tenant.connection.rotate_secret",
			"tenant.audit.read", "tenant.usage.read",
		} {
			if actions[r][a] {
				acts = append(acts, a)
			}
		}
		if len(acts) == 0 {
			continue
		}
		out = append(out, ResourceActions{Resource: r, Actions: acts})
	}
	return out
}

func reconcileBuiltinPermissions(tx *gorm.DB) error {
	builtin := []string{model.RolePlatformAdmin, model.RoleTenantAdmin, model.RoleOperator, model.RoleViewer, model.RoleTeamAdmin, model.RoleBusinessUser}
	if err := tx.Where("role_id IN ?", builtin).Delete(&model.Permission{}).Error; err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, p := range BuiltinPermissionMatrix() {
		key := p.Role + "|" + p.Action + "|" + p.Resource
		if seen[key] {
			continue
		}
		seen[key] = true
		perm := model.Permission{ID: id.New(), RoleID: p.Role, Action: p.Action, Resource: p.Resource, Effect: model.PermissionEffectAllow}
		if err := tx.Create(&perm).Error; err != nil {
			return err
		}
	}
	return nil
}

// seedTeamAdminOwners grants the team_admin role to every user who owns at
// least one team, backfilling pre-existing teams created before the role.
func seedTeamAdminOwners(tx *gorm.DB) error {
	var ids []string
	if err := tx.Model(&model.Team{}).Where("owner_id <> '' AND owner_id IS NOT NULL").Distinct().Pluck("owner_id", &ids).Error; err != nil {
		return err
	}
	for _, id := range ids {
		var n int64
		if err := tx.Model(&model.UserRole{}).Where("user_id = ? AND role_id = ?", id, model.RoleTeamAdmin).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			if err := tx.Create(&model.UserRole{UserID: id, RoleID: model.RoleTeamAdmin}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
