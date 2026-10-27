package ragflow

import (
	"errors"
	"strconv"
	"strings"
)

// T6.3 Capability version preflight (223 T6 / 224 §2.4): a fail-closed gate
// over the registry version bands. The probe (VersionProbe.ProbeVersion) is
// the mandatory "real contract drill" guard before any RAGFlow upgrade that
// touches the version-sensitive surface. VerifiedMaxVersion is resolved from
// the registry data (T6.2 set MaxVerified=v1.0.0-rc1 everywhere).

// releaseParts is the parsed loose RAGFlow release shape v?MAJOR.MINOR.PATCH
// with an optional -rcN pre-release marker. A release without a marker is a
// stable release; a stable release sorts above any pre-release.
type releaseParts struct {
	major, minor, patch int
	rc                  int // -1 when the release is not an rc pre-release
}

func parseRelease(version string) (releaseParts, error) {
	var parts releaseParts
	s := strings.TrimPrefix(strings.TrimSpace(version), "v")
	rcIndex := strings.Index(s, "-rc")
	if rcIndex == 0 {
		return parts, errors.New("release missing a version number")
	}
	main := s
	parts.rc = -1
	if rcIndex >= 0 {
		main = s[:rcIndex]
		rcNum, err := strconv.Atoi(s[rcIndex+3:])
		if err != nil {
			return parts, errors.New("malformed rc number in " + version)
		}
		parts.rc = rcNum
	} else if dash := strings.Index(s, "-"); dash >= 0 {
		main = s[:dash]
	}
	nums := strings.Split(main, ".")
	if len(nums) != 3 {
		return parts, errors.New("malformed release version " + version)
	}
	values := [3]int{}
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(nums[i])
		if err != nil {
			return parts, errors.New("malformed release component in " + version)
		}
		values[i] = n
	}
	parts.major, parts.minor, parts.patch = values[0], values[1], values[2]
	return parts, nil
}

// CompareReleaseVersions orders two loose RAGFlow release strings. rc1 < rc2 <
// the stable release with the same core numbers. Returns -1/0/1 or an error on
// a malformed input. 223 T6 / 224 §2.4.
func CompareReleaseVersions(a, b string) (int, error) {
	pa, err := parseRelease(a)
	if err != nil {
		return 0, err
	}
	pb, err := parseRelease(b)
	if err != nil {
		return 0, err
	}
	for _, pair := range [][2]int{{pa.major, pb.major}, {pa.minor, pb.minor}, {pa.patch, pb.patch}} {
		if pair[0] < pair[1] {
			return -1, nil
		}
		if pair[0] > pair[1] {
			return 1, nil
		}
	}
	// Core numbers equal: order by release-ness, then rc number.
	rcOrder := func(rc int) int {
		if rc >= 0 {
			return rc
		}
		return int(^uint(0)>>1) // stable sorts above every rc
	}
	ao, bo := rcOrder(pa.rc), rcOrder(pb.rc)
	switch {
	case ao < bo:
		return -1, nil
	case ao > bo:
		return 1, nil
	}
	return 0, nil
}

// VersionGateDecision is the outcome of the version preflight. Ordering is
// meaningful: Block > VerificationRequired > Degrade > Pass.
type VersionGateDecision string

const (
	// VersionGateBlock rejects when the engine version cannot be established
	// (fail-closed, aligned with gateway quota's fail-closed stance).
	VersionGateBlock VersionGateDecision = "block"
	// VersionGateVerificationRequired appears when the observed engine is
	// newer than the verified band: the release cannot proceed until the
	// live contract drill passes on that version.
	VersionGateVerificationRequired VersionGateDecision = "verification_required"
	// VersionGateDegrade appears when the observed engine is older than the
	// verified band and no MinVersion floor is proven: the capability is
	// served conservatively (degraded) instead of trusted.
	VersionGateDegrade VersionGateDecision = "degrade"
	// VersionGatePass means the observed engine is inside the verified band.
	VersionGatePass VersionGateDecision = "pass"
)

// decisionRank orders decisions from most to least severe for aggregation.
func decisionRank(decision VersionGateDecision) int {
	switch decision {
	case VersionGateBlock:
		return 0
	case VersionGateVerificationRequired:
		return 1
	case VersionGateDegrade:
		return 2
	default:
		return 3
	}
}

func worseDecision(a, b VersionGateDecision) VersionGateDecision {
	if decisionRank(a) <= decisionRank(b) {
		return a
	}
	return b
}

// VerifiedMaxVersion resolves the newest MaxVerified across the registry
// (T6.2 has set every endpoint to v1.0.0-rc1, so this is the verified band
// ceiling). Returns "" when nothing is verified.
func VerifiedMaxVersion() string {
	max := ""
	for _, e := range endpointRegistry {
		if e.MaxVerified == "" {
			continue
		}
		if max == "" {
			max = e.MaxVerified
			continue
		}
		if cmp, err := CompareReleaseVersions(e.MaxVerified, max); err == nil && cmp > 0 {
			max = e.MaxVerified
		}
	}
	return max
}

// EvaluateVersionGate is the fail-closed 面级 gate over ObserveVersioned.
// probeErr or an empty observation blocks; an engine inside the verified band
// passes; newer engines require contract re-drill; older engines degrade.
func EvaluateVersionGate(observed string, probeErr error) (VersionGateDecision, string) {
	if probeErr != nil {
		return VersionGateBlock, "version probe failed: " + probeErr.Error()
	}
	observed = strings.TrimSpace(observed)
	if observed == "" {
		return VersionGateBlock, "no engine version observed"
	}
	verified := VerifiedMaxVersion()
	if verified == "" {
		return VersionGateBlock, "registry has no verified version band"
	}
	cmp, err := CompareReleaseVersions(observed, verified)
	if err != nil {
		return VersionGateBlock, "cannot compare observed version " + observed + ": " + err.Error()
	}
	switch {
	case cmp < 0:
		return VersionGateDegrade, "engine " + observed + " is below the verified band " + verified + "; capability degrades conservatively"
	case cmp > 0:
		return VersionGateVerificationRequired, "engine " + observed + " exceeds the verified band " + verified + "; live contract re-drill required before release"
	default:
		return VersionGatePass, "engine " + observed + " is within the verified band " + verified
	}
}

// CapabilityVerdict is one capability's aggregated band verdict across the
// endpoints that depend on it.
type CapabilityVerdict struct {
	Name     string
	Decision VersionGateDecision
	Reason   string
}

// VersionPreflight is the upgrade preflight output: the observed engine, the
// overall decision and the per-capability rollup. 223 T6/224 §2.4 T6.3.
type VersionPreflight struct {
	ObservedVersion string
	Decision        VersionGateDecision
	Reason          string
	Capabilities    []CapabilityVerdict
}

// RunVersionPreflight aggregates the registry bands per capability. Every
// capability that no endpoint depends on today is omitted. The overall
// decision is the worst across capabilities (and the face-level gate).
func RunVersionPreflight(observed string, probeErr error) VersionPreflight {
	preflight := VersionPreflight{ObservedVersion: observed}
	if probeErr != nil || strings.TrimSpace(observed) == "" {
		decision, reason := EvaluateVersionGate(observed, probeErr)
		preflight.Decision, preflight.Reason = decision, reason
		return preflight
	}
	capabilities := map[string]map[string]VersionGateDecision{}
	for _, e := range endpointRegistry {
		for _, capability := range e.UsedByCapability {
			if capabilities[capability] == nil {
				capabilities[capability] = map[string]VersionGateDecision{}
			}
			if e.MaxVerified == "" {
				capabilities[capability][e.Method+" "+e.Path] = VersionGateBlock
				continue
			}
			cmp, err := CompareReleaseVersions(observed, e.MaxVerified)
			if err != nil {
				capabilities[capability][e.Method+" "+e.Path] = VersionGateBlock
				continue
			}
			switch {
			case cmp < 0:
				capabilities[capability][e.Method+" "+e.Path] = VersionGateDegrade
			case cmp > 0:
				capabilities[capability][e.Method+" "+e.Path] = VersionGateVerificationRequired
			default:
				capabilities[capability][e.Method+" "+e.Path] = VersionGatePass
			}
		}
	}
	overall := VersionGatePass
	for capability, endpointDecisions := range capabilities {
		aggregate := VersionGatePass
		for _, decision := range endpointDecisions {
			aggregate = worseDecision(aggregate, decision)
		}
		preflight.Capabilities = append(preflight.Capabilities, CapabilityVerdict{
			Name: capability, Decision: aggregate,
			Reason: "worst band verdict across " + strconv.Itoa(len(endpointDecisions)) + " endpoints",
		})
		overall = worseDecision(overall, aggregate)
	}
	if overall == VersionGatePass {
		overall, _ = EvaluateVersionGate(observed, nil)
	}
	reason := "within verified band"
	if overall != VersionGatePass {
		reason = "upgrade preflight not clean; see capability verdicts"
	}
	preflight.Decision, preflight.Reason = overall, reason
	return preflight
}
