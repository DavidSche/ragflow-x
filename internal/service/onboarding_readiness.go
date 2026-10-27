package service

import (
	"context"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// Onboarding milestone keys. The frontend maps these to localized copy; the
// backend returns no prose so the API never becomes a second i18n source.
const (
	MilestoneDatasetReady     = "dataset_ready"
	MilestoneAssistantReady   = "assistant_ready"
	MilestoneTrialCompleted   = "trial_completed"
	MilestoneFirstCitedAnswer = "first_cited_answer"
)

// OnboardingMilestone is one derived readiness fact.
type OnboardingMilestone struct {
	Key      string                 `json:"key"`
	Done     bool                   `json:"done"`
	Evidence map[string]interface{} `json:"evidence"`
}

// OnboardingReadiness is the four-milestone projection. It is fully derived:
// no new table, no stored state, recomputed on every read (doc/228 §3.1
// T7.1′, 0 new migrations).
type OnboardingReadiness struct {
	AssistantID        string                `json:"assistant_id"`
	ScenarioTemplateID string                `json:"scenario_template_id,omitempty"`
	Completed          int                   `json:"completed"`
	Total              int                   `json:"total"`
	Next               string                `json:"next,omitempty"`
	Milestones         []OnboardingMilestone `json:"milestones"`
}

// OnboardingReadiness derives the four onboarding milestones for one
// assistant. Cross-tenant reads are not supported: a trial run and its
// evidence are always workspace-local.
func (s *Service) OnboardingReadiness(ctx context.Context, tenantID, assistantID string) (*OnboardingReadiness, error) {
	out := &OnboardingReadiness{
		AssistantID: assistantID,
		Total:       4,
		Milestones: []OnboardingMilestone{
			{Key: MilestoneDatasetReady, Evidence: map[string]interface{}{}},
			{Key: MilestoneAssistantReady, Evidence: map[string]interface{}{}},
			{Key: MilestoneTrialCompleted, Evidence: map[string]interface{}{}},
			{Key: MilestoneFirstCitedAnswer, Evidence: map[string]interface{}{}},
		},
	}
	var chat *model.ChatShadow
	if assistantID != "" {
		found, err := s.Store.GetChatShadow(ctx, tenantID, assistantID, false)
		if err != nil {
			return nil, err
		}
		chat = found
	}

	// ① knowledge ready: some dataset in scope finished parsing.
	datasetEvidence := out.Milestones[0].Evidence
	datasets := []string{}
	if chat != nil {
		datasets = splitCSV(chat.DatasetIDs)
	}
	parsed := 0
	if len(datasets) > 0 {
		for _, datasetID := range datasets {
			tasks, _, err := s.Store.ListTasksByDataset(ctx, tenantID, datasetID, 1, 50)
			if err != nil {
				return nil, err
			}
			for _, task := range tasks {
				if task.TaskType == model.TaskTypeParse && task.Status == model.TaskStatusDone {
					parsed++
					break
				}
			}
		}
	} else {
		summary, err := s.Store.ParseTaskStatusSummary(ctx, tenantID)
		if err != nil {
			return nil, err
		}
		parsed = int(summary.Done)
	}
	datasetEvidence["bound_datasets"] = len(datasets)
	datasetEvidence["parsed_datasets"] = parsed
	out.Milestones[0].Done = parsed > 0 || (chat == nil && len(datasets) == 0 && parsed > 0)

	// ② assistant usable: active chat with at least one dataset bound.
	if chat != nil {
		out.ScenarioTemplateID = chat.ScenarioTemplateID
	}
	assistantEvidence := out.Milestones[1].Evidence
	assistantEvidence["exists"] = chat != nil
	assistantEvidence["bound_datasets"] = len(datasets)
	assistantEvidence["active"] = chat != nil && chat.Status == model.TenantStatusActive
	out.Milestones[1].Done = chat != nil && chat.Status == model.TenantStatusActive && len(datasets) > 0

	// ③ trial completed: a COMPLETED run whose template version equals the
	// assistant's pinned version, i.e. the trial actually exercised the config
	// the assistant ships with.
	trialEvidence := out.Milestones[2].Evidence
	trialEvidence["template_linked"] = chat != nil && chat.ScenarioTemplateID != ""
	trialEvidence["template_version"] = int64(0)
	if chat != nil {
		trialEvidence["template_version"] = chat.ScenarioTemplateVersion
	}
	if chat != nil && chat.ScenarioTemplateID != "" {
		run, err := s.Store.LatestCompletedTrialRun(ctx, tenantID, chat.ScenarioTemplateID, chat.ScenarioTemplateVersion, chat.ID)
		if err != nil {
			return nil, err
		}
		if run != nil {
			trialEvidence["run_id"] = run.ID
			trialEvidence["eval_set_hash"] = run.EvalSetHash
			out.Milestones[2].Done = true
		}
	}

	// ④ first cited answer: a completed chat turn that returned citations.
	answerEvidence := out.Milestones[3].Evidence
	if chat != nil {
		count, err := s.Store.CountCitedChatAnswers(ctx, tenantID, assistantID)
		if err != nil {
			return nil, err
		}
		answerEvidence["cited_answers"] = count
		out.Milestones[3].Done = count > 0
	}

	for _, milestone := range out.Milestones {
		if milestone.Done {
			out.Completed++
		} else if out.Next == "" {
			out.Next = milestone.Key
		}
	}
	return out, nil
}
