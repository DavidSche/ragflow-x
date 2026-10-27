package difydsl

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var difyToolReferencePattern = regexp.MustCompile(`§tool:[^:]+:([^§]+)§`)

func mapAt(value map[string]any, path ...string) map[string]any {
	current := value
	for _, key := range path {
		if current == nil {
			return nil
		}
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	return current
}

func mapSliceAt(value map[string]any, path ...string) []map[string]any {
	items := sliceAt(value, path...)
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			result = append(result, object)
		}
	}
	return result
}

func sliceAt(value map[string]any, path ...string) []any {
	if len(path) == 0 || value == nil {
		return nil
	}
	current := value
	for _, key := range path[:len(path)-1] {
		next, ok := current[key].(map[string]any)
		if !ok {
			return nil
		}
		current = next
	}
	items, ok := current[path[len(path)-1]].([]any)
	if !ok {
		return nil
	}
	return items
}

func stringAt(value map[string]any, path ...string) string {
	if len(path) == 0 || value == nil {
		return ""
	}
	current := value
	for _, key := range path[:len(path)-1] {
		next, ok := current[key].(map[string]any)
		if !ok {
			return ""
		}
		current = next
	}
	result, _ := current[path[len(path)-1]].(string)
	return result
}

func boolAt(value map[string]any, path ...string) bool {
	if len(path) == 0 || value == nil {
		return false
	}
	current := value
	for _, key := range path[:len(path)-1] {
		next, ok := current[key].(map[string]any)
		if !ok {
			return false
		}
		current = next
	}
	result, _ := current[path[len(path)-1]].(bool)
	return result
}

func anyString(value any) string {
	switch result := value.(type) {
	case string:
		return result
	case bool:
		if result {
			return "true"
		}
		return "false"
	case int:
		return fmt.Sprint(result)
	case int64:
		return fmt.Sprint(result)
	case float64:
		return fmt.Sprint(result)
	default:
		return ""
	}
}

func toAnySlice[T any](values []T) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func edgeHandle(edge map[string]any) string {
	return stringOr(anyString(edge["sourceHandle"]), "source")
}

func isHTTPURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	return err == nil && parsed.Host != "" && (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https"))
}
func isInternalURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(host, ".local") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
}

func intOr(value map[string]any, key string, fallback int) int {
	if value == nil {
		return fallback
	}
	switch result := value[key].(type) {
	case int:
		return result
	case int64:
		return int(result)
	case float64:
		return int(result)
	default:
		return fallback
	}
}

func floatOr(value map[string]any, key string, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	result, _ := value[key].(float64)
	if result == 0 {
		return fallback
	}
	return result
}

func stringOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func base64String(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

func sortedKeys(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func sortedKeySet(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
