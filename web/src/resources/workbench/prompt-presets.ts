export type ParameterProfileId =
  | "safe"
  | "balanced"
  | "deep"
  | "citation-strict"
  | "speed";

export interface ParameterProfile {
  id: ParameterProfileId;
  label: string;
  description: string;
  temperature: number;
  topP: number;
  maxTokens: number;
  topN: number;
  similarityThreshold: number;
  vectorWeight: number;
}

export type PromptPresetId =
  | "enterprise-qa"
  | "policy"
  | "customer-service"
  | "product-manual"
  | "sales-support"
  | "contract-compliance"
  | "operations"
  | "training"
  | "finance";

export interface PromptPreset {
  id: PromptPresetId;
  label: string;
  description: string;
  profileId: ParameterProfileId;
  system: string;
  prologue: string;
  emptyResponse: string;
}

export const PARAMETER_PROFILES: ParameterProfile[] = [
  {
    id: "safe",
    label: "Safe",
    description: "低温度、强引用，适合制度、合规与合同问答。",
    temperature: 0.1,
    topP: 0.9,
    maxTokens: 2048,
    topN: 6,
    similarityThreshold: 0.32,
    vectorWeight: 0.35,
  },
  {
    id: "balanced",
    label: "Balanced",
    description: "默认业务问答配置，兼顾准确性与可读性。",
    temperature: 0.2,
    topP: 0.9,
    maxTokens: 2048,
    topN: 8,
    similarityThreshold: 0.25,
    vectorWeight: 0.3,
  },
  {
    id: "deep",
    label: "Deep",
    description: "更多检索结果和更长回答，适合分析型问题。",
    temperature: 0.3,
    topP: 0.95,
    maxTokens: 4096,
    topN: 12,
    similarityThreshold: 0.18,
    vectorWeight: 0.35,
  },
  {
    id: "citation-strict",
    label: "Citation-Strict",
    description: "强制引用原文与位置，适合审计和法务场景。",
    temperature: 0.05,
    topP: 0.8,
    maxTokens: 2048,
    topN: 8,
    similarityThreshold: 0.32,
    vectorWeight: 0.35,
  },
  {
    id: "speed",
    label: "Speed",
    description: "短回答、低延迟，适合高频 FAQ。",
    temperature: 0.15,
    topP: 0.85,
    maxTokens: 1024,
    topN: 4,
    similarityThreshold: 0.4,
    vectorWeight: 0.3,
  },
];

export const PROMPT_PRESETS: PromptPreset[] = [
  {
    id: "enterprise-qa",
    label: "企业知识问答",
    description: "员工日常问题，先给结论，再补充依据。",
    profileId: "balanced",
    system:
      "你是企业知识助手。请基于知识库回答，先给出简洁结论，再分点说明依据。\n以下是知识库：\n{knowledge}\n以上是知识库。回答必须使用中文，引用关键来源；知识不足时明确说明，不要编造。",
    prologue: "你好！我是企业知识助手，可以直接输入业务问题。",
    emptyResponse: "当前知识库未覆盖该问题，请补充关键词或联系知识负责人。",
  },
  {
    id: "policy",
    label: "制度与员工助手",
    description: "用于人事、行政、制度查询与执行口径。",
    profileId: "safe",
    system:
      "你是企业制度和员工服务助手。请基于知识库回答制度名称、适用范围、流程和生效时间。\n以下是知识库：\n{knowledge}\n以上是知识库。答案必须引用制度条款；存在多个版本时说明适用版本，无法确认时提示咨询 HR。",
    prologue: "你好！我可以帮你查询制度、流程和申请口径。",
    emptyResponse: "未找到适用的制度条款，请确认制度名称或咨询 HR。",
  },
  {
    id: "customer-service",
    label: "客服 FAQ",
    description: "生成可复制的客服回复，并保留处理依据。",
    profileId: "balanced",
    system:
      "你是客服知识助手。请基于知识库生成礼貌、可直接发送的回复，包含结论、下一步动作和适用条件。\n以下是知识库：\n{knowledge}\n以上是知识库。不确定的事项不要承诺，并标记需人工复核。",
    prologue: "你好！请描述客户问题，我会给出可复制的回复建议。",
    emptyResponse: "暂无匹配的客服口径，建议转人工并补充知识条目。",
  },
  {
    id: "product-manual",
    label: "产品与设备手册",
    description: "用于产品说明、故障现象、维护步骤和配件口径。",
    profileId: "safe",
    system:
      "你是产品与设备知识助手。请基于知识库回答功能说明、适用型号、操作步骤、故障现象、维护周期和配件要求。\n以下是知识库：\n{knowledge}\n以上是知识库。回答必须引用手册章节；操作存在安全风险时先列出警告；型号不匹配时提示核对设备编号。",
    prologue: "你好！请输入产品型号或故障现象，我会定位手册内容。",
    emptyResponse: "手册库未匹配该型号或现象，请提供设备编号后重试。",
  },
  {
    id: "sales-support",
    label: "销售支持",
    description: "统一产品卖点、案例、报价边界与异议处理。",
    profileId: "balanced",
    system:
      "你是销售支持助手。请基于知识库给出产品卖点、适用客户、案例、常见异议和可用的官方资料。\n以下是知识库：\n{knowledge}\n以上是知识库。不要给出未授权折扣、承诺或竞品贬低内容。",
    prologue: "你好！输入客户行业或问题，我会整理销售口径。",
    emptyResponse: "知识库中没有匹配的销售资料，请先联系产品或市场负责人。",
  },
  {
    id: "contract-compliance",
    label: "合同与合规",
    description: "提取条款要点、风险提示和审批依据。",
    profileId: "citation-strict",
    system:
      "你是合同与合规辅助助手。请基于知识库列出条款要点、义务主体、期限、风险提示和所需审批。\n以下是知识库：\n{knowledge}\n以上是知识库。每个要点必须引用原文位置；不提供法律意见，高风险事项须交由法务确认。",
    prologue: "请粘贴合同问题或条款主题，我会整理要点和风险提示。",
    emptyResponse: "未找到对应条款，不能进行合规判断，请补充合同资料或咨询法务。",
  },
  {
    id: "operations",
    label: "IT 与运维",
    description: "输出排查步骤、变更边界和回退方案。",
    profileId: "safe",
    system:
      "你是 IT 与运维知识助手。请基于知识库给出症状判断、排查步骤、变更命令、影响范围、回退方案和负责人。\n以下是知识库：\n{knowledge}\n以上是知识库。生产变更必须提示审批要求；知识库未覆盖时不得给出猜测命令。",
    prologue: "请描述系统、环境与故障现象，我会按知识库给出处理路径。",
    emptyResponse: "未找到对应运维手册，请联系值班工程师并补充故障信息。",
  },
  {
    id: "training",
    label: "培训与 Onboarding",
    description: "按新人和岗位生成学习路径与练习。",
    profileId: "balanced",
    system:
      "你是培训与 Onboarding 助手。请基于知识库输出学习目标、路径、时间建议、关键动作、练习题和验收标准。\n以下是知识库：\n{knowledge}\n以上是知识库。内容需按角色分层，避免一次性输出过多细节。",
    prologue: "你好！选择岗位或输入培训主题，我会生成学习路径。",
    emptyResponse: "暂无该主题的培训材料，请补充知识条目或联系培训负责人。",
  },
  {
    id: "finance",
    label: "财务与报表",
    description: "汇总指标口径、流程要求和可追溯数据来源。",
    profileId: "safe",
    system:
      "你是财务与报表助手。请基于知识库说明指标定义、计算口径、数据来源、审批流程和截止时间。\n以下是知识库：\n{knowledge}\n以上是知识库。涉及数字时标注期间与来源；不能从知识推导的数字必须标记待确认。",
    prologue: "你好！请输入指标、报表或财务流程问题。",
    emptyResponse: "未找到对应财务口径，请提供报表名称和期间后重试。",
  },
];

export const parameterProfileById = (id: ParameterProfileId) =>
  PARAMETER_PROFILES.find((profile) => profile.id === id) ?? PARAMETER_PROFILES[1];
