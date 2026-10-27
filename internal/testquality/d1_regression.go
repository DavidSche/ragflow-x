package testquality

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// D1Freeze is the frozen identity of the D1 regression benchmark: corpus,
// judge, protocol and thresholds. It is loaded together with the SHA-256 of
// the manifest file so a result bundle can prove which freeze it was produced
// against (doc/222 §12.7, doc/227 C4.1).
type D1Freeze struct {
	Manifest D1Manifest
	// SHA256 is the hex digest of the manifest file bytes, not of a parsed
	// struct, so reformatting the file is visible in the bundle.
	SHA256 string
}

// D1Manifest describes the benchmark that must not change between releases.
type D1Manifest struct {
	Version    string         `json:"version"`
	Status     string         `json:"status"` // draft | frozen
	RecordedAt string         `json:"recorded_at"`
	Owner      string         `json:"owner"`
	Corpus     D1Corpus       `json:"corpus"`
	Judge      D1Judge        `json:"judge"`
	Protocol   D1Protocol     `json:"protocol"`
	Thresholds D1Thresholds   `json:"thresholds"`
	Tuning     D1TuningPolicy `json:"tuning"`
}

// D1Corpus pins the document set the questions were written against.
type D1Corpus struct {
	Name      string `json:"name"`
	Source    string `json:"source"`
	SHA256    string `json:"sha256"`
	FileCount int    `json:"file_count"`
	BuildID   string `json:"build_id"`
}

// D1Judge pins the model and prompt that turned answers into verdicts.
type D1Judge struct {
	Model           string  `json:"model"`
	PromptSHA256    string  `json:"prompt_sha256"`
	Temperature     float64 `json:"temperature"`
	DisableThinking bool    `json:"disable_thinking"`
}

// D1Protocol pins sampling and repeatability rules.
type D1Protocol struct {
	SampleSize       int     `json:"sample_size"`
	Passes           int     `json:"passes"`
	MinPassAgreement float64 `json:"min_pass_agreement"`
	TopK             int     `json:"top_k"`
	PrimaryMetric    string  `json:"primary_metric"`
}

// D1Thresholds are the quality gate targets (doc/223 §quality gate). They are
// reported as findings only; wiring them into the release gate is C4.5.
type D1Thresholds struct {
	CitationPrecisionMin  float64            `json:"citation_precision_min"`
	CitationVerifiableMin float64            `json:"citation_verifiable_min"`
	OverAbstainMin        float64            `json:"over_abstain_min"`
	OverAbstainMax        float64            `json:"over_abstain_max"`
	AbstainCorrectMin     float64            `json:"abstain_correct_min"`
	ConfidentWrongMax     float64            `json:"confident_wrong_max"`
	BucketAccuracyMin     map[string]float64 `json:"bucket_accuracy_min"`
}

// D1TuningPolicy blocks using the regression set as a tuning surface.
type D1TuningPolicy struct {
	Allowed bool   `json:"allowed"`
	Role    string `json:"role"` // regression
}

// D1Bundle is one submitted result bundle: run identity plus per-case rows for
// every pass. Aggregates are recomputed from the rows and are never trusted.
type D1Bundle struct {
	ManifestVersion string        `json:"manifest_version"`
	ManifestSHA256  string        `json:"manifest_sha256"`
	Run             D1Run         `json:"run"`
	Passes          [][]D1CaseRow `json:"passes"`
	Declared        *D1Metrics    `json:"declared,omitempty"`
}

// D1Run records how the run was produced.
type D1Run struct {
	System       string `json:"system"`
	SystemID     string `json:"system_id"`
	Version      string `json:"version"`
	StartedAt    string `json:"started_at"`
	CorpusSHA256 string `json:"corpus_sha256"`
	BuildID      string `json:"build_id"`
	JudgeModel   string `json:"judge_model"`
	SetRole      string `json:"set_role"` // regression | holdout
	TuningUsed   bool   `json:"tuning_used"`
	TopK         int    `json:"top_k"`
}

// D1CaseRow is the smallest auditable unit: the verdict for one question.
type D1CaseRow struct {
	CaseID       string   `json:"case_id"`
	Bucket       string   `json:"bucket,omitempty"` // plain | table | multihop | ...
	Answerable   bool     `json:"answerable"`
	Abstained    bool     `json:"abstained"`
	Correct      bool     `json:"correct"`
	Confidence   string   `json:"confidence"` // high | medium | low
	CitedDocs    []string `json:"cited_docs"`
	ExpectedDocs []string `json:"expected_docs"`
}

// D1Metrics is the recomputed metric set. Undefined ratios are nil rather than
// zero: "no case declares expected citations" must never read as 0% (the same
// rule the trial run view follows).
type D1Metrics struct {
	Accuracy           float64            `json:"accuracy"`
	CitationVerifiable *float64           `json:"citation_verifiable"`
	CitationPrecision  *float64           `json:"citation_precision"`
	OverAbstain        *float64           `json:"over_abstain"`
	AbstainCorrect     *float64           `json:"abstain_correct"`
	ConfidentWrong     float64            `json:"confident_wrong"`
	BucketAccuracy     map[string]float64 `json:"bucket_accuracy,omitempty"`
	Cases              int                `json:"cases"`
	Answered           int                `json:"answered"`
}

// D1Report is the verifier verdict.
type D1Report struct {
	ManifestVersion string    `json:"manifest_version"`
	ManifestSHA256  string    `json:"manifest_sha256"`
	System          D1Run     `json:"run"`
	Metrics         D1Metrics `json:"metrics"`
	PassAgreement   float64   `json:"pass_agreement"`
	Violations      []string  `json:"violations"`
	QualityFindings []string  `json:"quality_findings"`
	// GateEligible only means "the bundle is contractually comparable with the
	// frozen baseline"; quality thresholds are reported separately until C4.5.
	GateEligible bool `json:"gate_eligible"`
}

// D1 contract violation codes.
const (
	D1ViolationManifestUnfrozen    = "D1_MANIFEST_UNFROZEN"
	D1ViolationVersionMismatch     = "D1_VERSION_MISMATCH"
	D1ViolationFingerprintMismatch = "D1_FINGERPRINT_MISMATCH"
	D1ViolationCorpusMismatch      = "D1_CORPUS_MISMATCH"
	D1ViolationBuildMismatch       = "D1_BUILD_MISMATCH"
	D1ViolationJudgeMismatch       = "D1_JUDGE_MISMATCH"
	D1ViolationTopKMismatch        = "D1_TOP_K_MISMATCH"
	D1ViolationTuningOnRegression  = "D1_TUNING_ON_REGRESSION_SET"
	D1ViolationHoldoutReusesCorpus = "D1_HOLDOUT_REUSES_REGRESSION_CORPUS"
	D1ViolationPassCount           = "D1_PASS_COUNT_MISMATCH"
	D1ViolationSampleSize          = "D1_SAMPLE_SIZE_MISMATCH"
	D1ViolationCaseSet             = "D1_CASE_SET_MISMATCH"
	D1ViolationDuplicateCase       = "D1_DUPLICATE_CASE"
	D1ViolationPassAgreement       = "D1_PASS_AGREEMENT_BELOW_MIN"
	D1ViolationDeclaredMetric      = "D1_DECLARED_METRIC_MISMATCH"
)

// LoadD1ManifestBytes parses a manifest and fingerprints the raw bytes.
func LoadD1ManifestBytes(data []byte) (*D1Freeze, error) {
	var manifest D1Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("testquality: parse D1 manifest: %w", err)
	}
	sum := sha256.Sum256(data)
	return &D1Freeze{Manifest: manifest, SHA256: hex.EncodeToString(sum[:])}, nil
}

// LoadD1ManifestFile reads and fingerprints a manifest file.
func LoadD1ManifestFile(path string) (*D1Freeze, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("testquality: read D1 manifest: %w", err)
	}
	return LoadD1ManifestBytes(data)
}

// ParseD1Bundle parses a submitted bundle.
func ParseD1Bundle(data []byte) (*D1Bundle, error) {
	var bundle D1Bundle
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return nil, fmt.Errorf("testquality: parse D1 bundle: %w", err)
	}
	return &bundle, nil
}

// Frozen reports whether the manifest has every identity field filled in.
// A draft manifest must never be cited as release evidence.
func (f *D1Freeze) Frozen() bool {
	m := f.Manifest
	if m.Status != "frozen" {
		return false
	}
	required := []string{
		m.Version, m.RecordedAt, m.Owner,
		m.Corpus.Name, m.Corpus.SHA256, m.Corpus.BuildID,
		m.Judge.Model, m.Judge.PromptSHA256,
	}
	for _, value := range required {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	if m.Protocol.SampleSize <= 0 || m.Protocol.Passes <= 0 {
		return false
	}
	if m.Corpus.FileCount <= 0 {
		return false
	}
	return true
}

// Verify recomputes the metrics from per-case rows and checks the bundle
// against the freeze. Contract violations invalidate comparability; quality
// threshold misses are reported but do not set GateEligible=false.
func (f *D1Freeze) Verify(bundle *D1Bundle) (*D1Report, error) {
	if bundle == nil {
		return nil, fmt.Errorf("testquality: D1 bundle is required")
	}
	report := &D1Report{
		ManifestVersion: f.Manifest.Version,
		ManifestSHA256:  f.SHA256,
		System:          bundle.Run,
		Violations:      []string{},
		QualityFindings: []string{},
	}
	m := f.Manifest

	if !f.Frozen() {
		report.Violations = append(report.Violations, D1ViolationManifestUnfrozen)
	}
	if bundle.ManifestVersion != m.Version {
		report.Violations = append(report.Violations, D1ViolationVersionMismatch)
	}
	if !strings.EqualFold(bundle.ManifestSHA256, f.SHA256) {
		report.Violations = append(report.Violations, D1ViolationFingerprintMismatch)
	}
	if !strings.EqualFold(bundle.Run.CorpusSHA256, m.Corpus.SHA256) {
		report.Violations = append(report.Violations, D1ViolationCorpusMismatch)
	}
	if bundle.Run.BuildID != m.Corpus.BuildID {
		report.Violations = append(report.Violations, D1ViolationBuildMismatch)
	}
	if bundle.Run.JudgeModel != m.Judge.Model {
		report.Violations = append(report.Violations, D1ViolationJudgeMismatch)
	}
	if m.Protocol.TopK > 0 && bundle.Run.TopK != m.Protocol.TopK {
		report.Violations = append(report.Violations, D1ViolationTopKMismatch)
	}
	if bundle.Run.TuningUsed {
		report.Violations = append(report.Violations, D1ViolationTuningOnRegression)
	}
	if bundle.Run.SetRole != "regression" && strings.EqualFold(bundle.Run.CorpusSHA256, m.Corpus.SHA256) {
		report.Violations = append(report.Violations, D1ViolationHoldoutReusesCorpus)
	}
	if len(bundle.Passes) != m.Protocol.Passes {
		report.Violations = append(report.Violations, D1ViolationPassCount)
	}

	rows := firstPass(bundle.Passes)
	report.Metrics = computeD1Metrics(rows)
	report.PassAgreement = d1PassAgreement(bundle.Passes)
	report.Metrics.Cases = len(rows)

	if m.Protocol.SampleSize > 0 && len(rows) != m.Protocol.SampleSize {
		report.Violations = append(report.Violations, D1ViolationSampleSize)
	}
	if err := d1CheckCaseSet(bundle.Passes); err != "" {
		report.Violations = append(report.Violations, err)
	}
	if len(bundle.Passes) > 1 && report.PassAgreement < m.Protocol.MinPassAgreement {
		report.Violations = append(report.Violations, D1ViolationPassAgreement)
	}
	if bundle.Declared != nil && metricsDiffer(report.Metrics, *bundle.Declared) {
		report.Violations = append(report.Violations, D1ViolationDeclaredMetric)
	}

	report.QualityFindings = d1QualityFindings(m.Thresholds, report.Metrics)
	report.GateEligible = len(report.Violations) == 0
	sort.Strings(report.Violations)
	sort.Strings(report.QualityFindings)
	return report, nil
}

func firstPass(passes [][]D1CaseRow) []D1CaseRow {
	if len(passes) == 0 {
		return nil
	}
	return passes[0]
}

func computeD1Metrics(rows []D1CaseRow) D1Metrics {
	metrics := D1Metrics{BucketAccuracy: map[string]float64{}}
	if len(rows) == 0 {
		return metrics
	}
	var answerable, unanswerable, overAbstain, abstainCorrect, answeredCorrect, confidentWrong int
	var verifiable, answeredWithCitations int
	var precisionSum float64
	var precisionCases int
	bucketTotal := map[string]int{}
	bucketCorrect := map[string]int{}

	for _, row := range rows {
		if row.Answerable {
			answerable++
		} else {
			unanswerable++
		}
		if row.Abstained {
			if row.Answerable {
				overAbstain++
			} else if row.Correct {
				abstainCorrect++
			}
		} else {
			metrics.Answered++
			if row.Correct {
				answeredCorrect++
			} else if strings.EqualFold(row.Confidence, "high") {
				confidentWrong++
			}
		}
		if !row.Abstained && row.Correct {
			intersection := countIntersection(row.CitedDocs, row.ExpectedDocs)
			if len(row.ExpectedDocs) > 0 {
				answeredWithCitations++
				if intersection > 0 {
					verifiable++
				}
			}
			if len(row.CitedDocs) > 0 {
				precisionSum += float64(intersection) / float64(len(row.CitedDocs))
				precisionCases++
			}
		}
		if row.Bucket != "" {
			bucketTotal[row.Bucket]++
			if row.Correct {
				bucketCorrect[row.Bucket]++
			}
		}
	}

	metrics.Accuracy = ratio(answeredCorrect, len(rows))
	if answeredWithCitations > 0 {
		metrics.CitationVerifiable = floatPtr(ratio(verifiable, answeredWithCitations))
	}
	if precisionCases > 0 {
		metrics.CitationPrecision = floatPtr(precisionSum / float64(precisionCases))
	}
	if answerable > 0 {
		metrics.OverAbstain = floatPtr(ratio(overAbstain, answerable))
	}
	if unanswerable > 0 {
		metrics.AbstainCorrect = floatPtr(ratio(abstainCorrect, unanswerable))
	}
	metrics.ConfidentWrong = ratio(confidentWrong, len(rows))
	for bucket, total := range bucketTotal {
		metrics.BucketAccuracy[bucket] = ratio(bucketCorrect[bucket], total)
	}
	return metrics
}

func indexD1Rows(rows []D1CaseRow) map[string]D1CaseRow {
	index := make(map[string]D1CaseRow, len(rows))
	for _, row := range rows {
		index[row.CaseID] = row
	}
	return index
}

func d1PassAgreement(passes [][]D1CaseRow) float64 {
	if len(passes) < 2 {
		return 1
	}
	base := indexD1Rows(passes[0])
	agree, total := 0, 0
	for _, pass := range passes[1:] {
		for _, row := range pass {
			other, ok := base[row.CaseID]
			if !ok {
				continue
			}
			total++
			if other.Abstained == row.Abstained && other.Correct == row.Correct {
				agree++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(agree) / float64(total)
}

func d1CheckCaseSet(passes [][]D1CaseRow) string {
	seen := map[string]bool{}
	for _, row := range firstPass(passes) {
		if seen[row.CaseID] {
			return D1ViolationDuplicateCase
		}
		seen[row.CaseID] = true
	}
	for _, pass := range passes[1:] {
		if len(pass) != len(seen) {
			return D1ViolationCaseSet
		}
		current := map[string]bool{}
		for _, row := range pass {
			if !seen[row.CaseID] {
				return D1ViolationCaseSet
			}
			current[row.CaseID] = true
		}
		if len(current) != len(seen) {
			return D1ViolationCaseSet
		}
	}
	return ""
}

func d1QualityFindings(thresholds D1Thresholds, metrics D1Metrics) []string {
	findings := []string{}
	if metrics.CitationPrecision == nil {
		findings = append(findings, "citation_precision undefined (no answered case cites a document)")
	} else if *metrics.CitationPrecision < thresholds.CitationPrecisionMin {
		findings = append(findings, fmt.Sprintf("citation_precision %.4f < %.4f", *metrics.CitationPrecision, thresholds.CitationPrecisionMin))
	}
	if metrics.CitationVerifiable == nil {
		findings = append(findings, "citation_verifiable undefined (no expected citations declared)")
	} else if *metrics.CitationVerifiable < thresholds.CitationVerifiableMin {
		findings = append(findings, fmt.Sprintf("citation_verifiable %.4f < %.4f", *metrics.CitationVerifiable, thresholds.CitationVerifiableMin))
	}
	if metrics.OverAbstain != nil && thresholds.OverAbstainMax > 0 {
		if *metrics.OverAbstain < thresholds.OverAbstainMin || *metrics.OverAbstain > thresholds.OverAbstainMax {
			findings = append(findings, fmt.Sprintf("over_abstain %.4f outside [%.2f, %.2f]", *metrics.OverAbstain, thresholds.OverAbstainMin, thresholds.OverAbstainMax))
		}
	}
	if metrics.AbstainCorrect != nil && *metrics.AbstainCorrect < thresholds.AbstainCorrectMin {
		findings = append(findings, fmt.Sprintf("abstain_correct %.4f < %.4f", *metrics.AbstainCorrect, thresholds.AbstainCorrectMin))
	}
	if thresholds.ConfidentWrongMax > 0 && metrics.ConfidentWrong > thresholds.ConfidentWrongMax {
		findings = append(findings, fmt.Sprintf("confident_wrong %.4f > %.4f", metrics.ConfidentWrong, thresholds.ConfidentWrongMax))
	}
	for bucket, minimum := range thresholds.BucketAccuracyMin {
		value, ok := metrics.BucketAccuracy[bucket]
		if !ok {
			continue
		}
		if value < minimum {
			findings = append(findings, fmt.Sprintf("bucket %s accuracy %.4f < %.4f", bucket, value, minimum))
		}
	}
	return findings
}

func metricsDiffer(computed, declared D1Metrics) bool {
	if !almostEqual(computed.Accuracy, declared.Accuracy) || !almostEqual(computed.ConfidentWrong, declared.ConfidentWrong) {
		return true
	}
	pairs := [][2]*float64{
		{computed.CitationVerifiable, declared.CitationVerifiable},
		{computed.CitationPrecision, declared.CitationPrecision},
		{computed.OverAbstain, declared.OverAbstain},
		{computed.AbstainCorrect, declared.AbstainCorrect},
	}
	for _, pair := range pairs {
		if (pair[0] == nil) != (pair[1] == nil) {
			return true
		}
		if pair[0] != nil && !almostEqual(*pair[0], *pair[1]) {
			return true
		}
	}
	return false
}

func almostEqual(a, b float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-9
}

func countIntersection(left, right []string) int {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	set := make(map[string]bool, len(right))
	for _, value := range right {
		set[value] = true
	}
	count := 0
	for _, value := range left {
		if set[value] {
			count++
		}
	}
	return count
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func floatPtr(value float64) *float64 { return &value }
