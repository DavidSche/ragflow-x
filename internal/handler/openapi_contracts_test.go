package handler_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/docs"
	"github.com/ragflow-x/ragflow-x/internal/handler"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// The per-endpoint envelopes in openapi_contracts.go exist so the published contract can
// say what `data` is. They are mirrors of `response.Body` (via the embedded EnvelopeMeta),
// and mirrors drift — the compiler cannot see the generated document, and the document
// cannot see the handler. Two properties are worth pinning:

// TestEnvelopeMetaMirrorsResponseBody pins the half of the envelope that every endpoint
// shares. If `response.Body` gains or renames a field and EnvelopeMeta does not, every
// documented response describes a body nobody sends while s25 stays green (the spec is
// still fresh — fresh about the wrong type).
func TestEnvelopeMetaMirrorsResponseBody(t *testing.T) {
	want := reflect.TypeOf(response.Body{})
	got := reflect.TypeOf(handler.EnvelopeMeta{})

	// response.Body is {Code, Message, Data, TraceID}; EnvelopeMeta is that minus Data,
	// because Data is exactly the part each endpoint has to name for itself.
	if want.NumField() != got.NumField()+1 {
		t.Fatalf("response.Body has %d field(s), EnvelopeMeta has %d — expected exactly one more (Data)",
			want.NumField(), got.NumField())
	}
	if f, ok := want.FieldByName("Data"); !ok {
		t.Fatalf("response.Body no longer has a Data field; the envelope split is stale")
	} else if _, ok := got.FieldByName(f.Name); ok {
		t.Fatalf("EnvelopeMeta declares Data; the payload must stay per-endpoint")
	}
	j := 0
	for i := 0; i < want.NumField(); i++ {
		w := want.Field(i)
		if w.Name == "Data" {
			continue // the payload, deliberately not shared
		}
		g := got.Field(j)
		j++
		if w.Name != g.Name {
			t.Errorf("field %d: name %q != %q", i, g.Name, w.Name)
		}
		if w.Type != g.Type {
			t.Errorf("field %s: type %s != %s", w.Name, g.Type, w.Type)
		}
		if w.Tag.Get("json") != g.Tag.Get("json") {
			t.Errorf("field %s: json tag %q != %q", w.Name, g.Tag.Get("json"), w.Tag.Get("json"))
		}
	}
}

// TestPublishedEnvelopesCarryATypedData reads the generated spec and refuses two shapes
// that both used to be green:
//
//   - an envelope whose `data` is `{}` (the annotation named the endpoint type in prose but
//     not in the schema), and
//   - a `docs.Envelope` that swag mutated into `allOf: [{$ref: data}]`, which points at a
//     component named after the field and is shared by every endpoint that used the
//     `Envelope{data=X}` form (doc/310 §22.2 P-310-12). One endpoint's payload then appears
//     to be every endpoint's payload.
func TestPublishedEnvelopesCarryATypedData(t *testing.T) {
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
				AllOf      []json.RawMessage          `json:"allOf"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(docs.OpenAPISpecJSON, &spec); err != nil {
		t.Fatalf("parse embedded spec: %v", err)
	}
	schemas := spec.Components.Schemas
	if len(schemas) == 0 {
		t.Fatalf("embedded spec has no schemas")
	}

	shared, ok := schemas["docs.Envelope"]
	if !ok {
		t.Fatalf("docs.Envelope is missing from the spec")
	}
	if len(shared.AllOf) != 0 {
		t.Errorf("docs.Envelope has allOf: a field override on the shared envelope was used again (P-310-12)")
	}
	if _, ok := schemas["data"]; ok {
		t.Errorf("the spec has a component literally named %q: swag stored a field override there", "data")
	}

	seen := 0
	for name, s := range schemas {
		if name == "docs.Envelope" {
			continue // the shared base: Data is interface{} on purpose (error bodies vary)
		}
		if len(s.Properties) == 0 {
			continue
		}
		raw, ok := s.Properties["data"]
		if !ok {
			continue
		}
		seen++
		var shape map[string]any
		if err := json.Unmarshal(raw, &shape); err != nil {
			t.Fatalf("%s.data: %v", name, err)
		}
		_, isRef := shape["$ref"]
		_, isItems := shape["items"]
		typ, _ := shape["type"].(string)
		if !isRef && !isItems && typ != "object" && typ != "array" {
			t.Errorf("%s.data is %s — an envelope must name its payload type, not leave it empty",
				name, string(raw))
		}
	}
	if seen == 0 {
		t.Fatalf("no schema in the spec declares a data property; the test is looking at nothing")
	}
}
