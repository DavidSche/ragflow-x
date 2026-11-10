package service

import (
	"context"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/logger"
)

// Compilation engine kind resolution (doc/262 §C, 2026-11-10).
//
// RGX compilation kinds are *product* capabilities (wiki / page_index / tree …).
// The engine kind is what actually decides the compiler variant and the
// compile_kwd the products are indexed under, and the mapping is NOT stable
// across engine versions:
//
//	engine < 1.0.0 : page_index is its own variant; products are readable through
//	                 GET /datasets/{id}/artifacts?page_type=page_index and through
//	                 retrieval with include_knowledge_compilation=true.
//	engine >= 1.0.0: upstream reclassified page_index/timeline/knowledge_graph/
//	                 session_* into the Structure variant (internal/ingestion/
//	                 component/knowledge_compiler/common.KindToVariant). Products
//	                 are entity/relation rows (type_kwd=entity|relation,
//	                 compile_kwd=<kind>, scope_kwd=doc); the artifacts service
//	                 only lists type_kwd=wiki_page and retrieval only returns
//	                 compile_kwd=wiki rows, so a page_index run can never bind
//	                 artifacts on that band.
//
// Evidence: tmp/v1_page_index_drift_evidence.md (controlled probes on 151).
// Rather than projecting structure rows into page_index artifacts (a semantic
// swap that would silently downgrade the payload-hash binding contract), the
// unsupported kinds are rejected at trigger time with a declarative alternative.

// CompilationEngineKindStatus classifies how an RGX kind lands on the engine.
const (
	// CompilationEngineKindSupported means the engine kind produces products that
	// RGX artifact projection can read and bind to the accepted dispatch manifest.
	CompilationEngineKindSupported = "supported"
	// CompilationEngineKindUnverified means the engine kind is not probe-verified
	// on this band: runs are allowed but carry the unverified marker in the run
	// detail so evidence strength is never overstated.
	CompilationEngineKindUnverified = "unverified"
	// CompilationEngineKindUnsupported means the engine kind cannot produce
	// bindable products on this band; runs fail closed at trigger time.
	CompilationEngineKindUnsupported = "unsupported"
)

// Compilation engine bands.
const (
	CompilationEngineBandLegacy  = "legacy"  // < 1.0.0
	CompilationEngineBandV1      = "1.0+"    // >= 1.0.0 (structure reclassification)
	CompilationEngineBandUnknown = "unknown" // engine version not observable
)

// Compilation engine kind reasons (stable audit vocabulary).
const (
	CompilationEngineReasonArtifactAPIWikiOnly    = "engine_artifact_api_wiki_only"
	CompilationEngineReasonArtifactReadUnverified = "engine_artifact_readability_unverified"
	CompilationEngineReasonUnknownKind            = "unknown_compilation_kind"
	CompilationEngineReasonVersionUnobservable    = "engine_version_unobservable"
	CompilationEngineReasonLegacyKindContract     = "legacy_kind_contract"
	CompilationEngineReasonV1WikiArtifactProbe    = "v1_wiki_artifact_probe_verified"
)

// compilationEngineVersion returns the engine version used for compilation kind
// resolution. It prefers the capability registry (populated by the startup
// verification) and falls back to a short live probe so a cold process still
// resolves the correct band instead of guessing.
func (s *Service) compilationEngineVersion(ctx context.Context) string {
	if s.capabilities != nil {
		if version := s.capabilities.detected(); version != "" {
			return version
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	version, err := s.RAGFlow.EngineVersion(probeCtx)
	if err != nil {
		logger.Warn("compilation engine version probe failed", "error", err.Error())
		return ""
	}
	return version
}

// engineKindRejectionMessage renders the fail-closed trigger rejection. It names
// the detected band, the reason and the declarative alternative so callers can
// switch kind instead of retrying into a permanent unknown state.
func engineKindRejectionMessage(resolution CompilationEngineKind) string {
	message := "compilation kind " + resolution.Kind + " is not supported by engine " + resolution.EngineVersion +
		" (band " + resolution.EngineBand + ", reason " + resolution.Reason + ")"
	if resolution.Alternative != "" {
		message += "; use kind " + resolution.Alternative + " instead"
	}
	return message
}

// compilationEngineReadableKindsV1 are the engine kinds whose products the
// v1.0.0 artifacts API can list and whose detail carries content_md_rendered +
// source_chunk_ids + source_doc_ids (probe-verified 2026-11-10: wiki pages
// returned 2–3 artifacts with full binding payload).
var compilationEngineReadableKindsV1 = map[string]struct{}{
	model.CompilationKindWiki: {},
}

// CompilationEngineKind is the resolved engine-side landing of an RGX
// compilation kind for one engine version band.
type CompilationEngineKind struct {
	Kind          string `json:"kind"`
	EngineKind    string `json:"engine_kind"`
	Status        string `json:"status"`
	EngineBand    string `json:"engine_band"`
	EngineVersion string `json:"engine_version,omitempty"`
	Reason        string `json:"reason,omitempty"`
	Alternative   string `json:"alternative,omitempty"`
	Evidence      string `json:"evidence,omitempty"`
}

// Bindable reports whether a run for this resolution may proceed.
func (k CompilationEngineKind) Bindable() bool {
	return k.Status != CompilationEngineKindUnsupported
}

// compilationEngineBand classifies an engine version into a mapping band.
func compilationEngineBand(engineVersion string) string {
	version := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(engineVersion), "v"))
	if version == "" {
		return CompilationEngineBandUnknown
	}
	if compareSemanticVersion(version, "1.0.0") >= 0 {
		return CompilationEngineBandV1
	}
	return CompilationEngineBandLegacy
}

// resolveCompilationEngineKind maps an RGX compilation kind onto the engine for
// the given engine version. It is a pure function so drills, handlers and the
// trigger gate all share one source of truth (doc/262 §C).
func resolveCompilationEngineKind(kind, engineVersion string) CompilationEngineKind {
	kind = normalizeCompilationKind(kind)
	if kind == "" {
		return CompilationEngineKind{
			Kind: kind, EngineKind: "", Status: CompilationEngineKindUnsupported,
			EngineBand: compilationEngineBand(engineVersion), EngineVersion: engineVersion,
			Reason: CompilationEngineReasonUnknownKind,
		}
	}
	resolution := CompilationEngineKind{
		Kind: kind, EngineKind: kind,
		EngineBand: compilationEngineBand(engineVersion), EngineVersion: engineVersion,
	}
	switch resolution.EngineBand {
	case CompilationEngineBandV1:
		if _, readable := compilationEngineReadableKindsV1[kind]; readable {
			resolution.Status = CompilationEngineKindSupported
			resolution.Reason = CompilationEngineReasonV1WikiArtifactProbe
			resolution.Evidence = "artifacts API + retrieval return wiki_page products with content_md_rendered and source_chunk_ids"
			return resolution
		}
		if kind == model.CompilationKindPageIndex {
			// Probe-verified dead end on this band: no artifacts, no retrieval rows.
			resolution.EngineKind = model.CompilationKindWiki
			resolution.Status = CompilationEngineKindUnsupported
			resolution.Reason = CompilationEngineReasonArtifactAPIWikiOnly
			resolution.Alternative = model.CompilationKindWiki
			resolution.Evidence = "page_index is a Structure variant on this band; products are entity/relation rows that neither the artifacts API nor retrieval expose (tmp/v1_page_index_drift_evidence.md)"
			return resolution
		}
		// Not probed on this band: allowed, but explicitly unverified so the run
		// detail never claims payload-hash binding evidence it did not observe.
		resolution.Status = CompilationEngineKindUnverified
		resolution.Reason = CompilationEngineReasonArtifactReadUnverified
		resolution.Evidence = "no artifact readability probe archived for this kind on the 1.0+ band"
		return resolution
	case CompilationEngineBandUnknown:
		// Version unobservable: do not invent a mapping, but keep the legacy
		// contract so a transient version-probe failure cannot block ingestion.
		resolution.Status = CompilationEngineKindUnverified
		resolution.Reason = CompilationEngineReasonVersionUnobservable
		return resolution
	default:
		resolution.Status = CompilationEngineKindSupported
		resolution.Reason = CompilationEngineReasonLegacyKindContract
		return resolution
	}
}
