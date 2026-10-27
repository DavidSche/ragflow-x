package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// BuiltinScenarioTemplate is the single content source for the shipped
// scenario templates (doc/228 §3.1 T7.0). The web wizard reads the same rows
// through the catalog API; there is no static frontend copy anymore.
type BuiltinScenarioTemplate struct {
	Key         string
	Name        string
	Description string
	AppTypes    []string
	Payload     ScenarioTemplatePayload
}

// builtinScenarioBootstrapActor is the audit actor recorded for system-seeded
// templates. Tenant users never authenticate as this principal.
const builtinScenarioBootstrapActor = "system"

var builtinScenarioTemplates = []BuiltinScenarioTemplate{
	{
		Key:         "enterprise-qa",
		Name:        "企业知识问答",
		Description: "员工日常问题统一入口，先给结论、再给依据。",
		AppTypes:    []string{"chat", "search"},
		Payload: ScenarioTemplatePayload{
			PromptPresetID:     "enterprise-qa",
			ParameterProfileID: "balanced",
			DatasetSuggestions: []string{"制度流程", "FAQ", "项目知识包"},
			SampleQuestions:    []string{"报销流程是什么？", "新员工第 1 周要完成什么？"},
			EvaluationQuestions: []string{
				"报销标准和截止时间是什么？",
				"报销流程需要哪些审批人？",
			},
			PermissionAdvice: "全员可访问；管理配置仅限实施管理员。",
			PublishChecklist: []string{"绑定最新制度库", "试跑 10 个高频问题", "开启反馈"},
			ExpectedOutcome:  "高频业务问题优先解决，引用率和采纳率可见。",
			Translations: map[string]ScenarioTemplateTranslation{
				"en": {
					Name:               "Enterprise knowledge Q&A",
					Description:        "One entry point for everyday employee questions: conclusion first, evidence after.",
					DatasetSuggestions: []string{"Policies", "FAQ", "Project knowledge"},
					SampleQuestions:    []string{"What is the expense reimbursement process?", "What must a new hire complete in week 1?"},
					EvaluationQuestions: []string{
						"What are the reimbursement limits and cut-off times?",
						"Which approvers does the reimbursement flow require?",
					},
					PermissionAdvice: "Available to everyone; configuration is limited to implementation admins.",
					PublishChecklist: []string{"Bind the current policy set", "Trial-run 10 frequent questions", "Enable feedback"},
					ExpectedOutcome:  "Frequent questions resolve first, with visible citation and adoption rates.",
				},
			},
		},
	},
	{
		Key:         "policy",
		Name:        "制度与员工助手",
		Description: "人事、行政、考勤、报销等制度口径统一。",
		AppTypes:    []string{"chat", "search"},
		Payload: ScenarioTemplatePayload{
			PromptPresetID:     "policy",
			ParameterProfileID: "safe",
			DatasetSuggestions: []string{"员工手册", "制度文件", "政策问答"},
			SampleQuestions:    []string{"年假如何申请？", "加班调休的有效期是多久？"},
			EvaluationQuestions: []string{
				"试用期考核标准是什么？",
				"请假审批链路是谁？",
			},
			PermissionAdvice: "全员可访问；工资类制度按团队权限单独隔离。",
			PublishChecklist: []string{"确认制度版本生效时间", "标记敏感薪资文档", "开启引用展示"},
			ExpectedOutcome:  "制度问题不再依赖人工反复解释。",
			Translations: map[string]ScenarioTemplateTranslation{
				"en": {
					Name:               "Policy and employee assistant",
					Description:        "One consistent answer for HR, admin, attendance and expense policies.",
					DatasetSuggestions: []string{"Employee handbook", "Policy documents", "Policy FAQ"},
					SampleQuestions:    []string{"How do I request annual leave?", "How long does overtime time-off stay valid?"},
					EvaluationQuestions: []string{
						"What are the probation review criteria?",
						"Who approves a leave request?",
					},
					PermissionAdvice: "Available to everyone; salary-related policies are isolated per team.",
					PublishChecklist: []string{"Confirm the effective policy version", "Mark sensitive salary documents", "Show citations"},
					ExpectedOutcome:  "Policy questions stop depending on repeated manual explanation.",
				},
			},
		},
	},
	{
		Key:         "customer-service",
		Name:        "客服 FAQ",
		Description: "客服可复制回复、口径统一、复杂问题转人工。",
		AppTypes:    []string{"chat", "search", "agent"},
		Payload: ScenarioTemplatePayload{
			PromptPresetID:     "customer-service",
			ParameterProfileID: "balanced",
			DatasetSuggestions: []string{"客服 FAQ", "工单案例", "服务政策"},
			SampleQuestions:    []string{"订单未收到怎么办？", "发票丢失如何补开？"},
			EvaluationQuestions: []string{
				"7 天无理由退货条件是什么？",
				"投诉升级给哪个负责人？",
			},
			PermissionAdvice: "客服团队可访问；管理配置仅限服务负责人。",
			PublishChecklist: []string{"绑定 FAQ 和政策", "验证无答案转人工", "统计采纳率"},
			ExpectedOutcome:  "一线客服回复速度和口径一致性提升。",
			Translations: map[string]ScenarioTemplateTranslation{
				"en": {
					Name:               "Customer service FAQ",
					Description:        "Copy-ready replies, one service voice, and escalation for complex cases.",
					DatasetSuggestions: []string{"Support FAQ", "Ticket cases", "Service policies"},
					SampleQuestions:    []string{"What if the order never arrived?", "How do I reissue a lost invoice?"},
					EvaluationQuestions: []string{
						"What are the conditions for a 7-day no-reason return?",
						"Who owns an escalated complaint?",
					},
					PermissionAdvice: "Available to the support team; configuration is limited to the service owner.",
					PublishChecklist: []string{"Bind FAQ and policies", "Verify the hand-off path when no answer exists", "Track adoption"},
					ExpectedOutcome:  "Faster first-line replies with a consistent service voice.",
				},
			},
		},
	},
	{
		Key:         "product-manual",
		Name:        "产品与设备手册",
		Description: "按型号检索操作、故障、维护和配件信息。",
		AppTypes:    []string{"search", "agent"},
		Payload: ScenarioTemplatePayload{
			PromptPresetID:     "product-manual",
			ParameterProfileID: "safe",
			DatasetSuggestions: []string{"产品手册", "维修手册", "配件清单"},
			SampleQuestions:    []string{"设备报 E03 如何处理？", "泵体滤芯多久更换？"},
			EvaluationQuestions: []string{
				"E03 的官方处理步骤是什么？",
				"更换滤芯需要注意哪些安全项？",
			},
			PermissionAdvice: "技术支持、售后和现场工程师可访问。",
			PublishChecklist: []string{"确认型号目录", "验证故障码检索", "补充安全警告"},
			ExpectedOutcome:  "现场工程师减少翻手册时间并降低误操作。",
			Translations: map[string]ScenarioTemplateTranslation{
				"en": {
					Name:               "Product and equipment manual",
					Description:        "Look up operation, fault, maintenance and spare-part details by model.",
					DatasetSuggestions: []string{"Product manuals", "Service manuals", "Spare parts"},
					SampleQuestions:    []string{"How do I handle error E03?", "How often should the pump filter be replaced?"},
					EvaluationQuestions: []string{
						"What is the official E03 resolution procedure?",
						"Which safety steps apply when replacing the filter?",
					},
					PermissionAdvice: "Available to technical support, after-sales and field engineers.",
					PublishChecklist: []string{"Confirm the model catalog", "Verify fault-code retrieval", "Add safety warnings"},
					ExpectedOutcome:  "Field engineers spend less time in manuals and make fewer mistakes.",
				},
			},
		},
	},
	{
		Key:         "sales-support",
		Name:        "销售支持",
		Description: "统一产品卖点、案例、报价边界和异议处理。",
		AppTypes:    []string{"chat", "agent"},
		Payload: ScenarioTemplatePayload{
			PromptPresetID:     "sales-support",
			ParameterProfileID: "balanced",
			DatasetSuggestions: []string{"产品资料", "客户案例", "销售话术"},
			SampleQuestions:    []string{"向制造业客户推荐哪个方案？", "常见异议如何回应？"},
			EvaluationQuestions: []string{
				"产品的三个核心卖点是什么？",
				"哪些折扣需要审批？",
			},
			PermissionAdvice: "销售团队可访问；成本和底价文档单独授权。",
			PublishChecklist: []string{"隔离成本底价", "绑定最新案例", "检查承诺边界"},
			ExpectedOutcome:  "销售准备时间和口径风险下降。",
			Translations: map[string]ScenarioTemplateTranslation{
				"en": {
					Name:               "Sales support",
					Description:        "Aligned value propositions, references, pricing boundaries and objection handling.",
					DatasetSuggestions: []string{"Product material", "Customer stories", "Sales playbooks"},
					SampleQuestions:    []string{"Which offering fits a manufacturing customer?", "How should I answer the common objection?"},
					EvaluationQuestions: []string{
						"What are the three core value points?",
						"Which discounts require approval?",
					},
					PermissionAdvice: "Available to sales; cost and floor-price documents need separate grants.",
					PublishChecklist: []string{"Isolate cost and floor price", "Bind the latest references", "Check commitment boundaries"},
					ExpectedOutcome:  "Less preparation time and lower messaging risk.",
				},
			},
		},
	},
	{
		Key:         "contract-compliance",
		Name:        "合同与合规",
		Description: "条款要点、风险提示、审批依据可追溯。",
		AppTypes:    []string{"chat", "agent"},
		Payload: ScenarioTemplatePayload{
			PromptPresetID:     "contract-compliance",
			ParameterProfileID: "citation-strict",
			DatasetSuggestions: []string{"合同模板", "合规制度", "审批口径"},
			SampleQuestions:    []string{"付款条款有哪些风险？", "数据保护条款怎么审？"},
			EvaluationQuestions: []string{
				"违约金上限的原文依据是什么？",
				"数据出境需要哪些审批？",
			},
			PermissionAdvice: "法务、合规和授权商务可访问；终稿修改仍走审批。",
			PublishChecklist: []string{"开启无引用不回答", "绑定审批口径", "定期复查模板"},
			ExpectedOutcome:  "合同初审更快且每个结论可回溯。",
			Translations: map[string]ScenarioTemplateTranslation{
				"en": {
					Name:               "Contract and compliance",
					Description:        "Clause summaries, risk flags and approval evidence that can be traced back.",
					DatasetSuggestions: []string{"Contract templates", "Compliance policies", "Approval rules"},
					SampleQuestions:    []string{"What are the risks in the payment clause?", "How should the data protection clause be reviewed?"},
					EvaluationQuestions: []string{
						"Where does the penalty cap come from in the source text?",
						"Which approvals does cross-border data transfer require?",
					},
					PermissionAdvice: "Available to legal, compliance and authorized business staff; final edits still need approval.",
					PublishChecklist: []string{"Require citations before answering", "Bind approval rules", "Review templates regularly"},
					ExpectedOutcome:  "Faster first-pass contract review with traceable conclusions.",
				},
			},
		},
	},
	{
		Key:         "operations",
		Name:        "IT 与运维助手",
		Description: "故障排查、变更步骤、回退方案和负责人可查。",
		AppTypes:    []string{"chat", "search", "agent"},
		Payload: ScenarioTemplatePayload{
			PromptPresetID:     "operations",
			ParameterProfileID: "safe",
			DatasetSuggestions: []string{"运维手册", "变更记录", "故障复盘"},
			SampleQuestions:    []string{"生产队列积压怎么排查？", "数据库回退步骤是什么？"},
			EvaluationQuestions: []string{
				"服务无响应的第一处理动作是什么？",
				"发布回退由谁确认？",
			},
			PermissionAdvice: "运维和技术负责人可访问；生产变更保持审批门禁。",
			PublishChecklist: []string{"绑定最新变更 SOP", "验证危险命令提示", "开启审计"},
			ExpectedOutcome:  "故障处理路径更稳定，避免临时猜测命令。",
			Translations: map[string]ScenarioTemplateTranslation{
				"en": {
					Name:               "IT and operations assistant",
					Description:        "Troubleshooting, change steps, rollback plans and owners in one place.",
					DatasetSuggestions: []string{"Runbooks", "Change records", "Postmortems"},
					SampleQuestions:    []string{"How do I triage a production queue backlog?", "What is the database rollback procedure?"},
					EvaluationQuestions: []string{
						"What is the first response when a service stops responding?",
						"Who confirms a release rollback?",
					},
					PermissionAdvice: "Available to operations and technical leads; production changes stay behind approvals.",
					PublishChecklist: []string{"Bind the latest change SOP", "Verify dangerous-command warnings", "Enable audit"},
					ExpectedOutcome:  "More stable incident handling without improvised commands.",
				},
			},
		},
	},
	{
		Key:         "training",
		Name:        "培训与 Onboarding",
		Description: "按角色生成学习路径、练习和验收标准。",
		AppTypes:    []string{"chat", "agent"},
		Payload: ScenarioTemplatePayload{
			PromptPresetID:     "training",
			ParameterProfileID: "balanced",
			DatasetSuggestions: []string{"岗位培训包", "流程说明", "考核材料"},
			SampleQuestions:    []string{"新销售第 1 周学什么？", "实施工程师需要掌握哪些流程？"},
			EvaluationQuestions: []string{
				"第 1 个月的关键验收项是什么？",
				"新员工需要参加哪些系统培训？",
			},
			PermissionAdvice: "全员可访问；部门专属材料按团队授权。",
			PublishChecklist: []string{"按角色分层材料", "验证学习路径", "收集反馈"},
			ExpectedOutcome:  "新员工上手周期可衡量并缩短。",
			Translations: map[string]ScenarioTemplateTranslation{
				"en": {
					Name:               "Training and onboarding",
					Description:        "Role-based learning paths, exercises and acceptance criteria.",
					DatasetSuggestions: []string{"Role onboarding kits", "Process guides", "Assessment material"},
					SampleQuestions:    []string{"What does a new sales rep learn in week 1?", "Which processes must implementation engineers know?"},
					EvaluationQuestions: []string{
						"What are the key acceptance items for month 1?",
						"Which system trainings must a new hire attend?",
					},
					PermissionAdvice: "Available to everyone; department-specific material follows team grants.",
					PublishChecklist: []string{"Layer material by role", "Verify the learning path", "Collect feedback"},
					ExpectedOutcome:  "Time to productivity becomes measurable and shorter.",
				},
			},
		},
	},
	{
		Key:         "finance",
		Name:        "财务与报表",
		Description: "指标口径、数据来源、审批和截止时间可追溯。",
		AppTypes:    []string{"chat", "search"},
		Payload: ScenarioTemplatePayload{
			PromptPresetID:     "finance",
			ParameterProfileID: "safe",
			DatasetSuggestions: []string{"财务制度", "指标口径", "报表说明"},
			SampleQuestions:    []string{"毛利率如何计算？", "月结截止日是哪天？"},
			EvaluationQuestions: []string{
				"应收周转天数的口径是什么？",
				"报销付款审核链路是什么？",
			},
			PermissionAdvice: "财务和管理者可访问；明细数据仍按项目/团队隔离。",
			PublishChecklist: []string{"锁定指标版本", "验证口径引用", "限制敏感明细"},
			ExpectedOutcome:  "报表口径争议减少，管理分析更快。",
			Translations: map[string]ScenarioTemplateTranslation{
				"en": {
					Name:               "Finance and reporting",
					Description:        "Metric definitions, data sources, approvals and cut-off dates that can be traced.",
					DatasetSuggestions: []string{"Finance policies", "Metric definitions", "Report guides"},
					SampleQuestions:    []string{"How is gross margin calculated?", "When is the month-end close deadline?"},
					EvaluationQuestions: []string{
						"What is the definition of DSO?",
						"What is the review chain for expense payments?",
					},
					PermissionAdvice: "Available to finance and managers; detailed records stay isolated per project/team.",
					PublishChecklist: []string{"Pin the metric version", "Verify definition citations", "Restrict sensitive detail"},
					ExpectedOutcome:  "Fewer definition disputes and faster management analysis.",
				},
			},
		},
	},
}

// BuiltinScenarioTemplates returns a defensive copy of the shipped catalog.
func BuiltinScenarioTemplates() []BuiltinScenarioTemplate {
	out := make([]BuiltinScenarioTemplate, 0, len(builtinScenarioTemplates))
	for _, item := range builtinScenarioTemplates {
		copied := item
		copied.AppTypes = append([]string(nil), item.AppTypes...)
		out = append(out, copied)
	}
	return out
}

// EnsureBuiltinScenarioTemplates seeds the shipped templates for one tenant.
// It is idempotent per (tenant_id, key): existing rows — including tenant
// customizations that reuse a builtin key — are never overwritten, only
// missing keys are added. It returns the number of rows created.
func (s *Service) EnsureBuiltinScenarioTemplates(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, nil
	}
	existing, _, err := s.Store.ListScenarioTemplates(ctx, tenantID, 1, 500, repository.GovernanceFilter{})
	if err != nil {
		return 0, err
	}
	present := make(map[string]bool, len(existing))
	for _, asset := range existing {
		present[asset.Key] = true
	}
	created := 0
	now := time.Now().UTC()
	for _, item := range builtinScenarioTemplates {
		if present[item.Key] {
			continue
		}
		payload, err := json.Marshal(item.Payload)
		if err != nil {
			return created, err
		}
		appTypes, err := validAppTypes(item.AppTypes)
		if err != nil {
			return created, err
		}
		asset := &model.ScenarioTemplateAsset{
			ID: id.New(), TenantID: tenantID, Key: item.Key, Name: item.Name,
			AppTypes: joinStrings(appTypes, ","), Description: item.Description,
			LatestVersion: 1, Source: model.TemplateSourceBuiltin,
			Status: model.AssetStatusPublished, PayloadJSON: string(payload),
			CreatedBy: builtinScenarioBootstrapActor, CreatedAt: now, UpdatedAt: now,
		}
		version := &model.ScenarioTemplateVersion{
			ID: id.New(), TenantID: tenantID, TemplateID: asset.ID, Version: 1,
			PayloadJSON: asset.PayloadJSON, ChangeNote: "builtin seed",
			CreatedBy: builtinScenarioBootstrapActor, CreatedAt: now,
		}
		if err := s.Store.CreateScenarioTemplate(ctx, asset, version); err != nil {
			// A concurrent bootstrap (or a tenant edit landing first) already
			// created the key: treat it as a no-op instead of failing the seed.
			current, getErr := s.Store.GetScenarioTemplateByKey(ctx, tenantID, item.Key)
			if getErr == nil && current != nil {
				continue
			}
			return created, err
		}
		created++
	}
	return created, nil
}

// BootstrapScenarioTemplates backfills builtin templates for every existing
// tenant at startup. Failures are logged and audited per tenant but never
// block startup (doc/228 §3.1 T7.0 trigger 3).
func (s *Service) BootstrapScenarioTemplates(ctx context.Context) {
	const pageSize = 200
	for page := 1; ; page++ {
		tenants, total, err := s.Store.ListTenants(ctx, page, pageSize, repository.TenantFilter{})
		if err != nil {
			logger.Warn("scenario template bootstrap: list tenants failed", "error", err)
			return
		}
		for _, tenant := range tenants {
			created, err := s.EnsureBuiltinScenarioTemplates(ctx, tenant.ID)
			detail := fmt.Sprintf(`{"created":%d}`, created)
			entry := &model.AuditLog{
				ID: id.New(), TenantID: tenant.ID, UserID: builtinScenarioBootstrapActor,
				Action: "scenario_template.bootstrap", Resource: "scenario-template",
				Result: "SUCCESS", DetailJSON: detail, At: time.Now().UTC(),
			}
			if err != nil {
				entry.Result = "FAILED"
				entry.DetailJSON = fmt.Sprintf(`{"created":%d,"error":"bootstrap failed"}`, created)
				logger.Warn("scenario template bootstrap failed", "tenant_id", tenant.ID, "error", err)
			}
			if err := s.RecordAudit(ctx, entry); err != nil {
				logger.Warn("scenario template bootstrap audit failed", "tenant_id", tenant.ID, "error", err)
			}
		}
		if int64(page*pageSize) >= total || len(tenants) == 0 {
			return
		}
	}
}

func joinStrings(values []string, sep string) string {
	if len(values) == 0 {
		return ""
	}
	out := values[0]
	for _, value := range values[1:] {
		out += sep + value
	}
	return out
}
