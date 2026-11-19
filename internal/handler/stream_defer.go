package handler

import (
	"github.com/gin-gonic/gin"
)

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
	d.w.Header().Set("Content-Type", "text/event-stream")
	d.w.WriteHeader(200)
}

func (d *deferredStreamWriter) Write(p []byte) (int, error) {
	d.commit()
	return d.w.Write(p)
}

// Committed reports whether the response is already pinned to 200. The handler
// must not try to set another status after that; it appends a terminal error
// frame instead.
func (d *deferredStreamWriter) Committed() bool { return d.wrote }
