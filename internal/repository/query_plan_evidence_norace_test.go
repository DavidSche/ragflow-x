//go:build !race

package repository

// queryPlanEvidenceUnderRaceDetector reports whether this binary was built with
// the race detector, in which case query plan runtimes are not comparable
// evidence and must never be written to the nightly evidence directory.
const queryPlanEvidenceUnderRaceDetector = false
