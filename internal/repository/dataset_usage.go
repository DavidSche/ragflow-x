package repository

import (
	"context"
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

// DatasetUsage describes who else would be affected by a destructive dataset
// change. Capability checks (doc/276 §7.3) surface this *before* a repair runs,
// because RAGFlow's dataset config is global: rewriting chunk_method changes
// retrieval for every application bound to the dataset.
type DatasetUsage struct {
	DatasetID string
	ChatIDs   []string
	SearchIDs []string
	// ReleaseIDs are assistant releases holding an ACTIVE binding to the
	// dataset. A published release that still references the dataset is the
	// strongest signal that a repair needs an explicit acknowledgement.
	ReleaseIDs []string
}

// Shared returns the total number of distinct consumers.
func (u DatasetUsage) Shared() int {
	return len(u.ChatIDs) + len(u.SearchIDs) + len(u.ReleaseIDs)
}

// CountDatasetSharedUsage scans the three ownership projections that reference
// datasets: chat shadows (comma separated dataset_ids), search app shadows
// (same encoding) and active dataset binding versions (single dataset_id).
func (s *store) CountDatasetSharedUsage(ctx context.Context, tenantID, datasetID string) (DatasetUsage, error) {
	usage := DatasetUsage{DatasetID: datasetID}
	if datasetID == "" {
		return usage, nil
	}
	var chats []model.ChatShadow
	if err := s.WithContext(ctx).
		Where("tenant_id = ? AND dataset_ids LIKE ?", tenantID, "%"+datasetID+"%").
		Find(&chats).Error; err != nil {
		return usage, err
	}
	for _, chat := range chats {
		if datasetIDIn(chat.DatasetIDs, datasetID) {
			usage.ChatIDs = append(usage.ChatIDs, chat.ID)
		}
	}
	var apps []model.SearchAppShadow
	if err := s.WithContext(ctx).
		Where("tenant_id = ? AND dataset_ids LIKE ?", tenantID, "%"+datasetID+"%").
		Find(&apps).Error; err != nil {
		return usage, err
	}
	for _, app := range apps {
		if datasetIDIn(app.DatasetIDs, datasetID) {
			usage.SearchIDs = append(usage.SearchIDs, app.ID)
		}
	}
	var bindings []model.DatasetBindingVersion
	if err := s.WithContext(ctx).
		Where("tenant_id = ? AND dataset_id = ? AND status = ?", tenantID, datasetID, "ACTIVE").
		Find(&bindings).Error; err != nil {
		return usage, err
	}
	for _, binding := range bindings {
		usage.ReleaseIDs = append(usage.ReleaseIDs, binding.AssistantReleaseID)
	}
	sort.Strings(usage.ChatIDs)
	sort.Strings(usage.SearchIDs)
	sort.Strings(usage.ReleaseIDs)
	return usage, nil
}

// SetDatasetPushdownEnabled flips the local metadata-pushdown policy for one
// dataset (doc/276 §7.4 / S13). This is a platform execution policy, not an
// upstream dataset config: RAGFlow never learns about the flag itself, it only
// receives the meta_fields backfill that the flag triggers.
func (s *store) SetDatasetPushdownEnabled(ctx context.Context, tenantID, datasetID string, enabled bool) error {
	result := s.WithContext(ctx).Model(&model.DatasetLink{}).
		Where("tenant_id = ? AND id = ?", tenantID, datasetID).
		Update("pushdown_enabled", enabled)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// datasetIDIn is an exact membership test over the comma separated dataset_ids
// encoding used by the shadow projections; LIKE alone would match prefixes.
func datasetIDIn(list, datasetID string) bool {
	for _, item := range strings.Split(list, ",") {
		if strings.TrimSpace(item) == datasetID {
			return true
		}
	}
	return false
}
