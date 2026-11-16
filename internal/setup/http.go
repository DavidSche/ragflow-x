package setup

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/ratelimit"
	"github.com/ragflow-x/ragflow-x/internal/webfs"
)

type envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, envelope{Code: 0, Data: data})
}

func fail(c *gin.Context, status, code int, message string) {
	c.JSON(status, envelope{Code: code, Message: message})
}

// bootstrapRouter builds the minimal, unauthenticated first-run router. It only
// exposes the setup endpoints; the whole engine is swapped out on Apply.
//
// The embedded frontend is served here too: the SPA renders the setup wizard
// from /api/v1/system/setup/*, so a freshly unpacked release binary that cannot
// reach a database must still answer GET / with the UI. Without this fallback
// the first run looked broken — the browser got a bare "404 page not found"
// even though web/dist was embedded in the binary.
func (m *Manager) bootstrapRouter() http.Handler {
	if m.cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(obs.Get().Middleware(), middleware.SecurityHeaders(m.cfg.Security), middleware.CORS(m.cfg.Security), gin.Recovery())
	if m.cfg.Observability.MetricsEnabled {
		r.GET(m.cfg.Observability.MetricsPath, middleware.MetricsAuth(m.cfg.Observability.MetricsToken), obs.Get().MetricsHandler())
	}

	grp := r.Group("/api/v1/system/setup")
	grp.GET("/status", m.httpStatus)
	grp.GET("/public-key", m.httpPublicKey)
	// The bundled SPA is the login page even in wizard mode, and it reads these
	// three endpoints before it can decide what to render. Leaving them out made
	// a first run log 404s (/branding/public, /auth/oidc/status, /auth/logout)
	// and a "logout request failed" warning on a screen that had no error at all.
	// They are read-only or idempotent, so the bootstrap router can answer them
	// without a database: there is no tenant, no IdP and no session yet.
	r.GET("/api/v1/branding/public", m.httpBrandingPublic)
	r.GET("/api/v1/auth/oidc/status", func(c *gin.Context) {
		ok(c, gin.H{"enabled": false, "login_path": "/api/v1/auth/oidc/start"})
	})
	r.POST("/api/v1/auth/logout", func(c *gin.Context) { ok(c, nil) })
	// Liveness/readiness in wizard mode. The image's HEALTHCHECK curls
	// /api/v1/readyz; those endpoints used to exist only on the full router, so a
	// container that had booted into the setup wizard reported `unhealthy` for
	// the whole first run — a working wizard looked like a crash-looping pod.
	// Readiness here means "serving, and the next step is the operator's": the
	// wizard is a supported state, not a degraded one.
	r.GET("/api/v1/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/api/v1/readyz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ready", "setup_required": true})
	})
	limiter := ratelimit.NewMemory()
	limit := int64(m.cfg.Setup.RateLimitPerMin)
	if limit <= 0 {
		limit = 10
	}
	grp.POST("/preflight", middleware.RateLimit(limiter, middleware.ClientIPKey, limit, time.Minute), m.httpPreflight)
	grp.POST("/apply", middleware.RateLimit(limiter, middleware.ClientIPKey, limit, time.Minute), m.httpApply)
	// The SPA's admin-only step posts here, and only the full router serves it.
	// Answering with an explanation instead of a bare 404 matters: this exact
	// request is what a launcher trial (`start.sh --sqlite`, database reachable,
	// no engine yet) used to log as "Failed to load resource ... 404".
	grp.POST("/admin", middleware.RateLimit(limiter, middleware.ClientIPKey, limit, time.Minute), m.httpSetupAdminUnavailable)
	// Registered last so the setup endpoints above win; /api/* still 404s as JSON.
	r.NoRoute(webfs.Handler())
	return r
}

func (m *Manager) httpStatus(c *gin.Context) {
	st, err := m.Status(c.Request.Context())
	if err != nil {
		fail(c, http.StatusInternalServerError, 500, err.Error())
		return
	}
	ok(c, st)
}

func (m *Manager) httpPublicKey(c *gin.Context) {
	ok(c, gin.H{"public_key": m.PublicKeyPEM()})
}

// httpBrandingPublic serves the platform name while no database exists yet. The
// configured brand lives in system settings, which needs a database; the app name
// from the config is what a first-run operator should see. The real branding
// replaces it the moment the engine swaps in.
func (m *Manager) httpBrandingPublic(c *gin.Context) {
	name := strings.TrimSpace(m.cfg.App.Name)
	if name == "" {
		name = "RAGFlow-X"
	}
	ok(c, gin.H{"name": name, "logo": ""})
}

func (m *Manager) httpPreflight(c *gin.Context) {
	var req ApplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, 40000, "invalid payload")
		return
	}
	st, err := m.Preflight(c.Request.Context(), req)
	if err != nil {
		fail(c, http.StatusBadRequest, 40001, err.Error())
		return
	}
	ok(c, st)
}

// httpSetupAdminUnavailable explains why the admin-only step cannot run here.
// It is not a degraded implementation: creating the first admin needs the service
// graph (tenant + user + role), which only exists once the engine is built.
func (m *Manager) httpSetupAdminUnavailable(c *gin.Context) {
	fail(c, http.StatusConflict, 40009,
		"the platform is not initialized yet: submit the full setup form "+
			"(POST /api/v1/system/setup/apply) with the database and RAGFlow engine "+
			"connection, the server creates the administrator there")
}

func (m *Manager) httpApply(c *gin.Context) {
	var req ApplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		fail(c, http.StatusBadRequest, 40000, "invalid payload")
		return
	}
	if err := m.Apply(c.Request.Context(), req); err != nil {
		fail(c, http.StatusBadRequest, 40004, err.Error())
		return
	}
	ok(c, gin.H{"configured": true})
}
