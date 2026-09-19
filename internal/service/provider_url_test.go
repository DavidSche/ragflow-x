package service

import (
	"errors"
	"testing"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// ScenarioID: SC-NETURL-001
func TestP0_NETURL_001_ValidateProviderBaseURLRejectsUnsafeNetworkShapes(t *testing.T) {
	tests := []struct {
		name         string
		rawURL       string
		allowPrivate bool
		wantCode     int
	}{
		{name: "empty url", rawURL: "", wantCode: 40033},
		{name: "relative url", rawURL: "example.com", wantCode: 40033},
		{name: "unsupported scheme", rawURL: "ftp://example.com", wantCode: 40033},
		{name: "credentials", rawURL: "http://user:pass@example.com", wantCode: 40033},
		{name: "fragment", rawURL: "http://example.com#fragment", wantCode: 40033},
		{name: "ipv4 loopback", rawURL: "http://127.0.0.1:8000", wantCode: 40034},
		{name: "ipv4 private", rawURL: "http://10.0.0.1:8000", wantCode: 40034},
		{name: "ipv4 link local", rawURL: "http://169.254.169.254/latest-meta-data", wantCode: 40034},
		{name: "ipv4 unspecified", rawURL: "http://0.0.0.0:8000", wantCode: 40034},
		{name: "ipv4 cgnat start", rawURL: "http://100.64.0.1:8000", wantCode: 40034},
		{name: "ipv4 cgnat end", rawURL: "http://100.127.255.254:8000", wantCode: 40034},
		{name: "ipv4 broadcast", rawURL: "http://255.255.255.255:8000", wantCode: 40034},
		{name: "ipv4 multicast", rawURL: "http://224.0.0.1:8000", wantCode: 40034},
		{name: "ipv6 loopback", rawURL: "http://[::1]:8000", wantCode: 40034},
		{name: "ipv6 link local", rawURL: "http://[fe80::1]:8000", wantCode: 40034},
		{name: "ipv6 unique local", rawURL: "http://[fc00::1]:8000", wantCode: 40034},
		{name: "ipv6 multicast", rawURL: "http://[ff02::1]:8000", wantCode: 40034},
		{name: "ipv4-mapped ipv6 loopback", rawURL: "http://[::ffff:127.0.0.1]:8000", wantCode: 40034},
		{name: "public ipv4 is allowed", rawURL: "http://8.8.8.8:8000"},
		{
			name:         "private network opt-in allows loopback",
			rawURL:       "http://127.0.0.1:8000",
			allowPrivate: true,
		},
		{
			name:         "private network opt-in allows cgnat",
			rawURL:       "http://100.64.0.1:8000",
			allowPrivate: true,
		},
		{
			name:         "private network opt-in still rejects broadcast",
			rawURL:       "http://255.255.255.255:8000",
			allowPrivate: true,
			wantCode:     40034,
		},
		{
			name:         "private network opt-in skips hostname resolution",
			rawURL:       "http://invalid.invalid:8000",
			allowPrivate: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProviderBaseURL(tt.rawURL, tt.allowPrivate)
			if tt.wantCode == 0 {
				if err != nil {
					t.Fatalf("expected acceptance, got %v", err)
				}
				return
			}

			var apiErr *httperr.Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected httperr.Error, got %v", err)
			}
			if apiErr.Status != 400 || apiErr.Code != tt.wantCode {
				t.Fatalf("expected status 400 code %d, got status %d code %d", tt.wantCode, apiErr.Status, apiErr.Code)
			}
		})
	}
}
