package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
)

func canonicalJSONHash(value interface{}) string {
	raw, _ := json.Marshal(canonicalizeJSONValue(value))
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func canonicalizeJSONValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		normalized := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			normalized[key] = canonicalizeJSONValue(item)
		}
		return normalized
	case []interface{}:
		normalized := make([]interface{}, len(typed))
		for index, item := range typed {
			normalized[index] = canonicalizeJSONValue(item)
		}
		return normalized
	case time.Time:
		return typed.UTC().Format("2006-01-02T15:04:05.000000000Z07:00")
	default:
		return value
	}
}

// resourceUpstreamSemanticPayload freezes the fields that participate in
// content-conflict detection. Identity and sync/request metadata are handled
// elsewhere and must never enter this payload.
func resourceUpstreamSemanticPayload(resourceType string, payload map[string]interface{}) map[string]interface{} {
	include := func(keys ...string) map[string]interface{} {
		result := make(map[string]interface{}, len(keys))
		for _, key := range keys {
			if value, ok := payload[key]; ok {
				result[key] = value
			}
		}
		return result
	}
	switch resourceType {
	case model.SyncItemTypeDataset:
		return include("name", "chunk_count", "document_count")
	case model.SyncItemTypeChat:
		return include(
			"name", "status", "llm_id", "prompt_config", "dataset_ids",
			"top_n", "top_k", "rerank_id", "similarity_threshold",
			"vector_similarity_weight",
		)
	case model.SyncItemTypeAgent:
		return include(
			"title", "description", "canvas_type", "canvas_category",
			"release", "dsl",
		)
	case model.SyncItemTypeSearchApp:
		return include("name", "description", "search_config")
	case model.SyncItemTypeMemory:
		return include("name", "description", "memory_type", "storage_type", "forgetting_policy")
	default:
		return map[string]interface{}{}
	}
}

func resourceUpstreamHash(resourceType string, payload map[string]interface{}) string {
	return canonicalJSONHash(resourceUpstreamSemanticPayload(resourceType, payload))
}

// resourceLocalHash is intentionally scoped to the Local Managed Payload. In
// the current Shadow schema, ownership/project/review/discovery fields are
// governance facts and are excluded. RAGFlow-owned fields are represented by
// the upstream hash, not by this local hash.
func resourceLocalHash(resourceType string, parts map[string]interface{}) string {
	var keys []string
	switch resourceType {
	case model.SyncItemTypeDataset:
		keys = []string{"name", "document_count"}
	case model.SyncItemTypeChat:
		keys = []string{"name", "status", "dataset_ids"}
	case model.SyncItemTypeAgent:
		keys = []string{"title"}
	case model.SyncItemTypeSearchApp:
		keys = []string{"name", "status", "dataset_ids"}
	case model.SyncItemTypeMemory:
		keys = []string{"name", "memory_type"}
	default:
		keys = []string{}
	}
	semantic := make(map[string]interface{}, len(keys))
	for _, key := range keys {
		if value, ok := parts[key]; ok {
			semantic[key] = value
		}
	}
	return canonicalJSONHash(semantic)
}

// redactValue keeps a short semantic digest so a redacted persisted payload
// remains traceable across runs without exposing the original secret or text.
func redactValue(key string, value interface{}) interface{} {
	lowerKey := strings.ToLower(key)
	if strings.Contains(lowerKey, "prompt") ||
		strings.Contains(lowerKey, "api_key") ||
		strings.Contains(lowerKey, "apikey") ||
		strings.Contains(lowerKey, "token") ||
		strings.Contains(lowerKey, "password") ||
		strings.Contains(lowerKey, "secret") {
		raw, _ := json.Marshal(value)
		sum := sha256.Sum256(raw)
		return "[REDACTED:" + hex.EncodeToString(sum[:])[:12] + "]"
	}
	switch typed := value.(type) {
	case map[string]interface{}:
		return redactMap(typed)
	case []interface{}:
		for index, item := range typed {
			typed[index] = redactValue(key, item)
		}
		return typed
	default:
		return value
	}
}

func redactMap(input map[string]interface{}) map[string]interface{} {
	output := make(map[string]interface{}, len(input))
	for key, value := range input {
		output[key] = redactValue(key, value)
	}
	return output
}

func resourcePersistedPayload(resourceType string, payload map[string]interface{}) map[string]interface{} {
	return redactMap(resourceUpstreamSemanticPayload(resourceType, payload))
}
