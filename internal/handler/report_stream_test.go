package handler

// The report job surface (doc/288 §8.3 / §8.8 / T5): the snapshot that carries the
// resume cursor, and the SSE stream that replays from it.
//
// These tests go through the router for everything that has a status code, because
// the two failure modes worth pinning are HTTP-shaped:
//
//   - an expired cursor must be a 409 *before* any `text/event-stream` header, or a
//     client that already committed to reading frames treats the empty stream as
//     "no new events" and misses the terminal one;
//   - a foreign job id must be 404/44109 and not an existence oracle.
//
// Framing is asserted on the raw body: the frontend parser only understands `data:`
// lines, so `seq` inside the JSON body is the contract, and the `id:` line is the
// proxy/EventSource convenience that must agree with it.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/service"
)

// reportStreamTestTimeout is how long a stream test waits for the handler to finish
// on its own. A terminal event must close the stream well inside it; a stream that
// is supposed to stay open is given a much shorter budget on purpose.
const reportStreamTestTimeout = 5 * time.Second

// reportStream issues a GET that is allowed to hang and returns what was written.
// `finished` is false when the handler had to be cancelled, which is how the
// open-ended case proves it really was waiting for the next event.
func (h *reportHarness) reportStream(t *testing.T, path string, headers map[string]string, timeout time.Duration) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.router.ServeHTTP(rec, req)
	}()
	select {
	case <-done:
		return rec, true
	case <-time.After(timeout):
		cancel()
		<-done
		return rec, false
	}
}

// reportAppendEvent writes one event through the repository, the way a producing
// transaction would. Fixtures may write events directly: what is under test is the
// transport, and the sequence allocation is the repository's own contract (A29).
func reportAppendEvent(t *testing.T, svc *service.Service, tenantID, jobID, eventType, payload string) int64 {
	t.Helper()
	seq, err := svc.Store.AppendReportEvent(context.Background(), &model.ReportJobEvent{
		TenantID: tenantID, JobID: jobID, Type: eventType, PayloadJSON: payload,
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("append %s event: %v", eventType, err)
	}
	if seq <= 0 {
		t.Fatalf("append %s event: seq must be assigned, got %d", eventType, seq)
	}
	return seq
}

// seededReportJob creates a report through the route and returns the job id its
// creation wrote. The job is `queued` with exactly one event (`job.queued`).
func seededReportJob(t *testing.T, svc *service.Service, engine *reportHandlerEngine, fx *reportHarness, name string) string {
	t.Helper()
	datasetID := seedReportHandlerDataset(t, svc, engine, fx.tenantID, name)
	rec, body := fx.do(t, http.MethodPost, "/api/v1/reports", "req-"+name+"-0001", reportCreateBody(datasetID))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: %d (%s)", rec.Code, rec.Body.String())
	}
	data, _ := body["data"].(map[string]interface{})
	job, _ := data["job"].(map[string]interface{})
	jobID, _ := job["id"].(string)
	if jobID == "" {
		t.Fatalf("create must return the job: %v", body)
	}
	return jobID
}

func TestReportJobSnapshotRouteCarriesResumeCursor(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "snapshot", model.RoleOperator)
	jobID := seededReportJob(t, svc, engine, fx, "policy-job-snapshot")

	rec, body := fx.do(t, http.MethodGet, "/api/v1/report-jobs/"+jobID, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot must be 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	data, _ := body["data"].(map[string]interface{})
	if seq, _ := data["event_seq"].(float64); int64(seq) != 1 {
		t.Fatalf("a queued job has written exactly one event: %v", data["event_seq"])
	}
	if status, _ := data["status"].(string); status != model.ReportJobQueued {
		t.Fatalf("snapshot status must be the row's, got %q", status)
	}
	if terminal, _ := data["terminal"].(bool); terminal {
		t.Fatal("a queued job is not terminal")
	}
	if data["job_id"] != jobID {
		t.Fatalf("snapshot must answer the job that was asked for: %v", data["job_id"])
	}
	if revision, _ := data["revision"].(map[string]interface{}); revision["revision"].(float64) != 1 {
		t.Fatalf("the first run is revision 1: %v", revision["revision"])
	}
	if sections, _ := data["sections"].([]interface{}); sections == nil {
		t.Fatalf("sections must be [] before the outline exists: %v", data["sections"])
	}
	if gaps, _ := data["gaps"].([]interface{}); gaps == nil {
		t.Fatalf("gaps must be [] and never null: %v", data["gaps"])
	}
}

func TestReportStreamReplaysFromCursor(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "stream", model.RoleOperator)
	jobID := seededReportJob(t, svc, engine, fx, "policy-job-stream")
	reportAppendEvent(t, svc, fx.tenantID, jobID, "job.progress", `{"progress_done":1,"progress_total":5}`)
	reportAppendEvent(t, svc, fx.tenantID, jobID, "job.failed", `{"error_code":"44103"}`)

	rec, finished := fx.reportStream(t, "/api/v1/report-jobs/"+jobID+"/events", nil, reportStreamTestTimeout)
	if !finished {
		t.Fatal("a terminal event must close the stream without the client cancelling")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("stream must be 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("stream must be text/event-stream, got %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("a replayed stream must not be cached, got %q", cc)
	}
	frames := reportParseFrames(t, rec.Body.String())
	if len(frames) != 3 {
		t.Fatalf("expected three frames (queued, progress, failed), got %d: %s", len(frames), rec.Body.String())
	}
	for i, frame := range frames {
		if frame.ID != int64(i+1) || frame.Seq != int64(i+1) {
			t.Fatalf("frame %d must carry id and body seq %d, got id=%d seq=%d", i, i+1, frame.ID, frame.Seq)
		}
	}
	if frames[0].Type != "job.queued" || frames[2].Type != "job.failed" {
		t.Fatalf("frames must preserve event order and type: %+v", frames)
	}
	if frames[2].JobID != jobID {
		t.Fatalf("each frame names its job: %+v", frames[2])
	}
	if !strings.Contains(rec.Body.String(), `"progress_done":1`) {
		t.Fatalf("payload must pass through untouched: %s", rec.Body.String())
	}

	// `after=1` resumes strictly after the cursor: the queued frame is not replayed.
	rec, _ = fx.reportStream(t, "/api/v1/report-jobs/"+jobID+"/events?after=1", nil, reportStreamTestTimeout)
	resumed := reportParseFrames(t, rec.Body.String())
	if len(resumed) != 2 || resumed[0].Seq != 2 {
		t.Fatalf("after=1 must start at seq 2, got %+v", resumed)
	}
}

func TestReportStreamHonorsLastEventIdHeader(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "stream-header", model.RoleOperator)
	jobID := seededReportJob(t, svc, engine, fx, "policy-job-stream-header")
	reportAppendEvent(t, svc, fx.tenantID, jobID, "job.failed", `{}`)

	// EventSource clients send the header, never the query; both must resume the
	// same way or the two client styles would see different streams.
	rec, _ := fx.reportStream(t, "/api/v1/report-jobs/"+jobID+"/events",
		map[string]string{"Last-Event-ID": "1"}, reportStreamTestTimeout)
	if frames := reportParseFrames(t, rec.Body.String()); len(frames) != 1 || frames[0].Seq != 2 {
		t.Fatalf("Last-Event-ID=1 must start at seq 2, got %+v", frames)
	}

	// The query wins when both are present: it is the primary protocol.
	rec, _ = fx.reportStream(t, "/api/v1/report-jobs/"+jobID+"/events?after=0",
		map[string]string{"Last-Event-ID": "1"}, reportStreamTestTimeout)
	if frames := reportParseFrames(t, rec.Body.String()); len(frames) != 2 {
		t.Fatalf("?after=0 must win over the header, got %+v", frames)
	}
}

func TestReportStreamRefusesExpiredCursor(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "stream-expired", model.RoleOperator)
	jobID := seededReportJob(t, svc, engine, fx, "policy-job-stream-expired")
	// Run the real janitor past the retention window: the creation event (seq 1) is
	// purged, then two fresh events land at seq 2 and 3. A cursor at 1 now points
	// into a hole, which is exactly what the retention pass creates in production.
	if _, err := svc.Store.PurgeExpiredReportEvents(
		context.Background(), time.Now().UTC().Add(8*24*time.Hour), 100,
	); err != nil {
		t.Fatalf("purge expired events: %v", err)
	}
	progress := reportAppendEvent(t, svc, fx.tenantID, jobID, "job.progress", `{"progress_done":1}`)
	reportAppendEvent(t, svc, fx.tenantID, jobID, "job.failed", `{}`)
	if progress != 2 {
		t.Fatalf("the surviving range must start at seq 2, got %d", progress)
	}

	rec, body := fx.do(t, http.MethodGet, "/api/v1/report-jobs/"+jobID+"/events?after=1", "", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("an expired cursor must be 409, got %d (%s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("a refusal must not open a stream, got %q", ct)
	}
	if code := envelopeCode(t, body); code != 44115 {
		t.Fatalf("an expired cursor must answer 44115, got %d", code)
	}
	data, _ := body["data"].(map[string]interface{})
	if reset, _ := data["reset_required"].(bool); !reset {
		t.Fatalf("the client must be told to reset: %v", data)
	}
	if minSeq, _ := data["min_seq"].(float64); int64(minSeq) != progress {
		t.Fatalf("min_seq must be the oldest surviving seq (%d): %v", progress, data["min_seq"])
	}
	if eventSeq, _ := data["event_seq"].(float64); int64(eventSeq) != 3 {
		t.Fatalf("event_seq must be the job's allocation (3): %v", data["event_seq"])
	}

	// Resuming from the oldest surviving event is accepted and replays the tail.
	rec, _ = fx.reportStream(t, "/api/v1/report-jobs/"+jobID+"/events?after=2", nil, reportStreamTestTimeout)
	if rec.Code != http.StatusOK {
		t.Fatalf("a continuous cursor must be accepted, got %d", rec.Code)
	}
	if frames := reportParseFrames(t, rec.Body.String()); len(frames) != 1 || frames[0].Seq != 3 {
		t.Fatalf("the tail must be replayed from seq 3, got %+v", frames)
	}
}

func TestReportStreamTreatsUnreadableCursorAsFullReplay(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "stream-garbage", model.RoleOperator)
	jobID := seededReportJob(t, svc, engine, fx, "policy-job-stream-garbage")
	reportAppendEvent(t, svc, fx.tenantID, jobID, "job.failed", `{}`)

	// A cursor that cannot be parsed is "no cursor": replaying from the start is
	// always correct, and a 400 here would need a code the 441xx set does not have.
	for _, cursor := range []string{"abc", "-1", "1.5"} {
		rec, _ := fx.reportStream(t, "/api/v1/report-jobs/"+jobID+"/events?after="+cursor, nil, reportStreamTestTimeout)
		if rec.Code != http.StatusOK {
			t.Fatalf("after=%s must be a full replay, got %d", cursor, rec.Code)
		}
		if frames := reportParseFrames(t, rec.Body.String()); len(frames) != 2 || frames[0].Seq != 1 {
			t.Fatalf("after=%s must replay from seq 1, got %+v", cursor, frames)
		}
	}
}

func TestReportStreamIsOwnerScoped(t *testing.T) {
	svc, engine := newReportService(t)
	owner := newReportHarness(t, svc, engine, "stream-owner", model.RoleOperator)
	other := newReportHarness(t, svc, engine, "stream-other", model.RoleOperator)
	jobID := seededReportJob(t, svc, engine, owner, "policy-job-stream-owner")

	for _, path := range []string{
		"/api/v1/report-jobs/" + jobID,
		"/api/v1/report-jobs/" + jobID + "/events",
	} {
		rec, body := other.do(t, http.MethodGet, path, "", nil)
		if rec.Code != http.StatusNotFound || envelopeCode(t, body) != 44109 {
			t.Fatalf("%s from another workspace must be 404/44109, got %d/%d",
				path, rec.Code, envelopeCode(t, body))
		}
	}
}

// TestReportStreamWaitsForTheNextEvent is the open-ended path: no terminal event, so
// the handler polls and keeps the connection open. The short timeout is the point —
// the test fails if the handler closes early and proves it was still waiting if it
// had to be cancelled.
func TestReportStreamWaitsForTheNextEvent(t *testing.T) {
	svc, engine := newReportService(t)
	fx := newReportHarness(t, svc, engine, "stream-wait", model.RoleOperator)
	jobID := seededReportJob(t, svc, engine, fx, "policy-job-stream-wait")

	rec, finished := fx.reportStream(t, "/api/v1/report-jobs/"+jobID+"/events", nil, 300*time.Millisecond)
	if finished {
		t.Fatalf("a non-terminal stream must stay open, got: %q", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "job.queued") {
		t.Fatalf("the existing event must be delivered before waiting: %q", rec.Body.String())
	}
}

func TestReportEventFrameKeepsPayload(t *testing.T) {
	stream := &service.ReportEventStream{JobID: "job-1", RevisionID: "rev-1"}
	event := model.ReportJobEvent{
		Seq: 7, Type: "job.partial", CreatedAt: time.Unix(1700000000, 0).UTC(),
		PayloadJSON: `{"gaps":[{"section_key":"risk","reason":"budget_exhausted"}]}`,
	}
	frame, err := reportEventFrameJSON(stream, event)
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	frameText := string(frame)
	if !strings.Contains(frameText, `"seq":7`) || !strings.Contains(frameText, `"job_id":"job-1"`) ||
		!strings.Contains(frameText, `"revision_id":"rev-1"`) || !strings.Contains(frameText, `"type":"job.partial"`) {
		t.Fatalf("frame header is wrong: %s", frameText)
	}
	if !strings.Contains(frameText, `"payload":{"gaps"`) {
		t.Fatalf("payload must pass through untouched: %s", frameText)
	}
	if strings.Contains(frameText, "\n") {
		t.Fatalf("a data line must not contain a raw newline: %s", frameText)
	}

	// An unreadable payload degrades to {} rather than poisoning the stream: the seq
	// and type are the contract, and the client can still advance its cursor.
	frame, err = reportEventFrameJSON(stream, model.ReportJobEvent{
		Seq: 8, Type: "job.progress", PayloadJSON: "{not json",
	})
	if err != nil || !strings.Contains(string(frame), `"payload":{}`) {
		t.Fatalf("broken payload must degrade to {}, got %s (%v)", string(frame), err)
	}
}

// reportFrame is the decoded form of one SSE frame.
type reportFrame struct {
	ID    int64
	Seq   int64
	Type  string
	JobID string
}

// reportParseFrames decodes a recorded SSE body. Only `data:` lines are parsed, the
// way the frontend does (adapters.ts `readSse`); a frame whose `id:` disagrees with
// its body `seq` fails the caller's assertions, which is the point of parsing both.
func reportParseFrames(t *testing.T, body string) []reportFrame {
	t.Helper()
	var frames []reportFrame
	var id int64
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "id: "):
			parsed, err := strconv.ParseInt(strings.TrimPrefix(line, "id: "), 10, 64)
			if err != nil {
				t.Fatalf("unreadable id line %q: %v", line, err)
			}
			id = parsed
		case strings.HasPrefix(line, "data: "):
			var payload struct {
				Seq   int64  `json:"seq"`
				Type  string `json:"type"`
				JobID string `json:"job_id"`
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
				t.Fatalf("unreadable data line %q: %v", line, err)
			}
			frames = append(frames, reportFrame{ID: id, Seq: payload.Seq, Type: payload.Type, JobID: payload.JobID})
		}
	}
	return frames
}
