package service

import (
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

type routeEvalTarget struct {
	name        string
	kind        string
	description string
	intent      string
	keywords    []string
	questions   []string
}

func (target routeEvalTarget) routingMetadata() ([]string, []string) {
	return target.keywords, target.questions
}

// Doc41RouteEvaluationCases seeds the first M2.5 suite from the raven-demo
// business domains defined by doc/41 S16: safety regulations, risk-control
// statistics, and subordinate-enterprise reports. Expected IDs are resolved by
// tenant catalog name so the suite remains portable across environments.
var doc41RouteTargets = []routeEvalTarget{
	{
		name: "安全政策法规助手", kind: model.AssistantKindChat,
		description: "解答安全生产法律法规、作业规程、审批备案、隐患治理和培训应急要求。",
		intent:      "safety_regulation_qa",
		keywords:    []string{"安全", "安全生产", "安全生产法", "有限空间", "动火作业", "高处作业", "危险化学品", "特种作业", "安全培训", "应急预案", "隐患排查", "双重预防", "承包商入场", "事故报告", "安全设施", "受限空间", "粉尘防爆", "起重机械", "临时用电", "消防通道", "风险辨识", "安全生产责任制", "重大事故隐患", "三级安全教育", "外包施工", "职业病危害", "汛期检查", "燃气泄漏"},
		questions: []string{
			"安全生产法对企业主要负责人的职责有哪些要求？",
			"有限空间作业前需要完成哪些审批和检测？",
			"动火作业分级后现场监护有什么规定？",
			"高处作业安全带应如何正确使用？",
			"危险化学品储存区域的距离要求是什么？",
			"特种作业人员必须取得什么证件？",
			"企业安全培训学时和考核要求有哪些？",
			"应急预案备案和演练频次如何规定？",
			"隐患排查治理台账应该包含哪些内容？",
			"双重预防机制的风险分级管控要求是什么？",
			"承包商入场作业需要审查哪些安全资料？",
			"生产安全事故报告的时限和层级是什么？",
			"安全设施设计审查适用于哪些建设项目？",
			"受限空间应急救援为什么禁止盲目施救？",
			"粉尘爆炸危险场所的防爆要求有哪些？",
			"起重机械作业前需要检查哪些项目？",
			"临时用电的漏电保护和接地要求是什么？",
			"消防通道被占用应依据哪些条款处理？",
			"企业开展安全风险辨识应覆盖哪些对象？",
			"安全生产责任制考核结果如何应用？",
			"重大事故隐患判定标准包括哪些情形？",
			"新员工三级安全教育分别包含什么内容？",
			"外包施工队伍的安全协议应约定什么？",
			"职业病危害告知卡和警示标识怎么设置？",
			"汛期安全生产检查需要关注哪些风险？",
			"燃气使用场所的泄漏报警要求是什么？",
			"危险化学品重大危险源如何辨识分级？",
			"企业安全费用提取和使用范围是什么？",
			"安全生产标准化评审的要素有哪些？",
			"事故调查报告应包含哪些必备结论？",
		},
	},
	{
		name: "风险点防控统计助手", kind: model.AssistantKindChat,
		description: "统计风险点分布、风险等级、整改完成率、趋势排名、预警阈值和治理进展。",
		intent:      "risk_control_statistics",
		keywords:    []string{"安全", "安全生产", "风险点", "风险等级", "风险分布", "统计", "区域排名", "行业对比", "整改完成率", "新增销号", "高风险企业", "挂牌督办", "逾期未整改", "月度汇总", "危险化工", "建筑施工", "责任单位", "事故率", "重大风险源", "预警阈值", "报表字段", "交叉分析", "缺失值", "点位占比", "管控措施", "复查情况"},
		questions: []string{
			"本季度各企业风险点总数是多少？",
			"重大风险源数量最多的企业是哪家？",
			"风险等级为红色的点位有哪些？",
			"按区域统计风险点分布并给出排名。",
			"各行业隐患整改完成率对比如何？",
			"近三个月风险点新增和销号趋势如何？",
			"高风险企业清单和主要风险类型是什么？",
			"挂牌督办事项的闭环率是多少？",
			"统计表里逾期未整改的隐患有哪些？",
			"风险点防控数据的月度汇总结果是什么？",
			"危险化工企业的风险点占比是多少？",
			"建筑施工企业的主要风险类型如何分布？",
			"不同责任单位的风险治理进展如何？",
			"整改完成率低于目标的企业有哪些？",
			"事故率与风险点密度是否出现异常？",
			"各企业重大风险源变化情况如何对比？",
			"需要重点关注的风险指标有哪些？",
			"风险点等级变化是否集中在某些区域？",
			"本年度安全风险统计数据可以怎么筛选？",
			"风险点防控统计库中企业数量是多少？",
			"各类型隐患的数量和占比是多少？",
			"风险管控措施落实率怎么统计？",
			"不同风险等级的点位复查情况如何？",
			"哪个区域红色风险点增长最快？",
			"请交叉分析行业和风险等级的分布。",
			"请汇总逾期未复查的风险点数量。",
			"请按月份展示隐患治理完成情况。",
			"请导出风险点排名报表所需的字段。",
			"统计表中缺失值主要集中在哪些字段？",
			"高风险点预警阈值应该按什么指标设置？",
		},
	},
	{
		name: "下属企业安全报告助手", kind: model.AssistantKindChat,
		description: "汇总和解读下属企业安全报告中的结论、问题、投入、整改建议和风险研判。",
		intent:      "enterprise_safety_report_analysis",
		keywords:    []string{"安全", "安全生产", "安全报告", "总体结论", "突出风险", "责任落实", "安全投入", "事故教训", "培训演练", "制度建设", "专项整改", "风险研判", "年度总结", "带班检查", "考核结果", "共性隐患", "应急预案建设", "安全运营", "下一步工作", "风险变化趋势", "危险作业", "整改闭环", "安全生产月", "挂图作战", "安全文化", "关键指标", "外包队伍管理", "事故案例", "防控体系", "问题清单", "督导意见"},
		questions: []string{
			"省属企业安全生产报告的总体结论是什么？",
			"下属企业报告中有哪些突出风险问题？",
			"报告里安全生产责任落实情况如何总结？",
			"各下属企业安全投入对比结论是什么？",
			"报告中提到的典型事故教训有哪些？",
			"下属企业安全培训演练情况怎么样？",
			"报告对企业制度建设的建议是什么？",
			"安全生产专项整改行动进展如何？",
			"报告中重点领域的风险研判是什么？",
			"年度安全生产总结的主要亮点是什么？",
			"企业负责人带班检查情况在报告里如何？",
			"安全考核结果在报告中怎么体现？",
			"报告指出的共性隐患有哪些？",
			"下属企业应急预案建设情况如何？",
			"报告中安全检查发现问题如何分类？",
			"省属企业安全运营情况的整体评价是什么？",
			"报告建议的下一步工作计划是什么？",
			"各企业安全报告的风险变化趋势如何？",
			"报告中涉及的危险作业有哪些？",
			"下属企业隐患整改闭环情况如何描述？",
			"报告里安全生产月活动成效是什么？",
			"安全生产报告中提到的挂图作战是什么？",
			"报告中企业安全文化建设情况如何？",
			"下属企业安全报告的关键指标有哪些？",
			"报告中对外包队伍管理有什么要求？",
			"报告里的安全事故案例说明了什么问题？",
			"报告如何评估安全风险防控体系有效性？",
			"下属企业安全报告的问题清单在哪里？",
			"报告中上级督导意见的落实情况如何？",
			"报告对企业安全生产形势怎么研判？",
		},
	},
	{
		name: "安全生产风险点防控统计表问答-r2", kind: model.AssistantKindAgent,
		description: "根据风险点防控统计表回答企业详情、防控报告、分类统计、督导检查和互检问题。",
		intent:      "safety_risk_control_workflow",
		keywords:    []string{"企业风险点防控详情", "企业风险防控报告", "企业风险防控分类统计", "国资委督导检查表", "国资委督导检查报告", "企业互检问题详情", "企业互检情况统计"},
		questions: []string{
			"企业风险点防控详情",
			"企业风险防控报告",
			"企业风险防控分类统计",
			"国资委督导检查表",
			"国资委督导检查报告",
			"企业互检问题详情",
			"企业互检情况统计",
		},
	},
}

func Doc41RouteEvaluationCases() []RouteEvalCaseInput {
	targets := doc41RouteTargets
	cases := make([]RouteEvalCaseInput, 0, 103)
	for _, target := range targets {
		for questionIndex, question := range target.questions {
			caseType := model.RouteEvalCasePositive
			if strings.Contains(question, "对比") || strings.Contains(question, "交叉") || strings.Contains(question, "汇总") {
				caseType = model.RouteEvalCaseHardNegative
			}
			agentCase := RouteEvalCaseInput{
				Question: question, ExpectedKind: target.kind,
				ExpectedName: target.name, CaseType: caseType,
			}
			if target.kind == model.AssistantKindAgent && questionIndex < 2 {
				agentCase.Split = model.RouteEvalSplitValidation
			}
			cases = append(cases, agentCase)
		}
	}
	ambiguous := []string{
		"帮我看一下最新要求。",
		"这个数据怎么解释？",
		"请整理一下重点内容。",
		"有没有异常？",
		"继续分析。",
		"根据报告说明情况。",
	}
	for _, question := range ambiguous {
		cases = append(cases, RouteEvalCaseInput{Question: question, CaseType: model.RouteEvalCaseAmbiguous})
	}
	return cases
}

func doc41RouteEvaluationMetadata() []routeEvalTarget {
	targets := []routeEvalTarget{
		{name: "安全政策法规助手", kind: model.AssistantKindChat},
		{name: "风险点防控统计助手", kind: model.AssistantKindChat},
		{name: "下属企业安全报告助手", kind: model.AssistantKindChat},
		{name: "安全生产风险点防控统计表问答-r2", kind: model.AssistantKindAgent},
	}
	known := map[string]routeEvalTarget{}
	for _, target := range doc41RouteTargets {
		known[target.name] = target
	}
	for index := range targets {
		targets[index] = known[targets[index].name]
	}
	return targets
}
