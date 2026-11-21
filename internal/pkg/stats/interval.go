// Package stats holds the exact binomial arithmetic that the plan's thresholds
// are *stated in* (doc/301 §4.2, §9.2, §14.A).
//
// It exists as its own package for one reason: two places have to compute the same
// interval — the release gate (`internal/service.EvaluateReleaseGate`, which certifies
// a run) and the gold-set evaluation (`cmd/goldtool eval`, which certifies an
// experiment). If those two ever computed "CP upper bound" differently, the product
// would be able to pass a gate and fail the experiment that gate is supposed to
// rest on, with both sides printing a plausible number. One implementation, one set
// of pinned reference values.
//
// Everything here is Clopper–Pearson (exact) rather than a normal approximation:
// the plan's denominators are 36/40/24/12/8, and at those sizes a Wald interval
// produces zero-width bounds at the extremes — the arithmetic would claim certainty
// exactly where the sample says nothing.
package stats

import "math"

// Alpha is the one-sided tail of a two-sided 95% interval. The plan's ceilings
// ("CP upper bound ≤10%") are one-sided statements at this alpha, and doc/301 §14.A
// quotes 0/36 → 9.74% at the same value, so the constant is part of the contract
// rather than a local choice.
const Alpha = 0.025

// Upper returns the exact one-sided upper confidence bound for a binomial
// proportion: the largest θ with P(X ≤ k; n, θ) ≥ alpha.
//
// It is computed by bisection on the regularized incomplete beta function rather
// than by pulling in a statistics dependency, because the gate must run offline and
// its numbers must not change with a library version bump. The one closed form worth
// keeping in mind is k=0: the bound is 1−alpha^(1/n), so 0/36 at alpha=0.025 is
// 9.74% — the figure doc/301 §14.A quotes.
func Upper(k, n int64, alpha float64) float64 {
	if n <= 0 {
		return 1.0
	}
	if k >= n {
		return 1.0
	}
	if k < 0 {
		k = 0
	}
	if alpha <= 0 || alpha >= 1 {
		alpha = Alpha
	}
	// Closed forms at the two ends keep the bisection away from its worst conditioning.
	if k == 0 {
		return 1 - math.Pow(alpha, 1/float64(n))
	}
	low, high := float64(k)/float64(n), 1.0
	for i := 0; i < 200; i++ {
		mid := (low + high) / 2
		// P(X <= k; n, mid) = I_{1-mid}(n-k, k+1)
		cdf := betaRegularized(float64(n-k), float64(k+1), 1-mid)
		if cdf > alpha {
			low = mid
		} else {
			high = mid
		}
		if high-low < 1e-12 {
			break
		}
	}
	return (low + high) / 2
}

// Lower returns the exact one-sided lower confidence bound. It is the mirror image of
// Upper (Lower(k,n,α) = 1 − Upper(n−k,n,α)), which is why it is written as that
// subtraction rather than as a second bisection: two bisections could disagree at the
// last digit, and a report that prints "23/24 ∈ [78.9%, 99.9%]" must not depend on
// which side was computed.
//
// This is the bound the plan gates `conflicting_recall` on (doc/301 §9.2: "召回 CP
// 下界 ≥78%"), and it is the reason 22/24 is *not* enough: 91.7% point estimate,
// 73.0% lower bound.
func Lower(k, n int64, alpha float64) float64 {
	if n <= 0 || k <= 0 {
		return 0
	}
	if k >= n {
		return 1 - Upper(0, n, alpha)
	}
	return 1 - Upper(n-k, n, alpha)
}

// Interval returns the exact two-sided Clopper–Pearson interval at the given
// confidence (0.95 for everything the plan reports).
//
// Both bounds are returned as fractions in [0,1]; the caller formats them, because a
// report has to print the numerator and denominator next to the percentage or the
// interval has no denominator and cannot be checked (doc/301 §4.2).
func Interval(k, n int64, confidence float64) (low, high float64) {
	if n <= 0 {
		return 0, 1
	}
	if confidence <= 0 || confidence >= 1 {
		confidence = 0.95
	}
	alpha := (1 - confidence) / 2
	return Lower(k, n, alpha), Upper(k, n, alpha)
}

// betaRegularized is the regularized incomplete beta I_x(a,b).
func betaRegularized(a, b, x float64) float64 {
	if x <= 0 {
		return 0
	}
	if x >= 1 {
		return 1
	}
	// log B(a,b) enters with a minus sign: I_x(a,b) = x^a(1-x)^b / B(a,b) * cf / a.
	// Getting this sign wrong makes every bound collapse toward 0, which reads as a
	// *stricter* gate — the failure mode the known-value test exists to catch.
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	lab, _ := math.Lgamma(a + b)
	// `logBeta` below is −ln B(a,b) = ln Γ(a+b) − ln Γ(a) − ln Γ(b), which is what the
	// usual expression adds to the exponents. Writing the sign the intuitive way
	// (ln B(a,b) with a minus) yields bounds that collapse toward 0 — a *stricter*
	// looking gate that proves nothing, which is why the known-value test exists.
	logBeta := lab - la - lb
	front := math.Exp(logBeta + a*math.Log(x) + b*math.Log(1-x))
	// The continued fraction converges fast for x < (a+1)/(a+b+2); mirror otherwise.
	if x < (a+1)/(a+b+2) {
		return front * betaContinuedFraction(a, b, x) / a
	}
	return 1 - front*betaContinuedFraction(b, a, 1-x)/b
}

// betaContinuedFraction is the modified Lentz evaluation of the incomplete beta
// continued fraction (Numerical Recipes §6.4).
func betaContinuedFraction(a, b, x float64) float64 {
	const (
		maxIterations = 300
		epsilon       = 3e-14
		tiny          = 1e-30
	)
	qab, qap, qam := a+b, a+1, a-1
	c := 1.0
	d := 1 - qab*x/qap
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	h := d
	for m := 1; m <= maxIterations; m++ {
		mf := float64(m)
		m2 := 2 * mf
		aa := mf * (b - mf) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		h *= d * c
		aa = -(a + mf) * (qab + mf) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		delta := d * c
		h *= delta
		if math.Abs(delta-1) < epsilon {
			break
		}
	}
	return h
}
