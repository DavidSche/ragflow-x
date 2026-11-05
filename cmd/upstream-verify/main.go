// upstream-verify is the front command of the upstream RAGFlow version
// upgrade verification pipeline (doc/262 SOP). Run it every time a new
// upstream release is deployed locally: it probes the engine version, runs
// the fail-closed capability version preflight (T6.3), summarises the known
// version deltas, and prints a verdict for whether a live contract re-drill
// is mandatory before the version pin may be raised.
//
// It pairs with scripts/upstream-verify.sh, which orchestrates the actual
// live contract test sets. This command alone is the semantic judge; the
// script is the executor.
//
// Usage (same environment variables as the live contract tests):
//
//	RGX_RAGFLOW_LIVE_BASE_URL=... RGX_RAGFLOW_LIVE_API_KEY=... \
//	  go run ./cmd/upstream-verify [-json]
//
// Exit codes (mirror the VersionGateDecision ordering so the orchestrating
// script can branch on the process exit code alone):
//
//	0  pass                     engine is inside the verified band
//	3  degrade                  engine is older than the verified band
//	4  verification_required    engine is newer than the verified band ->
//	                             live contract re-drill MANDATORY before release
//	2  block                    version cannot be probed/parsed (fail-closed)
//	1  usage / missing env
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/provider/ragflow"
)

const (
	exitUsage                = 1
	exitBlock                = 2
	exitDegrade              = 3
	exitVerificationRequired = 4
	exitPass                 = 0
)

// report is the machine-readable upgrade verification verdict. It is also
// the text block the orchestrating script archives into tmp/ reports.
type report struct {
	ProbedAt             time.Time                   `json:"probed_at"`
	BaseURL              string                      `json:"base_url"` // scheme stripped
	ObservedVersion      string                      `json:"observed_version"`
	PinnedBaseline       string                      `json:"pinned_baseline"`
	VerifiedMax          string                      `json:"verified_max"`
	Decision             ragflow.VersionGateDecision `json:"decision"`
	Reason               string                      `json:"reason"`
	Capabilities         []ragflow.CapabilityVerdict `json:"capabilities"`
	UnclassifiedEndpoint []string                    `json:"unclassified_endpoints"`
	IssueTotals          ragflow.VersionIssueSummary `json:"issue_totals"`
	ContractDrillCommand string                      `json:"contract_drill_command"`
	NextSteps            []string                    `json:"next_steps"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Getenv))
}

func run(args []string, getenv func(string) string) int {
	var asJSON bool
	for _, a := range args {
		switch a {
		case "-json":
			asJSON = true
		case "-h", "--help":
			printUsage()
			return 0
		default:
			fmt.Fprintf(os.Stderr, "upstream-verify: unknown flag %q\n", a)
			printUsage()
			return exitUsage
		}
	}
	base := getenv("RGX_RAGFLOW_LIVE_BASE_URL")
	key := getenv("RGX_RAGFLOW_LIVE_API_KEY")
	if base == "" || key == "" {
		fmt.Fprintln(os.Stderr, "upstream-verify: missing env RGX_RAGFLOW_LIVE_BASE_URL / RGX_RAGFLOW_LIVE_API_KEY")
		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := ragflow.NewHTTPClient(base, key, 15*time.Second, 4)
	observed, probeErr := ragflow.NewVersionProbe(client).ProbeVersion(ctx)

	r := newReport(time.Now(), base, observed, probeErr)

	if asJSON {
		raw, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "upstream-verify: encode report: %v\n", err)
			return exitBlock
		}
		fmt.Println(string(raw))
	} else {
		fmt.Print(renderText(r))
	}
	return exitCodeFor(r.Decision)
}

// maskBaseURL drops the scheme and query/fragment noise so archived reports
// never carry scheme-specific surface; the host:port stays for operator
// identification (it is a local red-line network, matching the live test env
// convention elsewhere in this repo).
func maskBaseURL(base string) string {
	base = strings.TrimSpace(base)
	if i := strings.Index(base, "://"); i >= 0 {
		base = base[i+3:]
	}
	return strings.TrimSuffix(base, "/")
}

func newReport(probedAt time.Time, baseURL, observed string, probeErr error) report {
	preflight := ragflow.RunVersionPreflight(observed, probeErr)
	issues := ragflow.SummarizeVersionIssues()
	gap := ragflow.VersionBandGapReport()

	paths := make([]string, 0, len(gap))
	for _, e := range gap {
		paths = append(paths, e.Method+" "+e.Path)
	}
	sort.Strings(paths)

	// Contract drill command: point the operator at the orchestrating
	// pipeline so a verification_required verdict has a one-liner to run.
	drill := ""
	next := []string{}
	switch preflight.Decision {
	case ragflow.VersionGateVerificationRequired:
		drill = "RGX_RAGFLOW_LIVE_BASE_URL=<engine> RGX_RAGFLOW_LIVE_API_KEY=<key> bash scripts/upstream-verify.sh"
		next = append(next,
			"run the live contract sets (script above); every set must PASS on "+observed+" before raising the pin",
			"then raise: version.go baselineVersion, endpoint_registry.go MaxVerified, capabilities bands, known-issues status (doc/262 §抬版钉)",
		)
	case ragflow.VersionGateDegrade:
		next = append(next,
			"engine is OLDER than the verified band and no MinVersion floor is proven; capability degrade is conservative",
			"either upgrade the engine back inside the band, or deliberately pin a lower verified band after a degraded contract pass",
		)
	case ragflow.VersionGatePass:
		next = append(next,
			"observed version equals the pinned verified band; no contract risk introduced by this engine",
			"still run scripts/upstream-verify.sh once per release to archive the contract evidence (doc/262 §例行)",
		)
	default: // block
		next = append(next,
			"version could not be established (probe error / malformed / empty) - fail-closed, do NOT proceed with any release",
			"check engine reachability and RGX_RAGFLOW_LIVE_BASE_URL / RGX_RAGFLOW_LIVE_API_KEY, then re-run",
		)
	}

	return report{
		ProbedAt:             probedAt,
		BaseURL:              maskBaseURL(baseURL),
		ObservedVersion:      observed,
		PinnedBaseline:       ragflow.PinnedBaselineVersion(),
		VerifiedMax:          ragflow.VerifiedMaxVersion(),
		Decision:             preflight.Decision,
		Reason:               preflight.Reason,
		Capabilities:         preflight.Capabilities,
		UnclassifiedEndpoint: paths,
		IssueTotals:          issues,
		ContractDrillCommand: drill,
		NextSteps:            next,
	}
}

func renderText(r report) string {
	var b strings.Builder
	b.WriteString("upstream-verify - RAGFlow version upgrade verdict\n")
	b.WriteString("  probed at        : " + r.ProbedAt.Format(time.RFC3339) + "\n")
	b.WriteString("  engine base URL  : " + r.BaseURL + "\n")
	if r.ObservedVersion != "" {
		b.WriteString("  observed version : " + r.ObservedVersion + "\n")
	} else {
		b.WriteString("  observed version : (probe failed)\n")
	}
	b.WriteString("  pinned baseline  : " + r.PinnedBaseline + "\n")
	b.WriteString("  verified max     : " + r.VerifiedMax + "\n")
	b.WriteString("  decision         : " + string(r.Decision) + "  <- " + r.Reason + "\n")

	nonPass := make([]ragflow.CapabilityVerdict, 0, len(r.Capabilities))
	for _, c := range r.Capabilities {
		if c.Decision != ragflow.VersionGatePass {
			nonPass = append(nonPass, c)
		}
	}
	sort.Slice(nonPass, func(i, j int) bool {
		if nonPass[i].Decision != nonPass[j].Decision {
			return decisionRank(nonPass[i].Decision) < decisionRank(nonPass[j].Decision)
		}
		return nonPass[i].Name < nonPass[j].Name
	})
	if len(nonPass) > 0 {
		b.WriteString(fmt.Sprintf("  capability gates : %d/%d non-pass\n", len(nonPass), len(r.Capabilities)))
		for _, c := range nonPass {
			b.WriteString(fmt.Sprintf("      %-22s %s  %s\n", c.Name, string(c.Decision), c.Reason))
		}
	} else if len(r.Capabilities) > 0 {
		b.WriteString(fmt.Sprintf("  capability gates : all %d pass\n", len(r.Capabilities)))
	} else {
		b.WriteString("  capability gates : (probe failed, none aggregated)\n")
	}

	if len(r.UnclassifiedEndpoint) > 0 {
		b.WriteString(fmt.Sprintf("  unclassified     : %d endpoint(s) without a version band (T6.2 backlog not drained)\n", len(r.UnclassifiedEndpoint)))
		for _, p := range r.UnclassifiedEndpoint {
			b.WriteString("      " + p + "\n")
		}
	} else {
		b.WriteString("  unclassified     : none (version band backlog drained)\n")
	}

	it := r.IssueTotals
	b.WriteString(fmt.Sprintf("  known version issues: total=%d regression=%d observed=%d untested=%d\n",
		it.Total, it.Regression, it.Observed, it.Untested))
	if len(it.RegressionMap) > 0 {
		names := make([]string, 0, len(it.RegressionMap))
		for c := range it.RegressionMap {
			names = append(names, c)
		}
		sort.Strings(names)
		b.WriteString("      regressions  : " + strings.Join(names, ", ") + "\n")
	}

	if r.ContractDrillCommand != "" {
		b.WriteString("\n  next (required)  : " + r.ContractDrillCommand + "\n")
	}
	for _, s := range r.NextSteps {
		b.WriteString("  note             : " + s + "\n")
	}
	return b.String()
}

func decisionRank(d ragflow.VersionGateDecision) int {
	switch d {
	case ragflow.VersionGateBlock:
		return 0
	case ragflow.VersionGateVerificationRequired:
		return 1
	case ragflow.VersionGateDegrade:
		return 2
	default:
		return 3
	}
}

// exitCodeFor maps the preflight decision to a stable process exit code the
// orchestrating shell branches on.
func exitCodeFor(d ragflow.VersionGateDecision) int {
	switch d {
	case ragflow.VersionGatePass:
		return exitPass
	case ragflow.VersionGateDegrade:
		return exitDegrade
	case ragflow.VersionGateVerificationRequired:
		return exitVerificationRequired
	default:
		return exitBlock
	}
}

func printUsage() {
	fmt.Println("upstream-verify: RAGFlow version upgrade verification front (doc/262)")
	fmt.Println("  env  RGX_RAGFLOW_LIVE_BASE_URL, RGX_RAGFLOW_LIVE_API_KEY")
	fmt.Println("  flag -json  machine-readable report; default text verdict")
	fmt.Println("  exit 0=pass 2=block 3=degrade 4=verification_required")
}
