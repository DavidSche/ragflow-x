package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
	"github.com/ragflow-x/ragflow-x/internal/repository"
)

func artifactRef(artifact *model.ResourceSyncArtifact) string {
	if artifact == nil {
		return ""
	}
	return "artifact:" + artifact.ID
}

func artifactIDFromRef(ref string) string {
	return strings.TrimPrefix(ref, "artifact:")
}

func (s *Service) createResourceSyncArtifact(ctx context.Context, store repository.Store, artifactType, resourceType string, content interface{}, runID, itemID, bindingVersionID string) (*model.ResourceSyncArtifact, error) {
	contentJSON, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}
	contentHash := canonicalJSONHash(content)
	existing, err := store.GetResourceSyncArtifactByHash(ctx, artifactType, contentHash)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		now := time.Now().UTC()
		if err := s.extendResourceSyncArtifactLifecycle(ctx, store, existing.ID, now); err != nil {
			return nil, err
		}
		lifecycle, err := store.GetResourceSyncArtifactLifecycle(ctx, existing.ID)
		if err != nil {
			return nil, err
		}
		if lifecycle != nil {
			existing.RetainUntil = lifecycle.RetainUntil
		}
		return existing, nil
	}
	now := time.Now().UTC()
	artifact := &model.ResourceSyncArtifact{
		ID: id.New(), ArtifactType: artifactType, ResourceType: resourceType,
		SourceID: ResourceSyncSourceID, SourceCredentialVersion: ResourceSyncCredentialVersion,
		SchemaVersion: 1, ContentHash: contentHash, ContentJSON: string(contentJSON),
		SyncRunID: runID, SyncItemID: itemID, BindingVersionID: bindingVersionID,
		RetainUntil: s.resourceSyncArtifactRetainUntil(now),
		CreatedAt:   now, UpdatedAt: now,
	}
	lifecycle := &model.ResourceSyncArtifactLifecycle{
		ArtifactID: artifact.ID, RetainUntil: artifact.RetainUntil,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.CreateResourceSyncArtifact(ctx, artifact, lifecycle); err != nil {
		return nil, err
	}
	return artifact, nil
}

func (s *Service) extendResourceSyncArtifactLifecycle(ctx context.Context, store repository.Store, artifactID string, now time.Time) error {
	current, err := store.GetResourceSyncArtifactLifecycle(ctx, artifactID)
	if err != nil {
		return err
	}
	next := s.resourceSyncArtifactRetainUntil(now)
	if current != nil && next != nil && current.RetainUntil != nil && !current.RetainUntil.Before(*next) {
		return nil
	}
	return store.ExtendResourceSyncArtifactLifecycle(ctx, &model.ResourceSyncArtifactLifecycle{
		ArtifactID: artifactID, RetainUntil: next, CreatedAt: now, UpdatedAt: now,
	})
}

func (s *Service) convergeResourceSyncArtifactLifecycles(ctx context.Context) error {
	const pageSize = 100
	for page := 1; ; page++ {
		artifacts, total, err := s.Store.ListResourceSyncArtifacts(ctx, page, pageSize)
		if err != nil {
			return err
		}
		lifecycleIDs := make([]string, 0, len(artifacts))
		for _, artifact := range artifacts {
			lifecycleIDs = append(lifecycleIDs, artifact.ID)
		}
		lifecycles, err := s.Store.ListResourceSyncArtifactLifecyclesByIDs(ctx, lifecycleIDs)
		if err != nil {
			return err
		}
		lifecycleByID := make(map[string]model.ResourceSyncArtifactLifecycle, len(lifecycles))
		for _, lifecycle := range lifecycles {
			lifecycleByID[lifecycle.ArtifactID] = lifecycle
		}
		for index := range artifacts {
			artifact := &artifacts[index]
			lifecycle, ok := lifecycleByID[artifact.ID]
			if !ok {
				return httperr.Internal("sync artifact lifecycle is unavailable")
			}
			if lifecycle.RetainUntil == nil {
				continue
			}
			next := s.resourceSyncArtifactRetainUntil(artifact.CreatedAt.UTC())
			if next != nil && lifecycle.RetainUntil != nil && !lifecycle.RetainUntil.Before(*next) {
				continue
			}
			now := time.Now().UTC()
			if err := s.Store.ExtendResourceSyncArtifactLifecycle(ctx, &model.ResourceSyncArtifactLifecycle{
				ArtifactID: artifact.ID, RetainUntil: next, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				return err
			}
		}
		if int64(page*pageSize) >= total {
			return nil
		}
	}
}

func (s *Service) resourceSyncArtifactRetainUntil(now time.Time) *time.Time {
	retention := s.currentRetentionPolicy()
	approvalDays := s.currentApprovalConfig().RetentionDays
	days := approvalDays
	if retention.Enabled {
		for _, classDays := range []int{retention.AuditDays, retention.UsageDays, retention.FeedbackDays, retention.JobDays} {
			if classDays > days {
				days = classDays
			}
		}
	}
	if days <= 0 {
		return nil
	}
	retainUntil := now.AddDate(0, 0, days)
	return &retainUntil
}

func (s *Service) getRunSnapshot(ctx context.Context, run *model.SyncRun) (map[string]json.RawMessage, error) {
	snapshot := map[string]json.RawMessage{}
	raw := run.SourceSnapshotJSON
	if artifactID := artifactIDFromRef(run.SourceSnapshotRef); artifactID != "" && artifactID != run.SourceSnapshotRef {
		artifact, err := s.GetResourceSyncArtifact(ctx, artifactID)
		if err != nil {
			return nil, err
		}
		if artifact == nil || artifact.ArtifactType != "sync_snapshot" {
			return nil, httperr.Internal("sync snapshot artifact is unavailable")
		}
		raw = artifact.ContentJSON
	}
	if raw == "" {
		return snapshot, nil
	}
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return nil, httperr.Internal("invalid sync snapshot")
	}
	return snapshot, nil
}

func (s *Service) GetResourceSyncArtifact(ctx context.Context, artifactID string) (*model.ResourceSyncArtifact, error) {
	if artifactID == "" {
		return nil, httperr.BadRequest(40099, "artifact id is required")
	}
	artifact, err := s.Store.GetResourceSyncArtifact(ctx, artifactID)
	if err != nil {
		return nil, err
	}
	if artifact == nil {
		return nil, httperr.NotFound("sync artifact not found")
	}
	lifecycle, err := s.Store.GetResourceSyncArtifactLifecycle(ctx, artifactID)
	if err != nil {
		return nil, err
	}
	if lifecycle == nil {
		return nil, httperr.Internal("sync artifact lifecycle is unavailable")
	}
	artifact.RetainUntil = lifecycle.RetainUntil
	if lifecycle.RetainUntil != nil && lifecycle.RetainUntil.Before(time.Now().UTC()) {
		return nil, httperr.NotFound("sync artifact retention has expired")
	}
	return artifact, nil
}
