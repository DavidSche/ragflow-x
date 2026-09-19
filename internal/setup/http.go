package setup

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ragflow-x/ragflow-x/internal/middleware"
	"github.com/ragflow-x/ragflow-x/internal/obs"
	"github.com/ragflow-x/ragflow-x/internal/pkg/ratelimit"
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
	limiter := ratelimit.NewMemory()
	limit := int64(m.cfg.Setup.RateLimitPerMin)
	if limit <= 0 {
		limit = 10
	}
	grp.POST("/preflight", middleware.RateLimit(limiter, middleware.ClientIPKey, limit, time.Minute), m.httpPreflight)
	grp.POST("/apply", middleware.RateLimit(limiter, middleware.ClientIPKey, limit, time.Minute), m.httpApply)
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
