// Package connector implements the data-source provider runtime for dataset
// ingestion (doc/225 §8). Providers scan a page at a time and advance a
// process-local pagination token; content revisions are owned by the durable
// Connector.CursorJSON map and diffed by BuildPlan. The package is pure Go and
// has no engine coupling: engine connectors enter through the adapter layer
// (doc/225 §8.1) rather than bespoke scanning code.
package connector

import (
	"context"
	"sort"
	"time"
)

// Entry is a single scanned object in provider-relative form (doc/225 §8).
type Entry struct {
	Key      string    `json:"key"`      // provider-relative path
	Size     int64     `json:"size"`     // bytes
	MTime    time.Time `json:"mtime"`    // last modified time (UTC)
	Revision string    `json:"revision"` // content fingerprint (ETag / mtime+size)
	Kind     string    `json:"kind"`     // "file"
}

// Provider is the deterministic-source scanning contract. List returns at most
// one page (bounded by the caller's budget, model.ConnectorMaxBatch) and an
// opaque next-token; an empty token means the scan is exhausted. Implementations
// must check ctx.Err() every batch so the 30-minute hard deadline and the
// per-batch context check (doc/225 §8 F5/F8) cancel promptly.
type Provider interface {
	// Type returns the stable provider key registered in the registry.
	Type() string
	// ValidateSpec validates the connector spec (non-credential).
	ValidateSpec(spec map[string]any) error
	// Test performs a connectivity probe without a full scan.
	Test(ctx context.Context, spec map[string]any, cred map[string]string) error
	// List scans the next page after token ("", starts at the beginning).
	List(ctx context.Context, spec map[string]any, cred map[string]string, token string) ([]Entry, string, error)
	// Download fetches full content for a scanned key. This is the v0
	// materialization step feeding UploadDocumentWithSourceKey; the streaming
	// FetchStream(ctx, spec, cred, cursor, emit) contract (doc/225 §8) is the
	// declared evolution for large-object sources.
	Download(ctx context.Context, spec map[string]any, cred map[string]string, key string) ([]byte, error)
}

// StreamProvider is the reserved streaming contract (doc/225 §8). Providers that
// can emit entries incrementally may implement it; the scheduler keeps using
// List page-by-page until the ingestion bridge supports streaming uploads.
type StreamProvider interface {
	FetchStream(ctx context.Context, spec map[string]any, cred map[string]string, cursor string, emit func([]Entry) error) error
}

// Registry maps provider type -> factory and exposes metadata for UIs.
type Registry struct {
	factories map[string]func() Provider
	meta      map[string]Meta
}

// MaxObjectBytes bounds a single connector object/materialized document. It
// mirrors the document upload ceiling (doc/225 §6.2: single document ≤25 MiB).
const MaxObjectBytes int64 = 25 << 20

// Connector tiers come from doc/227 §4.2.4. Tier A is the private-cloud
// native path that must close the whole business loop inside X; Tier B is a
// restricted upstream adapter (not enabled in this release); Tier C is an
// explicit non-goal (internet wiki/consumer SaaS) and must not appear in the
// provider registry.
const (
	TierNative      = "A"
	TierRestricted  = "B"
	TierUnsupported = "C"

	ClosureFull        = "full"
	ClosureRestricted  = "restricted"
	ClosureUnsupported = "unsupported"
)

// Meta carries UI-facing provider metadata (label, spec form schema) plus the
// coverage contract so the wizard cannot advertise a source whose business
// loop is not closed in X (doc/227 C2.1).
type Meta struct {
	Type          string      `json:"type"`
	Label         string      `json:"label"`
	Tier          string      `json:"tier"`
	Closure       string      `json:"closure"`
	TargetVersion string      `json:"target_version,omitempty"`
	SpecFields    []FieldSpec `json:"spec_fields"`
	// CredFields describes the credential form (sealed KV map). S3 needs two
	// values (access_key_id/access_secret) and WebDAV two (username/password),
	// so the wizard cannot be driven by a single field name.
	CredFields []FieldSpec `json:"cred_fields"`
}

// FieldSpec describes one spec form field.
type FieldSpec struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Default     any    `json:"default,omitempty"`
}

// NewRegistry returns the built-in provider registry for the first-release set
// (doc/225 §8: LocalFolder / S3 compatible / WebDAV).
func NewRegistry() *Registry {
	r := &Registry{factories: map[string]func() Provider{}, meta: map[string]Meta{}}
	r.Register(func() Provider { return &LocalFolder{} }, Meta{
		Type:          "local_folder",
		Label:         "本地目录 (LocalFolder)",
		Tier:          TierNative,
		Closure:       ClosureFull,
		TargetVersion: "v0.4.0 GA",
		SpecFields: []FieldSpec{
			{Key: "root", Label: "根目录", Required: true, Placeholder: `\\host\share\ 或 /srv/data`},
		},
	})
	r.Register(func() Provider { return &S3Provider{} }, Meta{
		Type:          "s3",
		Label:         "S3 / MinIO (S3 兼容)",
		Tier:          TierNative,
		Closure:       ClosureFull,
		TargetVersion: "v0.4.0 GA",
		SpecFields: []FieldSpec{
			{Key: "endpoint", Label: "Endpoint", Required: true, Placeholder: "https://s3.example.com"},
			{Key: "region", Label: "Region", Default: "us-east-1"},
			{Key: "bucket", Label: "Bucket", Required: true},
			{Key: "prefix", Label: "Prefix (可选)"},
		},
		CredFields: []FieldSpec{
			{Key: "access_key_id", Label: "Access Key ID", Required: true},
			{Key: "access_secret", Label: "Access Secret", Required: true, Secret: true},
		},
	})
	r.Register(func() Provider { return &WebDAVProvider{} }, Meta{
		Type:          "webdav",
		Label:         "WebDAV",
		Tier:          TierNative,
		Closure:       ClosureFull,
		TargetVersion: "v0.4.0 GA",
		SpecFields: []FieldSpec{
			{Key: "base_url", Label: "Base URL", Required: true, Placeholder: "https://dav.example.com/"},
			{Key: "path", Label: "根路径 (可选)", Default: "/"},
		},
		CredFields: []FieldSpec{
			{Key: "username", Label: "Username", Required: true},
			{Key: "password", Label: "Password", Required: true, Secret: true},
		},
	})
	return r
}

// Register installs a provider factory and its metadata. It panics on
// duplicate types so a colliding registration fails fast.
func (r *Registry) Register(factory func() Provider, meta Meta) {
	if _, ok := r.factories[factory().Type()]; ok {
		panic("duplicate connector provider type " + factory().Type())
	}
	r.factories[factory().Type()] = factory
	r.meta[factory().Type()] = meta
}

// New returns an instance for the type, or nil when unknown.
func (r *Registry) New(providerType string) Provider {
	f, ok := r.factories[providerType]
	if !ok {
		return nil
	}
	return f()
}

// MetaFor returns UI metadata for the type.
func (r *Registry) MetaFor(providerType string) (Meta, bool) {
	m, ok := r.meta[providerType]
	return m, ok
}

// Types returns the sorted provider types in the registry.
func (r *Registry) Types() []string {
	out := make([]string, 0, len(r.factories))
	for t := range r.factories {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// BuildPlan diffs the persisted revision cursor (key -> revision) against a
// scanned page and returns the added/changed/deleted candidate keys. Final
// idempotency is owned by the incremental ledger (doc/225 §5.3); this function
// is only the cursor-quick diff used to drive the apply/upload pipeline.
func BuildPlan(prev map[string]string, cur []Entry) (added, changed, deleted []string) {
	seen := make(map[string]bool, len(cur))
	for _, e := range cur {
		seen[e.Key] = true
		old, ok := prev[e.Key]
		switch {
		case !ok:
			added = append(added, e.Key)
		case old != e.Revision:
			changed = append(changed, e.Key)
		}
	}
	for k := range prev {
		if !seen[k] {
			deleted = append(deleted, k)
		}
	}
	sort.Strings(added)
	sort.Strings(changed)
	sort.Strings(deleted)
	return added, changed, deleted
}
