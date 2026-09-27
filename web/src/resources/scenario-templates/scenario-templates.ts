import {
  PARAMETER_PROFILES,
  PROMPT_PRESETS,
  parameterProfileById,
  type ParameterProfileId,
  type PromptPresetId,
} from "../workbench/prompt-presets";

export type ScenarioAppType = "chat" | "search" | "agent";

export interface ScenarioTemplate {
  id:
    | "enterprise-qa"
    | "policy"
    | "customer-service"
    | "product-manual"
    | "sales-support"
    | "contract-compliance"
    | "operations"
    | "training"
    | "finance";
  label: string;
  description: string;
  appTypes: ScenarioAppType[];
  promptPresetId: PromptPresetId;
  profileId: ParameterProfileId;
  suggestedDatasetNames: string[];
  sampleQuestions: string[];
  evaluationQuestions: string[];
  permissionAdvice: string;
  publishChecklist: string[];
  expectedOutcome: string;
}

export const SCENARIO_TEMPLATES: ScenarioTemplate[] = [
  {
    id: "enterprise-qa",
    label: "企业知识问答",
    description: "员工日常问题统一入口，先给结论、再给依据。",
    appTypes: ["chat", "search"],
    promptPresetId: "enterprise-qa",
    profileId: "balanced",
    suggestedDatasetNames: ["制度流程", "FAQ", "项目知识包"],
    sampleQuestions: ["报销流程是什么？", "新员工第 1 周要完成什么？"],
    evaluationQuestions: ["报销标准和截止时间是什么？", "报销流程需要哪些审批人？"],
    permissionAdvice: "全员可访问；管理配置仅限实施管理员。",
    publishChecklist: ["绑定最新制度库", "试跑 10 个高频问题", "开启反馈"],
    expectedOutcome: "高频业务问题优先解决，引用率和采纳率可见。",
  },
  {
    id: "policy",
    label: "制度与员工助手",
    description: "人事、行政、考勤、报销等制度口径统一。",
    appTypes: ["chat", "search"],
    promptPresetId: "policy",
    profileId: "safe",
    suggestedDatasetNames: ["员工手册", "制度文件", "政策问答"],
    sampleQuestions: ["年假如何申请？", "加班调休的有效期是多久？"],
    evaluationQuestions: ["试用期考核标准是什么？", "请假审批链路是谁？"],
    permissionAdvice: "全员可访问；工资类制度按团队权限单独隔离。",
    publishChecklist: ["确认制度版本生效时间", "标记敏感薪资文档", "开启引用展示"],
    expectedOutcome: "制度问题不再依赖人工反复解释。",
  },
  {
    id: "customer-service",
    label: "客服 FAQ",
    description: "客服可复制回复、口径统一、复杂问题转人工。",
    appTypes: ["chat", "search", "agent"],
    promptPresetId: "customer-service",
    profileId: "balanced",
    suggestedDatasetNames: ["客服 FAQ", "工单案例", "服务政策"],
    sampleQuestions: ["订单未收到怎么办？", "发票丢失如何补开？"],
    evaluationQuestions: ["7 天无理由退货条件是什么？", "投诉升级给哪个负责人？"],
    permissionAdvice: "客服团队可访问；管理配置仅限服务负责人。",
    publishChecklist: ["绑定 FAQ 和政策", "验证无答案转人工", "统计采纳率"],
    expectedOutcome: "一线客服回复速度和口径一致性提升。",
  },
  {
    id: "product-manual",
    label: "产品与设备手册",
    description: "按型号检索操作、故障、维护和配件信息。",
    appTypes: ["search", "agent"],
    promptPresetId: "product-manual",
    profileId: "safe",
    suggestedDatasetNames: ["产品手册", "维修手册", "配件清单"],
    sampleQuestions: ["设备报 E03 如何处理？", "泵体滤芯多久更换？"],
    evaluationQuestions: ["E03 的官方处理步骤是什么？", "更换滤芯需要注意哪些安全项？"],
    permissionAdvice: "技术支持、售后和现场工程师可访问。",
    publishChecklist: ["确认型号目录", "验证故障码检索", "补充安全警告"],
    expectedOutcome: "现场工程师减少翻手册时间并降低误操作。",
  },
  {
    id: "sales-support",
    label: "销售支持",
    description: "统一产品卖点、案例、报价边界和异议处理。",
    appTypes: ["chat", "agent"],
    promptPresetId: "sales-support",
    profileId: "balanced",
    suggestedDatasetNames: ["产品资料", "客户案例", "销售话术"],
    sampleQuestions: ["向制造业客户推荐哪个方案？", "常见异议如何回应？"],
    evaluationQuestions: ["产品的三个核心卖点是什么？", "哪些折扣需要审批？"],
    permissionAdvice: "销售团队可访问；成本和底价文档单独授权。",
    publishChecklist: ["隔离成本底价", "绑定最新案例", "检查承诺边界"],
    expectedOutcome: "销售准备时间和口径风险下降。",
  },
  {
    id: "contract-compliance",
    label: "合同与合规",
    description: "条款要点、风险提示、审批依据可追溯。",
    appTypes: ["chat", "agent"],
    promptPresetId: "contract-compliance",
    profileId: "citation-strict",
    suggestedDatasetNames: ["合同模板", "合规制度", "审批口径"],
    sampleQuestions: ["付款条款有哪些风险？", "数据保护条款怎么审？"],
    evaluationQuestions: ["违约金上限的原文依据是什么？", "数据出境需要哪些审批？"],
    permissionAdvice: "法务、合规和授权商务可访问；终稿修改仍走审批。",
    publishChecklist: ["开启无引用不回答", "绑定审批口径", "定期复查模板"],
    expectedOutcome: "合同初审更快且每个结论可回溯。",
  },
  {
    id: "operations",
    label: "IT 与运维助手",
    description: "故障排查、变更步骤、回退方案和负责人可查。",
    appTypes: ["chat", "search", "agent"],
    promptPresetId: "operations",
    profileId: "safe",
    suggestedDatasetNames: ["运维手册", "变更记录", "故障复盘"],
    sampleQuestions: ["生产队列积压怎么排查？", "数据库回退步骤是什么？"],
    evaluationQuestions: ["服务无响应的第一处理动作是什么？", "发布回退由谁确认？"],
    permissionAdvice: "运维和技术负责人可访问；生产变更保持审批门禁。",
    publishChecklist: ["绑定最新变更 SOP", "验证危险命令提示", "开启审计"],
    expectedOutcome: "故障处理路径更稳定，避免临时猜测命令。",
  },
  {
    id: "training",
    label: "培训与 Onboarding",
    description: "按角色生成学习路径、练习和验收标准。",
    appTypes: ["chat", "agent"],
    promptPresetId: "training",
    profileId: "balanced",
    suggestedDatasetNames: ["岗位培训包", "流程说明", "考核材料"],
    sampleQuestions: ["新销售第 1 周学什么？", "实施工程师需要掌握哪些流程？"],
    evaluationQuestions: ["第 1 个月的关键验收项是什么？", "新员工需要参加哪些系统培训？"],
    permissionAdvice: "全员可访问；部门专属材料按团队授权。",
    publishChecklist: ["按角色分层材料", "验证学习路径", "收集反馈"],
    expectedOutcome: "新员工上手周期可衡量并缩短。",
  },
  {
    id: "finance",
    label: "财务与报表",
    description: "指标口径、数据来源、审批和截止时间可追溯。",
    appTypes: ["chat", "search"],
    promptPresetId: "finance",
    profileId: "safe",
    suggestedDatasetNames: ["财务制度", "指标口径", "报表说明"],
    sampleQuestions: ["毛利率如何计算？", "月结截止日是哪天？"],
    evaluationQuestions: ["应收周转天数的口径是什么？", "报销付款审核链路是什么？"],
    permissionAdvice: "财务和管理者可访问；明细数据仍按项目/团队隔离。",
    publishChecklist: ["锁定指标版本", "验证口径引用", "限制敏感明细"],
    expectedOutcome: "报表口径争议减少，管理分析更快。",
  },
];

export const scenarioTemplateById = (id: string) =>
  SCENARIO_TEMPLATES.find((template) => template.id === id);

export const scenarioPromptPreset = (template: ScenarioTemplate) =>
  PROMPT_PRESETS.find((preset) => preset.id === template.promptPresetId) ?? PROMPT_PRESETS[0];

export const scenarioParameterProfile = (template: ScenarioTemplate) =>
  PARAMETER_PROFILES.find((profile) => profile.id === template.profileId) ?? parameterProfileById(template.profileId);
