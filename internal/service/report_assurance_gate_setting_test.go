package service

// G-A8 (doc/301 §10.5) — `assurance_gate=required` must be *unrepresentable* in v0.7.0.
//
// The gate is advisory-only until E1 calibrates the classifier (T18 in v0.7.1), and
// the release gate asserts that the refusal lives in the settings validator rather
// than in a document. A validator that accepted `required` and let some other layer
// ignore it would mean the value can be stored, audited and quoted back to a customer
// while nothing blocks: the worst of both worlds.
//
// Two properties are pinned, because either one alone is a half-promise:
//
//   - `required` is refused by the validator, with a business error the UI can show;
//   - refusing it leaves the stored revision untouched — a rejected write must not
//     half-apply, or the operator would see "revision 7" with no change to it.

import (
	"context"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/config"
)

func TestAssuranceGateRefusesRequiredBeforeCalibration(t *testing.T) {
	svc := newAuthzSvc(t)
	ctx := context.Background()
	if err := svc.InitializeSystemSettingsFromConfig(ctx, config.Config{}); err != nil {
		t.Fatal(err)
	}
	before, err := svc.GetSystemSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: before.Revision,
		Changes:          map[string]interface{}{"report.assurance_gate": "required"},
		Note:             "v0.7.0 只做 advisory",
		Confirmed:        true,
	}); err == nil {
		t.Fatal("v0.7.0 must refuse assurance_gate=required until E1 calibrates the classifier")
	}
	after, err := svc.GetSystemSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision {
		t.Fatalf("a refused write bumped the settings revision %d → %d",
			before.Revision, after.Revision)
	}
	// The view nests the effective value inside the field's metadata; read it there so
	// the assertion is about what the gate will actually consult.
	field, _ := after.Groups["report"]["assurance_gate"].(map[string]interface{})
	if got := field["effective"]; got != "advisory" {
		t.Fatalf("assurance_gate after the refusal = %v, want the untouched default advisory", got)
	}

	// `advisory` itself must be accepted: a validator that rejects everything would
	// pass the assertion above while making the feature unusable.
	if _, err := svc.UpdateSystemSettings(ctx, "admin", SystemSettingsPatchRequest{
		ExpectedRevision: after.Revision,
		Changes:          map[string]interface{}{"report.assurance_gate": "advisory"},
		Note:             "advisory 可用",
		Confirmed:        true,
	}); err != nil {
		t.Fatalf("advisory must be an allowed value, got %v", err)
	}
}
