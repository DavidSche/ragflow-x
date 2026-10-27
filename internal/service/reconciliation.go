package service

import (
	"context"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

// ReconcileExternalResources removes local shadow rows for resources that no
// longer exist in RAGFlow. External list failures abort the run so a transient
// engine outage can never cause mass deletion.
func (s *Service) ReconcileExternalResources(ctx context.Context, tenantID string) error {
	liveDatasets, err := s.RAGFlow.ListDatasets(ctx)
	if err != nil {
		return httperr.New(502, 50208, "ragflow list datasets failed")
	}
	liveDatasetIDs := make(map[string]struct{}, len(liveDatasets))
	for _, dataset := range liveDatasets {
		liveDatasetIDs[dataset.ID] = struct{}{}
	}

	links, err := s.Store.ListByTenant(ctx, tenantID, repository.DatasetFilter{})
	if err != nil {
		return err
	}
	for _, link := range links {
		if _, found := liveDatasetIDs[link.RAGFlowDatasetID]; found {
			continue
		}
		if err := s.Store.DeleteDatasetLinkByTenant(ctx, tenantID, link.ID); err != nil {
			return err
		}
	}

	liveChats, err := s.RAGFlow.ListChats(ctx)
	if err != nil {
		return httperr.New(502, 50245, "ragflow list chats failed")
	}
	liveChatIDs := make(map[string]struct{}, len(liveChats))
	for _, chat := range liveChats {
		liveChatIDs[chat.ID] = struct{}{}
	}

	for page := 1; ; page++ {
		chats, total, err := s.Store.ListChatShadows(ctx, tenantID, false, repository.ChatFilter{}, page, 100)
		if err != nil {
			return err
		}
		for _, chat := range chats {
			if _, found := liveChatIDs[chat.ID]; found {
				continue
			}
			if err := s.Store.DeleteChatShadow(ctx, chat.ID); err != nil {
				return err
			}
		}
		if int64(len(chats)) >= total || len(chats) == 0 {
			break
		}
	}
	return nil
}

// EnqueueResourceReconciliation schedules an idempotent tenant-scoped check.
func (s *Service) EnqueueResourceReconciliation(ctx context.Context, tenantID string) (bool, error) {
	if s.Runner == nil {
		return false, httperr.Internal("async worker not initialized")
	}
	active, err := s.Store.CountActiveJobs(ctx, model.JobKindResourceReconciliation, tenantID)
	if err != nil {
		return false, err
	}
	if active > 0 {
		return false, nil
	}
	key := "resource-reconciliation:" + tenantID
	return s.Runner.Enqueue(ctx, model.JobKindResourceReconciliation, key, tenantID, "", time.Time{}, 3)
}
