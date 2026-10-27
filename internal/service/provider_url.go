package service

import (
	"net"
	"net/netip"
	"net/url"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

// ValidateProviderBaseURL performs URL and DNS checks before a provider base
// URL is persisted. Validation happens outside an HTTP request so SSRF is
// rejected even if the value is used later by an asynchronous gateway path.
func ValidateProviderBaseURL(rawURL string, allowPrivateNetwork bool) error {
	if strings.TrimSpace(rawURL) == "" {
		return httperr.BadRequest(40033, "provider base_url is required")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return httperr.BadRequest(40033, "provider base_url must be an absolute URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return httperr.BadRequest(40033, "provider base_url scheme must be http or https")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return httperr.BadRequest(40033, "provider base_url must not contain credentials or a fragment")
	}
	if err := validateProviderHost(parsed.Hostname(), allowPrivateNetwork); err != nil {
		return err
	}
	return nil
}

func validateProviderHost(hostname string, allowPrivateNetwork bool) error {
	if ip, err := netip.ParseAddr(hostname); err == nil {
		if isForbiddenIP(ip, allowPrivateNetwork) {
			return httperr.BadRequest(40034, "provider base_url host is not allowed")
		}
		return nil
	}
	if allowPrivateNetwork {
		return nil
	}
	ips, err := net.LookupIP(hostname)
	if err != nil || len(ips) == 0 {
		return httperr.BadRequest(40034, "provider base_url host cannot be resolved")
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok || isForbiddenIP(addr.Unmap(), allowPrivateNetwork) {
			return httperr.BadRequest(40034, "provider base_url resolves to a disallowed address")
		}
	}
	return nil
}

func isForbiddenIP(ip netip.Addr, allowPrivateNetwork bool) bool {
	if ip.Is4In6() {
		ip = ip.Unmap()
	}
	if ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if ip.Is4() {
		octets := ip.As4()
		if isIPv4Broadcast(octets) {
			return true
		}
	}
	if allowPrivateNetwork {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || isCarrierNAT(ip)
}

func isIPv4Broadcast(octets [4]byte) bool {
	return octets == [4]byte{255, 255, 255, 255}
}

func isCarrierNAT(ip netip.Addr) bool {
	if !ip.Is4() {
		return false
	}
	octets := ip.As4()
	value := uint32(octets[0])<<24 | uint32(octets[1])<<16 |
		uint32(octets[2])<<8 | uint32(octets[3])
	carrierNat := uint32(100)<<24 | uint32(64)<<16
	return value&0xFFC00000 == carrierNat&0xFFC00000
}
