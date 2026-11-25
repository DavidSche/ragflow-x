package docs_test

import (
	"reflect"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/docs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/response"
)

// TestEnvelopeMirrorsResponseBody pins the hand-written mirror in internal/docs to
// the envelope handlers actually write. The generated spec describes
// `docs.Envelope`; if the two definitions diverge, the published contract starts
// describing a body nobody sends (or omits a field everybody receives) while every
// other check stays green — the spec is still "fresh", it is just fresh about the
// wrong type. Comparison is field-by-field on name, type and json tag, in order,
// because swag emits the properties it sees.
func TestEnvelopeMirrorsResponseBody(t *testing.T) {
	want := reflect.TypeOf(response.Body{})
	got := reflect.TypeOf(docs.Envelope{})
	if want.NumField() != got.NumField() {
		t.Fatalf("docs.Envelope has %d field(s), response.Body has %d", got.NumField(), want.NumField())
	}
	for i := 0; i < want.NumField(); i++ {
		w, g := want.Field(i), got.Field(i)
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
