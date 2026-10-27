package relcheck

// ReviewItem records one external review item and its closure evidence. The
// registry mirrors doc 137 §20 (review 139), §21 (review 140), §24 (review 141)
// and §32 (review 143). Evidence layers:
//  1. the mapping table row exists in the plan document (verified by scanning
//     §<Section> for the item id);
//  2. the listed regression test symbols exist under internal/;
//  3. the listed milestone/evidence documents exist under doc/develop/.
type ReviewItem struct {
	Review        string   `json:"review"`
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Section       int      `json:"section"`
	TestSymbols   []string `json:"test_symbols"`
	MilestoneDocs []string `json:"milestone_docs"`
}

// ReviewCheckResult is the verification outcome for one review item.
type ReviewCheckResult struct {
	ReviewItem
	MappingRowFound bool     `json:"mapping_row_found"`
	MissingTests    []string `json:"missing_tests"`
	MissingDocs     []string `json:"missing_docs"`
}

func (r *ReviewCheckResult) Pass() bool {
	return r.MappingRowFound && len(r.MissingTests) == 0 && len(r.MissingDocs) == 0
}

// ReviewRegistry is the REL-003 closure registry. Test symbols are chosen as
// representative regression evidence per review item (not an exhaustive list);
// milestone docs record the closure narratives.
func ReviewRegistry() []ReviewItem {
	return []ReviewItem{
		// ---- 外部评审 139（doc 137 §20）----
		{Review: "139", ID: "P0-1", Section: 20, Title: "Memory 默认关闭与 Raw 默认开启语义冲突",
			TestSymbols:   []string{"TestGovernedMemorySearchRequiresActivePolicy", "TestLegacyMemoryMessageGateTombstoneAndForget", "TestMemoryPolicyControlPlaneFailClosed"},
			MilestoneDocs: []string{"198-MemoryPrivacyConsentGovernance里程碑记录.md"}},
		{Review: "139", ID: "P0-2", Section: 20, Title: "撤回、异步写入和策略暂停竞态",
			TestSymbols:   []string{"TestMemoryDispatchAdmissionUsesCurrentPolicyAndActivePrincipal", "TestMemoryOperationTombstoneCancelsAcceptedDispatch", "TestMemoryOperationOutboxCreationIsAtomic"},
			MilestoneDocs: []string{"199-MemoryComplianceDeleteAndPrivacyCloseout里程碑记录.md"}},
		{Review: "139", ID: "P0-3", Section: 20, Title: "Compilation Evidence 版本绑定",
			TestSymbols:   []string{"TestMigrationV143CompilationImprovementGateReadiness", "TestHTTPClient_GetDatasetArtifactContract"},
			MilestoneDocs: []string{"197-WikiABAndCompilationArtifactKindIsolation里程碑记录.md"}},
		{Review: "139", ID: "P0-4", Section: 20, Title: "Memory 在模型调用前授权",
			TestSymbols:   []string{"TestPreModelContextPolicyMismatchFailsClosed", "TestAgentCompletionPersistsPreModelContextDecision", "TestChatAppCompletionPersistsPreModelContextDecision"},
			MilestoneDocs: []string{"199-MemoryComplianceDeleteAndPrivacyCloseout里程碑记录.md"}},
		{Review: "139", ID: "P0-5", Section: 20, Title: "外部操作幂等与 unknown 恢复",
			TestSymbols:   []string{"TestMemoryDispatchAbsentExternalMessageRequiresCompensation", "TestCompilationUnknownRecoveryRequiresTwoNoMatchResultsAndApproval", "TestOutboxEventLeaseAndRetry"},
			MilestoneDocs: []string{"202-MemoryMemberExitInvalidationLiveAndCompilationReplacementDrill里程碑记录.md"}},
		{Review: "139", ID: "P1-1", Section: 20, Title: "A/B 统计与样本口径",
			TestSymbols:   []string{"TestEvaluationProtocolRegistrationValidatesInput", "TestLiveWikiSearchDatasetPairedABV4E2E"},
			MilestoneDocs: []string{"205-评测协议预注册治理与Wiki归因加固里程碑记录.md"}},
		{Review: "139", ID: "P1-2", Section: 20, Title: "PII 不是唯一隐私控制",
			TestSymbols:   []string{"TestMemoryPrivacyGateDeterministicLabels", "TestMemoryPrivacyGateSealsOperationMetadata"},
			MilestoneDocs: []string{"198-MemoryPrivacyConsentGovernance里程碑记录.md"}},
		{Review: "139", ID: "P1-3", Section: 20, Title: "Context Service 职责过重",
			TestSymbols:   []string{"TestPreModelContextRequestConflictDoesNotCallRAGFlow", "TestContextDecisionLookupRequiresOwnerOrGovernance"},
			MilestoneDocs: []string{"199-MemoryComplianceDeleteAndPrivacyCloseout里程碑记录.md"}},
		{Review: "139", ID: "P1-4", Section: 20, Title: "状态机分别解释",
			TestSymbols:   []string{"TestMemoryDispatchAdmissionUsesCurrentPolicyAndActivePrincipal", "TestMigrationV144MemoryPrivacyConsentGovernance"},
			MilestoneDocs: []string{"199-MemoryComplianceDeleteAndPrivacyCloseout里程碑记录.md"}},
		{Review: "139", ID: "P1-5", Section: 20, Title: "数据库约束不足",
			TestSymbols:   []string{"TestBuiltinRBACReconciliationMigrationBackfillsExistingDatabase", "TestMemoryOperationOutboxCreationIsAtomic"},
			MilestoneDocs: []string{"199-MemoryComplianceDeleteAndPrivacyCloseout里程碑记录.md"}},

		// ---- 外部评审 140（doc 137 §21）----
		{Review: "140", ID: "P0-1", Section: 21, Title: "测试 29 与 RAGFlow 幂等能力矛盾",
			TestSymbols:   []string{"TestMemoryDispatchAbsentExternalMessageRequiresCompensation", "TestOutboxEventLeaseAndRetry"},
			MilestoneDocs: []string{"202-MemoryMemberExitInvalidationLiveAndCompilationReplacementDrill里程碑记录.md"}},
		{Review: "140", ID: "P0-2", Section: 21, Title: "X 用户身份与 RAGFlow Principal 映射",
			TestSymbols:   []string{"TestLiveMemoryPrincipalMappingIdentityGate"},
			MilestoneDocs: []string{"202-MemoryMemberExitInvalidationLiveAndCompilationReplacementDrill里程碑记录.md"}},
		{Review: "140", ID: "P0-3", Section: 21, Title: "项目共享 Memory Consent 与成员退出",
			TestSymbols:   []string{"TestProjectSharedMemoryConsentLifecycle", "TestProjectSharedMemoryRequiresPersonalConsent", "TestLiveProjectMemoryMemberExitInvalidationE2E"},
			MilestoneDocs: []string{"202-MemoryMemberExitInvalidationLiveAndCompilationReplacementDrill里程碑记录.md"}},
		{Review: "140", ID: "P1-1", Section: 21, Title: "Compilation 幂等与 unknown",
			TestSymbols:   []string{"TestCompilationUnknownRecoveryRequiresTwoNoMatchResultsAndApproval", "TestLiveCompilationUnknownRecoveryE2E"},
			MilestoneDocs: []string{"202-MemoryMemberExitInvalidationLiveAndCompilationReplacementDrill里程碑记录.md"}},
		{Review: "140", ID: "P1-2", Section: 21, Title: "Document Manifest Hash",
			TestSymbols:   []string{"TestHTTPClient_GetDatasetArtifactContract", "TestMigrationV143CompilationImprovementGateReadiness"},
			MilestoneDocs: []string{"197-WikiABAndCompilationArtifactKindIsolation里程碑记录.md"}},
		{Review: "140", ID: "P1-3", Section: 21, Title: "Evidence/Readiness/Strategy 状态关系",
			TestSymbols:   []string{"TestMigrationV143CompilationImprovementGateReadiness", "TestRoutePolicyRequiresAgentFlowReadiness"},
			MilestoneDocs: []string{"195-PageIndexCompiledProvenanceAndProtocolV3里程碑记录.md"}},
		{Review: "140", ID: "P1-4", Section: 21, Title: "分层评测协议",
			TestSymbols:   []string{"TestEvaluationProtocolRegistrationCanonicalizesKeyOrder", "TestLivePageIndexSearchDatasetPairedABV3E2E", "TestLiveWikiSearchDatasetPairedABV4E2E"},
			MilestoneDocs: []string{"209-评测协议V4ProvenanceCitation与CompiledFirst检索模式里程碑记录.md"}},
		{Review: "140", ID: "P1-5", Section: 21, Title: "RBAC 重建边界",
			TestSymbols:   []string{"TestBuiltinRBACReconciliationMigrationBackfillsExistingDatabase", "TestBuiltinRoleCatalogMatchesDoc30"},
			MilestoneDocs: []string{"199-MemoryComplianceDeleteAndPrivacyCloseout里程碑记录.md"}},

		// ---- 外部评审 141（doc 137 §24）----
		{Review: "141", ID: "P0-1", Section: 24, Title: "Memory 撤回与 dispatch 竞态",
			TestSymbols:   []string{"TestMemoryDispatchAdmissionUsesCurrentPolicyAndActivePrincipal", "TestMemoryOperationTombstoneCancelsAcceptedDispatch"},
			MilestoneDocs: []string{"199-MemoryComplianceDeleteAndPrivacyCloseout里程碑记录.md"}},
		{Review: "141", ID: "P0-2", Section: 24, Title: "执行期间 Manifest 变化",
			TestSymbols:   []string{"TestMigrationV143CompilationImprovementGateReadiness", "TestHTTPClient_GetDatasetArtifactContract"},
			MilestoneDocs: []string{"197-WikiABAndCompilationArtifactKindIsolation里程碑记录.md"}},
		{Review: "141", ID: "P1-1", Section: 24, Title: "Consent Gate 指标矛盾",
			TestSymbols:   []string{"TestMemoryPrivacyGateDeterministicLabels", "TestMemoryPrivacyGateSealsOperationMetadata"},
			MilestoneDocs: []string{"198-MemoryPrivacyConsentGovernance里程碑记录.md"}},
		{Review: "141", ID: "P1-2", Section: 24, Title: "Principal Mapping 指标",
			TestSymbols:   []string{"TestLiveMemoryPrincipalMappingIdentityGate"},
			MilestoneDocs: []string{"202-MemoryMemberExitInvalidationLiveAndCompilationReplacementDrill里程碑记录.md"}},
		{Review: "141", ID: "P1-3", Section: 24, Title: "unknown 人工恢复",
			TestSymbols:   []string{"TestCompilationUnknownRecoveryRequiresTwoNoMatchResultsAndApproval", "TestMigrationV140CompilationUnknownRecoveryGovernance"},
			MilestoneDocs: []string{"202-MemoryMemberExitInvalidationLiveAndCompilationReplacementDrill里程碑记录.md"}},
		{Review: "141", ID: "P1-4", Section: 24, Title: "端点权限矩阵",
			TestSymbols:   []string{"TestEveryInternalEndpointHasADrill", "TestEndpointRegistryCoversClientMethods", "TestCapabilityReportIncludesEndpointCoverage"},
			MilestoneDocs: []string{"201-RGXProductionEquivalentDeploymentAndBudgetDenialObservability里程碑记录.md"}},

		// ---- 外部评审 143（doc 137 §32）----
		{Review: "143", ID: "P1-1", Section: 32, Title: "EvaluationRun 归属缺口",
			TestSymbols:   []string{"TestEvaluationScopeIsolatesStrategyAndMemoryPolicyFromRelease"},
			MilestoneDocs: []string{"209-评测协议V4ProvenanceCitation与CompiledFirst检索模式里程碑记录.md"}},
		{Review: "143", ID: "P1-2", Section: 32, Title: "Self Policy 租户默认模板",
			TestSymbols:   []string{"TestSelfMemoryPolicyTemplateAndConsent", "TestMigrationV144MemoryPrivacyConsentGovernance"},
			MilestoneDocs: []string{"198-MemoryPrivacyConsentGovernance里程碑记录.md"}},
		{Review: "143", ID: "P2-1", Section: 32, Title: "代码锚点漂移",
			TestSymbols:   nil,
			MilestoneDocs: []string{"209-评测协议V4ProvenanceCitation与CompiledFirst检索模式里程碑记录.md"}},
		{Review: "143", ID: "P2-2", Section: 32, Title: "hybrid 成本口径",
			TestSymbols:   []string{"TestLiveWikiSearchDatasetPairedABV4E2E"},
			MilestoneDocs: []string{"209-评测协议V4ProvenanceCitation与CompiledFirst检索模式里程碑记录.md"}},
		{Review: "143", ID: "P2-3", Section: 32, Title: "pending invalidation 指标",
			TestSymbols:   []string{"TestMemoryOperationTombstoneCancelsAcceptedDispatch"},
			MilestoneDocs: []string{"199-MemoryComplianceDeleteAndPrivacyCloseout里程碑记录.md"}},
		{Review: "143", ID: "P2-4", Section: 32, Title: "Search decision",
			TestSymbols:   []string{"TestSearchGovernedMemoryAllowsActivePolicyAndForcesFilters", "TestEvaluateMemoryConflictsValueConflictDowngrades"},
			MilestoneDocs: []string{"209-评测协议V4ProvenanceCitation与CompiledFirst检索模式里程碑记录.md"}},
		{Review: "143", ID: "P2-5", Section: 32, Title: "run 202 语义",
			TestSymbols:   []string{"TestExecuteRoutedMixedToolAnswer"},
			MilestoneDocs: []string{"201-RGXProductionEquivalentDeploymentAndBudgetDenialObservability里程碑记录.md"}},
		{Review: "143", ID: "P2-6", Section: 32, Title: "knowledge-ops 注册",
			TestSymbols:   []string{"TestBuiltinRoleCatalogMatchesDoc30"},
			MilestoneDocs: []string{"201-RGXProductionEquivalentDeploymentAndBudgetDenialObservability里程碑记录.md"}},
		{Review: "143", ID: "P2-7", Section: 32, Title: "Dataset 收益来源",
			TestSymbols:   []string{"TestCapabilityReportIncludesEndpointCoverage"},
			MilestoneDocs: []string{"201-RGXProductionEquivalentDeploymentAndBudgetDenialObservability里程碑记录.md"}},
		{Review: "143", ID: "P2-8", Section: 32, Title: "文档 lint",
			TestSymbols:   nil,
			MilestoneDocs: []string{"209-评测协议V4ProvenanceCitation与CompiledFirst检索模式里程碑记录.md"}},
	}
}
