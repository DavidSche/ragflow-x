package service

import (
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func chatDatasetBoundIDs(rows []model.ChatShadow, dataset *model.DatasetLink) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if containsDatasetBinding(row.DatasetIDs, dataset) {
			ids = append(ids, row.ID)
		}
	}
	return ids
}

func searchAppDatasetBoundIDs(rows []model.SearchAppShadow, dataset *model.DatasetLink) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if containsDatasetBinding(row.DatasetIDs, dataset) {
			ids = append(ids, row.ID)
		}
	}
	return ids
}

func containsDatasetBinding(raw string, dataset *model.DatasetLink) bool {
	for _, candidate := range strings.Split(raw, ",") {
		if strings.TrimSpace(candidate) == dataset.RAGFlowDatasetID || strings.TrimSpace(candidate) == dataset.ID {
			return true
		}
	}
	return false
}
