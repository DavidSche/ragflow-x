package webfs

import (
	"compress/gzip"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// assets returns the embedded frontend filesystem rooted at its index.html.
// Each build-tag variant (dist_placeholder.go / dist_embedded.go) supplies it.

// Handler is the Gin no-route handler serving the embedded frontend with SPA
// fallback and gzip negotiation. Unknown non-/api paths fall back to index.html
// so deep links like /datasets/123 survive a hard refresh; assets are
// content-hashed by Vite and marked immutable, while index.html is never cached.
func Handler() gin.HandlerFunc { return handlerFor(assets()) }

// handlerFor is the same handler over an arbitrary filesystem. The seam exists
// because the compression rules (which type, which size, which request) are the
// part worth testing, and a test that can only see the embedded build can only
// assert "some bytes came back".
func handlerFor(fsys fs.FS) gin.HandlerFunc {
	fileServer := http.FileServer(http.FS(fsys))
	return func(c *gin.Context) {
		// API and operational endpoints must never fall back to index.html:
		// a missing endpoint is a JSON 404, not the UI shell.
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api/") || p == "/metrics" {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		w := newGzipWriter(c.Writer, c.Request)
		defer w.Close()
		// Vary belongs on every asset/shell answer, compressed or not: a shared
		// cache that stores the identity form and replays it to a client that
		// asked for gzip is a cache bug, not a client bug.
		w.Header().Add("Vary", "Accept-Encoding")

		name := strings.TrimPrefix(p, "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(fsys, path.Clean(name)); err == nil {
			if name != "index.html" {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fileServer.ServeHTTP(w, c.Request)
			return
		}
		data, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "frontend assets unavailable"})
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Length", itoa(len(data)))
		// Gin pre-sets 404 on the NoRoute path before this handler runs, so the
		// SPA fallback must claim 200 explicitly — otherwise every deep link
		// (e.g. /setup or /datasets/123) renders the app but answers 404, and
		// curl -f / uptime checks / proxies treat a working page as an error.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

// minCompressBytes is where gzip stops costing more than it saves: the header
// alone is ~18 bytes and the CPU is paid on every request. The shell is ~1.2 KB
// and the JS bundles are 1.4–1.7 MB, so everything that matters is far above it.
const minCompressBytes = 1024

// compressibleTypes is a closed set, not a prefix test on "text/": the bytes
// served here are the ones Vite emitted, and a media file is already compressed.
// Re-encoding it makes the payload larger and bills CPU per request.
var compressibleTypes = map[string]bool{
	"text/html":                 true,
	"text/css":                  true,
	"text/plain":                true,
	"text/javascript":           true,
	"application/javascript":    true,
	"application/json":          true,
	"application/manifest+json": true,
	"image/svg+xml":             true,
}

func compressibleType(contentType string) bool {
	if contentType == "" {
		return false
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return compressibleTypes[mt]
}

// acceptsGzip answers "may this response be gzipped?". `q` is a relative
// preference with three decimals of precision, so `gzip;q=0`, `gzip;q=0.0`,
// `gzip;q=0.00` and `gzip;q=0.000` are the same refusal — a substring test for
// "q=0" gets the first one and serves the other three a 1.7 MB bundle
// (doc/304 §4 E176). Everything else is a positive weight.
func acceptsGzip(r *http.Request) bool {
	header := r.Header.Get("Accept-Encoding")
	gzipQ, starQ := -1.0, -1.0
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		token := strings.ToLower(strings.TrimSpace(fields[0]))
		if token != "gzip" && token != "*" {
			continue
		}
		q := 1.0
		for _, param := range fields[1:] {
			kv := strings.SplitN(strings.TrimSpace(param), "=", 2)
			if len(kv) != 2 || !strings.EqualFold(strings.TrimSpace(kv[0]), "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(kv[1]), 64)
			if err != nil {
				// An unparseable weight is not a refusal: RFC 9110 says a
				// malformed `q` is treated as the default, and inventing a
				// refusal here would turn a typo into an uncompressed bundle.
				q = 1.0
				continue
			}
			q = parsed
		}
		if token == "gzip" {
			gzipQ = q
		} else {
			starQ = q
		}
	}
	if gzipQ >= 0 {
		return gzipQ > 0
	}
	// `*` covers codings that were not named explicitly, gzip included.
	return starQ > 0
}

// gzipWriter compresses a response at the moment its status and headers are
// known, so Content-Length can be dropped before anything is written. Buffering
// the body to decide later would mean holding a 1.7 MB bundle per concurrent
// request; deciding at WriteHeader costs nothing.
type gzipWriter struct {
	gin.ResponseWriter
	req         *http.Request
	gz          *gzip.Writer
	wroteHeader bool
	closed      bool
}

func newGzipWriter(w gin.ResponseWriter, req *http.Request) *gzipWriter {
	return &gzipWriter{ResponseWriter: w, req: req}
}

func (w *gzipWriter) shouldCompress(code int) bool {
	if code != http.StatusOK || w.req.Method == http.MethodHead {
		return false
	}
	if w.Header().Get("Content-Encoding") != "" {
		return false
	}
	if !compressibleType(w.Header().Get("Content-Type")) {
		return false
	}
	if n, ok := contentLength(w.Header().Get("Content-Length")); ok && n < minCompressBytes {
		return false
	}
	return acceptsGzip(w.req)
}

func (w *gzipWriter) WriteHeader(code int) {
	if w.wroteHeader {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.wroteHeader = true
	if w.shouldCompress(code) {
		w.Header().Set("Content-Encoding", "gzip")
		// The identity length is no longer the length of this body; leaving it
		// in place truncates the response at the proxy.
		w.Header().Del("Content-Length")
		w.ResponseWriter.WriteHeader(code)
		w.gz = gzip.NewWriter(w.ResponseWriter)
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *gzipWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

func (w *gzipWriter) Flush() {
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	w.ResponseWriter.Flush()
}

// Close finishes the gzip stream. Without it the last block stays in the
// writer's buffer and the browser sees a truncated document — the failure mode
// is an HTML page that renders halfway, which reads like a frontend bug.
func (w *gzipWriter) Close() {
	if w.closed {
		return
	}
	w.closed = true
	if w.gz != nil {
		_ = w.gz.Close()
		w.gz = nil
	}
}

func contentLength(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	var n int64
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int64(s[i]-'0')
	}
	return n, true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
