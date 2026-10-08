package ragflow

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type DatasetArtifact struct {
	Slug     string `json:"slug"`
	Title    string `json:"title"`
	PageType string `json:"page_type"`
}

type DatasetArtifactFilter struct {
	Page     int
	PageSize int
	PageType string
	Topic    string
	Keywords string
}

type datasetArtifactResponse struct {
	Items []DatasetArtifact `json:"items"`
	Total int64             `json:"total"`
}

func normalizeDatasetArtifactFilter(filter DatasetArtifactFilter) (DatasetArtifactFilter, error) {
	if filter.Page < 1 {
		return filter, protocolError("page must be at least 1")
	}
	if filter.PageSize < 1 || filter.PageSize > 100 {
		return filter, protocolError("page_size must be between 1 and 100")
	}
	filter.PageType = strings.TrimSpace(filter.PageType)
	filter.Topic = strings.TrimSpace(filter.Topic)
	filter.Keywords = strings.TrimSpace(filter.Keywords)
	return filter, nil
}

func (c *HTTPClient) ListDatasetArtifacts(
	ctx context.Context, datasetID string, filter DatasetArtifactFilter,
) ([]DatasetArtifact, int64, error) {
	datasetID = strings.TrimSpace(datasetID)
	if datasetID == "" {
		return nil, 0, protocolError("dataset_id is required")
	}
	filter, err := normalizeDatasetArtifactFilter(filter)
	if err != nil {
		return nil, 0, err
	}
	query := url.Values{}
	query.Set("page", fmt.Sprintf("%d", filter.Page))
	query.Set("page_size", fmt.Sprintf("%d", filter.PageSize))
	if filter.PageType != "" {
		query.Set("page_type", filter.PageType)
	}
	if filter.Topic != "" {
		query.Set("topic", filter.Topic)
	}
	if filter.Keywords != "" {
		query.Set("keywords", filter.Keywords)
	}
	path := "/datasets/" + url.PathEscape(datasetID) + "/artifacts?" + query.Encode()
	var out datasetArtifactResponse
	if err := c.do(ctx, http.MethodGet, path, nil, "", &out); err != nil {
		return nil, 0, err
	}
	if out.Items == nil {
		out.Items = []DatasetArtifact{}
	}
	return out.Items, out.Total, nil
}
