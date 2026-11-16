package service

import "testing"

// citationLocatorKnowledge mirrors the `policy` / `contract-compliance` seed
// contract: structured chunking is what supplies citation anchors.
func citationLocatorKnowledge() *ScenarioTemplateKnowledge {
	return &ScenarioTemplateKnowledge{
		Datasets: []ScenarioKnowledgeDataset{{
			Role: scenarioDatasetRolePrimary,
			ParseIntent: &ScenarioParseIntent{
				Mode: "builtin", ChunkMethod: "paper",
			},
		}},
		RequiredCapabilities: []ScenarioCapabilityRequirement{requireCitationConfig},
	}
}

func generalDatasetProfile() DatasetCapabilityProfile {
	// A `general` dataset carries no structured chunking, so the probe emits no
	// citation_locator evidence at all.
	return DatasetCapabilityProfile{
		DatasetID: "ds-1", ChunkMethod: "general",
		DocumentCount: 2, ParsedCount: 2,
		Capabilities: []CapabilityEvidence{{
			Capability: "vector", Status: CapabilityStatusOperational, Source: "dataset.config.embedding_model",
		}},
	}
}

func repairActions(verdict DatasetCapabilityVerdict) []string {
	out := make([]string, 0, len(verdict.Repairs))
	for _, repair := range verdict.Repairs {
		out = append(out, repair.Action)
	}
	return out
}

// A missing citation locator on a `general` dataset used to be reported twice:
// once as `apply_parse_profile` (the chunk-method rewrite that actually fixes
// it) and once as `reconfigure_dataset` (which the wizard cannot drive at all).
// The wizard rendered the undrivable one first, so the only button that could
// close the gap was pushed below a repair with no button (E2E 2026-11-16).
func TestCitationLocatorGapIsOnlyAChunkMethodRepair(t *testing.T) {
	knowledge := citationLocatorKnowledge()
	verdict := EvaluateRequirements(knowledge, scenarioDatasetRolePrimary, generalDatasetProfile())

	actions := repairActions(verdict)
	if len(actions) != 1 || actions[0] != "apply_parse_profile" {
		t.Fatalf("repairs = %v, want exactly [apply_parse_profile]", actions)
	}
	if verdict.Repairs[0].ChunkMethod != "paper" {
		t.Fatalf("repair target = %q, want paper", verdict.Repairs[0].ChunkMethod)
	}
	if verdict.Repairs[0].RequiresReparse != true {
		t.Fatalf("parsed dataset repair must demand a re-parse acknowledgement")
	}
	var citation RequirementVerdict
	for _, item := range verdict.Requirements {
		if item.Capability == "citation_locator" {
			citation = item
		}
	}
	if citation.Satisfied {
		t.Fatalf("citation_locator must stay unsatisfied on general chunking")
	}
	if citation.Reason != "chunk_method_mismatch" {
		t.Fatalf("reason = %q, want chunk_method_mismatch", citation.Reason)
	}
	if verdict.Verdict != CapabilityVerdictRepairable {
		t.Fatalf("verdict = %q, want %q", verdict.Verdict, CapabilityVerdictRepairable)
	}
}

// Once the dataset is rewritten with the template's chunk method the same
// requirement is satisfied and no repair is offered — the loop has to end.
func TestCitationLocatorSatisfiedAfterChunkRewrite(t *testing.T) {
	profile := generalDatasetProfile()
	profile.ChunkMethod = "paper"
	profile.Capabilities = append(profile.Capabilities, parserConfigEvidence(nil, "paper")...)

	verdict := EvaluateRequirements(citationLocatorKnowledge(), scenarioDatasetRolePrimary, profile)
	if verdict.Verdict != CapabilityVerdictSatisfied {
		t.Fatalf("verdict = %q (repairs %v), want %q", verdict.Verdict, repairActions(verdict), CapabilityVerdictSatisfied)
	}
	if actions := repairActions(verdict); len(actions) != 0 {
		t.Fatalf("repairs = %v, want none", actions)
	}
}

// A chunk method the probe could not read must not be guessed at: with no
// observed method the requirement keeps the old `below_min_level` shape and the
// operator-driven reconfigure repair, rather than claiming a re-chunk it cannot
// name.
func TestCitationLocatorWithoutObservedChunkMethodStaysManual(t *testing.T) {
	profile := generalDatasetProfile()
	profile.ChunkMethod = ""

	verdict := EvaluateRequirements(citationLocatorKnowledge(), scenarioDatasetRolePrimary, profile)
	if actions := repairActions(verdict); len(actions) != 1 || actions[0] != "reconfigure_dataset" {
		t.Fatalf("repairs = %v, want [reconfigure_dataset]", actions)
	}
}
