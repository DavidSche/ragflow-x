package service

import (
	"encoding/json"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// expectedTrialSnapshot rebuilds the snapshot from the assistant as the product
// reads it back (chat shadow config + bound datasets) and from the template
// payload, deliberately without reusing the creation code. If CreateTrialRun
// drops, defaults or normalises a field, the diff below is non-empty — which is
// how the empty scenario_template_id regression was found in the first place.
func expectedTrialSnapshot(t *testing.T, f *trialFixture) TrialAuthoringSnapshot {
	t.Helper()
	shadow, err := f.svc.Store.GetChatShadow(f.ctx, f.tenant.ID, f.chat.ID, false)
	if err != nil || shadow == nil {
		t.Fatalf("read assistant: %v", err)
	}
	authoring := ChatAuthoring{}
	if err := json.Unmarshal([]byte(shadow.ConfigJSON), &authoring); err != nil {
		t.Fatalf("decode assistant authoring: %v", err)
	}
	payload, err := parseTemplatePayload(f.template.PayloadJSON)
	if err != nil {
		t.Fatalf("decode template payload: %v", err)
	}
	return TrialAuthoringSnapshot{
		AssistantID:            shadow.ID,
		AssistantName:          shadow.Name,
		AssistantKind:          model.TrialAssistantKindChat,
		DatasetIDs:             splitCSV(shadow.DatasetIDs),
		Language:               chatLanguage(authoring.Language),
		PromptConfig:           authoring.PromptConfig,
		LLMID:                  authoring.LLMID,
		RerankID:               authoring.RerankID,
		TopN:                   authoring.TopN,
		TopK:                   authoring.TopK,
		SimilarityThreshold:    authoring.SimilarityThreshold,
		VectorSimilarityWeight: authoring.VectorSimilarityWeight,
		TemplateID:             f.template.ID,
		TemplateKey:            f.template.Key,
		TemplateName:           f.template.Name,
		TemplateVersion:        f.template.LatestVersion,
		PromptPresetID:         payload.PromptPresetID,
		ParameterProfileID:     payload.ParameterProfileID,
		Mode:                   model.TrialModeRetrievalOnly,
		PolicyID:               trialPolicyID,
	}
}

// TestTrialRunSnapshotEqualsAssistantConfiguration is the "snapshot diff = 0"
// exit criterion of v0.4.0 (doc 227 §7 G6): the run a user clicks into must
// describe exactly the assistant configuration, field by field, with no field
// skipped. It also re-derives the policy hash from the stored snapshot, so an
// edited threshold/weight cannot keep a valid-looking hash.
func TestTrialRunSnapshotEqualsAssistantConfiguration(t *testing.T) {
	f := newTrialFixture(t)
	run, err := f.svc.CreateTrialRun(f.ctx, f.tenant.ID, "owner", TrialRunInput{
		ScenarioTemplateID: f.template.ID, AssistantID: f.chat.ID, IdempotencyKey: "diff-zero",
	})
	if err != nil {
		t.Fatalf("create trial run: %v", err)
	}
	final := f.waitForRun(t, run.ID)

	expected := expectedTrialSnapshot(t, f)
	diff := trialSnapshotDiff(final.Snapshot, expected)
	if !diff.Identical {
		for _, change := range diff.Changes {
			t.Errorf("snapshot drift on %s: run=%s assistant=%s", change.Field, change.Run, change.Now)
		}
		t.Fatalf("trial snapshot must equal the assistant configuration (diff=0), got %d change(s)", len(diff.Changes))
	}

	// The comparison must not be vacuous: every json-tagged field of the
	// snapshot type has to participate.
	if fields := trialSnapshotFieldValues(final.Snapshot); len(fields) < 20 {
		t.Fatalf("diff engine only compared %d fields; the snapshot has more", len(fields))
	}

	recomputed, err := trialPolicyHash(final.Snapshot)
	if err != nil {
		t.Fatalf("recompute policy hash: %v", err)
	}
	if recomputed != final.PolicyHash {
		t.Fatalf("policy_hash must bind the stored snapshot: stored=%s recomputed=%s", final.PolicyHash, recomputed)
	}
	tampered := final.Snapshot
	tampered.SimilarityThreshold += 0.05
	tamperedHash, err := trialPolicyHash(tampered)
	if err != nil {
		t.Fatalf("recompute tampered hash: %v", err)
	}
	if tamperedHash == final.PolicyHash {
		t.Fatal("editing similarity_threshold must change policy_hash")
	}
}

// TestTrialSnapshotDiffNamesDriftedFields pins the diff semantics: one drifted
// field yields exactly one named change, dataset reordering is not drift, and
// nested prompt config edits are detected.
func TestTrialSnapshotDiffNamesDriftedFields(t *testing.T) {
	base := TrialAuthoringSnapshot{
		AssistantID: "chat-1", AssistantName: "assistant", AssistantKind: model.TrialAssistantKindChat,
		DatasetIDs: []string{"ds-a", "ds-b"}, Language: "zh",
		PromptConfig: map[string]interface{}{"temperature": 0.1, "system": "be brief"},
		LLMID:        "deepseek-chat", RerankID: "rerank-1",
		TopN: 6, TopK: 1024, SimilarityThreshold: 0.32, VectorSimilarityWeight: 0.35,
		TemplateID: "tpl-1", TemplateKey: "enterprise-qa", TemplateName: "Enterprise QA",
		TemplateVersion: 3, PromptPresetID: "safe", ParameterProfileID: "safe",
		Mode: model.TrialModeRetrievalOnly, PolicyID: trialPolicyID,
	}

	cases := []struct {
		name   string
		mutate func(*TrialAuthoringSnapshot)
		want   []string
	}{
		{name: "none", mutate: func(*TrialAuthoringSnapshot) {}, want: nil},
		{name: "top_n", mutate: func(s *TrialAuthoringSnapshot) { s.TopN++ }, want: []string{"top_n"}},
		{name: "top_k", mutate: func(s *TrialAuthoringSnapshot) { s.TopK++ }, want: []string{"top_k"}},
		{name: "threshold", mutate: func(s *TrialAuthoringSnapshot) { s.SimilarityThreshold = 0.2 }, want: []string{"similarity_threshold"}},
		{name: "weight", mutate: func(s *TrialAuthoringSnapshot) { s.VectorSimilarityWeight = 0.5 }, want: []string{"vector_similarity_weight"}},
		{name: "llm", mutate: func(s *TrialAuthoringSnapshot) { s.LLMID = "other" }, want: []string{"llm_id"}},
		{name: "language", mutate: func(s *TrialAuthoringSnapshot) { s.Language = "en" }, want: []string{"language"}},
		{name: "template_version", mutate: func(s *TrialAuthoringSnapshot) { s.TemplateVersion++ }, want: []string{"template_version"}},
		{name: "dataset_membership", mutate: func(s *TrialAuthoringSnapshot) { s.DatasetIDs = []string{"ds-a"} }, want: []string{"dataset_ids"}},
		{name: "dataset_order_only", mutate: func(s *TrialAuthoringSnapshot) { s.DatasetIDs = []string{"ds-b", "ds-a"} }, want: nil},
		{
			name: "prompt_config_nested",
			mutate: func(s *TrialAuthoringSnapshot) {
				s.PromptConfig = map[string]interface{}{"temperature": 0.9, "system": "be brief"}
			},
			want: []string{"prompt_config"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := base
			current.DatasetIDs = append([]string(nil), base.DatasetIDs...)
			tc.mutate(&current)
			diff := trialSnapshotDiff(base, current)
			if len(diff.Changes) != len(tc.want) {
				t.Fatalf("expected %v, got %+v", tc.want, diff.Changes)
			}
			for i, field := range tc.want {
				if diff.Changes[i].Field != field {
					t.Fatalf("expected change %q, got %q", field, diff.Changes[i].Field)
				}
			}
			if diff.Identical != (len(tc.want) == 0) {
				t.Fatalf("Identical must reflect the change list: %+v", diff)
			}
		})
	}
}
