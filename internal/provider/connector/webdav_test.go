package connector

import "testing"

// TestWebDAVProviderReusesClientsWithinInstance pins the pooling contract for
// the directory walk: one provider instance (one sync run) shares its
// PROPFIND and download clients, so keep-alive connections survive across the
// 20k-directory budget.
func TestWebDAVProviderReusesClientsWithinInstance(t *testing.T) {
	p := &WebDAVProvider{}
	if p.propfindClient() != p.propfindClient() {
		t.Fatal("propfind client should be stable within a provider instance")
	}
	if p.downloadClient() != p.downloadClient() {
		t.Fatal("download client should be stable within a provider instance")
	}
	if p.propfindClient() == p.downloadClient() {
		t.Fatal("propfind and download clients should be distinct (different timeouts)")
	}
}
