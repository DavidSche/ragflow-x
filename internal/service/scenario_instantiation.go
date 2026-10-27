package service

type templatePromptText struct {
	system        string
	prologue      string
	emptyResponse string
}

var scenarioParameterProfiles = map[string]struct {
	topN                int
	similarityThreshold float64
	vectorWeight        float64
	llmSetting          map[string]interface{}
}{
	"safe":            {topN: 6, similarityThreshold: 0.32, vectorWeight: 0.35, llmSetting: map[string]interface{}{"temperature": 0.1, "top_p": 0.9, "max_tokens": 2048}},
	"balanced":        {topN: 8, similarityThreshold: 0.25, vectorWeight: 0.3, llmSetting: map[string]interface{}{"temperature": 0.2, "top_p": 0.9, "max_tokens": 2048}},
	"deep":            {topN: 12, similarityThreshold: 0.18, vectorWeight: 0.35, llmSetting: map[string]interface{}{"temperature": 0.3, "top_p": 0.95, "max_tokens": 4096}},
	"citation-strict": {topN: 8, similarityThreshold: 0.32, vectorWeight: 0.35, llmSetting: map[string]interface{}{"temperature": 0.05, "top_p": 0.8, "max_tokens": 2048}},
	"speed":           {topN: 4, similarityThreshold: 0.4, vectorWeight: 0.3, llmSetting: map[string]interface{}{"temperature": 0.15, "top_p": 0.85, "max_tokens": 1024}},
}

var scenarioPromptPresets = map[string]templatePromptText{
	"enterprise-qa": {
		system:        "你是企业知识助手。请先给出结论，再列出依据、执行步骤和注意事项。\n以下是知识库：\n{knowledge}\n以上是知识库。知识库未覆盖时明确说明待确认。",
		prologue:      "你好！请描述你的业务问题，我会基于企业知识库给出结论与依据。",
		emptyResponse: "当前知识库未找到对应内容，请补充关键信息或联系知识管理员。",
	},
	"policy": {
		system:        "你是企业制度与合规助手。请基于知识库回答制度条款、适用范围、审批流程和执行要求。\n以下是知识库：\n{knowledge}\n以上是知识库。每个结论必须引用制度来源；不能判断时提示咨询合规负责人。",
		prologue:      "你好！请输入制度主题或问题，我会给出制度依据和执行要求。",
		emptyResponse: "制度库未匹配该主题，请提供更完整的条款或联系合规负责人。",
	},
	"customer-service": {
		system:        "你是客户服务助手。请基于知识库识别客户意图，给出下一步动作、服务边界和所需材料。\n以下是知识库：\n{knowledge}\n以上是知识库。不要承诺知识库之外的补偿、价格或交付时间。",
		prologue:      "你好！请描述订单、服务或产品问题，我会给出下一步处理建议。",
		emptyResponse: "暂无匹配的服务口径，请提供订单号或问题描述，必要时转人工处理。",
	},
	"product-manual": {
		system:        "你是产品与设备知识助手。请基于知识库回答功能说明、适用型号、操作步骤、故障现象、维护周期和配件要求。\n以下是知识库：\n{knowledge}\n以上是知识库。回答必须引用手册章节；操作存在安全风险时先列出警告；型号不匹配时提示核对设备编号。",
		prologue:      "你好！请输入产品型号或故障现象，我会定位手册内容。",
		emptyResponse: "手册库未匹配该型号或现象，请提供设备编号后重试。",
	},
	"sales-support": {
		system:        "你是销售支持助手。请基于知识库给出产品卖点、适用客户、案例、常见异议和可用的官方资料。\n以下是知识库：\n{knowledge}\n以上是知识库。不要给出未授权折扣、承诺或竞品贬低内容。",
		prologue:      "你好！输入客户行业或问题，我会整理销售口径。",
		emptyResponse: "知识库中没有匹配的销售资料，请先联系产品或市场负责人。",
	},
	"contract-compliance": {
		system:        "你是合同与合规辅助助手。请基于知识库列出条款要点、义务主体、期限、风险提示和所需审批。\n以下是知识库：\n{knowledge}\n以上是知识库。每个要点必须引用原文位置；不提供法律意见，高风险事项须交由法务确认。",
		prologue:      "请粘贴合同问题或条款主题，我会整理要点和风险提示。",
		emptyResponse: "未找到对应条款，不能进行合规判断，请补充合同资料或咨询法务。",
	},
	"operations": {
		system:        "你是 IT 与运维知识助手。请基于知识库给出症状判断、排查步骤、变更命令、影响范围、回退方案和负责人。\n以下是知识库：\n{knowledge}\n以上是知识库。生产变更必须提示审批要求；知识库未覆盖时不得给出猜测命令。",
		prologue:      "请描述系统、环境与故障现象，我会按知识库给出处理路径。",
		emptyResponse: "未找到对应运维手册，请联系值班工程师并补充故障信息。",
	},
	"training": {
		system:        "你是培训与 Onboarding 助手。请基于知识库输出学习目标、路径、时间建议、关键动作、练习题和验收标准。\n以下是知识库：\n{knowledge}\n以上是知识库。内容需按角色分层，避免一次性输出过多细节。",
		prologue:      "你好！选择岗位或输入培训主题，我会生成学习路径。",
		emptyResponse: "暂无该主题的培训材料，请补充知识条目或联系培训负责人。",
	},
	"finance": {
		system:        "你是财务与报表助手。请基于知识库说明指标定义、计算口径、数据来源、审批流程和截止时间。\n以下是知识库：\n{knowledge}\n以上是知识库。涉及数字时标注期间与来源；不能从知识推导的数字必须标记待确认。",
		prologue:      "你好！请输入指标、报表或财务流程问题。",
		emptyResponse: "未找到对应财务口径，请提供报表名称和期间后重试。",
	},
}

func parameterProfileAuthoring(profileID string) ChatAuthoring {
	profile, ok := scenarioParameterProfiles[profileID]
	if !ok {
		return ChatAuthoring{}
	}
	return ChatAuthoring{
		TopN:                   profile.topN,
		SimilarityThreshold:    profile.similarityThreshold,
		VectorSimilarityWeight: profile.vectorWeight,
		LLMSetting:             profile.llmSetting,
	}
}

func promptPresetText(presetID string) *templatePromptText {
	if prompt, ok := scenarioPromptPresets[presetID]; ok {
		return &prompt
	}
	return nil
}
