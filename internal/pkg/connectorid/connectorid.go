// Package connectorid owns the canonical "one logical source, one connector"
// identity used by both the connector service and the migration that backfills
// legacy rows (doc/227 C2.3). It depends only on the standard library so the
// database migration can use exactly the same normalization as runtime.
package connectorid

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// OwnershipKey returns the tenant-scoped identity of a logical source. The
// target dataset is part of the identity because one bucket/path may feed two
// datasets, but only once each. Credentials are deliberately excluded so
// rotation never changes identity.
func OwnershipKey(tenantID, provider, datasetID string, spec map[string]any) string {
	target := CanonicalTarget(provider, spec)
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"v1", tenantID, provider, datasetID, target,
	}, "\n")))
	return "src:v1:" + hex.EncodeToString(sum[:])
}

// CanonicalTarget normalizes the provider-specific source target so equivalent
// spellings (trailing slashes, "." segments, Windows path casing) share one
// identity instead of creating duplicate bindings.
func CanonicalTarget(provider string, spec map[string]any) string {
	switch provider {
	case "local_folder":
		root := filepath.Clean(strings.TrimSpace(SpecString(spec, "root")))
		if runtime.GOOS == "windows" {
			root = strings.ToLower(root)
		}
		return "root=" + root
	case "s3":
		return strings.Join([]string{
			"endpoint=" + strings.ToLower(strings.TrimRight(strings.TrimSpace(SpecString(spec, "endpoint")), "/")),
			"region=" + strings.TrimSpace(SpecString(spec, "region")),
			"bucket=" + strings.TrimSpace(SpecString(spec, "bucket")),
			"prefix=" + strings.Trim(strings.TrimSpace(SpecString(spec, "prefix")), "/"),
		}, "|")
	case "webdav":
		davPath := strings.TrimSpace(SpecString(spec, "path"))
		if davPath == "" {
			davPath = "/"
		}
		if !strings.HasPrefix(davPath, "/") {
			davPath = "/" + davPath
		}
		rawBase := strings.TrimSpace(SpecString(spec, "base_url"))
		u, err := url.Parse(rawBase)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return "base_url=" + strings.TrimRight(rawBase, "/") + "|path=" + path.Clean(davPath)
		}
		// Scheme and host are case-insensitive; the collection path is not.
		// Merge base path + connector path so both spellings of one collection
		// share identity, and drop userinfo because credentials live elsewhere.
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		u.User = nil
		u.Path = path.Join("/", u.Path, davPath)
		u.RawPath = ""
		u.RawQuery = ""
		u.Fragment = ""
		return "url=" + u.String()
	default:
		b, _ := json.Marshal(spec)
		return string(b)
	}
}

// SpecString reads a string field from a decoded connector spec.
func SpecString(spec map[string]any, key string) string {
	v, _ := spec[key].(string)
	return v
}
