package connector

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// newConnectorHTTPClient builds a pool-backed HTTP client meant to be shared
// across one provider instance's requests. Reusing a single Transport across
// pages and objects within a run keeps keep-alive connections alive (which
// matters for large listings and many small downloads) and keeps connector
// traffic off the process-global http.DefaultTransport.
func newConnectorHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
}
