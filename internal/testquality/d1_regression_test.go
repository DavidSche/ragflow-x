package testquality

import (
	"os"
	"path/filepath"
	"testing"
)

func loadFreeze(t *testing.T, name string) *D1Freeze {
	t.Helper()
	path := filepath.Join("testdata", name)
	freeze, err := LoadD1ManifestFile(path)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return freeze
}

// d1FixtureRows is the synthetic six-case result set used by every verifier
// test. It deliberately contains one over-abstain, one correct abstain, one
// confidently wrong answer and one table bucket, so every metric has a
// non-degenerate value.
func d1FixtureRows() []D1CaseRow {
	return []D1CaseRow{
		{CaseID: "d1-01", Answerable: true, Correct: true, Confidence: "high", CitedDocs: []string{"a", "b"}, ExpectedDocs: []string{"a"}},
		{CaseID: "d1-02", Answerable: true, Correct: true, Confidence: "medium", CitedDocs: []string{"x"}, ExpectedDocs: []string{"a"}},
		{CaseID: "d1-03", Bucket: "table", Answerable: true, Correct: true, Confidence: "high", CitedDocs: []string{"t"}, ExpectedDocs: []string{"t"}},
		{CaseID: "d1-04", Answerable: true, Abstained: true, Correct: false, Confidence: "low"},
		{CaseID: "d1-05", Answerable: false, Abstained: true, Correct: true, Confidence: "high"},
		{CaseID: "d1-06", Answerable: true, Correct: false, Confidence: "high", CitedDocs: []string{"y"}, ExpectedDocs: []string{"a"}},
	}
}

func cloneRows(rows []D1CaseRow) []D1CaseRow {
	out := make([]D1CaseRow, len(rows))
	copy(out, rows)
	return out
}

func fixtureBundle(freeze *D1Freeze, passes ...[]D1CaseRow) *D1Bundle {
	if len(passes) == 0 {
		passes = [][]D1CaseRow{cloneRows(d1FixtureRows()), cloneRows(d1FixtureRows())}
	}
	bundle := &D1Bundle{
		ManifestVersion: freeze.Manifest.Version,
		ManifestSHA256:  freeze.SHA256,
		Run: D1Run{
			System:       "ragflow-x",
			SystemID:     "sys-fixture",
			Version:      "v0.4.0-fixture",
			StartedAt:    "2026-10-26T00:00:00Z",
			CorpusSHA256: freeze.Manifest.Corpus.SHA256,
			BuildID:      freeze.Manifest.Corpus.BuildID,
			JudgeModel:   freeze.Manifest.Judge.Model,
			SetRole:      "regression",
			TopK:         freeze.Manifest.Protocol.TopK,
		},
		Passes: passes,
	}
	return bundle
}

func assertViolation(t *testing.T, report *D1Report, code string) {
	t.Helper()
	for _, violation := range report.Violations {
		if violation == code {
			return
		}
	}
	t.Fatalf("expected violation %s, got %v", code, report.Violations)
}

func TestD1ShippedManifestStaysDraftUntilFrozen(t *testing.T) {
	freeze := loadFreeze(t, "d1_regression_manifest.json")
	if freeze.Frozen() {
		t.Fatal("shipped D1 manifest must stay draft until corpus and judge identity are filled")
	}
	// The draft still pins the protocol shape from doc/222 §12.7 so the
	// freeze cannot silently shrink the sample.
	if freeze.Manifest.Protocol.SampleSize != 51 {
		t.Fatalf("draft sample size = %d, want 51", freeze.Manifest.Protocol.SampleSize)
	}
	if freeze.Manifest.Protocol.Passes != 2 {
		t.Fatalf("draft passes = %d, want 2", freeze.Manifest.Protocol.Passes)
	}

	report, err := freeze.Verify(fixtureBundle(freeze))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	assertViolation(t, report, D1ViolationManifestUnfrozen)
	if report.GateEligible {
		t.Fatal("a bundle verified against a draft manifest must not be gate eligible")
	}
}

func TestD1FrozenFixtureRecomputesMetricsFromRows(t *testing.T) {
	freeze := loadFreeze(t, "d1_fixture_manifest.json")
	if !freeze.Frozen() {
		t.Fatal("fixture manifest should be frozen")
	}
	report, err := freeze.Verify(fixtureBundle(freeze))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(report.Violations) != 0 {
		t.Fatalf("unexpected violations: %v", report.Violations)
	}
	if !report.GateEligible {
		t.Fatal("comparable bundle should be gate eligible")
	}
	if report.PassAgreement != 1 {
		t.Fatalf("pass agreement = %v, want 1", report.PassAgreement)
	}

	metrics := report.Metrics
	if metrics.Cases != 6 || metrics.Answered != 4 {
		t.Fatalf("cases=%d answered=%d, want 6/4", metrics.Cases, metrics.Answered)
	}
	if !almostEqual(metrics.Accuracy, 0.5) {
		t.Fatalf("accuracy = %v, want 0.5", metrics.Accuracy)
	}
	if metrics.CitationVerifiable == nil || !almostEqual(*metrics.CitationVerifiable, 2.0/3.0) {
		t.Fatalf("citation_verifiable = %v, want 0.6667", metrics.CitationVerifiable)
	}
	if metrics.CitationPrecision == nil || !almostEqual(*metrics.CitationPrecision, 0.5) {
		t.Fatalf("citation_precision = %v, want 0.5", metrics.CitationPrecision)
	}
	if metrics.OverAbstain == nil || !almostEqual(*metrics.OverAbstain, 0.2) {
		t.Fatalf("over_abstain = %v, want 0.2", metrics.OverAbstain)
	}
	if metrics.AbstainCorrect == nil || !almostEqual(*metrics.AbstainCorrect, 1) {
		t.Fatalf("abstain_correct = %v, want 1", metrics.AbstainCorrect)
	}
	if !almostEqual(metrics.ConfidentWrong, 1.0/6.0) {
		t.Fatalf("confident_wrong = %v, want 0.1667", metrics.ConfidentWrong)
	}
	if got := metrics.BucketAccuracy["table"]; !almostEqual(got, 1) {
		t.Fatalf("table bucket accuracy = %v, want 1", got)
	}
}

func TestD1UndefinedMetricsStayNilNotZero(t *testing.T) {
	freeze := loadFreeze(t, "d1_fixture_manifest.json")
	rows := []D1CaseRow{
		{CaseID: "d1-01", Answerable: false, Abstained: true, Correct: true},
		{CaseID: "d1-02", Answerable: false, Abstained: true, Correct: false},
		{CaseID: "d1-03", Answerable: false, Abstained: true, Correct: true},
		{CaseID: "d1-04", Answerable: false, Abstained: true, Correct: true},
		{CaseID: "d1-05", Answerable: false, Abstained: true, Correct: true},
		{CaseID: "d1-06", Answerable: false, Abstained: true, Correct: true},
	}
	report, err := freeze.Verify(fixtureBundle(freeze, rows, cloneRows(rows)))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if report.Metrics.CitationPrecision != nil {
		t.Fatalf("citation_precision should be nil when nothing is answered, got %v", *report.Metrics.CitationPrecision)
	}
	if report.Metrics.OverAbstain != nil {
		t.Fatalf("over_abstain should be nil with no answerable case, got %v", *report.Metrics.OverAbstain)
	}
}

func TestD1TamperedDeclaredAggregateIsRejected(t *testing.T) {
	freeze := loadFreeze(t, "d1_fixture_manifest.json")
	bundle := fixtureBundle(freeze)
	bundle.Declared = &D1Metrics{Accuracy: 0.9, ConfidentWrong: 0, Cases: 6}
	report, err := freeze.Verify(bundle)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	assertViolation(t, report, D1ViolationDeclaredMetric)
	if report.GateEligible {
		t.Fatal("bundle with declared aggregate that does not match the rows must not be gate eligible")
	}
}

func TestD1BundleMustQuoteTheManifestFingerprint(t *testing.T) {
	freeze := loadFreeze(t, "d1_fixture_manifest.json")
	bundle := fixtureBundle(freeze)
	bundle.ManifestSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	report, err := freeze.Verify(bundle)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	assertViolation(t, report, D1ViolationFingerprintMismatch)
}

func TestD1BundleIdentityMustMatchFreeze(t *testing.T) {
	freeze := loadFreeze(t, "d1_fixture_manifest.json")
	bundle := fixtureBundle(freeze)
	bundle.Run.CorpusSHA256 = "other-corpus"
	bundle.Run.BuildID = "other-build"
	bundle.Run.JudgeModel = "other-judge"
	bundle.Run.TopK = 3
	report, err := freeze.Verify(bundle)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	assertViolation(t, report, D1ViolationCorpusMismatch)
	assertViolation(t, report, D1ViolationBuildMismatch)
	assertViolation(t, report, D1ViolationJudgeMismatch)
	assertViolation(t, report, D1ViolationTopKMismatch)
}

func TestD1HoldoutRoleCannotReuseTheRegressionCorpus(t *testing.T) {
	freeze := loadFreeze(t, "d1_fixture_manifest.json")
	bundle := fixtureBundle(freeze)
	bundle.Run.SetRole = "holdout"
	report, err := freeze.Verify(bundle)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	assertViolation(t, report, D1ViolationHoldoutReusesCorpus)
	if report.GateEligible {
		t.Fatal("a holdout run over the regression corpus must not be gate eligible")
	}
}

func TestD1SampleSizeAndCaseSetAreEnforced(t *testing.T) {
	freeze := loadFreeze(t, "d1_fixture_manifest.json")
	short := d1FixtureRows()[:5]
	report, err := freeze.Verify(fixtureBundle(freeze, short, cloneRows(d1FixtureRows())))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	assertViolation(t, report, D1ViolationSampleSize)

	duplicated := append(cloneRows(d1FixtureRows()), d1FixtureRows()[0])
	report, err = freeze.Verify(fixtureBundle(freeze, duplicated, duplicated))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	assertViolation(t, report, D1ViolationDuplicateCase)
}

func TestD1TwoPassDisagreementFailsComparability(t *testing.T) {
	freeze := loadFreeze(t, "d1_fixture_manifest.json")
	second := cloneRows(d1FixtureRows())
	second[0].Correct = false
	report, err := freeze.Verify(fixtureBundle(freeze, cloneRows(d1FixtureRows()), second))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	assertViolation(t, report, D1ViolationPassAgreement)
	if report.PassAgreement >= 1 {
		t.Fatalf("pass agreement = %v, want < 1", report.PassAgreement)
	}
	if report.GateEligible {
		t.Fatal("non-deterministic verdicts must not be gate eligible")
	}
}

func TestD1TuningOnRegressionSetIsBlocked(t *testing.T) {
	freeze := loadFreeze(t, "d1_fixture_manifest.json")
	bundle := fixtureBundle(freeze)
	bundle.Run.TuningUsed = true
	report, err := freeze.Verify(bundle)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	assertViolation(t, report, D1ViolationTuningOnRegression)
	if report.GateEligible {
		t.Fatal("tuned regression run must not be gate eligible")
	}
}

func TestD1QualityFindingsDoNotBlockComparability(t *testing.T) {
	freeze := loadFreeze(t, "d1_fixture_manifest.json")
	weak := []D1CaseRow{
		{CaseID: "d1-01", Bucket: "table", Answerable: true, Correct: false, Confidence: "high", CitedDocs: []string{"z"}, ExpectedDocs: []string{"a"}},
		{CaseID: "d1-02", Bucket: "table", Answerable: true, Correct: false, Confidence: "high"},
		{CaseID: "d1-03", Answerable: true, Correct: true, Confidence: "high", CitedDocs: []string{"z"}, ExpectedDocs: []string{"a"}},
		{CaseID: "d1-04", Answerable: true, Abstained: true},
		{CaseID: "d1-05", Answerable: true, Abstained: true},
		{CaseID: "d1-06", Answerable: false, Abstained: true, Correct: false},
	}
	report, err := freeze.Verify(fixtureBundle(freeze, weak, cloneRows(weak)))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(report.QualityFindings) == 0 {
		t.Fatal("expected quality findings for a weak run")
	}
	if !report.GateEligible {
		t.Fatalf("quality findings must not affect comparability yet (C4.5 wires them), violations=%v", report.Violations)
	}
}

func TestD1BundleRejectsUnknownFields(t *testing.T) {
	data := []byte(`{"manifest_version":"x","manifest_sha256":"y","run":{},"passes":[],"aggregate":0.9}`)
	if _, err := ParseD1Bundle(data); err == nil {
		t.Fatal("unknown bundle fields must be rejected so a typo cannot hide an aggregate")
	}
}

func TestD1VerifierRequiresBundle(t *testing.T) {
	freeze := loadFreeze(t, "d1_fixture_manifest.json")
	if _, err := freeze.Verify(nil); err == nil {
		t.Fatal("nil bundle must be rejected")
	}
	if freeze.SHA256 == "" {
		t.Fatal("freeze must carry the manifest file hash")
	}
	if _, err := os.Stat(filepath.Join("testdata", "d1_regression_manifest.json")); err != nil {
		t.Fatalf("shipped manifest missing: %v", err)
	}
}
