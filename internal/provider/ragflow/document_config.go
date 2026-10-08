package ragflow

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

type DocumentParseConfigUpdate struct {
	Pipeline        bool
	BuiltinParserID string
	PipelineID      string
	ParserConfig    map[string]interface{}
	MetaFields      map[string]interface{}
}

func (c *HTTPClient) UpdateDocumentParseConfig(ctx context.Context, datasetID, documentID string, update DocumentParseConfigUpdate) error {
	payload := map[string]interface{}{}
	if update.Pipeline {
		if update.PipelineID == "" {
			return protocolError("pipeline_id is required for pipeline parse mode")
		}
		payload["parse_type"] = 2
		payload["pipeline_id"] = update.PipelineID
	} else {
		if update.BuiltinParserID == "" {
			return protocolError("parser_id is required for builtin parse mode")
		}
		payload["parse_type"] = 1
		payload["parser_id"] = update.BuiltinParserID
	}
	if len(update.ParserConfig) > 0 {
		payload["parser_config"] = update.ParserConfig
	}
	if update.MetaFields != nil {
		payload["meta_fields"] = update.MetaFields
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return wrapError("marshal document parse config", err)
	}
	path := "/datasets/" + url.PathEscape(datasetID) + "/documents/" + url.PathEscape(documentID)
	return c.do(ctx, http.MethodPatch, path, bytes.NewReader(body), "application/json", nil)
}
