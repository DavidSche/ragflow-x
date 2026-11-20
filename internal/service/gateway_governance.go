package service

import (
	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

// Gateway governance envelope (doc/301 §5.10 GW-1).
//
// The three API-key routes do not have the same response shape, and the contract
// says so instead of pretending they do:
//
//   - `/v1/chat/completions` is an OpenAI-compatible pass-through of a
//     generation-only call. It does not retrieve, so `citations` and
//     `answerability` are not merely empty there — they are structurally
//     impossible. Its governance travels in response headers only.
//   - `/v1/search-apps/:id/completions` and `/v1/agents/:id/completions` are
//     knowledge routes: they answer from the corpus and already carry the engine
//     `reference` blob. Governance for them goes into `data.governance` and never
//     rewrites `answer` / `reference`, so an existing client keeps working
//     unchanged.
//
// Every field is omitted when there is nothing true to say. `degraded` in
// particular is not "the answer was imperfect" — it means the governance data
// itself could not be produced (answerability evaluation is disabled in this
// deployment), which is exactly the case where shipping `answerable: true` would
// be a fabricated claim.

// GovernanceSchema names the envelope/frame schema so a client can tell which
// governance revision it is reading without parsing fields.
const GovernanceSchema = "ragflowx.governance/v1"

// gatewayGovernanceCitationCap bounds the governance citation list. It is a
// pointer list (index into `reference`), so the cap costs a caller nothing: the
// full chunks are already in the response body.
const gatewayGovernanceCitationCap = 20

// GatewayGovernanceCitation is a pointer into the response's `reference` array.
//
// Index rather than a copy, deliberately: duplicating chunk text into a second
// field would create two places where "what was cited" can disagree, and the
// governance block would then be able to disagree with the body it annotates.
type GatewayGovernanceCitation struct {
	Index        int     `json:"index"`
	DocumentID   string  `json:"document_id,omitempty"`
	DocumentName string  `json:"document_name,omitempty"`
	Similarity   float64 `json:"similarity,omitempty"`
}

// GatewayGovernance is `data.governance` for the knowledge routes.
type GatewayGovernance struct {
	// Answerability is the same per-turn claim knowledge-ops stores
	// (`EvaluateAnswerability`), so the gateway and the console cannot report
	// different honest outcomes for the same answer.
	Answerability *AnswerabilityClaim `json:"answerability,omitempty"`
	// Citations points into `data.reference`; omitted when nothing was cited.
	Citations []GatewayGovernanceCitation `json:"citations,omitempty"`
	// Degraded means the governance block is incomplete, not that the answer is bad.
	Degraded       bool   `json:"degraded,omitempty"`
	DegradedReason string `json:"degraded_reason,omitempty"`
	// Schema is repeated in-body so a client that only parsed JSON (or only read the
	// SSE frame) can version-gate on one string (doc/301 §5.10 GW-1b).
	Schema string `json:"schema"`
}

// GatewaySearchAppData is the `/v1/search-apps/:id/completions` body for the
// gateway. Field names match the console response exactly (`answer`,
// `reference`), which is what keeps GW-1 additive: only `governance` is new.
type GatewaySearchAppData struct {
	Answer     string                   `json:"answer,omitempty"`
	Reference  []map[string]interface{} `json:"reference,omitempty"`
	Governance *GatewayGovernance       `json:"governance,omitempty"`
}

// GatewayAgentData is the `/v1/agents/:id/completions` body for the gateway. The
// embedded provider response keeps every OpenAI-compatible field (`choices`,
// `usage`, `answer`, `reference`) at the same JSON position it had before.
type GatewayAgentData struct {
	ragflow.CompletionResponse
	Governance *GatewayGovernance `json:"governance,omitempty"`
}

// GatewayGovernanceFor renders the governance block for one completed knowledge
// answer.
//
// It never invents a value: when answerability evaluation is disabled the claim
// is left out and `degraded` says why. `citations` is derived from the same
// reference list the caller is returning, so the two cannot disagree.
func (s *Service) GatewayGovernanceFor(answer string, reference []map[string]interface{}) *GatewayGovernance {
	cfg := s.currentKnowledgeOpsConfig()
	governance := &GatewayGovernance{Schema: GovernanceSchema}
	if cfg.AnswerabilityEnabled {
		claim := EvaluateAnswerability(answer, reference, cfg.NoEvidenceSimilarity, 0, cfg.MaxCitationsPerDocument)
		governance.Answerability = &claim
	} else {
		governance.Degraded = true
		governance.DegradedReason = "answerability_disabled"
	}
	governance.Citations = gatewayGovernanceCitations(reference)
	if governance.Citations == nil && governance.Answerability == nil && !governance.Degraded {
		// Nothing to say and nothing wrong: the caller omits the whole block rather
		// than sending `{}`, which a client cannot distinguish from "supported".
		return nil
	}
	return governance
}

// gatewayGovernanceCitations projects the engine reference list into pointer
// entries. It is a projection, not a filter: an entry whose document identity is
// empty is still listed by index, because "this chunk was cited but the engine
// did not name the document" is a fact the caller should see.
func gatewayGovernanceCitations(reference []map[string]interface{}) []GatewayGovernanceCitation {
	if len(reference) == 0 {
		return nil
	}
	limit := len(reference)
	if limit > gatewayGovernanceCitationCap {
		limit = gatewayGovernanceCitationCap
	}
	out := make([]GatewayGovernanceCitation, 0, limit)
	for index := 0; index < limit; index++ {
		entry := reference[index]
		if entry == nil {
			continue
		}
		out = append(out, GatewayGovernanceCitation{
			Index:        index,
			DocumentID:   gatewayCitationIdentity(entry),
			DocumentName: gatewayCitationName(entry),
			Similarity:   gatewayCitationSimilarity(entry),
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// gatewayCitationIdentity is `documentIdentity` plus a chunk-id fallback.
//
// The governance list points at individual citations, so an entry that the engine did
// not attribute to a document can still be resolvable by its chunk. `documentIdentity`
// itself is left alone: it feeds the answerability claim, where a chunk id is not a
// document and counting it as one would inflate `distinct_docs`.
func gatewayCitationIdentity(entry map[string]interface{}) string {
	if identity := documentIdentity(entry); identity != "" {
		return identity
	}
	for _, key := range []string{"chunk_id", "id"} {
		if value := stringFromAny(entry[key]); value != "" {
			return value
		}
	}
	return ""
}

// gatewayCitationName prefers the human-readable document title over the identity
// keys `documentIdentity` accepts, which can return an opaque id.
func gatewayCitationName(entry map[string]interface{}) string {
	for _, key := range []string{"document_name", "doc_name", "name", "title", "document_keyword"} {
		if value := stringFromAny(entry[key]); value != "" {
			return value
		}
	}
	return ""
}

// gatewayCitationSimilarity reads the retrieval score under any of the keys the
// engine versions have used. Zero is the honest answer when none is present: a
// caller must not read "no score reported" as "score 0.0 out of 1".
func gatewayCitationSimilarity(entry map[string]interface{}) float64 {
	for _, key := range []string{"similarity", "score", "vector_similarity", "term_similarity"} {
		if value, ok := entry[key]; ok {
			if score := floatValue(value); score > 0 {
				return score
			}
		}
	}
	return 0
}

// GatewayPolicyHeader is the `X-RGX-Policy` value on every gateway response: the
// contract version this deployment enforces. A caller comparing it to its own
// pinned contract version can detect a governance change without parsing bodies.
const GatewayPolicyHeader = "X-RGX-Policy"

// GatewayDegradedHeader is set to "true" only when governance was incomplete; it
// is deliberately absent otherwise, because a header that is always present and
// almost always "false" gets ignored.
const GatewayDegradedHeader = "X-RGX-Degraded"

// DegradedForHeader reports whether the header should be emitted at all.
func (g *GatewayGovernance) DegradedForHeader() bool {
	return g != nil && g.Degraded
}
