package service

import (
	"encoding/json"
	"net/netip"
	"strings"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
)

func validateAPIKeyIPs(values []string) error {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return httperr.BadRequest(40043, "api key allowed ip cannot be empty")
		}
		if _, err := netip.ParseAddr(value); err != nil {
			if _, prefixErr := netip.ParsePrefix(value); prefixErr != nil {
				return httperr.BadRequest(40043, "api key allowed ip must be an IP or CIDR")
			}
		}
	}
	return nil
}

func parseAPIKeyIPs(raw string) []string {
	var values []string
	_ = json.Unmarshal([]byte(raw), &values)
	return values
}

// AuthorizeAPIKeyIP applies the credential's source-address constraint after
// key authentication. An omitted list preserves unrestricted legacy keys; an
// explicit empty list is fail-closed. Malformed persisted entries also fail
// closed so a database-side write cannot weaken the boundary.
func (s *Service) AuthorizeAPIKeyIP(key *model.APIKey, source string) error {
	if key == nil || key.AllowedIPsJSON == "" {
		return nil
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(source))
	if err != nil {
		return httperr.Forbidden("api key is not authorized from this ip")
	}
	addr = addr.Unmap()
	for _, value := range parseAPIKeyIPs(key.AllowedIPsJSON) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		allowed, parseErr := netip.ParseAddr(value)
		if parseErr == nil {
			if allowed.Unmap() == addr {
				return nil
			}
			continue
		}
		prefix, parseErr := netip.ParsePrefix(value)
		if parseErr != nil || !prefix.Contains(addr) {
			continue
		}
		return nil
	}
	return httperr.Forbidden("api key is not authorized from this ip")
}
