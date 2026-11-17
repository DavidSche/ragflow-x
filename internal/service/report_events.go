package service

// Report job event types (doc/288 §8.8). This is the *broadcast* closed set the
// SSE stream and the frontend reducer share — 15 values, no more.
//
// The internal outbox type `report_job_finalize` is deliberately not here: outbox
// events are delivery mechanics, and mixing the two registries would let a
// mechanical retry type leak into the UI contract (doc/288 §7.4 / R31).
//
// Adding a value is a three-place contract change: this list, the frontend
// reducer's exhaustive switch, and the SSE contract test.
const (
	reportEventJobQueued        = "job.queued"
	reportEventOutlineReady     = "outline.ready"
	reportEventOutlineConfirmed = "outline.confirmed"
	reportEventSectionStarted   = "section.started"
	reportEventSectionSucceeded = "section.succeeded"
	reportEventSectionFailed    = "section.failed"
	reportEventJobProgress      = "job.progress"
	reportEventJobQualityGate   = "job.quality_gate"
	reportEventJobAssembling    = "job.assembling"
	reportEventJobFinalizing    = "job.finalizing"
	reportEventArtifactReady    = "artifact.ready"
	reportEventJobSucceeded     = "job.succeeded"
	reportEventJobPartial       = "job.partial"
	reportEventJobFailed        = "job.failed"
	reportEventJobCancelled     = "job.cancelled"
)

// ReportJobEventTypes returns the closed set of broadcastable event types, in the
// order §8.8 lists them (lifecycle order). Callers must copy before mutating.
func ReportJobEventTypes() []string {
	return []string{
		reportEventJobQueued, reportEventOutlineReady, reportEventOutlineConfirmed,
		reportEventSectionStarted, reportEventSectionSucceeded, reportEventSectionFailed,
		reportEventJobProgress, reportEventJobQualityGate, reportEventJobAssembling,
		reportEventJobFinalizing, reportEventArtifactReady, reportEventJobSucceeded,
		reportEventJobPartial, reportEventJobFailed, reportEventJobCancelled,
	}
}

// isReportTerminalEvent reports whether an event type can only be written by the
// finalize transaction. The frontend uses the same boundary to decide when to stop
// polling; the backend uses it to keep terminal writes in one place (§8.2).
func isReportTerminalEvent(eventType string) bool {
	switch eventType {
	case reportEventJobSucceeded, reportEventJobPartial, reportEventJobFailed, reportEventJobCancelled:
		return true
	}
	return false
}

// ReportEventTerminal exposes the boundary above to the SSE handler, which closes
// the connection after the frame no later event can follow. It is a wrapper rather
// than a second switch so the handler cannot drift from the finalize set.
func ReportEventTerminal(eventType string) bool { return isReportTerminalEvent(eventType) }
