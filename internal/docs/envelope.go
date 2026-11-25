// Package docs holds the generated OpenAPI artifacts (`docs.go`, `swagger.json`,
// `swagger.yaml`) plus the one hand-written type the swag annotations name.
//
// The three generated files come from `bash scripts/release-gate/openapi.sh gen
// internal/docs` (doc/310 §12.1). This file does not: swag resolves
// `{object} docs.Envelope` by loading the package, so the annotation target has to
// be a real Go type that lives in the same package as the artifacts. It is a
// mirror of `internal/pkg/response.Body`, and `envelope_test.go` pins the mirror
// field by field — a hand-written copy of a shape that handlers actually emit is
// exactly the kind of duplication that drifts silently.
package docs

// Envelope is the success envelope every documented handler returns.
//
// Why a struct mirror and not `type Envelope = response.Body`: swag names the
// schema after the type it resolves, so an alias would rename the definition to
// `response.Body` and change the artifact — i.e. the fix for one duplication
// would rewrite the published contract. The mirror plus the reflection test keeps
// the artifact's shape stable without letting the two definitions diverge.
type Envelope struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
	TraceID string      `json:"trace_id,omitempty"`
}
