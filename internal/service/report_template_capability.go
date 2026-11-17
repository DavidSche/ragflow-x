package service

import (
	"context"
	"strings"
)

// CheckReportTemplateCapabilities is §11.4's read-only capability probe for a
// report template. It reuses the exact evaluation machinery the scenario wizard
// check uses (`EvaluateRequirements` + `AggregateVerdicts`), and the same
// contract the create-time recheck applies (`reportKnowledgeContract`: vector
// retrieval at `operational` for every bound dataset, whatever its role), so the
// wizard's preview and the server's creation gate can never disagree about a
// dataset.
//
// Unlike the scenario check it deliberately does NOT persist observation rows:
// the report side's evidence is fed by the account ledger, not by probes, and a
// shadow table would advertise a repair channel report runs do not have. The
// endpoint only discloses — repairs are data, not actions, here.
func (s *Service) CheckReportTemplateCapabilities(
	ctx context.Context, actorID, tenantID, templateKey string, input CapabilityCheckInput,
) (*CapabilityCheckView, error) {
	if err := s.AuthorizeObject(ctx, actorID, "read", "report-template", tenantID, ""); err != nil {
		return nil, err
	}
	template, err := s.resolveReportTemplate(ctx, tenantID, strings.TrimSpace(templateKey), 0)
	if err != nil {
		return nil, err
	}
	datasets, err := normalizeReportDatasets(input.Datasets)
	if err != nil {
		return nil, err
	}
	knowledge := reportKnowledgeContract(datasets)
	view := &CapabilityCheckView{
		TemplateID: template.ID, TemplateVersion: template.Version,
		AppType: strings.TrimSpace(input.AppType), CapabilitySetVersion: CapabilityCheckVersion,
	}
	var anyBlocked, anyLimited, anyRepairable bool
	for _, binding := range datasets {
		profile, probeStatus, err := s.DescribeDataset(ctx, tenantID, binding.DatasetID)
		if err != nil {
			return nil, err
		}
		verdict := EvaluateRequirements(knowledge, binding.Role, *profile)
		verdict.ProbeStatus = probeStatus
		view.Datasets = append(view.Datasets, verdict)
		switch verdict.Verdict {
		case CapabilityVerdictBlocked:
			anyBlocked = true
		case CapabilityVerdictProbeLimited:
			anyLimited = true
		case CapabilityVerdictRepairable:
			anyRepairable = true
		}
	}
	allSupported := len(view.Datasets) > 0
	for _, item := range view.Datasets {
		if item.Verdict != CapabilityVerdictSatisfied {
			allSupported = false
		}
	}
	view.Aggregate = AggregateVerdicts(knowledge, view.Datasets)
	view.Verdict = aggregateVerdict(anyBlocked, anyLimited, anyRepairable, allSupported)
	view.ProbeStatus = aggregateProbeStatus(view.Datasets)
	return view, nil
}
