package ragflow

import (
	"context"
	"fmt"
	"strings"
)

// ListDatasetArtifacts returns the configured compiled artifact page without
// generating model output.
func (m *Mock) ListDatasetArtifacts(
	_ context.Context, datasetID string, filter DatasetArtifactFilter,
) ([]DatasetArtifact, int64, error) {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return nil, 0, fmt.Errorf("dataset_id is required")
	}
	filter, err := normalizeDatasetArtifactFilter(filter)
	if err != nil {
		return nil, 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastArtifacts = &filter
	artifacts := m.artifacts[datasetID]
	start := (filter.Page - 1) * filter.PageSize
	if start >= len(artifacts) {
		return []DatasetArtifact{}, int64(len(artifacts)), nil
	}
	end := start + filter.PageSize
	if end > len(artifacts) {
		end = len(artifacts)
	}
	return append([]DatasetArtifact(nil), artifacts[start:end]...), int64(len(artifacts)), nil
}

func (m *Mock) GetDatasetArtifact(
	_ context.Context, datasetID, pageType, slug string,
) (*DatasetArtifactDetail, error) {
	datasetID, pageType, slug = strings.TrimSpace(datasetID), strings.TrimSpace(pageType), strings.TrimSpace(slug)
	if datasetID == "" || pageType == "" || slug == "" {
		return nil, fmt.Errorf("dataset_id, page_type and slug are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	detail, exists := m.artifactDetails[datasetID][pageType][slug]
	if !exists {
		m.lastArtifactDetail = nil
		return nil, nil
	}
	m.lastArtifactDetail = &detail
	return &detail, nil
}
