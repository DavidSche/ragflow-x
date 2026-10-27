package middleware

import (
	"crypto/subtle"
	"net"
	"strings"

	"github.com/gin-gonic/gin"
)

// MetricsAuth protects the Prometheus endpoint. An empty token disables the
// endpoint rather than silently exposing process and traffic metrics.
// MetricsAuth protects the Prometheus endpoint. An empty token disables the
// endpoint rather than silently exposing process and traffic metrics.
// allowCIDRs permits exact IPs or CIDR networks (for example 10.0.0.5/32 and
// 10.0.0.0/16) to scrape without a bearer token. An empty allowlist keeps
// token-only access. Authentication uses the socket peer address, never a
// client-controlled forwarding header.
func MetricsAuth(token string, allowCIDRs ...string) gin.HandlerFunc {
	return metricsAuthHandler(token, allowCIDRs)
}

func metricsAuthHandler(token string, allowCIDRs []string) gin.HandlerFunc {
	allowed := make([]*net.IPNet, 0, len(allowCIDRs))
	for _, value := range allowCIDRs {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			panic("metrics_allow_cidrs: invalid entry " + value)
		}
		allowed = append(allowed, network)
	}

	return func(c *gin.Context) {
		if token == "" {
			c.AbortWithStatus(404)
			return
		}
		if remote := net.ParseIP(c.RemoteIP()); remote != nil {
			for _, network := range allowed {
				if network.Contains(remote) {
					c.Next()
					return
				}
			}
		}
		header := c.GetHeader("Authorization")
		const prefix = "Bearer "
		if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
			c.AbortWithStatus(401)
			return
		}
		presented := header[len(prefix):]
		if subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
			c.AbortWithStatus(401)
			return
		}
		c.Next()
	}
}
