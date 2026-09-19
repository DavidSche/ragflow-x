package service

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/ragflow-x/ragflow-x/internal/model"
	"github.com/ragflow-x/ragflow-x/internal/pkg/httperr"
	"github.com/ragflow-x/ragflow-x/internal/pkg/id"
)

const enterpriseConnectionProbeTimeout = 5 * time.Second

// EnterpriseConnectionHealthView returns the current immutable configuration
// and the latest persisted probe. CredentialRef is intentionally excluded by
// callers that need a secret-free API projection; this internal view still uses
// the repository model because the HTTP handler serializes a safe projection.
type EnterpriseConnectionHealthView struct {
	Connection model.EnterpriseConnection             `json:"connection"`
	Version    model.EnterpriseConnectionVersion      `json:"version"`
	TenantName string                                 `json:"tenant_name"`
	Latest     *model.EnterpriseConnectionHealthCheck `json:"latest_health_check,omitempty"`
}

// EnterpriseModelAvailability is computed per allowed ModelRef. Authorization
// is derived from the exact Binding Version, not from dynamic model discovery.
type EnterpriseModelAvailability struct {
	ModelRef     string `json:"model_ref"`
	Availability string `json:"availability"`
	Reason       string `json:"reason"`
}

// EnterpriseBindingAvailability couples the governance view with its derived
// availability so callers cannot confuse lifecycle authorization with runtime
// reachability.
type EnterpriseBindingAvailability struct {
	EnterpriseBindingView
	Availability      string                        `json:"availability"`
	Reason            string                        `json:"reason"`
	ModelAvailability []EnterpriseModelAvailability `json:"model_availability"`
}

// EnterpriseRouteAvailability is calculated for a Model Route that is exactly
// pinned to this connection. It never follows Current pointers.
type EnterpriseRouteAvailability struct {
	Route        model.ModelRoute `json:"route"`
	Availability string           `json:"availability"`
	Reason       string           `json:"reason"`
}

// EnterpriseAvailabilityView separates the three layers required by doc/77:
// connection reachability, binding authorization, and exact route usability.
type EnterpriseAvailabilityView struct {
	Connection model.EnterpriseConnection        `json:"connection"`
	Version    model.EnterpriseConnectionVersion `json:"version"`
	TenantName string                            `json:"tenant_name"`
	Bindings   []EnterpriseBindingAvailability   `json:"bindings"`
	Routes     []EnterpriseRouteAvailability     `json:"routes"`
}

func probeURL(baseURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", httperr.BadRequest(40067, "enterprise connection base_url is invalid")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/models"
	parsed.RawQuery = ""
	return parsed.String(), nil
}

func enterpriseHealthFromHTTPStatus(status int) (string, string) {
	switch {
	case status >= 200 && status <= 399:
		return model.RuntimeHealthHealthy, "provider endpoint is reachable"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return model.RuntimeHealthHealthy, "provider endpoint is reachable and requires authorization"
	case status == http.StatusTooManyRequests:
		return model.RuntimeHealthDegraded, "provider endpoint is rate limited"
	default:
		return model.RuntimeHealthUnavailable, "provider endpoint returned an unexpected status"
	}
}

// enterpriseProbeDialer resolves once, rejects disallowed addresses, and dials
// the same address used for validation. This prevents DNS rebinding during the
// outbound probe. Private deployments may explicitly allow internal addresses.
func (s *Service) enterpriseProbeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("probe address is invalid")
	}
	resolver := &net.Resolver{}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("probe host cannot be resolved")
	}
	addresses := make([]netip.Addr, 0, len(ips))
	for _, item := range ips {
		addr, ok := netip.AddrFromSlice(item.IP)
		if !ok {
			return nil, fmt.Errorf("probe host returned an invalid address")
		}
		addr = addr.Unmap()
		if isForbiddenIP(addr, s.allowPrivateProviderBaseURL) {
			return nil, httperr.BadRequest(40034, "provider base_url host is not allowed")
		}
		addresses = append(addresses, addr)
	}
	dialer := &net.Dialer{Timeout: 3 * time.Second}
	return dialer.DialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
}

func (s *Service) enterpriseProbeClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           s.enterpriseProbeDialContext,
		DisableKeepAlives:     true,
		MaxIdleConns:          0,
		IdleConnTimeout:       time.Second,
		TLSHandshakeTimeout:   3 * time.Second,
		ResponseHeaderTimeout: 4 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{
		Timeout:       enterpriseConnectionProbeTimeout,
		Transport:     transport,
		CheckRedirect: s.providerRedirectPolicy,
	}
}

// TestEnterpriseConnection performs a read-only endpoint probe. The request
// carries no Secret and never records a provider response body.
func (s *Service) TestEnterpriseConnection(ctx context.Context, scope TenantScope, connectionID, actorID string) (*EnterpriseConnectionHealthView, error) {
	view, err := s.GetEnterpriseConnectionForScope(ctx, scope, connectionID)
	if err != nil {
		return nil, err
	}
	if view.Connection.LifecycleStatus == model.EnterpriseLifecycleDisabled ||
		view.Connection.LifecycleStatus == model.EnterpriseLifecycleRetired {
		return nil, httperr.BadRequest(40067, "disabled or retired connection cannot be probed")
	}
	if err := ValidateProviderBaseURL(view.Version.BaseURL, s.allowPrivateProviderBaseURL); err != nil {
		return nil, err
	}
	target, err := probeURL(view.Version.BaseURL)
	if err != nil {
		return nil, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, enterpriseConnectionProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
	if err != nil {
		return nil, httperr.BadRequest(40067, "enterprise connection probe URL is invalid")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "RAGFlow-X-HealthProbe/1.0")

	started := time.Now().UTC()
	resp, err := s.enterpriseProbeClient().Do(req)
	latency := time.Since(started).Milliseconds()
	now := time.Now().UTC()
	check := &model.EnterpriseConnectionHealthCheck{
		ID: id.New(), ConnectionID: view.Connection.ID,
		ConnectionVersion: view.Version.Version, LatencyMS: latency,
		CreatedBy: actorID, CreatedAt: now,
	}
	if err != nil {
		check.Health = model.RuntimeHealthUnavailable
		check.Message = "provider endpoint is unreachable"
	} else {
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1))
		check.HTTPStatus = resp.StatusCode
		check.Health, check.Message = enterpriseHealthFromHTTPStatus(resp.StatusCode)
	}
	if err := s.Store.RecordEnterpriseConnectionHealthCheck(ctx, check, check.Health, now); err != nil {
		return nil, err
	}
	view.Connection.RuntimeHealth = check.Health
	view.Connection.LastHealthCheckAt = &now
	return &EnterpriseConnectionHealthView{
		Connection: view.Connection,
		Version:    view.Version,
		TenantName: view.TenantName,
		Latest:     check,
	}, nil
}

// GetEnterpriseConnectionHealth returns the latest persisted probe without
// triggering an outbound request.
func (s *Service) GetEnterpriseConnectionHealth(ctx context.Context, scope TenantScope, connectionID string) (*EnterpriseConnectionHealthView, error) {
	view, err := s.GetEnterpriseConnectionForScope(ctx, scope, connectionID)
	if err != nil {
		return nil, err
	}
	latest, err := s.Store.GetLatestEnterpriseConnectionHealthCheck(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	return &EnterpriseConnectionHealthView{
		Connection: view.Connection,
		Version:    view.Version,
		TenantName: view.TenantName,
		Latest:     latest,
	}, nil
}

func connectionAvailability(connection *model.EnterpriseConnection) (string, string) {
	switch connection.LifecycleStatus {
	case model.EnterpriseLifecycleActive:
		switch connection.RuntimeHealth {
		case model.RuntimeHealthHealthy:
			return "AVAILABLE", "connection is active and reachable"
		case model.RuntimeHealthDegraded:
			return "DEGRADED", "connection is active but degraded"
		case model.RuntimeHealthUnavailable:
			return "UNAVAILABLE", "connection endpoint is unavailable"
		default:
			return "UNKNOWN", "connection has not been probed"
		}
	case model.EnterpriseLifecycleDeprecated:
		return "DEGRADED", "connection is deprecated"
	case model.EnterpriseLifecycleDisabled, model.EnterpriseLifecycleRetired:
		return "UNAVAILABLE", "connection is not runnable"
	default:
		return "UNKNOWN", "connection lifecycle is unknown"
	}
}

func (s *Service) GetEnterpriseConnectionAvailability(ctx context.Context, scope TenantScope, connectionID string) (*EnterpriseAvailabilityView, error) {
	connectionView, err := s.GetEnterpriseConnectionForScope(ctx, scope, connectionID)
	if err != nil {
		return nil, err
	}
	connectionAvailability, connectionReason := connectionAvailability(&connectionView.Connection)

	var tenantIDs []string
	if !scope.ScopeAll() {
		tenantIDs = scope.AllowedTenantIDs
	}
	bindings, err := s.Store.ListEnterpriseBindingsByConnection(ctx, connectionID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		ids = append(ids, binding.BindingID)
	}
	bindingVersions, err := s.Store.ListEnterpriseBindingVersions(ctx, ids)
	if err != nil {
		return nil, err
	}
	currentBindingByID := make(map[string]*model.EnterpriseConnectionBinding, len(bindings))
	for index := range bindings {
		currentBindingByID[bindings[index].BindingID] = &bindings[index]
	}
	currentVersionByID := make(map[string]model.EnterpriseConnectionBindingVersion, len(bindings))
	for _, version := range bindingVersions {
		if binding := currentBindingByID[version.BindingID]; binding != nil && binding.CurrentBindingVersion == version.Version {
			currentVersionByID[version.BindingID] = version
		}
	}
	tenantNames := s.tenantNameMap(ctx)

	bindingAvailabilityByID := make(map[string]string, len(bindings))
	bindingViews := make([]EnterpriseBindingAvailability, 0, len(bindings))
	for _, binding := range bindings {
		version, ok := currentVersionByID[binding.BindingID]
		if !ok {
			continue
		}
		availability, reason := connectionAvailability, connectionReason
		if binding.LifecycleStatus != model.EnterpriseLifecycleActive {
			availability, reason = "UNAVAILABLE", "binding is revoked or disabled"
		} else if availability == "AVAILABLE" {
			reason = "binding is active and authorized"
		}
		bindingAvailabilityByID[binding.BindingID] = availability
		models := make([]EnterpriseModelAvailability, 0, len(decodeStringList(version.AllowedModelRefs)))
		for _, modelRef := range decodeStringList(version.AllowedModelRefs) {
			modelAvailability, modelReason := availability, reason
			if availability == "AVAILABLE" {
				modelReason = "model is authorized by the exact binding version"
			}
			models = append(models, EnterpriseModelAvailability{
				ModelRef: modelRef, Availability: modelAvailability, Reason: modelReason,
			})
		}
		bindingViews = append(bindingViews, EnterpriseBindingAvailability{
			EnterpriseBindingView: EnterpriseBindingView{
				Binding: binding, Version: version, TenantName: tenantNames[binding.TenantID],
			},
			Availability: availability, Reason: reason, ModelAvailability: models,
		})
	}

	routes, err := s.Store.ListModelRoutesByConnection(ctx, tenantIDs, connectionID)
	if err != nil {
		return nil, err
	}
	routeViews := make([]EnterpriseRouteAvailability, 0, len(routes))
	for _, route := range routes {
		availability, reason := "UNKNOWN", "route is not pinned to an enterprise connection version"
		if !route.Enabled {
			availability, reason = "UNAVAILABLE", "route is disabled"
		} else if route.CurrentPinID != "" && route.CurrentPinVersion > 0 {
			pin, pinErr := s.Store.GetModelRouteEnterprisePin(ctx, route.TenantID, route.ID, route.CurrentPinID, route.CurrentPinVersion)
			if pinErr != nil {
				return nil, pinErr
			}
			if pin == nil || pin.ConnectionID != connectionID {
				availability, reason = "UNAVAILABLE", "route pin does not match the connection"
			} else if pin.ConnectionVersion != connectionView.Version.Version {
				availability, reason = "UNAVAILABLE", "route pin references a superseded connection version"
			} else if binding, ok := currentVersionByID[pin.BindingID]; !ok || binding.TenantID != route.TenantID {
				availability, reason = "UNAVAILABLE", "pinned binding version is unavailable"
			} else if binding.Version != pin.BindingVersion {
				availability, reason = "UNAVAILABLE", "route pin references a superseded binding version"
			} else {
				allowed := false
				for _, modelRef := range decodeStringList(binding.AllowedModelRefs) {
					if modelRef == pin.ModelRef {
						allowed = true
						break
					}
				}
				if !allowed || route.TargetModel != pin.ModelRef {
					availability, reason = "UNAVAILABLE", "route model is not authorized by the pinned binding version"
				} else {
					availability = bindingAvailabilityByID[pin.BindingID]
					reason = "route is enabled and exactly pinned to an authorized binding version"
				}
			}
		}
		routeViews = append(routeViews, EnterpriseRouteAvailability{
			Route: route, Availability: availability, Reason: reason,
		})
	}
	return &EnterpriseAvailabilityView{
		Connection: connectionView.Connection,
		Version:    connectionView.Version,
		TenantName: connectionView.TenantName,
		Bindings:   bindingViews,
		Routes:     routeViews,
	}, nil
}
