package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// chatStreamWriteBudget is how far forward each SSE frame pushes the
// connection's write deadline.
//
// `http.Server.WriteTimeout` is an absolute deadline measured from the start of
// the request, not an idle timeout, so a `write_timeout` tuned for interactive
// calls (60 s by default) severs a chat answer in the middle of the sentence. Doc
// 316 §十 records the log that showed it: six `/api/v1/chat/completions` streams
// cut at exactly the provider budget, with the server's 60 s the next ceiling
// waiting behind it. Extending per frame keeps a live answer alive and still
// bounds a client that stopped reading — the same treatment report streams got in
// `reportStreamExtendDeadline`, applied where the frames are written so both chat
// entry points inherit it.
const chatStreamWriteBudget = 5 * time.Minute

// deferredStreamWriter postpones the 200 + `text/event-stream` commitment until
// the first byte actually goes out.
//
// Both chat stream entry points used to write the header *before* calling the
// service, which made every pre-stream refusal indistinguishable from a started
// turn: the client saw a 200 with an error frame in it. That is the same class of
// bug as reporting a failure as content — the status code is the contract, and a
// rejection that cannot be retried with a different body looks like a dead
// session. A14 made it reachable from outside: `report_context` is validated
// inside the service, before the first byte, so streaming it through a committed
// 200 hid a plain `400/44105`.
//
// When the service fails *after* writing (a provider that dies mid-answer), this
// changes nothing: the header is already out and the terminal error frame stays
// the only way to say so (doc/227 C3.3).
type deferredStreamWriter struct {
	w     gin.ResponseWriter
	wrote bool
}

func newDeferredStreamWriter(w gin.ResponseWriter) *deferredStreamWriter {
	return &deferredStreamWriter{w: w}
}

// commit writes the SSE preamble once. It is idempotent so a service that
// flushes twice does not append a second header block.
func (d *deferredStreamWriter) commit() {
	if d.wrote {
		return
	}
	d.wrote = true
	d.extendWriteDeadline()
	d.w.Header().Set("Content-Type", "text/event-stream")
	d.w.WriteHeader(200)
}

func (d *deferredStreamWriter) Write(p []byte) (int, error) {
	d.commit()
	d.extendWriteDeadline()
	return d.w.Write(p)
}

// extendWriteDeadline pushes the connection's absolute write deadline forward by
// one budget. Best-effort by design: httptest.ResponseRecorder has no deadlines
// and gin may wrap a writer that cannot set them, and neither is a reason to drop
// a frame that is otherwise ready to go.
func (d *deferredStreamWriter) extendWriteDeadline() {
	_ = http.NewResponseController(d.w).SetWriteDeadline(time.Now().Add(chatStreamWriteBudget))
}

// Committed reports whether the response is already pinned to 200. The handler
// must not try to set another status after that; it appends a terminal error
// frame instead.
func (d *deferredStreamWriter) Committed() bool { return d.wrote }
