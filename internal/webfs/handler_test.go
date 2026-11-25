package webfs

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func newTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.NoRoute(Handler())
	return r
}

func get(t *testing.T, r http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// TestSPAFallbackAnswers200 pins the status code of the SPA shell. Gin marks the
// NoRoute path as 404 before the handler runs; the fallback renders index.html,
// so it has to claim 200 or a working page is reported as missing (a first-run
// release binary answered "404 page not found" for GET / before this).
func TestSPAFallbackAnswers200(t *testing.T) {
	r := newTestRouter()
	for _, path := range []string{"/", "/setup", "/datasets/123", "/dashboard?tab=1"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: SPA shell must answer 200, got %d", path, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("%s: expected html, got %q", path, ct)
		}
		if !strings.Contains(rec.Body.String(), "RAGFlow-X") {
			t.Fatalf("%s: body is not the bundled index.html: %q", path, rec.Body.String())
		}
	}
}

// TestSPAFallbackKeepsAPINotFoundJSON keeps the API surface honest: a missing
// endpoint is a JSON 404, never the UI shell.
func TestSPAFallbackKeepsAPINotFoundJSON(t *testing.T) {
	r := newTestRouter()
	for _, path := range []string{"/api/v1/does-not-exist", "/metrics"} {
		rec := get(t, r, path)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: expected 404, got %d", path, rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "<html") || !strings.Contains(body, "not found") {
			t.Fatalf("%s: expected JSON 404 body, got %q", path, body)
		}
	}
}

// TestIndexIsNotCached guards the cache split: hashed assets are immutable,
// index.html must never be cached (otherwise a deploy leaves stale asset URLs).
func TestIndexIsNotCached(t *testing.T) {
	r := newTestRouter()
	rec := get(t, r, "/")
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("index.html must not be cached, got Cache-Control %q", cc)
	}
}

// ── gzip negotiation (T28 / E156) ────────────────────────────────────────
//
// The readings these tests defend, in the order they hurt a phone user:
// a 1.7 MB bundle sent identity (1.7 MB of a metered connection), a shared
// cache replaying identity bytes to a client that asked for gzip, and a
// truncated document that renders halfway (Close() never called).

func testFS() fstest.MapFS {
	big := strings.Repeat("<p>row</p>", 512) // ~5 KB
	js := strings.Repeat("console.log(1);", 512)
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><html><body>" + big + "</body></html>")},
		"assets/app.js": &fstest.MapFile{Data: []byte(js)},
		"assets/logo.png": &fstest.MapFile{Data: bytes.Repeat([]byte{0x89, 'P', 'N', 'G'}, 2048)},
		"assets/tiny.css": &fstest.MapFile{Data: []byte("a{color:red}")},
	}
}

func routerFor(fsys fs.FS) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.NoRoute(handlerFor(fsys))
	return r
}

func request(t *testing.T, r http.Handler, path string, acceptEncoding string, method string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func gunzip(t *testing.T, body []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("response claims gzip but is not: %v", err)
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gzip body truncated: %v", err)
	}
	return string(out)
}

func TestAssetsAreGzippedWhenTheClientAcceptsIt(t *testing.T) {
	r := routerFor(testFS())
	rec := request(t, r, "/assets/app.js", "gzip, deflate, br", http.MethodGet)
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("a 7 KB script must be gzipped, got Content-Encoding %q", enc)
	}
	if cl := rec.Header().Get("Content-Length"); cl != "" {
		t.Fatalf("compressed body must not carry the identity Content-Length, got %q", cl)
	}
	if got := gunzip(t, rec.Body.Bytes()); !strings.HasPrefix(got, "console.log(1);") {
		t.Fatalf("gzip body is not the asset: %q", got[:40])
	}
	if rec.Body.Len() >= len(strings.Repeat("console.log(1);", 512)) {
		t.Fatalf("compression made the body bigger: %d bytes", rec.Body.Len())
	}
}

func TestSmallBodiesAreNotCompressed(t *testing.T) {
	r := routerFor(testFS())
	rec := request(t, r, "/assets/tiny.css", "gzip", http.MethodGet)
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("a 12-byte stylesheet must not be re-encoded, got %q", enc)
	}
	if body := rec.Body.String(); body != "a{color:red}" {
		t.Fatalf("small body must be served verbatim, got %q", body)
	}
}

func TestBinaryAssetsAreNotCompressed(t *testing.T) {
	r := routerFor(testFS())
	rec := request(t, r, "/assets/logo.png", "gzip", http.MethodGet)
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("a PNG is already compressed; got Content-Encoding %q", enc)
	}
	if rec.Body.Len() != 8192 {
		t.Fatalf("PNG must be served byte-for-byte, got %d bytes", rec.Body.Len())
	}
}

func TestVaryIsAlwaysPresent(t *testing.T) {
	r := routerFor(testFS())
	for _, ae := range []string{"", "gzip", "br"} {
		rec := request(t, r, "/assets/app.js", ae, http.MethodGet)
		if vary := rec.Header().Get("Vary"); !strings.Contains(vary, "Accept-Encoding") {
			t.Fatalf("Accept-Encoding %q: Vary must name it, got %q", ae, vary)
		}
	}
}

func TestGzipRefusedWhenTheClientSaysQZero(t *testing.T) {
	r := routerFor(testFS())
	rec := request(t, r, "/assets/app.js", "gzip;q=0, deflate", http.MethodGet)
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("q=0 is a refusal, not a preference; got Content-Encoding %q", enc)
	}
}

func TestSPAFallbackShellIsCompressed(t *testing.T) {
	r := routerFor(testFS())
	rec := request(t, r, "/datasets/123", "gzip", http.MethodGet)
	if rec.Code != http.StatusOK {
		t.Fatalf("deep link must answer 200, got %d", rec.Code)
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("the shell is the first thing a phone loads; got Content-Encoding %q", enc)
	}
	if got := gunzip(t, rec.Body.Bytes()); !strings.Contains(got, "<!doctype html>") {
		t.Fatalf("fallback body is not the shell: %q", got[:40])
	}
}

// A HEAD answer describes the same resource as GET. Compressing it would emit a
// gzip header for a body that never arrives, so it is deliberately not
// negotiated — and the identity length is still the right length.
func TestHeadKeepsIdentityLength(t *testing.T) {
	r := routerFor(testFS())
	rec := request(t, r, "/assets/app.js", "gzip", http.MethodHead)
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("HEAD must not claim a body encoding, got %q", enc)
	}
	if cl := rec.Header().Get("Content-Length"); cl == "" {
		t.Fatalf("HEAD must keep the identity Content-Length")
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD must not write a body, got %d bytes", rec.Body.Len())
	}
}
