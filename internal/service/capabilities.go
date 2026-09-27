package service

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

const (
	CapabilityVerified   = "VERIFIED"
	CapabilityCompatible = "COMPATIBLE"
	CapabilityUnverified = "UNVERIFIED"
	CapabilityDeprecated = "DEPRECATED"
	RuntimeHealthy       = "HEALTHY"
	RuntimeUnavailable   = "UNAVAILABLE"
	RuntimeUnknown       = "UNKNOWN"
)

type RAGFlowCapability struct {
	Name            string     `json:"name"`
	APIProfile      string     `json:"api_profile"`
	Lifecycle       string     `json:"lifecycle"`
	MinVersion      string     `json:"min_version"`
	MaxVerified     string     `json:"max_verified"`
	TrialPattern    string     `json:"trial_pattern,omitempty"`
	ContractTest    bool       `json:"contract_test"`
	Fallback        string     `json:"fallback"`
	Status          string     `json:"status"`
	VerifiedVersion string     `json:"verified_version,omitempty"`
	VerifiedAt      *time.Time `json:"verified_at,omitempty"`
	RuntimeHealth   string     `json:"runtime_health"`
	HealthChangedAt *time.Time `json:"health_changed_at,omitempty"`
	Evidence        string     `json:"evidence"`
}

type RAGFlowCapabilityReport struct {
	Provider             string              `json:"provider"`
	ProviderVersion      string              `json:"provider_version"`
	PinnedVersion        string              `json:"pinned_version"`
	DetectedVersion      string              `json:"detected_version,omitempty"`
	RuntimeHealth        string              `json:"runtime_health"`
	VerifiedAt           *time.Time          `json:"verified_at,omitempty"`
	Items                []RAGFlowCapability `json:"items"`
	UpgradeDecision      string              `json:"upgrade_decision"`
	UpgradeReason        string              `json:"upgrade_reason,omitempty"`
	BlockingCapabilities []string            `json:"blocking_capabilities,omitempty"`
	TrialCapabilities    []string            `json:"trial_capabilities,omitempty"`
	ScenarioRequirements map[string][]string `json:"scenario_requirements"`
	// EndpointCoverage summarizes the provider endpoint stability registry
	// (doc/121 B2): how many upstream endpoints RAGFlow-X depends on are
	// documented in the official API reference versus learned from source and
	// therefore gated by the internal-endpoint live contract drill.
	EndpointCoverage ragflow.EndpointCoverage `json:"endpoint_coverage"`
}

const ragflowAdapterContractVersion = "adapter.v1"

var capabilityScenarioRequirements = map[string][]string{
	model.CapabilityKnowledgeChat:  {"chat", "session", "references"},
	model.CapabilityAgenticTask:    {"agent", "agent_session"},
	model.CapabilityExplicitSearch: {"search_app"},
}

type capabilityRegistry struct {
	mu              sync.RWMutex
	provider        string
	pinnedVersion   string
	detectedVersion string
	runtimeHealth   string
	verifiedAt      *time.Time
	items           map[string]*RAGFlowCapability
}

var pinnedRAGFlowVersion = "0.27.1"

func newCapabilityRegistry() *capabilityRegistry {
	registry := &capabilityRegistry{
		provider:      "ragflow",
		pinnedVersion: pinnedRAGFlowVersion,
		runtimeHealth: RuntimeUnknown,
		items:         map[string]*RAGFlowCapability{},
	}
	declarations := []RAGFlowCapability{
		{Name: "chat", APIProfile: "chat.v1", Lifecycle: "GA", MinVersion: "0.27.0", MaxVerified: "0.27.*", TrialPattern: "0.28.*", ContractTest: true, Fallback: "stable chat contract"},
		{Name: "session", APIProfile: "session.v1", Lifecycle: "GA", MinVersion: "0.27.0", MaxVerified: "0.27.*", TrialPattern: "0.28.*", ContractTest: true, Fallback: "new session"},
		{Name: "agent", APIProfile: "agent.v1", Lifecycle: "GA", MinVersion: "0.27.0", MaxVerified: "0.27.*", TrialPattern: "0.28.*", ContractTest: true, Fallback: "private endpoint disabled"},
		{Name: "agent_session", APIProfile: "agent-session.v1", Lifecycle: "BETA", MinVersion: "0.27.0", MaxVerified: "0.27.*", TrialPattern: "0.28.*", ContractTest: true, Fallback: "non-streaming result"},
		{Name: "search_app", APIProfile: "search-app.v1", Lifecycle: "GA", MinVersion: "0.27.0", MaxVerified: "0.27.*", TrialPattern: "0.28.*", ContractTest: true, Fallback: "chat.v1"},
		{Name: "memory", APIProfile: "memory.v1", Lifecycle: "GA", MinVersion: "0.27.0", MaxVerified: "0.27.*", TrialPattern: "0.28.*", ContractTest: true, Fallback: "empty memory"},
		{Name: "references", APIProfile: "references.v1", Lifecycle: "GA", MinVersion: "0.27.0", MaxVerified: "0.27.*", TrialPattern: "0.28.*", ContractTest: true, Fallback: "no citation"},
		{Name: "stream", APIProfile: "stream.v1", Lifecycle: "GA", MinVersion: "0.27.0", MaxVerified: "0.27.*", TrialPattern: "0.28.*", ContractTest: true, Fallback: "non-streaming"},
		{Name: "structured_output", APIProfile: "structured-output.v1", Lifecycle: "EXPERIMENTAL", MinVersion: "0.27.0", MaxVerified: "0.27.*", TrialPattern: "0.28.*", ContractTest: false, Fallback: "text"},
		{Name: "openai_compatibility", APIProfile: "openai-compatible.v1", Lifecycle: "BETA", MinVersion: "0.27.0", MaxVerified: "0.27.*", TrialPattern: "0.28.*", ContractTest: false, Fallback: "ragflow-native"},
	}
	for i := range declarations {
		declaration := declarations[i]
		declaration.Status = CapabilityUnverified
		declaration.RuntimeHealth = RuntimeUnknown
		declaration.Evidence = "declared compatibility baseline"
		registry.items[declaration.Name] = &declaration
	}
	return registry
}

func compareSemanticVersion(left, right string) int {
	parse := func(value string) []int {
		value = strings.TrimPrefix(strings.TrimSpace(value), "v")
		parts := strings.Split(value, ".")
		result := make([]int, 3)
		for i := 0; i < 3 && i < len(parts); i++ {
			number, err := strconv.Atoi(parts[i])
			if err != nil {
				continue
			}
			result[i] = number
		}
		return result
	}
	a, b := parse(left), parse(right)
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

func versionMatchesPattern(version, pattern string) bool {
	return patternSetMatches(version, pattern)
}

func patternSetMatches(version, patterns string) bool {
	for _, pattern := range strings.Split(patterns, "|") {
		if versionMatchesSinglePattern(version, pattern) {
			return true
		}
	}
	return false
}

func versionMatchesSinglePattern(version, pattern string) bool {
	parts := strings.Split(strings.TrimPrefix(pattern, "v"), ".")
	versionParts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(version, "v"), ""), ".")
	for i, part := range parts {
		if part == "*" {
			return true
		}
		if i >= len(versionParts) || versionParts[i] != part {
			return false
		}
	}
	return len(versionParts) >= len(parts)
}

func (r *capabilityRegistry) snapshot() RAGFlowCapabilityReport {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotLocked()
}

func (r *capabilityRegistry) snapshotLocked() RAGFlowCapabilityReport {
	report := RAGFlowCapabilityReport{
		Provider:             r.provider,
		ProviderVersion:      ragflowAdapterContractVersion,
		PinnedVersion:        r.pinnedVersion,
		DetectedVersion:      r.detectedVersion,
		RuntimeHealth:        r.runtimeHealth,
		VerifiedAt:           r.verifiedAt,
		Items:                make([]RAGFlowCapability, 0, len(r.items)),
		ScenarioRequirements: make(map[string][]string),
		EndpointCoverage:     ragflow.EndpointCoverageSummary(),
	}
	for scenario, requirements := range capabilityScenarioRequirements {
		report.ScenarioRequirements[scenario] = append([]string(nil), requirements...)
	}
	for _, item := range r.items {
		report.Items = append(report.Items, *item)
	}
	sort.Slice(report.Items, func(i, j int) bool {
		return report.Items[i].Name < report.Items[j].Name
	})
	capabilityUpgradeGate(&report)
	return report
}

func (r *capabilityRegistry) markRuntimeUnavailable(err error) RAGFlowCapabilityReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	r.runtimeHealth = RuntimeUnavailable
	r.detectedVersion = ""
	r.verifiedAt = nil
	for _, item := range r.items {
		item.RuntimeHealth = RuntimeUnavailable
		item.HealthChangedAt = &now
		item.Evidence = err.Error()
	}
	return r.snapshotLocked()
}

func (s *Service) EnsureCapabilityCompatibility(ctx context.Context, capability string) (RAGFlowCapabilityReport, error) {
	required, ok := capabilityScenarioRequirements[capability]
	if !ok {
		return RAGFlowCapabilityReport{}, httperr.New(422, 42260, "unsupported capability")
	}
	report, err := s.VerifyRAGFlowCapabilities(ctx)
	if err != nil {
		return report, httperr.New(422, 42260, "capability runtime is unavailable")
	}
	items := make(map[string]RAGFlowCapability, len(report.Items))
	for _, item := range report.Items {
		items[item.Name] = item
	}
	for _, name := range required {
		item, ok := items[name]
		if !ok || item.Status != CapabilityVerified || item.RuntimeHealth != RuntimeHealthy {
			return report, httperr.New(422, 42260, "capability contract is not verified for production release")
		}
	}
	return report, nil
}

func (r *capabilityRegistry) verify(version string) RAGFlowCapabilityReport {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	r.detectedVersion = version
	r.runtimeHealth = RuntimeHealthy
	r.verifiedAt = &now
	for _, item := range r.items {
		item.RuntimeHealth = RuntimeHealthy
		item.HealthChangedAt = &now
		versionSupported := compareSemanticVersion(version, item.MinVersion) >= 0
		compatible := item.ContractTest && versionSupported && versionMatchesPattern(version, item.TrialPattern)
		if !item.ContractTest || !versionSupported || !versionMatchesPattern(version, item.MaxVerified) {
			if compatible {
				item.Status = CapabilityCompatible
				item.VerifiedVersion = ""
				item.VerifiedAt = nil
				item.Evidence = "version is a compatibility candidate; real contract drill is required"
				continue
			}
			item.Status = CapabilityUnverified
			item.VerifiedVersion = ""
			item.VerifiedAt = nil
			item.Evidence = "version outside verified contract matrix"
			continue
		}
		item.Status = CapabilityVerified
		item.VerifiedVersion = version
		item.VerifiedAt = &now
		item.Evidence = "startup version + capability contract baseline"
	}
	return r.snapshotLocked()
}

// ListRAGFlowCapabilities returns the declared/verified matrix without probing
// the engine. Runtime health is last observed state only.
func (s *Service) ListRAGFlowCapabilities() RAGFlowCapabilityReport {
	return s.capabilities.snapshot()
}

// VerifyRAGFlowCapabilities revalidates the pinned compatibility contract. It
// never changes declared API profiles; transient failures only update runtime
// health and leave verified capability identity intact.
func (s *Service) VerifyRAGFlowCapabilities(ctx context.Context) (RAGFlowCapabilityReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	version, err := s.RAGFlow.EngineVersion(ctx)
	if err != nil {
		return s.capabilities.markRuntimeUnavailable(err), err
	}
	return s.capabilities.verify(version), nil
}

func capabilityUpgradeGate(report *RAGFlowCapabilityReport) {
	if report.RuntimeHealth == RuntimeUnavailable {
		report.UpgradeDecision = "BLOCKED_RUNTIME_UNAVAILABLE"
		report.UpgradeReason = "RAGFlow runtime is unavailable; capability contract cannot be trusted"
		return
	}
	if report.DetectedVersion == "" {
		report.UpgradeDecision = "UNKNOWN_RUNTIME"
		report.UpgradeReason = "runtime version has not been verified"
		return
	}
	blocking := make([]string, 0)
	trials := make([]string, 0)
	for _, item := range report.Items {
		if item.ContractTest && item.Status != CapabilityVerified {
			blocking = append(blocking, item.Name)
		}
		if item.Status == CapabilityCompatible {
			trials = append(trials, item.Name)
		}
	}
	if len(blocking) > 0 {
		if versionMatchesPattern(report.DetectedVersion, "0.28.*") {
			report.UpgradeDecision = "BLOCKED_CONTRACT_DRILL_REQUIRED"
			report.UpgradeReason = "0.28 APIs are compatibility candidates until the real contract drill is archived"
		} else {
			report.UpgradeDecision = "BLOCKED_VERSION_NOT_APPROVED"
			report.UpgradeReason = "detected version is outside the approved contract matrix"
		}
		report.BlockingCapabilities = blocking
		report.TrialCapabilities = trials
		return
	}
	report.UpgradeDecision = "READY"
	report.UpgradeReason = "all contract-tested capabilities are verified for the detected version"
}

// StartCapabilityVerification performs the startup contract check in the
// background so a temporarily unavailable engine cannot block API startup.
func (s *Service) StartCapabilityVerification(ctx context.Context) {
	go func() {
		_, _ = s.VerifyRAGFlowCapabilities(ctx)
	}()
}
