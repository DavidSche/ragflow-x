package service

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Canonical default-model types on the provider boundary (doc/269 §3.1). The
// order is display order; aliases (asr/vision/img2txt) are normalised at the
// provider boundary and never appear here.
var canonicalDefaultModelTypes = []string{
	"chat", "embedding", "rerank", "speech2text", "image2text", "tts", "ocr", "decision",
}

// knownOfficialBaselineTypes is the deterministic lower bound of the default
// model types supported by each known official RAGFlow baseline version
// (source evidence, doc/269 §3.1/§3.2: user_tenant.go defaultModelFields case
// set). decision is absent from v1.0.0. Keyed by the raw version string as
// returned by ProbeVersion (without the "v" prefix). This is a *known fact* —
// used as a floor — and must NEVER absorb a build-specific enhancement (v9
// self-audit: merging decision in here would silently claim capability no
// official v1.0.0 has).
var knownOfficialBaselineTypes = map[string][]string{
	"1.0.0": {"chat", "embedding", "rerank", "speech2text", "image2text", "tts", "ocr"},
}

// unknownBuildConservativeTypes is the only safe claim for a version/build we
// do not recognise: chat + embedding. Never over-estimate (review P1-1).
var unknownBuildConservativeTypes = []string{"chat", "embedding"}

// defaultCapabilityTTL bounds how long a lazy version probe / negative binding
// survives before re-resolution. Short enough that an upgraded RAGFlow is
// noticed, long enough that GET default-types doesn't hammer /system/version.
const defaultCapabilityTTL = 30 * time.Second

// buildIdentityKey uniquely identifies a RAGFlow build for capability caching.
// The cache MUST be scoped per build (review P1-1 / v9): switching the
// connected RAGFlow build must never leak another build's capability knowledge
// — that is exactly how decision would be wrongly claimed for an official
// v1.0.0 that lacks it.
type buildIdentityKey string

// capabilityCacheEntry is the per-build capability knowledge.
type capabilityCacheEntry struct {
	// source is a label of which tier produced supported (registered built /
	// patch profile / official baseline / conservative).
	source    string
	version   string // captured at resolve time (may be "" when unknown)
	supported map[string]bool
	// negative binds a type deemed unsupported by upstream "model type X is
	// invalid" feedback, with the observation time so it can expire (an
	// upgraded build might add the type back).
	negative  map[string]time.Time
	fetchedAt time.Time
}

func (e *capabilityCacheEntry) supports(t string) bool {
	if at, bad := e.negative[t]; bad && time.Since(at) < negativeBindingTTL {
		return false
	}
	return e.supported[t]
}

// negativeBindingTTL bounds upstream "type invalid" feedback.
const negativeBindingTTL = 10 * time.Minute

// defaultModelCapabilityRegistry holds per-build capability knowledge on the
// Service. It is a single instance-level cache because the service talks to
// exactly one RAGFlow deployment at a time.
type defaultModelCapabilityRegistry struct {
	mu      sync.Mutex
	byBuild map[buildIdentityKey]*capabilityCacheEntry
}

func newDefaultModelCapabilityRegistry() *defaultModelCapabilityRegistry {
	return &defaultModelCapabilityRegistry{byBuild: map[buildIdentityKey]*capabilityCacheEntry{}}
}

// normalizeBuildKey assembles the build identity key from whatever identity
// facts we have. version is always present (possibly "" for unknown); image
// build ID is not discoverable through any current upstream interface, so it is
// deliberately absent rather than fabricated (ever project rule: no invented
// dependencies, review P0-4). patchProfile is an optional local hotfix profile.
func normalizeBuildKey(version string, patchProfile string) buildIdentityKey {
	return buildIdentityKey(version + "|" + strings.TrimSpace(patchProfile))
}

// resolve returns (entry, computedWithKnownVersion). When the version probe
// fails, version stays "" and the conservative tier applies.
func (r *defaultModelCapabilityRegistry) resolve(version string, patchProfile string) *capabilityCacheEntry {
	key := normalizeBuildKey(version, patchProfile)
	r.mu.Lock()
	defer r.mu.Unlock()
	if ent, ok := r.byBuild[key]; ok && time.Since(ent.fetchedAt) < defaultCapabilityTTL {
		return ent
	}
	ent := r.compute(version, patchProfile)
	r.byBuild[key] = ent
	return ent
}

// recordNegative inserts an upstream "model type X is invalid" observation into
// the entry for the given build. It upgrades existing entries in place and
// creates a conservative entry when none exists, so a later supports() call
// short-circuits to unsupported.
func (r *defaultModelCapabilityRegistry) recordNegative(version string, patchProfile string, modelType string) {
	key := normalizeBuildKey(version, patchProfile)
	r.mu.Lock()
	defer r.mu.Unlock()
	ent, ok := r.byBuild[key]
	if !ok || time.Since(ent.fetchedAt) >= defaultCapabilityTTL {
		ent = r.compute(version, patchProfile)
		ent.fetchedAt = time.Now()
		r.byBuild[key] = ent
	}
	if ent.negative == nil {
		ent.negative = map[string]time.Time{}
	}
	ent.negative[modelType] = time.Now()
}

// compute applies the 5-tier priority (doc/269 §4.5 + v9 self-audit ⑧(b)):
//
//  1. explicitly registered build identity (knownBuilds) -> its types
//  2. registered mirror/patch profile -> its types
//  3. known official baseline version -> its deterministic types (floor)
//  4. unknown version/build -> conservative {chat, embedding}
//  5. (negative bindings applied at supports() time, overriding positives)
func (r *defaultModelCapabilityRegistry) compute(version string, patchProfile string) *capabilityCacheEntry {
	ent := &capabilityCacheEntry{version: version, supported: map[string]bool{}, fetchedAt: time.Now()}

	if types, ok := registeredBuildTypes[normalizeBuildKey(version, patchProfile)]; ok {
		ent.source = "registered-build"
		for _, t := range types {
			ent.supported[t] = true
		}
		return ent
	}
	if types, ok := registeredPatchProfileTypes[strings.TrimSpace(patchProfile)]; ok {
		ent.source = "patch-profile"
		for _, t := range types {
			ent.supported[t] = true
		}
		return ent
	}
	if v := strings.TrimPrefix(version, "v"); v != "" {
		if types, ok := knownOfficialBaselineTypes[v]; ok {
			ent.source = "official-baseline"
			for _, t := range types {
				ent.supported[t] = true
			}
			return ent
		}
	}
	// Unknown version/build -> conservative floor.
	ent.source = "conservative"
	for _, t := range unknownBuildConservativeTypes {
		ent.supported[t] = true
	}
	return ent
}

// registeredBuildTypes / registeredPatchProfileTypes are the explicit
// "knownBuilds" capability table (doc/269 §4.5 L2): a deployer that runs a
// custom build (e.g. v1.0.0 plus a decision hotfix) registers it here; entries
// matching the exact build/patch identity win over the official-baseline and
// conservative tiers. Kept empty-by-default so official behaviour stays the
// source-of-truth floor; additions are code-reviewed, never inferred.
var registeredBuildTypes = map[buildIdentityKey][]string{}
var registeredPatchProfileTypes = map[string][]string{}

// --- Service-facing methods -------------------------------------------------

// probeBuildVersion returns the connected RAGFlow version (without "v"), or ""
// when the probe fails. Results are cached briefly so capability resolution is
// not a hot network path.
func (s *Service) probeBuildVersion(ctx context.Context) string {
	if v := s.ragflowVersionCache.memo(); v != "" {
		return v
	}
	v, err := s.RAGFlow.EngineVersion(ctx)
	if err != nil {
		return ""
	}
	s.ragflowVersionCache.set(v)
	return v
}

// defaultModelTypeSupported reports whether the canonical model type can be set
// as a tenant default on the currently connected RAGFlow build.
func (s *Service) defaultModelTypeSupported(ctx context.Context, modelType string) bool {
	for _, t := range canonicalDefaultModelTypes {
		if t == modelType {
			entry := s.defaultCapabilities.resolve(s.probeBuildVersion(ctx), "")
			return entry.supports(modelType)
		}
	}
	// Not in the canonical vocabulary at all -> unsupported.
	return false
}

// recordDefaultModelTypeInvalid feeds upstream "model type X is invalid"
// feedback back into the capability cache, scoped to the current build.
func (s *Service) recordDefaultModelTypeInvalid(ctx context.Context, modelType string) {
	version := ""
	if pv := s.probeBuildVersion(ctx); pv != "" {
		version = pv
	}
	if version == "" {
		return // cannot bind a negative to any build; do not pollute
	}
	s.defaultCapabilities.recordNegative(version, "", modelType)
}

// ragflowVersionCache is a tiny memo for the version probe on the Service.
type ragflowVersionCache struct {
	mu sync.Mutex
	v  string
	at time.Time
}

func (c *ragflowVersionCache) memo() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.v != "" && time.Since(c.at) < defaultCapabilityTTL*2 {
		return c.v
	}
	return ""
}

func (c *ragflowVersionCache) set(v string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.v = v
	c.at = time.Now()
}
