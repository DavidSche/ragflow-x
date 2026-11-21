package stats

import (
	"math"
	"testing"
)

// TestTheIntervalMatchesThePlannedFigures pins the arithmetic against the exact
// numbers doc/301 prints, not against itself.
//
// This is the one test that makes the plan's thresholds checkable: "0/36 ⇒ 上界 9.74%"
// (§14.A), "1/40 ⇒ 13.16%", "0/72 ⇒ 4.99%", "1/60 ⇒ 8.94%" (§4.2 sample-size table),
// "22/24 ⇒ [73.0, 99.0]", "23/24 ⇒ [78.9, 99.9]" (§9.2 E2), "88/102 ⇒ [78.04, 92.29]"
// and "87/102 ⇒ [76.9, 91.5]" (§4.2 accuracy rows), "9/12 ⇒ [42.8, 94.5]" and
// "6/8 ⇒ [34.9, 96.8]" (§4.2 observation rows). Every reference value was produced by
// `scipy.stats.beta.ppf` at the same alpha.
//
// A bound that is quietly too narrow is the dangerous direction: it certifies with
// arithmetic that would not survive a reviewer, and nothing else in the system would
// notice.
func TestTheIntervalMatchesThePlannedFigures(t *testing.T) {
	upper := []struct {
		k, n int64
		want float64
	}{
		{0, 36, 0.097394}, // §14.A: 0/36 ⇒ 9.74%
		{1, 40, 0.131586}, // §4.2: 1/40 ⇒ 13.16% (fails ≤10%, hence "only add samples")
		{0, 72, 0.049944}, // §4.2: 0/72 ⇒ 4.99%
		{1, 60, 0.089399}, // §4.2: 1/60 ⇒ 8.94%
		{1, 36, 0.145289},
		{5, 36, 0.294975},
		{2, 20, 0.316983},
		{0, 12, 0.264648},
		{36, 36, 1.0}, // k=n is certainty, not a bisection
	}
	for _, tc := range upper {
		if got := Upper(tc.k, tc.n, Alpha); math.Abs(got-tc.want) > 5e-4 {
			t.Fatalf("Upper(%d/%d) = %.6f, want %.6f", tc.k, tc.n, got, tc.want)
		}
	}

	lower := []struct {
		k, n int64
		want float64
	}{
		{22, 24, 0.730027}, // §9.2: 22/24 ⇒ 下界 73.0%（点估计达标、下界不达标）
		{23, 24, 0.788798}, // §9.2: 23/24 ⇒ [78.9, 99.9]，唯一过线的最小改动
		{9, 12, 0.428142},  // §4.2 观察行：outdated_recall 9/12
		{6, 8, 0.349144},   // §4.2 观察行：6/8
		{88, 102, 0.780443},
		{87, 102, 0.769073}, // §4.2: 87/102 下界 76.9% 不够
	}
	for _, tc := range lower {
		if got := Lower(tc.k, tc.n, Alpha); math.Abs(got-tc.want) > 5e-4 {
			t.Fatalf("Lower(%d/%d) = %.6f, want %.6f", tc.k, tc.n, got, tc.want)
		}
	}
}

// TestTheTwoSidedIntervalBracketsThePointEstimate pins the property that makes the
// interval usable in a report: it must contain the observed proportion, and it must
// reach 0 at k=0 and 1 at k=n (Clopper–Pearson is exact, so the degenerate ends are
// part of the definition rather than an artefact).
func TestTheTwoSidedIntervalBracketsThePointEstimate(t *testing.T) {
	cases := []struct{ k, n int64 }{{0, 36}, {1, 40}, {22, 24}, {6, 8}, {24, 24}, {120, 120}}
	for _, c := range cases {
		low, high := Interval(c.k, c.n, 0.95)
		point := float64(c.k) / float64(c.n)
		if low > point+1e-9 || high < point-1e-9 {
			t.Fatalf("%d/%d: interval [%.6f, %.6f] does not bracket %.6f", c.k, c.n, low, high, point)
		}
		if c.k == 0 && low != 0 {
			t.Fatalf("0/%d: lower bound must be 0, got %.9f", c.n, low)
		}
		if c.k == c.n && high != 1 {
			t.Fatalf("%d/%d: upper bound must be 1, got %.9f", c.k, c.n, high)
		}
	}
}

// TestDegenerateDenominatorsDoNotClaimCertainty: with n=0 there is no estimate, so the
// interval must be the whole unit interval. Returning anything narrower would let an
// empty sample read as a measurement — the same failure the gate's "no denominator ⇒
// unverified" rule exists to prevent.
func TestDegenerateDenominatorsDoNotClaimCertainty(t *testing.T) {
	if low, high := Interval(0, 0, 0.95); low != 0 || high != 1 {
		t.Fatalf("Interval(0/0) = [%v, %v], want [0, 1]", low, high)
	}
	if got := Upper(0, 0, Alpha); got != 1 {
		t.Fatalf("Upper(0/0) = %v, want 1", got)
	}
	if got := Lower(0, 0, Alpha); got != 0 {
		t.Fatalf("Lower(0/0) = %v, want 0", got)
	}
}

// TestLowerIsTheMirrorOfUpper: the two one-sided bounds come from one bisection, so a
// report cannot print a pair that disagrees with the counts it also prints.
func TestLowerIsTheMirrorOfUpper(t *testing.T) {
	for k := int64(0); k <= 24; k++ {
		got := Lower(k, 24, Alpha)
		want := 1 - Upper(24-k, 24, Alpha)
		if math.Abs(got-want) > 1e-12 {
			t.Fatalf("Lower(%d/24) = %.9f but 1−Upper(%d/24) = %.9f", k, got, 24-k, want)
		}
	}
}
